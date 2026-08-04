// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_superseded_session_test.go covers M6 (UAT 2026-07-31):
// run_task's retry-with-steering loop mints a BRAND NEW session for every
// redispatched attempt (createTaskSessionSync's sessStore.NewSession call,
// reached again once the caller's deferred closure re-enters ExecuteTask
// with the redispatchTaskID these tests capture directly), but before this
// fix nothing ever closed out the PREVIOUS attempt's own session — only the
// FINAL attempt's session was ever touched (completeTaskWithResult's direct
// session.StatusArchived SetMeta + finalizeTaskLifecycle). Every intermediate,
// superseded attempt kept session.StatusActive permanently: the sessions
// list accumulates entries that look live forever, misleading anything that
// counts or reconciles active work (UI, usage accounting, orphan sweeps).
//
// Both call sites task_executor.go's finishTaskRun can route a redispatch
// through are covered: consumeAttemptOrExhaust (the attempt-consuming
// no-signal/judge-unmet path) and rejectBareEvidenceClaim's free re-dispatch
// (the non-attempt-consuming first-bare-claim path) — both mint a fresh
// session on their next real dispatch and both, before this fix, left their
// OWN taskSessionID stuck at StatusActive.
//
// Following this package's own precedent (evidence_gate_test.go's
// TestEvidenceGate_ConsecutiveRejectionsRouteThroughAttemptBudgetOnSecond),
// these tests call finishTaskRun directly against a manually-created,
// real UnifiedStore-backed session — real store-backed state, not a spy —
// rather than driving a full ExecuteTask dispatch/goroutine cycle, since
// finishTaskRun is exactly where consumeAttemptOrExhaust/
// rejectBareEvidenceClaim are reached from.
package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestConsumeAttemptOrExhaust_SupersededSessionTransitionsToInterrupted
// covers the no-signal path (FR-045): a worker response with no TASK_STATUS
// marker at all routes straight to consumeAttemptOrExhaust without ever
// needing a judge. With MaxAttempts=2, the first attempt's outcome (newAttempt
// 1 < maxAttempts 2) takes the RE-DISPATCH branch (writeSteeringPrompt), the
// exact branch this fix's transitionTaskLifecycle call was added to.
//
// Positive lower bound (Binding Rule 4): this does not merely assert
// Status != StatusActive — it pins the EXACT terminal status
// (StatusInterrupted, via LifecycleCancelled's canonical mirror in
// lifecycle_bridge.go), and also confirms the redispatch actually happened
// (non-empty redispatch id, AttemptCount incremented, Status == next) so the
// test cannot pass vacuously against a branch that silently didn't run.
func TestConsumeAttemptOrExhaust_SupersededSessionTransitionsToInterrupted(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	taskStore := GetTaskStore(al)
	maxAttempts := 2
	tk := &task.Task{
		ID: "t-superseded-consume-attempt", AgentID: "native-agent", WorkspaceID: "test-ws",
		Title:       "superseded attempt session (consumeAttemptOrExhaust)",
		Status:      task.StatusInProgress,
		MaxAttempts: &maxAttempts,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	sessStore := al.GetAgentStore("native-agent")
	if sessStore == nil {
		t.Fatal("native-agent session store not available")
	}
	meta, err := sessStore.NewSession(session.SessionTypeTask, "system", "native-agent")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	taskSessionID := meta.ID

	// Precondition (positive control): a freshly minted session must start
	// Active — otherwise a "not Active anymore" assertion below would be
	// meaningless.
	before, err := sessStore.GetMeta(taskSessionID)
	if err != nil {
		t.Fatalf("get session meta before finishTaskRun: %v", err)
	}
	if before.Status != session.StatusActive {
		t.Fatalf("precondition failed: freshly created session status = %q, want %q",
			before.Status, session.StatusActive)
	}

	resp := "did some work but forgot to report a completion signal"
	redispatch := al.taskExecutor.finishTaskRun(context.Background(), tk, taskSessionID, resp, nil, "")

	if redispatch == "" {
		t.Fatal("expected a non-empty re-dispatch id — a no-signal outcome with attempts remaining must " +
			"re-dispatch, not this test proves nothing about the superseded branch otherwise")
	}
	if redispatch != tk.ID {
		t.Errorf("redispatch id = %q, want %q", redispatch, tk.ID)
	}

	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if final.AttemptCount != 1 {
		t.Fatalf("attempt_count = %d, want 1 — the no-signal outcome must consume an attempt via "+
			"consumeAttemptOrExhaust", final.AttemptCount)
	}
	if final.Status != task.StatusNext {
		t.Fatalf("status = %q, want %q (re-dispatchable) — the redispatch branch, not attempt exhaustion, "+
			"must have been taken (maxAttempts=2, this is attempt 1)", final.Status, task.StatusNext)
	}

	after, err := sessStore.GetMeta(taskSessionID)
	if err != nil {
		t.Fatalf("get session meta after finishTaskRun: %v", err)
	}
	if after.Status != session.StatusInterrupted {
		t.Errorf("superseded attempt's session status = %q, want %q — M6: consumeAttemptOrExhaust's "+
			"re-dispatch branch supersedes this session (a brand new one is minted for the next attempt) "+
			"but never transitioned THIS session out of Active, so it would stay Active forever",
			after.Status, session.StatusInterrupted)
	}
}

// TestRejectBareEvidenceClaim_SupersededSessionTransitionsToInterrupted
// covers the OTHER redispatch path: a bare completion claim (a TASK_STATUS
// marker with no preceding [goal:evidence] line) takes the FREE re-dispatch
// branch in rejectBareEvidenceClaim (the first rejection for a task does not
// consume an attempt — see evidence_gate_test.go), which — like
// consumeAttemptOrExhaust — mints a fresh session for the next attempt and,
// before this fix, left THIS attempt's session stuck at StatusActive too.
func TestRejectBareEvidenceClaim_SupersededSessionTransitionsToInterrupted(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		t.Fatal("the verifier must NEVER be dispatched for a bare (evidence-free) completion claim")
		return nil, nil
	}}

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-superseded-bare-evidence", AgentID: "native-agent", WorkspaceID: "test-ws",
		Title:    "superseded attempt session (rejectBareEvidenceClaim)",
		Status:   task.StatusInProgress,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "must do X")},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	sessStore := al.GetAgentStore("native-agent")
	if sessStore == nil {
		t.Fatal("native-agent session store not available")
	}
	meta, err := sessStore.NewSession(session.SessionTypeTask, "system", "native-agent")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	taskSessionID := meta.ID

	before, err := sessStore.GetMeta(taskSessionID)
	if err != nil {
		t.Fatalf("get session meta before finishTaskRun: %v", err)
	}
	if before.Status != session.StatusActive {
		t.Fatalf("precondition failed: freshly created session status = %q, want %q",
			before.Status, session.StatusActive)
	}

	resp := "I finished the work.\nTASK_STATUS: success\n" // no [goal:evidence] line — bare claim
	redispatch := al.taskExecutor.finishTaskRun(context.Background(), tk, taskSessionID, resp, nil, "")

	if redispatch == "" {
		t.Fatal("expected a non-empty re-dispatch id — the FIRST bare-claim rejection re-dispatches for free")
	}

	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if final.AttemptCount != 0 {
		t.Fatalf("attempt_count = %d, want 0 — the first bare-claim rejection must NOT consume an attempt "+
			"(FR-035) — this pins we're in the free-redispatch branch, not consumeAttemptOrExhaust's",
			final.AttemptCount)
	}

	after, err := sessStore.GetMeta(taskSessionID)
	if err != nil {
		t.Fatalf("get session meta after finishTaskRun: %v", err)
	}
	if after.Status != session.StatusInterrupted {
		t.Errorf("superseded attempt's session status = %q, want %q — M6: rejectBareEvidenceClaim's free "+
			"re-dispatch branch supersedes this session but never transitioned it out of Active, so it "+
			"would stay Active forever",
			after.Status, session.StatusInterrupted)
	}
}
