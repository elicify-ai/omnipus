// Omnipus — run_task Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"errors"
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
	tk := seedStandaloneTask(t, store, task.StatusBlocked, "worker")

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
