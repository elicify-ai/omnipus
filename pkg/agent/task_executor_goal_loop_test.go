// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_goal_loop_test.go covers the TaskExecutor attempt loop
// (finishTaskRun/consumeAttemptOrExhaust/adjudicateClaim, task_executor.go)
// end-to-end through real ExecuteTask dispatches: attempt-count boundaries,
// the hard ceiling, and the Scratchpad exemption (FR-048).

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// alwaysUnmetJudgeProvider scripts the Judge System Agent to always return a
// well-formed but unmet verdict, so a worker's repeated "TASK_STATUS:
// success" claim never terminates the goal loop early — letting these tests
// pin the exact dispatch/attempt count against EffectiveTaskMaxAttempts.
func alwaysUnmetJudgeProvider() *fakeJudgeProvider {
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": false, "criteria": [{"id":"c1","met":false,"reason":"still missing evidence"}]}`,
		}, nil
	}}
}

func TestTaskExecutor_AttemptBoundaries(t *testing.T) {
	cases := []struct {
		name           string
		maxAttempts    int
		wantDispatches int
	}{
		{"max_1", 1, 1},
		{"max_2", 2, 2},
		{"max_3_default", 3, 3},
		{"max_4", 4, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			worker := &scriptedProvider{
				responseBody: "did the work\nTASK_STATUS: success\nTASK_SUMMARY: I finished it.",
			}
			al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
			judgeInst.Provider = alwaysUnmetJudgeProvider()

			maxAttempts := tc.maxAttempts
			maxPtr := &maxAttempts
			tk := &task.Task{
				Title: "boundary task", Prompt: "do it", Action: task.ActionLLM,
				AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
				MaxAttempts: maxPtr,
				Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
			}
			if err := al.taskStore.Create(tk); err != nil {
				t.Fatalf("create task: %v", err)
			}

			if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
				t.Fatalf("ExecuteTask: %v", err)
			}

			final := waitForCompletionContractTerminal(t, al, tk.ID)
			if final.Status != task.StatusFailed {
				t.Fatalf("status = %q, want %q — an always-unmet judge must never yield done "+
					"(result: %s)", final.Status, task.StatusFailed, final.Result)
			}
			if final.AttemptCount != tc.wantDispatches {
				t.Errorf("attempt_count = %d, want %d", final.AttemptCount, tc.wantDispatches)
			}
			worker.mu.Lock()
			gotDispatches := worker.callCount
			worker.mu.Unlock()
			if gotDispatches != tc.wantDispatches {
				t.Errorf("worker dispatched %d times, want %d", gotDispatches, tc.wantDispatches)
			}
		})
	}
}

// TestTaskExecutor_AttemptHardCeiling_StopsUnconditionally probes FR-047's
// independent hard ceiling: with AttemptCount pre-inflated to already be at
// the ceiling (2x max_attempts) before the run even starts (simulating a
// pending/duplicate re-dispatch), the loop must stop after exactly ONE more
// dispatch rather than running away.
func TestTaskExecutor_AttemptHardCeiling_StopsUnconditionally(t *testing.T) {
	worker := &scriptedProvider{
		responseBody: "did the work\nTASK_STATUS: success\nTASK_SUMMARY: I finished it.",
	}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judgeInst.Provider = alwaysUnmetJudgeProvider()

	const maxAttempts = 3
	maxPtr := new(int)
	*maxPtr = maxAttempts
	tk := &task.Task{
		Title: "ceiling probe", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts:  maxPtr,
		AttemptCount: 2 * maxAttempts, // already at the hard ceiling
		Criteria:     []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q", final.Status, task.StatusFailed)
	}
	worker.mu.Lock()
	dispatches := worker.callCount
	worker.mu.Unlock()
	if dispatches != 1 {
		t.Errorf("worker dispatched %d times, want exactly 1 — the loop must stop unconditionally "+
			"rather than looping past the hard ceiling", dispatches)
	}
}

// TestTaskExecutor_ScratchpadExemptFromGoalLoop proves FR-048/D5: a
// Scratchpad (set_todos) task is exempt from the goal loop ENTIRELY. A
// worker output with no TASK_STATUS marker must fail closed immediately (no
// retry, no attempt consumed) — the pre-ADR-049 behavior — never entering
// the new unmet/re-dispatch path a non-Scratchpad task would.
func TestTaskExecutor_ScratchpadExemptFromGoalLoop(t *testing.T) {
	worker := &scriptedProvider{responseBody: "no completion marker here at all"}
	al, _ := newGoalLoopTestLoop(t, worker, nil)

	tk := &task.Task{
		Title: "scratchpad task", Prompt: "todo tracking", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		Scratchpad: true,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q (immediate fail-closed, no retry)", final.Status, task.StatusFailed)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a Scratchpad task must never enter the goal loop", final.AttemptCount)
	}
	worker.mu.Lock()
	dispatches := worker.callCount
	worker.mu.Unlock()
	if dispatches != 1 {
		t.Errorf("worker dispatched %d times, want exactly 1 (no retry for a Scratchpad task)", dispatches)
	}
}

// TestTaskExecutor_JudgeMetVerdict_CompletesTaskDone is the happy-path
// end-to-end proof: a worker claims success, the judge confirms met=true,
// and the task lands Done via completeTaskWithResult (never via the marker
// alone) — with the DAG auto-advance still firing exactly once (proven by
// the dependent task advancing to in_progress).
func TestTaskExecutor_JudgeMetVerdict_CompletesTaskDone(t *testing.T) {
	worker := &scriptedProvider{
		responseBody: "did the work\nTASK_STATUS: success\nTASK_SUMMARY: I finished it.",
	}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"evidenced"}]}`,
		}, nil
	}}

	tk := &task.Task{
		Title: "judged success", Prompt: "do it", Action: task.ActionLLM,
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
		t.Fatalf("status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a met verdict on the first attempt must never increment it",
			final.AttemptCount)
	}
}
