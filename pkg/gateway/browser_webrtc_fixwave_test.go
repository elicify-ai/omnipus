//go:build !cgo

// browser_webrtc_fixwave_test.go — fix-wave BE findings coverage for
// pkg/gateway/browser_webrtc.go: fix 1 (sticky failed capture Start), fix 3
// (encoder-liveness watchdog + push-state-on-stop), fix 7 (DC input error
// parity), and fix 9 (single gate-ladder classifier). Mirrors
// browser_webrtc_test.go's conventions (newBrowserWSTestHandler,
// newTestBrowserWSConn/drainOneFrame/decodeWebRTCState, fakeRelay/
// fakeEncoderStarter) — kept in a separate file per the fix-wave's own
// grouping rather than growing the existing file further.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// newFixWaveHandlerWithAudit mirrors newBrowserWSHandlerWithAudit
// (browser_ws_test.go) but accepts a mutate func, the way
// newBrowserWSTestHandler does — this file's tests need both audit
// visibility AND per-test config control (ExecPath, CaptureSharedContext).
func newFixWaveHandlerWithAudit(t *testing.T, mutate func(cfg *config.Config)) (*BrowserWSHandler, *agent.AgentLoop, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	auditDir := filepath.Join(tmpDir, "system")

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: workspaceDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Mode:     config.SandboxModeOff,
			AuditLog: true,
		},
	}
	cfg.Tools.Browser.LiveViewEnabled = true
	cfg.Tools.Browser.TakeControlEnabled = true
	if mutate != nil {
		mutate(cfg)
	}

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newBrowserWSHandler(al, "")
	return handler, al, auditDir
}

// ---------------------------------------------------------------------------
// Fix 1: sticky failed capture Start.
// ---------------------------------------------------------------------------

// TestHandleWebRTCOffer_StartFailure_ClearsStickySessionAndAuditsDistinctEvent
// proves all three parts of fix 1: (a) the failure is audited under a
// DISTINCT event name (EventBrowserWebRTCStreamStartFailed), never a reuse
// of the success event at WARN; (b) cs.Stop() actually ran (its own
// onStopped-triggered EventBrowserWebRTCStreamStopped record fires); and
// (c) the manager's CaptureSession reference is cleared afterward, so
// ensureCaptureSession builds a genuinely FRESH session for the next offer
// instead of being stuck reusing the permanently-broken one.
//
// A configured, non-existent tools.browser.exec_path makes Start() fail
// FAST: ClassifyVideoCapabilityWithExec's exec_path branch is a filename-only
// heuristic (capability.go — never stats the path), so the capability gate
// still reports Capable=true, while defaultEncoderStarter's very first step
// (mgr.Session -> ensureStarted -> resolveExecPath) synchronously os.Stats
// the configured path and fails immediately (no real Chrome, no 20s
// captureStartTimeout wait) — the exact same fast-fail technique
// browser_ws_test.go's newTabActionTestFixtures already relies on.
func TestHandleWebRTCOffer_StartFailure_ClearsStickySessionAndAuditsDistinctEvent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ClassifyVideoCapabilityWithExec only ever reports Capable=true on linux")
	}
	tmpDir := t.TempDir()
	bogusExec := filepath.Join(tmpDir, "no-such-chrome-binary")
	handler, al, auditDir := newFixWaveHandlerWithAudit(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.CaptureSharedContext = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.ExecPath = bogusExec
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, ok := al.BrowserManagerForAgent(defaultAgent.ID)
	require.True(t, ok)
	require.True(t, mgr.CaptureVideoCapability().Capable,
		"capability gate must report Capable=true via the exec_path filename heuristic, so the ladder reaches Start()")

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-start-fail",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-start-fail", "user-1", data, al.GetConfig())

	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(t, got.Available)
	require.Equal(t, "error", got.Reason)
	require.Nil(t, state.webrtc, "a failed Start() must never leave a viewer registered on the connection")

	// (a) a DISTINCT audit event, not a reuse of StreamStarted-with-WARN.
	failRec := lastBrowserAuditRecord(t, auditDir, audit.EventBrowserWebRTCStreamStartFailed)
	assert.Equal(t, audit.SeverityWarn, failRec.Severity)
	assert.Equal(t, defaultAgent.ID, failRec.Fields["agent_id"])
	assert.Equal(t, "sess-start-fail", failRec.Fields["session_id"])
	assert.NotEmpty(t, failRec.Fields["error"])

	for _, r := range readBrowserAuditRecords(t, auditDir) {
		assert.NotEqual(t, audit.EventBrowserWebRTCStreamStarted, r.Event,
			"a failed Start() must never emit the SUCCESS event, at any severity")
	}

	// (b) cs.Stop() actually ran — proven by its own onStopped-side audit
	// record, not just the absence of a hang.
	lastBrowserAuditRecord(t, auditDir, audit.EventBrowserWebRTCStreamStopped)

	// (c) the sticky-session bug itself: mgr.CaptureSession() must be nil,
	// not pointing at the permanently-broken session.
	require.Nil(t, mgr.CaptureSession(),
		"a failed Start() must not leave a stale CaptureSession registered on the manager")

	cs2, err := handler.ensureCaptureSession(mgr, defaultAgent.ID, al.GetConfig())
	require.NoError(t, err)
	t.Cleanup(cs2.Stop)
	require.NotNil(t, cs2, "ensureCaptureSession after the cleared failure must construct a genuinely fresh session")
}

// ---------------------------------------------------------------------------
// Fix 3: encoder-liveness watchdog + push-state-to-viewers on any Stop().
// ---------------------------------------------------------------------------

// TestWatchEncoderLiveness_StopsStaleSessionAndNotifiesAttachedViewer proves
// fix 3 end to end at the unit level: a session whose LastPingAt goes stale
// (no browser_capture_control{ping} beacon within encoderLivenessStaleAfter)
// is stopped by the watchdog, AND the attached viewer immediately receives a
// browser_webrtc_state{available:false, reason:"error"} frame — rather than
// learning of the stop only once its own ICE connection eventually times out
// (~5s later, pre-fix).
func TestWatchEncoderLiveness_StopsStaleSessionAndNotifiesAttachedViewer(t *testing.T) {
	origInterval, origStale := encoderLivenessCheckInterval, encoderLivenessStaleAfter
	encoderLivenessCheckInterval = 5 * time.Millisecond
	encoderLivenessStaleAfter = 20 * time.Millisecond
	t.Cleanup(func() {
		encoderLivenessCheckInterval = origInterval
		encoderLivenessStaleAfter = origStale
	})

	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	var calls int32
	relay := &fakeRelay{}
	cs, err := browser.NewCaptureSessionWithDeps(nil, "watchdog-agent", relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	_, err = cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
	require.NoError(t, err)

	var onStoppedCalls int32
	cs.SetOnStopped(func() {
		atomic.AddInt32(&onStoppedCalls, 1)
		handler.notifyViewersStreamStopped(cs.ViewerIDs())
	})

	wc := newTestBrowserWSConn()
	handler.registerWebRTCViewerConn("viewer-watchdog", wc, "sess-watchdog")
	cs.AddViewer("viewer-watchdog")

	// Establish a baseline ping, then go silent — LastPingAt stays fixed
	// while encoderLivenessStaleAfter elapses.
	cs.RecordPing()

	go handler.watchEncoderLiveness(cs, "watchdog-agent")

	require.Eventually(t, func() bool { return atomic.LoadInt32(&onStoppedCalls) == 1 }, 2*time.Second, 5*time.Millisecond,
		"watchdog must stop a session with no ping beacon within staleAfter")

	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(t, got.Available)
	require.Equal(t, "error", got.Reason)
}

// TestWatchEncoderLiveness_ExitsOnDoneWithoutStopping proves the watchdog
// does NOT stop a session that stops for some OTHER reason first (e.g. an
// explicit Stop() elsewhere) — it must exit cleanly via cs.Done(), never
// double-stop or spin forever.
func TestWatchEncoderLiveness_ExitsOnDoneWithoutStopping(t *testing.T) {
	origInterval, origStale := encoderLivenessCheckInterval, encoderLivenessStaleAfter
	encoderLivenessCheckInterval = 5 * time.Millisecond
	encoderLivenessStaleAfter = 24 * time.Hour // never trips on its own
	t.Cleanup(func() {
		encoderLivenessCheckInterval = origInterval
		encoderLivenessStaleAfter = origStale
	})

	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	var calls int32
	relay := &fakeRelay{}
	cs, err := browser.NewCaptureSessionWithDeps(nil, "watchdog-agent-2", relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	watchdogDone := make(chan struct{})
	go func() {
		handler.watchEncoderLiveness(cs, "watchdog-agent-2")
		close(watchdogDone)
	}()

	cs.Stop() // stops for a reason OTHER than watchdog staleness

	select {
	case <-watchdogDone:
	case <-time.After(2 * time.Second):
		t.Fatal("watchEncoderLiveness did not exit after cs.Done() closed")
	}
	require.Equal(t, 1, relay.closeCount(), "relay.Close must have been called exactly once (no double-stop from the watchdog)")
}

// ---------------------------------------------------------------------------
// Fix 7: DC input dispatch error parity with the WS path's sessionErrorStatus.
// ---------------------------------------------------------------------------

// TestSurfaceWebRTCInputError_SendsAndThrottles proves fix 7's core
// behavior: a non-benign input dispatch error reaches the driving viewer's
// main WS connection as a browser_status(error) frame (mirroring
// handleInput's sessionErrorStatus), an IDENTICAL repeat within
// minInputErrorInterval is throttled for a non-discrete kind, and a discrete
// kind (navigate) is NEVER throttled — exactly handleInput's own discipline
// (browser_ws.go), reused here rather than reinvented.
func TestSurfaceWebRTCInputError_SendsAndThrottles(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	handler.registerWebRTCViewerConn("viewer-dc-err", wc, "sess-dc-err")
	t.Cleanup(func() { handler.unregisterWebRTCViewerConn("viewer-dc-err") })

	dispatchErr := errors.New("browser live: no active live view for session \"default\"")

	handler.surfaceWebRTCInputError("viewer-dc-err", "mouse_move", dispatchErr)
	frame1 := drainOneFrame(t, wc)
	var f1 struct {
		Type    string `json:"type"`
		State   string `json:"state"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(frame1, &f1))
	require.Equal(t, "browser_status", f1.Type)
	require.Equal(t, "error", f1.State)
	require.Contains(t, f1.Message, dispatchErr.Error())

	// An IDENTICAL repeat within the throttle window, for a non-discrete
	// kind, must be suppressed.
	handler.surfaceWebRTCInputError("viewer-dc-err", "mouse_move", dispatchErr)
	select {
	case <-wc.sendCh:
		t.Fatal("an identical repeat within minInputErrorInterval must be throttled for a non-discrete kind")
	case <-time.After(100 * time.Millisecond):
	}

	// A "navigate" kind (discrete) with the SAME message must NEVER be
	// throttled, even inside the cooldown window.
	handler.surfaceWebRTCInputError("viewer-dc-err", "navigate", dispatchErr)
	frame2 := drainOneFrame(t, wc)
	var f2 struct {
		Type  string `json:"type"`
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(frame2, &f2))
	require.Equal(t, "browser_status", f2.Type)
	require.Equal(t, "error", f2.State)
}

// TestSurfaceWebRTCInputError_NoRegisteredViewer_IsNoop proves a detached/
// unregistered viewerID never panics and never blocks — the DC input sink
// races against detach in production (a message may arrive just as the
// viewer disconnects).
func TestSurfaceWebRTCInputError_NoRegisteredViewer_IsNoop(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	// Must not panic; must return promptly (no registered conn to send to).
	handler.surfaceWebRTCInputError("never-registered-viewer", "mouse_move", errors.New("boom"))
}

// TestWebrtcInputSink_NonBenignError_SurfacedToViewer exercises fix 7's full
// production wiring end to end (not just surfaceWebRTCInputError in
// isolation, which TestSurfaceWebRTCInputError_SendsAndThrottles already
// covers directly): a real, non-benign LiveView dispatch failure — "no
// active live view for session" (session never attached at all) is
// explicitly a REAL/surfaced failure per handleInput's own doc comment
// (browser_ws.go), NOT one of IsBenignLiveInputError's three benign kinds
// (not-controller, rate-limited, and the third — see live.go) — must reach
// the registered viewer's WS connection as a browser_status(error) frame
// when dispatched through the ACTUAL production sink
// (handler.webrtcInputSink(mgr)), parsing a real DC message exactly as a
// browser's data channel would send it.
func TestWebrtcInputSink_NonBenignError_SurfacedToViewer(t *testing.T) {
	browserCfg, err := browser.DefaultConfig()
	require.NoError(t, err)
	browserCfg.ProfileDir = t.TempDir()
	mgr, err := browser.NewBrowserManager(browserCfg, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	// mgr.Live() with NO attach() ever called -> Input() returns "no active
	// live view for session ..." — a genuine, non-benign dispatch failure
	// (never a real Chrome launch attempt: LiveViewRegistry.Input's very
	// first step is a pure in-memory session lookup that fails before any
	// CDP call).

	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	handler.registerWebRTCViewerConn("viewer-nonbenign", wc, "sess-nonbenign")
	t.Cleanup(func() { handler.unregisterWebRTCViewerConn("viewer-nonbenign") })

	sink := handler.webrtcInputSink(mgr)
	x, y := 1.0, 2.0
	inputFrame := generated.BrowserInputFrame{Type: "browser_input", Kind: "mouse_move", X: &x, Y: &y}
	raw, err := json.Marshal(inputFrame)
	require.NoError(t, err)

	sink("viewer-nonbenign", raw)

	frame := drainOneFrame(t, wc)
	var f struct {
		Type    string `json:"type"`
		State   string `json:"state"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(frame, &f))
	require.Equal(t, "browser_status", f.Type)
	require.Equal(t, "error", f.State)
	require.Contains(t, f.Message, "browser input failed")
}

// ---------------------------------------------------------------------------
// Fix 9: single gate-ladder classifier (webrtcUnavailableReason).
// ---------------------------------------------------------------------------

// TestWebrtcUnavailableReason_GateLadder table-tests webrtcUnavailableReason
// directly — the shared classifier both announceWebRTCAvailability and
// handleWebRTCOffer now call, so the two paths can never spell a rejection
// reason differently.
func TestWebrtcUnavailableReason_GateLadder(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.ProfileDir = t.TempDir() // guaranteed not_capable: no chrome installed here
	})
	t.Cleanup(handler.Wait)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, ok := al.BrowserManagerForAgent(defaultAgent.ID)
	require.True(t, ok)

	t.Run("disabled", func(t *testing.T) {
		cfg := al.GetConfig()
		cfg.Tools.Browser.WebRTCEnabled = false
		require.Equal(t, "disabled", webrtcUnavailableReason(cfg, mgr))
	})

	t.Run("lite_build", func(t *testing.T) {
		if !webrtc.Available {
			t.Skip("this build is already lite (webrtc.Available=false) — covered by the not-lite branch elsewhere")
		}
		origAvailable := webrtc.Available
		webrtc.Available = false
		t.Cleanup(func() { webrtc.Available = origAvailable })

		cfg := al.GetConfig()
		cfg.Tools.Browser.WebRTCEnabled = true
		require.Equal(t, "lite_build", webrtcUnavailableReason(cfg, mgr))
	})

	t.Run("not_capable", func(t *testing.T) {
		cfg := al.GetConfig()
		cfg.Tools.Browser.WebRTCEnabled = true
		require.False(t, mgr.CaptureVideoCapability().Capable, "test setup: this ProfileDir must classify not-capable")
		require.Equal(t, "not_capable", webrtcUnavailableReason(cfg, mgr))
	})
}

// TestWebrtcUnavailableReason_AgreesAcrossBothCallers proves the SIMPL
// finding directly: announceWebRTCAvailability and handleWebRTCOffer's own
// gate-ladder evaluation (both now routed through webrtcUnavailableReason)
// produce the IDENTICAL reason for the identical (cfg, mgr) input — the bug
// class this fix eliminates is the two call sites drifting apart.
func TestWebrtcUnavailableReason_AgreesAcrossBothCallers(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = false // exercises the "disabled" branch identically in both paths
	})
	t.Cleanup(handler.Wait)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, ok := al.BrowserManagerForAgent(defaultAgent.ID)
	require.True(t, ok)
	cfg := al.GetConfig()

	directReason := webrtcUnavailableReason(cfg, mgr)

	wc := newTestBrowserWSConn()
	handler.announceWebRTCAvailability(wc, mgr, "sess-agree", "viewer-agree", cfg)
	announced := decodeWebRTCState(t, drainOneFrame(t, wc))

	require.Equal(t, directReason, announced.Reason,
		"announceWebRTCAvailability must report the SAME reason webrtcUnavailableReason computes directly")
}
