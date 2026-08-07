package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestAttemptsVsRounds_DistinctBrakes pins FR-178: the per-member/task
// attempts brake (Task.AttemptCount, sole writer
// TaskExecutor.consumeAttemptOrExhaust) and the per-goal/plan judge-rounds
// brake (plan.JudgeRounds, sole writer PlanEngine.applyJudgeRoundOutcomeLocked)
// are TWO DISTINCT counters — never conflated, and whichever trips first stops
// its OWN scope locally. The matrix row 58 claimed this test; it did not exist.
//
// Direction A (rounds): exhausting JudgeRounds fails the PLAN on
// judge_rounds_exhausted WITHOUT consuming a member-task attempt.
// Direction B (attempts): exhausting AttemptCount fails the TASK WITHOUT
// tripping the plan's rounds brake (the plan stays running, JudgeRounds intact).
func TestAttemptsVsRounds_DistinctBrakes(t *testing.T) {
	t.Run("rounds_brake_does_not_consume_an_attempt", func(t *testing.T) {
		// Direction A: drive a plan's judge rounds to exhaustion (the same path
		// TestPlanEngine_JudgeRoundsExhausted_FailsPlan exercises) and assert a
		// member task's AttemptCount is UNCHANGED — the rounds brake never
		// touches the attempts counter.
		h := newTestPlanEngine(t)
		one := 1
		mustCreatePlan(t, h.plans, &plan.Plan{
			ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
			DoD:    []task.AcceptanceCriterion{planProseCriterion("The thing is done")},
			Bounds: &plan.PlanBounds{PlanJudgeMaxRounds: &one},
		})
		// Member task seeded with a NON-zero AttemptCount (3 prior attempts) so
		// any conflation (rounds-exhaustion consuming an attempt) would show as
		// a change. Status `done` so allMembersTerminal==true and the judge runs.
		const priorAttempts = 3
		mustCreateTask(t, h.tasks, &task.Task{
			Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
			AttemptCount: priorAttempts,
		})
		h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
			return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
				Met:          false,
				PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: false, Reason: "still not yet"}},
			}}
		}

		// Round 1: consumes the only allowed round, ends unmet.
		h.pe.processPlan(context.Background(), "p1")
		h.pe.judgeWG.Wait()

		// Round 2: JudgeRounds(1) >= maxRounds(1) -> plan fails on rounds,
		// WITHOUT a second judge call.
		h.pe.processPlan(context.Background(), "p1")

		failedPlan, err := h.plans.Get("p1")
		if err != nil {
			t.Fatal(err)
		}
		if failedPlan.State != plan.StateFailed {
			t.Fatalf("plan state = %q, want failed (rounds brake tripped)", failedPlan.State)
		}
		if failedPlan.FailedReason != plan.FailedReasonJudgeRoundsExhausted {
			t.Fatalf("failed_reason = %q, want judge_rounds_exhausted", failedPlan.FailedReason)
		}
		if failedPlan.JudgeRounds != 1 {
			t.Errorf("plan JudgeRounds = %d, want 1 (the rounds brake consumed a round, not an attempt)", failedPlan.JudgeRounds)
		}

		// THE FR-178 ASSERTMENT: the member task's AttemptCount is UNCHANGED —
		// exhausting rounds did NOT consume an attempt (never conflated).
		member, err := h.tasks.List(task.Filter{PlanID: "p1"})
		if err != nil {
			t.Fatalf("list plan members: %v", err)
		}
		if len(member) != 1 {
			t.Fatalf("expected 1 member, got %d", len(member))
		}
		if member[0].AttemptCount != priorAttempts {
			t.Errorf("member AttemptCount = %d, want %d — the rounds brake MUST NOT consume an attempt (FR-178)",
				member[0].AttemptCount, priorAttempts)
		}
		if member[0].Status != task.StatusDone {
			t.Errorf("member status = %q, want done (rounds brake fails the PLAN, not the member task)", member[0].Status)
		}
	})

	t.Run("attempts_brake_does_not_trip_the_rounds_brake", func(t *testing.T) {
		// Direction B: drive a member task's attempts to exhaustion via the SOLE
		// AttemptCount writer (consumeAttemptOrExhaust) and assert the OWNING
		// PLAN is untouched — JudgeRounds intact, still running, NOT failed on
		// rounds. The two brakes write to different stores/fields; the
		// attempts brake fails the TASK, it does not trip the plan rounds brake.
		al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

		// A plan record in its OWN store, seeded at JudgeRounds=0/running. The
		// TaskExecutor never receives a reference to this store — the very
		// architecture that keeps the two brakes distinct — so asserting it is
		// untouched proves non-conflation.
		planDir := t.TempDir()
		planStore := plan.New(filepath.Join(planDir, "plans"))
		if err := planStore.Create(&plan.Plan{
			ID: "plan-attempts", Title: "Attempts plan", WorkspaceID: "default",
			OwnerAgentID: "native-agent", State: plan.StateRunning,
		}); err != nil {
			t.Fatalf("create plan: %v", err)
		}

		maxAttempts := 2
		tk := &task.Task{
			Title: "attempts-brake member", Prompt: "do it", Action: task.ActionLLM,
			AgentID: "native-agent", Priority: 3, WorkspaceID: "default",
			Status:       task.StatusNext,
			PlanID:       "plan-attempts",
			MaxAttempts:  &maxAttempts,
			AttemptCount: maxAttempts - 1, // one consumeAttemptOrExhaust call exhausts it
		}
		if err := al.taskStore.Create(tk); err != nil {
			t.Fatalf("create task: %v", err)
		}
		// consumeAttemptOrExhaust CASes against in_progress — establish it.
		inProg := task.StatusInProgress
		if _, err := al.taskStore.Update(tk.ID, task.Patch{Status: &inProg}); err != nil {
			t.Fatalf("set in_progress: %v", err)
		}
		fresh, err := al.taskStore.Get(tk.ID)
		if err != nil {
			t.Fatalf("reload task: %v", err)
		}

		// Sole AttemptCount writer, verdict=nil (no-signal unmet outcome).
		al.taskExecutor.consumeAttemptOrExhaust(context.Background(), fresh, "", "claim summary", nil, nil)

		// The TASK failed on attempts (the attempts brake tripped for ITS scope).
		final, err := al.taskStore.Get(tk.ID)
		if err != nil {
			t.Fatalf("get final task: %v", err)
		}
		if final.Status != task.StatusFailed {
			t.Fatalf("task status = %q, want failed (attempts brake exhausted the task)", final.Status)
		}
		if final.AttemptCount != maxAttempts {
			t.Errorf("task AttemptCount = %d, want %d", final.AttemptCount, maxAttempts)
		}

		// THE FR-178 ASSERTMENT: the owning PLAN is untouched — JudgeRounds
		// intact, still running, NOT failed on rounds. The attempts brake did
		// NOT trip the rounds brake (never conflated).
		untouched, err := planStore.Get("plan-attempts")
		if err != nil {
			t.Fatalf("reload plan: %v", err)
		}
		if untouched.JudgeRounds != 0 {
			t.Errorf("plan JudgeRounds = %d, want 0 — exhausting attempts MUST NOT trip the rounds brake (FR-178)",
				untouched.JudgeRounds)
		}
		if untouched.State != plan.StateRunning {
			t.Errorf("plan state = %q, want running — the attempts brake fails the TASK, not the plan on rounds",
				untouched.State)
		}
		if untouched.FailedReason == plan.FailedReasonJudgeRoundsExhausted {
			t.Error("plan must NOT be failed on judge_rounds_exhausted when only attempts exhausted")
		}
	})
}
