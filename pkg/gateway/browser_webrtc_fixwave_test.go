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
	"fmt"
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
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// newFixWaveHandlerWithAudit mirrors newBrowserWSHandlerWithAudit
// (browser_ws_test.go) but accepts a mutate func, the way
// newBrowserWSTestHandler does — this file's tests need both audit
// visibility AND per-test config control (ExecPath, ProfileDir).
func newFixWaveHandlerWithAudit(
	t *testing.T,
	mutate func(cfg *config.Config),
) (*BrowserWSHandler, *agent.AgentLoop, string) {
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
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			// This test resolves the default agent (browser capture is keyed to
			// it). There is no implicit "main" sentinel to be that agent any
			// more (ADR-064).
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
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
	handler, al, auditDir := newMeasuredFixWaveHandlerWithAudit(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.ExecPath = bogusExec
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)
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

	data, offerEpoch := prepareWebRTCHandlerFixture(t, handler, al, &state, data)

	handler.handleWebRTCOffer(wc, &state, "viewer-start-fail", "user-1", data, al.GetConfig(), offerEpoch)

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
	require.Nil(t, mgr.CaptureSessionForPanel("handler-fixture:sess-start-fail"),
		"a failed Start() must not leave a stale CaptureSession registered on the manager")

	cs2, err := handler.ensureCaptureSession(mgr, defaultAgent.ID, "handler-fixture:sess-start-fail", al.GetConfig())
	require.NoError(t, err)
	t.Cleanup(cs2.Stop)
	require.NotNil(t, cs2, "ensureCaptureSession after the cleared failure must construct a genuinely fresh session")
}

// ---------------------------------------------------------------------------
// 2026-07-28 incident: ingest-timeout classification (distinct from a
// generic HandleViewerOffer failure) on the offerErr branch — see
// webrtc.ErrNoIngestVideoTrack's doc comment (pkg/tools/browser/webrtc/
// ingest.go) and audit.EventBrowserWebRTCViewerOfferFailed's doc comment
// (pkg/audit/events.go) for the full incident writeup this closes.
// ---------------------------------------------------------------------------

// webrtcCapableGateMutate returns a newFixWaveHandlerWithAudit mutate func
// that makes webrtcUnavailableReason's gate ladder report available=true
// (WebRTCEnabled on, not a lite build, and CaptureVideoCapability.Capable
// true) WITHOUT needing a real installed Chrome — mirrors
// TestHandleWebRTCOffer_StartFailure_ClearsStickySessionAndAuditsDistinctEvent's
// technique: a configured, non-existent (never stat'd) ExecPath makes
// ClassifyVideoCapabilityWithExec trust it via its filename-only heuristic.
// Needed by the ingest-timeout classification tests below, which bypass
// Start() entirely (pre-seeded fake CaptureSession) but still pass through
// the SAME gate-ladder check every real offer does.
func webrtcCapableGateMutate(t *testing.T) func(cfg *config.Config) {
	t.Helper()
	tmpDir := t.TempDir()
	bogusExec := filepath.Join(tmpDir, "no-such-chrome-binary")
	return func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.ExecPath = bogusExec
	}
}

// TestWebrtcUnavailableReason_PoolAttachedManagerPassesTheGate is the
// gateway-level guard for the ADR-075 FR-037 pool cutover — the same
// regression pkg/tools/browser's TestCaptureVideoCapability_
// PoolAttachedCountsAsAttached pins one layer down, asserted here on the
// function production actually calls.
//
// Every manager the gateway ever sees comes from pkg/agent/loop.go's
// browserFactory, which calls AttachPool — and AttachPool deliberately leaves
// m.coordinator nil until the pool has launched Chrome. When the ADR-048
// attachment check still read m.coordinator directly, every such manager
// classified not_capable until something else happened to start a browser,
// which is what turned eleven WebRTC tests in this package from their real
// "error" outcomes into "not_capable" on CI, and what silently skipped the
// boot capture warm-up for an operator running warm_capture_at_boot with
// warm_tab_at_boot off.
//
// Deliberately NOT gated on runtime.GOOS: the eleven tests it backstops all
// skip off linux, which is exactly why this regression reached CI unseen from
// a Mac. The gate here is the host's own base video classification — if this
// machine cannot classify video-capable at all there is nothing to assert,
// and that is stated rather than assumed.
func TestWebrtcUnavailableReason_PoolAttachedManagerPassesTheGate(t *testing.T) {
	handler, al, _ := newFixWaveHandlerWithAudit(t, webrtcCapableGateMutate(t))
	t.Cleanup(handler.Wait)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	if !mgr.VideoCapability().Capable {
		t.Skipf("host cannot classify video-capable at all (%s) — nothing for the ADR-048 attachment check to gate",
			mgr.VideoCapability().Reason)
	}
	if !webrtc.Available {
		t.Skip("lite build: webrtcUnavailableReason short-circuits before the capability gate")
	}

	// The load-bearing precondition: no Chrome has been launched, so the
	// coordinator cache is empty and only the pool attachment can carry the
	// verdict. Without it this test would pass for the wrong reason on a host
	// that happened to start a browser.
	require.Nil(t, mgr.Coordinator(),
		"test setup: nothing in this test may launch Chrome — the pool attachment is what is under test")

	require.True(t, mgr.CaptureVideoCapability().Capable,
		"a pool-attached manager must classify capture-capable before its Chrome is launched (reason=%q)",
		mgr.CaptureVideoCapability().Reason)
	require.Equal(t, "", webrtcUnavailableReason(al.GetConfig(), mgr),
		"the gate ladder must let a pool-attached manager through, so a launch failure surfaces as \"error\" and not \"not_capable\"")
}

// newHandleWebRTCOfferWithFakeCapture pre-seeds mgr's CaptureSession with a
// fake-relay-backed session (via BrowserManager.EnsureCaptureSession, so
// handleWebRTCOffer's own ensureCaptureSession call finds it already
// populated and never constructs a REAL browser.NewCaptureSession) and then
// drives handleWebRTCOffer's full path — Start() (fake, instant) ->
// AddViewer -> HandleViewerOffer (fake, returns viewerOfferErr) — without
// ever touching real chromedp/Pion. Returns the decoded wire state frame.
func newHandleWebRTCOfferWithFakeCapture(
	t *testing.T,
	handler *BrowserWSHandler,
	al *agent.AgentLoop,
	agentID string,
	relay *fakeRelay,
) webrtcStateFrameDecoder {
	t.Helper()
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), agentID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, agentID, newRequestFixtureRelay(relay), fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	_, err = mgr.EnsureCaptureSession(func() (*browser.CaptureSession, error) { return cs, nil })
	require.NoError(t, err)
	t.Cleanup(cs.Stop)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   agentID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-offer-fail",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	data, offerEpoch := prepareWebRTCHandlerFixture(t, handler, al, &state, data)

	handler.handleWebRTCOffer(wc, &state, "viewer-offer-fail", "user-1", data, al.GetConfig(), offerEpoch)
	return decodeWebRTCState(t, drainOneFrame(t, wc))
}

// TestHandleWebRTCOffer_IngestTimeout_ClassifiedDistinctlyInAuditAndLogs
// proves the classification the 2026-07-28 incident fix added: a
// HandleViewerOffer failure wrapping webrtc.ErrNoIngestVideoTrack is audited
// under EventBrowserWebRTCViewerOfferFailed with reason="ingest_timeout" —
// distinguishable from any OTHER HandleViewerOffer failure, which the
// companion test below proves still classifies as reason="error".
//
// UPDATED: this test used to assert the WIRE-level reason stayed "error",
// because the 2026-07-28 fix was deliberately backend-only and deferred "a
// distinct wire-level reason + prompt client reaction" to a frontend-lead
// follow-up in src/lib/browserWebRTC.ts. THAT FOLLOW-UP HAS LANDED: the wire
// enum in contracts/components/schemas/BrowserWebRTCStateFrame.yaml (and the
// inlined copy in contracts/asyncapi.yaml, which is what actually feeds the
// generated zod) now carries ingest_timeout, translateWebRTCFallbackReason
// renders it, and handleWebRTCOffer sends the classified reason instead of
// the literal "error". So the wire assertion below now expects the same
// value the audit record does.
//
// The companion negative-control test is what keeps this honest: it still
// requires a NON-ingest failure to report "error" on the wire, so a
// regression that made every failure report ingest_timeout would fail there.
func TestHandleWebRTCOffer_IngestTimeout_ClassifiedDistinctlyInAuditAndLogs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ClassifyVideoCapabilityWithExec only ever reports Capable=true on linux")
	}
	handler, al, auditDir := newFixWaveHandlerWithAudit(t, webrtcCapableGateMutate(t))
	t.Cleanup(handler.Wait)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	relay := &fakeRelay{viewerOfferErr: fmt.Errorf(
		"webrtc: viewer [viewer-1/x]: %w after waiting 15s", webrtc.ErrNoIngestVideoTrack,
	)}
	got := newHandleWebRTCOfferWithFakeCapture(t, handler, al, defaultAgent.ID, relay)

	require.True(t, got.Available, "an ingest-timeout must still allow a future offer (available stays true)")
	require.Equal(t, "ingest_timeout", got.Reason,
		"the classified reason must reach the WIRE, not just the log and audit record — "+
			"a viewer told only \"reported an error starting video\" cannot tell an ingest "+
			"timeout (restart capture) from a generic failure (retry the viewer)")

	rec := lastBrowserAuditRecord(t, auditDir, audit.EventBrowserWebRTCViewerOfferFailed)
	assert.Equal(t, audit.SeverityWarn, rec.Severity)
	assert.Equal(t, defaultAgent.ID, rec.Fields["agent_id"])
	assert.Equal(t, "viewer-offer-fail", rec.Fields["viewer_id"])
	assert.Equal(t, "sess-offer-fail", rec.Fields["session_id"])
	assert.Equal(t, "ingest_timeout", rec.Fields["reason"],
		"a failure wrapping webrtc.ErrNoIngestVideoTrack must classify as ingest_timeout, not the generic error reason")
	assert.Contains(t, rec.Fields["error"], "no ingest video track")
}

// TestHandleWebRTCOffer_GenericViewerOfferFailure_StillClassifiedAsError is
// the negative control for the test above: proves the classifier actually
// DISTINGUISHES rather than always reporting "ingest_timeout" for any
// HandleViewerOffer failure.
func TestHandleWebRTCOffer_GenericViewerOfferFailure_StillClassifiedAsError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ClassifyVideoCapabilityWithExec only ever reports Capable=true on linux")
	}
	handler, al, auditDir := newFixWaveHandlerWithAudit(t, webrtcCapableGateMutate(t))
	t.Cleanup(handler.Wait)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	relay := &fakeRelay{viewerOfferErr: errors.New("webrtc: viewer offer: set remote description failed")}
	got := newHandleWebRTCOfferWithFakeCapture(t, handler, al, defaultAgent.ID, relay)

	require.True(t, got.Available)
	require.Equal(t, "error", got.Reason)

	rec := lastBrowserAuditRecord(t, auditDir, audit.EventBrowserWebRTCViewerOfferFailed)
	assert.Equal(t, "error", rec.Fields["reason"],
		"a non-ingest-timeout HandleViewerOffer failure must classify as the generic error reason")
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
		handler.notifyViewersStreamStopped(cs, cs.ViewerIDs())
	})

	wc, _, _ := registerPublicationViewer(t, handler, cs, "viewer-watchdog")
	cs.AddViewer("viewer-watchdog")

	// Establish a baseline ping, then go silent — LastPingAt stays fixed
	// while encoderLivenessStaleAfter elapses.
	cs.RecordPing()

	go handler.watchEncoderLiveness(cs, "watchdog-agent", encoderLivenessCheckInterval, encoderLivenessStaleAfter)

	require.Eventually(
		t,
		func() bool { return atomic.LoadInt32(&onStoppedCalls) == 1 },
		2*time.Second,
		5*time.Millisecond,
		"watchdog must stop a session with no ping beacon within staleAfter",
	)

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
	checkInterval, staleAfter := encoderLivenessCheckInterval, encoderLivenessStaleAfter
	go func() {
		handler.watchEncoderLiveness(cs, "watchdog-agent-2", checkInterval, staleAfter)
		close(watchdogDone)
	}()

	cs.Stop() // stops for a reason OTHER than watchdog staleness

	select {
	case <-watchdogDone:
	case <-time.After(2 * time.Second):
		t.Fatal("watchEncoderLiveness did not exit after cs.Done() closed")
	}
	require.Equal(
		t,
		1,
		relay.closeCount(),
		"relay.Close must have been called exactly once (no double-stop from the watchdog)",
	)
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
	f := newHandlerContextFixture(t, false)
	source, route := admittedInputRoute(t, f, "viewer-dc-err")
	failure := errors.New("browser live: no active live view")
	route.report(source, "mouse_move", failure)
	var first generated.BrowserStatusFrame
	require.NoError(t, json.Unmarshal(drainOneFrame(t, f.conn), &first))
	require.Equal(t, "error", first.State)
	require.NotNil(t, first.Message)
	require.Contains(t, *first.Message, failure.Error())
	require.NotNil(t, first.OperationOnly)
	require.True(t, *first.OperationOnly)
	route.report(source, "mouse_move", failure)
	select {
	case <-f.conn.sendCh:
		t.Fatal("identical continuous error escaped throttle")
	case <-time.After(100 * time.Millisecond):
	}
	route.report(source, "navigate", failure)
	var discrete generated.BrowserStatusFrame
	require.NoError(t, json.Unmarshal(drainOneFrame(t, f.conn), &discrete))
	require.Equal(t, "error", discrete.State)
}

// TestSurfaceWebRTCInputError_NoRegisteredViewer_IsNoop proves a detached/
// unregistered viewerID never panics and never blocks — the DC input sink
// races against detach in production (a message may arrive just as the
// viewer disconnects).
func TestSurfaceWebRTCInputError_NoRegisteredViewer_IsNoop(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	source, route := admittedInputRoute(t, f, "viewer-dc-err")
	f.state.clearAttachment()
	route.report(source, "mouse_move", errors.New("late input failure"))
	select {
	case frame := <-f.conn.sendCh:
		t.Fatalf("retired viewer received late error: %s", frame.data)
	default:
	}
}

// A current, attached media viewer receives a real navigation refusal through the production contextual input sink.
func TestWebrtcInputSink_NonBenignError_SurfacedToViewer(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	source, _ := admittedInputRoute(t, f, "viewer-nonbenign")
	raw, err := json.Marshal(generated.BrowserInputFrame{Type: "browser_input", Kind: "navigate", Url: strPtr("javascript:alert(1)")})
	require.NoError(t, err)
	newWebRTCContextInputSink(false)(source, "viewer-nonbenign", raw)
	var status generated.BrowserStatusFrame
	require.NoError(t, json.Unmarshal(drainOneFrame(t, f.conn), &status))
	require.Equal(t, "error", status.State)
	require.NotNil(t, status.Message)
	require.Contains(t, *status.Message, "browser input failed")
}

// Both attached viewers reach the same real navigation refusal, even when only one holds the presentation control indicator.
func TestWebrtcInputSink_NonControllerViewerIsNotRejected(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	sourceA, _ := admittedInputRoute(t, f, "viewerA")
	other := f
	other.state = &browserConnState{}
	other.conn = newTestBrowserWSConn()
	epoch := other.state.beginAttach()
	require.True(t, other.state.bindAttachment(epoch, f.manager, "chat", "panel"))
	t.Cleanup(func() { other.state.clearAttachment() })
	sourceB, _ := admittedInputRoute(t, other, "viewerB")
	require.True(t, f.manager.Live().TakeControl("panel", "viewerA"))
	raw, err := json.Marshal(generated.BrowserInputFrame{Type: "browser_input", Kind: "navigate", Url: strPtr("javascript:alert(1)")})
	require.NoError(t, err)
	sink := newWebRTCContextInputSink(false)
	for _, tc := range []struct {
		source context.Context
		viewer string
		conn   *browserWSConn
	}{{sourceA, "viewerA", f.conn}, {sourceB, "viewerB", other.conn}} {
		sink(tc.source, tc.viewer, raw)
		var status generated.BrowserStatusFrame
		require.NoError(t, json.Unmarshal(drainOneFrame(t, tc.conn), &status))
		require.Equal(t, "error", status.State)
		require.NotNil(t, status.Message)
		require.Contains(t, *status.Message, "browser input failed", "both viewers must reach the same downstream failure")
	}
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
		// Nested one level below t.TempDir(), like every other browser test in
		// this package — NOT the bare t.TempDir() this used to be.
		// InstallRootForProfileDir is grandparent(profileDir)/chromium, so a
		// bare t.TempDir() ("$TMPDIR/TestX/001") resolves the managed-Chrome
		// install root to "$TMPDIR/chromium" — a machine-wide path OUTSIDE
		// the test's own tree. On any host where an earlier run left a real
		// Chrome-for-Testing download there (reproduced on a dev Mac
		// 2026-09-03), the base classifier reports Capable=true and this
		// subtest's whole premise is false; it only kept passing because the
		// ADR-048 coordinator check happened to reject the manager for an
		// unrelated reason. Nesting puts the install root at
		// "$TMPDIR/TestX/chromium", which t.TempDir() guarantees is empty.
		cfg.Tools.Browser.ProfileDir = filepath.Join(t.TempDir(), "browser-profile")
	})
	t.Cleanup(handler.Wait)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

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
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)
	cfg := al.GetConfig()

	directReason := webrtcUnavailableReason(cfg, mgr)

	wc := newTestBrowserWSConn()
	handler.announceWebRTCAvailabilityContext(context.Background(), wc, mgr, "sess-agree", "viewer-agree", cfg)
	announced := decodeWebRTCState(t, drainOneFrame(t, wc))

	require.Equal(t, directReason, announced.Reason,
		"announceWebRTCAvailability must report the SAME reason webrtcUnavailableReason computes directly")
}
