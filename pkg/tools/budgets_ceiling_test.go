// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// budgets_ceiling_test.go — RED tests for the agent budget ceiling
// (tool-parity spec r17 §13.1 test 8; scenarios 3.1–3.8; dataset D-3).
//
// ORACLE: D-3's rows with the globals named there (task_max_attempts 3;
// supervision_max_attempts 3). The production configuration binding is missing;
// the explicit fatal rows are commissioning blockers, not budget verification.
// Test-only PlanBoundsCeilingsFn cannot configure production. The invariants
// remain the spec's: the ceiling wins, one above refuses the whole call
// naming the global, 0 is refused (minimum 1), clear always accepted, people
// uncapped.

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBudgets_CeilingWins(t *testing.T) {
	setGlobal := func(t *testing.T, n int) {
		t.Helper()
		t.Fatalf("BLOCKED: task tool has no production accessor for planning.task_max_attempts=%d — required by §7.2 TK-05 / D-3; assigning a test-only variable cannot configure production", n)
	}

	t.Run("D-3 row 1: at and below the global is accepted (1 and 3)", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)
		setGlobal(t, 3)

		for _, v := range []int{1, 3} {
			res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "max_attempts": v})
			require.False(t, res.IsError, "max_attempts %d (global 3) accepted: %s", v, res.ForLLM)
			got, err := store.Get(tk.ID)
			require.NoError(t, err)
			require.NotNil(t, got.MaxAttempts)
			assert.Equal(t, v, *got.MaxAttempts)
		}
	})

	t.Run("D-3 row 2: one above the global refuses naming the global", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)
		setGlobal(t, 3)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "max_attempts": 4})
		require.True(t, res.IsError, "got success: %s", res.ForLLM)
		assert.Equal(t, "max_attempts 4 is above the global limit of 3. Use 3 or less.", res.ForLLM,
			"the §7.11 budget text, byte for byte")
		got, _ := store.Get(tk.ID)
		assert.Nil(t, got.MaxAttempts, "nothing written on refusal")
	})

	t.Run("D-3 row 3: zero is refused (minimum 1)", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)
		setGlobal(t, 3)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "max_attempts": 0})
		require.True(t, res.IsError, "got success: %s", res.ForLLM)
		assert.Contains(t, res.ForLLM, "max_attempts", "the refusal names the field")
	})

	t.Run("D-3 row 4: lowering from 10 to 8 still refuses — the ceiling wins", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		ten := 10
		double := &ten
		_, err := store.Update(tk.ID, task.Patch{MaxAttempts: &double})
		require.NoError(t, err, "fixture: a person set an override of 10 earlier")
		tool := NewTaskUpdateTool(store)
		setGlobal(t, 3)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "max_attempts": 8})
		require.True(t, res.IsError, "MIN-003: a reduction that stays above the global is refused; got success: %s", res.ForLLM)
		assert.Equal(t, "max_attempts 8 is above the global limit of 3. Use 3 or less.", res.ForLLM)
	})

	t.Run("D-3 row 5: lowering to exactly the global is accepted", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		ten := 10
		double := &ten
		_, err := store.Update(tk.ID, task.Patch{MaxAttempts: &double})
		require.NoError(t, err)
		tool := NewTaskUpdateTool(store)
		setGlobal(t, 3)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "max_attempts": 3})
		require.False(t, res.IsError, "%s", res.ForLLM)
		got, _ := store.Get(tk.ID)
		require.NotNil(t, got.MaxAttempts)
		assert.Equal(t, 3, *got.MaxAttempts)
	})

	t.Run("D-3 row 6: clear_max_attempts always accepted, inherits the global", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		ten := 10
		double := &ten
		_, err := store.Update(tk.ID, task.Patch{MaxAttempts: &double})
		require.NoError(t, err)
		tool := NewTaskUpdateTool(store)
		setGlobal(t, 3)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"task_id": tk.ID, "clear_max_attempts": true,
		})
		require.False(t, res.IsError, "clearing is always accepted (TK-05): %s", res.ForLLM)
		got, _ := store.Get(tk.ID)
		assert.Nil(t, got.MaxAttempts, "cleared: inherits (3)")
	})

	t.Run("3.3 mutable while running", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInProgress, false)
		tool := NewTaskUpdateTool(store)
		setGlobal(t, 3)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "max_attempts": 2})
		require.False(t, res.IsError, "max_attempts stays mutable while running (operator decision "+
			"2026-09-12, TK-05): %s", res.ForLLM)
		got, _ := store.Get(tk.ID)
		require.NotNil(t, got.MaxAttempts)
		assert.Equal(t, 2, *got.MaxAttempts)
	})

	t.Run("D-3 row 7/8 + 3.6/3.7: plan bounds ceiling via create_plan", func(t *testing.T) {
		if PlanBoundsCeilingsFn == nil {
			t.Fatalf("BLOCKED: the plan-bounds ceiling accessor is not wired (scaffolding seam nil) — " +
				"required by §7.3 PL-08 / D-3 rows 7–8")
		}
		prev := PlanBoundsCeilingsFn
		PlanBoundsCeilingsFn = func() PlanBoundsCeilings {
			return PlanBoundsCeilings{
				PlanJudgeMaxRounds:            20,
				IdleExpiryDays:                7,
				SupervisionMaxAttempts:        3,
				SupervisionTurnTimeoutSeconds: 600,
			}
		}
		t.Cleanup(func() { PlanBoundsCeilingsFn = prev })

		ps, _ := newPlanAndTaskStores(t)
		tool := NewPlanCreateTool(ps)
		tool.SetOwnerValidator(allowOwner)

		// 3.6: one bound above the global refuses the whole call, naming 3.
		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"title": "bounds too high", "owner_agent_id": "agent-a",
			"bounds": map[string]any{"supervision_max_attempts": 4},
		})
		require.True(t, res.IsError, "supervision_max_attempts 4 above the global 3 must refuse: %s", res.ForLLM)
		assert.Contains(t, res.ForLLM, "above the global limit of 3",
			"the §7.11 budget shape, naming the field and the global")

		// 3.7: bounds below the global are kept, merged field by field.
		res = tool.Execute(parityCtx("agent-a"), map[string]any{
			"title": "bounds merged", "owner_agent_id": "agent-a",
			"bounds": map[string]any{
				"supervision_max_attempts": 2,
				"idle_expiry_days":         5,
			},
		})
		require.False(t, res.IsError, "bounds at/below the global are accepted: %s", res.ForLLM)
		var out struct {
			PlanID string `json:"plan_id"`
		}
		require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out), "parse create_plan result: %s", res.ForLLM)
		got, err := ps.Get(out.PlanID)
		require.NoError(t, err)
		require.NotNil(t, got.Bounds)
		require.NotNil(t, got.Bounds.SupervisionMaxAttempts)
		require.NotNil(t, got.Bounds.IdleExpiryDays)
		assert.Equal(t, 2, *got.Bounds.SupervisionMaxAttempts)
		assert.Equal(t, 5, *got.Bounds.IdleExpiryDays)
	})
}
