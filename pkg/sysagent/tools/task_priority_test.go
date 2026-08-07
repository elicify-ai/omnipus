// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// task_priority_test.go — regression coverage for the M2/M7 fixes on the
// privileged cross-workspace task tools (create_task_in_workspace,
// update_task_in_workspace, list_tasks_in_workspace).
//
// M2: priority validation must reject an explicit out-of-range value
// (including 0) on BOTH create and update, matching the range check REST and
// the plain create_task/update_task tools enforce (task.ValidatePriority is
// the single shared helper all of them call).
//
// M7: priority, stream, and write_set — all accepted by
// create_task_in_workspace — must be observable back through
// list_tasks_in_workspace; before this fix they were silently dropped from
// the read surface, which is exactly what let an M2-shaped bug go unnoticed
// (no read path existed to catch a wrong persisted value).
//
// Traces to: docs/internal/uat/uat-report-adr057-CONSOLIDATED-2026-08-03.md M2/M7.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestCreateTaskInWorkspace_PriorityBoundaryMatrix covers the full create-path
// matrix: 0 (explicit-zero, rejected), 1 and 5 (valid bounds), 6 and a
// negative value (rejected), and absent (accepted, defaults to 3 on read via
// EffectivePriority). Binding Rule 4: every rejection is paired with an
// acceptance at the same boundary.
func TestCreateTaskInWorkspace_PriorityBoundaryMatrix(t *testing.T) {
	cases := []struct {
		name         string
		priority     any // float64 value, or nil to omit the arg entirely
		wantErr      bool
		wantPriority int // checked only when !wantErr
	}{
		{"explicit_zero_rejected", float64(0), true, 0},
		{"one_lower_bound_valid", float64(1), false, 1},
		{"five_upper_bound_valid", float64(5), false, 5},
		{"six_rejected", float64(6), true, 0},
		{"negative_rejected", float64(-1), true, 0},
		{"absent_defaults_to_three", nil, false, 3},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps, home := newTestDepsWithHome(t)
			seedWorkspace(t, home, testWorkspaceID)

			args := map[string]any{
				"name":         "priority matrix " + c.name,
				"workspace_id": testWorkspaceID,
			}
			if c.priority != nil {
				args["priority"] = c.priority
			}

			result := systools.NewTaskCreateTool(deps).Execute(callerCtx("caller-agent"), args)

			if c.wantErr {
				require.True(t, result.IsError, "expected an error for priority %v", c.priority)
				assert.Contains(t, result.ForLLM, "priority must be between 1 and 5",
					"expected the shared priority validation message")
				// Nothing must have been persisted for a rejected create.
				rows, err := task.New(home + "/tasks").List(task.Filter{WorkspaceID: testWorkspaceID})
				require.NoError(t, err)
				assert.Empty(t, rows, "a rejected create must not persist a task")
				return
			}

			require.False(t, result.IsError, "create_task_in_workspace: %s", result.ForLLM)
			var out struct {
				ID string `json:"id"`
			}
			require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &out))
			got, err := task.New(home + "/tasks").Get(out.ID)
			require.NoError(t, err)
			assert.Equal(t, c.wantPriority, got.EffectivePriority(),
				"persisted (effective) priority mismatch")
		})
	}
}

// TestUpdateTaskInWorkspace_PriorityBoundaryMatrix mirrors the create matrix
// for update_task_in_workspace, whose tool layer has no inline range check at
// all — it relies entirely on task.Store.Update's Patch-based validation
// (already correct pre-fix, since Patch.Priority is a *int). This test locks
// that contract in explicitly so a future refactor of either layer cannot
// silently regress it.
func TestUpdateTaskInWorkspace_PriorityBoundaryMatrix(t *testing.T) {
	cases := []struct {
		name         string
		priority     any
		wantErr      bool
		wantPriority int
	}{
		{"explicit_zero_rejected", float64(0), true, 2},
		{"one_lower_bound_valid", float64(1), false, 1},
		{"five_upper_bound_valid", float64(5), false, 5},
		{"six_rejected", float64(6), true, 2},
		{"negative_rejected", float64(-1), true, 2},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps, home := newTestDepsWithHome(t)
			seedWorkspace(t, home, testWorkspaceID)
			const taskID = "01JXPRIORITYMATRIX000001"
			seedTask(t, home, taskID, "priority update matrix", task.StatusInbox, nil)
			// Seed a known starting priority (2) directly, so a rejected update's
			// "unchanged" assertion is meaningful (0 would be ambiguous with unset).
			store := task.New(home + "/tasks")
			two := 2
			_, err := store.Update(taskID, task.Patch{Priority: &two})
			require.NoError(t, err)

			result := systools.NewTaskUpdateTool(deps).Execute(callerCtx("caller-agent"), map[string]any{
				"id":       taskID,
				"priority": c.priority,
			})

			if c.wantErr {
				require.True(t, result.IsError, "expected an error for priority %v", c.priority)
				assert.Contains(t, result.ForLLM, "priority must be between 1 and 5",
					"expected the shared priority validation message")
			} else {
				require.False(t, result.IsError, "update_task_in_workspace: %s", result.ForLLM)
			}

			got, gerr := store.Get(taskID)
			require.NoError(t, gerr)
			assert.Equal(t, c.wantPriority, got.Priority, "priority mismatch after update attempt")
		})
	}
}

// TestListTasksInWorkspace_SurfacesPriorityStreamWriteSet is the M7
// regression test: priority, stream, and write_set — all accepted by
// create_task_in_workspace — must round-trip through list_tasks_in_workspace
// so a caller can actually verify what was persisted.
func TestListTasksInWorkspace_SurfacesPriorityStreamWriteSet(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)

	ctx := callerCtx("caller-agent")
	createResult := systools.NewTaskCreateTool(deps).Execute(ctx, map[string]any{
		"name":         "rich params task",
		"workspace_id": testWorkspaceID,
		"priority":     float64(2),
		"stream":       "uat-stream",
		"write_set":    []any{"uat-output.txt"},
	})
	require.False(t, createResult.IsError, "create_task_in_workspace: %s", createResult.ForLLM)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(createResult.ForLLM), &created))

	env := runWorkspaceList(t, deps, ctx, map[string]any{"workspace_id": testWorkspaceID})
	require.Len(t, env.Tasks, 1)
	row := env.Tasks[0]

	require.Equal(t, created.ID, row["id"])
	require.Contains(t, row, "priority", "priority must be observable on the read surface (M7)")
	assert.Equal(t, float64(2), row["priority"], "priority must round-trip exactly")
	require.Contains(t, row, "stream", "stream must be observable on the read surface (M7)")
	assert.Equal(t, "uat-stream", row["stream"], "stream must round-trip exactly")
	require.Contains(t, row, "write_set", "write_set must be observable on the read surface (M7)")
	writeSet, ok := row["write_set"].([]any)
	require.True(t, ok, "write_set must be an array, got %T", row["write_set"])
	require.Len(t, writeSet, 1)
	assert.Equal(t, "uat-output.txt", writeSet[0])
}

// TestListTasksInWorkspace_PriorityDefaultsToThreeWhenUnset proves the M7 fix
// reports a meaningful priority (3, via EffectivePriority) rather than 0/absent
// for a task created without an explicit priority — matching the REST read
// surface's own EffectivePriority-based default.
func TestListTasksInWorkspace_PriorityDefaultsToThreeWhenUnset(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)

	ctx := callerCtx("caller-agent")
	createResult := systools.NewTaskCreateTool(deps).Execute(ctx, map[string]any{
		"name":         "no explicit priority",
		"workspace_id": testWorkspaceID,
	})
	require.False(t, createResult.IsError, "create_task_in_workspace: %s", createResult.ForLLM)

	env := runWorkspaceList(t, deps, ctx, map[string]any{"workspace_id": testWorkspaceID})
	require.Len(t, env.Tasks, 1)
	assert.Equal(t, float64(3), env.Tasks[0]["priority"],
		"a task created without an explicit priority must read back as 3, not 0/absent")
}
