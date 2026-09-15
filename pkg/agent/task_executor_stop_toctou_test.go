// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_stop_toctou_test.go covers the ADR-052 FR-014/§6.4(b) Stop
// guarantee's executor-side TOCTOU fix (7-reviewer + architect gate,
// final-fix wave). The outcome-writing call sites
// (consumeTaskAttempt and completeTaskWithResult) previously wrote via a plain
// task.Store.Update with NO guard against a concurrent Stop having already
// moved the task out of in_progress — a Stop landing between the caller's
// stale read of a task and one of these writes could be silently
// overwritten: interleaving (a) "revive via consumeAttempt" (Status ->
// next, CancelReason auto-cleared by updateLocked's own leaving-failed
// clear — reviving a task the user just stopped and letting runTask's own
// defer re-dispatch it) and interleaving (b) "done overwrite" (a stale MET
// verdict resolving failed[stopped_by_user] -> done, recording cancelled
// work as successfully DONE). All three writes are now a
// task.Store.UpdateIfStatus compare-and-swap against the status the caller
// believes the task is still at; a conflict drops the outcome (logged, no
// re-dispatch) rather than applying it.
//
// Every test below drives the task store DIRECTLY to simulate the
// interleaving (mirrors task_executor_adjudicate_claim_test.go's own FR-014
// test, and the sanctioned "driving the store directly to simulate the
// interleaving" technique) rather than racing goroutines: the function
// under test is called with a deliberately-STALE in-memory *task.Task
// (Status still in_progress) while the ON-DISK task has already been moved
// to failed+stopped_by_user — exactly the state a goroutine holds after
// re-reading a task but before a concurrent Stop lands and it decides/writes
// its own outcome. This is deterministic (no timing/goroutine races) and,
// unlike a goroutine-based race, would have reliably FAILED against the
// pre-fix code (plain Update, no CAS) — verified by temporarily disabling
// each CAS call and re-running (see the final report for details).
package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// simulateConcurrentStop drives taskStore directly to the SAME state
// PlanEngine.cancelMemberLocked (plan_engine.go) writes on a real Stop —
// bypassing StopTask/StopPlan's own locking entirely, exactly simulating
// "a concurrent Stop already landed" without relying on scheduler timing.
func simulateConcurrentStop(t *testing.T, taskStore *task.Store, id string) {
	t.Helper()
	failedStatus := task.StatusFailed
	cancelReason := task.CancelReasonStoppedByUser
	stopResult := "[reason:stopped_by_user] Cancelled by tester via Stop."
	if _, err := taskStore.Update(id, task.Patch{
		Status: &failedStatus, CancelReason: &cancelReason, Result: &stopResult,
	}); err != nil {
		t.Fatalf("simulate concurrent Stop: %v", err)
	}
}

// TestConsumeTaskAttempt_ReviveGuard_DropsOutcomeAfterConcurrentStop is
// interleaving (a): pre-fix, this call would blindly Update Status->next +
// AttemptCount+1 against a stale `t`, REVIVING a task the user had just
// Stopped (and wiping its stopped_by_user marker via updateLocked's own
// leaving-failed auto-clear). Post-fix, the CAS write conflicts (the task
// is no longer in_progress on disk) and the outcome is dropped.
func TestConsumeTaskAttempt_ReviveGuard_DropsOutcomeAfterConcurrentStop(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)

	tk := &task.Task{
		ID: "t-revive-guard", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "revive guard",
		Status: task.StatusInProgress,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	stale := *tk // the caller's in-memory view, taken BEFORE the simulated Stop
	simulateConcurrentStop(t, taskStore, tk.ID)

	redispatch := al.taskExecutor.consumeTaskAttempt(context.Background(), &stale, "", "unmet claim", nil)

	if redispatch != "" {
		t.Errorf("must not re-dispatch a task the Stop already claimed, got %q", redispatch)
	}
	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusFailed || final.CancelReason != task.CancelReasonStoppedByUser {
		t.Fatalf("status=%q cancel_reason=%q, want the Stop outcome UNCHANGED (not revived to next)",
			final.Status, final.CancelReason)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a dropped conflict write must not consume an attempt", final.AttemptCount)
	}
}

// TestConsumeTaskAttempt_ExhaustedBranch_DropsTerminalWriteAfterStop
// proves the SAME guard covers the exhausted-attempts branch's OWN terminal
// write (completeTaskWithResult called with expected=next from inside
// consumeTaskAttempt): the attempt-increment CAS itself must conflict
// first (task is not in_progress), so the exhausted branch is never even
// reached — the terminal handover write and wakeOwnerAttemptsExhausted must
// not fire either.
func TestConsumeTaskAttempt_ExhaustedBranch_DropsTerminalWriteAfterStop(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)

	maxAttempts := 1
	tk := &task.Task{
		ID: "t-exhaust-guard", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "exhaust guard",
		Status: task.StatusInProgress, MaxAttempts: &maxAttempts, AttemptCount: 0,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	stale := *tk
	simulateConcurrentStop(t, taskStore, tk.ID)

	redispatch := al.taskExecutor.consumeTaskAttempt(context.Background(), &stale, "", "unmet claim", nil)
	if redispatch != "" {
		t.Errorf("must not re-dispatch, got %q", redispatch)
	}
	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Result != "[reason:stopped_by_user] Cancelled by tester via Stop." {
		t.Errorf("Result = %q, want the Stop's own Result UNCHANGED — the exhausted-branch handover must never land",
			final.Result)
	}
}
