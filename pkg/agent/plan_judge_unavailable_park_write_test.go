// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// plan_judge_unavailable_park_write_test.go — regression coverage for the H1
// review finding against the UAT-defect-B fix (plan_judge_unavailable_bound_test.go).
//
// THE HOLE. plan.MaxConsecutiveJudgeUnavailable bounds the streak of abandoned
// judge rounds, and past it surfaceJudgeUnavailableStall parks the plan at
// PhaseStalled. But the park is a STORE WRITE, and the write can fail. When it
// did, the engine logged one ERROR and returned with the plan still phased
// `judging` — and processPlan's crash-resume arm then started another judge
// round on the very next tick. The hold-back gate could not stop it: it asked
// for `phase == stalled && parked`, and the phase clause was exactly what the
// failed write never set. The streak climbed to 4, 5, 6..., each iteration
// re-entering the same failing write; JudgeRounds never incremented and the
// supervision ladder was never armed, so neither the round ceiling nor the
// correction budget could end it either. The bound existed and enforced
// nothing.
//
// THE FIX. processPlan's `judging` arm now intercepts a plan whose streak is
// at the bound BEFORE the crash-resume, and re-attempts the PARK rather than
// the ROUND (reparkJudgeUnavailablePlanLocked). That retry is itself bounded
// by plan.MaxJudgeUnavailableParkAttempts; past it the plan is failed closed
// at supervision_unavailable rather than parked again.
//
// HOW THESE TESTS INJECT THE FAILURE. PlanEngine.planStore is a concrete
// *plan.Store, so a test cannot substitute one that fails. What the tests do
// instead is reproduce the STATE a failed park leaves behind — the plan back
// at plan_phase=judging with no stall note, while the in-memory streak sits at
// the bound — by writing that phase back through the store. The engine cannot
// tell the two apart, and deliberately so: the retry counter measures the park
// NOT TAKING EFFECT rather than Update returning an error, which also covers a
// park that is written and then reverted by something else.

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
)

// tickUntilJudgeUnavailablePark runs the engine until its judge-unavailability
// streak reaches the bound and the plan parks at PhaseStalled, and returns the
// number of judge calls spent getting there.
func tickUntilJudgeUnavailablePark(t *testing.T, h *planEngineHarness) int {
	t.Helper()
	var got *plan.Plan
	for i := 0; i < plan.MaxConsecutiveJudgeUnavailable; i++ {
		got = tick(t, h)
	}
	if got.PlanPhase != plan.PhaseStalled {
		t.Fatalf("precondition: plan_phase = %q after %d unavailable rounds, want %q",
			got.PlanPhase, plan.MaxConsecutiveJudgeUnavailable, plan.PhaseStalled)
	}
	return h.judge.callCount()
}

// undoPark reproduces a park that did not take effect: the plan sits back at
// plan_phase=judging with no stall note, exactly as it does when the park's
// store write fails.
func undoPark(t *testing.T, h *planEngineHarness) {
	t.Helper()
	judging := plan.PhaseJudging
	cleared := ""
	if _, err := h.plans.Update("p1", plan.Patch{PlanPhase: &judging, HandoverText: &cleared}); err != nil {
		t.Fatalf("undoPark: %v", err)
	}
}

// judgeUnavailableStreakOf reads the engine's in-memory streak for p1 under the
// same mutex the engine uses.
func judgeUnavailableStreakOf(h *planEngineHarness) int {
	h.pe.mu.Lock()
	defer h.pe.mu.Unlock()
	return h.pe.judgeUnavailableStreak["p1"]
}

// TestPlanEngine_JudgeUnavailable_ParkThatDidNotPersistIsNotRejudged is the H1
// regression test.
//
// Against the pre-fix engine the first tick after the park is undone resumes
// the judge round (the `judging` arm's crash-resume path), so the judge call
// count climbs and the streak climbs with it — unbounded, one iteration per
// tick. Against the fixed engine no round is started at all: the park is
// re-attempted, and this time it sticks.
func TestPlanEngine_JudgeUnavailable_ParkThatDidNotPersistIsNotRejudged(t *testing.T) {
	h := newAllTerminalPlanWithBrokenJudge(t, "turn timed out: context deadline exceeded")
	callsAtPark := tickUntilJudgeUnavailablePark(t, h)

	streakAtPark := judgeUnavailableStreakOf(h)
	if streakAtPark != plan.MaxConsecutiveJudgeUnavailable {
		t.Fatalf("precondition: streak = %d, want %d", streakAtPark, plan.MaxConsecutiveJudgeUnavailable)
	}

	// Three ticks, each starting from the state a failed park write leaves.
	// Every one of them must refuse to judge.
	for i := 1; i <= 3; i++ {
		undoPark(t, h)

		got := tick(t, h)

		if calls := h.judge.callCount(); calls != callsAtPark {
			t.Fatalf("tick %d: judge called %d times, was %d at the park — a plan whose "+
				"unavailability streak is at the bound must never start another judge round, "+
				"whether or not the park that was meant to stop it managed to persist", i, calls, callsAtPark)
		}
		if streak := judgeUnavailableStreakOf(h); streak != streakAtPark {
			t.Fatalf("tick %d: streak = %d, was %d — the streak climbing past the bound is the "+
				"unbounded loop itself; the bound is only real if nothing can re-enter it", i, streak, streakAtPark)
		}
		if got.State != plan.StateRunning {
			t.Fatalf("tick %d: state = %q, want running — a park that can be re-attempted must be "+
				"re-attempted, not escalated to a terminal state on the first failure", i, got.State)
		}
		if got.PlanPhase != plan.PhaseStalled {
			t.Fatalf("tick %d: plan_phase = %q, want %q — the engine must re-attempt the park it "+
				"could not persist, so the plan ends the tick visibly stuck rather than silently "+
				"claiming to be judging", i, got.PlanPhase, plan.PhaseStalled)
		}
		if got.HandoverText == "" {
			t.Fatalf("tick %d: the re-attempted park must restore the stall note that explains it; "+
				"a bare phase flip moves the silence rather than ending it", i)
		}
	}
}

// TestPlanEngine_JudgeUnavailable_ParkThatNeverPersistsFailsClosed pins the
// terminal end of the ladder. A park that keeps not taking effect cannot be
// retried forever either: the adjudicator can never see it, plan_correct can
// never act on it, and the bounded FR-021/FR-022 supervision ladder it was
// supposed to hand the plan to is never armed. Past
// plan.MaxJudgeUnavailableParkAttempts the plan must END.
func TestPlanEngine_JudgeUnavailable_ParkThatNeverPersistsFailsClosed(t *testing.T) {
	h := newAllTerminalPlanWithBrokenJudge(t, "judge_not_configured")
	callsAtPark := tickUntilJudgeUnavailablePark(t, h)

	var got *plan.Plan
	// One tick per allowed re-attempt, plus the one that gives up. Every tick
	// starts from the state a failed park write leaves.
	for i := 0; i <= plan.MaxJudgeUnavailableParkAttempts; i++ {
		undoPark(t, h)
		got = tick(t, h)
		if got.State != plan.StateRunning {
			break
		}
	}

	if got.State != plan.StateFailed {
		t.Fatalf("state = %q after %d re-park attempts, want %q — a park that never persists leaves "+
			"the plan neither judged, nor visible, nor correctable, nor terminal, which is the "+
			"silent-stuck class the whole bound exists to end",
			got.State, plan.MaxJudgeUnavailableParkAttempts+1, plan.StateFailed)
	}
	if got.FailedReason != plan.FailedReasonSupervisionUnavailable {
		t.Fatalf("failed_reason = %q, want %q — the plan ends because supervision could not be "+
			"reached, not because the work or the round budget failed",
			got.FailedReason, plan.FailedReasonSupervisionUnavailable)
	}
	if calls := h.judge.callCount(); calls != callsAtPark {
		t.Fatalf("judge called %d times, was %d at the park — not one further judge round may run "+
			"anywhere on this ladder", calls, callsAtPark)
	}
	if got.JudgeRounds != 0 {
		t.Fatalf("judge_rounds = %d, want 0 — unavailability still burns no round, including on "+
			"the path that ends the plan", got.JudgeRounds)
	}
	if got.HandoverText == "" {
		t.Fatal("the terminal must explain itself: an operator reading this plan needs to know the " +
			"judge was unreachable AND that the engine could not record it")
	}
}

// TestPlanEngine_JudgeUnavailable_ParkRetryBudgetIsPerEpisode pins that the
// re-park budget is not a lifetime total. A plan that recovers — the judge
// answers, clearing the streak — must get a full budget again if it later
// re-parks, exactly like the streak it is paired with.
func TestPlanEngine_JudgeUnavailable_ParkRetryBudgetIsPerEpisode(t *testing.T) {
	h := newAllTerminalPlanWithBrokenJudge(t, "transient")
	tickUntilJudgeUnavailablePark(t, h)

	// Spend the whole budget but one.
	for i := 0; i < plan.MaxJudgeUnavailableParkAttempts; i++ {
		undoPark(t, h)
		tick(t, h)
	}

	// The judge becomes reachable again, which is what clears the episode.
	h.pe.clearJudgeUnavailableStreak("p1")

	h.pe.mu.Lock()
	rec, present := h.pe.judgeUnavailableParks["p1"]
	h.pe.mu.Unlock()
	if present {
		t.Fatalf("a reachable judge must clear the park record with the streak; a stale record "+
			"hands the NEXT park a pre-spent budget (attempts=%d) and fails a plan closed for a "+
			"failure that had already resolved", rec.attempts)
	}
}
