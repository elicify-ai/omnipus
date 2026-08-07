// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// FR-6.5: completing a task via system.task.update un-gates its waiting
// dependents (blocked→next) once all blocked_by deps are done.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedTask writes a task.Task directly to disk at home/tasks/<id>.json.
// This replaces the old seedGTDTask which used the deleted boardtask.Task type.
func seedTask(t *testing.T, home, id, title string, status task.Status, blockedBy []string) {
	t.Helper()
	tasksDir := filepath.Join(home, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)
	tk := task.Task{
		ID:          id,
		Title:       title,
		Status:      status,
		Action:      task.ActionLLM,
		WorkspaceID: testWorkspaceID,
		BlockedBy:   blockedBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	data, err := json.Marshal(tk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, id+".json"), data, 0o600))
}

// diskTaskStatus reads the on-disk task.Status for the task with the given id.
func diskTaskStatus(t *testing.T, home, id string) task.Status {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "tasks", id+".json"))
	require.NoError(t, err)
	var tk task.Task
	require.NoError(t, json.Unmarshal(data, &tk))
	return tk.Status
}

// TestSysagentTaskUpdate_CompleteAdvancesDependent verifies that setting a task
// to "done" via system.task.update advances a blocked dependent to next.
//
// Traces to: FR-6.5 — completing a task un-gates its blocked dependents.
func TestSysagentTaskUpdate_CompleteAdvancesDependent(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const blockerID = "01JXADVANCE_BLOCKER000001"
	const depID = "01JXADVANCE_DEPENDENT0001"

	// Blocker is "next" (ready to be completed via the tool).
	// Dependent is "blocked" with blocked_by=[blockerID].
	seedTask(t, home, blockerID, "Blocker", task.StatusNext, nil)
	seedTask(t, home, depID, "Dependent", task.StatusBlocked, []string{blockerID})

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(callerCtx("caller-agent"), map[string]any{
		"id":     blockerID,
		"status": "done",
	})
	require.False(t, result.IsError, "completing blocker must succeed; got %s", result.ForLLM)

	require.Equal(t, task.StatusNext, diskTaskStatus(t, home, depID),
		"dependent should be advanced blocked→next after blocker completes")
}

// TestSysagentTaskUpdate_CompleteWithUnsatisfiedDepStaysBlocked verifies a
// dependent with a still-incomplete second blocker stays blocked.
//
// Traces to: FR-6.5 — all deps must be done before a blocked task advances.
func TestSysagentTaskUpdate_CompleteWithUnsatisfiedDepStaysBlocked(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const aID = "01JXADVANCE_A00000000001"
	const xID = "01JXADVANCE_X00000000001"
	const cID = "01JXADVANCE_C00000000001"

	seedTask(t, home, aID, "Blocker A", task.StatusNext, nil)
	seedTask(t, home, xID, "Blocker X", task.StatusNext, nil)
	seedTask(t, home, cID, "Dependent C", task.StatusBlocked, []string{aID, xID})

	tool := systools.NewTaskUpdateTool(deps)
	res := tool.Execute(callerCtx("caller-agent"), map[string]any{"id": aID, "status": "done"})
	require.False(t, res.IsError, "completing A must succeed; got %s", res.ForLLM)

	require.Equal(t, task.StatusBlocked, diskTaskStatus(t, home, cID),
		"C must stay blocked while X is not done")
}
