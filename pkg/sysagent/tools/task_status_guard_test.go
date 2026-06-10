// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// H3: A4 sysagent status-guard tests for feat/level1-project-task-mgmt.
//
// The A4 guard in TaskUpdateTool.Execute rejects status="active" — only /start
// may set that value. This prevents an agent from bypassing the striped-mutex
// check-and-set sequence by writing "active" directly via system.task.update.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/boardtask"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
)

// seedGTDTask writes a minimal GTD task to disk for use in tool tests.
func seedGTDTask(t *testing.T, home, id, name, status string) {
	t.Helper()
	tasksDir := filepath.Join(home, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)
	tk := boardtask.Task{
		ID:        id,
		Name:      name,
		Status:    boardtask.Status(status),
		CreatedAt: now,
		UpdatedAt: now,
	}
	data, err := json.Marshal(tk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, id+".json"), data, 0o600))
}

// TestSysagentTaskUpdate_A4_StatusActiveRejected verifies that the A4 guard in
// TaskUpdateTool.Execute returns IsError=true when the caller tries to set
// status="active" directly, and that the task's status is not changed.
//
// BDD:
//
//	Given a GTD task in status=inbox,
//	When system.task.update is called with {"id":..., "status":"active"},
//	Then the result has IsError=true,
//	And the result message contains "active",
//	And the task's on-disk status is still "inbox".
//
// Traces to: project-task-management-level1-spec.md — A4 (sysagent cannot set active)
func TestSysagentTaskUpdate_A4_StatusActiveRejected(t *testing.T) {
	// Traces to: A4 — TaskUpdateTool must reject status="active" with IsError=true.
	deps, home := newTestDepsWithHome(t)

	const taskID = "01JXSTATUSGUARD_ACTIVE0001"
	seedGTDTask(t, home, taskID, "WriteTests", "inbox")

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"id":     taskID,
		"status": "active",
	})

	assert.True(t, result.IsError,
		"system.task.update with status='active' must return IsError=true")
	assert.Contains(t, result.ForLLM, "active",
		"error message must mention 'active' to explain what went wrong")

	// On-disk status must remain "inbox" (guard must not have written the file).
	var onDisk boardtask.Task
	data, err := os.ReadFile(filepath.Join(home, "tasks", taskID+".json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &onDisk))
	assert.Equal(t, boardtask.StatusInbox, onDisk.Status,
		"on-disk status must still be 'inbox' — the A4 guard must not write the file")
}

// TestSysagentTaskUpdate_A4_StatusNextAllowed verifies the positive control:
// status="next" must succeed (IsError=false) and the task must be updated.
//
// BDD:
//
//	Given a GTD task in status=inbox,
//	When system.task.update is called with {"id":..., "status":"next"},
//	Then the result has IsError=false,
//	And the task's on-disk status is "next".
//
// Traces to: project-task-management-level1-spec.md — A4 positive control
func TestSysagentTaskUpdate_A4_StatusNextAllowed(t *testing.T) {
	// Traces to: A4 positive control — "next" is a valid transition via the tool.
	deps, home := newTestDepsWithHome(t)

	const taskID = "01JXSTATUSGUARD_NEXT00001"
	seedGTDTask(t, home, taskID, "DeployService", "inbox")

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"id":     taskID,
		"status": "next",
	})

	assert.False(t, result.IsError,
		"system.task.update with status='next' must return IsError=false; got: %s", result.ForLLM)

	// On-disk status must be "next".
	var onDisk boardtask.Task
	data, err := os.ReadFile(filepath.Join(home, "tasks", taskID+".json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &onDisk))
	assert.Equal(t, boardtask.StatusNext, onDisk.Status,
		"on-disk status must be 'next' after a successful update")
}

// TestSysagentTaskUpdate_A4_Differentiation verifies that setting status="active"
// and status="next" produce different outcomes (not both IsError=true, not hardcoded).
//
// Traces to: project-task-management-level1-spec.md — A4 differentiation
func TestSysagentTaskUpdate_A4_Differentiation(t *testing.T) {
	// Traces to: A4 — two different status values produce two different tool outcomes.
	deps, home := newTestDepsWithHome(t)

	const activeTaskID = "01JXSTATUSGUARD_DIFFA0001"
	const nextTaskID = "01JXSTATUSGUARD_DIFFN0001"
	seedGTDTask(t, home, activeTaskID, "TaskForActive", "inbox")
	seedGTDTask(t, home, nextTaskID, "TaskForNext", "inbox")

	tool := systools.NewTaskUpdateTool(deps)

	resultActive := tool.Execute(context.Background(), map[string]any{
		"id":     activeTaskID,
		"status": "active",
	})
	resultNext := tool.Execute(context.Background(), map[string]any{
		"id":     nextTaskID,
		"status": "next",
	})

	assert.True(t, resultActive.IsError,
		"active must produce an error")
	assert.False(t, resultNext.IsError,
		"next must not produce an error")
	assert.NotEqual(t, resultActive.IsError, resultNext.IsError,
		"active and next must produce different IsError outcomes (not both true, not hardcoded)")
}
