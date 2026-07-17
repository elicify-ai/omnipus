// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// browser_metrics_test.go — Test 28 (M-7, FR-019):
// TestObservability_MetricsEmitted. Hermetic: every external dependency
// (classifier, ingest endpoint, encoder-page launch, capture driver,
// encoder-page listener) is faked exactly like browser_stream_test.go, so
// this drives the REAL production metric hooks — stream_relay.go's
// deliverToViewer drop path, browser_stream.go's StepDown/handleEncoderDrop
// capture-restart paths, browser_stream.go's videoRelayAdapter.Ingest
// fps/bitrate accounting, browser_ingest.go's auditReject — with no real
// Chrome, no CDP, no network.
//
// The Xvfb/PulseAudio sidecar restart counters (components B/C) are the one
// exception: their real crash->restart hook lives inside Linux-only,
// process-spawning code (display_linux.go / audiosink_linux.go) that this
// cross-platform package cannot drive without a real Xvfb/pulseaudio binary.
// That real end-to-end path is proven separately by additive assertions in
// pkg/tools/browser's pre-existing Test 17
// (TestXvfbSidecar_SpawnSuperviseRestart) and Test 31
// (TestPulseSidecar_RestartReEnumeratesDevice), which already simulate a
// sidecar crash+restart hermetically. Here we prove the registry side of
// those two metrics directly (register + increment), which is what "every
// FR-019 counter/gauge registered and increments" requires at this test's
// altitude.
package gateway

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

func TestObservability_MetricsEmitted(t *testing.T) {
	// Hermetic: swap in a fresh metrics registry for this test's duration
	// and restore the previous one afterward, so this test can assert exact
	// counts without being polluted by (or polluting) any other test that
	// shares these package-level singletons. The swap itself goes through
	// swapGlobalBrowserVideoMetrics (atomic.Pointer-backed) rather than a
	// plain package-var reassignment: StepDown/watchEncoder below launch
	// background goroutines that read the singleton concurrently, and a
	// plain reassignment here (including t.Cleanup's restore, which can run
	// while one of those goroutines is still in flight) would be an
	// unsynchronized race under `go test -race`.
	m := newBrowserVideoMetrics()
	prev := swapGlobalBrowserVideoMetrics(m)
	t.Cleanup(func() {
		swapGlobalBrowserVideoMetrics(prev)
		browser.SetBrowserMetricsRecorder(prev)
	})

	// ---- live-stream count (gauge) + capture restart (counter) ----
	// via a real orchestrator + fakes (same apparatus as browser_stream_test.go).
	ing := newFakeIngest()
	launch := &fakeLauncher{}
	cap := &fakeCaptureStarter{}
	srv := &fakeEncoderServer{}
	o := newTestOrchestrator(capable(), ing, launch.launch, cap.start, srv)
	// newOrchestrator already registered m.setStreamCounter(o.relay.StreamCount);
	// RegisterBrowserVideo (not called by newTestOrchestrator) is what wires
	// browser.SetBrowserMetricsRecorder in production, so wire it explicitly
	// here to exercise the same relay-drop hook below.
	browser.SetBrowserMetricsRecorder(m)

	wc := newTestVideoConn()
	h, err := o.AttachViewer(AttachParams{
		WC: wc, AgentID: "agent1", SessionID: "sess1", ViewerID: "v1",
		RootCtx: context.Background(), AgentCtx: context.Background(),
		VideoCaps: []string{"avc1.4D401E", "vp8"},
	})
	if err != nil || h == nil {
		t.Fatalf("expected a live stream; err=%v handle=%v", err, h)
	}
	defer h.Detach()
	streamID := ing.mintCalls[0]

	if got := m.streamCount(); got != 1 {
		t.Fatalf("live-stream count gauge = %d, want 1", got)
	}

	// ---- per-stream fps/bitrate (chunk/byte counters) ----
	adapter := &videoRelayAdapter{orch: o}
	adapter.Ingest(streamID, EncodedChunk{Seq: 0, TS: 0, Key: true, Kind: "video", Payload: make([]byte, 100)})
	adapter.Ingest(streamID, EncodedChunk{Seq: 1, TS: 33, Key: false, Kind: "video", Payload: make([]byte, 40)})
	adapter.Ingest(streamID, EncodedChunk{Seq: 0, TS: 0, Key: false, Kind: "audio", Payload: make([]byte, 20)})

	m.streamChunksMu.Lock()
	c := m.streamChunks[streamID]
	m.streamChunksMu.Unlock()
	if c == nil {
		t.Fatal("expected a per-stream chunk-counter entry after ingest")
	}
	if got := c.videoChunks.Load(); got != 2 {
		t.Fatalf("video chunk count = %d, want 2", got)
	}
	if got := c.videoBytes.Load(); got != 140 {
		t.Fatalf("video byte count = %d, want 140", got)
	}
	if got := c.audioChunks.Load(); got != 1 {
		t.Fatalf("audio chunk count = %d, want 1", got)
	}
	if got := c.audioBytes.Load(); got != 20 {
		t.Fatalf("audio byte count = %d, want 20", got)
	}

	// ---- per-viewer drop rate (counter) ----
	// A second viewer on the SAME stream, attaching AFTER a keyframe+delta
	// are already cached (the two video ingests above). CR2 made
	// StreamRelay.Attach flush that GOP replay (keyframe + 1 delta = 2
	// chunks) into the viewer's send queue WHILE HOLDING THE STREAM LOCK, so
	// the queue depth must be large enough to absorb that replay WITHOUT
	// dropping — a depth-1 queue (the stale precondition here) is consumed
	// by the replay itself before the test's own two intentional deltas ever
	// run, which is exactly the bug this test was failing on ("viewer drop
	// total = 2 before queue full, want 0"). Depth 3 = 2 replay chunks + the
	// "first delta fills it" step below, leaving the "second delta finds it
	// full" step to be the one genuine, isolated drop this test intends to
	// exercise — the exact production drop-in-isolation path (FR-004),
	// through relay.Attach + the real videoRelayAdapter.Ingest ->
	// deliverToViewer.
	smallWC := &browserWSConn{sendCh: make(chan *wsSendItem, 3), doneCh: make(chan struct{})}
	vv2 := newWSVideoViewer(smallWC, streamID, "sess2", "v2")
	if _, aerr := o.relay.Attach(streamID, vv2); aerr != nil {
		t.Fatalf("relay.Attach: %v", aerr)
	}
	// The GOP replay (2 chunks) must have been fully absorbed with no drop.
	if got := m.viewerDropTotal.Load(); got != 0 {
		t.Fatalf("viewer drop total = %d after Attach's GOP replay, want 0 (queue depth sized to absorb it)", got)
	}
	// First delta fills the queue (2 replay chunks + this one = 3/3; nobody
	// drains smallWC.sendCh).
	adapter.Ingest(streamID, EncodedChunk{Seq: 2, TS: 66, Key: false, Kind: "video", Payload: []byte{1}})
	if got := m.viewerDropTotal.Load(); got != 0 {
		t.Fatalf("viewer drop total = %d before the queue is full, want 0", got)
	}
	// Second delta finds the queue still full -> dropped in isolation.
	adapter.Ingest(streamID, EncodedChunk{Seq: 3, TS: 99, Key: false, Kind: "video", Payload: []byte{2}})
	if got := m.viewerDropTotal.Load(); got != 1 {
		t.Fatalf("viewer drop total = %d, want 1", got)
	}

	// ---- capture restart count (counter): StepDown path ----
	// PT-FLAKY fix: poll the ACTUAL metric under test, not a proxy signal.
	// StepDown (browser_stream.go:~787) starts the new capture driver (which
	// bumps cap.count()) BEFORE it calls IncCaptureRestart() — waiting on the
	// proxy (cap.count()==2) leaves a race window where this goroutine can
	// observe the proxy flip yet still read captureRestartTotal==0 an
	// instant later, which is exactly the intermittent "want 1" flake under
	// `-p` load. Polling captureRestartTotal directly closes that window
	// deterministically.
	o.StepDown(streamID)
	if !eventuallyTrue(2*time.Second, func() bool { return m.captureRestartTotal.Load() == 1 }) {
		t.Fatalf("capture restart total after StepDown = %d, want 1 within the deadline", m.captureRestartTotal.Load())
	}
	if got := cap.count(); got != 2 {
		t.Fatalf("expected StepDown to restart capture, cap.count() = %d, want 2", got)
	}

	// ---- capture restart count (counter): CRIT-002 re-mint+relaunch path ----
	// Same PT-FLAKY fix: handleEncoderDrop (browser_stream.go:~705) re-mints
	// + relaunches (bumping ing.mintCount()/launch.count()) BEFORE it calls
	// IncCaptureRestart() — poll the metric itself, not the proxy.
	firstTab := launch.tabAt(0)
	close(firstTab.done) // simulate the ingest drop (encoder tab crash)
	if !eventuallyTrue(2*time.Second, func() bool { return m.captureRestartTotal.Load() == 2 }) {
		t.Fatalf("capture restart total after encoder-drop relaunch = %d, want 2 (StepDown + relaunch) within the deadline", m.captureRestartTotal.Load())
	}
	if got := launch.count(); got != 2 {
		t.Fatalf("expected the orchestrator to relaunch after the simulated encoder drop; launch count = %d, want 2", got)
	}
	if got := ing.mintCount(); got != 2 {
		t.Fatalf("expected the orchestrator to re-mint after the simulated encoder drop; mint count = %d, want 2", got)
	}

	// ---- ingest-auth-reject count (counter), labeled by reason ----
	rejectIng := NewCaptureIngestHandler(IngestDeps{Relay: &spyRelay{}})
	fc := newFakeConn()
	fc.feedText(initFrameBytes("never-minted-stream", "bogus-token", "vp8"))
	fc.closeIncoming()
	waitDoneChan(t, serveAsync(rejectIng, fc))

	m.ingestAuthRejectMu.Lock()
	rejectCount := m.ingestAuthRejectTotal["unknown_or_dead_token"]
	m.ingestAuthRejectMu.Unlock()
	if rejectCount != 1 {
		t.Fatalf("ingest_auth_reject_total{reason=unknown_or_dead_token} = %d, want 1", rejectCount)
	}

	// ---- decode-error rate (counter) ----
	// No client->server wire message reports a decode error today (see
	// RecordDecodeError's doc comment in browser_metrics.go) — this proves
	// the metric is registered and increments; it is not wired to a live
	// production signal yet.
	m.RecordDecodeError()
	if got := m.decodeErrorTotal.Load(); got != 1 {
		t.Fatalf("decode error total = %d, want 1", got)
	}

	// ---- Xvfb / PulseAudio sidecar restart counts (counters) ----
	// See the file doc comment: the real crash->restart hook is Linux-only,
	// process-spawning code proven separately by pkg/tools/browser's Test
	// 17/31. Here we prove the registry method itself — the same method
	// those hooks call via browser.SetBrowserMetricsRecorder.
	m.IncXvfbSidecarRestart()
	m.IncPulseSidecarRestart()
	if got := m.xvfbSidecarRestartTotal.Load(); got != 1 {
		t.Fatalf("xvfb sidecar restart total = %d, want 1", got)
	}
	if got := m.pulseSidecarRestartTotal.Load(); got != 1 {
		t.Fatalf("pulse sidecar restart total = %d, want 1", got)
	}

	// ---- /metrics exposes every FR-019 series with the values above ----
	a := &restAPI{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	a.HandleMetrics(rr, req)
	body := rr.Body.String()

	for _, want := range []string{
		"# TYPE omnipus_browser_live_stream_count gauge",
		"omnipus_browser_live_stream_count 1",
		"# TYPE omnipus_browser_stream_chunks_total counter",
		"# TYPE omnipus_browser_stream_bytes_total counter",
		"# TYPE omnipus_browser_viewer_drop_total counter",
		"omnipus_browser_viewer_drop_total 1",
		"# TYPE omnipus_browser_decode_error_total counter",
		"omnipus_browser_decode_error_total 1",
		"# TYPE omnipus_browser_capture_restart_total counter",
		"omnipus_browser_capture_restart_total 2",
		"# TYPE omnipus_browser_xvfb_sidecar_restart_total counter",
		"omnipus_browser_xvfb_sidecar_restart_total 1",
		"# TYPE omnipus_browser_pulse_sidecar_restart_total counter",
		"omnipus_browser_pulse_sidecar_restart_total 1",
		"# TYPE omnipus_browser_ingest_auth_reject_total counter",
		`omnipus_browser_ingest_auth_reject_total{reason="unknown_or_dead_token"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/metrics body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// eventuallyTrue polls cond until it reports true or timeout elapses,
// returning cond()'s final value.
func eventuallyTrue(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// waitDoneChan blocks (with a bound) for serveAsync's completion channel.
func waitDoneChan(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveConn did not return within the deadline")
	}
}
