package tools

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedCriteriaTask creates a task with one prose acceptance criterion — the
// "hard tier" that must always be adjudicated by the judge before it can
// reach `done` (ADR-049 C1/SD-B2). Mirrors seedTask but stamps Criteria.
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

// TestTaskUpdate_DoneOnCriteriaTask_OutOfBand_Rejected proves the review-r2
// Chunk 1 fix: an out-of-band update_task(status:"done") call on a
// criteria-bearing task — one the caller's turn is NOT currently executing as
// its own task run (tools.ToolRunningTaskID(ctx) unset or naming a different
// task) — is rejected outright rather than staged as a PendingJudgeClaim.
// Staging here would strand the task non-terminal forever: only
// finishTaskRun (task_executor.go), reached exclusively from that task's own
// executor run, ever reads/adjudicates PendingJudgeClaim.
func TestTaskUpdate_DoneOnCriteriaTask_OutOfBand_Rejected(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedCriteriaTask(t, store, "agent-a", "agent-a", "ws-1")
	tool := NewTaskUpdateTool(store)

	// No ToolRunningTaskID set at all — an out-of-band call (e.g. the agent
	// poking at a criteria task nobody is currently executing).
	ctx := WithAgentID(context.Background(), "agent-a")

	res := tool.Execute(ctx, map[string]any{
		"task_id": tk.ID,
		"status":  updStatusDone,
		"result":  "I claim this is done",
	})
	if !res.IsError {
		t.Fatalf("expected out-of-band done-claim on a criteria task to be rejected, got success: %s", res.ForLLM)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status == task.StatusDone {
		t.Errorf("task must NOT have been marked done by an out-of-band call, got status=%q", got.Status)
	}
	if got.PendingJudgeClaim != "" {
		t.Errorf("task must NOT have a staged PendingJudgeClaim from an out-of-band call, got %q",
			got.PendingJudgeClaim)
	}
}

// TestTaskUpdate_DoneOnCriteriaTask_OutOfBand_WrongRunningTask_Rejected proves
// the gate compares the RUNNING task's ID, not just "is any task running" —
// a turn that IS a genuine task-run, but for a DIFFERENT task, must still be
// rejected when it tries to force-complete tk.
func TestTaskUpdate_DoneOnCriteriaTask_OutOfBand_WrongRunningTask_Rejected(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedCriteriaTask(t, store, "agent-a", "agent-a", "ws-1")
	tool := NewTaskUpdateTool(store)

	ctx := WithAgentID(context.Background(), "agent-a")
	ctx = WithRunningTaskID(ctx, "some-other-task-id")

	res := tool.Execute(ctx, map[string]any{
		"task_id": tk.ID,
		"status":  updStatusDone,
		"result":  "I claim this is done",
	})
	if !res.IsError {
		t.Fatalf("expected done-claim naming a task other than the running one to be rejected, got success: %s", res.ForLLM)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PendingJudgeClaim != "" {
		t.Errorf("must NOT have staged a claim for a task that is not the running task, got %q",
			got.PendingJudgeClaim)
	}
}

// TestTaskUpdate_DoneOnCriteriaTask_InRun_StagesClaim proves the in-run path
// is unchanged by the Chunk 1 fix: a worker calling update_task(done) FROM
// WITHIN that task's own executor run (tools.ToolRunningTaskID(ctx) ==
// task_id, exactly as task_executor.go's runTask/runTaskFromInProgress stamp
// it) still stages a PendingJudgeClaim for finishTaskRun to adjudicate — it
// is not rejected, and Status is not written directly.
func TestTaskUpdate_DoneOnCriteriaTask_InRun_StagesClaim(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedCriteriaTask(t, store, "agent-a", "agent-a", "ws-1")
	tool := NewTaskUpdateTool(store)

	ctx := WithAgentID(context.Background(), "agent-a")
	ctx = WithRunningTaskID(ctx, tk.ID) // exactly this task's own run

	res := tool.Execute(ctx, map[string]any{
		"task_id": tk.ID,
		"status":  updStatusDone,
		"result":  "the verifiable work is complete",
	})
	if res.IsError {
		t.Fatalf("expected in-run done-claim to be staged (not rejected): %s", res.ForLLM)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status == task.StatusDone {
		t.Errorf("Status must remain unpatched pending judge adjudication, got status=%q", got.Status)
	}
	if got.PendingJudgeClaim != "the verifiable work is complete" {
		t.Errorf("expected PendingJudgeClaim to be staged with the claim text, got %q", got.PendingJudgeClaim)
	}
}
