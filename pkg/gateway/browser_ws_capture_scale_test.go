// browser_ws_capture_scale_test.go — regression coverage for three external
// review findings against pkg/gateway/browser_ws.go / browser_webrtc.go
// (2026-08-13):
//
//   - F2 (HIGH): a browser_viewport frame's device_scale_factor arriving
//     before a WebRTC attachment commits used to be silently dropped —
//     handleViewport's SetCaptureScale call is gated on
//     peekWebRTCAttachment() != nil, and a cold panel open routinely sends
//     its (often only) viewport frame before browser_webrtc_offer finishes
//     negotiating. Fixed by browserConnState.pendingCaptureScale +
//     BrowserWSHandler.applyColdStartRecapture (browser_webrtc.go).
//   - F3 (HIGH): the capture-ingest control frame only carried capture_scale
//     when scale > 1, so a viewer dropping back to DPR 1 could never
//     un-ratchet a previously-higher scale. Fixed by sending capture_scale
//     unconditionally on every recapture action.
//   - F10 (MEDIUM): device_scale_factor was recorded on the capture session
//     before being range-checked anywhere (gateway.validate_inbound
//     defaults to false, so nothing else enforces the wire schema's
//     maximum). Fixed by clamping to maxDeviceScaleFactor in handleViewport
//     before the value is used for anything.
//
// Kept in a separate file per this project's own established convention
// (browser_webrtc_fixwave_test.go's header comment) rather than growing an
// existing file further.
//
// RESOURCE RULE: run narrowly —
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestBrowserWS_(HandleViewport|ApplyColdStartRecapture|CaptureIngest)' -p 1 ./pkg/gateway/
//
// never the full gateway suite / ./....

package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// f64ptr is a tiny helper so test literals can address a float64 constant
// inline, matching generated.BrowserViewportFrame.DeviceScaleFactor's
// *float64 field.
func f64ptr(v float64) *float64 { return &v }

// ---------------------------------------------------------------------------
// F10 — dsf must be clamped to the contract range BEFORE it is recorded
// anywhere, not just before SetViewport's own (separate) CDP-call range
// check.
// ---------------------------------------------------------------------------

// TestBrowserWS_HandleViewport_ClampsOutOfRangeScale_BeforeRecording proves a
// malformed/hostile device_scale_factor (50, far outside the contract's
// [1,3] range) never reaches CaptureSession.SetCaptureScale unclamped. Before
// the F10 fix, dsf flowed straight from the wire frame into
// att.capture.SetCaptureScale(dsf) with no bound at all — CaptureScale()
// only floors values below 1, it never caps the top — so this test's
// assertion (CaptureScale() <= maxDeviceScaleFactor) fails against the
// pre-fix code, which would have recorded 50 verbatim.
//
// No live Chrome/CDP is needed: state.mgr.Live() has no live view registered
// for the session (no browser_attach happened), so
// LiveViewRegistry.SetViewport's own lookup returns applied=false, err=nil —
// the well-established "no live view bound yet" branch handleViewport's own
// doc comment describes. That branch is reached only AFTER the
// record-before-resize step this test is targeting, so it does not
// interfere with the assertion.
func TestBrowserWS_HandleViewport_ClampsOutOfRangeScale_BeforeRecording(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, ok := al.BrowserManagerForAgent(defaultAgent.ID)
	require.True(t, ok)

	relay := &fakeRelay{}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	wc := newTestBrowserWSConn()
	state := &browserConnState{mgr: mgr, sessionID: "sess-clamp"}
	// White-box: install the attachment directly (bypassing the full offer
	// handshake, irrelevant to what this test targets) so handleViewport's
	// direct att.capture.SetCaptureScale call path is exercised.
	state.webrtc = &webrtcAttachment{agentID: defaultAgent.ID, capture: cs}
	viewerID := "viewer-clamp"

	frame := generated.BrowserViewportFrame{
		Type:              string(generated.WsFrameTypeBrowserViewport),
		Width:             800,
		Height:            600,
		DeviceScaleFactor: f64ptr(50),
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleViewport(wc, state, viewerID, data)

	require.LessOrEqual(t, cs.CaptureScale(), maxDeviceScaleFactor,
		"an out-of-contract device_scale_factor must be clamped before it ever reaches SetCaptureScale")
	require.Equal(t, maxDeviceScaleFactor, cs.CaptureScale(),
		"50 clamped to the contract ceiling must land exactly on maxDeviceScaleFactor, not merely 'somewhere below it'")

	// F2's remember-path must observe the SAME clamped value, not the raw
	// wire value — otherwise a cold-start commit (applyColdStartRecapture)
	// could re-introduce the very out-of-range value this test just proved
	// gets clamped on the direct path.
	require.Equal(t, maxDeviceScaleFactor, state.pendingViewportScale(),
		"pendingCaptureScale must also carry the clamped value, not the raw out-of-range one")
}

// TestBrowserWS_HandleViewport_ClampsSubOneScale_BeforeRecording covers the
// low end of the same F10 fix: a device_scale_factor below 1 (e.g. a buggy
// client sending 0.1) must floor to 1, matching the contract's minimum and
// CaptureScale()'s own floor — asserted here at the RECORDING boundary
// itself, before CaptureScale()'s independent floor could paper over a bug
// in the new clamp.
func TestBrowserWS_HandleViewport_ClampsSubOneScale_BeforeRecording(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, ok := al.BrowserManagerForAgent(defaultAgent.ID)
	require.True(t, ok)

	relay := &fakeRelay{}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	wc := newTestBrowserWSConn()
	state := &browserConnState{mgr: mgr, sessionID: "sess-clamp-low"}
	state.webrtc = &webrtcAttachment{agentID: defaultAgent.ID, capture: cs}
	viewerID := "viewer-clamp-low"

	frame := generated.BrowserViewportFrame{
		Type:              string(generated.WsFrameTypeBrowserViewport),
		Width:             800,
		Height:            600,
		DeviceScaleFactor: f64ptr(0.1),
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleViewport(wc, state, viewerID, data)

	require.Equal(t, float64(1), state.pendingViewportScale(),
		"a sub-1 device_scale_factor must floor to 1 at the recording boundary")
}

// ---------------------------------------------------------------------------
// F2 — a scale remembered before an attachment existed must reach the
// capture session once the attachment commits.
// ---------------------------------------------------------------------------

// TestBrowserWS_ApplyColdStartRecapture_AppliesRememberedScale is the direct
// regression for F2. It reproduces the exact cold-open timeline without
// needing a live Chromium or the (OS-gated) WebRTC capability ladder:
//
//  1. A browser_viewport frame's scale is remembered on the connection via
//     rememberViewportScale — exactly what handleViewport now does
//     UNCONDITIONALLY, including (this is the bug) when no WebRTC attachment
//     exists yet to receive it directly.
//  2. applyColdStartRecapture runs — exactly what handleWebRTCOffer calls the
//     instant its own attachment commits.
//
// Confirmed non-vacuous by direct experiment: temporarily reverting
// applyColdStartRecapture's body to ONLY the pre-F2 geometry branch (no
// state.pendingViewportScale()/SetCaptureScale call at all — the exact code
// this function replaced) makes this assertion fail, since cs.CaptureScale()
// stays at its 1.0 default when nothing has ever called SetCaptureScale. The
// production code was restored immediately after.
func TestBrowserWS_ApplyColdStartRecapture_AppliesRememberedScale(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, ok := al.BrowserManagerForAgent(defaultAgent.ID)
	require.True(t, ok)

	relay := &fakeRelay{}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	state := &browserConnState{mgr: mgr, sessionID: "sess-cold-start"}
	require.Nil(t, state.webrtc, "precondition: no attachment exists yet — this is the exact cold-open window F2 covers")

	// Simulate handleViewport having already run BEFORE any WebRTC
	// attachment existed: it remembers the scale on the connection
	// unconditionally (browser_ws.go), even though peekWebRTCAttachment()
	// was nil at that moment and the direct SetCaptureScale call was
	// therefore skipped.
	state.rememberViewportScale(2.0)
	require.Equal(t, float64(1), cs.CaptureScale(),
		"precondition: the capture session must not yet have received the scale directly")

	// The attachment now commits (mirrors handleWebRTCOffer's
	// commitWebRTCAttachment succeeding) and calls the cold-start corrective
	// path.
	handler.applyColdStartRecapture(state, cs)

	require.Equal(t, 2.0, cs.CaptureScale(),
		"a device_scale_factor remembered before the attachment committed must still reach the capture session — "+
			"otherwise the Retina fix stays inert until the user manually resizes the panel")
}

// TestBrowserWS_ApplyColdStartRecapture_NoPendingScale_LeavesDefault is the
// negative case: when no browser_viewport frame has arrived at all
// (pendingCaptureScale's zero-value sentinel), applyColdStartRecapture must
// not call SetCaptureScale(0) — CaptureScale() would still floor that to 1
// today, but asserting the call is skipped entirely guards against a future
// change to CaptureScale()'s floor silently changing this method's
// behavior, and documents the sentinel's intent.
func TestBrowserWS_ApplyColdStartRecapture_NoPendingScale_LeavesDefault(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, ok := al.BrowserManagerForAgent(defaultAgent.ID)
	require.True(t, ok)

	relay := &fakeRelay{}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	state := &browserConnState{mgr: mgr, sessionID: "sess-cold-start-none"}
	require.Equal(t, float64(0), state.pendingViewportScale(), "precondition: sentinel zero value, nothing remembered")

	handler.applyColdStartRecapture(state, cs)

	require.Equal(t, float64(1), cs.CaptureScale(), "with nothing remembered, the capture session's scale stays at its default")
}

// ---------------------------------------------------------------------------
// F3 — capture_scale must be sent on every recapture, including a downgrade
// to 1, not only when scale > 1.
// ---------------------------------------------------------------------------

// TestCaptureIngest_Recapture_AlwaysSendsCaptureScale is the direct
// regression for F3. It drives the REAL captureIngestWSHandler (the same
// production `send` closure browser_webrtc.go's handleIngestConn installs)
// over a real loopback WebSocket, so the assertion exercises the actual
// wire-encoding path rather than a re-derived copy of its logic.
//
// The capture session's scale is first ratcheted up to 2, then explicitly
// dropped back to 1 (SetCaptureScale(1)) — F3's exact scenario: a Retina
// viewer moving to a non-Retina monitor, or a second viewer joining a shared
// per-agent CaptureSession at a lower DPR. Recapture() is then triggered and
// the resulting browser_capture_control frame is read directly off the
// wire.
//
// Confirmed non-vacuous by direct experiment: temporarily restoring the
// pre-F3 guard (`if scale := cs.CaptureScale(); scale > 1 { frame.CaptureScale
// = &scale }`) makes this test's require.NotNil(ctrl.CaptureScale) assertion
// fail — the field is omitted entirely when scale == 1, exactly reproducing
// the "downgrade to 1 never reaches the wire" bug. The production code was
// restored immediately after.
func TestCaptureIngest_Recapture_AlwaysSendsCaptureScale(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, ok := al.BrowserManagerForAgent(defaultAgent.ID)
	require.True(t, ok)

	relay := &fakeRelay{}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	handler.captures.set(defaultAgent.ID, cs)

	// Ratchet up, then explicitly back down — the exact sequence F3 says was
	// impossible to communicate to the encoder before this fix.
	cs.SetCaptureScale(2)
	cs.SetCaptureScale(1)
	require.Equal(t, float64(1), cs.CaptureScale())

	ingestHandler := newCaptureIngestWSHandler(al, handler.captures)
	t.Cleanup(ingestHandler.Wait)

	srv := httptest.NewServer(ingestHandler)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// Each attempt below dials its OWN fresh connection and reads from it AT
	// MOST once. This is deliberate, not incidental: gorilla/websocket
	// permanently poisons a *Conn after its FIRST read error (including a
	// plain deadline timeout) — "repeated read on failed websocket
	// connection" is a hard panic, not a retryable error — so a loop that
	// re-reads the SAME conn after a timeout is unsafe. A second hello with
	// the same token is explicitly supported and supersedes the prior one
	// (BindIngest's own doc comment: "the old conn" gets closed), so
	// redialing per attempt is also exactly what a reconnecting real encoder
	// would do, not a test-only workaround.
	//
	// The wait itself is for BindIngest to land server-side: requestControl
	// is a no-op send with no ingest connection bound yet, so a Recapture()
	// fired before the server finishes processing hello would be silently
	// lost — there is no exported signal for "ingest bound", so this retries
	// rather than assuming a fixed delay is always enough on a loaded CI box.
	var ctrl generated.BrowserCaptureControlFrame
	deadline := time.Now().Add(5 * time.Second)
	for {
		attemptCtrl, ok := tryCaptureIngestRecapture(t, wsURL, cs)
		if ok {
			ctrl = attemptCtrl
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no recapture control frame observed from any attempt within the deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}

	require.NotNil(t, ctrl.CaptureScale,
		"capture_scale must be present on every recapture frame, even when scale == 1 (a downgrade) — an "+
			"absent field is indistinguishable downstream from 'leave the scale unchanged'")
	require.Equal(t, float64(1), *ctrl.CaptureScale)
}

// tryCaptureIngestRecapture dials a fresh capture-ingest connection, sends
// the hello handshake, triggers exactly one Recapture(), and attempts
// exactly one read of the resulting browser_capture_control frame — see the
// caller's doc comment for why never more than one read per connection.
// Returns ok=false (not a test failure) on any timing miss, so the caller
// can retry with a brand new connection.
func tryCaptureIngestRecapture(t *testing.T, wsURL string, cs *browser.CaptureSession) (generated.BrowserCaptureControlFrame, bool) {
	t.Helper()
	var zero generated.BrowserCaptureControlFrame

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, httpResp, err := dialer.Dial(wsURL, nil)
	if httpResp != nil {
		httpResp.Body.Close()
	}
	require.NoError(t, err)
	defer conn.Close()

	hello := generated.BrowserCaptureHelloFrame{
		Type:       string(generated.WsFrameTypeBrowserCaptureHello),
		Token:      cs.TokenHex(),
		ExtVersion: "test-1.0.0",
	}
	helloData, err := json.Marshal(hello)
	require.NoError(t, err)
	if err := conn.WriteMessage(websocket.TextMessage, helloData); err != nil {
		return zero, false
	}

	// Brief settle time for the server goroutine to read hello and call
	// BindIngest before this attempt's Recapture() fires.
	time.Sleep(30 * time.Millisecond)
	cs.Recapture()

	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, raw, readErr := conn.ReadMessage()
	if readErr != nil {
		return zero, false
	}
	var ctrl generated.BrowserCaptureControlFrame
	if jsonErr := json.Unmarshal(raw, &ctrl); jsonErr != nil || ctrl.Action != "recapture" {
		return zero, false
	}
	return ctrl, true
}
