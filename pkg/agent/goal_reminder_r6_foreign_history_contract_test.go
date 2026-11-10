package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalReminderR6SameTaskForeignHistoryIsolation: an earlier owned run of a
// task is progress. A later run of that same task, started by a foreign
// session, is not, even after the current row is deleted or replaced. A fresh
// owned run of the same task must still reset the streak. Founder R6: progress
// follows actual run ownership, not the current row, the assignee, or a shared
// agent.
func TestGoalReminderR6SameTaskForeignHistoryIsolation(t *testing.T) {
	t.Run("CurrentRowDeleted", func(t *testing.T) {
		r6SignalGapSameTaskForeign(t, true)
	})
	t.Run("CurrentRowReplacedByForeignSession", func(t *testing.T) {
		r6SignalGapSameTaskForeign(t, false)
	})
}

func r6SignalGapSameTaskForeign(t *testing.T, deleteRow bool) {
	t.Helper()
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t))
	store := GetTaskStore(h.al)
	require.NotNil(t, store, "SETUP: the keeper must read the real task store")
	tk := mustCreateTask(t, store, &task.Task{
		Title: "R6 same-task foreign history", WorkspaceID: testHarnessWorkspaceMembershipID,
		AgentID: testDefaultAgentID, Status: task.StatusNext,
	})
	ownedSession := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	r6SignalGapPersistTerminalRun(t, h, tk.ID, h.id, ownedSession, nil)
	grSetStreak(t, h, 1)
	h.age(t)
	consumed := h.reminder(t)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "the earlier owned run of this same task is progress and resets the seeded streak")
	grTextReminder(t, h, consumed)

	foreignInitiator := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	foreignRun := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	require.NotEqual(t, h.id, foreignInitiator)
	require.NotEqual(t, h.id, foreignRun)
	require.NotEqual(t, ownedSession, foreignRun)
	r6SignalGapRetireTaskRow(t, store, tk.ID, foreignInitiator, deleteRow)
	foreignRunID := r6SignalGapPersistTerminalRun(t, h, tk.ID, foreignInitiator, foreignRun, []string{h.id})
	r6SignalGapRequireSameTaskRuns(t, store, tk.ID, h.id, ownedSession, foreignRun)

	grSetStreak(t, h, 2)
	h.age(t)
	outstanding := h.reminder(t)
	assert.Equal(t, 3, goalRecordForSession(t, h.id).ZeroOutputPushes, "a later foreign run of task %s (run %s) is not progress; the pending text reminder counts once and must not be reset to 0", tk.ID, foreignRunID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))

	freshSession := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	r6SignalGapPersistTerminalRun(t, h, tk.ID, h.id, freshSession, nil)
	grTextReminder(t, h, outstanding)
	grSetStreak(t, h, 1)
	h.age(t)
	fresh := h.reminder(t)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "a fresh owned completion of the same task must reset the streak; otherwise the foreign result is an empty scan")
	grTextReminder(t, h, fresh)
}

func r6SignalGapRetireTaskRow(t *testing.T, store *task.Store, taskID, foreignInitiator string, deleteRow bool) {
	t.Helper()
	if deleteRow {
		_, err := store.Delete(taskID)
		require.NoError(t, err)
		_, err = store.Get(taskID)
		require.Error(t, err, "SETUP: the current task row must actually be gone")
		return
	}
	initiator := &task.Initiator{AgentID: testDefaultAgentID, SessionID: foreignInitiator, Depth: 1}
	sameAgent := testDefaultAgentID
	cleared := ""
	updated, err := store.Update(taskID, task.Patch{
		SessionID: &foreignInitiator, Initiator: &initiator, AgentID: &sameAgent, CompletedAt: &cleared,
	})
	require.NoError(t, err)
	require.Equal(t, foreignInitiator, updated.SessionID, "SETUP: the current row must name the foreign session")
	require.Equal(t, testDefaultAgentID, updated.AgentID, "SETUP: the shared agent must remain, and still must not grant ownership")
	require.NotNil(t, updated.Initiator)
	require.Equal(t, foreignInitiator, updated.Initiator.SessionID)
}

func r6SignalGapRequireSameTaskRuns(t *testing.T, store *task.Store, taskID, goalSession, ownedSession, foreignSession string) {
	t.Helper()
	runs, err := store.ListRuns(taskID)
	require.NoError(t, err)
	var owned, foreign bool
	for _, run := range runs {
		require.Equal(t, taskID, run.TaskID, "history under test must be this task, not a different task")
		require.NotNil(t, run.EndedAt, "both discriminator runs must be persisted terminal runs")
		switch run.SessionID {
		case ownedSession:
			owned = true
		case foreignSession:
			foreign = true
			assert.Contains(t, run.RecipientSessionIDs, goalSession, "the goal session is only a recipient of the foreign run, which is not ownership")
		}
	}
	require.True(t, owned, "SETUP: the retained owned run must survive the current-row change")
	require.True(t, foreign, "SETUP: the later run must be the same task, not a different task")
}

func r6SignalGapPersistTerminalRun(t *testing.T, h *grHarness, taskID, initiatorSession, runSession string, recipients []string) string {
	t.Helper()
	require.NotEqual(t, initiatorSession, "", "SETUP: a terminal run needs its real initiating session")
	err := h.al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: runSession, Generation: 1, State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, OwnerScopeID: initiatorSession,
		WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: testDefaultAgentID,
		Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: taskID, CallID: "r6-signal-gap-" + runSession},
		InitiatedBy: &session.InitiatedBy{
			AgentID: testDefaultAgentID, SessionID: initiatorSession, Depth: 1,
			Authorization: session.Authorization{Mode: session.AuthorizationModeTask, RemainingDepth: 1},
		},
	})
	require.NoError(t, err)
	store := GetTaskStore(h.al)
	opened, created, err := store.OpenRun(taskID, nil, task.RunKindManual, runSession, recipients)
	require.NoError(t, err)
	require.True(t, created, "SETUP: each completion needs a new run of this task")
	require.NoError(t, store.CloseRun(taskID, opened.RunID, task.StatusDone, "terminal result"))
	return opened.RunID
}
