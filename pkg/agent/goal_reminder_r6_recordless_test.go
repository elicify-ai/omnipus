package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gitevidence"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoalReminderRecordlessParity(t *testing.T) {
	t.Run("FiveCompletedUnproductiveOutcomesPauseWithoutAutoCompile", func(t *testing.T) {
		h := grNewRecordlessBrake(t)
		original := h.goalID
		for dispatch := 1; dispatch <= 5; dispatch++ {
			if dispatch > 1 {
				h.age(t)
			}
			msg := h.reminder(t)
			assert.Equal(t, dispatch-1, goalRecordForSession(t, h.id).ZeroOutputPushes)
			grTextReminder(t, h, msg)
			assert.Empty(t, goalRecordForSession(t, h.id).Criteria, "R6: no separate two-nudge compile may author a record")
			assert.Equal(t, original, h.goalID)
		}
		h.age(t)
		grAssertFifthPause(t, h)
		assert.Empty(t, goalRecordForSession(t, h.id).Criteria)
	})
	t.Run("HumanReplyResumesSameRecordlessGoal", func(t *testing.T) {
		h := grNewRecordlessBrake(t)
		msg := h.reminder(t)
		grSetStreak(t, h, 4)
		grTextReminder(t, h, msg)
		h.age(t)
		grAssertFifthPause(t, h)
		run := h.human(t, "Continue this same goal without replacing it.")
		call := grAwaitCall(t, h.root, run)
		h.finish(t, run, call)
		assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "the actual human reply resets this same goal before any new reminder completes")
		// If the ordinary human-resume path immediately publishes its one
		// registration reminder, it must be a real pending outcome, not a
		// spent streak or an engine-authored compile.
		completedAfterHuman := 0
		select {
		case pending := <-h.al.bus.InboundChan():
			grTextReminder(t, h, pending)
			completedAfterHuman = 1
		default:
		}
		h.age(t)
		next := h.reminder(t)
		assert.Equal(t, completedAfterHuman, goalRecordForSession(t, h.id).ZeroOutputPushes, "assess exactly the actual registration reminder completed after human reset, or zero if none was emitted")
		assert.Empty(t, goalRecordForSession(t, h.id).Criteria)
		grTextReminder(t, h, next)
	})
	t.Run("MoreThanFiveProductiveRecordlessRemindersHaveNoLifetimeCap", grRecordlessProductive)
}

func grRecordlessProductive(t *testing.T) {
	h := grNewRecordlessBrake(t)
	dir, err := workspace.EnsureWorkDir(config.OmnipusHomeDir(), testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	repo, err := gitevidence.Open(dir, gitevidence.WithRedactor(func(s string) string { return s }))
	require.NoError(t, err)
	for n := 0; n < 7; n++ { // seven actual outcomes exceeds any lifetime-five cap
		if n > 0 {
			h.age(t)
		}
		msg := h.reminder(t)
		assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
		grTextReminder(t, h, msg)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "recordless-work.txt"), []byte(fmt.Sprintf("actual goal work round %d\n", n)), 0o600))
		commit, commitErr := repo.Commit(gitevidence.BoundaryAgent, gitevidence.CommitMeta{AgentID: testDefaultAgentID}, []string{"recordless-work.txt"})
		require.NoError(t, commitErr)
		require.False(t, commit.Skipped)
		assert.Empty(t, goalRecordForSession(t, h.id).Criteria)
	}
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
	assert.Equal(t, 8, h.root.count(), "one bootstrap and seven actual ordinary reminders executed")
}

func grBrakeBackgroundCompletion(t *testing.T) {
	for _, related := range []bool{true, false} {
		t.Run(fmt.Sprintf("ActualOwnerMatches=%v", related), func(t *testing.T) {
			h := grNewRecordedBrake(t)
			grTextReminder(t, h, h.reminder(t))
			owner := h.id
			if !related {
				owner = newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
			}
			ps := grRegisteredProcess(t, owner, true)
			grSetStreak(t, h, 1)
			ps.SetStatus("done") // existing actual locked terminal writer, not StartTime/Read
			require.True(t, ps.IsDone())
			h.age(t)
			next := h.reminder(t)
			want := 2 // one prior assessed unproductive outcome + this completed text-only reminder
			if related {
				want = 0
			}
			assert.Equal(t, want, goalRecordForSession(t, h.id).ZeroOutputPushes, "completion belongs only to the exact owning session family")
			grTextReminder(t, h, next)
		})
	}
}

// A true owned running process must not consume any persisted outcome. The
// registry represents the external process edge; no OS command is started.
func TestGoalReminderBackgroundOwnership(t *testing.T) {
	t.Run("RootAndCompletedDescendantRunning", grBrakeBackgroundCommands)
	t.Run("UnrelatedAndForegroundControls", grBrakeUnrelatedBackground)
	t.Run("CompletedProcessIsProgressOnlyForItsOwner", grBrakeBackgroundCompletion)
	t.Run("RootReturnedWhileRealHelperStillRuns", grBrakeLiveHelper)
	t.Run("DurableQueuedHelperSuppresses", grBrakeQueuedHelper)
}
