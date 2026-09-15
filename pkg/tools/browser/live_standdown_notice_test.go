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
