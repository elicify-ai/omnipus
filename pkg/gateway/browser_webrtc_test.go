// browser_webrtc_test.go — ADR-047 / wave-plan W2-A gateway WebRTC signaling
// tests (pkg/gateway/browser_webrtc.go). Mirrors browser_ws_test.go's
// convention of calling handler methods directly (white-box, same package)
// for logic that doesn't require a live Chromium — handleWebRTCOffer's gate
// ladder is pure config/capability/registry logic below the
// "start the encoder page" boundary, so it's fully testable without CDP.
//
// Traces to: docs/internal/architecture/ADR-047-live-browser-webrtc.md

package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// fakeRelay is a test double for browser.RelaySession — this package's own
// copy (pkg/tools/browser's fakeRelay in capture_session_test.go is
// unexported and package-private), mirroring wave-plan W2-A's "fake
// webrtc.Session behind a narrow interface you define at the consumption
// point."
type fakeRelay struct {
	mu            sync.Mutex
	closedViewers []string
	closeCalls    int
	// stats is returned by Stats() (fix-wave HIGH: the watchdog RTP-progress
	// staleness test needs a settable VideoPackets/HasVideo so it can hold
	// packet counts frozen across ticks — zero value matches every existing
	// caller's prior "always webrtc.Stats{}" expectation).
	stats webrtc.Stats
	// viewerOfferErr, when set, is returned by HandleViewerOffer instead of a
	// success answer — 2026-07-28 incident coverage (ingest-timeout
	// classification, TestHandleWebRTCOffer_ViewerOfferFailed_*): lets tests
	// exercise the offerErr branch of handleWebRTCOffer with a controlled
	// error (e.g. one wrapping webrtc.ErrNoIngestVideoTrack) without waiting
	// out a real waitForTracksTimeout. nil (the zero value) preserves every
	// existing caller's "always succeeds" expectation.
	viewerOfferErr error
	// viewerOfferBlock, when non-nil, makes HandleViewerOffer wait on it
	// before returning (FIX WAVE A finding 1 coverage): simulates a slow
	// negotiation — the real relay's waitForTracks can legitimately wait up
	// to waitForTracksTimeout — so a test can prove readLoop's own goroutine
	// is NOT blocked by it. nil (the zero value) preserves every existing
	// caller's "returns immediately" expectation.
	viewerOfferBlock chan struct{}
}

func (f *fakeRelay) HandleIngestOffer(sdp string) (string, error) { return "ingest-answer", nil }
func (f *fakeRelay) HandleViewerOffer(viewerID, sdp string) (string, error) {
	f.mu.Lock()
	err := f.viewerOfferErr
	block := f.viewerOfferBlock
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	if err != nil {
		return "", err
	}
	return "viewer-answer-" + viewerID, nil
}

func (f *fakeRelay) CloseViewer(viewerID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closedViewers = append(f.closedViewers, viewerID)
}
func (f *fakeRelay) SignalRecapture()                  {}
func (f *fakeRelay) SendToViewer(string, []byte) error { return nil }
func (f *fakeRelay) Stats() webrtc.Stats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stats
}

// setStats installs a new Stats() return value (fix-wave watchdog test seam).
func (f *fakeRelay) setStats(s webrtc.Stats) {
	f.mu.Lock()
	f.stats = s
	f.mu.Unlock()
}

func (f *fakeRelay) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return nil
}

// closeCount reports how many times Close() has been called (fix-wave
// watchdog test: proves the watchdog does not double-stop a session that
// already stopped for another reason).
func (f *fakeRelay) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCalls
}

// fakeEncoderStarter never touches real chromedp — it just counts
// invocations and either returns a bare cancelable context or startErr.
func fakeEncoderStarter(callCount *int32, startErr error) browser.EncoderStarter {
	return func(context.Context, *browser.BrowserManager, string, string, string) (context.Context, context.CancelFunc, error) {
		atomic.AddInt32(callCount, 1)
		if startErr != nil {
			return nil, nil, startErr
		}
		ctx, cancel := context.WithCancel(context.Background())
		return ctx, cancel, nil
	}
}

// webrtcStateFrameDecoder is a test-only decode target for
// browser_webrtc_state frames — not a wire-format type (Constraint #8 is
// about production pkg/gateway structs; this never round-trips over the
// wire, only decodes what the handler already marshaled via the generated
// type).
type webrtcStateFrameDecoder struct { // not-wire-format: decode-only test assertion target.
	Type      string `json:"type"`
	Available bool   `json:"available"`
	Active    *bool  `json:"active,omitempty"`
	HasAudio  *bool  `json:"has_audio,omitempty"`
	Reason    string `json:"reason,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// ReasonDetail is the free-text cause that accompanies the closed
	// `reason` enum (UAT case 16) — see browser_webrtc_reason_detail_test.go.
	ReasonDetail string `json:"reason_detail,omitempty"`
}

// newTestBrowserWSConn builds a bare browserWSConn with no real
// *websocket.Conn — safe for handleWebRTCOffer, which only ever writes via
// sendCriticalGen/sendFrameGen (sendCh), never touches wc.conn directly.
func newTestBrowserWSConn() *browserWSConn {
	return &browserWSConn{
		sendCh: make(chan []byte, 8),
		doneCh: make(chan struct{}),
	}
}

// drainOneFrame reads and decodes exactly one frame off wc.sendCh.
func drainOneFrame(t *testing.T, wc *browserWSConn) json.RawMessage {
	t.Helper()
	select {
	case data := <-wc.sendCh:
		return data
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a frame on sendCh")
		return nil
	}
}

func decodeWebRTCState(t *testing.T, raw json.RawMessage) webrtcStateFrameDecoder {
	t.Helper()
	var f webrtcStateFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, string(generated.WsFrameTypeBrowserWebrtcState), f.Type)
	return f
}

func TestHandleWebRTCOffer_GateLadder_DisabledByConfig(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = false
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-1",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig(), 0)

	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(t, got.Available, "webrtc_enabled=false must report available=false")
	require.Equal(t, "disabled", got.Reason)
	require.Nil(t, state.webrtc, "no viewer must be registered on the disabled path")
}

func TestHandleWebRTCOffer_GateLadder_InvalidFrame(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	var state browserConnState
	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", []byte("not json"), al.GetConfig(), 0)

	raw := drainOneFrame(t, wc)
	var f struct {
		Type  string `json:"type"`
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, "browser_status", f.Type)
	require.Equal(t, "error", f.State)
}

func TestHandleWebRTCOffer_GateLadder_MissingFields(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		SessionId: "sess-1",
		// AgentId and Sdp deliberately omitted.
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig(), 0)

	raw := drainOneFrame(t, wc)
	var f struct {
		Type  string `json:"type"`
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, "browser_status", f.Type)
	require.Equal(t, "error", f.State)
}

func TestHandleWebRTCOffer_GateLadder_UnknownAgent(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
	})
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   "agent-that-does-not-exist",
		Sdp:       "v=0\r\n",
		SessionId: "sess-1",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig(), 0)

	raw := drainOneFrame(t, wc)
	var f struct {
		Type  string `json:"type"`
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, "browser_status", f.Type)
	require.Equal(t, "error", f.State)
}

func TestHandleWebRTCOffer_GateLadder_NotCapable(t *testing.T) {
	// ProfileDir MUST be isolated under t.TempDir(): InstallRoot() is derived
	// from it (InstallRootForProfileDir), and leaving it unset would resolve
	// to this POD's real $OMNIPUS_HOME/browser/chromium — a real, persistent,
	// cross-test-shared directory that may already have a full-Chrome build
	// installed from prior E2E work, making this "not capable" assertion
	// flaky/false depending on unrelated pod state.
	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)
	// No full-Chrome build installed under mgr.InstallRoot() in this test's
	// tmp dir — CaptureVideoCapability must report not_capable. Asserted via
	// CaptureVideoCapability (the ADR-048 condition-3-aware gate), NOT the
	// older, narrower ClassifyVideoCapability: webrtcUnavailableReason (the
	// function handleWebRTCOffer's gate ladder actually calls, see
	// browser_webrtc.go) consults mgr.CaptureVideoCapability() specifically
	// (fix-wave A renamed the manager-facing path from VideoCapability to
	// CaptureVideoCapability) — asserting the precondition via the OLD
	// classifier only incidentally agrees here (no chrome installed) rather
	// than proving the SAME function the production code path consults.
	require.False(t, mgr.CaptureVideoCapability().Capable)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-1",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig(), 0)

	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(t, got.Available)
	require.Equal(t, "not_capable", got.Reason)
	require.Nil(t, state.webrtc)
}

// TestHandleWebRTCOffer_CapableButLaunchFails plants a fake (non-functional,
// merely executable-bit-set) "chrome" binary at the exact path
// ClassifyVideoCapability/findInstalledBuild look for, so the capability
// gate passes (Capable=true) — exercising the ladder's final branch
// ("ensure the capture session, then HandleViewerOffer") — WITHOUT a real
// Chromium: mgr.Session (called from defaultEncoderStarter, inside
// cs.Start) then tries to actually LAUNCH that fake binary over the CDP
// pipe, which fails fast (not a real Chrome process), so the session
// reports state=error rather than hanging. This is the closest unit-level
// proxy for the "ok" gate-ladder branch reachable without a real browser
// (real success is covered by the wave-3 Playwright e2e pass on the pod).
func TestHandleWebRTCOffer_CapableButLaunchFails(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		// chrome-for-testing publishes no linux/arm64 build (installer.go's
		// cftPlatform errors before findInstalledBuild ever inspects the
		// fake binary this test plants below), so ClassifyVideoCapability
		// can never report Capable=true on linux/arm64 either — confirmed
		// by the Cross-Platform CI matrix (ubuntu-24.04-arm, arm64) failing
		// here.
		t.Skip("ClassifyVideoCapability only ever reports Capable=true on linux/amd64")
	}
	// ProfileDir isolation is DOUBLY load-bearing here (see the identical
	// note on TestHandleWebRTCOffer_GateLadder_NotCapable): this test also
	// WRITES a fake "chrome" binary into InstallRoot() below — without
	// isolation that write would permanently pollute this pod's real
	// $OMNIPUS_HOME/browser/chromium directory for every other test/process
	// on the box, not just leak state within this test run.
	// Force the exec resolver onto the managed install path (the fake binary
	// below), bypassing its $PATH lookup of a real google-chrome/chromium.
	// Without this, a runner that happens to have real Chrome installed (GH
	// Actions ubuntu-latest images do) resolves to it INSTEAD of the fake
	// binary this test relies on for a deterministic, fast launch failure —
	// resolveExecPath (exec_resolver.go's execPathCaches.resolve) tries $PATH
	// BEFORE the managed install root unless this override is set. See
	// exec_resolver.go's resolve doc comment.
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "1")

	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	installRoot := mgr.InstallRoot()
	fakeBinDir := filepath.Join(installRoot, "fake-version", "chrome-linux64")
	require.NoError(t, os.MkdirAll(fakeBinDir, 0o755))
	fakeBin := filepath.Join(fakeBinDir, "chrome")
	require.NoError(t, os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	require.True(
		t,
		browser.ClassifyVideoCapability(installRoot).Capable,
		"capability gate must now report Capable=true",
	)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-1",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig(), 0)

	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(t, got.Available, "a launch failure must degrade to available=false, never break the JPEG fallback")
	require.Equal(t, "error", got.Reason)
}

func TestHandleWebRTCOffer_DetachClearsViewerState(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	// Fabricate a webrtc-attached state as if handleWebRTCOffer had ACTUALLY
	// succeeded — i.e. via AddViewer + HandleViewerOffer, exactly like
	// handleWebRTCOffer's own call sequence, so the resulting
	// *browser.ViewerAttachHandle is real and detachWebRTCViewer's
	// identity-checked CleanupViewerOffer(att.handle) has something genuine
	// to act on (fix-wave GAP 1: detachWebRTCViewer no longer tears down via
	// the bare, viewerID-only Relay().CloseViewer/RemoveViewer pair — see its
	// doc comment) — and verify detachWebRTCViewer still tears a normal
	// (non-superseded) attachment down cleanly — this is the readLoop defer /
	// browser_detach cleanup path (browser_ws.go), tested here without a live
	// relay.
	var calls int32
	relay := &fakeRelay{}
	cs, err := browser.NewCaptureSessionWithDeps(nil, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	gen := cs.AddViewer("viewer-1")
	_, handle, err := cs.HandleViewerOffer("viewer-1", "offer-sdp", gen)
	require.NoError(t, err)

	state := browserConnState{webrtc: &webrtcAttachment{agentID: defaultAgent.ID, capture: cs, handle: handle}}
	handler.detachWebRTCViewer(&state, "viewer-1")

	require.Nil(t, state.webrtc)
	require.Equal(t, 0, cs.ViewerCount())
	relay.mu.Lock()
	closed := append([]string(nil), relay.closedViewers...)
	relay.mu.Unlock()
	require.Equal(t, []string{"viewer-1"}, closed)
}

// TestHandleWebRTCOffer_DetachOfStaleAttachmentDoesNotClobberNewerOfferSameViewerID
// is GAP 1's OTHER direction: detachWebRTCViewer must not undo a NEWER,
// still-live registration for the same viewerID when the thing it is
// actually tearing down (state.webrtc) is an OLDER, already-superseded
// attachment. This reproduces the traced race directly: offer A commits (its
// *ViewerAttachHandle lands in state.webrtc); a second offer B for the SAME
// viewerID (e.g. an ICE-restart reconnect) then registers its own,
// independent AddViewer generation + relay connection — but has NOT yet
// itself committed into state.webrtc (commitWebRTCAttachment/webrtcEpoch only
// guard the single-slot state.webrtc field, not CaptureSession.viewers or the
// relay's own registry — see detachWebRTCViewer's doc comment). A detach or
// connection-close then arrives and pulls out A (the still-committed
// attachment) via takeWebRTCAttachment.
//
// Before the GAP 1 fix, detachWebRTCViewer called
// att.capture.RemoveViewer(viewerID) — UNCONDITIONAL, viewerID-only — which
// would have deleted B's CaptureSession-level registration too (ViewerCount
// dropping to 0 while B is still live). CleanupViewerOffer(att.handle) is
// gen-checked (RemoveViewerIfCurrent) and must leave B's registration
// completely untouched.
func TestHandleWebRTCOffer_DetachOfStaleAttachmentDoesNotClobberNewerOfferSameViewerID(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	const viewerID = "viewer-reconnect-race"
	var calls int32
	relay := &fakeRelay{}
	cs, err := browser.NewCaptureSessionWithDeps(nil, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	// Offer A: dispatched first, committed into state.webrtc (the ONLY thing
	// this connection currently believes is its live WebRTC attachment).
	genA := cs.AddViewer(viewerID)
	_, handleA, err := cs.HandleViewerOffer(viewerID, "offer-sdp-a", genA)
	require.NoError(t, err)
	state := browserConnState{webrtc: &webrtcAttachment{agentID: defaultAgent.ID, capture: cs, handle: handleA}}

	// Offer B: a SEPARATE, later offer for the SAME viewerID that has ALSO
	// registered successfully (superseding A's generation in
	// CaptureSession.viewers) but has NOT itself committed into state.webrtc
	// yet — modeling the exact window handleWebRTCOffer's own goroutine
	// occupies between HandleViewerOffer returning and commitWebRTCAttachment
	// running.
	genB := cs.AddViewer(viewerID)
	require.NotEqual(t, genA, genB, "setup: AddViewer must mint a distinct generation per call")
	_, _, err = cs.HandleViewerOffer(viewerID, "offer-sdp-b", genB)
	require.NoError(t, err)
	require.Equal(t, 1, cs.ViewerCount(), "setup: B's registration must be live before the detach races in")

	// A detach (or the connection closing) arrives, targeting the connection's
	// SINGLE committed attachment — which is still A, since B hasn't
	// committed.
	handler.detachWebRTCViewer(&state, viewerID)

	require.Nil(t, state.webrtc, "detach must always clear the connection's committed attachment slot")
	require.Equal(t, 1, cs.ViewerCount(),
		"A's stale cleanup must not clobber B's newer, still-live registration for the same viewerID")
}

// ---------------------------------------------------------------------------
// Input mapping parity (wave-plan W2-A item 4)
// ---------------------------------------------------------------------------

func TestBrowserInputFrameToLiveInput_TableParity(t *testing.T) {
	f64 := func(v float64) *float64 { return &v }
	str := func(v string) *string { return &v }
	i := func(v int) *int { return &v }

	cases := []struct {
		name  string
		frame generated.BrowserInputFrame
		want  browser.LiveInput
	}{
		{
			name:  "mouse_move with coords",
			frame: generated.BrowserInputFrame{Kind: "mouse_move", X: f64(12.5), Y: f64(30)},
			want:  browser.LiveInput{Kind: "mouse_move", X: 12.5, Y: 30, HasXY: true},
		},
		{
			name:  "mouse_down with button",
			frame: generated.BrowserInputFrame{Kind: "mouse_down", X: f64(1), Y: f64(2), Button: str("left")},
			want:  browser.LiveInput{Kind: "mouse_down", X: 1, Y: 2, HasXY: true, Button: "left"},
		},
		{
			name:  "wheel with deltas, no coords",
			frame: generated.BrowserInputFrame{Kind: "wheel", DeltaX: f64(-5), DeltaY: f64(10)},
			want:  browser.LiveInput{Kind: "wheel", DeltaX: -5, DeltaY: 10, HasXY: false},
		},
		{
			name: "key_down with key/code/keycode/modifiers",
			frame: generated.BrowserInputFrame{
				Kind: "key_down", Key: str("Enter"), Code: str("Enter"),
				KeyCode: i(13), Modifiers: i(2),
			},
			want: browser.LiveInput{Kind: "key_down", Key: "Enter", Code: "Enter", KeyCode: 13, Modifiers: 2},
		},
		{
			name:  "text",
			frame: generated.BrowserInputFrame{Kind: "text", Text: str("hello")},
			want:  browser.LiveInput{Kind: "text", Text: "hello"},
		},
		{
			name:  "navigate with url, no coords",
			frame: generated.BrowserInputFrame{Kind: "navigate", Url: str("https://example.com")},
			want:  browser.LiveInput{Kind: "navigate", URL: "https://example.com"},
		},
		{
			name:  "coordinate omitted entirely (HasXY must stay false, not silently 0,0)",
			frame: generated.BrowserInputFrame{Kind: "mouse_up"},
			want:  browser.LiveInput{Kind: "mouse_up", HasXY: false},
		},
		{
			name:  "explicit 0,0 coordinates ARE HasXY=true (distinguishable from omitted)",
			frame: generated.BrowserInputFrame{Kind: "mouse_move", X: f64(0), Y: f64(0)},
			want:  browser.LiveInput{Kind: "mouse_move", X: 0, Y: 0, HasXY: true},
		},
		{
			// Root-cause doc Fault 3
			// (docs/internal/browser-viewport-input-rootcause-2026-07-31.md):
			// capture_width/capture_height carry the intrinsic pixel size of
			// the capture frame the client mapped x/y into, so
			// dispatchInput can rescale into the tab's real CSS viewport.
			name: "mouse_move with capture_width/capture_height set",
			frame: generated.BrowserInputFrame{
				Kind: "mouse_move", X: f64(100), Y: f64(79),
				CaptureWidth: f64(319), CaptureHeight: f64(158),
			},
			want: browser.LiveInput{
				Kind: "mouse_move", X: 100, Y: 79, HasXY: true,
				CaptureWidth: 319, CaptureHeight: 158,
			},
		},
		{
			// Omitted capture_width/capture_height (older client, or a
			// server that never populated them) must convert to the zero
			// value, not some sentinel — dispatchInput's rescale gate
			// (CaptureWidth > 0 && CaptureHeight > 0) relies on exactly
			// this to mean "dispatch unscaled".
			name:  "mouse_down with capture_width/capture_height omitted",
			frame: generated.BrowserInputFrame{Kind: "mouse_down", X: f64(5), Y: f64(6), Button: str("left")},
			want: browser.LiveInput{
				Kind: "mouse_down", X: 5, Y: 6, HasXY: true, Button: "left",
				CaptureWidth: 0, CaptureHeight: 0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := browserInputFrameToLiveInput(tc.frame)
			require.Equal(t, tc.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// captureRegistry
// ---------------------------------------------------------------------------

func TestCaptureRegistry_SetFindRemove(t *testing.T) {
	reg := newCaptureRegistry()

	var calls int32
	relayA := &fakeRelay{}
	csA, err := browser.NewCaptureSessionWithDeps(nil, "agent-a", relayA, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	relayB := &fakeRelay{}
	csB, err := browser.NewCaptureSessionWithDeps(nil, "agent-b", relayB, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	reg.set("agent-a", csA)
	reg.set("agent-b", csB)

	gotID, gotCS := reg.findByToken(csA.TokenHex())
	require.Equal(t, "agent-a", gotID)
	require.Same(t, csA, gotCS)

	gotID, gotCS = reg.findByToken(csB.TokenHex())
	require.Equal(t, "agent-b", gotID)
	require.Same(t, csB, gotCS)

	gotID, gotCS = reg.findByToken("0000")
	require.Empty(t, gotID)
	require.Nil(t, gotCS)

	// removeIfCurrent with a stale reference must not remove the live entry.
	stale, err := browser.NewCaptureSessionWithDeps(nil, "agent-a", &fakeRelay{}, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	reg.removeIfCurrent("agent-a", stale)
	gotID, gotCS = reg.findByToken(csA.TokenHex())
	require.Equal(t, "agent-a", gotID)
	require.Same(t, csA, gotCS)

	reg.removeIfCurrent("agent-a", csA)
	gotID, gotCS = reg.findByToken(csA.TokenHex())
	require.Empty(t, gotID)
	require.Nil(t, gotCS)
}

// ---------------------------------------------------------------------------
// Capture-ingest WS loopback gate + token auth
// ---------------------------------------------------------------------------

func TestCaptureIngestWSHandler_RejectsNonLoopback(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, nil)
	handler := newCaptureIngestWSHandler(al, newCaptureRegistry())

	req := httptest.NewRequest("GET", "/api/v1/browser/capture-ingest", nil)
	req.RemoteAddr = "8.8.8.8:54321"
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, 403, rec.Code, "a non-loopback RemoteAddr must be rejected before any WS upgrade")
}

func TestCaptureIngestWSHandler_TokenMismatchClosesConnection(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, nil)
	reg := newCaptureRegistry()
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "agent-a", &fakeRelay{}, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	reg.set("agent-a", cs)

	handler := newCaptureIngestWSHandler(al, reg)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + srv.URL[len("http"):] + "/api/v1/browser/capture-ingest"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { _ = conn.Close() })

	hello := generated.BrowserCaptureHelloFrame{
		Type:       string(generated.WsFrameTypeBrowserCaptureHello),
		Token:      "0000000000000000000000000000000000000000000000000000000000000000",
		ExtVersion: "1.0.0",
	}
	data, err := json.Marshal(hello)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	_, _, readErr := conn.ReadMessage()
	require.Error(t, readErr, "a token-mismatched hello must close the connection with no further frames")
}

// TestCaptureIngestWSHandler_RejectsNonWebSocketUpgrade covers the
// ServeHTTP branch BETWEEN the loopback gate and the WS upgrade — a
// loopback request that never sets an Upgrade:websocket header must be
// rejected with 426 (Upgrade Required), never silently 200/close. Gap-sweep
// addition (W2-C): this specific branch had no coverage — only the
// non-loopback (403, TestCaptureIngestWSHandler_RejectsNonLoopback) and
// token-mismatch paths were tested.
func TestCaptureIngestWSHandler_RejectsNonWebSocketUpgrade(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, nil)
	handler := newCaptureIngestWSHandler(al, newCaptureRegistry())

	req := httptest.NewRequest("GET", "/api/v1/browser/capture-ingest", nil)
	req.RemoteAddr = "127.0.0.1:54321" // loopback -- must pass the FIRST gate
	// Deliberately NO Upgrade header.
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, 426, rec.Code, "a loopback, non-WS-upgrade request must be rejected with 426 Upgrade Required")
}

// TestCaptureIngestWSHandler_MalformedFirstFrame_NotHello_ClosesConnection
// covers serveConn's "not_hello" branch: a first frame that is either
// invalid JSON or valid JSON with the wrong `type` (i.e. not
// browser_capture_hello) must close the connection immediately, exactly
// like a token mismatch — before ANY session lookup is attempted. Gap-sweep
// addition (W2-C): only the valid-hello/wrong-token path
// (TestCaptureIngestWSHandler_TokenMismatchClosesConnection) was covered;
// this proves the type-discriminator gate itself is enforced.
func TestCaptureIngestWSHandler_MalformedFirstFrame_NotHello_ClosesConnection(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, nil)
	reg := newCaptureRegistry()
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "agent-a", &fakeRelay{}, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	reg.set("agent-a", cs)

	handler := newCaptureIngestWSHandler(al, reg)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + srv.URL[len("http"):] + "/api/v1/browser/capture-ingest"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { _ = conn.Close() })

	// A syntactically valid frame, but the WRONG type (not
	// browser_capture_hello) -- must be treated identically to garbage JSON.
	wrongType := generated.BrowserCaptureControlFrame{
		Type:   string(generated.WsFrameTypeBrowserCaptureControl),
		Action: "ping",
	}
	data, err := json.Marshal(wrongType)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	_, _, readErr := conn.ReadMessage()
	require.Error(t, readErr, "a non-hello first frame must close the connection with no further frames")
}

// TestCaptureIngestWSHandler_ValidateInbound_RejectsSchemaInvalidHello
// proves gateway.validate_inbound is actually WIRED on the capture-ingest
// socket (not just that the BrowserCaptureHelloFrame.yaml schema itself is
// correct in isolation -- see TestWebRTCFrameSchemasRoundTrip in
// browser_webrtc_contract_test.go for that): a hello frame with a
// schema-invalid token (shorter than the schema's minLength:16) must be
// rejected via the "schema_invalid" branch of serveConn, closing the
// connection, before token lookup ever runs -- distinct from a
// well-formed-but-wrong token (TestCaptureIngestWSHandler_TokenMismatchClosesConnection,
// which is a "token_mismatch" rejection reachable only once schema
// validation has already passed or is disabled). Gap-sweep addition (W2-C).
func TestCaptureIngestWSHandler_ValidateInbound_RejectsSchemaInvalidHello(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Gateway.ValidateInbound = true
	})
	reg := newCaptureRegistry()
	handler := newCaptureIngestWSHandler(al, reg)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + srv.URL[len("http"):] + "/api/v1/browser/capture-ingest"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { _ = conn.Close() })

	// token is well under BrowserCaptureHelloFrame.yaml's minLength:16 --
	// schema-invalid regardless of whether any session's real token matches.
	tooShort := generated.BrowserCaptureHelloFrame{
		Type:       string(generated.WsFrameTypeBrowserCaptureHello),
		Token:      "short",
		ExtVersion: "1.0.0",
	}
	data, err := json.Marshal(tooShort)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	_, _, readErr := conn.ReadMessage()
	require.Error(
		t,
		readErr,
		"a schema-invalid hello (token too short) must close the connection when validate_inbound is enabled",
	)
}

func TestCaptureIngestWSHandler_HelloSupersedesPreviousConnection(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, nil)
	reg := newCaptureRegistry()
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "agent-a", &fakeRelay{}, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	reg.set("agent-a", cs)

	handler := newCaptureIngestWSHandler(al, reg)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + srv.URL[len("http"):] + "/api/v1/browser/capture-ingest"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	hello := generated.BrowserCaptureHelloFrame{
		Type:       string(generated.WsFrameTypeBrowserCaptureHello),
		Token:      cs.TokenHex(),
		ExtVersion: "1.0.0",
	}
	data, err := json.Marshal(hello)
	require.NoError(t, err)

	conn1, resp1, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp1 != nil {
		resp1.Body.Close()
	}
	t.Cleanup(func() { _ = conn1.Close() })
	require.NoError(t, conn1.WriteMessage(websocket.TextMessage, data))

	// Give the server a moment to bind conn1 as the current ingest connection.
	time.Sleep(100 * time.Millisecond)

	conn2, resp2, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp2 != nil {
		resp2.Body.Close()
	}
	t.Cleanup(func() { _ = conn2.Close() })
	require.NoError(t, conn2.WriteMessage(websocket.TextMessage, data))

	// conn1 must be closed by the server once conn2's hello supersedes it.
	conn1.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	_, _, readErr := conn1.ReadMessage()
	require.Error(
		t,
		readErr,
		"the OLD ingest connection must be closed once a second hello with the same token arrives",
	)
}

// ── ADR-048 condition-2 fence (re-scoped 2026-07-18) ─────────────────────────
//
// UAT root-cause regression tests for "video black when the agent drives":
// the fence must deny a NEW capture only when another agent's capture
// session is ACTIVELY VIEWED, must supersede (Stop) a viewerless leftover,
// and must never consult mere live browser sessions (the old behavior that
// permanently locked the panel out of video after a second agent's tools
// opened their own session).

// TestHandleWebRTCOffer_OtherAgentViewedCapture_Denied: another agent's
// capture session with an attached viewer is a true conflict — the offer is
// denied with reason "multi_agent_capture_denied", and the other session is left untouched.
func TestHandleWebRTCOffer_OtherAgentViewedCapture_Denied(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		// chrome-for-testing publishes no linux/arm64 build (installer.go's
		// cftPlatform errors before findInstalledBuild ever inspects the
		// fake binary this test plants below), so ClassifyVideoCapability
		// can never report Capable=true on linux/arm64 either — confirmed
		// by the Cross-Platform CI matrix (ubuntu-24.04-arm, arm64) failing
		// here.
		t.Skip("ClassifyVideoCapability only ever reports Capable=true on linux/amd64")
	}
	// See TestHandleWebRTCOffer_CapableButLaunchFails' identical Setenv for
	// why: forces the exec resolver onto the fake managed binary below
	// instead of a real Chrome the runner might have on $PATH. This
	// particular test denies at the fence before ever reaching a real
	// launch attempt, but the override is set for defense-in-depth
	// consistency with its sibling fence tests in this file.
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "1")
	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	// Capability gate must pass so the ladder reaches the fence.
	installRoot := mgr.InstallRoot()
	fakeBinDir := filepath.Join(installRoot, "fake-version", "chrome-linux64")
	require.NoError(t, os.MkdirAll(fakeBinDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fakeBinDir, "chrome"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	require.True(t, browser.ClassifyVideoCapability(installRoot).Capable)

	// Another agent's capture session, actively viewed.
	var calls int32
	otherCS, err := browser.NewCaptureSessionWithDeps(
		nil,
		"other-agent",
		&fakeRelay{},
		fakeEncoderStarter(&calls, nil),
		nil,
	)
	require.NoError(t, err)
	otherCS.AddViewer("their-viewer")
	handler.captures.set("other-agent", otherCS)
	t.Cleanup(otherCS.Stop)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-1",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig(), 0)

	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(t, got.Available, "an actively-viewed conflicting capture must deny the offer")
	require.Equal(t, "multi_agent_capture_denied", got.Reason)
	require.Nil(t, state.webrtc)
	select {
	case <-otherCS.Done():
		t.Fatal("the actively-viewed conflicting capture must NOT be stopped by a denied offer")
	default:
	}
}

// TestHandleWebRTCOffer_OtherAgentViewerlessCapture_Superseded: a viewerless
// (grace-period) capture session of another agent must be stopped and the
// ladder must proceed for the requesting agent — the ordinary single-user
// "panel moved to the other agent" flow that the old live-session fence
// broke. The ladder then proceeds to a launch failure in this
// chrome-less harness (same terminal state as
// TestHandleWebRTCOffer_CapableButLaunchFails); the discriminator between
// "denied at the fence" and "proceeded past it" is the superseded session's
// Done() channel.
func TestHandleWebRTCOffer_OtherAgentViewerlessCapture_Superseded(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		// chrome-for-testing publishes no linux/arm64 build (installer.go's
		// cftPlatform errors before findInstalledBuild ever inspects the
		// fake binary this test plants below), so ClassifyVideoCapability
		// can never report Capable=true on linux/arm64 either — confirmed
		// by the Cross-Platform CI matrix (ubuntu-24.04-arm, arm64) failing
		// here.
		t.Skip("ClassifyVideoCapability only ever reports Capable=true on linux/amd64")
	}
	// See TestHandleWebRTCOffer_CapableButLaunchFails' identical Setenv for
	// why: this test's ladder proceeds past the fence into a real launch
	// attempt for the requesting agent, which must hit the fake managed
	// binary below deterministically rather than a real Chrome the runner
	// might have on $PATH.
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "1")
	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	installRoot := mgr.InstallRoot()
	fakeBinDir := filepath.Join(installRoot, "fake-version", "chrome-linux64")
	require.NoError(t, os.MkdirAll(fakeBinDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fakeBinDir, "chrome"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	require.True(t, browser.ClassifyVideoCapability(installRoot).Capable)

	// Another agent's capture session with NO viewers (grace-period leftover).
	var calls int32
	otherCS, err := browser.NewCaptureSessionWithDeps(
		nil,
		"other-agent",
		&fakeRelay{},
		fakeEncoderStarter(&calls, nil),
		nil,
	)
	require.NoError(t, err)
	handler.captures.set("other-agent", otherCS)
	t.Cleanup(otherCS.Stop)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-1",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig(), 0)

	// FIX WAVE A finding 3: otherCS.Stop() now runs off h.captureFenceMu
	// (fired via `go otherCS.Stop()` so one agent's teardown can't block a
	// DIFFERENT agent's fence acquisition) — so the supersede is no longer
	// guaranteed to have completed the INSTANT handleWebRTCOffer returns.
	// Poll instead of a bare non-blocking select.
	require.Eventually(t, func() bool {
		select {
		case <-otherCS.Done():
			return true
		default:
			return false
		}
	}, 2*time.Second, 5*time.Millisecond,
		"a viewerless conflicting capture must be superseded (stopped), not left to deny the offer")
	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(t, got.Available, "this chrome-less harness must then fail at launch (the fence itself passed)")
	require.Equal(t, "error", got.Reason)
}

// TestCaptureRegistry_OtherSessions pins the fence's enumeration helper:
// only OTHER agents' sessions are returned.
func TestCaptureRegistry_OtherSessions(t *testing.T) {
	reg := newCaptureRegistry()
	var calls int32
	csA, err := browser.NewCaptureSessionWithDeps(nil, "agent-a", &fakeRelay{}, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	csB, err := browser.NewCaptureSessionWithDeps(nil, "agent-b", &fakeRelay{}, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	reg.set("agent-a", csA)
	reg.set("agent-b", csB)

	others := reg.otherSessions("agent-a")
	require.Len(t, others, 1)
	require.Contains(t, others, "agent-b")
	require.Empty(t, reg.otherSessions("zzz-nonexistent")["agent-c"])
	require.Len(t, reg.otherSessions("zzz-nonexistent"), 2)
}
