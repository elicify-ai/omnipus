package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// Regression coverage for the operator-reported failure of 2026-08-03
// (`0803 (1).mov`): the human opened the live panel, and the mouse, keyboard
// and omnibox were all dead for the entire session. The panel showed
// "Someone else is driving".
//
// Cause: input was gated on an EXCLUSIVE single-controller lock. Any other
// attached viewer — a second panel, a pop-out, or an automation session that
// never detached — took the wheel and left the real user's input silently
// discarded. An earlier partial fix (EnsureControlForInput, "the user drives
// by default") did not close it: it refused precisely when a *different,
// still-attached* viewer held the lock, which is exactly the reported case.
//
// The product model is: the live panel is a REAL BROWSER the human uses
// normally, and the agent can steer it too — both, concurrently. Input is
// never refused because someone else is present.
//
// These tests exist so that model cannot silently regress into a lock again.

// TestSharedControl_SecondViewerInputStillDispatches is the direct reproduction
// of the reported bug: viewerA (say, an automation session) holds control,
// viewerB (the human) acts, and viewerB's input must reach the page.
func TestSharedControl_SecondViewerInputStillDispatches(t *testing.T) {
	var dispatched int
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched++
		return nil
	})

	require.True(t, lv.takeControl("automation-viewer"),
		"precondition: some other viewer holds the lock, as in the reported session")

	// The human navigates via the omnibox.
	err := lv.dispatchInput("human-viewer", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"})
	require.NoError(t, err,
		"the human's navigation must not be refused because another viewer holds control — "+
			"this is the exact failure in the 2026-08-03 recording")
	require.Equal(t, 1, dispatched)
}

// TestSharedControl_BothViewersActConcurrently — the agent and the human are
// both expected to steer the same tab. Neither may lock the other out.
func TestSharedControl_BothViewersActConcurrently(t *testing.T) {
	var dispatched int
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched++
		return nil
	})

	// Interleaved, with no take/release dance between them.
	for i := 0; i < 3; i++ {
		require.NoError(t, lv.dispatchInput("agent-viewer", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"}))
		require.NoError(t, lv.dispatchInput("human-viewer", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"}))
	}
	require.Equal(t, 6, dispatched,
		"agent and human must both be able to act on the same tab, interleaved, with no arbitration")
}

// TestSharedControl_NoViewerHoldsControl — the common case after a reload or a
// cold open: nobody has "taken the wheel" yet. The first click must land, not
// be spent acquiring a lock.
func TestSharedControl_NoViewerHoldsControl(t *testing.T) {
	var dispatched int
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched++
		return nil
	})

	lv.mu.Lock()
	require.Empty(t, lv.controller, "precondition: nobody holds control")
	lv.mu.Unlock()

	require.NoError(t, lv.dispatchInput("human-viewer", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"}))
	require.Equal(t, 1, dispatched, "the very first action must land, not be consumed acquiring a lock")
}

// TestSharedControl_RateLimitStillApplies — removing the control gate must not
// remove the abuse guard. The rate limit is self-correcting (slow down and the
// next event lands), unlike the lock, which never self-corrected.
func TestSharedControl_RateLimitStillApplies(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}

	var lastErr error
	for i := 0; i < maxInputEventsPerSecond+5; i++ {
		lastErr = lv.dispatchInput("human-viewer", LiveInput{Kind: "mouse_move"})
	}
	require.Error(t, lastErr)
	require.Contains(t, lastErr.Error(), "rate limit",
		"the rate limit must survive the removal of the control gate")
	require.True(t, IsBenignLiveInputError(lastErr))
}

// TestSharedControl_ControllerRemainsPresentational — lv.controller is kept for
// the header chip and the control-sink broadcast. This pins that it is no
// longer consulted for authorization: taking control must not change whether
// anyone else's input dispatches.
func TestSharedControl_ControllerRemainsPresentational(t *testing.T) {
	var dispatched int
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched++
		return nil
	})

	require.NoError(t, lv.dispatchInput("viewerB", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"}))
	before := dispatched

	require.True(t, lv.takeControl("viewerA"))
	require.NoError(t, lv.dispatchInput("viewerB", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"}))
	require.Equal(t, before+1, dispatched,
		"whether some viewer holds lv.controller must have NO effect on another viewer's dispatch")

	lv.releaseControl("viewerA")
	require.NoError(t, lv.dispatchInput("viewerB", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"}))
	require.Equal(t, before+2, dispatched, "releasing control must likewise change nothing about dispatch")
}
