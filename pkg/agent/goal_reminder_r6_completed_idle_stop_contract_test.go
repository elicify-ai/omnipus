package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalReminderR6CompletedIdleStopKeepsQueuedWakeDormant isolates the
// top-level Stop pause: no live human turn can reach cancellation's older
// pause writer. The queued input and later human resume are real producers.
func TestGoalReminderR6CompletedIdleStopKeepsQueuedWakeDormant(t *testing.T) {
	h := grNewRecordedBrake(t)
	queued := h.reminder(t)
	grSetStreak(t, h, 2)
	prior := h.load(t)
	require.Equal(t, session.LifecycleCompleted, prior.State)
	require.Nil(t, h.al.ownLiveTurn(h.id), "SETUP: Stop must target an already completed, idle ordinary root")
	before := h.root.count()

	grStop(t, h) // authenticated one-session Stop, no new human turn
	assert.Equal(t, session.LifecycleCompleted, h.load(t).State, "Stop leaves the already completed execution truthful")
	assert.Equal(t, prior.Generation, h.load(t).Generation)
	assert.Equal(t, prior.ExecutionID, h.load(t).ExecutionID)
	for tick := 0; tick < 2; tick++ {
		h.age(t)
		h.assertNoKeeperWake(t, "Stop on the already-completed root")
	}

	// Positively join the real receiver even when the bad path calls the
	// provider. No idle/provider-count absence is used as a completion signal.
	run := h.system(queued)
	calls, result := r6WarnJoinStoppedWake(t, h, run)
	assert.NoError(t, result.err, "the stopped automatic input is held, not a failed human turn")
	assert.Len(t, calls, 0, "Stop before delivery must prevent provider/tool execution on the completed root")
	assert.Equal(t, before, h.root.count(), "a held queued reminder cannot spend another provider call")
	assert.Equal(t, prior.Generation, h.load(t).Generation, "Stop must prevent the old wake from reviving the completed root")
	assert.Equal(t, prior.ExecutionID, h.load(t).ExecutionID, "the held wake must not replace the execution identity")
	assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "Stop and held input do not assess a completed reminder")
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	assert.True(t, h.al.goalKeeperPausedByStop(h.id), "the top-level Stop wiring must retain the keeper pause")

	human := h.human(t, "Resume this same goal after my Stop.")
	call := grAwaitCall(t, h.root, human)
	require.True(t, call.human, "SETUP: only a genuine human execution resumes the stopped keeper")
	h.finish(t, human, call)
	grAwaitOwner(t, call)
	assert.False(t, h.al.goalKeeperPausedByStop(h.id), "the later human turn lifts the Stop hold")
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	h.age(t)
	grTextReminder(t, h, h.reminder(t))
	assert.Equal(t, before+2, h.root.count(), "exactly one later human turn and one resumed reminder execute")
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
}

func r6WarnJoinStoppedWake(t *testing.T, h *grHarness, run *grPending) ([]*grCall, grResult) {
	t.Helper()
	watchdog := time.NewTimer(grWatchdog)
	defer watchdog.Stop()
	var calls []*grCall
	for {
		select {
		case call := <-h.root.requests:
			calls = append(calls, call)
			h.root.release(call)
		case <-run.done:
			for _, call := range calls {
				grAwaitOwner(t, call)
			}
			return calls, run.result
		case <-watchdog.C:
			t.Fatal("SETUP: the actual stopped-wake receiver did not join")
			return nil, grResult{}
		}
	}
}
