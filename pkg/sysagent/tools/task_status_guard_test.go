// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// H3: sysagent status-guard tests for the unified 6-state task vocabulary
// (ADR-051 D5 removed `planning`, formerly 7-state).
//
// The old A4 guard ("status=active only via /start") has been REMOVED in Sprint
// 2: the new store accepts any of the 6 valid statuses without a transition
// machine.
//
// UAT batch3 S58 fix (docs/internal/qa/uat-report-full-tool-catalog-batch3-2026-09-02.md,
// finding #2): an invalid status value (not in the 6-state vocab) used to be
// SILENTLY IGNORED by the update tool — success with no error, status left
// unchanged, no way for the caller to tell their request was dropped. That
// is a HIGH-severity UAT-confirmed defect, not intended behavior: it
// diverged from TaskCreateTool.Execute's own explicit rejection of the same
// input. The tool now rejects an unrecognized status with INVALID_INPUT,
// mirroring TaskCreateTool exactly (same message shape, same enumerated
// list). TestSysagentTaskUpdate_InvalidStatusIgnored and
// TestSysagentTaskUpdate_RemovedPlanningStatusIgnored below were rewritten
// in place (not deleted) to pin the corrected, rejecting behavior — their
// original "silently ignored" assertions were the pre-fix bug, not a
// legitimate contract to preserve.
//
// Deleted: TestSysagentTaskUpdate_A4_StatusActiveRejected — the A4 guard that
// rejected status="active" was removed when the 7-state vocabulary (since
// reduced to 6-state, ADR-051 D5) replaced the old board vocab; "active" is
// simply not in the enum, so it is now rejected as unrecognized (see above),
// not silently accepted.
// Deleted: TestSysagentTaskUpdate_A4_Differentiation — same reason.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// testWorkspaceID is the workspace ID seeded in test helpers.
const testWorkspaceID = "01JXTEST_WORKSPACE000001"

// seedWorkspace writes a minimal workspace JSON file so system.task.create
// (which validates workspace_id existence) can find it.
func seedWorkspace(t *testing.T, home, id string) {
	t.Helper()
	wsDir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)
	ws := map[string]any{
		"id":         id,
		"name":       "Test Workspace",
		"status":     "active",
		"created_at": now,
		"updated_at": now,
	}
	data, err := json.Marshal(ws)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, id+".json"), data, 0o600))
}

// TestSysagentTaskUpdate_InvalidStatusRejected verifies that an invalid
// status value (one not in the 6-state vocabulary, ADR-051 D5) is REJECTED
// with a clear INVALID_INPUT error: the tool returns IsError=true and the
// task's on-disk status is left unchanged.
//
// BDD:
//
//	Given a task in status=inbox,
//	When system.task.update is called with {"id":..., "status":"active"},
//	Then the result has IsError=true, naming the unrecognized status and the
//	  6 valid values,
//	And the task's on-disk status is still "inbox".
//
// Traces to: UAT batch3 S58 (finding #2) — TaskUpdateTool.Execute's status
// branch used to have no else clause for an unrecognized value, silently
// no-opping instead of erroring like TaskCreateTool.Execute does for the
// identical input. Renamed from *_InvalidStatusIgnored (the old, pre-fix
// name pinned the bug itself).
func TestSysagentTaskUpdate_InvalidStatusRejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const taskID = "01JXSTATUSGUARD_INVAL0001"
	// "active" is NOT in the 6-state vocab.
	seedTask(t, home, taskID, "WriteTests", task.StatusInbox, nil)

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(callerCtx("caller-agent"), map[string]any{
		"id":     taskID,
		"status": "active",
	})

	require.True(t, result.IsError,
		"system.task.update with an unknown status MUST return IsError — a silent no-op hides the failed request")
	assert.Contains(t, result.ForLLM, "active",
		"error message must name the rejected status value")
	assert.Contains(t, result.ForLLM, "inbox, next, blocked, done, failed",
		"error message must enumerate the valid status values, mirroring TaskCreateTool")

	// On-disk status must remain "inbox" (the invalid status was not applied).
	assert.Equal(t, task.StatusInbox, diskTaskStatus(t, home, taskID),
		"on-disk status must still be 'inbox' — the rejected status must not be applied")
}

// TestSysagentTaskUpdate_RemovedPlanningStatusRejected verifies that the
// removed "planning" status (ADR-051 D5) is treated exactly like any other
// unknown status value by the sysagent update tool: rejected with
// IsError=true, not applied. Canary catching a regression that re-introduces
// "planning" as a live status OR reverts to the pre-fix silent-ignore
// behavior.
//
// Traces to: pkg/task/task.go validStatuses (ADR-051 D5 removed StatusPlanning);
// UAT batch3 S58 (finding #2).
func TestSysagentTaskUpdate_RemovedPlanningStatusRejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const taskID = "01JXSTATUSGUARD_PLAN0001"
	seedTask(t, home, taskID, "WriteTests", task.StatusInbox, nil)

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(callerCtx("caller-agent"), map[string]any{
		"id":     taskID,
		"status": "planning",
	})

	require.True(t, result.IsError,
		"system.task.update with status=planning MUST return IsError — it is not a valid status")
	assert.Equal(t, task.StatusInbox, diskTaskStatus(t, home, taskID),
		"on-disk status must still be 'inbox' — the removed 'planning' status must not be applied")
}

// TestSysagentTaskUpdate_ValidStatusApplied verifies that a valid 6-state status
// (status="next") is applied successfully and the on-disk status is updated.
//
// BDD:
//
//	Given a task in status=inbox,
//	When system.task.update is called with {"id":..., "status":"next"},
//	Then the result has IsError=false,
//	And the task's on-disk status is "next".
//
// Traces to: Sprint-2 unified task store — valid status transition applied.
func TestSysagentTaskUpdate_ValidStatusApplied(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const taskID = "01JXSTATUSGUARD_VALID0001"
	seedTask(t, home, taskID, "DeployService", task.StatusInbox, nil)

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(callerCtx("caller-agent"), map[string]any{
		"id":     taskID,
		"status": "next",
	})

	assert.False(t, result.IsError,
		"system.task.update with status='next' must return IsError=false; got: %s", result.ForLLM)

	assert.Equal(t, task.StatusNext, diskTaskStatus(t, home, taskID),
		"on-disk status must be 'next' after a successful update")
}

// TestSysagentTaskUpdate_AllSettableStatusesApplied verifies that every directly
// settable status in the 6-state vocabulary (ADR-051 D5) can be set via
// system.task.update (differentiation test: each produces a different on-disk
// status, proving no hardcoding).
//
// `blocked` is deliberately EXCLUDED here: it is a derived side-state that the
// store enters/leaves only through the dependency-recompute hatch, and a direct
// wire write to `blocked` is rejected with ErrBlockedNotSettable. That guard is
// covered separately by TestSysagentTaskUpdate_BlockedNotSettable below.
//
// `in_progress` is ALSO excluded as of issue #593 (Option A): it is a DISPATCH
// state reached only through real execution (run_task / the executor's
// ClaimForRun / REST's handleTaskPatch / set_todos-via-Create) — a direct
// system.task.update write into in_progress from any other status is now
// rejected, mirroring the `blocked` derived-state guard in spirit (not
// caller-settable) though the mechanism differs (an explicit forge-rejection
// check, not ErrBlockedNotSettable). That guard is covered separately by
// TestSysagentTaskUpdate_InProgressForgeRejected below.
//
// Traces to: Sprint-2 unified task store — the four remaining directly-settable
// statuses are writable; `blocked` is derived (pkg/task/store.go
// ErrBlockedNotSettable) and `in_progress` is dispatch-only (issue #593).
func TestSysagentTaskUpdate_AllSettableStatusesApplied(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	// All six canonical statuses EXCEPT the derived `blocked` side-state and
	// the dispatch-only `in_progress` state (issue #593).
	statuses := []task.Status{
		task.StatusInbox,
		task.StatusNext,
		task.StatusDone,
		task.StatusFailed,
	}

	for _, st := range statuses {
		id := "01JXSTATUSGUARD_" + string(st)[:4] + "00001"
		// Seed at a different starting status so the update produces a real change.
		// `inbox` is the universal seed; when the target IS inbox, seed `next` so
		// the write still changes the on-disk value. Every from→to pair used here
		// is a legal transition (only `done` is frozen and only `blocked` is gated,
		// neither of which we transition OUT of in this table).
		var seedStatus task.Status
		if st == task.StatusInbox {
			seedStatus = task.StatusNext
		} else {
			seedStatus = task.StatusInbox
		}
		seedTask(t, home, id, "Task for "+string(st), seedStatus, nil)

		tool := systools.NewTaskUpdateTool(deps)
		result := tool.Execute(callerCtx("caller-agent"), map[string]any{
			"id":     id,
			"status": string(st),
		})

		assert.False(t, result.IsError,
			"system.task.update with status=%q must succeed; got: %s", st, result.ForLLM)
		assert.Equal(t, st, diskTaskStatus(t, home, id),
			"on-disk status must be %q after update", st)
	}
}

// TestSysagentTaskUpdate_BlockedNotSettable verifies that `blocked` — a derived
// side-state — CANNOT be set directly through system.task.update. The store
// enters `blocked` only via the dependency engine (unmet blocked_by) and clears
// it to `next` when every blocker reaches done; a direct wire write is rejected
// with ErrBlockedNotSettable and the on-disk status is left unchanged.
//
// BDD:
//
//	Given a task in status=inbox,
//	When system.task.update is called with {"id":..., "status":"blocked"},
//	Then the result has IsError=true (the derived-side-state guard fires),
//	And the task's on-disk status is still "inbox" (the write did not apply).
//
// Traces to: Sprint-2 unified task store — `blocked` is derived, never settable
// directly (pkg/task/store.go ErrBlockedNotSettable).
func TestSysagentTaskUpdate_BlockedNotSettable(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const taskID = "01JXSTATUSGUARD_BLOCK0001"
	seedTask(t, home, taskID, "Task for blocked", task.StatusInbox, nil)

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(callerCtx("caller-agent"), map[string]any{
		"id":     taskID,
		"status": string(task.StatusBlocked),
	})

	assert.True(t, result.IsError,
		"system.task.update with status='blocked' must be rejected (derived side-state); got: %s", result.ForLLM)
	assert.Equal(t, task.StatusInbox, diskTaskStatus(t, home, taskID),
		"on-disk status must remain 'inbox' — the rejected blocked write must not apply")
}

// TestSysagentTaskUpdate_InProgressForgeRejected is the issue #593 regression
// test for update_task_in_workspace — the MORE permissive twin of the plain
// update_task tool flagged as the same hole (it can mutate another agent's
// task once the delegation gate clears, unlike the plain tool's strict
// assignee/creator-only ownership union).
//
//   - (a) the delegator/creator of a `next` task (agent-namespaced
//     CreatedByAgentID, distinct from the assignee) calling
//     system.task.update(status:"in_progress") is REJECTED with a message
//     naming run_task, and the task is unchanged on disk (re-read, not just a
//     non-nil error).
//   - (b) positive control: status:"failed" and a plain name (title) edit
//     both still succeed and persist.
//   - (c) a task that is ALREADY in_progress is not broken by an unrelated
//     (non-status) update, and a same-status resend is a harmless no-op.
func TestSysagentTaskUpdate_InProgressForgeRejected(t *testing.T) {
	t.Run("creator forging next to in_progress is rejected, task unchanged on disk", func(t *testing.T) {
		deps, home := newTestDepsWithHome(t)
		seedWorkspace(t, home, testWorkspaceID)

		const taskID = "01JXSTATUSGUARD_FORGE0001"
		// Delegator "mia" dispatched this task to "worker" via the AGENT path
		// (CreatedByAgentID stamped) — mirroring the issue #593 UAT repro. The
		// creator passes the ownership union check even though the task is
		// assigned to someone else.
		writeTask(t, home, task.Task{
			ID: taskID, Title: "delegated work", Status: task.StatusNext,
			WorkspaceID: testWorkspaceID, AgentID: "worker", CreatedByAgentID: "mia",
		})

		tool := systools.NewTaskUpdateTool(deps)
		result := tool.Execute(callerCtx("mia"), map[string]any{
			"id":     taskID,
			"status": "in_progress",
		})

		assert.True(t, result.IsError,
			"expected the forged in_progress write to be rejected, got success: %s", result.ForLLM)
		assert.Contains(t, result.ForLLM, "run_task",
			"expected rejection message to name run_task as the correct path")

		store := task.New(filepath.Join(home, "tasks"))
		got, err := store.Get(taskID)
		require.NoError(t, err)
		assert.Equal(t, task.StatusNext, got.Status, "task must remain 'next' on disk")
		assert.Empty(t, got.StartedAt, "started_at must NOT be stamped by a rejected write")
	})

	t.Run("positive control: failed and name edits still work", func(t *testing.T) {
		deps, home := newTestDepsWithHome(t)

		const taskID = "01JXSTATUSGUARD_FORGE0002"
		seedTask(t, home, taskID, "original name", task.StatusNext, nil)

		tool := systools.NewTaskUpdateTool(deps)

		nameResult := tool.Execute(callerCtx("caller-agent"), map[string]any{
			"id":   taskID,
			"name": "renamed",
		})
		require.False(t, nameResult.IsError, "name edit must succeed: %s", nameResult.ForLLM)

		failResult := tool.Execute(callerCtx("caller-agent"), map[string]any{
			"id":     taskID,
			"status": "failed",
			"result": "gave up",
		})
		require.False(t, failResult.IsError, "status:failed must succeed: %s", failResult.ForLLM)
		assert.Equal(t, task.StatusFailed, diskTaskStatus(t, home, taskID))

		store := task.New(filepath.Join(home, "tasks"))
		got, err := store.Get(taskID)
		require.NoError(t, err)
		assert.Equal(t, "renamed", got.Title, "name edit must have persisted")
	})

	t.Run("already in_progress: unrelated update is not broken", func(t *testing.T) {
		deps, home := newTestDepsWithHome(t)

		const taskID = "01JXSTATUSGUARD_FORGE0003"
		seedTask(t, home, taskID, "already running", task.StatusInProgress, nil)

		tool := systools.NewTaskUpdateTool(deps)

		// Unrelated field edit must succeed and leave status alone.
		nameResult := tool.Execute(callerCtx("caller-agent"), map[string]any{
			"id":   taskID,
			"name": "still running, renamed",
		})
		require.False(t, nameResult.IsError,
			"unrelated update on an already-in_progress task must succeed: %s", nameResult.ForLLM)
		assert.Equal(t, task.StatusInProgress, diskTaskStatus(t, home, taskID))

		// A same-status resend is a no-op, not a forge, and must be allowed.
		resendResult := tool.Execute(callerCtx("caller-agent"), map[string]any{
			"id":     taskID,
			"status": "in_progress",
		})
		require.False(t, resendResult.IsError,
			"a same-status resend on an already in_progress task must not be rejected: %s", resendResult.ForLLM)
		assert.Equal(t, task.StatusInProgress, diskTaskStatus(t, home, taskID))
	})
}

// TestSysagentTaskUpdate_WorkspaceIDUpdatedFieldsAccurate verifies the
// second half of UAT batch3 S58 (finding #2): `updated_fields` must list
// "workspace_id" only when the value genuinely changed the task's
// workspace, not merely because the caller resupplied the key (including an
// unchanged value).
//
// BDD:
//
//	Given a task already in workspace A,
//	When system.task.update resupplies workspace_id=A (the SAME value),
//	Then `updated_fields` does NOT contain "workspace_id" (nothing changed),
//	And the task's on-disk workspace_id is still A;
//	When a later call supplies workspace_id=B (a DIFFERENT, real workspace),
//	Then `updated_fields` DOES contain "workspace_id",
//	And the task's on-disk workspace_id is now B.
func TestSysagentTaskUpdate_WorkspaceIDUpdatedFieldsAccurate(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const workspaceA = "01JXTESTWORKSPACEAAAA0001"
	const workspaceB = "01JXTESTWORKSPACEBBBB0001"
	seedWorkspace(t, home, workspaceA)
	seedWorkspace(t, home, workspaceB)

	const taskID = "01JXSTATUSGUARD_WSID0001"
	writeTask(t, home, task.Task{
		ID: taskID, Title: "move me maybe", Status: task.StatusInbox,
		WorkspaceID: workspaceA,
	})

	tool := systools.NewTaskUpdateTool(deps)

	// Resupplying the SAME workspace_id must be a no-op: not listed in
	// updated_fields, and no write performed.
	sameResult := tool.Execute(callerCtx("caller-agent"), map[string]any{
		"id":           taskID,
		"workspace_id": workspaceA,
	})
	require.False(t, sameResult.IsError, "resupplying the same workspace_id must succeed: %s", sameResult.ForLLM)

	var sameResp struct {
		UpdatedFields []string `json:"updated_fields"`
	}
	require.NoError(t, json.Unmarshal([]byte(sameResult.ForLLM), &sameResp))
	assert.NotContains(t, sameResp.UpdatedFields, "workspace_id",
		"updated_fields must NOT list workspace_id when the resupplied value did not change anything")

	store := task.New(filepath.Join(home, "tasks"))
	afterSame, err := store.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, workspaceA, afterSame.WorkspaceID, "workspace_id must remain A after the no-op resend")

	// Supplying a GENUINELY DIFFERENT workspace_id must be reported and applied.
	moveResult := tool.Execute(callerCtx("caller-agent"), map[string]any{
		"id":           taskID,
		"workspace_id": workspaceB,
	})
	require.False(t, moveResult.IsError, "moving to a different workspace must succeed: %s", moveResult.ForLLM)

	var moveResp struct {
		UpdatedFields []string `json:"updated_fields"`
	}
	require.NoError(t, json.Unmarshal([]byte(moveResult.ForLLM), &moveResp))
	assert.Contains(t, moveResp.UpdatedFields, "workspace_id",
		"updated_fields must list workspace_id when the task's workspace genuinely changed")

	afterMove, err := store.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, workspaceB, afterMove.WorkspaceID, "workspace_id must be B after the genuine move")
}

// TestSysagentTaskUpdate_Differentiation verifies that two different valid
// status values produce two different on-disk outcomes (not hardcoded).
//
// Traces to: Sprint-2 unified task store — differentiation: different inputs → different outputs.
func TestSysagentTaskUpdate_Differentiation(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const inboxTaskID = "01JXSTATUSGUARD_DIFF_IN01"
	const nextTaskID = "01JXSTATUSGUARD_DIFF_NX01"
	seedTask(t, home, inboxTaskID, "TaskForInbox", task.StatusNext, nil)
	seedTask(t, home, nextTaskID, "TaskForNext", task.StatusInbox, nil)

	tool := systools.NewTaskUpdateTool(deps)

	resultInbox := tool.Execute(callerCtx("caller-agent"), map[string]any{
		"id":     inboxTaskID,
		"status": "inbox",
	})
	resultNext := tool.Execute(callerCtx("caller-agent"), map[string]any{
		"id":     nextTaskID,
		"status": "next",
	})

	assert.False(t, resultInbox.IsError, "inbox update must succeed")
	assert.False(t, resultNext.IsError, "next update must succeed")

	assert.Equal(t, task.StatusInbox, diskTaskStatus(t, home, inboxTaskID),
		"first task must have status=inbox on disk")
	assert.Equal(t, task.StatusNext, diskTaskStatus(t, home, nextTaskID),
		"second task must have status=next on disk")
	assert.NotEqual(t,
		diskTaskStatus(t, home, inboxTaskID),
		diskTaskStatus(t, home, nextTaskID),
		"two different status updates must produce two different on-disk statuses (not hardcoded)",
	)
}
