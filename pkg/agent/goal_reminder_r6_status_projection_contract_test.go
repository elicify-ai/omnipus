package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Focused payload for the implementation branch only. The frozen a523 RED
// worktree is NOT edited; parent runs this exact symbol before/after labels.
func TestGoalReminderPauseStatusProjectionContract(t *testing.T) {
	t.Run("RealPausedStatusUsesGoalReasonAndDoesNotResume", func(t *testing.T) {
		h := grFifthOutstanding(t)
		grAssertFifthPause(t, h)
		before := goalRecordForSession(t, h.id)
		beforeLife := h.load(t)
		calls := h.root.count()
		query := h.human(t, "/goal status")
		answer := grJoin(t, query)
		require.NoError(t, answer.err)
		assert.Contains(t, answer.reply, "Latest goal reason: No progress after 5 reminders — goal paused. Reply to continue.")
		assert.NotContains(t, answer.reply, "Latest judge reason:", "an engine no-progress pause is not a Judge verdict")
		assert.Equal(t, calls, h.root.count(), "status query must not start model work")
		assert.Equal(t, beforeLife, h.load(t), "status query must not start another execution/generation")
		assert.Equal(t, before, goalRecordForSession(t, h.id), "status query itself must not resume, reset the streak or re-arm activity")
		h.age(t)
		h.assertNoKeeperWake(t, "a status query cannot resume the actual R6 pause")
		assert.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes)
	})
	t.Run("EmptyReasonWithPreviousRoundsDoesNotDenyThoseRounds", func(t *testing.T) {
		h := grNewRecordedBrake(t)
		_, err := resolveGoalRecordStore().Update(h.goalID, func(g *goal.Goal) error {
			g.Round = 2 // formatting fixture: an existing persisted previous-round count
			g.LatestReason = ""
			return nil
		})
		require.NoError(t, err)
		before := goalRecordForSession(t, h.id)
		calls := h.root.count()
		answer := grJoin(t, h.human(t, "/goal status"))
		require.NoError(t, answer.err)
		assert.Contains(t, answer.reply, "Rounds: 2/")
		assert.Contains(t, answer.reply, "Latest goal reason: (no reason recorded)")
		assert.NotContains(t, answer.reply, "(no round completed yet)")
		assert.Equal(t, before, goalRecordForSession(t, h.id))
		assert.Equal(t, calls, h.root.count())
	})
}
