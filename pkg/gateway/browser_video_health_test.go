package gateway

// Gateway-side coverage for the browser_video_health fan-out (issue #674).
//
// The gap this closes: the gateway learns the capture's video feed died the
// instant Pion reports a terminal PeerConnection state, but nothing carried
// that to the panel — the SPA only found out by exhausting its own 45s
// first-frame deadline. These tests hold the fan-out to the contract the
// schema declares, and to ADR-061's rule that a video failure must be
// VISIBLE and SPECIFIC rather than silently degraded.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// videoHealthFrameDecoder mirrors the wire shape for assertion purposes only.
// Not a wire-format type: the frame is BUILT from generated.BrowserVideoHealthFrame
// in browser_video_health.go — this decodes what actually went out, which is
// the point (a test that re-used the generated struct could not catch a field
// the encoder dropped).
type videoHealthFrameDecoder struct { // not-wire-format: test-only decoder for assertions.
	Type        string  `json:"type"`
	SessionId   string  `json:"session_id"`
	State       string  `json:"state"`
	Attempt     *int    `json:"attempt"`
	MaxAttempts *int    `json:"max_attempts"`
	Detail      *string `json:"detail"`
}

func TestOnVideoHealth_ReachesEveryAttachedViewer(t *testing.T) {
	f := newVideoPublicationFixture(t)
	f.cs.AddViewer("viewer-b")
	wcB, stateB := newTabActionTestFixtures(t)
	stateB.sessionID = "panel-b"
	stateB.panelSessionID = stateB.mgr.PanelTabSetID("panel-b")
	t.Cleanup(func() { stateB.clearAttachment() })
	originB := stateB.commandAttachment()
	epochB := stateB.beginWebRTCOffer()
	require.True(t, stateB.commitWebRTCAttachment(epochB, &webrtcAttachment{capture: f.cs}))
	require.True(t, f.h.registerWebRTCViewerConnForCapture(stateB, epochB, originB.ctx, "viewer-b", wcB, "panel-b", f.cs))
	event := f.event
	event.ViewerIDs = []string{"viewer", "viewer-b"}
	f.h.onVideoHealth(event)
	for _, tc := range []struct {
		wc      *browserWSConn
		session string
	}{{f.wc, "tab-action-test-session"}, {wcB, "panel-b"}} {
		pending, ok := tc.wc.takeLatestFrame()
		require.True(t, ok)
		require.True(t, tc.wc.canSendFrame(pending))
		var got videoHealthFrameDecoder
		require.NoError(t, json.Unmarshal(pending.data, &got))
		require.Equal(t, string(generated.WsFrameTypeBrowserVideoHealth), got.Type)
		require.Equal(t, "recovering", got.State)
		require.Equal(t, tc.session, got.SessionId)
		require.NotNil(t, got.Attempt)
		require.Equal(t, 1, *got.Attempt)
		require.NotNil(t, got.MaxAttempts)
		require.Equal(t, 3, *got.MaxAttempts)
	}
}

// Detail is trusted server text but still must be redacted by the wire mapper.
// Fan-out authorization and delivery use real session claims in adjacent tests.
func TestOnVideoHealth_RedactsTheDetailItForwards(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	raw, err := json.Marshal(videoHealthFrame(browser.VideoHealthEvent{
		State:       browser.VideoHealthUnrecoverable,
		Attempt:     3,
		MaxAttempts: 3,
		Detail:      "capture token=" + secret + " never authenticated",
	}))
	require.NoError(t, err)
	var got videoHealthFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "unrecoverable", got.State)
	require.NotNil(t, got.Detail)
	require.NotContains(t, *got.Detail, secret)
	require.Contains(t, *got.Detail, "[redacted]")
}

// Optional-field serialization is tested directly at its wire boundary;
// capture authorization is covered by the real session fan-out fixtures.
func TestOnVideoHealth_DetailAndAttemptOmittedWhenAbsent(t *testing.T) {
	raw, err := json.Marshal(videoHealthFrame(browser.VideoHealthEvent{State: browser.VideoHealthRecovered}))
	require.NoError(t, err)
	var got videoHealthFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "recovered", got.State)
	require.Nil(t, got.Attempt)
	require.Nil(t, got.MaxAttempts)
	require.Nil(t, got.Detail)
	require.False(t, strings.Contains(string(raw), "null"))
}

func TestOnVideoHealth_UnknownViewerIsSkippedNotFatal(t *testing.T) {
	f := newVideoPublicationFixture(t)
	event := f.event
	event.ViewerIDs = []string{"viewer-gone", "viewer"}
	f.h.onVideoHealth(event)
	pending, ok := f.wc.takeLatestFrame()
	require.True(t, ok)
	var got videoHealthFrameDecoder
	require.NoError(t, json.Unmarshal(pending.data, &got))
	require.Equal(t, "recovering", got.State)
	require.NotNil(t, got.Attempt)
	require.Equal(t, 1, *got.Attempt)
}

// TestHandleAttach_RegistersTheVideoHealthObserver is a SOURCE guard, and its
// limits are stated plainly: it proves the registration line still exists in
// handleAttach, not that a live attach executes it.
//
// It is a source guard because the behavioural alternative is not reachable in
// a unit test — handleAttach registers the observer only AFTER
// mgr.Live().Attach() succeeds, and that call needs a real Chrome. (Refusing
// to register on a failed attach is correct: no attachment means no viewers to
// notify.)
//
// It earns its place anyway. The failure it catches is deletion — the entire
// #674 signal chain is inert if this one line goes, and everything downstream
// of it would still pass: the manager wiring test, the CaptureSession recovery
// tests, the fan-out tests, and every SPA test. That is precisely the
// "feature present but never connected" shape this project has shipped before
// (the inert start page, the default-agent singleton nothing wrote), which is
// why pkg/agent/window_trim_test.go uses the same technique.
func TestHandleAttach_RegistersTheVideoHealthObserver(t *testing.T) {
	src, err := os.ReadFile("browser_ws.go")
	require.NoError(t, err)

	body := funcBodyByName(t, string(src), "func (h *BrowserWSHandler) handleAttach(")
	require.Contains(t, body, "mgr.SetVideoHealthObserver(h.onVideoHealth)",
		"handleAttach must register the video-health observer on the manager — without it the gateway "+
			"still knows the instant the capture's video dies and still runs its bounded recovery, but "+
			"nothing ever tells the panel, and the user is back to waiting out a 45s timeout (#674)")
}

// funcBodyByName returns the source text from the line starting with decl up
// to the next top-level `\n}` — enough to scope the assertion above to
// handleAttach rather than to the whole file.
func funcBodyByName(t *testing.T, src, decl string) string {
	t.Helper()
	start := strings.Index(src, decl)
	require.GreaterOrEqual(t, start, 0, "declaration %q not found — has it been renamed?", decl)
	rest := src[start:]
	end := strings.Index(rest, "\n}\n")
	require.GreaterOrEqual(t, end, 0, "could not find the end of %q", decl)
	return rest[:end]
}
