// live_idle_test.go: tests for idle sweeper and stand-down of live views nobody is watching (ADR-085 FR-026a/FR-029/FR-031a/FR-041/FR-047/FR-052).

package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from live.go tests 2026-09-15 ---

func TestReleaseStoodDownForViewerPreservesOtherHolder(t *testing.T) {
	for _, tc := range []struct {
		name, holder, actor string
		allowed             bool
	}{
		{"owner", "owner", "owner", true},
		{"other viewer", "owner", "other", false},
		{"unowned latch", "", "other", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { return nil })
			lv.mgr.live.views[lv.sessionID] = lv
			lv.viewers["owner"] = struct{}{}
			lv.viewers["other"] = struct{}{}
			lv.controller = tc.holder
			lv.standDownUntilPrompt = true
			lv.handoverPending = true
			releases := make(chan struct{}, 1)
			lv.mgr.Live().SetReleaseNotifySink(lv.sessionID, "owner", func() { releases <- struct{}{} })
			require.Equal(t, tc.allowed, lv.mgr.Live().ReleaseStoodDownForViewer(lv.sessionID, tc.actor))
			require.Equal(t, !tc.allowed, lv.mgr.Live().IsStoodDown(lv.sessionID))
			lv.mu.Lock()
			holder, pending := lv.controller, lv.handoverPending
			lv.mu.Unlock()
			if tc.allowed {
				require.Empty(t, holder)
				require.False(t, pending)
			} else {
				require.Equal(t, "owner", holder)
				require.True(t, pending)
			}
			if tc.allowed && tc.holder != "" {
				select {
				case <-releases:
				case <-time.After(time.Second):
					t.Fatal("owner did not receive release")
				}
			} else {
				select {
				case <-releases:
					t.Fatal("refused or unowned release notified owner")
				case <-time.After(20 * time.Millisecond):
				}
			}
		})
	}
}

// TestSetHandoverPending_RaisesTheWaitingSurfaceNotice is BROWSER-FR-041/
// FR-048/FR-048a. browser_handover's tool body calls SetHandoverPending and
// DISCARDS its return value (tools_handover.go), so if the notice is not
// raised from inside this method it is not raised at all — which is exactly
// the state SF-2 found: the agent stands down for a sign-in and the operator
// is shown nothing.
//
// BDD: Given an agent hands the browser to the operator with a reason,
// When the handover-pending state is set, Then exactly one waiting-surface
// notice is raised, carrying that reason and the tab set it belongs to.
func TestSetHandoverPending_RaisesTheWaitingSurfaceNotice(t *testing.T) {
	drain := captureStandDownNotices(t)
	mgr := newHandoverTestManager(t, true)

	transitioned, holdStart := mgr.Live().SetHandoverPending(testSessionID, "please sign in to the bank")
	require.True(t, transitioned, "test setup: a first handover on a free tab set must be a transition")
	require.NotZero(t, holdStart, "test setup: a transition must mint the FR-044 hold-start stamp")

	got := drain(1)
	require.Len(t, got, 1, "exactly one notice per unbroken hold (FR-044)")
	n := got[0]
	assert.Equal(t, StandDownByHandover, n.Producer, "the producer must say which side raised this")
	assert.Equal(t, "please sign in to the bank", n.Reason,
		"FR-048a: the model-authored reason must reach the operator-facing surface")
	assert.Equal(t, testSessionID, n.TabSetID, "the notice must name the tab set that stood down")
	assert.Equal(t, testTranscriptSessionID, n.OwnerSessionID,
		"a session-owned tab set must resolve back to the chat it belongs to, or the gateway has "+
			"nowhere to put the line when no panel is attached")
	assert.Equal(t, holdStart, n.HoldStartedAtUnixNano,
		"the notice must carry the SAME hold-start the caller saw — the FR-044 message id is derived "+
			"from it, and a different value would mean live and replay disagree")
}

// TestSetHandoverPending_RaisesExactlyOneNoticePerUnbrokenHold is FR-044's
// idempotency, from the production path: a second handover call with no
// intervening release must not produce a second visible line.
func TestSetHandoverPending_RaisesExactlyOneNoticePerUnbrokenHold(t *testing.T) {
	drain := captureStandDownNotices(t)
	mgr := newHandoverTestManager(t, true)

	mgr.Live().SetHandoverPending(testSessionID, "first")
	second, _ := mgr.Live().SetHandoverPending(testSessionID, "second")
	require.False(t, second, "test setup: a repeat handover within one hold is not a transition")

	got := drain(1)
	assert.Len(t, got, 1, "a repeat handover within one unbroken hold must raise no second line (FR-044)")
	assert.Equal(t, "first", got[0].Reason, "the one line raised must be the FIRST hold's")
}

// TestReleaseAllStoodDown_ClearsEveryStoodDownTabSet is BROWSER-FR-029/FR-050's
// mechanism: the prompt release must reach every stood-down tab set the
// workspace's browser holds, not just the one the panel is on, and must report
// only the sets it actually cleared so the caller's audit trail carries no
// phantom releases.
func TestReleaseAllStoodDown_ClearsEveryStoodDownTabSet(t *testing.T) {
	mgr := newHandoverTestManager(t, true)

	// Two DIFFERENT tab sets in the same browser, stood down two different
	// ways — a human hold on the operator's set, an agent handover on the
	// chat's own. FR-050 exists because clearing only one of these leaves the
	// other deferring forever.
	require.True(t, mgr.Live().TakeControl(testOperatorSessionID, "human-viewer"))
	mgr.Live().SetHandoverPending(testSessionID, "sign in please")
	require.True(t, mgr.Live().IsStoodDown(testOperatorSessionID))
	require.True(t, mgr.Live().IsStoodDown(testSessionID))

	released := mgr.Live().ReleaseAllStoodDown()

	byID := map[string]string{}
	for _, r := range released {
		byID[r.SessionID] = r.FormerHolder
	}
	assert.Len(t, released, 2, "both stood-down tab sets must be released; got %+v", released)
	assert.Equal(t, "human-viewer", byID[testOperatorSessionID],
		"the released human hold must be attributed to its holder")
	assert.Contains(t, byID, testSessionID,
		"the handover-pending set must be released too — it is the case FR-050 is written about")
	assert.Empty(t, byID[testSessionID],
		"a handover-only stand-down has no interactive holder to report")

	assert.False(t, mgr.Live().IsStoodDown(testOperatorSessionID),
		"the gate must stop deferring on the operator's set after the release")
	assert.False(t, mgr.Live().IsStoodDown(testSessionID),
		"the gate must stop deferring on the chat's own set after the release")

	assert.Empty(t, mgr.Live().ReleaseAllStoodDown(),
		"a second release must report nothing — a caller that audited a phantom release would be "+
			"writing a trail that says the wheel was handed back twice")
}

// TestOwnerSessionIDFromTabSetKey pins the inverse of sessionKey, which is how
// a notice raised deep inside a LiveView names the chat it belongs to.
func TestOwnerSessionIDFromTabSetKey(t *testing.T) {
	assert.Equal(t, testTranscriptSessionID, ownerSessionIDFromTabSetKey(testSessionID),
		"a session-owned tab set must yield its transcript session id")
	assert.Empty(t, ownerSessionIDFromTabSetKey(testOperatorSessionID),
		"the operator's workspace-owned set belongs to no chat and must yield nothing — inventing a "+
			"session id here would file the notice in an unrelated thread")
	assert.Empty(t, ownerSessionIDFromTabSetKey("not-a-tab-set-key"))
	assert.Empty(t, ownerSessionIDFromTabSetKey(""))
}

// TestReleaseAllStoodDown_TheAgentsToolGateStopsDeferring is BROWSER-FR-029 +
// FR-050 end to end across BOTH stand-down producers at once, because the
// prompt release must clear both or the agent is left half locked out.
//
// BDD (D-G): Given a human holds the operator's tabs AND the agent has handed
// the chat's own tab set over for a sign-in, When the operator sends a new
// prompt (there is no hand-back button — the prompt IS the hand-back), Then
// every browser tool stops deferring on both tab sets.
func TestReleaseAllStoodDown_TheAgentsToolGateStopsDeferring(t *testing.T) {
	mgr := newHandoverTestManager(t, true)
	ctx := context.Background()

	// Precondition: with nothing held, the gate lets the agent through. Without
	// this, a test that only ever observes nil after the release could pass on
	// a gate that never defers at all.
	require.Nil(t, controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil),
		"test setup: an unheld browser must not defer")
	require.Nil(t, controlledResult(ctx, mgr, testKey, TabOwnerWorkspace(), "browser_click", nil),
		"test setup: an unheld operator tab set must not defer")

	// The two stand-downs, raised through the production calls: an agent
	// handover on the chat's own set, a human take on the operator's.
	transitioned, _ := mgr.Live().SetHandoverPending(testSessionID, "please sign in to the bank")
	require.True(t, transitioned, "test setup: the handover must be a real transition")
	require.True(t, mgr.Live().TakeControl(testOperatorSessionID, "human-viewer"),
		"test setup: the human take must succeed")

	deferredOwn := controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil)
	require.NotNil(t, deferredOwn,
		"a handed-over tab set must defer the agent's tools — otherwise the agent drives over the "+
			"operator mid-sign-in")
	require.False(t, deferredOwn.IsError, "a deferral is turn-coordination, not a tool failure")
	require.NotNil(t, deferredOwn.Deferred, "FR-012a: the deferral must be carried structurally")
	require.NotNil(t, controlledResult(ctx, mgr, testKey, TabOwnerWorkspace(), "browser_click", nil),
		"a human-held operator tab set must defer the agent's tools")

	// The operator's next prompt. This is the call ReleaseBrowserWheelForPrompt
	// makes on every browser the workspace owns.
	released := mgr.Live().ReleaseAllStoodDown()
	require.Len(t, released, 2,
		"FR-050: the prompt release must reach BOTH stood-down tab sets, not just the panel's; got %+v",
		released)

	assert.Nil(t, controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil),
		"FR-029: after the operator's prompt the agent must be able to drive its own tab set again — "+
			"a release that cleared only lv.controller would leave this deferring for the rest of the "+
			"process's life, with nothing in the UI to say so")
	assert.Nil(t, controlledResult(ctx, mgr, testKey, TabOwnerWorkspace(), "browser_click", nil),
		"FR-050: the operator's own tab set must be drivable again too")
}

// TestReleaseAllStoodDown_LeavesOtherBrowsersAlone is the scoping half: the
// prompt release must not un-gate a hold in a DIFFERENT workspace's browser.
// Without it, the assertions above would pass equally on an implementation that
// simply cleared every lock in the process.
func TestReleaseAllStoodDown_LeavesOtherBrowsersAlone(t *testing.T) {
	mgr := newHandoverTestManager(t, true)
	other := newHandoverTestManager(t, true)
	other.key = newTestBrowsingKey(t, "some-other-workspace")
	ctx := context.Background()

	otherOwner, err := TabOwnerSession("some-other-chat")
	require.NoError(t, err)
	otherSet := sessionKey(other.key, otherOwner)
	require.True(t, other.Live().TakeControl(otherSet, "other-human"))
	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"))

	require.Len(t, mgr.Live().ReleaseAllStoodDown(), 1,
		"the release must report only the sets THIS browser actually cleared")

	assert.Nil(t, controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil),
		"this workspace's browser is free again")
	assert.NotNil(t, controlledResult(ctx, other, other.key, otherOwner, "browser_click", nil),
		"another workspace's held browser must stay held — one operator's prompt must never take the "+
			"wheel out of a different operator's hands")
}

// TestReleaseStoodDown_BroadcastsTheFreedLockToOtherViewers is the guard for
// the half of ADR-039 UAT BE-1 that ADR-085 Finding 7(b) silently dropped.
//
// The two tests above exercise releaseControl and detach, which both still
// broadcast. Neither is reachable from a release any more: Finding 7(b)
// repointed pkg/gateway/browser_ws.go::handleControl at ReleaseStoodDown
// (correctly — releaseControl leaves the FR-026a latch standing), and
// ReleaseStoodDown is now the ONLY production path that frees the lock short
// of a disconnect. It notified the former holder and nobody else, so every
// OTHER attached panel stayed on "Someone else is driving" with its own
// Take-control affordance disabled — for as long as it stayed attached, over
// a browser nobody was driving. The full round trip is covered at the gateway
// by TestBrowserWS_Control_ControlledByOther_BroadcastsToSecondConnection,
// but that test needs a real Chromium and therefore SKIPs on most machines;
// this one is pure in-memory bookkeeping and always runs.
func TestReleaseStoodDown_BroadcastsTheFreedLockToOtherViewers(t *testing.T) {
	lv := &LiveView{
		sessionID:    "s1",
		viewers:      map[string]struct{}{"connA": {}, "connB": {}},
		controlSinks: make(map[string]ControlSink),
		releaseSinks: make(map[string]ReleaseNotifySink),
	}
	reg := &LiveViewRegistry{views: map[string]*LiveView{"s1": lv}}

	gotA := make(chan bool, 4)
	gotB := make(chan bool, 4)
	lv.controlSinks["connA"] = func(controlledByOther bool) { gotA <- controlledByOther }
	lv.controlSinks["connB"] = func(controlledByOther bool) { gotB <- controlledByOther }

	notifiedA := make(chan struct{}, 4)
	lv.releaseSinks["connA"] = func() { notifiedA <- struct{}{} }

	require.True(t, lv.takeControl("connA"))
	requireControlBroadcast(t, gotB, true, "conn B must first learn conn A took control")

	formerHolder, cleared := reg.ReleaseStoodDown("s1")
	require.Equal(t, "connA", formerHolder)
	require.True(t, cleared)

	requireControlBroadcast(t, gotB, false,
		"conn B must be told the lock was freed — without this it stays wedged on 'someone else is driving'")

	select {
	case <-notifiedA:
	case <-time.After(2 * time.Second):
		t.Fatal("the former holder must still get its FR-031b ReleaseNotifySink")
	}
	requireNoControlBroadcast(t, gotA,
		"the former holder is served by its own `released` lifecycle frame, never a control_only broadcast "+
			"(FR-031b: a control_only frame cannot clear the holder's own isControlling)")
}

// TestReleaseStoodDown_LatchOnlyStandDownDoesNotBroadcast pins the gate on the
// fan-out above. `cleared` is also true for a latch-only (FR-026a) or
// handover-only (FR-047) stand-down, but neither ever sets lv.controller, so
// no viewer was ever sent controlled_by_other=true for them and there is
// nothing to correct. Broadcasting anyway would be a status change the SPA
// must not see.
func TestReleaseStoodDown_LatchOnlyStandDownDoesNotBroadcast(t *testing.T) {
	lv := &LiveView{
		sessionID:    "s1",
		viewers:      map[string]struct{}{"connB": {}},
		controlSinks: make(map[string]ControlSink),
		releaseSinks: make(map[string]ReleaseNotifySink),
	}
	reg := &LiveViewRegistry{views: map[string]*LiveView{"s1": lv}}

	gotB := make(chan bool, 4)
	lv.controlSinks["connB"] = func(controlledByOther bool) { gotB <- controlledByOther }

	lv.mu.Lock()
	lv.standDownUntilPrompt = true // a handover/latch stand-down: no interactive holder
	lv.mu.Unlock()

	formerHolder, cleared := reg.ReleaseStoodDown("s1")
	require.Equal(t, "", formerHolder, "a latch-only stand-down has no interactive holder to report")
	require.True(t, cleared, "the latch itself was still cleared")

	requireNoControlBroadcast(t, gotB,
		"nobody was ever told controlled_by_other=true for a latch-only stand-down, so nothing needs clearing")
}
