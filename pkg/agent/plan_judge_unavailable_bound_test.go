// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// plan_judge_unavailable_bound_test.go — regression coverage for UAT defect B:
// a plan whose every member had finished never reached a terminal state.
//
// THE BUG. applyJudgeRoundOutcome's `result.Unavailable` branch reverted
// plan_phase to `dispatching` and returned. That is correct ONCE — a judge
// timeout is usually transient and must not burn a judge round. But it was
// unbounded: the next tick found the DAG still all-terminal, started another
// round, that round timed out too, and the plan oscillated
// dispatching -> judging -> dispatching indefinitely at progress=1.0 with
// judge_rounds frozen at its old value. Observed live for 16+ minutes and
// still not terminal, rendering the entire time as an ordinary
// "Running / Judging" chip — no error, no timeout, nothing distinguishing a
// wedged plan from real work. The correction path was locked out too, because
// plan_correct refuses a plan in phase `judging`.
//
// THE FIX. plan.MaxConsecutiveJudgeUnavailable bounds the streak. Past it the
// plan parks at PhaseStalled — visible on the wire, and a phase in which
// plan_correct is accepted — and wakes the adjudicator, whose attempt ladder
// is itself bounded and ends at a real terminal state.

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newAllTerminalPlanWithBrokenJudge seeds a running plan whose single member
// is already `done` (so allMembersTerminal is true and the plan judge is the
// only thing left to happen) and wires a judge that is permanently
// unavailable.
func newAllTerminalPlanWithBrokenJudge(t *testing.T, reason string) *planEngineHarness {
	t.Helper()
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		DoD:           []task.AcceptanceCriterion{planProseCriterion("The thing is done")},
		SourceChannel: "telegram", SourceChatID: "chat-p1",
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})
	h.judge.resultFn = func(_ JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Unavailable: true, Reason: reason}
	}
	return h
}

// tick runs one full engine pass and waits for the judge goroutine it may have
// launched, so each call is one complete round.
func tick(t *testing.T, h *planEngineHarness) *plan.Plan {
	t.Helper()
	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()
	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	return got
}

// TestPlanEngine_JudgeUnavailable_ParksAtStalledOnceRetryBoundIsReached is the
// defect-B regression test.
//
// Against the pre-fix engine every round — including the hundredth — reverts
// to `dispatching`, so the final assertion fails with
// plan_phase = "dispatching": the plan oscillates forever.
func TestPlanEngine_JudgeUnavailable_ParksAtStalledOnceRetryBoundIsReached(t *testing.T) {
	h := newAllTerminalPlanWithBrokenJudge(t, "turn timed out: context deadline exceeded")

	// Rounds below the bound must still retry — the transient case must keep
	// working, and this half of the test guards against "fix" it by parking
	// on the very first hiccup.
	for i := 1; i < plan.MaxConsecutiveJudgeUnavailable; i++ {
		got := tick(t, h)
		if got.PlanPhase != plan.PhaseDispatching {
			t.Fatalf("round %d: plan_phase = %q, want %q — a judge failure below the retry bound "+
				"must still be retried, not escalated", i, got.PlanPhase, plan.PhaseDispatching)
		}
		if got.State != plan.StateRunning {
			t.Fatalf("round %d: state = %q, want running", i, got.State)
		}
	}

	// The round that reaches the bound must PARK instead of reverting.
	got := tick(t, h)

	if got.PlanPhase != plan.PhaseStalled {
		t.Fatalf("after %d consecutive unavailable judge rounds: plan_phase = %q, want %q — "+
			"the plan is still oscillating dispatching<->judging with nothing left to dispatch "+
			"(UAT defect B)", plan.MaxConsecutiveJudgeUnavailable, got.PlanPhase, plan.PhaseStalled)
	}

	// The stall must be EXPLAINED, not merely flagged: "looks normal while
	// silently stuck" is the bug class here, so a bare phase flip with no
	// reason would only move the silence.
	if !strings.HasPrefix(got.HandoverText, stallHandoverNotePrefix) {
		t.Fatalf("HandoverText = %q, want a stall note (prefix %q)", got.HandoverText, stallHandoverNotePrefix)
	}
	if !strings.Contains(got.HandoverText, "judge") {
		t.Fatalf("HandoverText = %q, want it to name the judge as the reason", got.HandoverText)
	}
	if !strings.Contains(got.HandoverText, "context deadline exceeded") {
		t.Fatalf("HandoverText = %q, want it to carry the judge's own failure reason", got.HandoverText)
	}

	// The no-round-burn contract is unchanged: unavailability is the
	// provider's fault, not the plan's, and must not consume the user's
	// correction budget.
	if got.JudgeRounds != 0 {
		t.Fatalf("judge_rounds = %d, want 0 — an unavailable judge must still consume no round",
			got.JudgeRounds)
	}

	// The park must be a real supervision park, because that is what makes it
	// recoverable: PhaseStalled is a phase in which plan_correct is accepted,
	// and the wake receipt is what arms the bounded FR-021/FR-022 ladder that
	// eventually terminates the plan.
	if got.Supervision == nil || got.Supervision.WakeAt == "" {
		t.Fatal("parking must issue a supervision wake (durable receipt) — without it the plan is " +
			"parked with no deadline, no ceiling and no terminal state, which is the original bug " +
			"wearing a different phase name")
	}
	if !plan.IsSupervisionEligiblePhase(got.EffectivePlanPhase()) {
		t.Fatalf("phase %q must be supervision-eligible so plan_correct is accepted; the live "+
			"incident could not be corrected at all because the plan kept flipping to \"judging\"",
			got.EffectivePlanPhase())
	}
}

// TestPlanEngine_JudgeUnavailable_ParkedPlanIsNotRejudged pins the second half
// of the fix. Parking is worthless if the very next tick starts another judge
// round and reverts it — processPlan's phase switch handles only
// judging/synthesizing, so an all-terminal stalled plan would otherwise fall
// straight through to beginPlanJudgeRound and un-park itself.
func TestPlanEngine_JudgeUnavailable_ParkedPlanIsNotRejudged(t *testing.T) {
	h := newAllTerminalPlanWithBrokenJudge(t, "judge_not_configured")

	for i := 0; i < plan.MaxConsecutiveJudgeUnavailable; i++ {
		tick(t, h)
	}
	if got := tick(t, h); got.PlanPhase != plan.PhaseStalled {
		t.Fatalf("precondition: plan_phase = %q, want %q", got.PlanPhase, plan.PhaseStalled)
	}
	callsAtPark := h.judge.callCount()

	// Several further ticks must not start another judge round.
	for i := 0; i < 3; i++ {
		got := tick(t, h)
		if got.PlanPhase == plan.PhaseJudging || got.PlanPhase == plan.PhaseDispatching {
			t.Fatalf("tick %d after parking: plan_phase = %q — a parked plan must not be "+
				"re-dispatched or re-judged; it is waiting for an adjudicator", i, got.PlanPhase)
		}
	}
	if got := h.judge.callCount(); got != callsAtPark {
		t.Fatalf("judge called %d times after parking (was %d) — a parked plan must not burn "+
			"further judge rounds", got, callsAtPark)
	}
}

// TestPlanEngine_StalledForOtherReasons_IsStillJudgedWhenAllTerminal pins the
// boundary of the "don't re-judge a parked plan" gate in processPlan.
//
// This caught a real regression in the first version of the defect-B fix,
// which gated on plan_phase == stalled ALONE. PhaseStalled is also set by
// surfaceStallIfAny for an ordinary blocked/inbox stall, and that phase
// PERSISTS after the condition clears (surfaceStallIfAny is the only code that
// clears it, and it is only reached on the non-all-terminal path). So a plan
// that stalled on a blocked member and then had that member resolve arrives
// all-terminal and still phased `stalled` — and the broad gate wedged it
// forever, silently, which is the very failure mode defect B is about.
//
// Here the plan is stalled with NO judge-unavailability history, so its judge
// round must still run.
func TestPlanEngine_StalledForOtherReasons_IsStillJudgedWhenAllTerminal(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		DoD:           []task.AcceptanceCriterion{planProseCriterion("The thing is done")},
		SourceChannel: "telegram", SourceChatID: "chat-p1",
		// Parked at stalled by an ordinary blocked-member stall that has
		// since resolved — the phase outlives the condition.
		PlanPhase: plan.PhaseStalled,
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})
	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met:          true,
			PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: true, Reason: "ok"}},
		}}
	}

	got := tick(t, h)

	if h.judge.callCount() == 0 {
		t.Fatal("a plan stalled for a NON-judge reason that is now all-terminal must still be " +
			"judged — gating the judge on plan_phase==stalled alone wedges it forever with no " +
			"error and no terminal state")
	}
	if got.State != plan.StateDone {
		t.Fatalf("state = %q, want done — the judge returned MET, so the plan must complete", got.State)
	}
}

// TestPlanEngine_JudgeUnavailable_StreakResetsOnRealVerdict pins that the
// streak measures CONSECUTIVE failures. A judge that fails twice, recovers,
// and later fails again must not be one failure away from parking forever
// after — otherwise a long-lived plan on a flaky provider would eventually
// park for reasons that had already resolved.
func TestPlanEngine_JudgeUnavailable_StreakResetsOnRealVerdict(t *testing.T) {
	h := newAllTerminalPlanWithBrokenJudge(t, "transient")

	// Two failures — one short of the bound.
	for i := 1; i < plan.MaxConsecutiveJudgeUnavailable; i++ {
		if got := tick(t, h); got.PlanPhase != plan.PhaseDispatching {
			t.Fatalf("round %d: plan_phase = %q, want dispatching", i, got.PlanPhase)
		}
	}

	// The judge recovers and returns a real (unmet) verdict.
	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met: false,
			PerCriterion: []task.CriterionVerdict{
				{CriterionID: in.Criteria[0].ID, Met: false, Reason: "not yet"},
			},
		}}
	}
	got := tick(t, h)
	if got.JudgeRounds != 1 {
		t.Fatalf("judge_rounds = %d, want 1 after a real verdict", got.JudgeRounds)
	}
	if got.PlanPhase != plan.PhaseAwaitingSupervision {
		t.Fatalf("plan_phase = %q, want %q after an unmet verdict", got.PlanPhase, plan.PhaseAwaitingSupervision)
	}

	// A real verdict proves the judge is reachable, so the streak is over. The
	// engine's own accessor is the observable: asserting via further ticks
	// would be confounded by the unmet park's F2 signature gate.
	h.pe.mu.Lock()
	streak := h.pe.judgeUnavailableStreak["p1"]
	h.pe.mu.Unlock()
	if streak != 0 {
		t.Fatalf("judgeUnavailableStreak = %d after a real verdict, want 0 — the counter must "+
			"measure the CURRENT unbroken run of unavailability, not a lifetime total", streak)
	}
}
