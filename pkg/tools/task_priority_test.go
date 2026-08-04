package tools

// task_priority_test.go — regression coverage for the M2(b)/M7 fixes on the
// agent-facing create_task/list_tasks tools.
//
// M2(b) root cause (uat-report-adr057-batch1-dispatch-monitor-2026-08-03.md,
// "TC-03e"): create_task's Execute used to default `priority := 3` and only
// overwrite it `if p, ok := args["priority"].(float64); ok && p >= 1 && p <=
// 5` — any out-of-range value (0, 6, negative) simply failed that guard and
// fell through to the default with NO error at all. The caller received a
// success response with their explicit input silently discarded. This is a
// DIFFERENT defect shape from update_task's (which already validated
// correctly): the create path never even attempted to reject invalid input,
// it just quietly ignored it.
//
// M7 root cause (same report, "TC-03b"): taskListRow never carried priority/
// write_set/stream, so even if the value WERE wrong, no read tool could catch
// it — list_tasks was the one surface the UAT plan's own pass criterion
// pointed at ("Verify via list_tasks or list_jobs") and it could not verify
// anything.
import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestTaskCreate_PriorityBoundaryMatrix is the direct regression test for
// M2(b): before the fix, EVERY case in this table except the two valid ones
// would return IsError=false with priority silently coerced to 3 — this test
// would have failed RED against the old `p >= 1 && p <= 5` guard. Binding
// Rule 4: every rejection is paired with an acceptance at the same boundary.
func TestTaskCreate_PriorityBoundaryMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		priority     any // float64, or nil to omit the arg
		wantErr      bool
		wantPriority int
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
			t.Parallel()
			store := task.New(t.TempDir())
			tool := NewTaskCreateTool(store)
			tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

			ctx := WithAgentID(context.Background(), "caller")
			ctx = WithWorkspaceID(ctx, "ws-1")

			args := map[string]any{
				"title":    "priority matrix " + c.name,
				"prompt":   "do it",
				"agent_id": "agent-b",
				"criteria": validCriteriaArg(),
			}
			if c.priority != nil {
				args["priority"] = c.priority
			}

			res := tool.Execute(ctx, args)

			if c.wantErr {
				if !res.IsError {
					t.Fatalf("expected error for priority %v, got success: %s", c.priority, res.ForLLM)
				}
				assert.Contains(t, res.ForLLM, "priority must be between 1 and 5",
					"expected the shared priority validation message, got: %s", res.ForLLM)
				// Nothing must have been persisted for a rejected create — this is
				// the exact silent-discard shape M2(b) exhibited (success + wrong
				// data), so a would-be caller must be able to trust that a 400/
				// error truly means nothing landed.
				all, err := store.List(task.Filter{WorkspaceID: "ws-1"})
				require.NoError(t, err)
				assert.Empty(t, all, "a rejected create must not persist a task")
				return
			}

			require.False(t, res.IsError, "create_task with priority %v failed: %s", c.priority, res.ForLLM)
			var created struct {
				TaskID string `json:"task_id"`
			}
			require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &created))
			got, err := store.Get(created.TaskID)
			require.NoError(t, err)
			assert.Equal(t, c.wantPriority, got.Priority, "persisted priority mismatch")
		})
	}
}

// TestTaskList_SurfacesPriorityWriteSetStream is the M7 regression test:
// create_task accepts priority/write_set/stream, and list_tasks (role=
// delegator, since this is the creating agent's own dispatched task) must
// now show all three back — before the fix, taskListRow carried none of
// them, so this assertion would fail even though the underlying stored data
// was correct (a pure observability gap, per Finding §1 of the UAT report).
func TestTaskList_SurfacesPriorityWriteSetStream(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	createTool := NewTaskCreateTool(store)
	createTool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := createTool.Execute(ctx, map[string]any{
		"title":     "rich params task",
		"prompt":    "do it",
		"agent_id":  "agent-b",
		"criteria":  validCriteriaArg(),
		"priority":  float64(2),
		"stream":    "uat-stream",
		"write_set": []any{"uat-output.txt"},
	})
	require.False(t, res.IsError, "create_task: %s", res.ForLLM)
	var created struct {
		TaskID string `json:"task_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &created))

	listTool := NewTaskListTool(store)
	listRes := listTool.Execute(ctx, map[string]any{"role": "delegator"})
	require.False(t, listRes.IsError, "list_tasks: %s", listRes.ForLLM)

	var env struct {
		Tasks []map[string]any `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal([]byte(listRes.ForLLM), &env))
	require.Len(t, env.Tasks, 1)
	row := env.Tasks[0]

	require.Equal(t, created.TaskID, row["id"])
	require.Contains(t, row, "priority", "priority must be observable via list_tasks (M7)")
	assert.Equal(t, float64(2), row["priority"])
	require.Contains(t, row, "stream", "stream must be observable via list_tasks (M7)")
	assert.Equal(t, "uat-stream", row["stream"])
	require.Contains(t, row, "write_set", "write_set must be observable via list_tasks (M7)")
	ws, ok := row["write_set"].([]any)
	require.True(t, ok, "write_set must be an array, got %T", row["write_set"])
	require.Len(t, ws, 1)
	assert.Equal(t, "uat-output.txt", ws[0])
}

// TestTaskList_PriorityDefaultsToThreeWhenUnset proves the M7 read surface
// reports a meaningful priority (3, via EffectivePriority) for a task created
// without an explicit priority, matching REST's own EffectivePriority-based
// default rather than surfacing 0/absent.
func TestTaskList_PriorityDefaultsToThreeWhenUnset(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	createTool := NewTaskCreateTool(store)
	createTool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := createTool.Execute(ctx, map[string]any{
		"title":    "no explicit priority",
		"prompt":   "do it",
		"agent_id": "agent-b",
		"criteria": validCriteriaArg(),
	})
	require.False(t, res.IsError, "create_task: %s", res.ForLLM)

	listTool := NewTaskListTool(store)
	listRes := listTool.Execute(ctx, map[string]any{"role": "delegator"})
	require.False(t, listRes.IsError, "list_tasks: %s", listRes.ForLLM)

	var env struct {
		Tasks []map[string]any `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal([]byte(listRes.ForLLM), &env))
	require.Len(t, env.Tasks, 1)
	assert.Equal(t, float64(3), env.Tasks[0]["priority"],
		"a task created without an explicit priority must read back as 3, not 0/absent")
}
