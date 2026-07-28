// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- Restart of a plan Stopped while parked (ADR-052 §6.7 / ADR-055 FR-050) --
//
// The single most likely thing a user does to a parked plan is Stop it and
// then Play it again. Before the store-level restart clean slate covered
// plan_phase and the supervision ladder, that sequence produced two silent,
// durable failures — both of which survive a restart and neither of which is
// visible to the user until the plan is already unrecoverable. These tests
// pin the OUTCOMES, not the reset: a restarted plan that stalls gets its stall
// wake, and it gets its full adjudication budget.

const restartStalePreviousWakeAt = "2026-07-01T00:00:00Z"

// stopParkedPlanForRestart builds a plan that ran, parked at
// awaiting_supervision with a spent supervision ladder, and was then Stopped
// by the user — then restarts it (failed -> approved) and admits it back to
// running through the engine's own admission path. It returns the plan id.
//
// The member set is deliberately STALLED (one member blocked behind a failed
// one): the restarted plan can make no progress, which is exactly the
// condition the engine must surface and route to an adjudicator.
func stopParkedPlanForRestart(t *testing.T, h *planEngineHarness) string {
	t.Helper()
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p-restart", Title: "Parked, stopped, restarted", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateRunning, PlanPhase: plan.PhaseAwaitingSupervision,
		LastUnmetTerminalSignature: "sig-from-the-previous-life",
		Supervision: &plan.Supervision{
			WakeAt:           restartStalePreviousWakeAt,
			WakeError:        "supervision wake could not dispatch",
			Attempts:         3, // at the FR-022 ceiling for the previous park
			CorrectionRounds: 2,
			SessionID:        "sess-previous-life",
		},
	})
	// A stalled member set: t-blocker failed, t-blocked waits on it forever.
	mustCreateTask(t, h.tasks, &task.Task{
		ID: "t-blocker", Title: "blocker", WorkspaceID: "ws", PlanID: "p-restart", Status: task.StatusFailed,
	})
	mustCreateTask(t, h.tasks, &task.Task{
		ID: "t-blocked", Title: "blocked", WorkspaceID: "ws", PlanID: "p-restart",
		Status: task.StatusNext, BlockedBy: []string{"t-blocker"},
	})

	// Stop, then restart, then let the engine admit it back to running — the
	// real Play path, not a hand-written running plan.
	failed, stopped := plan.StateFailed, plan.FailedReasonStoppedByUser
	if _, err := h.plans.Update("p-restart", plan.Patch{State: &failed, FailedReason: &stopped}); err != nil {
		t.Fatalf("stop the parked plan: %v", err)
	}
	approved := plan.StateApproved
	if _, err := h.plans.Update("p-restart", plan.Patch{State: &approved}); err != nil {
		t.Fatalf("restart failed[stopped_by_user]->approved: %v", err)
	}
	h.pe.tryStartApprovedPlan(context.Background(), "p-restart")

	admitted, err := h.plans.Get("p-restart")
	if err != nil {
		t.Fatalf("get after admission: %v", err)
	}
	if admitted.State != plan.StateRunning {
		t.Fatalf("setup: plan is %q after Play, want running", admitted.State)
	}
	return "p-restart"
}

// TestRestartedParkedPlan_StillGetsItsStallWake is the outcome test for the
// stale-phase half. A plan Stopped while parked and then Played hits a blocked
// member set on its very next pass. It MUST surface that stall and wake an
// adjudicator.
//
// Carrying plan_phase=awaiting_supervision across the restart makes
// surfaceStallIfAny's precedence guard fire on every single pass — a guard its
// own comment calls "structurally unreachable in production", which the
// restart is precisely what makes reachable, and permanent. The user then sees
// a plan that says "Running", wears a stale "Awaiting supervision" chip, and
// makes no progress until idle expiry terminates it as failed(idle_expired) —
// a reason ValidateRestartTransition refuses to restart. The work is
// unrecoverable.
func TestRestartedParkedPlan_StillGetsItsStallWake(t *testing.T) {
	h := newTestPlanEngine(t)
	planID := stopParkedPlanForRestart(t, h)

	h.pe.processPlan(context.Background(), planID)

	got, err := h.plans.Get(planID)
	if err != nil {
		t.Fatalf("get after processPlan: %v", err)
	}
	if got.EffectivePlanPhase() != plan.PhaseStalled {
		t.Errorf("plan_phase = %q after the restarted run stalled, want %q — a restarted plan that inherits "+
			"`awaiting_supervision` never surfaces a stall again",
			got.EffectivePlanPhase(), plan.PhaseStalled)
	}
	if !strings.HasPrefix(got.HandoverText, stallHandoverNotePrefix) {
		t.Errorf("HandoverText = %q, want a stall note; the stall was never surfaced", got.HandoverText)
	}
	// The wake itself: a supervision wake stamps a FRESH receipt for this
	// park. No receipt, or the previous life's receipt, means no adjudicator
	// was ever asked to look at the restarted run.
	if got.Supervision == nil || got.Supervision.WakeAt == "" {
		t.Fatalf("no supervision wake receipt after the restarted run stalled: %+v", got.Supervision)
	}
	if got.Supervision.WakeAt == restartStalePreviousWakeAt {
		t.Errorf("supervision.wake_at = %q — that is the PREVIOUS life's receipt, so no new wake was issued",
			got.Supervision.WakeAt)
	}
}

// TestRestartedParkedPlan_GetsItsFullSupervisionBudget is the outcome test for
// the stale-ladder half, isolated from the phase half above: the restarted
// plan HAS surfaced its stall and issued its first supervision wake. That
// first wake must be attempt 1 of 3, and the plan must survive its first
// deadline lapse.
//
// Carrying supervision.attempts==3 across the restart makes the first re-park
// stamp 4, so FR-022's `attempts < max_attempts` check fails on the very first
// deadline and the plan dies failed(supervision_unavailable) after giving the
// adjudicator exactly one turn. That reason is a diagnosis of a broken
// PlanSupervisor — false here — and it is not restartable, so the plan is
// permanently dead.
func TestRestartedParkedPlan_GetsItsFullSupervisionBudget(t *testing.T) {
	h := newTestPlanEngine(t)
	planID := stopParkedPlanForRestart(t, h)

	// The park boundary is read off supervision.wake_at, so a restarted plan
	// that still carries the previous life's receipt hands its FIRST new park
	// the previous life's adjudication session — a transcript the plan has
	// already exhausted. Asked for this run's session, the engine must not
	// return that handle. (This harness has no agent loop, so a genuine fresh
	// mint yields "" rather than a new id; what matters is that the answer is
	// not the stale handle.)
	admitted, err := h.plans.Get(planID)
	if err != nil {
		t.Fatalf("get admitted plan: %v", err)
	}
	h.pe.planDecisionMu.Lock()
	// newPark=false on purpose: it is the WEAKEST caller signal, so the
	// assertion below still rests entirely on the restart having cleared the
	// previous life's receipt. Passing true would let the caller flag carry the
	// test and make it vacuous.
	sessionForThisRun := h.pe.ensureSupervisionSessionLocked(admitted, false)
	h.pe.planDecisionMu.Unlock()
	if sessionForThisRun == "sess-previous-life" {
		t.Errorf("the restarted run was handed the PREVIOUS life's adjudication session %q", sessionForThisRun)
	}

	// Pass 1: the stall is surfaced and the first supervision wake is issued.
	h.pe.processPlan(context.Background(), planID)

	afterFirstWake, err := h.plans.Get(planID)
	if err != nil {
		t.Fatalf("get after first wake: %v", err)
	}
	if afterFirstWake.Supervision == nil {
		t.Fatalf("no supervision state after the first wake")
	}
	if afterFirstWake.Supervision.Attempts != 1 {
		t.Errorf("supervision.attempts = %d after the FIRST wake of a restarted plan, want 1 — "+
			"the escalation ladder started pre-spent", afterFirstWake.Supervision.Attempts)
	}

	// Pass 2: the adjudication turn produced nothing before its deadline.
	// FR-022(a) must RE-ISSUE (attempts 1 -> 2), not terminate.
	wakeAt, perr := time.Parse(time.RFC3339, afterFirstWake.Supervision.WakeAt)
	if perr != nil {
		t.Fatalf("parse wake_at %q: %v", afterFirstWake.Supervision.WakeAt, perr)
	}
	h.clock.Set(wakeAt.Add(h.pe.supervisionTurnTimeout(afterFirstWake)).Add(time.Second))
	h.pe.processPlan(context.Background(), planID)

	afterDeadline, err := h.plans.Get(planID)
	if err != nil {
		t.Fatalf("get after deadline: %v", err)
	}
	if afterDeadline.State != plan.StateRunning {
		t.Fatalf("plan is %q(%s) after its FIRST supervision deadline lapsed, want it still running with "+
			"budget to spare", afterDeadline.State, afterDeadline.FailedReason)
	}
	if afterDeadline.FailedReason == plan.FailedReasonSupervisionUnavailable {
		t.Fatalf("plan failed supervision_unavailable after ONE adjudication turn — a false diagnosis of a " +
			"healthy PlanSupervisor, and not a restartable reason")
	}
	if afterDeadline.Supervision.Attempts != 2 {
		t.Errorf("supervision.attempts = %d after one re-issue, want 2", afterDeadline.Supervision.Attempts)
	}
}
