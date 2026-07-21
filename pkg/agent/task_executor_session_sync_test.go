// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_session_sync_test.go pins M1 (ADR-052 FR-029): a member
// task's SessionID must be assigned + persisted SYNCHRONOUSLY, before
// ExecuteTask ever returns — not asynchronously inside the run goroutine —
// so a plan-engine Stop fan-out (planDecisionMu) issued the instant after
// dispatch always has a cancel handle (SC-005).
//
// The scriptedProvider fixtures below carry a "[goal:evidence] ..." line
// immediately before "TASK_STATUS: success" (ADR-052 FR-035's
// evidence-marker gate, task_executor.go's finishTaskRun) — without it the
// gate would re-prompt instead of letting the claim reach the (always-met)
// judge these tests rely on for a deterministic single-attempt Done.

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// metJudgeProviderC1 scripts the Judge System Agent to always return a
// well-formed, immediately-met verdict for criterion "c1" — used by these
// tests to keep the goal loop deterministic (single attempt, terminal Done)
// so it does not interfere with the specific M1 assertion each test makes.
func metJudgeProviderC1() *fakeJudgeProvider {
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"evidenced"}]}`,
		}, nil
	}}
}

// TestExecuteTask_SessionIDAssignedSynchronouslyBeforeReturn calls
// ExecuteTask and immediately (no polling, no sleep) re-reads the task from
// the store: M1 requires SessionID to already be persisted at that instant.
// Before the M1 fix, the session was created asynchronously inside the run
// goroutine (task_executor.go's old runTask), so this exact assertion would
// have been flaky-to-always-failing depending on goroutine scheduling.
func TestExecuteTask_SessionIDAssignedSynchronouslyBeforeReturn(t *testing.T) {
	worker := &scriptedProvider{responseBody: "doing it\n[goal:evidence] verified against c1\nTASK_STATUS: success\nTASK_SUMMARY: done"}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judgeInst.Provider = metJudgeProviderC1()

	tk := &task.Task{
		Title: "sync session task", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.SessionID == "" {
		t.Fatal("task SessionID is empty immediately after ExecuteTask returned — M1/FR-029 requires it " +
			"to be assigned synchronously BEFORE dispatch, not asynchronously inside the run goroutine")
	}
	// The task has definitely left `next` by the time ExecuteTask returns
	// without error (ClaimForRun transitions next->in_progress synchronously
	// before the M1 session-creation code even runs) — it may have already
	// raced on to a terminal status given the instantly-responding scripted
	// worker+judge, so this only pins "left next", not a specific later
	// status.
	if got.Status == task.StatusNext {
		t.Fatalf("task status = %q immediately after ExecuteTask returned, want anything but next", got.Status)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want done (result: %s)", final.Status, final.Result)
	}
}

// TestExecuteTask_EndToEndStillCompletes_AfterM1Refactor is a regression
// check: moving session creation out of the run goroutine and into
// ExecuteTask itself (M1) must not change the actual dispatch/run/complete
// behavior for the ordinary happy path.
func TestExecuteTask_EndToEndStillCompletes_AfterM1Refactor(t *testing.T) {
	worker := &scriptedProvider{responseBody: "doing it\n[goal:evidence] verified against c1\nTASK_STATUS: success\nTASK_SUMMARY: done"}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judgeInst.Provider = metJudgeProviderC1()

	tk := &task.Task{
		Title: "end to end", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want done (result: %s)", final.Status, final.Result)
	}
	if final.SessionID == "" {
		t.Fatal("final task has no SessionID")
	}
}
