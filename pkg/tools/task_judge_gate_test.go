package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedCriteriaTask creates an in_progress task with one prose acceptance
// criterion (ADR-049 C1/SD-B2 hard tier). Mirrors seedTask but stamps Criteria.
func seedCriteriaTask(t *testing.T, store *task.Store, agentID, createdBy, wsID string) *task.Task {
	t.Helper()
	tk := &task.Task{
		Title:       "criteria task",
		Prompt:      "do something verifiable",
		Action:      task.ActionLLM,
		AgentID:     agentID,
		CreatedBy:   createdBy,
		WorkspaceID: wsID,
		Status:      task.StatusInProgress,
		Criteria: []task.AcceptanceCriterion{
			{
				Kind:   task.KindProse,
				Text:   "the work is verifiably done",
				Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: createdBy},
				Status: task.CritPending,
			},
		},
	}
	if err := store.Create(tk); err != nil {
		t.Fatalf("seedCriteriaTask: create: %v", err)
	}
	return tk
}

// TestTaskUpdate_StatusOnOwnRunningTask_Refused pins the founder decision of
// 2026-09-14: while a task's own run is executing, its worker cannot write the
// task's status at all — neither done nor failed, with or without criteria.
// Completion is claimed with goal_claim and decided by the judge; an honest
// give-up is goal_claim(status:"blocked").
//
// The `failed` and criteria-less `done` rows are the ones that isolate this
// rule: the separate out-of-band criteria-task refusal below cannot catch them.
func TestTaskUpdate_StatusOnOwnRunningTask_Refused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		criteria bool
		status   string
	}{
		{"failed_on_a_criteria_task", true, updStatusFailed},
		{"done_on_a_criteria_less_task", false, updStatusDone},
		{"failed_on_a_criteria_less_task", false, updStatusFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := task.New(t.TempDir())
			var tk *task.Task
			if tc.criteria {
				tk = seedCriteriaTask(t, store, "agent-a", "agent-a", "ws-1")
			} else {
				tk = seedTask(t, store, "agent-a", "agent-a", "ws-1")
				inProgress := task.StatusInProgress
				if _, err := store.Update(tk.ID, task.Patch{Status: &inProgress}); err != nil {
					t.Fatalf("arrange in_progress: %v", err)
				}
			}
			tool := NewTaskUpdateTool(store)
			ctx := WithRunningTaskID(WithAgentID(context.Background(), "agent-a"), tk.ID)

			res := tool.Execute(ctx, map[string]any{"task_id": tk.ID, "status": tc.status, "result": "I decided"})
			if !res.IsError {
				t.Fatalf("a status write on the caller's own running task must be refused, got: %s", res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, "goal_claim") {
				t.Errorf("the refusal must name the one claim path (goal_claim): %s", res.ForLLM)
			}
			got, err := store.Get(tk.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.Status != task.StatusInProgress {
				t.Errorf("status = %q, want in_progress — nothing may be written by a refused call", got.Status)
			}
		})
	}
}

// TestTaskUpdate_DoneOnCriteriaTask_OutOfBand_Rejected: a done write on a
// criteria task from outside that task's run is refused — nothing would ever
// adjudicate it.
func TestTaskUpdate_DoneOnCriteriaTask_OutOfBand_Rejected(t *testing.T) {
	t.Parallel()
	for _, ctx := range []context.Context{
		WithAgentID(context.Background(), "agent-a"),
		WithRunningTaskID(WithAgentID(context.Background(), "agent-a"), "some-other-task-id"),
	} {
		store := task.New(t.TempDir())
		tk := seedCriteriaTask(t, store, "agent-a", "agent-a", "ws-1")
		res := NewTaskUpdateTool(store).Execute(ctx, map[string]any{
			"task_id": tk.ID, "status": updStatusDone, "result": "I claim this is done",
		})
		if !res.IsError {
			t.Fatalf("out-of-band done on a criteria task must be refused, got: %s", res.ForLLM)
		}
		got, err := store.Get(tk.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Status == task.StatusDone {
			t.Errorf("task marked done by an out-of-band call")
		}
	}
}

// TestTaskUpdate_FailedOnAnotherRunningTask_Allowed is the boundary: the in-run
// refusal is scoped to the task the caller's OWN run is executing. A delegator
// ending some other task it created keeps working.
func TestTaskUpdate_FailedOnAnotherRunningTask_Allowed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedCriteriaTask(t, store, "agent-a", "agent-a", "ws-1")
	ctx := WithRunningTaskID(WithAgentID(context.Background(), "agent-a"), "some-other-task-id")

	res := NewTaskUpdateTool(store).Execute(ctx, map[string]any{"task_id": tk.ID, "status": updStatusFailed})
	if res.IsError {
		t.Fatalf("failing a task that is not the caller's own running task must be allowed: %s", res.ForLLM)
	}
	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
}
