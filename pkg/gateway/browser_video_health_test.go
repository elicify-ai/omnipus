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
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	wcA := newTestBrowserWSConn()
	wcB := newTestBrowserWSConn()
	handler.registerWebRTCViewerConn("viewer-a", wcA, "sess-a")
	handler.registerWebRTCViewerConn("viewer-b", wcB, "sess-b")
	t.Cleanup(func() {
		handler.unregisterWebRTCViewerConn("viewer-a")
		handler.unregisterWebRTCViewerConn("viewer-b")
	})

	handler.onVideoHealth(browser.VideoHealthEvent{
		AgentID:     "mia",
		ViewerIDs:   []string{"viewer-a", "viewer-b"},
		State:       browser.VideoHealthLost,
		Attempt:     1,
		MaxAttempts: 3,
		Detail:      "the live browser's video feed stopped — reconnecting automatically",
	})

	for _, tc := range []struct {
		wc      *browserWSConn
		session string
	}{{wcA, "sess-a"}, {wcB, "sess-b"}} {
		var f videoHealthFrameDecoder
		require.NoError(t, json.Unmarshal(drainOneFrame(t, tc.wc), &f))
		require.Equal(t, string(generated.WsFrameTypeBrowserVideoHealth), f.Type,
			"the capture feeds every viewer, so every viewer must be told when it dies")
		require.Equal(t, "lost", f.State)
		require.Equal(t, tc.session, f.SessionId,
			"each viewer must get its OWN session id for correlation, not another viewer's")
		require.NotNil(t, f.Attempt)
		require.Equal(t, 1, *f.Attempt)
		require.NotNil(t, f.MaxAttempts)
		require.Equal(t, 3, *f.MaxAttempts,
			"the panel must be able to say the recovery is bounded, not show an endless spinner")
		require.NotNil(t, f.Detail)
		require.Contains(t, *f.Detail, "video feed stopped")
	}
}

// TestOnVideoHealth_RedactsTheDetailItForwards — the detail is free text
// assembled server-side and could in principle carry a capture token. It goes
// through the SAME redactor browser_webrtc_state.reason_detail uses; a second,
// parallel implementation here would be one more place for a secret to leak
// into a browser.
func TestOnVideoHealth_RedactsTheDetailItForwards(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	handler.registerWebRTCViewerConn("viewer-redact", wc, "sess-redact")
	t.Cleanup(func() { handler.unregisterWebRTCViewerConn("viewer-redact") })

	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	handler.onVideoHealth(browser.VideoHealthEvent{
		AgentID:     "mia",
		ViewerIDs:   []string{"viewer-redact"},
		State:       browser.VideoHealthUnrecoverable,
		Attempt:     3,
		MaxAttempts: 3,
		Detail:      "capture token=" + secret + " never authenticated",
	})

	var f videoHealthFrameDecoder
	require.NoError(t, json.Unmarshal(drainOneFrame(t, wc), &f))
	require.Equal(t, "unrecoverable", f.State)
	require.NotNil(t, f.Detail)
	require.NotContains(t, *f.Detail, secret, "the capture token must never reach a browser")
	require.Contains(t, *f.Detail, "[redacted]")
}

// TestOnVideoHealth_DetailAndAttemptOmittedWhenAbsent — the schema marks
// attempt/max_attempts/detail optional, and a `recovered` transition genuinely
// has none of them. Emitting zeros would make the panel render "attempt 0 of
// 0", which is worse than saying nothing.
func TestOnVideoHealth_DetailAndAttemptOmittedWhenAbsent(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	handler.registerWebRTCViewerConn("viewer-ok", wc, "sess-ok")
	t.Cleanup(func() { handler.unregisterWebRTCViewerConn("viewer-ok") })

	handler.onVideoHealth(browser.VideoHealthEvent{
		AgentID:   "mia",
		ViewerIDs: []string{"viewer-ok"},
		State:     browser.VideoHealthRecovered,
	})

	raw := drainOneFrame(t, wc)
	var f videoHealthFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, "recovered", f.State)
	require.Nil(t, f.Attempt)
	require.Nil(t, f.MaxAttempts)
	require.Nil(t, f.Detail)
	// And they are genuinely absent on the wire, not sent as JSON nulls: the
	// contract is additionalProperties:false with optional fields, and the
	// SPA's zod schema rejects a null where a number is declared.
	require.False(t, strings.Contains(string(raw), "null"),
		"optional fields must be omitted, not serialised as null — the SPA drops a frame that fails schema validation")
}

// TestOnVideoHealth_UnknownViewerIsSkippedNotFatal — a viewer can detach
// between the snapshot taken inside the capture session and this fan-out. That
// is ordinary, and must not stop the remaining viewers being told.
func TestOnVideoHealth_UnknownViewerIsSkippedNotFatal(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	handler.registerWebRTCViewerConn("viewer-present", wc, "sess-present")
	t.Cleanup(func() { handler.unregisterWebRTCViewerConn("viewer-present") })

	handler.onVideoHealth(browser.VideoHealthEvent{
		AgentID:     "mia",
		ViewerIDs:   []string{"viewer-gone", "viewer-present"},
		State:       browser.VideoHealthRecovering,
		Attempt:     2,
		MaxAttempts: 3,
	})

	var f videoHealthFrameDecoder
	require.NoError(t, json.Unmarshal(drainOneFrame(t, wc), &f))
	require.Equal(t, "recovering", f.State)
	require.NotNil(t, f.Attempt)
	require.Equal(t, 2, *f.Attempt)
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
