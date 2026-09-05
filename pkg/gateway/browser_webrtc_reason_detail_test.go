// browser_webrtc_reason_detail_test.go — UAT case 16 coverage: a live-browser
// video failure must tell the operator the REAL cause, not a generic
// sentence.
//
// The defect this file pins: handleWebRTCOffer classified every failure into
// the closed `reason` enum and sent only that. The actual error chain —
// "capture session: create encoder target: browser: timed out after 20s
// waiting for the browser to attach the tab (target may be unresponsive)" —
// went to slog and the audit record and nowhere the operator looks, so the
// panel said "The live browser reported an error starting video" and the one
// fact that would have told them what to do sat in a log file.
//
// That is exactly what ADR-061 deleted the JPEG fallback to prevent. The
// fallback hid failures because a 30fps <img> is indistinguishable from
// video; a visible error that names no cause hides the SAME information one
// level up. The browser_attach path already does this right
// (browser_ws.go's "browser_attach failed: %s" carries the full chain as
// free text), which is the inconsistency these tests close.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// startFailureCause is the VERBATIM error an operator hit on UAT. Used as the
// injected failure so the assertions below quote the real thing rather than a
// synthetic string that happens to survive.
const startFailureCause = "capture session: create encoder target: browser: timed out after 20s " +
	"waiting for the browser to attach the tab (target may be unresponsive)"

// newHandleWebRTCOfferWithFailingStart mirrors
// newHandleWebRTCOfferWithFakeCapture (browser_webrtc_fixwave_test.go) but
// seeds a capture session whose ENCODER STARTER fails, so cs.Start() returns
// startErr verbatim (CaptureSession.Start propagates its startEncoder error
// unwrapped) and handleWebRTCOffer takes its `startErr != nil` branch — the
// branch the UAT failure actually came down.
// captureCapableManager resolves the agent's BrowserManager and gives it the
// one thing CaptureVideoCapability still demands that a headless unit test
// otherwise lacks: an attached shared-Chrome coordinator. Constructing a
// coordinator launches nothing (NewBrowserCoordinator only builds the
// bookkeeping struct — Chrome starts on the first ensureStarted, which these
// tests never reach because the CaptureSession is pre-seeded), so this buys
// the gate-ladder pass without a real browser and without a platform skip.
func captureCapableManager(t *testing.T, al *agent.AgentLoop, agentID string) *browser.BrowserManager {
	t.Helper()
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), agentID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	bcfg, err := browser.DefaultConfig()
	require.NoError(t, err)
	coord := browser.NewBrowserCoordinator(t.TempDir(), bcfg)
	t.Cleanup(coord.Shutdown)
	mgr.AttachSharedChrome(coord, browserTestKey(t, "webrtc-reason-detail"))

	require.True(t, mgr.CaptureVideoCapability().Capable,
		"the gate ladder must reach the capture path for these tests to exercise the branch they target: %s",
		mgr.CaptureVideoCapability().Reason)
	return mgr
}

func newHandleWebRTCOfferWithFailingStart(
	t *testing.T,
	handler *BrowserWSHandler,
	al *agent.AgentLoop,
	agentID string,
	startErr error,
) webrtcStateFrameDecoder {
	t.Helper()
	mgr := captureCapableManager(t, al, agentID)

	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(
		nil, agentID, &fakeRelay{}, fakeEncoderStarter(&calls, startErr), nil)
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
		SessionId: "sess-start-detail",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-start-detail", "user-1", data, al.GetConfig(), 0)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "the encoder starter must actually have been invoked")
	return decodeWebRTCState(t, drainOneFrame(t, wc))
}

// TestHandleWebRTCOffer_StartFailure_CarriesRealCauseToTheOperator is the
// UAT-case-16 regression test. It fails on the pre-fix build: the frame
// carried reason="error" and nothing else, so the panel's only possible copy
// was the generic sentence.
func TestHandleWebRTCOffer_StartFailure_CarriesRealCauseToTheOperator(t *testing.T) {
	handler, al, _ := newFixWaveHandlerWithAudit(t, webrtcCapableGateMutate(t))
	t.Cleanup(handler.Wait)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	got := newHandleWebRTCOfferWithFailingStart(t, handler, al, defaultAgent.ID, errors.New(startFailureCause))

	require.False(t, got.Available, "a failed capture Start takes WebRTC out of service for this connection")
	require.Equal(t, "error", got.Reason)
	require.Contains(t, got.ReasonDetail,
		"timed out after 20s waiting for the browser to attach the tab",
		"the operator must receive the REAL cause on the wire — a viewer told only "+
			`"reported an error starting video" has nothing to act on, while the gateway log `+
			"beside it names the exact condition")
}

// TestHandleWebRTCOffer_ViewerOfferFailure_CarriesRealCauseToTheOperator is
// the same requirement one branch over: HandleViewerOffer failures were
// equally mute on the wire.
func TestHandleWebRTCOffer_ViewerOfferFailure_CarriesRealCauseToTheOperator(t *testing.T) {
	handler, al, _ := newFixWaveHandlerWithAudit(t, webrtcCapableGateMutate(t))
	t.Cleanup(handler.Wait)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	captureCapableManager(t, al, defaultAgent.ID)
	relay := &fakeRelay{viewerOfferErr: errors.New(
		"webrtc: viewer [v1]: set remote description failed: sdp: invalid syntax on line 4")}
	got := newHandleWebRTCOfferWithFakeCapture(t, handler, al, defaultAgent.ID, relay)

	require.Equal(t, "error", got.Reason)
	require.Contains(t, got.ReasonDetail, "set remote description failed",
		"the offer-failure branch must also name its cause, not only classify it")
}

// TestWebRTCReasonDetail_RedactsSecretsAndBoundsLength pins the safety
// envelope on the free text this change starts shipping to the browser. A
// reason naming a URL or a target id is fine and is the whole point; a
// credential is not, and a 20KB nested chromedp error would blow the frame's
// own maxLength.
func TestWebRTCReasonDetail_RedactsSecretsAndBoundsLength(t *testing.T) {
	t.Run("nil error yields no detail", func(t *testing.T) {
		assert.Empty(t, webrtcReasonDetail(nil))
	})

	t.Run("keeps the actionable cause verbatim", func(t *testing.T) {
		assert.Equal(t, startFailureCause, webrtcReasonDetail(errors.New(startFailureCause)))
	})

	t.Run("keeps a URL and a target id — they are what makes it actionable", func(t *testing.T) {
		got := webrtcReasonDetail(errors.New(
			"capture session: navigate chrome-extension://abcdefg/encoder.html target 9F2A: no such target"))
		assert.Contains(t, got, "chrome-extension://abcdefg/encoder.html")
		assert.Contains(t, got, "9F2A")
	})

	t.Run("redacts a labelled credential", func(t *testing.T) {
		got := webrtcReasonDetail(errors.New("capture session: mint token: token=s3cr3tvalue rejected"))
		assert.NotContains(t, got, "s3cr3tvalue")
		assert.Contains(t, got, "capture session: mint token")
	})

	t.Run("redacts a bare long hex run (the capture token's shape)", func(t *testing.T) {
		tok := "4f3c9a1b2d8e7f60a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718"
		got := webrtcReasonDetail(errors.New("capture session: hello rejected for " + tok))
		assert.NotContains(t, got, tok)
		assert.Contains(t, got, "hello rejected")
	})

	t.Run("redacts a bearer token", func(t *testing.T) {
		got := webrtcReasonDetail(errors.New("ingest dial failed: Bearer abcDEF123ghiJKL456"))
		assert.NotContains(t, got, "abcDEF123ghiJKL456")
	})

	t.Run("collapses newlines so the panel renders one sentence", func(t *testing.T) {
		got := webrtcReasonDetail(errors.New("capture session:\n\tcreate encoder target:\r\n  timed out"))
		assert.Equal(t, "capture session: create encoder target: timed out", got)
	})

	t.Run("bounds the length to fit the wire field", func(t *testing.T) {
		got := webrtcReasonDetail(errors.New("x" + strings.Repeat("y", 4000)))
		assert.LessOrEqual(t, len(got), webrtcReasonDetailMax)
		assert.True(t, strings.HasSuffix(got, "…"), "a truncated detail must say so")
	})
}
