// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// task_parity_test.go — RED tests for the T8 task-tool write surface
// (tool-parity spec r17 §13.1 tests 3–7; scenarios 1.1–1.13, 2.1–2.16;
// datasets D-1, D-2, D-11).
//
// ORACLE: the spec's §7.2 table, §7.7 authority matrix and §7.11 error texts,
// byte for byte. The update_task tool does not accept most of these arguments
// yet (no prompt, description, tags, plan_id, trigger, max_attempts,
// clear_due, clear_max_attempts; status enum is done/failed only), so the
// expected RED failure is "the new argument was ignored / the new refusal did
// not happen", asserted on the real store — never a skip.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// Fixtures
// -----------------------------------------------------------------------------

// parityStorePair gives a task store and its sibling goal store under one
// temp root, the same derivation GoalStoreForTasks performs in production.
func parityStorePair(t *testing.T) (*task.Store, *goal.Store) {
	t.Helper()
	root := t.TempDir()
	store := task.New(filepath.Join(root, "tasks"))
	return store, GoalStoreForTasks(store)
}

// parityTask seeds a task; creator/assignee "" means person-created (no agent
// creator). withGoal pairs a goal record the way the create path does.
func parityTask(t *testing.T, store *task.Store, creator, assignee, wsID string, status task.Status, withGoal bool) *task.Task {
	t.Helper()
	tk := &task.Task{
		Title:            "parity fixture",
		Prompt:           "do the original work",
		Action:           task.ActionLLM,
		AgentID:          assignee,
		CreatedBy:        creator,
		CreatedByAgentID: creator,
		WorkspaceID:      wsID,
		Status:           status,
		Criteria:         []task.AcceptanceCriterion{proseCriterion("the work is verified")},
	}
	if creator == "" {
		tk.CreatedBy = "person"
	}
	require.NoError(t, store.Create(tk), "seed parity task")
	if withGoal {
		require.NoError(t, SyncTaskGoalRecord(GoalStoreForTasks(store), tk, true,
			[]task.AcceptanceCriterion{proseCriterion("the work is verified")}, true, nil),
			"pair the goal record the way fresh creation does")
	}
	return tk
}

func parityCtx(caller string) context.Context {
	ctx := context.Background()
	if caller != "" {
		ctx = WithAgentID(ctx, caller)
	}
	return WithWorkspaceID(ctx, "ws-a")
}

func parityGoalPrompt(t *testing.T, gs *goal.Store, taskID string) string {
	t.Helper()
	g, err := gs.GetByOwner(generated.GoalOwnerKindTask, taskID)
	require.NoError(t, err, "read the paired goal record")
	return g.Prompt
}

// -----------------------------------------------------------------------------
// Test 3 — TestUpdateTask_PromptAndGoalSync (scenarios 1.1–1.4, 1.8, 1.12, 1.13)
// -----------------------------------------------------------------------------

func TestUpdateTask_PromptAndGoalSync(t *testing.T) {
	t.Run("1.1 creator updates the prompt; paired goal follows", func(t *testing.T) {
		store, gs := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-a", task.StatusInbox, true)
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"task_id": tk.ID, "prompt": "Summarise the Q3 logs",
		})
		require.False(t, res.IsError, "creator prompt update must succeed: %s", res.ForLLM)

		got, err := store.Get(tk.ID)
		require.NoError(t, err)
		assert.Equal(t, "Summarise the Q3 logs", got.Prompt, "the task stores the new prompt")
		assert.Equal(t, "Summarise the Q3 logs", parityGoalPrompt(t, gs, tk.ID),
			"the paired goal's prompt equals the task prompt")
	})

	t.Run("1.2 ten-thousand-character prompt: task 10,000, goal first 4,000", func(t *testing.T) {
		store, gs := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-a", task.StatusInbox, true)
		tool := NewTaskUpdateTool(store)

		long := strings.Repeat("P", 10000)
		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "prompt": long})
		require.False(t, res.IsError, "a 10,000-character prompt is within the task bound: %s", res.ForLLM)

		got, err := store.Get(tk.ID)
		require.NoError(t, err)
		assert.Len(t, got.Prompt, 10000, "the task stores the full 10,000 characters")
		goalPrompt := parityGoalPrompt(t, gs, tk.ID)
		assert.Len(t, goalPrompt, 4000, "the goal stores the first 4,000 characters")
		assert.Equal(t, long[:4000], goalPrompt)
	})

	t.Run("1.3 no paired goal: prompt stored, none created", func(t *testing.T) {
		store, gs := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "prompt": "solo"})
		require.False(t, res.IsError, "%s", res.ForLLM)
		got, err := store.Get(tk.ID)
		require.NoError(t, err)
		assert.Equal(t, "solo", got.Prompt)

		_, gErr := gs.GetByOwner(generated.GoalOwnerKindTask, tk.ID)
		assert.True(t, errors.Is(gErr, goal.ErrOwnerNotFound),
			"no paired goal may be created by a prompt-only update; got %v", gErr)
	})

	t.Run("1.4 goal write fails: tool succeeds with goal_sync_warning, task updated", func(t *testing.T) {
		store, gs := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-a", task.StatusInbox, true)
		g, err := gs.GetByOwner(generated.GoalOwnerKindTask, tk.ID)
		require.NoError(t, err)
		const originalGoal = "the original goal instruction"
		g, err = gs.Update(g.GoalID, func(record *goal.Goal) error { record.Prompt = originalGoal; return nil })
		require.NoError(t, err)
		goalPath := filepath.Join(gs.Dir(), g.GoalID+".json")
		before, err := os.ReadFile(goalPath)
		require.NoError(t, err)
		// An entity sidecar directory makes the real write path fail at its
		// filesystem edge even under root, without damaging the readable goal.
		// No task/goal helper or method is mocked or replaced.
		lockPath := filepath.Join(gs.Dir(), g.GoalID+".lock")
		require.NoError(t, os.Remove(lockPath))
		require.NoError(t, os.Mkdir(lockPath, 0o700))
		_, fault := gs.Update(g.GoalID, func(*goal.Goal) error { return nil })
		var pathErr *os.PathError
		require.ErrorAs(t, fault, &pathErr, "instrument: an actual goal-store write fails, not a chmod assumption")
		assert.Equal(t, lockPath, pathErr.Path)
		assert.Equal(t, originalGoal, parityGoalPrompt(t, gs, tk.ID), "instrument: the original goal is still readable")

		res := NewTaskUpdateTool(store).Execute(parityCtx("agent-a"), map[string]any{
			"task_id": tk.ID, "prompt": "half saved",
		})
		require.False(t, res.IsError, "1.4 persists the task and returns success with a truthful warning: %s", res.ForLLM)
		var out map[string]any
		require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out))
		warning, ok := out["goal_sync_warning"].(string)
		require.True(t, ok, "the result must carry the named goal_sync_warning, not a silent partial save")
		assert.NotEmpty(t, warning)
		fresh, err := store.Get(tk.ID)
		require.NoError(t, err)
		assert.Equal(t, "half saved", fresh.Prompt)
		after, err := os.ReadFile(goalPath)
		require.NoError(t, err)
		assert.Equal(t, before, after, "the failed goal write leaves its bytes unchanged")
	})

	t.Run("1.8 running task: prompt mixed with title refuses whole, naming prompt", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInProgress, false)
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"task_id": tk.ID, "prompt": "new instruction", "title": "renamed",
		})
		require.True(t, res.IsError, "prompt is frozen while running; got success: %s", res.ForLLM)
		assert.Contains(t, res.ForLLM, `"prompt"`, "the refusal must name prompt (§7.11)")
		assert.Contains(t, res.ForLLM, "while the task is running")

		got, err := store.Get(tk.ID)
		require.NoError(t, err)
		assert.Equal(t, "do the original work", got.Prompt, "nothing applied")
		assert.Equal(t, "parity fixture", got.Title, "the mutable field riding along is not applied either")
	})

	t.Run("1.12 running task: creator's dod+priority refused; priority unchanged", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInProgress, false)
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"task_id": tk.ID, "dod": validDoDArg(), "priority": 2,
		})
		require.True(t, res.IsError, "a frozen field refuses the whole call: %s", res.ForLLM)
		assert.Contains(t, res.ForLLM, `"dod"`)

		got, err := store.Get(tk.ID)
		require.NoError(t, err)
		assert.Zero(t, got.Priority, "priority is unchanged by the refused call (0 = unset)")
	})

	t.Run("1.13 two interleaved writers: goal prompt equals the CURRENT persisted task prompt", func(t *testing.T) {
		store, gs := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-a", task.StatusInbox, true)
		tool := NewTaskUpdateTool(store)

		_ = gs
		_ = tk
		_ = tool
		// The old fixture let B finish while A held the coordination lock;
		// that deadlocks a conforming implementation. The required schedule
		// is A held -> B demonstrably queued -> release A -> join both.
		// No shared REST/tool ordering boundary or injectable in-lock seam
		// exists at dbdd1da6a. Do not invent a hook in production during RED.
		t.Fatal("BLOCKED: shared REST/tool task-write + goal-sync ordering boundary and its in-lock fixture seam not implemented — required by §7.2 TK-03 / scenario 1.13; assert B queued before releasing A, then task and goal both Beta[:4000]")
	})
}

// -----------------------------------------------------------------------------
// Test 4 — TestUpdateTask_DefinitionAuthority_Matrix
// (scenarios 1.5–1.7, 1.9–1.11; §7.7 authority matrix; §7.11 creator-only text)
// -----------------------------------------------------------------------------

func TestUpdateTask_DefinitionAuthority_Matrix(t *testing.T) {
	const creatorOnlyText = "Only the agent that created this task can change what it asks for " +
		"(instruction, criteria, definition of done or retry limit). Ask the creating agent or a person."

	newCriteria := func() []any {
		return []any{map[string]any{"kind": "prose", "text": "changed criteria"}}
	}

	t.Run("1.5 assignee that did not create it cannot send criteria", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-b"), map[string]any{"task_id": tk.ID, "criteria": newCriteria()})
		require.True(t, res.IsError, "the assignee's definition edit must be refused; got success: %s", res.ForLLM)
		assert.Equal(t, creatorOnlyText, res.ForLLM, "the §7.11 creator-only text, byte for byte")

		got, err := store.Get(tk.ID)
		require.NoError(t, err)
		require.Len(t, got.Criteria, 1)
		assert.Equal(t, "the work is verified", got.Criteria[0].Text, "nothing changed")
	})

	t.Run("1.6 assignee cannot send max_attempts either", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-b"), map[string]any{"task_id": tk.ID, "max_attempts": 2})
		require.True(t, res.IsError, "max_attempts is a definition field; got success: %s", res.ForLLM)
		assert.Equal(t, creatorOnlyText, res.ForLLM)
	})

	t.Run("1.7 creator=assignee edits dod on an idle task", func(t *testing.T) {
		store, gs := parityStorePair(t)
		// pkg/task has no Dod field (ADR-086 D5): the definition of done is
		// owned by the paired goal record, so the fixture pairs one first.
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusNext, true)
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"task_id": tk.ID,
			"dod":     []any{map[string]any{"kind": "prose", "text": "my own dod"}},
		})
		require.False(t, res.IsError, "%s", res.ForLLM)

		g, err := gs.GetByOwner(generated.GoalOwnerKindTask, tk.ID)
		require.NoError(t, err)
		require.NotEmpty(t, g.DoD, "the goal record carries the dod")
		assert.Equal(t, "my own dod", g.DoD[0].Text, "the creator may change what its own task asks for")
	})

	t.Run("1.9 person-created task: assignee cannot send a prompt", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "", "agent-b", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-b"), map[string]any{"task_id": tk.ID, "prompt": "nope"})
		require.True(t, res.IsError, "a person-created task has no agent creator; got success: %s", res.ForLLM)
		assert.Equal(t, creatorOnlyText, res.ForLLM)
		got, _ := store.Get(tk.ID)
		assert.Equal(t, "do the original work", got.Prompt, "nothing changed")
	})

	t.Run("1.11 reassigned assignee is still not the creator", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)
		tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
		// Reassign to C: the creator keeps definition rights; C (now the
		// assignee) does not.
		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "agent_id": "agent-c"})
		require.False(t, res.IsError, "reassignment setup: %s", res.ForLLM)

		res = tool.Execute(parityCtx("agent-c"), map[string]any{"task_id": tk.ID, "criteria": newCriteria()})
		require.True(t, res.IsError, "C is the assignee, not the creator: %s", res.ForLLM)
		assert.Equal(t, creatorOnlyText, res.ForLLM)
	})
}

// -----------------------------------------------------------------------------
// Test 5 — TestUpdateTask_StatusValuesAndStoreTransitions
// (scenarios 2.4–2.9; dataset D-11)
// -----------------------------------------------------------------------------

func TestUpdateTask_StatusValuesAndStoreTransitions(t *testing.T) {
	setStatus := func(t *testing.T, store *task.Store, caller string, tk *task.Task, status any) *ToolResult {
		t.Helper()
		tool := NewTaskUpdateTool(store)
		return tool.Execute(parityCtx(caller), map[string]any{"task_id": tk.ID, "status": status})
	}

	t.Run("2.4 failed task moves to next", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusFailed, false)
		res := setStatus(t, store, "agent-a", tk, "next")
		require.False(t, res.IsError, "failed→next is allowed (D-11): %s", res.ForLLM)
		got, _ := store.Get(tk.ID)
		assert.Equal(t, task.StatusNext, got.Status)
	})

	t.Run("2.5 inbox task moves to next", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		res := setStatus(t, store, "agent-a", tk, "next")
		require.False(t, res.IsError, "inbox→next is allowed (D-11): %s", res.ForLLM)
		got, _ := store.Get(tk.ID)
		assert.Equal(t, task.StatusNext, got.Status)
	})

	t.Run("2.6 done is terminal: done→inbox refused with the store's message", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusDone, false)
		res := setStatus(t, store, "agent-a", tk, "inbox")
		require.True(t, res.IsError, "got success: %s", res.ForLLM)
		assert.Contains(t, res.ForLLM, `"done" → "inbox" is not permitted (done is terminal)`,
			"§7.11 names the store's message as the refusal")
		got, _ := store.Get(tk.ID)
		assert.Equal(t, task.StatusDone, got.Status)
	})

	t.Run("2.7 inbox→in_progress and inbox→blocked refused", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		for _, status := range []string{"in_progress", "blocked"} {
			res := setStatus(t, store, "agent-a", tk, status)
			require.True(t, res.IsError, "%s must be refused: %s", status, res.ForLLM)
			got, _ := store.Get(tk.ID)
			assert.Equal(t, task.StatusInbox, got.Status, "still inbox after the refused %s", status)
		}
	})

	t.Run("2.8 no status during the caller's own run", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInProgress, false)
		// The caller is the task's runner: its own run is the judge's to end
		// (goal_claim), not update_task's.
		res := NewTaskUpdateTool(store).Execute(WithRunningTaskID(parityCtx("agent-a"), tk.ID), map[string]any{"task_id": tk.ID, "status": "failed"})
		require.True(t, res.IsError, "the runner cannot end its own run with update_task: %s", res.ForLLM)
		got, _ := store.Get(tk.ID)
		assert.Equal(t, task.StatusInProgress, got.Status)
	})

	t.Run("2.9 done refused on a task with criteria outside its own run", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, true)
		res := setStatus(t, store, "agent-a", tk, "done")
		require.True(t, res.IsError, "criteria-done refusal: %s", res.ForLLM)
		got, _ := store.Get(tk.ID)
		assert.Equal(t, task.StatusInbox, got.Status)
	})
}

// -----------------------------------------------------------------------------
// Test 6 — TestTaskPlanMembership_SharedRestAndTool (scenarios 2.11–2.15)
// -----------------------------------------------------------------------------

func TestTaskPlanMembership_SharedRestAndTool(t *testing.T) {
	// Reuse the shared plan/task fixture stores; plans are created directly
	// to control their state per row.
	newStores := func(t *testing.T) (*plan.Store, *task.Store) {
		return newPlanAndTaskStores(t)
	}
	seedPlan := func(t *testing.T, ps *plan.Store, wsID, title string, state plan.State) *plan.Plan {
		t.Helper()
		p := &plan.Plan{WorkspaceID: wsID, Title: title, Objective: "objective", State: state,
			OwnerAgentID: "agent-a"}
		require.NoError(t, ps.Create(p), "seed plan")
		return p
	}

	t.Run("2.11 joining an approved plan is refused; no plan", func(t *testing.T) {
		ps, ts := newStores(t)
		seedPlan(t, ps, "ws-a", "draft plan", plan.StateDraft)
		approved := seedPlan(t, ps, "ws-a", "approved plan", plan.StateApproved)
		tk := parityTask(t, ts, "agent-a", "agent-a", "ws-a", task.StatusNext, false)
		tool := NewTaskUpdateTool(ts)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "plan_id": approved.ID})
		require.True(t, res.IsError, "an approved plan refuses members: %s", res.ForLLM)
		assert.Contains(t, res.ForLLM, "plan", "2.11 refuses plan membership, not an unrelated unknown-field error")
		got, err := ts.Get(tk.ID)
		require.NoError(t, err)
		assert.Empty(t, got.PlanID, "2.11 refused membership leaves the task with no plan")
	})

	t.Run("2.12 detaching a next member moves it to inbox", func(t *testing.T) {
		ps, ts := newStores(t)
		p := seedPlan(t, ps, "ws-a", "plan", plan.StateDraft)
		tk := parityTask(t, ts, "agent-a", "agent-a", "ws-a", task.StatusNext, false)
		_, err := ts.Update(tk.ID, task.Patch{PlanID: &p.ID})
		require.NoError(t, err, "fixture: join the plan")
		tool := NewTaskUpdateTool(ts)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "plan_id": ""})
		require.False(t, res.IsError, "detaching is always allowed: %s", res.ForLLM)
		got, _ := ts.Get(tk.ID)
		assert.Empty(t, got.PlanID, "no plan")
		assert.Equal(t, task.StatusInbox, got.Status, "detach without an explicit status also sets inbox")
	})

	t.Run("2.13 explicit status wins over the detach's inbox", func(t *testing.T) {
		ps, ts := newStores(t)
		p := seedPlan(t, ps, "ws-a", "plan", plan.StateDraft)
		tk := parityTask(t, ts, "agent-a", "agent-a", "ws-a", task.StatusNext, false)
		_, err := ts.Update(tk.ID, task.Patch{PlanID: &p.ID})
		require.NoError(t, err)
		tool := NewTaskUpdateTool(ts)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"task_id": tk.ID, "plan_id": "", "status": "next",
		})
		require.False(t, res.IsError, "%s", res.ForLLM)
		got, _ := ts.Get(tk.ID)
		assert.Empty(t, got.PlanID)
		assert.Equal(t, task.StatusNext, got.Status, "an explicit status in the same call wins")
	})

	t.Run("2.14 detaching a done member keeps done", func(t *testing.T) {
		ps, ts := newStores(t)
		p := seedPlan(t, ps, "ws-a", "plan", plan.StateDraft)
		tk := parityTask(t, ts, "agent-a", "agent-a", "ws-a", task.StatusDone, false)
		_, err := ts.Update(tk.ID, task.Patch{PlanID: &p.ID})
		require.NoError(t, err)
		tool := NewTaskUpdateTool(ts)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "plan_id": ""})
		require.False(t, res.IsError, "detach without an explicit status is applied even when the "+
			"store refuses the inbox transition: %s", res.ForLLM)
		got, _ := ts.Get(tk.ID)
		assert.Empty(t, got.PlanID)
		assert.Equal(t, task.StatusDone, got.Status, "still Done")
	})

	t.Run("2.15 leaving an approved plan is allowed", func(t *testing.T) {
		ps, ts := newStores(t)
		p := seedPlan(t, ps, "ws-a", "plan", plan.StateApproved)
		tk := parityTask(t, ts, "agent-a", "agent-a", "ws-a", task.StatusNext, false)
		_, err := ts.Update(tk.ID, task.Patch{PlanID: &p.ID})
		require.NoError(t, err)
		tool := NewTaskUpdateTool(ts)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "plan_id": ""})
		require.False(t, res.IsError, "leaving is always allowed: %s", res.ForLLM)
		got, _ := ts.Get(tk.ID)
		assert.Empty(t, got.PlanID)
	})
}

// -----------------------------------------------------------------------------
// Test 7 — TestUpdateTask_OrgFields_ClearDue_EmptyTitle
// (scenarios 2.1–2.3, 2.10, 2.16; dataset D-1)
// -----------------------------------------------------------------------------

func TestUpdateTask_OrgFields_ClearDue_EmptyTitle(t *testing.T) {
	t.Run("2.1 description, tags normalised, run_isolated saved", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"task_id":      tk.ID,
			"description":  "notes",
			"tags":         []any{"Alpha", "beta", "alpha"},
			"run_isolated": true,
		})
		require.False(t, res.IsError, "organisation fields are the assignee's: %s", res.ForLLM)

		got, _ := store.Get(tk.ID)
		assert.Equal(t, "notes", got.Description)
		assert.Equal(t, []string{"alpha", "beta"}, got.Tags,
			"tags are lower-cased and de-duplicated (D-1)")
		assert.True(t, got.RunIsolated)
	})

	t.Run("2.2 clear_due removes the due date", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)
		due := "2026-12-01T00:00:00Z"
		_, err := store.Update(tk.ID, task.Patch{Due: &due})
		require.NoError(t, err)
		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "clear_due": true})
		require.False(t, res.IsError, "%s", res.ForLLM)
		got, _ := store.Get(tk.ID)
		assert.Empty(t, got.Due, "no due after clear_due (task.Due is an RFC 3339 string)")
	})

	t.Run("2.3 a due value in the same call wins over clear_due", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)
		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"task_id": tk.ID, "clear_due": true, "due": "2027-01-01T00:00:00Z",
		})
		require.False(t, res.IsError, "%s", res.ForLLM)
		got, _ := store.Get(tk.ID)
		require.NotEmpty(t, got.Due, "the due value wins over clear_due in the same call")
		assert.Contains(t, got.Due, "2027-01-01")
	})

	t.Run("2.10 empty title refused byte-for-byte", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)
		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "title": ""})
		require.True(t, res.IsError, "got success: %s", res.ForLLM)
		assert.Contains(t, res.ForLLM, "title must not be empty")
		got, _ := store.Get(tk.ID)
		assert.Equal(t, "parity fixture", got.Title, "unchanged")
	})

	t.Run("2.16 seventeen tags refused naming tags (D-1: 16/17)", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)

		tooMany := make([]any, 17)
		for i := range tooMany {
			tooMany[i] = fmt.Sprintf("tag-%02d", i)
		}
		res := tool.Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "tags": tooMany})
		require.True(t, res.IsError, "17 tags must be refused; got success: %s", res.ForLLM)
		assert.Contains(t, res.ForLLM, "tags", "the refusal names tags")
	})
}
