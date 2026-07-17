// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// browser_timeout_test.go — round-2 test-analysis TC-G6 (FR-018). Hermetic
// orchestrator-level coverage for the two FR-018 timeout paths that
// browser_stream_test.go's existing suite does not exercise:
//
//  1. capture/display bring-up that never produces a frame (StartCapture
//     itself returns an error, e.g. its own internal bring-up timeout — see
//     TestCapture_BringupTimeout in capture_test.go for that lower layer) —
//     AttachViewer must move the viewer straight to the unavailable state
//     and leave no dangling stream state behind.
//  2. a mid-stream liveness stall (chunks flow, then stop) — the
//     LivenessTimeout must fire and fail the stream (never spin forever),
//     surfacing both the generic unavailable status frame AND the
//     browser.live.video_stream_failed audit event.
//
// Every dependency is faked (fakeIngest, fakeLauncher, fakeCaptureStarter,
// fakeEncoderServer, capable(), newTestOrchestrator, newTestVideoConn — all
// reused directly from browser_stream_test.go, same package) — no real
// Chrome, no CDP, no network.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestStream_BringupFailure_NoStreamPersists is TC-G6(a): a capture bring-up
// that never produces a frame (StartCapture returns an error — the
// orchestrator-level symptom of a display/capture bring-up hang or timeout)
// must move the viewer straight to the unavailable state and leave no
// dangling stream: the minted token is ended, the encoder tab that was
// already opened is closed, and the loopback encoder listener is stopped.
//
// Traces to: docs/internal/specs/live-browser-video-streaming-spec.md FR-018 (bring-up bound).
func TestStream_BringupFailure_NoStreamPersists(t *testing.T) {
	ing := newFakeIngest()
	launch := &fakeLauncher{}
	capb := &fakeCaptureStarter{err: errors.New("capture bring-up: no frame produced within bound")}
	srv := &fakeEncoderServer{}
	o := newTestOrchestrator(capable(), ing, launch.launch, capb.start, srv)

	wc := newTestVideoConn()
	h, err := o.AttachViewer(AttachParams{
		WC: wc, AgentID: "agentX", SessionID: "sessX", ViewerID: "vX",
		AgentCtx:  context.Background(),
		VideoCaps: []string{"avc1.4D401E", "vp8"},
	})
	if err != nil {
		t.Fatalf("AttachViewer error: %v", err)
	}
	if h != nil {
		t.Fatal("expected a nil handle when capture bring-up never produces a frame")
	}

	f := findStatusError(t, wc)
	if f.Message == nil || *f.Message != browserVideoUnavailableMessage {
		t.Fatalf("expected the generic unavailable message, got %v", f.Message)
	}

	// No stream persists: the token that was minted for this attempt was
	// ended, the encoder tab that was already opened (encoder launch happens
	// BEFORE capture start in startStreamLocked) was closed, and the loopback
	// listener was stopped — nothing left running from the failed attempt.
	if ing.mintCount() != 1 || ing.endCount() != 1 {
		t.Fatalf(
			"expected the failed bring-up's token to be minted then ended; mint=%d end=%d",
			ing.mintCount(), ing.endCount(),
		)
	}
	if launch.count() != 1 {
		t.Fatalf("expected the encoder page to have been launched once before capture failed, got %d", launch.count())
	}
	if !launch.tabAt(0).closed.Load() {
		t.Fatal("expected the encoder tab to be closed after a failed capture bring-up")
	}
	if srv.stopCount() != 1 {
		t.Fatalf(
			"expected the loopback encoder listener to be stopped after a failed bring-up, got %d",
			srv.stopCount(),
		)
	}

	// Differentiation / no-dangling-state proof: clearing the injected error
	// and attaching again (same agent) must succeed and start a brand-new,
	// independent stream — the failed bring-up left no state blocking a
	// future attach.
	capb.mu.Lock()
	capb.err = nil
	capb.mu.Unlock()

	wc2 := newTestVideoConn()
	h2, err := o.AttachViewer(AttachParams{
		WC: wc2, AgentID: "agentX", SessionID: "sessX2", ViewerID: "vX2",
		AgentCtx:  context.Background(),
		VideoCaps: []string{"avc1.4D401E", "vp8"},
	})
	if err != nil {
		t.Fatalf("second AttachViewer error: %v", err)
	}
	if h2 == nil {
		t.Fatal("expected a live stream once capture bring-up succeeds")
	}
	defer h2.Detach()
	if ing.mintCount() != 2 {
		t.Fatalf("expected a fresh mint for the second, successful attach; got %d", ing.mintCount())
	}
	if launch.count() != 2 {
		t.Fatalf("expected a second, independent encoder launch; got %d", launch.count())
	}
}

// TestStream_BringupFailure_EmitsAuditEvent locks in the FR-024 fix that a cold
// capture bring-up failure — which unwinds manually in startStreamLocked before
// any *videoStream exists, so it cannot go through failStreamLocked — still
// emits a browser.live.* stream-failed audit record naming the stage. Before the
// fix only mid-stream stalls and kill-switch teardowns were audited, leaving a
// bring-up failure invisible in the audit trail.
func TestStream_BringupFailure_EmitsAuditEvent(t *testing.T) {
	ing := newFakeIngest()
	launch := &fakeLauncher{}
	capb := &fakeCaptureStarter{err: errors.New("capture bring-up: no frame produced within bound")}
	srv := &fakeEncoderServer{}

	dir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{Dir: dir})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}

	o := newOrchestrator(BrowserVideoDeps{
		Audit: lg,
		Config: BrowserVideoConfig{
			IngestWSURL:     "ws://127.0.0.1:5000/api/v1/browser/capture-ingest",
			LivenessTimeout: time.Hour,
		},
		Classify:      capable(),
		LaunchEncoder: launch.launch,
		StartCapture:  capb.start,
		EncoderServer: srv,
	})
	o.ingest = ing
	// ADR-044 Option A / Task B: launchEncoderTab now calls o.encoder.ensureRoot
	// BEFORE the LaunchEncoder seam faked above ever runs — fake ITS launch
	// seams too (mirrors newTestOrchestrator, browser_stream_test.go) so this
	// test stays hermetic (no real Chrome download/launch) and its injected
	// capture-start failure is what actually fails bring-up.
	o.encoder.resolveExecPath = func(context.Context, string) (string, error) {
		return "fake-encoder-chrome", nil
	}
	o.encoder.pipeLauncher = func(context.Context, string, string) (encoderPipeLaunch, error) {
		return encoderPipeLaunch{rootCtx: context.Background()}, nil
	}

	wc := newTestVideoConn()
	h, err := o.AttachViewer(AttachParams{
		WC: wc, AgentID: "agentZ", SessionID: "sessZ", ViewerID: "vZ",
		AgentCtx:  context.Background(),
		VideoCaps: []string{"avc1.4D401E", "vp8"},
	})
	if err != nil {
		t.Fatalf("AttachViewer error: %v", err)
	}
	if h != nil {
		t.Fatal("expected a nil handle when capture bring-up fails")
	}
	streamID := ing.mintCalls[0]

	if cerr := lg.Close(); cerr != nil {
		t.Fatalf("audit logger close: %v", cerr)
	}
	data, rerr := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if rerr != nil {
		t.Fatalf("read audit.jsonl: %v", rerr)
	}
	var recs []auditRec
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r auditRec
		if uerr := json.Unmarshal([]byte(line), &r); uerr != nil {
			t.Fatalf("unmarshal audit line %q: %v", line, uerr)
		}
		if strings.HasPrefix(r.Event, "browser.live.") {
			recs = append(recs, r)
		}
	}

	if got := countEvent(recs, audit.EventBrowserLiveVideoStreamFailed); got != 1 {
		t.Fatalf("expected exactly 1 %s audit event for the bring-up failure, got %d (records: %+v)",
			audit.EventBrowserLiveVideoStreamFailed, got, recs)
	}
	found := false
	for _, r := range recs {
		if r.Event != audit.EventBrowserLiveVideoStreamFailed {
			continue
		}
		if sid, _ := r.Fields["stream_id"].(string); sid != streamID {
			t.Fatalf("bring-up failed audit names stream_id=%q, want %q", sid, streamID)
		}
		if stage, _ := r.Fields["stage"].(string); stage != "start_capture" {
			t.Fatalf("bring-up failed audit stage=%q, want start_capture", stage)
		}
		found = true
	}
	if !found {
		t.Fatalf("no %s audit record found among: %+v", audit.EventBrowserLiveVideoStreamFailed, recs)
	}
}

// Note: this file reads audit output using auditRec/countEvent, both already
// defined in browser_ingest_test.go (same package `gateway`) — reused
// directly, not redeclared.

// TestStream_MidStreamStall_LivenessTimeoutFailsStream is TC-G6(b): a stream
// that IS genuinely live (a chunk was ingested and fanned to the viewer)
// whose chunks then STOP must have its FR-018 mid-stream liveness timeout
// fire and fail the stream — never spin forever waiting for a chunk that
// isn't coming. Uses a short LivenessTimeout in the orchestrator config (per
// the round-2 test-analysis instruction) and asserts BOTH the unavailable
// status frame delivered to the viewer AND the
// audit.EventBrowserLiveVideoStreamFailed audit record (content-checked
// against the actual failed stream_id, not just presence).
//
// Traces to: docs/internal/specs/live-browser-video-streaming-spec.md FR-018 (mid-stream liveness bound).
func TestStream_MidStreamStall_LivenessTimeoutFailsStream(t *testing.T) {
	ing := newFakeIngest()
	launch := &fakeLauncher{}
	capb := &fakeCaptureStarter{}
	srv := &fakeEncoderServer{}

	dir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{Dir: dir})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}

	const livenessTimeout = 80 * time.Millisecond
	o := newOrchestrator(BrowserVideoDeps{
		Audit: lg,
		Config: BrowserVideoConfig{
			IngestWSURL:     "ws://127.0.0.1:5000/api/v1/browser/capture-ingest",
			LivenessTimeout: livenessTimeout, // short — proves FR-018 fires promptly, not "eventually"
		},
		Classify:      capable(),
		LaunchEncoder: launch.launch,
		StartCapture:  capb.start,
		EncoderServer: srv,
	})
	o.ingest = ing
	// ADR-044 Option A / Task B: same hermeticity fix as
	// TestStream_BringupFailure_EmitsAuditEvent above — fake o.encoder's own
	// launch seams so ensureRoot never touches the network or spawns a real
	// Chrome process.
	o.encoder.resolveExecPath = func(context.Context, string) (string, error) {
		return "fake-encoder-chrome", nil
	}
	o.encoder.pipeLauncher = func(context.Context, string, string) (encoderPipeLaunch, error) {
		return encoderPipeLaunch{rootCtx: context.Background()}, nil
	}

	wc := newTestVideoConn()
	h, err := o.AttachViewer(AttachParams{
		WC: wc, AgentID: "agentY", SessionID: "sessY", ViewerID: "vY",
		AgentCtx:  context.Background(),
		VideoCaps: []string{"avc1.4D401E"},
	})
	if err != nil || h == nil {
		t.Fatalf("expected a live stream; err=%v handle=%v", err, h)
	}
	streamID := ing.mintCalls[0]
	drainStreamInit(t, wc)

	// One chunk flows — proves the stream is genuinely LIVE (mid-stream
	// stall), distinct from TestStream_BringupFailure_NoStreamPersists'
	// never-started scenario.
	adapter := &videoRelayAdapter{orch: o}
	adapter.Ingest(streamID, EncodedChunk{
		Seq: 0, TS: 1, Key: true, Codec: "avc1.4D4028", Kind: "video", Payload: []byte{1, 2},
	})
	nextBinaryChunk(t, wc, time.Second)

	// Then chunks STOP — no further Ingest call. The liveness timer armed by
	// noteChunk (called inside adapter.Ingest above) must fire and fail the
	// stream.
	f := findStatusError(t, wc)
	if f.Message == nil || *f.Message != browserVideoUnavailableMessage {
		t.Fatalf("expected the generic unavailable message after the liveness stall, got %v", f.Message)
	}

	// The stream was actually torn down: capture stopped, encoder tab closed,
	// loopback listener stopped, ingest stream ended.
	eventually(t, time.Second, func() bool {
		capb.mu.Lock()
		defer capb.mu.Unlock()
		return len(capb.drivers) == 1 && capb.drivers[0].stopped.Load()
	})
	if !launch.tabAt(0).closed.Load() {
		t.Fatal("expected the encoder tab to be closed after the liveness-timeout failure")
	}
	if srv.stopCount() != 1 {
		t.Fatalf("expected the loopback encoder listener to be stopped once, got %d", srv.stopCount())
	}
	if ing.endCount() != 1 || ing.endCalls[0] != streamID {
		t.Fatalf("expected EndStream(%q) exactly once; end calls=%v", streamID, ing.endCalls)
	}

	// The audit failure event was recorded, and it names THIS stream (content
	// check, not just presence) — proves the failure is attributed correctly,
	// not a generic/blank record.
	if cerr := lg.Close(); cerr != nil {
		t.Fatalf("audit logger close: %v", cerr)
	}
	data, rerr := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if rerr != nil {
		t.Fatalf("read audit.jsonl: %v", rerr)
	}
	var recs []auditRec
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r auditRec
		if uerr := json.Unmarshal([]byte(line), &r); uerr != nil {
			t.Fatalf("unmarshal audit line %q: %v", line, uerr)
		}
		if strings.HasPrefix(r.Event, "browser.live.") {
			recs = append(recs, r)
		}
	}

	if got := countEvent(recs, audit.EventBrowserLiveVideoStreamFailed); got != 1 {
		t.Fatalf("expected exactly 1 %s audit event, got %d (records: %+v)",
			audit.EventBrowserLiveVideoStreamFailed, got, recs)
	}
	found := false
	for _, r := range recs {
		if r.Event != audit.EventBrowserLiveVideoStreamFailed {
			continue
		}
		sid, _ := r.Fields["stream_id"].(string)
		if sid != streamID {
			t.Fatalf("stream_failed audit record names stream_id=%q, want %q", sid, streamID)
		}
		found = true
	}
	if !found {
		t.Fatalf("no %s audit record found among: %+v", audit.EventBrowserLiveVideoStreamFailed, recs)
	}
}
