// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_tasks_goal_delete_order_test.go — the ONE answer all three task-delete
// surfaces now give when the paired goal record cannot be removed
// (GOAL-FR-044/EC-4).
//
// Before this, the three surfaces gave three different answers to the same
// question, and the REST one gave the worst: it logged the failure at Error
// and returned 204 No Content. The task was gone, the goal record survived as
// a permanently-`active` orphan owned by an id that no longer resolves, and
// the caller was told it had succeeded. That is the same class of lie as SF-6
// in rest_tasks.go — a response indistinguishable from the correct one after a
// goal-record fault.
//
// The cleanup now runs BEFORE the task file is removed and a failure refuses
// the whole delete. The orphan is prevented rather than reported, and the
// caller's retry is meaningful because nothing was destroyed.
package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// denyGoalStoreReads makes the goal entity directory unreadable for the rest
// of the test, so tools.RemoveTaskGoalRecords' List() fails the way a genuine
// storage fault would. Skips the test when the mode change does not actually
// deny (running as root, or a filesystem that ignores the bit) rather than
// asserting against a fault that never happened.
func denyGoalStoreReads(t *testing.T, api *restAPI) {
	t.Helper()
	dir := filepath.Join(filepath.Dir(api.taskStore.Dir()), "entities", "goals")
	require.DirExists(t, dir, "fixture: the goal entity directory must exist before it is locked")
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() {
		// Restore before t.TempDir's own cleanup runs, or the temp tree cannot
		// be removed.
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Logf("restore goal dir permissions: %v", err)
		}
	})
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("goal entity directory is still readable with mode 0000 (running as root?) — " +
			"this test cannot create the storage fault it is about")
	}
}

// TestTaskDelete_GoalCleanupFailure_RefusesTheDeleteEntirely is the core case.
// A goal-store fault must not produce a 204 and an orphan; it must produce a
// real error AND leave the task intact so the caller can retry.
func TestTaskDelete_GoalCleanupFailure_RefusesTheDeleteEntirely(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	id := createTaskWithGoal(t, api, wsID, "task whose goal store breaks")

	gs := tools.GoalStoreForTasks(api.taskStore)
	_, err := gs.GetByOwner(gen.GoalOwnerKindTask, id)
	require.NoError(t, err, "fixture: the task must have a paired goal record before the delete")

	denyGoalStoreReads(t, api)

	w := deleteTaskRec(t, api, id)
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"a delete that cannot remove the paired goal record must NOT answer 204 — "+
			"204 with an orphan left behind is the lie this ordering exists to stop; body=%s",
		w.Body.String())
	assert.Contains(t, w.Body.String(), "goal record",
		"the error must say what actually failed, not a bare 'could not delete task'")

	// The task must still be there: nothing was destroyed, so a retry is both
	// meaningful and safe.
	stored, gErr := api.taskStore.Get(id)
	require.NoError(t, gErr,
		"the task must survive a refused delete — otherwise the refusal is itself a lie")
	assert.Equal(t, id, stored.ID)
}

// TestTaskDelete_SucceedsAndIsRetryableAfterTheFaultClears proves the refusal
// is transient rather than a dead end: once the goal store is readable again,
// the very same delete request succeeds and removes both records. This is what
// makes "refuse" the right answer instead of "report and carry on" — the
// caller has somewhere to go.
func TestTaskDelete_SucceedsAndIsRetryableAfterTheFaultClears(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	id := createTaskWithGoal(t, api, wsID, "retryable delete")

	dir := filepath.Join(filepath.Dir(api.taskStore.Dir()), "entities", "goals")
	require.DirExists(t, dir)
	require.NoError(t, os.Chmod(dir, 0o000))
	if _, err := os.ReadDir(dir); err == nil {
		require.NoError(t, os.Chmod(dir, 0o700))
		t.Skip("goal entity directory is still readable with mode 0000 (running as root?)")
	}

	require.Equal(t, http.StatusInternalServerError, deleteTaskRec(t, api, id).Code,
		"fixture: the first attempt must be refused")

	require.NoError(t, os.Chmod(dir, 0o700))

	w := deleteTaskRec(t, api, id)
	require.Equal(t, http.StatusNoContent, w.Code,
		"the retry must succeed once the fault clears; body=%s", w.Body.String())

	_, err := api.taskStore.Get(id)
	require.ErrorIs(t, err, task.ErrNotFound, "the task must be gone after the successful retry")
	_, gErr := tools.GoalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, id)
	require.Error(t, gErr, "the paired goal record must be gone too")
}
