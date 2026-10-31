package agent

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalReminderR6CompletedCounterWriteRetainsOutcomeUntilRepair is review
// warning F2: a real fifth completion cannot be lost when its accounting write
// fails. Publication/receipt faults before the producing turn do not cover it.
func TestGoalReminderR6CompletedCounterWriteRetainsOutcomeUntilRepair(t *testing.T) {
	h := grNewRecordedBrake(t)
	fifth := h.reminder(t)
	grSetStreak(t, h, 4)
	run := h.system(fifth)
	call := grAwaitCall(t, h.root, run)
	h.finish(t, run, call)
	grAwaitOwner(t, call)
	require.NotNil(t, call.owner)
	require.NotNil(t, call.turn)
	require.NotNil(t, call.record)
	h.age(t)
	pending := r6WarnPendingOutcome(h.goalID)
	require.NotNil(t, pending, "SETUP: the real text completion must await assessment")
	require.Same(t, call.owner, pending.execution, "SETUP: retain the actual producing owner, never a fabricated pending execution")
	require.Equal(t, call.turn.turnID, pending.turnID)
	require.Equal(t, call.record.ExecutionID, h.load(t).ExecutionID)
	require.Equal(t, 4, goalRecordForSession(t, h.id).ZeroOutputPushes)
	beforeCalls := h.root.count()
	store := resolveGoalRecordStore()

	// Existing root-independent, ONE-goal sidecar fault. Get/List stay real;
	// no transcript, metadata, lifecycle or other goal is made unwritable.
	failGoalRecordWrites(t, h.goalID)
	_, err := store.Update(h.goalID, func(*goal.Goal) error { return nil })
	require.ErrorIs(t, err, syscall.EISDIR, "SETUP: the exact goal's accounting write must be faulted")
	r6GateRequireStorageHealth(t, h)
	for tick := 0; tick < 3; tick++ {
		h.al.goalQuietWindowSettle(time.Now())
		assert.Equal(t, 4, goalRecordForSession(t, h.id).ZeroOutputPushes, "a failed accounting write must leave durable streak four")
		retained := r6WarnPendingOutcome(h.goalID)
		assert.Same(t, pending, retained, "a failed accounting write must retain the SAME completed outcome for retry")
		if retained != nil {
			assert.Equal(t, pending.turnID, retained.turnID)
			assert.Same(t, call.owner, retained.execution)
		}
		grNoPublishedReminder(t, h.al)
		assert.Equal(t, beforeCalls, h.root.count(), "uncommitted fifth accounting must not dispatch more paid work")
		assert.False(t, h.al.goalIsWaitingOnUser(h.goalID), "the failed write must not announce a pause that was never committed")
		assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
	}

	// Repair only the poisoned sidecar; prove an actual same-record write now
	// succeeds without manufacturing a completion, changing its count or
	// replacing the session/goal/owner. The retained real outcome does the rest.
	require.NoError(t, os.Remove(goalRecordLockPath(h.goalID)))
	repaired, err := store.Update(h.goalID, func(*goal.Goal) error { return nil })
	require.NoError(t, err, "SETUP: the repaired goal must accept a real write")
	require.Equal(t, 4, repaired.ZeroOutputPushes)
	h.al.goalQuietWindowSettle(time.Now())
	assert.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes, "repair must account for the retained fifth outcome exactly once")
	assert.Nil(t, r6WarnPendingOutcome(h.goalID), "only the successfully committed assessment consumes pending")
	assert.True(t, h.al.goalIsWaitingOnUser(h.goalID))
	assert.Equal(t, 1, grHasSystemLine(h.visibleHistory(t), grPauseLine), "one public pause follows the committed fifth outcome")
	for tick := 0; tick < 2; tick++ {
		h.al.goalQuietWindowSettle(time.Now())
		grNoPublishedReminder(t, h.al)
		assert.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes, "later ticks must not count the same completion twice")
		assert.Equal(t, 1, grHasSystemLine(h.visibleHistory(t), grPauseLine), "pause publication is idempotent")
	}
	assert.Equal(t, beforeCalls, h.root.count(), "there is no sixth reminder after repair")
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
}

func r6WarnPendingOutcome(goalID string) *goalReminderOutcome {
	work := goalReminderWorkFor(goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	return work.pending // observation only; never synthesize or consume it
}
