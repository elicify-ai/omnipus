// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_adjudicate_claim_test.go covers TaskExecutor.adjudicateClaim's
// two 7-reviewer-gate fixes (ADR-052 fix wave): item 3 (an empty completion
// claim fails closed BEFORE any verifier dispatch — never a slow judge/
// verifier turn for a claim with nothing to adjudicate) and item 4 (FR-014
// member-path parity — a judge verdict computed while the task concurrently
// left in_progress, e.g. via a Stop, must be dropped rather than silently
// overwriting the Stop outcome or consuming an attempt for a run that was
// actually cancelled).

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestTaskExecutor_AdjudicateClaim_EmptyClaimFailsClosedWithoutVerifierDispatch
// proves item 3: a completion claim with no real content (whitespace-only)
// is rejected fail-closed BEFORE al.JudgeCriteria/runVerifierAdjudication is
// ever reached — the empty-output regression's root cause, per the fix
// wave's finding, was exactly this path dispatching a full verifier turn
// for a claim with nothing to check evidence against.
func TestTaskExecutor_AdjudicateClaim_EmptyClaimFailsClosedWithoutVerifierDispatch(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-empty-claim", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "empty claim test",
		Status:   task.StatusInProgress,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "must do X")},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	redispatch := al.taskExecutor.adjudicateClaim(context.Background(), tk, "", "   ", nil)

	if fake.callCount() != 0 {
		t.Fatalf("an empty completion claim must never dispatch the verifier; callCount=%d", fake.callCount())
	}
	if redispatch == "" {
		t.Fatal("expected a re-dispatch id (attempt 1 of the default 3, consumed via the goal loop)")
	}
	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status == task.StatusDone {
		t.Fatal("an empty claim must never resolve to done")
	}
	if final.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1 (the fail-closed empty-claim reason still consumes an attempt "+
			"like any other unmet outcome)", final.AttemptCount)
	}
	if !strings.Contains(final.Result, "empty claim summary") {
		t.Errorf("result = %q, want it to explain the empty-claim fail-closed reason", final.Result)
	}
}

// TestTaskExecutor_AdjudicateClaim_FR014_DropsStaleVerdictAfterConcurrentStop
// proves item 4: JudgeCriteria's verifier turn runs OUTSIDE any lock, so a
// concurrent Stop (PlanEngine.StopTask, plan_engine.go) can flip the task
// out of in_progress WHILE the judge call is still in flight. adjudicateClaim
// must re-check the task's current status before applying the verdict —
// dropping it (no verdict write, no attempt consumption) rather than
// clobbering the Stop outcome, mirroring plan_engine.go's
// verdictStillApplicable at the plan-round level.
func TestTaskExecutor_AdjudicateClaim_FR014_DropsStaleVerdictAfterConcurrentStop(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	registered := make(chan struct{})
	proceed := make(chan struct{})
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		close(registered)
		<-proceed
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-fr014", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "fr014 test",
		Status:   task.StatusInProgress,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "the thing is done")},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	done := make(chan string, 1)
	go func() {
		done <- al.taskExecutor.adjudicateClaim(context.Background(), tk, "", "I finished the thing", nil)
	}()

	<-registered
	// Simulate a concurrent Stop (mirrors plan_engine.go's cancelMemberLocked)
	// landing WHILE the judge call above is still blocked on <-proceed.
	failedStatus := task.StatusFailed
	cancelReason := task.CancelReasonStoppedByUser
	stopResult := "[reason:stopped_by_user] Cancelled by tester via Stop."
	if _, err := taskStore.Update(tk.ID, task.Patch{
		Status: &failedStatus, CancelReason: &cancelReason, Result: &stopResult,
	}); err != nil {
		t.Fatalf("simulate concurrent Stop: %v", err)
	}
	close(proceed)
	redispatch := <-done

	if redispatch != "" {
		t.Errorf("adjudicateClaim must not request a re-dispatch once the verdict was dropped, got %q", redispatch)
	}
	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusFailed || final.CancelReason != task.CancelReasonStoppedByUser {
		t.Fatalf("task status=%q cancel_reason=%q, want the Stop outcome UNCHANGED by the stale verdict",
			final.Status, final.CancelReason)
	}
	if final.Result != stopResult {
		t.Errorf("result = %q, want it UNCHANGED from the Stop's own write (%q) — "+
			"the stale MET verdict must never overwrite it", final.Result, stopResult)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a dropped stale verdict must not consume an attempt", final.AttemptCount)
	}
}
