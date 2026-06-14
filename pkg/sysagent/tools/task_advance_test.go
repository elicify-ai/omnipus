// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// FR-6.5: completing a board task via system.task.update un-gates its waiting
// dependents (waiting→next) once all blocked_by deps are done.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/boardtask"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
)

// seedGTDTaskBlockedBy writes a GTD task with a blocked_by list.
func seedGTDTaskBlockedBy(t *testing.T, home, id, name, status string, blockedBy []string) {
	t.Helper()
	tasksDir := filepath.Join(home, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)
	tk := boardtask.Task{
		ID:        id,
		Name:      name,
		Status:    boardtask.Status(status),
		BlockedBy: blockedBy,
		CreatedAt: now,
		UpdatedAt: now,
	}
	data, err := json.Marshal(tk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, id+".json"), data, 0o600))
}

func diskStatus(t *testing.T, home, id string) boardtask.Status {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "tasks", id+".json"))
	require.NoError(t, err)
	var tk boardtask.Task
	require.NoError(t, json.Unmarshal(data, &tk))
	return tk.Status
}

// TestSysagentTaskUpdate_CompleteAdvancesDependent verifies that setting a board
// task to "done" via system.task.update advances a waiting dependent to next.
func TestSysagentTaskUpdate_CompleteAdvancesDependent(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const blockerID = "01JXADVANCE_BLOCKER000001"
	const depID = "01JXADVANCE_DEPENDENT0001"
	seedGTDTask(t, home, blockerID, "Blocker", "next")
	seedGTDTaskBlockedBy(t, home, depID, "Dependent", "waiting", []string{blockerID})

	tool := systools.NewTaskUpdateTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"id":     blockerID,
		"status": "done",
	})
	require.False(t, result.IsError, "completing blocker must succeed; got %s", result.ForLLM)

	require.Equal(t, boardtask.StatusNext, diskStatus(t, home, depID),
		"dependent should be advanced waiting→next after blocker completes")
}

// TestSysagentTaskUpdate_CompleteWithUnsatisfiedDepStaysWaiting verifies a
// dependent with a still-incomplete second blocker stays waiting.
func TestSysagentTaskUpdate_CompleteWithUnsatisfiedDepStaysWaiting(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	const aID = "01JXADVANCE_A00000000001"
	const xID = "01JXADVANCE_X00000000001"
	const cID = "01JXADVANCE_C00000000001"
	seedGTDTask(t, home, aID, "Blocker A", "next")
	seedGTDTask(t, home, xID, "Blocker X", "next")
	seedGTDTaskBlockedBy(t, home, cID, "Dependent C", "waiting", []string{aID, xID})

	tool := systools.NewTaskUpdateTool(deps)
	res := tool.Execute(context.Background(), map[string]any{"id": aID, "status": "done"})
	require.False(t, res.IsError, "completing A must succeed; got %s", res.ForLLM)

	require.Equal(t, boardtask.StatusWaiting, diskStatus(t, home, cID),
		"C must stay waiting while X is not done")
}
