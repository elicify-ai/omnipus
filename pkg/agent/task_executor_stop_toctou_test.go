// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_stop_toctou_test.go covers the ADR-052 FR-014/§6.4(b) Stop
// guarantee's executor-side TOCTOU fix (7-reviewer + architect gate,
// final-fix wave). The three outcome-writing call sites
// (consumeAttemptOrExhaust, completeTaskWithResult, and
// rejectBareEvidenceClaim's free-retry write) previously wrote via a plain
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

// TestConsumeAttemptOrExhaust_ReviveGuard_DropsOutcomeAfterConcurrentStop is
// interleaving (a): pre-fix, this call would blindly Update Status->next +
// AttemptCount+1 against a stale `t`, REVIVING a task the user had just
// Stopped (and wiping its stopped_by_user marker via updateLocked's own
// leaving-failed auto-clear). Post-fix, the CAS write conflicts (the task
// is no longer in_progress on disk) and the outcome is dropped.
func TestConsumeAttemptOrExhaust_ReviveGuard_DropsOutcomeAfterConcurrentStop(t *testing.T) {
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

	redispatch := al.taskExecutor.consumeAttemptOrExhaust(context.Background(), &stale, "", "unmet claim", nil, nil)

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

// TestConsumeAttemptOrExhaust_ExhaustedBranch_DropsTerminalWriteAfterStop
// proves the SAME guard covers the exhausted-attempts branch's OWN terminal
// write (completeTaskWithResult called with expected=next from inside
// consumeAttemptOrExhaust): the attempt-increment CAS itself must conflict
// first (task is not in_progress), so the exhausted branch is never even
// reached — the terminal handover write and wakeOwnerAttemptsExhausted must
// not fire either.
func TestConsumeAttemptOrExhaust_ExhaustedBranch_DropsTerminalWriteAfterStop(t *testing.T) {
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

	redispatch := al.taskExecutor.consumeAttemptOrExhaust(context.Background(), &stale, "", "unmet claim", nil, nil)
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

// TestCompleteTaskWithResult_DoneOverwriteGuard_DropsOutcomeAfterConcurrentStop
// is interleaving (b): a stale MET claim resolving to `done` must never
// overwrite a task the user Stopped. Also proves the returned `applied`
// bool correctly reports false on a dropped write (callers gate
// wakeOwnerAttemptsExhausted on this).
func TestCompleteTaskWithResult_DoneOverwriteGuard_DropsOutcomeAfterConcurrentStop(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)

	tk := &task.Task{
		ID: "t-done-overwrite-guard", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "done overwrite guard",
		Status: task.StatusInProgress,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	stale := *tk
	simulateConcurrentStop(t, taskStore, tk.ID)

	applied := al.taskExecutor.completeTaskWithResult(&stale, "", task.StatusInProgress, true, "claims success", nil)
	if applied {
		t.Fatal("completeTaskWithResult must report applied=false on a CAS conflict")
	}

	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusFailed || final.CancelReason != task.CancelReasonStoppedByUser {
		t.Fatalf("status=%q cancel_reason=%q, want the Stop outcome UNCHANGED — a stale MET claim must never "+
			"silently complete a cancelled task as done", final.Status, final.CancelReason)
	}
	if final.Result == "claims success" {
		t.Error("the stale claim's Result must never overwrite the Stop's own Result")
	}
}

// TestRejectBareEvidenceClaim_FreeRetry_DropsOutcomeAfterConcurrentStop
// covers the FREE (non-attempt-consuming) re-dispatch write — the same
// revive risk as interleaving (a), via a DIFFERENT call site than
// consumeAttemptOrExhaust.
func TestRejectBareEvidenceClaim_FreeRetry_DropsOutcomeAfterConcurrentStop(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)

	tk := &task.Task{
		ID: "t-free-retry-guard", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "free retry guard",
		Status: task.StatusInProgress,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	stale := *tk
	simulateConcurrentStop(t, taskStore, tk.ID)

	redispatch := al.taskExecutor.rejectBareEvidenceClaim(context.Background(), &stale, "", "steering text", nil)
	if redispatch != "" {
		t.Errorf("must not re-dispatch a task the Stop already claimed, got %q", redispatch)
	}
	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusFailed || final.CancelReason != task.CancelReasonStoppedByUser {
		t.Fatalf("status=%q cancel_reason=%q, want unchanged (the free-retry write must not revive the "+
			"stopped task)", final.Status, final.CancelReason)
	}
}

// TestRejectBareEvidenceClaim_StreakExhaust_DropsOutcomeAfterConcurrentStop
// is the "streak-exhaust path" the fix-wave brief names explicitly (the
// SECOND consecutive bare-evidence rejection, which routes through
// consumeAttemptOrExhaust — task_executor.go:828 pre-edit): proves the
// guard covers this specific entry point end-to-end, not just
// consumeAttemptOrExhaust in isolation.
func TestRejectBareEvidenceClaim_StreakExhaust_DropsOutcomeAfterConcurrentStop(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)

	tk := &task.Task{
		ID: "t-streak-exhaust-guard", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "streak exhaust guard",
		Status: task.StatusInProgress,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	// Pre-load the streak to 1 (the FIRST, free rejection already happened
	// for this task) so the NEXT call reaches the exhaust threshold
	// (evidenceGateMaxConsecutiveRejections == 2) and routes through
	// consumeAttemptOrExhaust.
	al.taskExecutor.bumpEvidenceRejectStreak(tk.ID)

	stale := *tk
	simulateConcurrentStop(t, taskStore, tk.ID)

	redispatch := al.taskExecutor.rejectBareEvidenceClaim(context.Background(), &stale, "", "steering text", nil)
	if redispatch != "" {
		t.Errorf("must not re-dispatch, got %q", redispatch)
	}
	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusFailed || final.CancelReason != task.CancelReasonStoppedByUser {
		t.Fatalf("status=%q cancel_reason=%q, want unchanged", final.Status, final.CancelReason)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a dropped conflict write must not consume an attempt", final.AttemptCount)
	}
}
