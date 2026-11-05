package tools

// Oracle: tool-parity r17 §7.2 rows TK-02 (description, at most 2000), TK-05
// (max_attempts, minimum 1, agent ceiling = live planning.task_max_attempts,
// ceiling wins), TK-08 (run_isolated) and TK-10 (tags, at most 16, each at most
// 64, normalised): create_task takes all four, as update_task already does.
// Each case runs the real tool against a real store and reads the stored task
// back; a refused call must create nothing.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
)

type t8CreateFixture struct {
	t     *testing.T
	store *task.Store
	tool  *TaskCreateTool
}

func newT8CreateFixture(t *testing.T) *t8CreateFixture {
	t.Helper()
	_, store := newPlanAndTaskStores(t)
	tool := NewTaskCreateTool(store)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
	return &t8CreateFixture{t: t, store: store, tool: tool}
}

// create runs create_task with the required fields plus extra.
func (f *t8CreateFixture) create(extra map[string]any) *ToolResult {
	f.t.Helper()
	args := map[string]any{"title": "fields task", "prompt": "do it", "agent_id": "worker", "criteria": validCriteriaArg(), "dod": validDoDArg()}
	for k, v := range extra {
		args[k] = v
	}
	ctx := WithWorkspaceID(WithAgentID(context.Background(), "jim"), "ws-1")
	return f.tool.Execute(ctx, args)
}

func (f *t8CreateFixture) count() int {
	f.t.Helper()
	all, err := f.store.List(task.Filter{})
	require.NoError(f.t, err)
	return len(all)
}

// created asserts success and returns the stored task.
func (f *t8CreateFixture) created(res *ToolResult) *task.Task {
	f.t.Helper()
	require.False(f.t, res.IsError, "create_task refused: %s", res.ForLLM)
	var out struct {
		TaskID string `json:"task_id"`
	}
	require.NoError(f.t, json.Unmarshal([]byte(res.ForLLM), &out))
	got, err := f.store.Get(out.TaskID)
	require.NoError(f.t, err)
	return got
}

// refused asserts an error result whose text contains want and that nothing
// new was stored.
func (f *t8CreateFixture) refused(res *ToolResult, before int, want string) {
	f.t.Helper()
	require.True(f.t, res.IsError, "expected a refusal containing %q, got success: %s", want, res.ForLLM)
	assert.Contains(f.t, res.ForLLM, want)
	assert.Equal(f.t, before, f.count(), "a refused create stores nothing")
}

func TestCreateTask_Description_TK02(t *testing.T) {
	f := newT8CreateFixture(t)
	t.Run("stored", func(t *testing.T) {
		got := f.created(f.create(map[string]any{"description": "what this is for"}))
		assert.Equal(t, "what this is for", got.Description)
	})
	t.Run("absent leaves it empty", func(t *testing.T) {
		assert.Empty(t, f.created(f.create(nil)).Description)
	})
	t.Run("2000 characters accepted, 2001 refused", func(t *testing.T) {
		got := f.created(f.create(map[string]any{"description": strings.Repeat("d ", 1000)}))
		assert.Len(t, []rune(got.Description), 2000)
		before := f.count()
		res := f.create(map[string]any{"description": strings.Repeat("d ", 1000) + "x"})
		require.True(t, res.IsError, "2001 characters must be refused: %s", res.ForLLM)
		assert.Contains(t, res.ForLLM, "description")
		assert.Equal(t, before, f.count())
	})
	t.Run("wrong type refused", func(t *testing.T) {
		before := f.count()
		f.refused(f.create(map[string]any{"description": 5}), before, "description must be a string")
	})
}

func TestCreateTask_Tags_TK10(t *testing.T) {
	f := newT8CreateFixture(t)
	t.Run("stored normalised: trimmed, lower-cased, de-duplicated in first-seen order", func(t *testing.T) {
		got := f.created(f.create(map[string]any{"tags": []any{" Alpha ", "beta", "ALPHA"}}))
		assert.Equal(t, []string{"alpha", "beta"}, got.Tags)
	})
	t.Run("empty list clears to none", func(t *testing.T) {
		assert.Empty(t, f.created(f.create(map[string]any{"tags": []any{}})).Tags)
	})
	t.Run("16 tags accepted, 17 refused", func(t *testing.T) {
		tags := func(n int) []any {
			out := make([]any, n)
			for i := range out {
				out[i] = "tag" + strings.Repeat("x", i)
			}
			return out
		}
		assert.Len(t, f.created(f.create(map[string]any{"tags": tags(16)})).Tags, 16)
		before := f.count()
		f.refused(f.create(map[string]any{"tags": tags(17)}), before, "tags")
	})
	t.Run("a 64 character tag is accepted, 65 refused", func(t *testing.T) {
		assert.Len(t, f.created(f.create(map[string]any{"tags": []any{strings.Repeat("t", 64)}})).Tags, 1)
		before := f.count()
		f.refused(f.create(map[string]any{"tags": []any{strings.Repeat("t", 65)}}), before, "tags[0]")
	})
	t.Run("an empty tag and a non-list are refused", func(t *testing.T) {
		before := f.count()
		f.refused(f.create(map[string]any{"tags": []any{"  "}}), before, "tags[0]")
		f.refused(f.create(map[string]any{"tags": "alpha"}), before, "tags must be a list of strings")
		f.refused(f.create(map[string]any{"tags": []any{1}}), before, "tags must be a list of strings")
	})
}

func TestCreateTask_RunIsolated_TK08(t *testing.T) {
	f := newT8CreateFixture(t)
	assert.True(t, f.created(f.create(map[string]any{"run_isolated": true})).RunIsolated)
	assert.False(t, f.created(f.create(map[string]any{"run_isolated": false})).RunIsolated)
	assert.False(t, f.created(f.create(nil)).RunIsolated, "default is not isolated")
	before := f.count()
	f.refused(f.create(map[string]any{"run_isolated": "yes"}), before, "run_isolated must be true or false")
}

func TestCreateTask_MaxAttempts_TK05(t *testing.T) {
	t.Run("shipped global 3: 1 and 3 accepted and stored", func(t *testing.T) {
		require.Equal(t, 3, config.DefaultConfig().Planning.EffectiveTaskMaxAttempts(nil), "instrument: the shipped global is 3")
		f := newT8CreateFixture(t)
		for _, v := range []int{1, 3} {
			got := f.created(f.create(map[string]any{"max_attempts": v}))
			require.NotNil(t, got.MaxAttempts)
			assert.Equal(t, v, *got.MaxAttempts)
		}
		assert.Nil(t, f.created(f.create(nil)).MaxAttempts, "absent means inherit")
	})
	t.Run("one above the ceiling refuses naming the global, and 0 or a fraction is refused", func(t *testing.T) {
		f := newT8CreateFixture(t)
		before := f.count()
		f.refused(f.create(map[string]any{"max_attempts": 4}), before, "max_attempts 4 is above the global limit of 3. Use 3 or less.")
		f.refused(f.create(map[string]any{"max_attempts": 0}), before, "max_attempts must be a whole number of at least 1")
		f.refused(f.create(map[string]any{"max_attempts": 1.5}), before, "max_attempts must be a whole number of at least 1")
		f.refused(f.create(map[string]any{"max_attempts": "2"}), before, "max_attempts must be a whole number of at least 1")
	})
	t.Run("the ceiling is the live planning value, read at every call", func(t *testing.T) {
		f := newT8CreateFixture(t)
		limit := 5
		f.tool.SetPlanningConfigFn(func() config.PlanningConfig {
			cfg := config.DefaultConfig().Planning
			cfg.TaskMaxAttempts = limit
			return cfg
		})
		assert.Equal(t, 5, *f.created(f.create(map[string]any{"max_attempts": 5})).MaxAttempts)
		before := f.count()
		f.refused(f.create(map[string]any{"max_attempts": 6}), before, "max_attempts 6 is above the global limit of 5. Use 5 or less.")

		limit = 2 // the operator lowers it; the very next call sees it
		f.refused(f.create(map[string]any{"max_attempts": 3}), before, "max_attempts 3 is above the global limit of 2. Use 2 or less.")
		assert.Equal(t, 2, *f.created(f.create(map[string]any{"max_attempts": 2})).MaxAttempts)
	})
}

// TestCreateTask_NewFieldsTogether proves the four fields travel in one call
// and the schema advertises them (parity with update_task, TK-02/05/08/10).
func TestCreateTask_NewFieldsTogether(t *testing.T) {
	f := newT8CreateFixture(t)
	got := f.created(f.create(map[string]any{"description": "d", "tags": []any{"Ops"}, "run_isolated": true, "max_attempts": 2}))
	assert.Equal(t, "d", got.Description)
	assert.Equal(t, []string{"ops"}, got.Tags)
	assert.True(t, got.RunIsolated)
	require.NotNil(t, got.MaxAttempts)
	assert.Equal(t, 2, *got.MaxAttempts)

	props, ok := f.tool.Parameters()["properties"].(map[string]any)
	require.True(t, ok)
	for _, name := range []string{"description", "tags", "run_isolated", "max_attempts"} {
		assert.Contains(t, props, name, "create_task advertises %s", name)
	}
}
