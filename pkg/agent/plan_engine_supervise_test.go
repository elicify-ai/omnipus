// plan_engine_supervise_test.go: tests for supervise — unmet Definition-of-Done handling and the signature gate (judge rounds, stall parking, supervision wakes and deadlines)

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- moved from plan_engine.go tests 2026-09-15 ---

// TestApplyJudgeRoundOutcomeLocked_UnmetWrite_DroppedAfterConcurrentStop is
// the "plan-scope variant" TOCTOU test the fix-wave brief names explicitly:
// PRE-FIX, the equivalent re-check (verdictStillApplicable) ran under its
// OWN separate lock acquisition and then RELEASED it before the outcome
// writes ran — plan.Store's own transition guard rejects a stale State
// write (failed->done is illegal), but has no opinion on the OTHER fields,
// so a Stop that had ALREADY landed by the time the outcome was applied
// could still see its own HandoverText clobbered by the round's steering
// text and a spurious plan_judge_unmet wakeOwner fired for an
// already-stopped plan. This drives the plan store DIRECTLY to simulate a
// Stop having already landed (the sanctioned "driving the store directly to
// simulate the interleaving" technique — mirrors the task-level FR-014
// tests) and calls applyJudgeRoundOutcome — the exact function
// runPlanJudgeRound now delegates to — directly with a freshly-computed
// UNMET verdict, proving its OWN re-check (not a stale caller-side one)
// drops the outcome entirely: JudgeRounds/PlanPhase never move, the Stop's
// HandoverText is never clobbered, and no owner-wake fires.
func TestApplyJudgeRoundOutcomeLocked_UnmetWrite_DroppedAfterConcurrentStop(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")

	// Simulate a Stop having ALREADY landed (bypassing StopPlan/planDecisionMu
	// entirely) before the round's outcome is applied — exactly the state
	// PlanEngine.StopPlan itself would have written.
	failedState := plan.StateFailed
	reason := plan.FailedReasonStoppedByUser
	stopHandover := `Plan "p1" was stopped by tester.`
	if _, err := h.plans.Update("p1", plan.Patch{
		State: &failedState, FailedReason: &reason, HandoverText: &stopHandover,
	}); err != nil {
		t.Fatalf("simulate concurrent Stop: %v", err)
	}

	h.pe.applyJudgeRoundOutcome("p1", JudgeCriteriaResult{
		Verdict: &task.JudgeVerdict{
			Met:          false,
			PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: false, Reason: "not done yet"}},
		},
	}, false, "")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.JudgeRounds != 0 {
		t.Errorf("JudgeRounds = %d, want 0 — the round's write must be dropped entirely, never applied to "+
			"an already-stopped plan", got.JudgeRounds)
	}
	if got.HandoverText != stopHandover {
		t.Errorf("HandoverText = %q, want the Stop's own handover UNCHANGED (%q) — must never be clobbered "+
			"by a stale round's steering text", got.HandoverText, stopHandover)
	}
	if got.State != plan.StateFailed || got.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("state=%q reason=%q, want the Stop outcome left untouched", got.State, got.FailedReason)
	}
	if events := h.notif.eventList(); len(events) != 0 {
		t.Errorf("expected 0 owner-wake notifications on a dropped round outcome, got %d: %+v", len(events), events)
	}
}

// TestApplyJudgeRoundOutcomeLocked_MetWrite_DroppedAfterConcurrentStop is the
// MET-verdict counterpart: synthesizeAndComplete's plan_phase=synthesizing +
// State=done writes must also never land on an already-stopped plan.
func TestApplyJudgeRoundOutcomeLocked_MetWrite_DroppedAfterConcurrentStop(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")

	failedState := plan.StateFailed
	reason := plan.FailedReasonStoppedByUser
	stopHandover := `Plan "p1" was stopped by tester.`
	if _, err := h.plans.Update("p1", plan.Patch{
		State: &failedState, FailedReason: &reason, HandoverText: &stopHandover,
	}); err != nil {
		t.Fatalf("simulate concurrent Stop: %v", err)
	}

	h.pe.applyJudgeRoundOutcome("p1", JudgeCriteriaResult{
		Verdict: &task.JudgeVerdict{Met: true},
	}, false, "")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateFailed || got.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("state=%q reason=%q, want the Stop outcome left untouched — a stale MET verdict must never "+
			"complete an already-stopped plan", got.State, got.FailedReason)
	}
	if got.PlanPhase == plan.PhaseSynthesizing {
		t.Error("plan_phase must not have been set to synthesizing for a dropped MET outcome")
	}
	if events := h.notif.eventList(); len(events) != 0 {
		t.Errorf("expected 0 owner-wake notifications, got %d: %+v", len(events), events)
	}
}

// TestApplyJudgeRoundOutcomeLocked_AppliesWhenStillRunning is the control:
// with NO concurrent Stop, the SAME function applies the outcome normally —
// proving the drop above is genuinely conditioned on the state re-check, not
// a function that silently no-ops always.
func TestApplyJudgeRoundOutcomeLocked_AppliesWhenStillRunning(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")

	h.pe.applyJudgeRoundOutcome("p1", JudgeCriteriaResult{
		Verdict: &task.JudgeVerdict{
			Met:          false,
			PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: false, Reason: "not done yet"}},
		},
	}, false, "")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.JudgeRounds != 1 {
		t.Errorf("JudgeRounds = %d, want 1 — a still-running plan's outcome must apply normally", got.JudgeRounds)
	}
	if got.State != plan.StateRunning {
		t.Errorf("state = %q, want running (unmet does not terminate the plan)", got.State)
	}
	// FR-012: the UNMET wake is family A — it targets the PlanSupervisor by
	// DIRECT dispatch and never touches pe.notifier. So the owner's chat
	// origin must see nothing (FR-016: the adjudicator's deliberation must not
	// reach the owner's conversation).
	for _, e := range h.notif.eventList() {
		if e.SourceKind == "plan_judge_unmet" {
			t.Errorf("the UNMET wake must NOT be published to the plan's chat origin (FR-016): %+v", e)
		}
	}
	// …but "the notifier was silent" is ALSO what a completely broken wake
	// looks like, so assert the supervision wake positively: it parked the
	// plan and armed FR-021's deadline as attempt 1. Without this the test
	// would keep passing if the UNMET limb stopped waking anyone at all.
	if got.EffectivePlanPhase() != plan.PhaseAwaitingSupervision {
		t.Errorf("plan_phase = %q, want awaiting_supervision — an UNMET outcome must park the plan for the adjudicator",
			got.EffectivePlanPhase())
	}
	if got.Supervision == nil || got.Supervision.WakeAt == "" {
		t.Errorf("the UNMET wake must stamp supervision.wake_at (it arms the deadline), got %+v", got.Supervision)
	} else if got.Supervision.Attempts != 1 {
		t.Errorf("supervision.attempts = %d, want 1 after the first supervision wake", got.Supervision.Attempts)
	}
}

// TestWakeSupervisor_SuppressionStreakIncrementsAcrossConsecutiveRefusals
// proves the counting half: repeated wakeSupervisor calls against a plan
// whose CAS claim is already held by a DIFFERENT session ID each increment
// the SAME plan's streak, giving an operator a way to tell "suppressed once
// or twice, routine" from "suppressed dozens of times running, the holder
// may be wedged".
func TestWakeSupervisor_SuppressionStreakIncrementsAcrossConsecutiveRefusals(t *testing.T) {
	h := newTestPlanEngine(t)
	p := mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p-streak", Title: "p-streak", WorkspaceID: "ws",
		OwnerAgentID: "agent-1", State: plan.StateRunning,
	})

	unit := supervisionUnitForPlan(p.ID)
	// A DIFFERENT, already-live session holds the claim — every wakeSupervisor
	// call below must hit the CAS-suppressed branch (ErrVerifierSessionHeld),
	// never the success path, so the streak measures ONLY suppressions.
	if err := h.pe.registry().Register(unit, "some-other-live-session"); err != nil {
		t.Fatalf("seed the held claim: %v", err)
	}

	for i, want := range []int{1, 2, 3} {
		h.pe.wakeSupervisor(p, "content", "test-reason", false)
		got := h.pe.supervisionSuppressStreak[p.ID]
		if got != want {
			t.Fatalf("call %d: supervisionSuppressStreak[%q] = %d, want %d (must increment on every "+
				"consecutive suppression)", i+1, p.ID, got, want)
		}
	}

	// The registry itself must NOT have been clobbered by any suppressed
	// call — the whole point of the CAS is that a suppressed wake has zero
	// side effects on the held claim.
	if sid, ok := h.pe.registry().Lookup(unit); !ok || sid != "some-other-live-session" {
		t.Fatalf("held claim was disturbed by a suppressed wake: Lookup = (%q, %v), want (%q, true)",
			sid, ok, "some-other-live-session")
	}
}

// TestWakeSupervisor_SuppressionStreakResetsOnceClaimSucceeds proves the
// reset half: once the prior holder releases the claim and a subsequent
// wakeSupervisor call actually SUCCEEDS (a real turn is about to run for this
// plan), the consecutive-suppression streak is cleared — so a later,
// unrelated run of suppressions for the same plan starts counting from zero
// rather than compounding across two different holders' overlap windows.
func TestWakeSupervisor_SuppressionStreakResetsOnceClaimSucceeds(t *testing.T) {
	h := newTestPlanEngine(t)
	p := mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p-reset", Title: "p-reset", WorkspaceID: "ws",
		OwnerAgentID: "agent-1", State: plan.StateRunning,
	})
	unit := supervisionUnitForPlan(p.ID)

	if err := h.pe.registry().Register(unit, "prior-holder-session"); err != nil {
		t.Fatalf("seed the held claim: %v", err)
	}
	h.pe.wakeSupervisor(p, "content", "test-reason", false)
	h.pe.wakeSupervisor(p, "content", "test-reason", false)
	if got := h.pe.supervisionSuppressStreak[p.ID]; got != 2 {
		t.Fatalf("setup: streak = %d, want 2 before the claim is released", got)
	}

	// The prior holder settles (mirrors onTurnSettled's release).
	h.pe.registry().Unregister(unit)

	// This call's CAS claim now succeeds — reset the notifier/dispatcher fakes
	// are already wired by newTestPlanEngine, so wakeSupervisor can proceed
	// past the claim into its own dispatch machinery without a nil panic.
	h.pe.wakeSupervisor(p, "content", "test-reason", false)

	if _, ok := h.pe.supervisionSuppressStreak[p.ID]; ok {
		t.Fatalf("supervisionSuppressStreak[%q] still has an entry after a successful claim — "+
			"must be cleared the moment a turn actually gets to run", p.ID)
	}
}
