//go:build !cgo

package perf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
	perfutil "github.com/dapicom-ai/omnipus/pkg/testutil"
)

// loadTestGuard returns true when OMNIPUS_RUN_LOAD_TEST=1.
// The load test is skipped in normal CI to keep go test ./... fast.
func loadTestGuard(t *testing.T) {
	t.Helper()
	if os.Getenv("OMNIPUS_RUN_LOAD_TEST") != "1" {
		t.Skip("load test requires OMNIPUS_RUN_LOAD_TEST=1")
	}
}

// loadSLOs are the hard SLO limits enforced by TestLoad2000Sessions.
const (
	sloP95FirstToken      = 1 * time.Second
	sloGoroutineLeakDelta = 10 // tolerated background goroutines
	sloDroppedFrames      = 0
)

const sloPeakRSSBytes uint64 = 500 * 1024 * 1024 // 500 MB

// loadConfig controls the run parameters. Kept as constants so the test is
// readable and so CI can see the implied runtime budget at a glance.
const (
	totalSessions  = 2000
	rampRate       = 50               // clients per second
	holdDuration   = 5 * time.Minute  // total window per session
	messagePeriod  = 30 * time.Second // messages per session during hold
	teardownGrace  = 10 * time.Second // time given for server goroutines to drain
	rssSampleEvery = 5 * time.Second  // RSS sampling cadence
)

// loadResultJSON is the schema written to tests/perf/results/.
type loadResultJSON struct {
	RunAt            string            `json:"run_at"`
	SessionsOpened   int               `json:"sessions_opened"`
	MessagesSent     int               `json:"messages_sent"`
	MessagesRecv     int               `json:"messages_recv"`
	DroppedFrames    int               `json:"dropped_frames"`
	P50FirstTokenMS  int64             `json:"p50_first_token_ms"`
	P95FirstTokenMS  int64             `json:"p95_first_token_ms"`
	P99FirstTokenMS  int64             `json:"p99_first_token_ms"`
	PeakRSSMB        float64           `json:"peak_rss_mb"`
	GoroutinesBefore int               `json:"goroutines_before"`
	GoroutinesAfter  int               `json:"goroutines_after"`
	GoroutineLeak    int               `json:"goroutine_leak"`
	DurationSeconds  float64           `json:"duration_seconds"`
	SLOBreaches      map[string]string `json:"slo_breaches,omitempty"`
}

// TestLoad2000Sessions exercises 2000 concurrent WebSocket sessions against a
// real in-process gateway with a scripted ScenarioProvider that returns a
// fixed 50-token reply.
//
// Plan 3 §1 Axis-6 SLOs:
//   - p95 first-token < 1 s
//   - Peak RSS < 500 MB
//   - Zero dropped frames
//   - Goroutine leak < 10 after teardown
//
// The test is guarded by OMNIPUS_RUN_LOAD_TEST=1; skip it in normal CI.
// Target runtime: ~6 minutes (ramp + hold + teardown).
func TestLoad2000Sessions(t *testing.T) {
	// Traces to: temporal-puzzling-melody.md §4 Axis-6 and §6 PR-C
	loadTestGuard(t)

	// Do NOT call t.Parallel() — load tests must run alone for accurate RSS.

	// Build a ScenarioProvider that returns a fixed 50-token reply for every
	// call. Use a repeating scenario by pre-loading a large number of steps so
	// we never hit ErrNoMoreResponses during the 5-minute hold.
	//
	// Each session sends: 1 initial + (5*60/30 = 10) hold messages = 11 max.
	// Total calls = 2000 * 11 = 22 000; pre-load 30 000 for headroom.
	const stepsToPreload = 30_000
	const fixedReply = "This is a scripted 50-token reply used for load testing. " +
		"It is intentionally short and deterministic so RSS and latency measurements " +
		"reflect gateway overhead, not payload size."

	scenario := testutil.NewScenario()
	for i := 0; i < stepsToPreload; i++ {
		scenario.WithText(fixedReply)
	}

	// Boot the gateway. StartTestGateway installs the scenario provider and
	// registers t.Cleanup to close the gateway when the test ends.
	gw := testutil.StartTestGateway(t,
		testutil.WithScenario(scenario),
		testutil.WithAllowEmpty(),
	)

	// Derive the WebSocket URL from the gateway HTTP URL.
	gwURL, err := url.Parse(gw.URL)
	if err != nil {
		t.Fatalf("load test: parse gateway URL %q: %v", gw.URL, err)
	}
	gwURL.Scheme = "ws"
	wsBase := gwURL.String()

	// ---- Metrics collection ----
	var (
		latencyMu sync.Mutex
		latencies []time.Duration
	)
	var (
		sessionsDone  int64 // atomic count of fully completed sessions
		msgSent       int64 // atomic
		msgRecv       int64 // atomic
		droppedFrames int64 // atomic
		peakRSSBytes  uint64
		peakRSSMu     sync.Mutex
	)

	// ---- RSS background sampler ----
	rssCtx, cancelRSSPoller := context.WithCancel(context.Background())
	defer cancelRSSPoller()
	go func() {
		ticker := time.NewTicker(rssSampleEvery)
		defer ticker.Stop()
		for {
			select {
			case <-rssCtx.Done():
				return
			case <-ticker.C:
				cur := perfutil.SampleRSS()
				peakRSSMu.Lock()
				if cur > peakRSSBytes {
					peakRSSBytes = cur
				}
				peakRSSMu.Unlock()
			}
		}
	}()

	// ---- Pre-run goroutine baseline ----
	goroutinesBefore := perfutil.CountGoroutines()

	runStart := time.Now()

	// ---- Ramp up ----
	// Ramp 2000 sessions at rampRate per second (40-second ramp).
	var wg sync.WaitGroup
	rampTicker := time.NewTicker(time.Second / rampRate)
	defer rampTicker.Stop()

	for i := 0; i < totalSessions; i++ {
		<-rampTicker.C

		wg.Add(1)
		sessionIdx := i
		go func() {
			defer wg.Done()
			runSession(t, sessionIdx, wsBase, holdDuration, messagePeriod,
				&msgSent, &msgRecv, &droppedFrames, &sessionsDone,
				&latencyMu, &latencies)
		}()
	}

	// Wait for all session goroutines to finish.
	wg.Wait()
	cancelRSSPoller()

	// ---- Teardown grace period for server goroutines ----
	time.Sleep(teardownGrace)

	// ---- Post-run measurements ----
	goroutinesAfter := perfutil.CountGoroutines()
	totalDuration := time.Since(runStart)
	sessionsOpened := int(atomic.LoadInt64(&sessionsDone))
	totalMsgSent := int(atomic.LoadInt64(&msgSent))
	totalMsgRecv := int(atomic.LoadInt64(&msgRecv))
	totalDropped := int(atomic.LoadInt64(&droppedFrames))
	peakRSSMu.Lock()
	finalPeakRSS := peakRSSBytes
	peakRSSMu.Unlock()

	// Copy latencies under the mutex before computing percentiles.
	latencyMu.Lock()
	allLatencies := make([]time.Duration, len(latencies))
	copy(allLatencies, latencies)
	latencyMu.Unlock()

	// ---- Compute percentiles ----
	// perfutil.Percentile sorts in-place; pass a copy for each call.
	p50Lat := func() time.Duration {
		cp := make([]time.Duration, len(allLatencies))
		copy(cp, allLatencies)
		return perfutil.Percentile(cp, 0.50)
	}()
	p95Lat := func() time.Duration {
		cp := make([]time.Duration, len(allLatencies))
		copy(cp, allLatencies)
		return perfutil.Percentile(cp, 0.95)
	}()
	p99Lat := func() time.Duration {
		cp := make([]time.Duration, len(allLatencies))
		copy(cp, allLatencies)
		return perfutil.Percentile(cp, 0.99)
	}()

	// ---- Build result for JSON output ----
	sloBreaches := map[string]string{}
	result := loadResultJSON{
		RunAt:            time.Now().UTC().Format(time.RFC3339),
		SessionsOpened:   sessionsOpened,
		MessagesSent:     totalMsgSent,
		MessagesRecv:     totalMsgRecv,
		DroppedFrames:    totalDropped,
		P50FirstTokenMS:  p50Lat.Milliseconds(),
		P95FirstTokenMS:  p95Lat.Milliseconds(),
		P99FirstTokenMS:  p99Lat.Milliseconds(),
		PeakRSSMB:        float64(finalPeakRSS) / (1024 * 1024),
		GoroutinesBefore: goroutinesBefore,
		GoroutinesAfter:  goroutinesAfter,
		GoroutineLeak:    goroutinesAfter - goroutinesBefore,
		DurationSeconds:  totalDuration.Seconds(),
	}

	// ---- SLO assertions ----
	// Guard: if no latencies were recorded at all, the percentile helpers
	// return 0 and the SLO check below would trivially pass. Treat an empty
	// sample as a hard failure — a "successful" load test with zero measured
	// first-token events means the harness or the server collapsed before
	// any data point could be recorded.
	if len(allLatencies) == 0 {
		sloBreaches["no_latency_samples"] = fmt.Sprintf(
			"no first-token latencies recorded across %d sessions — gateway or harness collapse",
			sessionsOpened,
		)
	}
	if p95Lat > sloP95FirstToken {
		msg := fmt.Sprintf("p95=%v > SLO=%v — distribution: p50=%v p95=%v p99=%v",
			p95Lat, sloP95FirstToken, p50Lat, p95Lat, p99Lat)
		sloBreaches["p95_first_token"] = msg
	}
	if finalPeakRSS > sloPeakRSSBytes {
		msg := fmt.Sprintf("peakRSS=%.1f MB > SLO=%.1f MB",
			float64(finalPeakRSS)/(1024*1024),
			float64(sloPeakRSSBytes)/(1024*1024))
		sloBreaches["peak_rss"] = msg
	}
	if totalDropped > sloDroppedFrames {
		sloBreaches["dropped_frames"] = fmt.Sprintf("dropped=%d > SLO=%d", totalDropped, sloDroppedFrames)
	}
	leak := goroutinesAfter - goroutinesBefore
	if leak >= sloGoroutineLeakDelta {
		sloBreaches["goroutine_leak"] = fmt.Sprintf(
			"leak=%d goroutines (before=%d after=%d) >= threshold=%d",
			leak, goroutinesBefore, goroutinesAfter, sloGoroutineLeakDelta)
	}

	result.SLOBreaches = sloBreaches

	// Write result JSON regardless of pass/fail for trend analysis.
	writeLoadResult(t, result)

	// Log a summary before asserting so the output is visible even on failure.
	t.Logf("Load test summary: sessions=%d sent=%d recv=%d dropped=%d "+
		"p50=%v p95=%v p99=%v peakRSS=%.1fMB goroutineLeak=%d duration=%v",
		sessionsOpened, totalMsgSent, totalMsgRecv, totalDropped,
		p50Lat, p95Lat, p99Lat,
		float64(finalPeakRSS)/(1024*1024), leak, totalDuration)

	// ---- Fail on SLO breaches ----
	for slo, msg := range sloBreaches {
		t.Errorf("SLO BREACH [%s]: %s", slo, msg)
	}
}

// runSession opens one WebSocket, sends the initial message, records the
// first-token latency, then keeps the connection alive for the hold duration,
// sending one message every msgPeriod.
func runSession(
	t *testing.T,
	idx int,
	wsBase string,
	hold time.Duration,
	msgPeriod time.Duration,
	msgSent, msgRecv, dropped, done *int64,
	latMu *sync.Mutex,
	latencies *[]time.Duration,
) {
	t.Helper()

	wsURL := wsBase + "/api/v1/chat/ws"
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	header := http.Header{}
	header.Set("Origin", wsBase)

	conn, resp, err := dialer.Dial(wsURL, header)
	if err != nil {
		// Count as dropped — the session never opened.
		atomic.AddInt64(dropped, 1)
		// Surface the first few dial failures so a future "all 2000
		// dropped" failure has actionable detail (HTTP status) instead
		// of an opaque counter. Caught the /api/v1/ws → /api/v1/chat/ws
		// route rename via this exact instrumentation; keep it in place
		// so the next breakage of a similar shape is one log read away.
		if idx < 5 {
			if resp != nil {
				t.Logf("session %d dial failed: err=%v status=%d", idx, err, resp.StatusCode)
			} else {
				t.Logf("session %d dial failed: err=%v (no resp)", idx, err)
			}
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		return
	}
	defer conn.Close()
	atomic.AddInt64(done, 1)

	// Send the mandatory auth frame first — the gateway requires every WS
	// client to authenticate before any other frame is honoured. In dev-mode
	// (StartTestGateway with WithAllowEmpty → DevModeBypass=true) any
	// non-empty token is accepted; the frame's structural validity matters,
	// not the token value. Without this frame the server silently drops
	// every subsequent payload, which previously caused all 2000 sessions
	// to report `recv=0 dropped=2000`.
	authFrame := `{"type":"auth","token":"dev-token"}`
	if wErr := conn.WriteMessage(websocket.TextMessage, []byte(authFrame)); wErr != nil {
		atomic.AddInt64(dropped, 1)
		return
	}

	// Send one initial user message, time until first assistant frame.
	// The wire-format frame `type` is "message" (see contracts/components/
	// schemas/MessageFrame.yaml — `type: const: message`). Earlier
	// versions of this test used "user_message", which is not in the
	// WsFrameType enum and was silently ignored by the server.
	userMsg := fmt.Sprintf(`{"type":"message","content":"load test message %d"}`, idx)
	sendStart := time.Now()
	if wErr := conn.WriteMessage(websocket.TextMessage, []byte(userMsg)); wErr != nil {
		atomic.AddInt64(dropped, 1)
		return
	}
	atomic.AddInt64(msgSent, 1)

	// Read until we see the first assistant frame (type: "token", "content", or "done").
	firstTokenReceived := false
	for !firstTokenReceived {
		_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		_, msg, rErr := conn.ReadMessage()
		if rErr != nil {
			atomic.AddInt64(dropped, 1)
			return
		}

		var frame struct {
			Type string `json:"type"`
		}
		if jsonErr := json.Unmarshal(msg, &frame); jsonErr == nil {
			switch frame.Type {
			case "session_started", "session_state":
				// Pre-token bookkeeping frames — ignore and keep reading.
				continue
			case "token", "content", "text", "assistant_message":
				if !firstTokenReceived {
					lat := time.Since(sendStart)
					latMu.Lock()
					*latencies = append(*latencies, lat)
					latMu.Unlock()
					firstTokenReceived = true
					atomic.AddInt64(msgRecv, 1)
				}
			case "done":
				if !firstTokenReceived {
					lat := time.Since(sendStart)
					latMu.Lock()
					*latencies = append(*latencies, lat)
					latMu.Unlock()
					atomic.AddInt64(msgRecv, 1)
				}
				// Message cycle complete — proceed to hold phase.
				goto holdPhase
			case "error":
				atomic.AddInt64(dropped, 1)
				return
			}
		}
	}

holdPhase:
	// Keep the connection alive for the remainder of holdDuration, sending
	// one message every msgPeriod.
	holdEnd := time.Now().Add(hold)
	holdTicker := time.NewTicker(msgPeriod)
	defer holdTicker.Stop()

	// Drain incoming frames in a separate goroutine so we do not block on send.
	// F14: distinguish expected close codes (1000/1001) from anomalous errors.
	// Expected close codes (CloseNormalClosure=1000, CloseGoingAway=1001) occur
	// when the server or client initiates a clean shutdown — these are not dropped
	// frames. Any other close code or non-close read error indicates an anomalous
	// termination and is counted as a dropped frame to surface regressions in P99.
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(msgPeriod + 15*time.Second))
			_, _, rErr := conn.ReadMessage()
			if rErr != nil {
				// Check if this is an expected close code (normal shutdown).
				// websocket.IsCloseError returns true for the listed close codes.
				if websocket.IsCloseError(rErr,
					websocket.CloseNormalClosure, // 1000 — we sent this ourselves
					websocket.CloseGoingAway,     // 1001 — server is shutting down
				) {
					// Expected close — not a dropped frame.
					return
				}
				// Anomalous: unexpected close code or raw read error.
				// Count as dropped so P99 latency does not hide regressions.
				atomic.AddInt64(dropped, 1)
				return
			}
			atomic.AddInt64(msgRecv, 1)
		}
	}()

	for time.Now().Before(holdEnd) {
		<-holdTicker.C
		holdMsg := fmt.Sprintf(`{"type":"message","content":"keep-alive %d"}`, idx)
		if wErr := conn.WriteMessage(websocket.TextMessage, []byte(holdMsg)); wErr != nil {
			atomic.AddInt64(dropped, 1)
			break
		}
		atomic.AddInt64(msgSent, 1)
	}

	// Cleanly close the WebSocket — server should honor the close frame.
	_ = conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	<-drainDone
}

// writeLoadResult writes the JSON result to tests/perf/results/.
// Creates the directory if it does not exist. Non-fatal on write error so
// the test result itself is authoritative.
func writeLoadResult(t *testing.T, result loadResultJSON) {
	t.Helper()

	resultsDir := filepath.Join("results")
	if mkErr := os.MkdirAll(resultsDir, 0o755); mkErr != nil {
		t.Logf("load test: failed to create results dir %q: %v", resultsDir, mkErr)
		return
	}

	// Replace colons so the filename is valid on all OSes.
	ts := time.Now().UTC().Format(time.RFC3339)
	safeTS := ""
	for _, ch := range ts {
		if ch == ':' {
			safeTS += "-"
		} else {
			safeTS += string(ch)
		}
	}
	filename := fmt.Sprintf("load-2000-%s.json", safeTS)

	data, marshalErr := json.MarshalIndent(result, "", "  ")
	if marshalErr != nil {
		t.Logf("load test: marshal result JSON: %v", marshalErr)
		return
	}

	path := filepath.Join(resultsDir, filename)
	if writeErr := os.WriteFile(path, data, 0o644); writeErr != nil {
		t.Logf("load test: write result file %q: %v", path, writeErr)
		return
	}
	t.Logf("load test: result written to %s", path)
}
