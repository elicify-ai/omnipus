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
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// browserViewportContractScaleRegexp matches the device_scale_factor
// property block in contracts/components/schemas/BrowserViewportFrame.yaml
// and captures its declared minimum/maximum. Anchored on the exact
// "type: number" line so it cannot accidentally match a different numeric
// property that happens to be named similarly.
var browserViewportContractScaleRegexp = regexp.MustCompile(
	`(?m)^  device_scale_factor:\n    type: number\n    minimum: (\d+(?:\.\d+)?)\n    maximum: (\d+(?:\.\d+)?)`,
)

// browserViewportContractScaleRange reads device_scale_factor's declared
// [minimum, maximum] straight out of the wire contract (Constraint #8's
// single source of truth) rather than from any Go constant, so tests using
// it cannot be made to pass by moving the Go-side constant they are meant to
// police. See this file's oracle-independence note on the F10 tests below:
// the original defect was exactly the opposite of this — the expected value
// WAS the constant under test.
func browserViewportContractScaleRange(t *testing.T) (rangeMin, rangeMax float64) {
	t.Helper()
	path := filepath.Join("..", "..", "contracts", "components", "schemas", "BrowserViewportFrame.yaml")
	raw, err := os.ReadFile(path) // gosec rationale (out of gosec scope; kept as documentation): fixed, repo-relative contract path
	require.NoError(t, err, "the contract must be readable — it is the authority on this range")

	m := browserViewportContractScaleRegexp.FindSubmatch(raw)
	require.NotNil(t, m,
		"BrowserViewportFrame.device_scale_factor must still declare minimum/maximum for this test to mean anything")

	minVal, err := strconv.ParseFloat(string(m[1]), 64)
	require.NoError(t, err)
	maxVal, err := strconv.ParseFloat(string(m[2]), 64)
	require.NoError(t, err)
	return minVal, maxVal
}

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
// declared range) never reaches CaptureSession.SetCaptureScale unclamped.
// Before the F10 fix, dsf flowed straight from the wire frame into
// att.capture.SetCaptureScale(dsf) with no bound at all — CaptureScale()
// only floors values below 1, it never caps the top — so this test's
// assertion (CaptureScale() <= contract max) fails against the pre-fix code,
// which would have recorded 50 verbatim.
//
// Oracle independence: the expected ceiling is read from
// contracts/components/schemas/BrowserViewportFrame.yaml via
// browserViewportContractScaleRange, NOT from the Go constant
// maxDeviceScaleFactor. An earlier version of this test asserted
// `CaptureScale() == maxDeviceScaleFactor` — the constant under test WAS the
// oracle, so bumping maxDeviceScaleFactor from 3.0 to 50.0 moved the
// goalposts with it and every assertion here stayed green. Proven by direct
// experiment: with that change applied, this test (as rewritten) still
// fails, because 50 no longer equals the contract-derived ceiling of 3.
//
// No live Chrome/CDP is needed: state.mgr.Live() has no live view registered
// for the session (no browser_attach happened), so
// LiveViewRegistry.SetViewport's own lookup returns applied=false, err=nil —
// the well-established "no live view bound yet" branch handleViewport's own
// doc comment describes. That branch is reached only AFTER the
// record-before-resize step this test is targeting, so it does not
// interfere with the assertion.
func TestBrowserWS_HandleViewport_ClampsOutOfRangeScale_BeforeRecording(t *testing.T) {
	_, contractMax := browserViewportContractScaleRange(t)

	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	relay := &fakeRelay{}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	wc := newTestBrowserWSConn()
	state := &browserConnState{mgr: mgr, sessionID: "sess-clamp", panelSessionID: mgr.PanelTabSetID("sess-clamp")}
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

	require.LessOrEqual(t, cs.CaptureScale(), contractMax,
		"an out-of-contract device_scale_factor must be clamped before it ever reaches SetCaptureScale")
	require.Equal(t, contractMax, cs.CaptureScale(),
		"50 clamped must land exactly on the contract's declared maximum, not merely 'somewhere below it'")

	// F2's remember-path must observe the SAME clamped value, not the raw
	// wire value — otherwise a cold-start commit (applyColdStartRecapture)
	// could re-introduce the very out-of-range value this test just proved
	// gets clamped on the direct path.
	require.Equal(t, contractMax, state.pendingViewportScale(),
		"pendingCaptureScale must also carry the clamped value, not the raw out-of-range one")
}

// TestBrowserWS_HandleViewport_ClampsSubOneScale_BeforeRecording covers the
// low end of the same F10 fix: a device_scale_factor below the contract's
// declared minimum (e.g. a buggy client sending 0.1) must floor to that
// minimum — asserted here at the RECORDING boundary itself, before
// CaptureScale()'s own independent floor could paper over a bug in the new
// clamp.
//
// Oracle independence: the floor is read from the contract
// (browserViewportContractScaleRange), not hardcoded as the literal `1` that
// happens to match today's handleViewport implementation.
func TestBrowserWS_HandleViewport_ClampsSubOneScale_BeforeRecording(t *testing.T) {
	contractMin, _ := browserViewportContractScaleRange(t)

	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	relay := &fakeRelay{}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	wc := newTestBrowserWSConn()
	state := &browserConnState{mgr: mgr, sessionID: "sess-clamp-low", panelSessionID: mgr.PanelTabSetID("sess-clamp-low")}
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

	require.Equal(t, contractMin, state.pendingViewportScale(),
		"a sub-minimum device_scale_factor must floor to the contract's declared minimum at the recording boundary")
}

// TestBrowserWS_HandleViewport_ScaleClampBoundaries is table-driven boundary
// and differentiation coverage for the same F10 clamp. It exists alongside
// the two tests above (rather than replacing them) to add cases those two
// don't reach:
//
//   - a value sitting exactly ON each contract edge must pass through
//     UNCHANGED — proves the clamp isn't simply forcing everything to one
//     constant, which the over/under-range tests alone can't distinguish
//     from a correct clamp (a broken "always return 3" implementation would
//     also pass the over-range test).
//   - a value just past each edge must still be clamped — the boundary+1 /
//     boundary-1 cases the base tests skip.
//   - a valid mid-range value (2.0) must also pass through unchanged, and be
//     DIFFERENT from the min/max boundary values — this is the
//     differentiation case: it rules out a hardcoded-response
//     implementation that always emits the same scale regardless of input.
//
// Every expected value is read from browserViewportContractScaleRange (the
// contract), never from maxDeviceScaleFactor — same oracle-independence
// rationale as the two tests above.
func TestBrowserWS_HandleViewport_ScaleClampBoundaries(t *testing.T) {
	contractMin, contractMax := browserViewportContractScaleRange(t)

	cases := []struct {
		name     string
		input    float64
		expected float64
	}{
		{"at_max_boundary_unchanged", contractMax, contractMax},
		{"just_above_max_clamped", contractMax + 0.5, contractMax},
		{"at_min_boundary_unchanged", contractMin, contractMin},
		{"just_below_min_floored", contractMin - 0.5, contractMin},
		{"mid_range_valid_unchanged", 2.0, 2.0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
				cfg.Tools.Browser.WebRTCEnabled = true
			})
			t.Cleanup(handler.Wait)

			defaultAgent := al.GetRegistry().GetDefaultAgent()
			require.NotNil(t, defaultAgent)
			mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
			require.Equal(t, agent.BrowserResolveOK, outcome)

			relay := &fakeRelay{}
			var calls int32
			cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
			require.NoError(t, err)

			wc := newTestBrowserWSConn()
			state := &browserConnState{
				mgr:            mgr,
				sessionID:      "sess-clamp-boundary-" + tc.name,
				panelSessionID: mgr.PanelTabSetID("sess-clamp-boundary-" + tc.name),
			}
			state.webrtc = &webrtcAttachment{agentID: defaultAgent.ID, capture: cs}
			viewerID := "viewer-clamp-boundary-" + tc.name

			frame := generated.BrowserViewportFrame{
				Type:              string(generated.WsFrameTypeBrowserViewport),
				Width:             800,
				Height:            600,
				DeviceScaleFactor: f64ptr(tc.input),
			}
			data, err := json.Marshal(frame)
			require.NoError(t, err)

			handler.handleViewport(wc, state, viewerID, data)

			require.Equal(t, tc.expected, cs.CaptureScale(),
				"input %v must clamp to %v per the contract's declared [min,max] range", tc.input, tc.expected)
			require.Equal(t, tc.expected, state.pendingViewportScale(),
				"pendingViewportScale must carry the same clamped value as the capture session")
		})
	}
}

// TestMaxDeviceScaleFactor_MatchesContractMaximum is the drift guard: the Go
// constant maxDeviceScaleFactor (browser_ws.go) and
// BrowserViewportFrame.device_scale_factor's contract `maximum` are
// documented (maxDeviceScaleFactor's own doc comment) as two values that
// "must stay in lockstep." Nothing before this test enforced that — a
// developer could edit either one alone and every other test in this file
// would still pass, because they all derive their expectations from
// whichever side of the pair they read. This test reads BOTH independently
// (the contract via browserViewportContractScaleRange, the constant
// directly) and fails the instant they disagree, regardless of what either
// value actually is.
func TestMaxDeviceScaleFactor_MatchesContractMaximum(t *testing.T) {
	_, contractMax := browserViewportContractScaleRange(t)

	require.Equal(t, contractMax, maxDeviceScaleFactor,
		"maxDeviceScaleFactor (browser_ws.go) and BrowserViewportFrame.device_scale_factor's contract maximum "+
			"must stay in lockstep — see maxDeviceScaleFactor's doc comment")
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
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	relay := &fakeRelay{}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	state := &browserConnState{mgr: mgr, sessionID: "sess-cold-start", panelSessionID: mgr.PanelTabSetID("sess-cold-start")}
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
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	relay := &fakeRelay{}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	state := &browserConnState{
		mgr:            mgr,
		sessionID:      "sess-cold-start-none",
		panelSessionID: mgr.PanelTabSetID("sess-cold-start-none"),
	}
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
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

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
