// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_supervision_suppression_streak_test.go covers a LOW code-review
// finding on wakeSupervisor's CAS-suppression logging (plan_engine.go): every
// suppression logged an identical Info line with no held-duration or attempt
// count, making a genuinely wedged claim (which would also block the plan's
// own stall-abandon path) indistinguishable from routine, healthy overlap
// (an existing supervision turn about to settle in the next tick or two).
//
// The fix adds supervisionSuppressStreak, an in-memory per-plan counter of
// CONSECUTIVE suppressions (bumpSupervisionSuppressStreak/
// clearSupervisionSuppressStreak, plan_engine.go), threaded into the
// suppression log line as consecutive_suppressions, and reset the moment a
// claim actually succeeds for that plan (a real turn is about to run).
package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
)

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
