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
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
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

// t3WaitForTerminal is waitForCompletionContractTerminal
// (task_completion_contract_test.go) with ONE difference: a deadline that
// scales with the number of worker dispatches the case expects, instead of
// a flat five seconds.
//
// Why it exists (wave T3, 2026-09-11). The shared helper waits 5s total.
// Every attempt in this file costs a full worker turn PLUS a full Judge
// turn, and those turns are real runTurn executions — only the LLM call
// itself is canned. On an unloaded machine each pair costs ~0.5s and even
// the four-attempt case fits; under the parallel-agent load this delivery
// runs at, a single canned-provider turn was measured at 2.7s, so the
// two-attempt case alone overran the flat deadline. The result is a red
// that reports nothing about the code: `TestTaskExecutor_AttemptBoundaries`
// max_2/max_3/max_4 and this file's own GOAL-FR-023 case all failed with
// "task did not reach a terminal status within the deadline" at load
// average 6.6 and all passed, unchanged, at load average 1.
//
// The assertions are untouched — this only stops the clock from deciding
// them. The wait CONDITION is copied verbatim from the shared helper
// (terminal status AND the session archive write landed), because returning
// on terminal status alone races finishTaskRun's trailing writes; see that
// helper's own doc comment.
//
// The shared helper itself is NOT edited: task_completion_contract_test.go
// is a protected regression file this delivery's plan bars every wave from
// touching. Only this file's call sites move.
func t3WaitForTerminal(t *testing.T, al *AgentLoop, taskID string, expectedDispatches int) *task.Task {
	t.Helper()
	if expectedDispatches < 1 {
		expectedDispatches = 1
	}
	// 10s of fixed headroom for harness/session setup, plus 15s per
	// worker+Judge turn pair — roughly five times the worst per-pair cost
	// measured under load, so the deadline stops being a variable.
	deadline := time.Now().Add(10*time.Second + time.Duration(expectedDispatches)*15*time.Second)
	for time.Now().Before(deadline) {
		got, err := al.taskStore.Get(taskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task.IsTerminal(got.Status) {
			if got.SessionID == "" {
				return got
			}
			sessStore := al.GetAgentStore(got.AgentID)
			if sessStore == nil {
				return got
			}
			if meta, merr := sessStore.GetMeta(got.SessionID); merr == nil && meta.Status == session.StatusArchived {
				return got
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task did not reach a terminal status within the deadline "+
		"(%d expected dispatch(es)); this is a WAIT timeout, not an assertion failure — "+
		"re-read it as inconclusive under machine load before treating it as a finding", expectedDispatches)
	return nil
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

			final := t3WaitForTerminal(t, al, tk.ID, tc.wantDispatches)
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

	final := t3WaitForTerminal(t, al, tk.ID, 1)
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

	final := t3WaitForTerminal(t, al, tk.ID, 1)
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

	final := t3WaitForTerminal(t, al, tk.ID, 1)
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

	final := t3WaitForTerminal(t, al, tk.ID, 1)
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

// ===========================================================================
// ADR-086 / GOAL-FR-023 — wave T3: attempt accounting for a legacy
// criteria-less task, end to end through real ExecuteTask dispatches.
//
// FR-023: "A task created before FR-021 with no criteria MUST continue to run
// and MUST continue to be judged by pkg/agent/judge.go::SoftTierCriterion.
// FR-021 binds at creation and at edit only."
//
// TestLegacyCriterialessTaskStillRuns_GOALFR023
// (task_executor_adjudicate_claim_test.go) proves WHICH criteria such a task
// is judged against, at the adjudicateClaim seam. This one proves the other
// half of "MUST continue to run": that the attempt ladder around it behaves
// exactly as it does for a task with explicit criteria — same dispatch count,
// same AttemptCount, same terminal state — so FR-021's new creation-time
// requirement cannot quietly strand the tasks that predate it, in either
// direction (neither looping forever nor failing out early).
//
// The explicit-criteria rows are the differentiation control: identical
// fixtures except for the criteria list, identical expectations. If the
// criteria-less path ever diverges — an extra free re-dispatch, a skipped
// attempt, an early exhaustion — the two halves of each table row disagree
// and the test goes red.
// ===========================================================================

func TestLegacyCriterialessTask_AttemptAccountingUnchanged_GOALFR023(t *testing.T) {
	cases := []struct {
		name     string
		criteria []task.AcceptanceCriterion
		judgeMet bool

		wantStatus       task.Status
		wantAttemptCount int
		wantDispatches   int
		wantJudgeCalls   int
	}{
		{
			name:     "criteria_less_unmet_walks_the_full_attempt_budget",
			criteria: nil,
			judgeMet: false,
			// Budget 2 below: two dispatches, two judged attempts, then the
			// attempts brake ends it. Exactly what an explicit-criteria task
			// does.
			wantStatus:       task.StatusFailed,
			wantAttemptCount: 2,
			wantDispatches:   2,
			wantJudgeCalls:   2,
		},
		{
			name:             "explicit_criteria_unmet_walks_the_same_budget",
			criteria:         []task.AcceptanceCriterion{proseCriterion("c1", "the widget is green")},
			judgeMet:         false,
			wantStatus:       task.StatusFailed,
			wantAttemptCount: 2,
			wantDispatches:   2,
			wantJudgeCalls:   2,
		},
		{
			name:     "criteria_less_met_completes_on_the_first_attempt",
			criteria: nil,
			judgeMet: true,
			// A met verdict on the first attempt consumes nothing — the
			// attempts counter is only written by the unmet path.
			wantStatus:       task.StatusDone,
			wantAttemptCount: 0,
			wantDispatches:   1,
			wantJudgeCalls:   1,
		},
		{
			name:             "explicit_criteria_met_completes_the_same_way",
			criteria:         []task.AcceptanceCriterion{proseCriterion("c1", "the widget is green")},
			judgeMet:         true,
			wantStatus:       task.StatusDone,
			wantAttemptCount: 0,
			wantDispatches:   1,
			wantJudgeCalls:   1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			worker := &scriptedProvider{
				responseBody: "did the work\n[goal:evidence] the widget is now green\n" +
					"TASK_STATUS: success\nTASK_SUMMARY: I painted it green.",
			}
			al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
			// The id-echoing Judge fixture (task_executor_adjudicate_claim_test.go)
			// answers for WHATEVER criterion ids the prompt carries, so the
			// criteria-less row is judged on its soft-tier id and the
			// explicit row on "c1" without either needing its own fixture —
			// which is what makes the two rows genuinely comparable.
			judge := &goalFR022JudgeProvider{met: tc.judgeMet}
			judgeInst.Provider = judge

			budget := 2
			tk := &task.Task{
				Title: "paint the widget", Prompt: "the widget must end up green",
				Action: task.ActionLLM, AgentID: "native-agent", Priority: 3,
				WorkspaceID: "default", Status: task.StatusNext,
				MaxAttempts: &budget, Criteria: tc.criteria,
			}
			if err := al.taskStore.Create(tk); err != nil {
				t.Fatalf("create task: %v", err)
			}

			if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
				t.Fatalf("ExecuteTask: %v", err)
			}

			final := t3WaitForTerminal(t, al, tk.ID, tc.wantDispatches)
			if final.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q (result: %s)", final.Status, tc.wantStatus, final.Result)
			}
			if final.AttemptCount != tc.wantAttemptCount {
				t.Errorf("attempt_count = %d, want %d", final.AttemptCount, tc.wantAttemptCount)
			}
			worker.mu.Lock()
			gotDispatches := worker.callCount
			worker.mu.Unlock()
			if gotDispatches != tc.wantDispatches {
				t.Errorf("worker dispatched %d time(s), want %d", gotDispatches, tc.wantDispatches)
			}
			if got := judge.callCount(); got != tc.wantJudgeCalls {
				t.Errorf("Judge dispatched %d time(s), want %d — a criteria-less legacy task must be "+
					"JUDGED on every attempt, never trusted and never skipped (GOAL-FR-022/FR-023)",
					got, tc.wantJudgeCalls)
			}
		})
	}
}
