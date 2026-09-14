// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_claim_not_trusted_adr086_test.go is the behavioural guard for
// GOAL-FR-022/FR-023 (ADR-086, joint delivery plan row R-27): a worker's
// completion claim is a REQUEST to be judged, never a completion.
//
// Why this file exists at all. R-27 named TWO trust-the-claim branches inside
// TaskExecutor.adjudicateClaim (since replaced by task_run_loop.go's
// adjudicateRunClaim). The second one completed a task outright —
// StatusDone, on the worker's own say-so, with no verdict of any kind —
// whenever the SOFT tier was in play (a task with no explicit Criteria,
// judged against judge.go::SoftTierCriterion) AND the Judge System Agent was
// absent from the registry. It was deleted, correctly: an unreachable Judge
// is a reason a claim CANNOT be adjudicated, not evidence that it is true.
//
// The deletion was then checked the only way that actually proves anything —
// the branch was put back, and the whole pkg/agent suite was re-run. NOTHING
// DIED. Not one test in the package asserted its absence; the sixteen task
// tests that were green before the deletion were green BECAUSE of it (they
// dispatch criteria-less tasks against a harness that registered no Judge, so
// every one of them took that branch and never reached a verdict), and after
// it they simply time out waiting for a completion that correctly never
// comes. A timeout is not a guard: it says "something did not finish", not
// "the claim was not trusted", and it would go green again the instant the
// defect returned.
//
// So this is the guard. It drives adjudicateRunClaim in exactly the condition
// the deleted branch keyed on and asserts the shape that replaced it (founder
// decision 2026-09-14, "Judge unavailable on a task"): a Judge only an
// operator can fix — here, none registered at all — ends the task Failed with
// a plain reason naming the fix, no attempt and no round consumed, no verdict
// recorded, and never a restart. Restore the branch and the first assertion dies.
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newJudgelessTaskLoop builds an AgentLoop with ONE native worker agent and
// deliberately NO Judge System Agent.
//
// This harness is built here rather than reused from
// task_completion_contract_test.go on purpose: that one now registers a Judge
// (it has to — every task in it reaches a completion claim), and a guard that
// depended on it would silently stop testing the judge-unregistered condition
// the day someone changed it. The absence of coreagent.IDJudge from this
// config IS the fixture.
func newJudgelessTaskLoop(t *testing.T) *AgentLoop {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{{
				ID:   "native-agent",
				Name: "Native Agent",
				Type: config.AgentTypeWorker,
				Home: t.TempDir(),
			}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	if _, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge)); ok {
		t.Fatalf("fixture broken: the Judge agent (%s) is registered, but this guard requires it ABSENT — "+
			"the condition the deleted trust-the-claim branch keyed on cannot be reached", coreagent.IDJudge)
	}
	return al
}

// TestTaskClaimIsNeverTrustedWhenJudgeUnregistered is the GOAL-FR-022
// regression guard described in this file's header.
//
// The scenario is assembled to be the deleted branch's best case, so that a
// pass cannot come from some unrelated thing blocking completion:
//
//   - the task carries NO Criteria but a real Title/Prompt, so
//     SoftTierCriterion returns a criterion and `usedSoftTier` is true — the
//     branch's first condition;
//   - no Judge is registered — its second condition;
//   - the task is genuinely in_progress via a real ClaimForRun, because
//     completeTaskWithResult CASes against exactly that status. A fixture that
//     left the task on `next` would make the restored branch's own write fail
//     and this guard would pass for the wrong reason;
//   - a real open TaskRun and a real ACTIVE task-owned goal record are seeded,
//     so "the run stays open" and "no round is consumed" are observable
//     properties of live records rather than assertions about nothing.
func TestTaskClaimIsNeverTrustedWhenJudgeUnregistered(t *testing.T) {
	al := newJudgelessTaskLoop(t)

	tk := &task.Task{
		Title:       "criteria-less task with a judgeable prompt",
		Prompt:      "make the export endpoint return CSV",
		Action:      task.ActionLLM,
		AgentID:     "native-agent",
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
		// Criteria deliberately EMPTY — this is the soft tier (GOAL-FR-023:
		// a pre-FR-021 task with no criteria still runs and is still judged).
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if soft := SoftTierCriterion(tk.Title, tk.Description, tk.Prompt); soft == nil {
		t.Fatal("fixture broken: SoftTierCriterion returned nil, so adjudicateRunClaim would take the " +
			"structurally-empty fail-closed branch instead of the judge-unregistered one under test")
	}

	claimed, err := al.taskStore.ClaimForRun(tk.ID, time.Now())
	if err != nil {
		t.Fatalf("ClaimForRun: %v", err)
	}
	if claimed.Status != task.StatusInProgress {
		t.Fatalf("fixture broken: claimed status = %q, want %q", claimed.Status, task.StatusInProgress)
	}

	seededRun, _, oerr := al.taskStore.OpenRun(tk.ID, nil, task.RunKindManual, "")
	if oerr != nil {
		t.Fatalf("OpenRun: %v", oerr)
	}
	run := &activeRun{runID: seededRun.RunID}

	// A REAL session, bound to the goal record and handed to adjudicateRunClaim
	// as the task's own session id — deliberately not "": completeTaskWithResult
	// archives that session as part of completing a task, so the restored
	// branch must have a genuine one to work on for this guard to be testing
	// the branch rather than an incidental failure inside it.
	_, taskSessionID := newGoalTestSession(t, al, "native-agent")
	seededGoal := seedActiveTaskGoalForClaimGuard(t, claimed.ID, taskSessionID)

	const claim = "Implemented the CSV export and checked the output by hand."
	step, _, redispatch := al.taskExecutor.adjudicateRunClaim(
		context.Background(), claimed, taskSessionID, claim, run, &taskRunState{})

	final, gerr := al.taskStore.Get(tk.ID)
	if gerr != nil {
		t.Fatalf("get task: %v", gerr)
	}
	// THE kill assertion: nothing may complete a task on the worker's say-so.
	if final.Status == task.StatusDone {
		t.Fatalf("status = %q — the task was COMPLETED with no Judge registered and no verdict of any kind "+
			"(GOAL-FR-022/R-27). result: %q", final.Status, final.Result)
	}
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q — a claim no Judge can check ends the task with the reason, rather "+
			"than leaving it in progress with nothing visible (result: %q)", final.Status, task.StatusFailed, final.Result)
	}
	if !strings.Contains(final.Result, "The Judge could not run") || !strings.Contains(final.Result, "not registered") {
		t.Errorf("result = %q, want the plain reason naming the missing Judge", final.Result)
	}
	if strings.Contains(final.Result, claim) {
		t.Errorf("result = %q — the worker's own claim must never be written as the result", final.Result)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a missing Judge is an environment gap, not a failed run", final.AttemptCount)
	}
	if step != runStepEnded || redispatch != "" {
		t.Errorf("step=%v redispatch=%q, want the run ended with no restart — restarting cannot fix it", step, redispatch)
	}

	runs, lerr := al.taskStore.ListRuns(tk.ID)
	if lerr != nil {
		t.Fatalf("ListRuns: %v", lerr)
	}
	if len(runs) != 1 || runs[0].RunID != seededRun.RunID {
		t.Fatalf("runs = %+v, want the one seeded run", runs)
	}
	if runs[0].Status != task.StatusFailed || runs[0].EndedAt == nil {
		t.Errorf("run status=%q ended_at=%v, want it closed failed with the task", runs[0].Status, runs[0].EndedAt)
	}

	afterGoal, rerr := goal.NewStore(config.OmnipusHomeDir()).Get(seededGoal.GoalID)
	if rerr != nil {
		t.Fatalf("re-read the task's goal record: %v", rerr)
	}
	if afterGoal.Round != 0 || afterGoal.AttemptsUsed != 0 {
		t.Errorf("goal round=%d attempts_used=%d, want 0/0 — nothing was judged", afterGoal.Round, afterGoal.AttemptsUsed)
	}
	if afterGoal.LatestVerdict != nil {
		t.Errorf("a verdict was recorded (%+v) — nothing adjudicated this claim", afterGoal.LatestVerdict)
	}
	if afterGoal.State == generated.GoalStateActive {
		t.Errorf("goal state = %q — the goal must end with its task", afterGoal.State)
	}
}

// seedActiveTaskGoalForClaimGuard creates and activates a task-owned goal
// record with its round counter at zero, so the guard's "no round consumed"
// assertion reads a real record rather than proving nothing. Criteria stay
// empty on the goal record AND on the task — adjudicateRunClaim judges the
// record's criteria, then the task's, and only then the soft tier — and the DoD
// is the built-in floor, which goal.New requires to be non-empty.
func seedActiveTaskGoalForClaimGuard(t *testing.T, taskID, sessionID string) *goal.Goal {
	t.Helper()
	now := time.Now().UTC()
	g, err := goal.New(generated.GoalOwnerKindTask, taskID, generated.GoalSourceTaskExplicit,
		"make the export endpoint return CSV", "", nil, newFloorDoD(), config.DefaultGoalMaxRounds, now)
	if err != nil {
		t.Fatalf("seedActiveTaskGoalForClaimGuard: goal.New: %v", err)
	}
	gs := goal.NewStore(config.OmnipusHomeDir())
	if cerr := gs.Create(g); cerr != nil {
		t.Fatalf("seedActiveTaskGoalForClaimGuard: Create: %v", cerr)
	}
	activated, uerr := gs.Update(g.GoalID, func(cur *goal.Goal) error {
		return cur.Activate(sessionID, now)
	})
	if uerr != nil {
		t.Fatalf("seedActiveTaskGoalForClaimGuard: Activate: %v", uerr)
	}
	return activated
}
