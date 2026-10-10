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

// allowAllDelegation installs a delegation pre-check that permits every
// caller->assignee pair, so a test can reach the dispatch stage without
// exercising the policy gate. run_task fails closed when no checker is
// installed (founder ruling 2026-10-10: the delegation policy always applies
// to an agent-initiated run), so every test that expects to dispatch — or that
// tests a stage AFTER the gate — must install this.
func allowAllDelegation(tool *TaskRunTool) {
	tool.SetDelegationDenyChecker(func(context.Context, string, string) *DelegationDenial { return nil })
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
	allowAllDelegation(tool)
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
	allowAllDelegation(tool)      // isolate the dispatcher gate: reach it, don't stop at the policy gate

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

// TestRunTask_UnwiredDelegationChecker_FailsClosed proves run_task refuses an
// agent-initiated start when no delegation-policy pre-check is installed: the
// policy always applies (founder ruling 2026-10-10), so a missing checker is a
// configuration error, never a silent launch. The task is left byte-identical —
// the refusal happens before any status write.
func TestRunTask_UnwiredDelegationChecker_FailsClosed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedStandaloneTask(t, store, task.StatusNext, "worker")

	dispatchCalled := false
	tool := NewTaskRunTool(store)
	// SetDelegationDenyChecker never called — the policy gate is unwired.
	tool.SetStartTaskNow(func(context.Context, string) (string, error) {
		dispatchCalled = true
		return "sess", nil
	})

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if !res.IsError {
		t.Fatal("expected fail-closed rejection when the delegation checker is unwired")
	}
	if dispatchCalled {
		t.Fatal("run_task must not dispatch when the delegation policy gate is unwired")
	}
	if !strings.Contains(res.ForLLM, "delegation is not configured") {
		t.Errorf("rejection must name the missing policy gate, got: %s", res.ForLLM)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Errorf("status = %q, want unchanged next (refused before any write)", got.Status)
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
	allowAllDelegation(tool)
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
	allowAllDelegation(tool)
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

// TestRunTask_DispatchFailure_UnrelatedStoredFailure_StillReverts proves the
// caller's Failed-disposition preservation guard (run_task.go's
// failedDisposition check, "A successful executor failure write stores the
// same cause it returns. Do not erase that disposition, or mistake an
// unrelated failure for it.") is scoped to THIS dispatch attempt's own cause.
// If the store already holds a Failed status with a DIFFERENT nonempty
// result — e.g. an out-of-band writer recorded its own unrelated failure
// between the in_progress transition and this dispatch call's own return —
// run_task must still perform the ordinary revert: restore the prior status,
// clear StartedAt, and surface this dispatch's own error, exactly as the
// matching-status-but-no-Failed-write case above does.
//
// Oracle: #1026 CHECK round 5 F1 (independent audit,
// coordination/logs/fix890-opus/1026-check5/REPORT.md). The mutant that
// replaces the cause-equality comparison at run_task.go:176
// (`fresh.Result == startErr.Error()`) with `true` survives
// TestRunTaskTool_SessionBindingFailureRemainsFailed (its store only ever
// reaches Failed with the SAME cause the executor returns — the real
// executor's failTaskBeforeDispatch always persists exactly the error it
// also returns) and TestRunTask_DispatchFailure_Reverts above (its fresh
// status stays in_progress, so the Failed guard is already false there
// regardless of the comparison). Only a differing stored cause exercises the
// equality itself. This fixture installs the differing cause directly via
// the same (context.Context, string) (string, error) SetStartTaskNow seam
// every other test in this file already uses — no new mock shape.
func TestRunTask_DispatchFailure_UnrelatedStoredFailure_StillReverts(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedStandaloneTask(t, store, task.StatusNext, "worker")

	const unrelatedCause = "disk write failure from an unrelated earlier attempt"
	dispatchErr := errors.New("dispatch cap reached")

	tool := NewTaskRunTool(store)
	allowAllDelegation(tool)
	tool.SetStartTaskNow(func(context.Context, string) (string, error) {
		// Simulate an out-of-band writer that already recorded the task
		// Failed for a DIFFERENT reason before this dispatch attempt's own
		// error comes back (the real executor never produces this shape on
		// its own dispatch path — its Failed write always carries the exact
		// error it also returns, which is why the caller's other tests can't
		// reach this branch). This exercises run_task's own defensive
		// comparison at the exact (ctx, taskID) (string, error) boundary the
		// caller already treats as real.
		failed := task.StatusFailed
		unrelated := unrelatedCause
		if _, uerr := store.Update(tk.ID, task.Patch{Status: &failed, Result: &unrelated}); uerr != nil {
			t.Fatalf("seed unrelated Failed disposition before returning dispatch error: %v", uerr)
		}
		return "", dispatchErr
	})

	res := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if !res.IsError {
		t.Fatal("expected error result on dispatch failure")
	}
	if !strings.Contains(res.ForLLM, dispatchErr.Error()) {
		t.Errorf("result = %q, want this dispatch attempt's own visible error %q", res.ForLLM, dispatchErr.Error())
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Errorf("status = %q after dispatch failure with an unrelated stored Failed cause, want reverted to next (must not preserve a stale, unrelated Failed disposition)", got.Status)
	}
	if got.StartedAt != "" {
		t.Errorf("started_at = %q after rollback, want cleared (not a phantom timestamp from the reverted in_progress transition)", got.StartedAt)
	}
	if got.Result != unrelatedCause {
		t.Errorf("result = %q after rollback, want the unrelated cause left untouched (%q) — the revert patch must not overwrite Result", got.Result, unrelatedCause)
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
	allowAllDelegation(tool)
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
