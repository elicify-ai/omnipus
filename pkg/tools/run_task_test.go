// Omnipus — run_task Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedStandaloneTask creates a standalone (no plan_id) task in the given
// status, with an assigned agent unless agentID is "".
func seedStandaloneTask(t *testing.T, store *task.Store, status task.Status, agentID string) *task.Task {
	t.Helper()
	tk := &task.Task{
		Title: "standalone", Prompt: "do it", Action: task.ActionLLM,
		AgentID: agentID, WorkspaceID: "ws-1", Status: status,
	}
	if err := store.Create(tk); err != nil {
		t.Fatalf("seed standalone task: %v", err)
	}
	return tk
}

// TestRunTask_RejectsInPlanTask proves an in-plan task is rejected AT THE
// TOOL BOUNDARY without ever calling the dispatcher (ADR-052 FR-019 G4/A3,
// spec Test 15).
func TestRunTask_RejectsInPlanTask(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := &task.Task{
		Title: "member", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "worker", WorkspaceID: "ws-1", Status: task.StatusNext,
		PlanID: "plan-123",
	}
	if err := store.Create(tk); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	dispatchCalled := false
	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(func(context.Context, string) (string, error) {
		dispatchCalled = true
		return "sess-1", nil
	})

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if !res.IsError {
		t.Fatal("expected rejection for an in-plan task")
	}
	if !strings.Contains(res.ForLLM, "member of plan") {
		t.Errorf("unexpected rejection message: %s", res.ForLLM)
	}
	if dispatchCalled {
		t.Fatal("run_task must not dispatch an in-plan task")
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Errorf("status = %q, want unchanged next", got.Status)
	}
}

// TestRunTask_StandaloneDispatch proves a standalone task is marked
// in_progress and dispatched via the injected TaskStartNowFunc.
func TestRunTask_StandaloneDispatch(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedStandaloneTask(t, store, task.StatusNext, "worker")

	var dispatchedTaskID string
	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(func(_ context.Context, taskID string) (string, error) {
		dispatchedTaskID = taskID
		return "sess-42", nil
	})

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if res.IsError {
		t.Fatalf("run_task: %s", res.ForLLM)
	}
	if dispatchedTaskID != tk.ID {
		t.Errorf("dispatcher called with %q, want %q", dispatchedTaskID, tk.ID)
	}

	var out struct {
		TaskID    string `json:"task_id"`
		Status    string `json:"status"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	if out.SessionID != "sess-42" {
		t.Errorf("session_id = %q, want sess-42", out.SessionID)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusInProgress {
		t.Errorf("status = %q, want in_progress", got.Status)
	}
}

// TestRunTask_UnwiredDispatcher_FailsClosed proves run_task refuses to run a
// task when no dispatcher is installed, and leaves the task's status
// unchanged (never a silent partial launch).
func TestRunTask_UnwiredDispatcher_FailsClosed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedStandaloneTask(t, store, task.StatusNext, "worker")

	tool := NewTaskRunTool(store) // SetStartTaskNow never called

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if !res.IsError {
		t.Fatal("expected fail-closed rejection when the dispatcher is unwired")
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Errorf("status = %q, want unchanged next", got.Status)
	}
}

// TestRunTask_RejectsDoneTask proves a completed (frozen) task cannot be re-run.
func TestRunTask_RejectsDoneTask(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedStandaloneTask(t, store, task.StatusDone, "worker")

	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(func(context.Context, string) (string, error) { return "sess", nil })

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if !res.IsError {
		t.Fatal("expected rejection for a done task")
	}
}

// TestRunTask_RejectsBlockedTask proves a task blocked on an unmet
// dependency cannot be manually run.
func TestRunTask_RejectsBlockedTask(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())

	// Seed a GENUINELY blocked task: an unmet blocker plus a dependent that
	// names it. Seeding StatusBlocked directly no longer works — Store.Create
	// derives `blocked` from blocked_by (the S2 UAT fix that made the lane
	// reachable at all), so a task marked blocked with an EMPTY blocked_by is
	// an inconsistent state and is correctly recomputed back to `next`. The
	// old fixture seeded exactly that impossible state, so the tool saw a
	// runnable task and this test failed for the wrong reason.
	blocker := seedStandaloneTask(t, store, task.StatusNext, "worker")
	tk := &task.Task{
		Title: "dependent", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "worker", WorkspaceID: "ws-1", Status: task.StatusNext,
		BlockedBy: []string{blocker.ID},
	}
	if err := store.Create(tk); err != nil {
		t.Fatalf("seed dependent task: %v", err)
	}
	if tk.Status != task.StatusBlocked {
		t.Fatalf("fixture precondition: dependent must derive to blocked, got %q", tk.Status)
	}

	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(func(context.Context, string) (string, error) { return "sess", nil })

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if !res.IsError {
		t.Fatal("expected rejection for a blocked task")
	}
}

// TestRunTask_RejectsFailedTask_ReRunnable proves a `failed` standalone task
// IS re-runnable (task status, unlike Plan state, is not frozen at failed —
// mirrors the board's Play affordance on a failed task).
func TestRunTask_RejectsFailedTask_ReRunnable(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedStandaloneTask(t, store, task.StatusFailed, "worker")

	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(func(context.Context, string) (string, error) { return "sess", nil })

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if res.IsError {
		t.Fatalf("expected a failed standalone task to be re-runnable: %s", res.ForLLM)
	}
}

// TestRunTask_RejectsNoAgent proves a task with no assigned agent cannot be run.
func TestRunTask_RejectsNoAgent(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedStandaloneTask(t, store, task.StatusNext, "")

	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(func(context.Context, string) (string, error) { return "sess", nil })

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if !res.IsError {
		t.Fatal("expected rejection for a task with no assigned agent")
	}
}

// TestRunTask_DispatchFailure_Reverts proves a dispatch failure reverts the
// task's status rather than stranding it in_progress with no agent running.
func TestRunTask_DispatchFailure_Reverts(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedStandaloneTask(t, store, task.StatusNext, "worker")

	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(func(context.Context, string) (string, error) {
		return "", errors.New("dispatch cap reached")
	})

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if !res.IsError {
		t.Fatal("expected error result on dispatch failure")
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Errorf("status = %q after dispatch failure, want reverted to next", got.Status)
	}
}

// TestRunTask_AlreadyInProgress_Idempotent proves run_task on an already
// in_progress task calls the (idempotent) dispatcher without erroring.
func TestRunTask_AlreadyInProgress_Idempotent(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedStandaloneTask(t, store, task.StatusInProgress, "worker")

	calls := 0
	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(func(context.Context, string) (string, error) {
		calls++
		return "sess-existing", nil
	})

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if res.IsError {
		t.Fatalf("run_task on already-running task: %s", res.ForLLM)
	}
	if calls != 1 {
		t.Errorf("dispatcher called %d times, want 1", calls)
	}
}

// alreadyRunningDispatcher returns a TaskStartNowFunc whose only job is to
// reproduce what *pkg/agent.TaskExecutor.StartTaskNow returns to a caller
// that lost the launch race: the typed already-running sentinel, wrapped.
func alreadyRunningDispatcher(taskID string) TaskStartNowFunc {
	return func(_ context.Context, id string) (string, error) {
		return "", fmt.Errorf("task_executor: StartTaskNow: claim: task %q: %w", taskID, task.ErrAlreadyRunning)
	}
}

// TestRunTask_AlreadyRunningSentinel_IsSafeNoOp proves the documented
// contract ("Calling this on an already-running task is a safe no-op
// returning the existing session") is TRUE for a caller that lost the start
// race: the dispatcher's typed already-running sentinel must come back as a
// SUCCESS result carrying the bound session id, and — critically — the
// loser must NOT revert the pre-stamped in_progress status of the winner's
// live run (the revert-on-loser regression this fix closes). Red on the
// pre-fix code, which returned an error and reverted status+StartedAt.
func TestRunTask_AlreadyRunningSentinel_IsSafeNoOp(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedStandaloneTask(t, store, task.StatusNext, "worker")
	// Seed the winner's already-bound session: a task whose start is in
	// flight under another caller carries a session id on the record.
	sid := "sess-existing"
	_, err := store.Update(tk.ID, task.Patch{SessionID: &sid})
	if err != nil {
		t.Fatalf("seed session id: %v", err)
	}

	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(alreadyRunningDispatcher(tk.ID))

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if res.IsError {
		t.Fatalf("an already-running start must be a safe no-op, not an error: %s", res.ForLLM)
	}

	var out struct {
		TaskID    string `json:"task_id"`
		Status    string `json:"status"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	if out.Status != string(task.StatusInProgress) {
		t.Errorf("status = %q, want in_progress", out.Status)
	}
	if out.SessionID != "sess-existing" {
		t.Errorf("session_id = %q, want the existing bound session sess-existing", out.SessionID)
	}

	// The winner's live run state must be intact: no revert of status and no
	// StartedAt wipe.
	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusInProgress {
		t.Errorf("status = %q after already-running no-op, want in_progress (no revert)", got.Status)
	}
	if got.StartedAt == "" {
		t.Error("StartedAt must survive the no-op (loser must not wipe the winner's stamp)")
	}
}

// TestRunTask_InProgressBranch_AlreadyRunningSentinel_NoOp proves the
// in_progress re-dispatch branch treats the dispatcher's already-running
// sentinel as the documented safe no-op (success with the existing session),
// not as "could not confirm running task". Red on the pre-fix code, which
// surfaced the loser error.
func TestRunTask_InProgressBranch_AlreadyRunningSentinel_NoOp(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedStandaloneTask(t, store, task.StatusInProgress, "worker")
	// Seed the live run's bound session (a live run holds one).
	sid := "sess-existing"
	_, err := store.Update(tk.ID, task.Patch{SessionID: &sid})
	if err != nil {
		t.Fatalf("seed session id: %v", err)
	}

	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(alreadyRunningDispatcher(tk.ID))

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if res.IsError {
		t.Fatalf("already-running re-dispatch must be a safe no-op, not an error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "sess-existing") {
		t.Errorf("result must carry the existing session id, got: %s", res.ForLLM)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusInProgress {
		t.Errorf("status = %q after no-op, want in_progress (no revert)", got.Status)
	}
}

// TestRunTask_NotFound proves run_task rejects an unknown task_id.
func TestRunTask_NotFound(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(func(context.Context, string) (string, error) { return "sess", nil })

	res := tool.Execute(context.Background(), map[string]any{"task_id": "nonexistent"})
	if !res.IsError {
		t.Fatal("expected rejection for a nonexistent task")
	}
}

// TestRunTask_RequiresTaskID proves task_id is a required arg.
func TestRunTask_RequiresTaskID(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskRunTool(store)
	res := tool.Execute(context.Background(), map[string]any{})
	if !res.IsError {
		t.Fatal("expected rejection for a missing task_id")
	}
}

// TestRunTask_NilStore_FailsClosed proves a nil store (metadata-only
// construction) never executes.
func TestRunTask_NilStore_FailsClosed(t *testing.T) {
	t.Parallel()
	tool := NewTaskRunTool(nil)
	res := tool.Execute(context.Background(), map[string]any{"task_id": "x"})
	if !res.IsError {
		t.Fatal("expected error with nil task store")
	}
}
