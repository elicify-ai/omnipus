// live_standdown_notice_test.go — ADR-085 BROWSER-FR-041/FR-044 (the visible
// waiting surface) and FR-029/FR-031a/FR-052 (the release and its audit
// observer), asserted at the seam the gateway consumes.
//
// WHY THIS FILE EXISTS. Every symbol it covers was BUILT and then connected to
// nothing: SetHandoverPending's transition return value was discarded by its
// only caller, ControlIdleReleaseHooks had no production registrar, and the
// notice seam did not exist at all — so no operator ever saw a line, and no
// release ever reached the audit trail. These tests assert the seam FIRES from
// the production call path, not that a struct can hold a function.

package browser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStandDownNotices installs a process-wide notice sink for the duration
// of one test and returns a drain function. The sink is invoked on its own
// goroutine (emitStandDownNotice's contract), so notices are delivered over a
// buffered channel and drained with a deadline rather than read straight off a
// slice — a plain slice read would be a data race and would also pass on a
// build where nothing is ever emitted.
func captureStandDownNotices(t *testing.T) (drain func(want int) []StandDownNotice) {
	t.Helper()
	ch := make(chan StandDownNotice, 8)
	SetStandDownNoticeSink(func(n StandDownNotice) { ch <- n })
	t.Cleanup(func() { SetStandDownNoticeSink(nil) })

	return func(want int) []StandDownNotice {
		t.Helper()
		got := make([]StandDownNotice, 0, want)
		deadline := time.After(3 * time.Second)
		for len(got) < want {
			select {
			case n := <-ch:
				got = append(got, n)
			case <-deadline:
				t.Fatalf("timed out waiting for %d stand-down notice(s); got %d", want, len(got))
			}
		}
		// Give a spurious extra notice a moment to show up, so "exactly one
		// per unbroken hold" can actually fail.
		select {
		case extra := <-ch:
			got = append(got, extra)
		case <-time.After(150 * time.Millisecond):
		}
		return got
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

// TestTakeControl_RaisesTheWaitingSurfaceNotice is the other FR-041 producer:
// a human taking the wheel through the panel.
func TestTakeControl_RaisesTheWaitingSurfaceNotice(t *testing.T) {
	drain := captureStandDownNotices(t)
	mgr := newHandoverTestManager(t, true)

	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"))

	got := drain(1)
	require.Len(t, got, 1, "a take must raise exactly one waiting line")
	assert.Equal(t, StandDownByTake, got[0].Producer)
	assert.Empty(t, got[0].Reason, "a human take has no model-authored reason to carry")
	assert.NotZero(t, got[0].HoldStartedAtUnixNano)
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

// TestSweeper_ReportsToTheGlobalReleaseHooks is SF-3's seam. Before the
// process-wide registration existed, ControlIdleReleaseHooks had NO production
// registrar at all, so the FR-031a/FR-052 sweeper released holds and left no
// audit record — the trail showed deferrals and handovers but never a release,
// and read as though the wheel was never given back.
//
// The sweeper's own 30s ticker is not driven here; sweepTick is called
// directly, which is the same function the ticker calls.
func TestSweeper_ReportsToTheGlobalReleaseHooks(t *testing.T) {
	var idle, disabled []string
	SetGlobalControlReleaseHooks(ControlIdleReleaseHooks{
		OnIdleRelease:     func(sessionID, _ string) { idle = append(idle, sessionID) },
		OnDisabledRelease: func(sessionID, _ string) { disabled = append(disabled, sessionID) },
	})
	t.Cleanup(func() { SetGlobalControlReleaseHooks(ControlIdleReleaseHooks{}) })

	// take_control_enabled=false is FR-052's forced release: it fires
	// regardless of the idle window, so no clock has to be advanced.
	mgr := newHandoverTestManager(t, false)
	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"))

	mgr.Live().sweepTick()

	assert.Equal(t, []string{testSessionID}, disabled,
		"an FR-052 switch-off release must reach the observer as a DISABLED release")
	assert.Empty(t, idle,
		"it must not be reported as an idle expiry — conflating the two loses the ability to tell "+
			"'nobody was watching' from 'an operator disabled the feature'")
	assert.False(t, mgr.Live().IsStoodDown(testSessionID), "the sweeper must actually have released it")
}

// TestPerRegistryReleaseHooksOverrideTheGlobalOne pins the resolution order,
// so a test that installs its own observer is never surprised by the
// gateway's process-wide one firing underneath it.
func TestPerRegistryReleaseHooksOverrideTheGlobalOne(t *testing.T) {
	globalFired := false
	SetGlobalControlReleaseHooks(ControlIdleReleaseHooks{
		OnDisabledRelease: func(string, string) { globalFired = true },
	})
	t.Cleanup(func() { SetGlobalControlReleaseHooks(ControlIdleReleaseHooks{}) })

	mgr := newHandoverTestManager(t, false)
	ownFired := false
	mgr.Live().SetControlIdleReleaseHooks(ControlIdleReleaseHooks{
		OnDisabledRelease: func(string, string) { ownFired = true },
	})
	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"))

	mgr.Live().sweepTick()

	assert.True(t, ownFired, "the registry's own observer must win")
	assert.False(t, globalFired, "the process-wide observer must not also fire")
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
