// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// task_f13_f14_f15_test.go — regression coverage for the exhaustive tool-catalog
// review findings F13, F14, F15 against the workspace-scoped task tools
// (create_task_in_workspace, update_task_in_workspace, delete_task_in_workspace).
//
// F13 (create_task_in_workspace):
//   - in_progress must be rejected at create — it is a dispatch-only state
//     (issue #593), and the sibling update tool already refuses this same
//     transition. Creating directly with status:"in_progress" bypassed that
//     guard entirely before this fix.
//   - An unknown/typo status value must be rejected outright, not silently
//     defaulted to inbox — same reasoning list_tasks_in_workspace already
//     applies to its own status filter.
//
// F14 (update_task_in_workspace):
//   - workspace_id must be applied only AFTER store.Update's patch validation
//     succeeds, so a call that fails validation (e.g. an out-of-range
//     priority) is all-or-nothing: the task must NOT end up moved to a
//     different workspace while the caller is told INVALID_INPUT.
//
// Traces to: half_b_report.md F13/F14/F15 (tool-manifest-tier-redesign review).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestCreateTaskInWorkspace_InProgressRejected is the F13(a) regression test:
// create_task_in_workspace must refuse status:"in_progress" at create time,
// mirroring the guard update_task_in_workspace already enforces on the same
// transition (issue #593, Option A) — in_progress is reached only through
// real dispatch (run_task), never persisted directly with no session and no
// executor.
func TestCreateTaskInWorkspace_InProgressRejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)

	result := systools.NewTaskCreateTool(deps).Execute(callerCtx("caller-agent"), map[string]any{
		"name":         "forged in_progress task",
		"workspace_id": testWorkspaceID,
		"status":       "in_progress",
	})

	require.True(t, result.IsError,
		"create_task_in_workspace with status:in_progress must be rejected, got success: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "run_task",
		"expected the rejection to name run_task as the correct path")

	rows, err := task.New(home + "/tasks").List(task.Filter{WorkspaceID: testWorkspaceID})
	require.NoError(t, err)
	assert.Empty(t, rows, "a rejected create must not persist a task")
}

// TestCreateTaskInWorkspace_UnknownStatusRejected is the F13(b) regression
// test: an invalid/typo status value must be rejected explicitly rather than
// silently swallowed to "inbox" — a typo becoming a silent default would
// teach the caller its requested status was applied when it never was, the
// same reasoning list_tasks_in_workspace already documents for its own
// status filter.
func TestCreateTaskInWorkspace_UnknownStatusRejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)

	result := systools.NewTaskCreateTool(deps).Execute(callerCtx("caller-agent"), map[string]any{
		"name":         "typo status task",
		"workspace_id": testWorkspaceID,
		"status":       "in-progress", // typo: hyphen instead of underscore
	})

	require.True(t, result.IsError,
		"create_task_in_workspace with an unknown status must be rejected, got success: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "unknown status",
		"expected the rejection to name the unknown status, not silently default it")

	rows, err := task.New(home + "/tasks").List(task.Filter{WorkspaceID: testWorkspaceID})
	require.NoError(t, err)
	assert.Empty(t, rows, "a rejected create must not persist a task defaulted to inbox")
}

// TestCreateTaskInWorkspace_ValidStatusesStillAccepted is a positive control
// for F13: every status still legally settable at create time (inbox, next,
// blocked is NOT settable — see task_status_guard tests — so only inbox,
// next, done, failed are exercised here) must still be accepted.
func TestCreateTaskInWorkspace_ValidStatusesStillAccepted(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)

	for _, st := range []task.Status{task.StatusInbox, task.StatusNext, task.StatusFailed} {
		st := st
		t.Run(string(st), func(t *testing.T) {
			result := systools.NewTaskCreateTool(deps).Execute(callerCtx("caller-agent"), map[string]any{
				"name":         "valid status " + string(st),
				"workspace_id": testWorkspaceID,
				"status":       string(st),
			})
			require.False(t, result.IsError, "create_task_in_workspace with status=%q must succeed: %s",
				st, result.ForLLM)
		})
	}
}

// TestUpdateTaskInWorkspace_WorkspaceIDNotMovedOnValidationFailure is the F14
// regression test: workspace_id must be applied only AFTER store.Update's
// patch validation succeeds. Before the fix, workspace_id was written and
// persisted in its own locked write BEFORE the rest of the patch was
// validated, so a call carrying BOTH a workspace_id move and an invalid field
// (here, an out-of-range priority) would still move the task to the new
// workspace and persist it — while the tool reported INVALID_INPUT and the
// caller reasonably assumed nothing changed. The whole call must now be
// all-or-nothing.
func TestUpdateTaskInWorkspace_WorkspaceIDNotMovedOnValidationFailure(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	const otherWorkspaceID = "01JXTEST_WORKSPACE000099"
	seedWorkspace(t, home, otherWorkspaceID)

	const taskID = "01JXF14_WORKSPACE_MOVE001"
	seedTask(t, home, taskID, "task to (not) move", task.StatusInbox, nil)

	result := systools.NewTaskUpdateTool(deps).Execute(callerCtx("caller-agent"), map[string]any{
		"id":           taskID,
		"workspace_id": otherWorkspaceID,
		"priority":     float64(6), // out of range: task.ValidatePriority rejects it
	})

	require.True(t, result.IsError,
		"update_task_in_workspace with an invalid priority must be rejected, got success: %s", result.ForLLM)

	got, err := task.New(home + "/tasks").Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, testWorkspaceID, got.WorkspaceID,
		"a call that fails patch validation must NOT have moved the task to the new workspace — "+
			"the whole update must be all-or-nothing")
}

// TestUpdateTaskInWorkspace_WorkspaceIDMovedWhenPatchValid is the positive
// control for F14: a workspace_id move accompanied by a VALID patch must
// still succeed and actually persist the move — proving the fix only defers
// the write, it does not silently drop it.
func TestUpdateTaskInWorkspace_WorkspaceIDMovedWhenPatchValid(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	const otherWorkspaceID = "01JXTEST_WORKSPACE000098"
	seedWorkspace(t, home, otherWorkspaceID)

	const taskID = "01JXF14_WORKSPACE_MOVE002"
	seedTask(t, home, taskID, "task that should move", task.StatusInbox, nil)

	result := systools.NewTaskUpdateTool(deps).Execute(callerCtx("caller-agent"), map[string]any{
		"id":           taskID,
		"workspace_id": otherWorkspaceID,
		"priority":     float64(2),
	})

	require.False(t, result.IsError, "update_task_in_workspace: %s", result.ForLLM)

	got, err := task.New(home + "/tasks").Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, otherWorkspaceID, got.WorkspaceID,
		"a call with a valid patch must actually persist the workspace_id move")
	assert.Equal(t, 2, got.Priority, "the accompanying valid patch field must also have persisted")
}

// TestDeleteTaskInWorkspace_SurfacesUnblockedTasks is the F15 regression test:
// deleting a task that other tasks depend on via blocked_by must surface the
// resulting unblocked dependents in the response payload (unblocked_tasks) —
// previously this cascade was completely undisclosed to the caller.
func TestDeleteTaskInWorkspace_SurfacesUnblockedTasks(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)

	const blockerID = "01JXF15_BLOCKER00000001"
	const dependentID = "01JXF15_DEPENDENT000001"
	// The dependent's ONLY blocker is blockerID, so once blockerID is deleted
	// the dependent's remaining blocked_by list is empty — vacuously
	// "every remaining blocker is done" — and it must be reported unblocked,
	// regardless of blockerID's own status at the time it was deleted.
	seedTask(t, home, blockerID, "blocker", task.StatusNext, nil)
	seedTask(t, home, dependentID, "dependent", task.StatusNext, []string{blockerID})

	result := systools.NewTaskDeleteTool(deps).Execute(callerCtx("caller-agent"), map[string]any{
		"id":      blockerID,
		"confirm": true,
	})
	require.False(t, result.IsError, "delete_task_in_workspace: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "unblocked_tasks",
		"the response must surface the dependents unblocked by this delete")
	assert.Contains(t, result.ForLLM, dependentID,
		"the unblocked dependent's id must appear in unblocked_tasks")
}
