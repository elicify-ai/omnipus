// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// H3: sysagent status-guard tests for the unified 6-state task vocabulary
// (ADR-051 D5 removed `planning`, formerly 7-state).
//
// The old A4 guard ("status=active only via /start") has been REMOVED in Sprint
// 2: the new store accepts any of the 6 valid statuses without a transition
// machine. Invalid status values (not in the 6-state vocab) are silently ignored
// by the update tool — the tool still returns success, but the status is left
// unchanged. This file verifies that behavior.
//
// Deleted: TestSysagentTaskUpdate_A4_StatusActiveRejected — the A4 guard that
// rejected status="active" was removed when the 7-state vocabulary (since
// reduced to 6-state, ADR-051 D5) replaced the old board vocab; "active" is
// simply not in the enum, so it is ignored.
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

// TestSysagentTaskUpdate_InvalidStatusIgnored verifies that an invalid status
// value (one not in the 6-state vocabulary, ADR-051 D5) is silently ignored:
// the tool returns IsError=false and the task's on-disk status is unchanged.
//
// BDD:
//
//	Given a task in status=inbox,
//	When system.task.update is called with {"id":..., "status":"active"},
//	Then the result has IsError=false (no guard error — invalid status is ignored),
//	And the task's on-disk status is still "inbox".
//
// Traces to: Sprint-2 unified task store — invalid status silently ignored
// (isValidTaskStatus gate in task.go Execute).
func TestSysagentTaskUpdate_InvalidStatusIgnored(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const taskID = "01JXSTATUSGUARD_INVAL0001"
	// "active" is NOT in the 6-state vocab: it is silently ignored by the new tool.
	seedTask(t, home, taskID, "WriteTests", task.StatusInbox, nil)

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(callerCtx("caller-agent"), map[string]any{
		"id":     taskID,
		"status": "active",
	})

	// The tool no longer guards against "active" — it simply skips it.
	assert.False(t, result.IsError,
		"system.task.update with an unknown status must NOT return IsError (invalid status is silently ignored)")

	// On-disk status must remain "inbox" (the invalid status was not applied).
	assert.Equal(t, task.StatusInbox, diskTaskStatus(t, home, taskID),
		"on-disk status must still be 'inbox' — the invalid status was silently ignored")
}

// TestSysagentTaskUpdate_RemovedPlanningStatusIgnored verifies that the
// removed "planning" status (ADR-051 D5) is treated exactly like any other
// unknown status value by the sysagent update tool: silently ignored, not
// applied, no error. Canary catching a regression that re-introduces
// "planning" as a live status.
//
// Traces to: pkg/task/task.go validStatuses (ADR-051 D5 removed StatusPlanning).
func TestSysagentTaskUpdate_RemovedPlanningStatusIgnored(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const taskID = "01JXSTATUSGUARD_PLAN0001"
	seedTask(t, home, taskID, "WriteTests", task.StatusInbox, nil)

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(callerCtx("caller-agent"), map[string]any{
		"id":     taskID,
		"status": "planning",
	})

	assert.False(t, result.IsError,
		"system.task.update with status=planning must NOT return IsError (invalid status is silently ignored)")
	assert.Equal(t, task.StatusInbox, diskTaskStatus(t, home, taskID),
		"on-disk status must still be 'inbox' — the removed 'planning' status was silently ignored, not applied")
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
// Traces to: Sprint-2 unified task store — the five directly-settable statuses
// are writable; `blocked` is derived (pkg/task/store.go ErrBlockedNotSettable).
func TestSysagentTaskUpdate_AllSettableStatusesApplied(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	// All six canonical statuses EXCEPT the derived `blocked` side-state.
	statuses := []task.Status{
		task.StatusInbox,
		task.StatusNext,
		task.StatusInProgress,
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
