// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_goal_loop_test.go covers the TaskExecutor attempt loop
// (finishTaskRun/consumeAttemptOrExhaust/adjudicateClaim, task_executor.go)
// end-to-end through real ExecuteTask dispatches: attempt-count boundaries,
// the hard ceiling, and the Scratchpad exemption (FR-048).
//
// scriptedProvider fixtures below carry a "[goal:evidence] ..." line
// immediately before every "TASK_STATUS: success" marker (ADR-052 FR-035,
// wired in task_executor.go's finishTaskRun ahead of
// parseTaskCompletionSignal) — without it, the evidence-marker gate would
// intercept the claim BEFORE it ever reaches the judge/attempt-consuming
// path these tests are pinning, inflating dispatch counts and desyncing
// them from AttemptCount.

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
				responseBody: "did the work\n[goal:evidence] verified against the acceptance criterion\nTASK_STATUS: success\nTASK_SUMMARY: I finished it.",
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

			if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
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
		responseBody: "did the work\n[goal:evidence] verified against the acceptance criterion\nTASK_STATUS: success\nTASK_SUMMARY: I finished it.",
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

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
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

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
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
		responseBody: "did the work\n[goal:evidence] verified against the acceptance criterion\nTASK_STATUS: success\nTASK_SUMMARY: I finished it.",
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

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
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

// TestGoalLoop_ExplicitUpdateTaskDone_StillJudged is review r1 blocker C1
// (SD-B2/FR-041): a worker calling the REAL update_task tool with
// status:"done" on a task that HAS acceptance criteria must NOT bypass the
// evidence-ladder judge. Before this fix, TaskUpdateTool wrote a terminal
// `done` and fired AdvanceBlockedDependents/onComplete synchronously at the
// tool-call boundary — finishTaskRun's task.IsTerminal check then just
// trusted it, and the Judge LLM was never even called. With the fix,
// TaskUpdateTool stages Task.PendingJudgeClaim instead, and finishTaskRun
// routes it through the SAME adjudicateClaim path a TASK_STATUS marker uses.
//
// With an always-unmet judge and max_attempts=1 (deterministic — the task
// goes straight to attempt-exhaustion after the one judged attempt, so the
// scripted worker provider never needs a second response queued), the task
// must land Failed (never Done), AttemptCount must be 1 (consumed, not
// skipped), the Judge LLM must have been called exactly once (proving it was
// NOT bypassed), and a task blocked on this one must stay Blocked (proving
// AdvanceBlockedDependents did not fire synchronously at the tool-call
// boundary either).
func TestGoalLoop_ExplicitUpdateTaskDone_StillJudged(t *testing.T) {
	worker := newScriptedProvider() // responses patched in below once tk.ID is known
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judgeInst.Provider = alwaysUnmetJudgeProvider()

	workerInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not found in registry")
	}
	// No-default-policy model (CLAUDE.md hard constraint 6): update_task needs
	// an explicit agent-level grant or it fails closed to "deny" before the
	// tool call under test ever executes.
	workerInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"update_task": config.ToolPolicyAllow},
	})

	maxAttempts := 1
	tk := &task.Task{
		Title: "explicit done claim, hard tier", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &maxAttempts,
		Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	dependent := &task.Task{
		Title: "dependent on the explicit-done task", Prompt: "do the dependent work", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default",
		Status: task.StatusBlocked, BlockedBy: []string{tk.ID},
	}
	if err := al.taskStore.Create(dependent); err != nil {
		t.Fatalf("create dependent task: %v", err)
	}

	worker.responses = []*providers.LLMResponse{
		{
			ToolCalls: []providers.ToolCall{{
				ID:   "call-update-task-done",
				Type: "function",
				Name: "update_task",
				Arguments: map[string]any{
					"task_id": tk.ID,
					"status":  "done",
					"result":  "Finished everything, trust me.",
				},
			}},
		},
		{
			// Marker-less final response — the explicit tool call above is the
			// only signal; must NOT itself resolve to a bypass either.
			Content: "Wrapping up now, nothing further to report.",
		},
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status == task.StatusDone {
		t.Fatalf("status = %q — an explicit update_task(done) claim on a task WITH acceptance criteria "+
			"must be judged, not trusted outright (this is the C1 bypass review r1 closes)", final.Status)
	}
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q (attempt exhaustion after one unmet-judged attempt)",
			final.Status, task.StatusFailed)
	}
	if final.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1 — the judged claim must consume an attempt, not be skipped",
			final.AttemptCount)
	}
	if final.PendingJudgeClaim != "" {
		t.Errorf("pending_judge_claim = %q, want cleared once adjudicated", final.PendingJudgeClaim)
	}

	judgeFake, ok := judgeInst.Provider.(*fakeJudgeProvider)
	if !ok {
		t.Fatal("judge provider is not a *fakeJudgeProvider (test setup bug)")
	}
	if judgeFake.callCount() != 1 {
		t.Errorf("judge LLM called %d times, want exactly 1 — the explicit update_task(done) call must "+
			"NOT bypass the judge", judgeFake.callCount())
	}

	finalDependent, err := al.taskStore.Get(dependent.ID)
	if err != nil {
		t.Fatalf("get dependent task: %v", err)
	}
	if finalDependent.Status != task.StatusBlocked {
		t.Errorf("dependent status = %q, want %q — AdvanceBlockedDependents must not fire "+
			"synchronously at the update_task(done) tool-call boundary for a judged (unmet) claim",
			finalDependent.Status, task.StatusBlocked)
	}
}
