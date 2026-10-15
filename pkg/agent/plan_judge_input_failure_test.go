package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// T3 (issue #1306, "the Judge must conclude"): a plan judge round that cannot
// read its own inputs — the plan record or the member tasks — must end like
// any unavailable round: no round consumed, the plan back to dispatching with
// a visible handover, the round's claim released, and a fresh round on the
// next tick once the fault clears. Never a silent return that leaves the plan
// at "judging" with nothing watching it.
func TestPlanJudgeRound_InputReadFailureEndsVisiblyAndRetries(t *testing.T) {
	for _, stage := range []string{"plan", "members"} {
		t.Run(stage, func(t *testing.T) {
			h := newAllTerminalPlanWithBrokenJudge(t, "unused")
			h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
				return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
					Met:          true,
					PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: true, Reason: "done"}},
				}}
			}
			faulty := true
			h.pe.judgeRoundReadFault = func(s string) error {
				if faulty && s == stage {
					return errors.New("simulated " + stage + " read failure")
				}
				return nil
			}

			got := tick(t, h)
			if got.PlanPhase != plan.PhaseDispatching {
				t.Fatalf("after an input-read failure plan_phase = %q, want %q (not left at judging)", got.PlanPhase, plan.PhaseDispatching)
			}
			if !strings.Contains(got.HandoverText, "retry") {
				t.Fatalf("handover = %q, want a visible note that the round will be retried", got.HandoverText)
			}
			if got.JudgeRounds != 0 {
				t.Fatalf("judge_rounds = %d, want 0 — an unreadable input consumes no round", got.JudgeRounds)
			}
			if _, held := h.pe.registry().Lookup(verifierUnitForPlan("p1")); held {
				t.Fatal("the failed round left its judge claim held")
			}
			if h.judge.callCount() != 0 {
				t.Fatalf("the Judge was called %d times on unreadable inputs", h.judge.callCount())
			}

			faulty = false
			got = tick(t, h)
			if h.judge.callCount() != 1 {
				t.Fatalf("after the fault cleared the Judge was called %d times, want a fresh round (1)", h.judge.callCount())
			}
			if got.State != plan.StateDone {
				t.Fatalf("after a met verdict the plan is %q, want done", got.State)
			}
		})
	}
}
