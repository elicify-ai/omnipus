// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_goal_loop_test.go covers the TaskExecutor's two-level loop
// (task_run_loop.go) end-to-end through real ExecuteTask dispatches: outer
// attempt boundaries, the hard ceiling on attempts, the Scratchpad exemption
// (FR-048), a judged completion, and a legacy criteria-less task's accounting.
// Workers finish turns the one way a native worker can: goal_claim.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// alwaysUnmetJudgeProvider scripts the Judge to return a well-formed but unmet
// verdict for criterion c1.
func alwaysUnmetJudgeProvider() *fakeJudgeProvider {
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": false, "criteria": [{"id":"c1","met":false,"reason":"still missing evidence"}]}`,
		}, nil
	}}
}

// t3WaitForTerminal waits for taskID to reach a terminal status AND for its
// session archive write to land (the last write completeTaskWithResult makes
// before its hooks), with a deadline that scales with the expected worker
// turns — every turn here is a real runTurn, only the LLM replies are canned.
func t3WaitForTerminal(t *testing.T, al *AgentLoop, taskID string, expectedDispatches int) *task.Task {
	t.Helper()
	if expectedDispatches < 1 {
		expectedDispatches = 1
	}
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
		"(%d expected dispatch(es)); this is a WAIT timeout, not an assertion failure", expectedDispatches)
	return nil
}

// TestTaskExecutor_AttemptBoundaries: with one goal try per run and a Judge
// that never upholds the claim, every run fails after its one try and the
// task restarts until its attempt limit — N runs, N worker turns, N Judge
// calls, AttemptCount N, then Failed.
func TestTaskExecutor_AttemptBoundaries(t *testing.T) {
	for _, maxAttempts := range []int{1, 2, 3} {
		t.Run("max_"+string(rune('0'+maxAttempts)), func(t *testing.T) {
			worker := newClaimingWorker(turnClaimMet("verified against the acceptance criterion"))
			al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = 1 })
			judge := &b6ScriptedJudge{metFromCall: alwaysUnmet, reason: "still missing evidence"}
			judgeInst.Provider = judge

			budget := maxAttempts
			tk := createTaskWithGoal(t, al, &task.Task{
				Title: "boundary task", Prompt: "do it", Action: task.ActionLLM,
				AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
				MaxAttempts: &budget,
				Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
			}, "no secrets appear in the output")

			if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
				t.Fatalf("ExecuteTask: %v", err)
			}
			final := t3WaitForTerminal(t, al, tk.ID, maxAttempts)
			if final.Status != task.StatusFailed {
				t.Fatalf("status = %q, want failed (result: %s)", final.Status, final.Result)
			}
			if final.AttemptCount != maxAttempts {
				t.Errorf("attempt_count = %d, want %d", final.AttemptCount, maxAttempts)
			}
			if got := worker.turnsStarted(); got != maxAttempts {
				t.Errorf("worker turns = %d, want %d (one try per run)", got, maxAttempts)
			}
			if got := judge.callCount(); got != maxAttempts {
				t.Errorf("Judge calls = %d, want %d", got, maxAttempts)
			}
		})
	}
}

// TestTaskExecutor_AttemptHardCeiling_StopsUnconditionally probes FR-047's
// independent hard ceiling on the OUTER counter: with AttemptCount already at
// twice the limit, the task stops after exactly ONE more run.
func TestTaskExecutor_AttemptHardCeiling_StopsUnconditionally(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("verified against the acceptance criterion"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = 1 })
	judgeInst.Provider = &b6ScriptedJudge{metFromCall: alwaysUnmet, reason: "unmet"}

	const maxAttempts = 3
	budget := maxAttempts
	tk := createTaskWithGoal(t, al, &task.Task{
		Title: "ceiling probe", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts:  &budget,
		AttemptCount: 2 * maxAttempts,
		Criteria:     []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}, "no secrets appear in the output")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	final := t3WaitForTerminal(t, al, tk.ID, 1)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want failed", final.Status)
	}
	if got := worker.turnsStarted(); got != 1 {
		t.Errorf("worker turns = %d, want exactly 1 — the loop must stop past the hard ceiling", got)
	}
}

// TestTaskExecutor_ScratchpadExemptFromGoalLoop proves FR-048: a set_todos
// checklist card never enters the goal loop. No completion signal fails it
// closed on the spot — no retry, no attempt, no goal record.
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
		t.Fatalf("status = %q, want failed (immediate fail-closed, no retry)", final.Status)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a Scratchpad task never enters the goal loop", final.AttemptCount)
	}
	worker.mu.Lock()
	dispatches := worker.callCount
	worker.mu.Unlock()
	if dispatches != 1 {
		t.Errorf("worker dispatched %d times, want exactly 1", dispatches)
	}
}

// TestTaskExecutor_JudgeMetVerdict_CompletesTaskDone: a worker claims with
// goal_claim, the Judge upholds it, and the task lands done on its first run.
func TestTaskExecutor_JudgeMetVerdict_CompletesTaskDone(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("verified against the acceptance criterion"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "evidenced"}
	judgeInst.Provider = judge

	tk := createTaskWithGoal(t, al, &task.Task{
		Title: "judged success", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}, "no secrets appear in the output")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	final := t3WaitForTerminal(t, al, tk.ID, 1)
	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want done (result: %s)", final.Status, final.Result)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a met verdict on the first run uses no attempt", final.AttemptCount)
	}
	if judge.callCount() != 1 {
		t.Errorf("Judge calls = %d, want 1", judge.callCount())
	}
}

// TestLegacyCriterialessTask_AttemptAccountingUnchanged_GOALFR023: a task
// created before criteria were mandatory (no criteria, no goal record) still
// runs, gets a goal record at its first run, is judged on the soft-tier
// criterion plus the floor Definition of Done, and walks the same attempt
// ladder as a task with explicit criteria.
func TestLegacyCriterialessTask_AttemptAccountingUnchanged_GOALFR023(t *testing.T) {
	cases := []struct {
		name             string
		criteria         []task.AcceptanceCriterion
		judgeMet         bool
		wantStatus       task.Status
		wantAttemptCount int
		wantTurns        int
	}{
		{"criteria_less_unmet_walks_the_attempt_budget", nil, false, task.StatusFailed, 2, 2},
		{"explicit_criteria_unmet_walks_the_same_budget",
			[]task.AcceptanceCriterion{proseCriterion("c1", "the widget is green")}, false, task.StatusFailed, 2, 2},
		{"criteria_less_met_completes_on_the_first_run", nil, true, task.StatusDone, 0, 1},
		{"explicit_criteria_met_completes_the_same_way",
			[]task.AcceptanceCriterion{proseCriterion("c1", "the widget is green")}, true, task.StatusDone, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			worker := newClaimingWorker(turnClaimMet("the widget is now green"))
			al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = 1 })
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

			final := t3WaitForTerminal(t, al, tk.ID, tc.wantTurns)
			if final.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q (result: %s)", final.Status, tc.wantStatus, final.Result)
			}
			if final.AttemptCount != tc.wantAttemptCount {
				t.Errorf("attempt_count = %d, want %d", final.AttemptCount, tc.wantAttemptCount)
			}
			if got := worker.turnsStarted(); got != tc.wantTurns {
				t.Errorf("worker turns = %d, want %d", got, tc.wantTurns)
			}
			if got := judge.callCount(); got != tc.wantTurns {
				t.Errorf("Judge calls = %d, want %d — every claim is judged, never trusted or skipped", got, tc.wantTurns)
			}
		})
	}
}
