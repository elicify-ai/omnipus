package browser

// webrtc_ingest_repro_test.go — a REPEATABLE reproduction harness for the
// intermittent live-video startup failure first seen in CI run 33943602552
// (branch release/v0.1.1 @ b84ed5400), where the ingest leg's ICE reached
// `failed` and the live panel stayed black on a "Waiting for the first
// frame…" spinner forever.
//
// What makes this harness different from every other browser test in this
// package: it is the FIRST one that injects window.__omnipusCapture and lets
// the real encoder.js run its real WebRTC flow. TestSpike_CaptureAgainstSecondChrome
// (browser_e2e_test.go, gate G-2) loads the same extension and drives
// chrome.tabCapture from an injected probe, but deliberately leaves encoder.js
// inert ("it is not, so the page is inert") — so the ingest ICE handshake this
// incident is about had never been exercised by any test at all. That is why
// the failure had never been reproduced outside CI.
//
// Everything below the encoder page is REAL: real Chrome, real capture
// extension, real encoder.js, real chrome.tabCapture + getUserMedia, real
// pion webrtc.Session, real CaptureSession with the real defaultEncoderStarter.
// The only substitution is the ingest WebSocket SERVER: this file replays the
// gateway's capture-ingest protocol (pkg/gateway/browser_webrtc.go's
// captureIngestWSHandler) in ~60 lines rather than standing up a whole
// AgentLoop, because the gateway handler's only contribution to the ICE path
// is to hand the offer to cs.HandleIngestOffer and write back the answer.
//
// It is a MEASUREMENT harness, not an assertion: an intermittent failure is
// a RATE, and a single green run proves nothing about it. The test therefore
// runs N capture cycles, reports a failure count and per-iteration evidence,
// and only fails outright if the measured failure rate exceeds a threshold
// the operator sets. Reading the reported numbers is the point.
//
// RESOURCE RULE — this launches a real Chrome. Run it ALONE, never as part of
// a package-wide or ./... run:
//
//	OMNIPUS_WEBRTC_REPRO=1 CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//	  -timeout 30m -v -run '^TestWebRTCIngestStartupRepro$' ./pkg/tools/browser/
//
// Knobs (all optional):
//
//	OMNIPUS_WEBRTC_REPRO=1          required opt-in; absent => the test skips
//	OMNIPUS_WEBRTC_REPRO_ITERS=20   capture cycles to run (default 10)
//	OMNIPUS_WEBRTC_REPRO_STUN=...   STUN URL handed to BOTH legs. Default is
//	                                the production default
//	                                (stun:stun.l.google.com:19302). "none"
//	                                means host-candidates-only; an
//	                                unroutable URL simulates a blocked or
//	                                slow STUN server (hypothesis (a)).
//	OMNIPUS_WEBRTC_REPRO_LOAD=8     CPU burner goroutines run for the whole
//	                                measurement, to reproduce the loaded-CI
//	                                condition (hypothesis (c)).
//	OMNIPUS_WEBRTC_REPRO_MAXFAIL=0  fail the test above this many failures.
//	                                Default 0 is a real gate: on a healthy
//	                                machine the correct rate IS zero.
//	OMNIPUS_WEBRTC_PION_LOG=debug   raise pion's own ICE logging (see
//	                                webrtc/icediag.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/captureext"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// reproDefaultSTUN mirrors pkg/config/defaults.go's Tools.Browser.WebRTCStunServer
// default. Duplicated as a literal rather than imported so the harness measures
// the value the incident actually ran with, even if the default later changes.
const reproDefaultSTUN = "stun:stun.l.google.com:19302"

// reproFirstFrameBudget bounds how long one capture cycle may take to deliver
// its first forwarded video RTP packet before the cycle is scored a failure.
//
// 45s is deliberately far more generous than any production budget (the SPA
// gives up at 30s, webrtc/ingest.go's waitForTracksTimeout at 15s): the
// question this harness answers is "did the media path ever come up", not
// "did it come up fast enough". Scoring a slow-but-successful startup as a
// failure would conflate hypothesis (c) with the ICE failure being hunted.
const reproFirstFrameBudget = 45 * time.Second

// ---------------------------------------------------------------------------
// Ingest WS protocol replay (see pkg/gateway/browser_webrtc.go).
// ---------------------------------------------------------------------------

type reproHelloFrame struct {
	Type       string `json:"type"`
	Token      string `json:"token"`
	ExtVersion string `json:"ext_version"`
}

type reproOfferFrame struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type reproAnswerFrame struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type reproControlFrame struct {
	Type           string  `json:"type"`
	Action         string  `json:"action"`
	Reason         *string `json:"reason,omitempty"`
	ExpectedWidth  int     `json:"expected_width,omitempty"`
	ExpectedHeight int     `json:"expected_height,omitempty"`
	MaxBitrate     int     `json:"max_bitrate,omitempty"`
}

type reproErrorFrame struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// reproIngestServer replays the gateway's capture-ingest WebSocket endpoint
// against whichever CaptureSession is currently under measurement.
//
// The token check is a plain equality rather than the gateway's constant-time
// registry sweep: this server serves exactly one session at a time and is
// bound to loopback by httptest, so there is no attacker to be timed. Every
// OTHER step is the gateway's, in the gateway's order, because each one is a
// place a real encoder can be rejected: hello-first, RecordExtVersion,
// BindIngest/UnbindIngest with the epoch guard, offer -> HandleIngestOffer ->
// answer, and close-on-error so encoder.js engages its reconnect path exactly
// as it would in production.
type reproIngestServer struct {
	mu      sync.Mutex
	current *CaptureSession
	logf    func(string, ...any)
}

func (s *reproIngestServer) setSession(cs *CaptureSession) {
	s.mu.Lock()
	s.current = cs
	s.mu.Unlock()
}

func (s *reproIngestServer) lookup(token string) *CaptureSession {
	s.mu.Lock()
	cs := s.current
	s.mu.Unlock()
	if cs == nil || !cs.ValidateToken(token) {
		return nil
	}
	return cs
}

func (s *reproIngestServer) handle(w http.ResponseWriter, r *http.Request) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		s.logf("ingest-ws: upgrade failed: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()
	conn.SetReadLimit(256 * 1024)

	var writeMu sync.Mutex
	sendJSON := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		return conn.WriteJSON(v)
	}

	if err := conn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		s.logf("ingest-ws: set read deadline: %v", err)
		return
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		s.logf("ingest-ws: read hello: %v", err)
		return
	}
	var hello reproHelloFrame
	if err := json.Unmarshal(raw, &hello); err != nil || hello.Type != "browser_capture_hello" {
		s.logf("ingest-ws: first frame was not a hello (%v)", err)
		return
	}
	cs := s.lookup(hello.Token)
	if cs == nil {
		s.logf("ingest-ws: token mismatch — closing")
		return
	}
	cs.RecordExtVersion(hello.ExtVersion)
	s.logf("ingest-ws: hello accepted (ext_version=%s)", hello.ExtVersion)

	send := func(action string, reason *string, expectedW, expectedH, maxBitrate int) error {
		return sendJSON(reproControlFrame{
			Type: "browser_capture_control", Action: action, Reason: reason,
			ExpectedWidth: expectedW, ExpectedHeight: expectedH, MaxBitrate: maxBitrate,
		})
	}
	prevClose, epoch := cs.BindIngest(send, func() { _ = conn.Close() })
	if prevClose != nil {
		prevClose()
	}
	defer cs.UnbindIngest(epoch)

	for {
		if err := conn.SetReadDeadline(time.Now().Add(45 * time.Second)); err != nil {
			return
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			s.logf("ingest-ws: read: %v", err)
			return
		}
		var probe struct {
			Type   string `json:"type"`
			Action string `json:"action"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			continue
		}
		switch probe.Type {
		case "browser_capture_offer":
			var offer reproOfferFrame
			if err := json.Unmarshal(raw, &offer); err != nil {
				continue
			}
			answer, err := cs.HandleIngestOffer(offer.SDP)
			if err != nil {
				s.logf("ingest-ws: HandleIngestOffer failed: %v", err)
				_ = sendJSON(reproErrorFrame{Type: "error", Message: err.Error()})
				return
			}
			if err := sendJSON(reproAnswerFrame{Type: "browser_capture_answer", SDP: answer}); err != nil {
				s.logf("ingest-ws: send answer: %v", err)
				return
			}
		case "browser_capture_control":
			if probe.Action == "ping" {
				cs.RecordPing()
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The harness itself.
// ---------------------------------------------------------------------------

// reproOutcome is one capture cycle's result.
type reproOutcome struct {
	iteration      int
	startErr       error
	firstFrameAt   time.Duration // 0 when no frame ever arrived
	videoPackets   int64
	iceFailed      bool
	remoteOffer    []string // full a=candidate lines Chrome sent
	localCands     []string // full a=candidate lines pion sent
	gatherMS       string
	remoteSummary  string // "host=4 srflx=2 mdns=0" — the decisive per-run fact
	transitions    string
	pageICEState   string
	pageConnState  string
	pageLastError  string
	pionMDNSWarned bool
	pageHistory    []string
}

// stage classifies WHERE a cycle died. Without it every failure reads as one
// undifferentiated "FAIL", and the two this harness actually produces are
// nothing alike: an encoder page that never loaded (a CDP/Chrome-startup
// problem, upstream of WebRTC entirely) and an ICE handshake that failed (the
// incident under investigation). Conflating them would let a run full of the
// former be reported as a reproduction of the latter.
func (o reproOutcome) stage() string {
	switch {
	case o.ok():
		return "OK"
	case o.startErr != nil:
		return "FAIL-encoder-start"
	case o.iceFailed:
		return "FAIL-ice"
	case o.pageICEState == "" || o.pageICEState == "new":
		return "FAIL-no-ice-attempt"
	case o.pageICEState == "checking":
		return "FAIL-ice-stuck-checking"
	default:
		return "FAIL-no-media"
	}
}

func (o reproOutcome) ok() bool { return o.startErr == nil && o.firstFrameAt > 0 }

func reproEnvInt(name string, def int) int {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// reproBurnCPU starts n goroutines that spin until stop closes. This is a
// blunt instrument on purpose: the failing CI runner was measured ~6.8x slower
// on an unrelated latency probe, i.e. starved of CPU rather than of any
// specific resource, and a scheduler that cannot run pion's connectivity-check
// loop or Chrome's mDNS responder on time is exactly the condition under test.
func reproBurnCPU(n int, stop <-chan struct{}) {
	for i := 0; i < n; i++ {
		go func() {
			x := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				for j := 0; j < 1_000_000; j++ {
					x += j
				}
				_ = x
			}
		}()
	}
}

func TestWebRTCIngestStartupRepro(t *testing.T) {
	if os.Getenv("OMNIPUS_WEBRTC_REPRO") != "1" {
		t.Skip("set OMNIPUS_WEBRTC_REPRO=1 to run the live-video startup reproduction harness (launches a real Chrome)")
	}

	iterations := reproEnvInt("OMNIPUS_WEBRTC_REPRO_ITERS", 10)
	maxFail := reproEnvInt("OMNIPUS_WEBRTC_REPRO_MAXFAIL", 0)
	loadWorkers := reproEnvInt("OMNIPUS_WEBRTC_REPRO_LOAD", 0)

	stun := reproDefaultSTUN
	if v, ok := os.LookupEnv("OMNIPUS_WEBRTC_REPRO_STUN"); ok {
		if strings.EqualFold(strings.TrimSpace(v), "none") {
			stun = ""
		} else {
			stun = strings.TrimSpace(v)
		}
	}

	execPath := requireBrowserOrFail(t)
	extDir, err := captureext.Seed(t.TempDir())
	require.NoError(t, err, "seed the capture extension")

	t.Logf("repro config: iterations=%d stun=%q load_workers=%d chrome=%s",
		iterations, stun, loadWorkers, execPath)

	// One Chrome for the whole measurement. Relaunching per iteration would
	// make the launch, not the ICE handshake, dominate both the runtime and
	// the variance — and the incident happened on a WARM browser (the capture
	// was a recapture into an already-running Chrome).
	coord, _ := spikeLaunchChrome(t, "repro", execPath, extDir)

	mgr, err := NewBrowserManager(BrowserConfig{
		Enabled:         true,
		Headless:        true,
		PageTimeout:     30 * time.Second,
		ExecPath:        execPath,
		ExtensionDir:    extDir,
		ExtensionID:     captureext.ExtensionID,
		TrustPathChrome: true,
	}, security.NewSSRFChecker(nil))
	require.NoError(t, err, "construct the browser manager")
	mgr.AttachSharedChrome(coord, browserTestKey("repro"))
	t.Cleanup(mgr.Shutdown)

	// Per-iteration log capture. Every line the relay and this harness emit is
	// tagged with the iteration that produced it, so a failure's evidence can
	// be printed on its own without the other N-1 cycles' noise.
	var logMu sync.Mutex
	iterLogs := map[int][]string{}
	currentIter := 0
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		logMu.Lock()
		iterLogs[currentIter] = append(iterLogs[currentIter], line)
		logMu.Unlock()
	}
	linesFor := func(iter int) []string {
		logMu.Lock()
		defer logMu.Unlock()
		out := make([]string, len(iterLogs[iter]))
		copy(out, iterLogs[iter])
		return out
	}

	ingest := &reproIngestServer{logf: logf}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/browser/capture-ingest", ingest.handle)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ingestURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/browser/capture-ingest"

	if loadWorkers > 0 {
		stop := make(chan struct{})
		reproBurnCPU(loadWorkers, stop)
		t.Cleanup(func() { close(stop) })
		// Let the load settle so the first iteration is not measured against a
		// still-ramping scheduler.
		time.Sleep(2 * time.Second)
	}

	outcomes := make([]reproOutcome, 0, iterations)
	for i := 1; i <= iterations; i++ {
		logMu.Lock()
		currentIter = i
		logMu.Unlock()
		outcomes = append(outcomes, runOneCaptureCycle(t, mgr, ingest, ingestURL, stun, i, logf, linesFor))
	}

	reportReproOutcomes(t, outcomes, linesFor)

	failures := 0
	for _, o := range outcomes {
		if !o.ok() {
			failures++
		}
	}
	if failures > maxFail {
		t.Fatalf("live-video startup failed %d/%d times (OMNIPUS_WEBRTC_REPRO_MAXFAIL=%d)", failures, len(outcomes), maxFail)
	}
}

// runOneCaptureCycle performs a single full capture start and scores it.
func runOneCaptureCycle(
	t *testing.T,
	mgr *BrowserManager,
	ingest *reproIngestServer,
	ingestURL, stun string,
	iter int,
	logf func(string, ...any),
	linesFor func(int) []string,
) reproOutcome {
	t.Helper()
	out := reproOutcome{iteration: iter}

	cs, err := NewCaptureSession(mgr, "repro-agent", mgr.OperatorSessionID(),
		webrtc.Config{StunServer: stun}, func(string, []byte) {}, logf)
	if err != nil {
		out.startErr = fmt.Errorf("construct capture session: %w", err)
		return out
	}
	ingest.setSession(cs)
	defer func() {
		cs.Stop()
		ingest.setSession(nil)
		mgr.ClearCaptureSession(cs)
	}()

	started := time.Now()
	if _, err := cs.Start(context.Background(), ingestURL); err != nil {
		out.startErr = fmt.Errorf("start capture: %w", err)
		return out
	}

	deadline := time.Now().Add(reproFirstFrameBudget)
	for time.Now().Before(deadline) {
		st := cs.Stats()
		if st.VideoPackets > 0 {
			out.firstFrameAt = time.Since(started)
			out.videoPackets = st.VideoPackets
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if out.firstFrameAt == 0 {
		out.videoPackets = cs.Stats().VideoPackets
	}

	// Read the ENCODER PAGE's own view before tearing it down. Chrome's side
	// of the handshake is otherwise invisible: the gateway log only ever shows
	// what pion saw, and the two disagreeing is itself a finding.
	readEncoderPageState(cs, &out)

	// Mine this iteration's relay log for the structured evidence the
	// ice-diag instrumentation emitted (webrtc/icediag.go).
	for _, line := range linesFor(iter) {
		switch {
		case strings.Contains(line, "ice-diag ingest remote offer summary: "):
			out.remoteSummary = line[strings.Index(line, "summary: ")+len("summary: "):]
		case strings.Contains(line, "ice-diag ingest remote offer: candidate:"):
			out.remoteOffer = append(out.remoteOffer, line[strings.Index(line, "candidate:"):])
		case strings.Contains(line, "ice-diag ingest local candidate "):
			// Keep the WHOLE line, timing prefix included. The offset at
			// which each candidate appeared is the measurement that
			// separates "host-only, instant" from "waited 5s for a STUN
			// server that never answered" -- stripping it to a bare
			// candidate string would throw away the evidence for
			// hypothesis (a).
			out.localCands = append(out.localCands, strings.TrimSpace(line))
		case strings.Contains(line, "ICE connection state -> failed"):
			out.iceFailed = true
		case strings.Contains(line, "Failed to discover mDNS candidate"):
			out.pionMDNSWarned = true
		case strings.Contains(line, "ice-diag ingest outcome=") && strings.Contains(line, "gather_ms="):
			out.gatherMS = strings.TrimSpace(line)
		case strings.Contains(line, "server gathering complete in"):
			out.transitions = strings.TrimSpace(line)
		}
	}
	return out
}

// readEncoderPageState pulls window.__omnipusState out of the live encoder
// page (encoder.js's debug surface: ws/ice/conn state, last error, and a
// 300-entry event history) before Stop() closes it.
//
// Best-effort by design: a cycle whose encoder page never loaded has no state
// to read, and that absence is itself recorded rather than treated as a test
// error.
func readEncoderPageState(cs *CaptureSession, out *reproOutcome) {
	cs.mu.Lock()
	tabCtx := cs.tabCtx
	cs.mu.Unlock()
	if tabCtx == nil {
		out.pageLastError = "(encoder page never started)"
		return
	}
	ctx, cancel := context.WithTimeout(tabCtx, 5*time.Second)
	defer cancel()
	var state struct {
		WSState   string `json:"wsState"`
		ICEState  string `json:"iceState"`
		ConnState string `json:"connState"`
		Status    string `json:"status"`
		LastError string `json:"lastError"`
		History   []struct {
			T   int64  `json:"t"`
			Evt string `json:"evt"`
		} `json:"history"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__omnipusState`, &state)); err != nil {
		out.pageLastError = "(could not read window.__omnipusState: " + err.Error() + ")"
		return
	}
	for _, h := range state.History {
		out.pageHistory = append(out.pageHistory, fmt.Sprintf("%d %s", h.T, h.Evt))
	}
	out.pageICEState = state.ICEState
	out.pageConnState = state.ConnState
	if state.LastError != "" {
		out.pageLastError = state.LastError
	} else {
		out.pageLastError = "status=" + state.Status + " ws=" + state.WSState
	}
}

// reportReproOutcomes prints the measurement: a one-line-per-iteration table
// first (the rate is the result), then the full evidence for every FAILED
// iteration plus one SUCCESSFUL iteration.
//
// One success is printed deliberately, not for symmetry: the decisive question
// about the mDNS hypothesis is whether the candidate sets of a healthy run and
// a failed run DIFFER, and that comparison is impossible if only failures are
// recorded.
func reportReproOutcomes(t *testing.T, outcomes []reproOutcome, linesFor func(int) []string) {
	t.Helper()
	t.Logf("=== live-video startup: %d cycles ===", len(outcomes))
	failures := 0
	for _, o := range outcomes {
		verdict := o.stage()
		if !o.ok() {
			failures++
		}
		t.Logf("  #%02d %-22s first_frame=%-8s pkts=%-5d ice_failed=%-5v mdns_warn=%-5v page_ice=%-11s chrome_offered=[%s] page=%s",
			o.iteration, verdict, reproDuration(o.firstFrameAt), o.videoPackets,
			o.iceFailed, o.pionMDNSWarned, o.pageICEState, o.remoteSummary, o.pageLastError)
		if o.startErr != nil {
			t.Logf("       start error: %v", o.startErr)
		}
	}
	t.Logf("=== failures: %d/%d ===", failures, len(outcomes))

	printed := map[int]bool{}
	for _, o := range outcomes {
		if !o.ok() {
			reproDumpIteration(t, o, linesFor)
			printed[o.iteration] = true
		}
	}
	for _, o := range outcomes {
		if o.ok() && !printed[o.iteration] {
			t.Logf("--- reference SUCCESS, for comparison ---")
			reproDumpIteration(t, o, linesFor)
			break
		}
	}
}

func reproDumpIteration(t *testing.T, o reproOutcome, linesFor func(int) []string) {
	t.Helper()
	t.Logf("--- iteration #%02d (%s) ---", o.iteration, o.stage())
	t.Logf("    %s", o.gatherMS)
	t.Logf("    %s", o.transitions)
	t.Logf("    encoder page: ice=%s conn=%s %s", o.pageICEState, o.pageConnState, o.pageLastError)
	for _, c := range o.remoteOffer {
		t.Logf("    chrome offered: %s", c)
	}
	for _, c := range o.localCands {
		t.Logf("    pion: %s", c)
	}
	// Every ice-diag line, on success as well as failure: the selected pair
	// and the per-candidate offsets are the comparison basis, and a summary
	// that drops them cannot answer why one run differed from another.
	for _, line := range linesFor(o.iteration) {
		if strings.Contains(line, "ice-diag") && strings.Contains(line, "selected pair") {
			t.Logf("    %s", strings.TrimSpace(line))
		}
	}
	if !o.ok() {
		t.Logf("    encoder page history:")
		for _, h := range o.pageHistory {
			t.Logf("      %s", h)
		}
		t.Logf("    full relay log:")
		for _, line := range linesFor(o.iteration) {
			t.Logf("      %s", line)
		}
	}
}

func reproDuration(d time.Duration) string {
	if d == 0 {
		return "never"
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}
