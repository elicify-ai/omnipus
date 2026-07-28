// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

import "testing"

// parkPlanForSupervision drives id to a REALISTIC parked-then-stopped state:
// running at plan_phase=awaiting_supervision with a durable unmet signature, a
// spent supervision ladder (a wake receipt, a wake error, attempts at the
// ceiling, an adjudication session, and a correction already counted), and
// then failed(stopped_by_user) — i.e. exactly what is on disk when a user hits
// Stop on a parked plan, the single most likely thing to happen to one.
func parkPlanForSupervision(t *testing.T, s *Store, id string) *Plan {
	t.Helper()
	approved, running, failed := StateApproved, StateRunning, StateFailed
	if _, err := s.Update(id, Patch{State: &approved}); err != nil {
		t.Fatalf("draft->approved: %v", err)
	}
	if _, err := s.Update(id, Patch{State: &running}); err != nil {
		t.Fatalf("approved->running: %v", err)
	}
	phase := PhaseAwaitingSupervision
	sig := "sig-from-the-previous-life"
	wakeAt := "2026-07-19T12:00:00Z"
	wakeErr := "supervision wake could not dispatch"
	attempts := 3
	rounds := 2
	sessionID := "sess-previous-life"
	if _, err := s.Update(id, Patch{
		PlanPhase:                   &phase,
		LastUnmetTerminalSignature:  &sig,
		SupervisionWakeAt:           &wakeAt,
		SupervisionWakeError:        &wakeErr,
		SupervisionAttempts:         &attempts,
		SupervisionCorrectionRounds: &rounds,
		SupervisionSessionID:        &sessionID,
	}); err != nil {
		t.Fatalf("park at awaiting_supervision: %v", err)
	}
	reason := FailedReasonStoppedByUser
	stopped, err := s.Update(id, Patch{State: &failed, FailedReason: &reason})
	if err != nil {
		t.Fatalf("running->failed(stopped_by_user): %v", err)
	}
	if stopped.PlanPhase != PhaseAwaitingSupervision || stopped.Supervision == nil || stopped.Supervision.Attempts != 3 {
		t.Fatalf("setup: unexpected stopped state: %+v (supervision %+v)", stopped, stopped.Supervision)
	}
	return stopped
}

// TestRestart_ClearsPlanPhase pins the durable half of the restart clean slate
// for plan_phase: a plan Stopped while parked and then restarted must not
// re-enter the state machine still claiming to be parked.
//
// The consequence this guards is not cosmetic. pkg/agent's surfaceStallIfAny
// returns early while the phase reads awaiting_supervision, so a restarted
// plan that inherits the phase never surfaces a stall, never wakes an
// adjudicator, and rots until idle expiry terminates it as
// failed(idle_expired) — a reason ValidateRestartTransition then refuses to
// restart. The engine-side proof of that outcome lives in pkg/agent
// (plan_restart_supervision_reset_test.go); this test pins the record it
// reads.
func TestRestart_ClearsPlanPhase(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Parked then stopped", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	parkPlanForSupervision(t, s, p.ID)

	approved := StateApproved
	restarted, err := s.Update(p.ID, Patch{State: &approved})
	if err != nil {
		t.Fatalf("restart failed[stopped_by_user]->approved: %v", err)
	}

	if restarted.PlanPhase != "" {
		t.Errorf("restart: PlanPhase = %q, want it cleared (a plan at `approved` has not run yet)", restarted.PlanPhase)
	}
	if got := restarted.EffectivePlanPhase(); got != PhaseIdle {
		t.Errorf("restart: EffectivePlanPhase() = %q, want %q", got, PhaseIdle)
	}
	if restarted.IsAwaitingSupervision() {
		t.Error("restart: plan still reports IsAwaitingSupervision(); the restarted run is not parked")
	}

	// The reset is durable, not just an in-memory artifact of the returned
	// pointer — a restart's whole point is surviving into the next run.
	reloaded, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if reloaded.PlanPhase != "" {
		t.Errorf("restart: reloaded PlanPhase = %q, want cleared on disk", reloaded.PlanPhase)
	}
}

// TestRestart_ResetsSupervisionLadder pins the other durable half: a restarted
// plan gets its FULL supervision attempt budget and a fresh park boundary,
// while the two fields whose whole contract is to outlive a park survive.
//
// Carried over, attempts==3 means the first re-park sets 4, the FR-022
// `attempts < max` check fails on that very first park, and the plan dies
// failed(supervision_unavailable) — a false diagnosis of a healthy
// PlanSupervisor, and not itself a restartable reason. Carried over, wake_at
// makes ensureSupervisionSessionLocked treat the new park as the OLD one and
// hand it the previous life's adjudication session.
func TestRestart_ResetsSupervisionLadder(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Parked then stopped", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	parkPlanForSupervision(t, s, p.ID)

	approved := StateApproved
	if _, err := s.Update(p.ID, Patch{State: &approved}); err != nil {
		t.Fatalf("restart failed[stopped_by_user]->approved: %v", err)
	}

	reloaded, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	sup := reloaded.Supervision
	if sup == nil {
		t.Fatal("restart dropped the whole supervision object; correction_rounds/session_id must survive")
	}
	if sup.Attempts != 0 {
		t.Errorf("restart: supervision.attempts = %d, want 0 — the ladder must not start pre-spent", sup.Attempts)
	}
	if sup.WakeAt != "" {
		t.Errorf("restart: supervision.wake_at = %q, want cleared — a spent deadline must not stay armed, "+
			"and the next park must mint its own adjudication session", sup.WakeAt)
	}
	if sup.WakeError != "" {
		t.Errorf("restart: supervision.wake_error = %q, want cleared — a prior life's wake failure is not "+
			"evidence about this run", sup.WakeError)
	}
	// The two fields that deliberately outlive a park (Supervision's type doc):
	// correction_rounds is cumulative for the life of the plan and is the only
	// thing that tells "the budget ran out with no correction" apart from
	// "corrections consumed the budget"; session_id is never blanked anywhere
	// because it is the handle a Stop cancels.
	if sup.CorrectionRounds != 2 {
		t.Errorf("restart: supervision.correction_rounds = %d, want 2 preserved (cumulative, never reset)",
			sup.CorrectionRounds)
	}
	if sup.SessionID != "sess-previous-life" {
		t.Errorf("restart: supervision.session_id = %q, want it preserved (never blanked; overwritten by the next mint)",
			sup.SessionID)
	}
}

// TestRestart_LeavesNilSupervisionNil pins applySupervisionPatch's
// no-allocation property across the restart path too: a plan that was never
// supervised must keep serialising with no `supervision` key at all, so the
// reset may never materialise an empty object just to zero it.
func TestRestart_LeavesNilSupervisionNil(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Never supervised", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	driveToFailed(t, s, p.ID, FailedReasonStoppedByUser, 1, "")

	approved := StateApproved
	restarted, err := s.Update(p.ID, Patch{State: &approved})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if restarted.Supervision != nil {
		t.Errorf("restart allocated a Supervision object on a never-supervised plan: %+v", restarted.Supervision)
	}
	reloaded, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if reloaded.Supervision != nil {
		t.Errorf("restart persisted an empty supervision object: %+v", reloaded.Supervision)
	}
}

// TestRestart_ResetWinsOverACraftedPatch pins the ordering the fix depends on:
// the clean-slate reset runs AFTER every per-field application (including
// applySupervisionPatch and the plan_phase field), so a caller that batches a
// phase or a supervision value into the same patch as the restart transition
// cannot smuggle either past it. This is the same crafted-patch class the
// FailedReason/JudgeRounds guard closes by rejecting; these five close it by
// overriding, which needs no cooperation from any caller.
func TestRestart_ResetWinsOverACraftedPatch(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Crafted restart", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	parkPlanForSupervision(t, s, p.ID)

	approved := StateApproved
	craftedPhase := PhaseAwaitingSupervision
	craftedWake := "2026-07-19T12:00:00Z"
	craftedAttempts := 3
	restarted, err := s.Update(p.ID, Patch{
		State:               &approved,
		PlanPhase:           &craftedPhase,
		SupervisionWakeAt:   &craftedWake,
		SupervisionAttempts: &craftedAttempts,
	})
	if err != nil {
		t.Fatalf("restart with a crafted patch: %v", err)
	}
	if restarted.PlanPhase != "" {
		t.Errorf("crafted plan_phase survived the restart reset: %q", restarted.PlanPhase)
	}
	if restarted.Supervision == nil {
		t.Fatal("supervision object dropped")
	}
	if restarted.Supervision.WakeAt != "" || restarted.Supervision.Attempts != 0 {
		t.Errorf("crafted supervision values survived the restart reset: wake_at=%q attempts=%d",
			restarted.Supervision.WakeAt, restarted.Supervision.Attempts)
	}
}

// TestNonRestartUpdate_LeavesPhaseAndSupervisionAlone is the negative control:
// the reset is scoped to the restart transition alone. An ordinary update — a
// park, a phase flip, a supervision write — must be completely unaffected, or
// the fix would silently disarm every live supervision deadline in the system.
func TestNonRestartUpdate_LeavesPhaseAndSupervisionAlone(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Live plan", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	approved, running := StateApproved, StateRunning
	if _, err := s.Update(p.ID, Patch{State: &approved}); err != nil {
		t.Fatalf("draft->approved: %v", err)
	}
	if _, err := s.Update(p.ID, Patch{State: &running}); err != nil {
		t.Fatalf("approved->running: %v", err)
	}

	phase := PhaseAwaitingSupervision
	wakeAt := "2026-07-19T12:00:00Z"
	attempts := 2
	parked, err := s.Update(p.ID, Patch{PlanPhase: &phase, SupervisionWakeAt: &wakeAt, SupervisionAttempts: &attempts})
	if err != nil {
		t.Fatalf("park: %v", err)
	}
	if parked.PlanPhase != PhaseAwaitingSupervision {
		t.Fatalf("ordinary park lost its phase: %q", parked.PlanPhase)
	}
	if parked.Supervision == nil || parked.Supervision.WakeAt != wakeAt || parked.Supervision.Attempts != 2 {
		t.Fatalf("ordinary park lost its supervision state: %+v", parked.Supervision)
	}

	// A no-op running->running update must not disturb either.
	stillRunning := StateRunning
	after, err := s.Update(p.ID, Patch{State: &stillRunning})
	if err != nil {
		t.Fatalf("running->running: %v", err)
	}
	if after.PlanPhase != PhaseAwaitingSupervision {
		t.Errorf("a non-restart state update cleared plan_phase: %q", after.PlanPhase)
	}
	if after.Supervision == nil || after.Supervision.WakeAt != wakeAt || after.Supervision.Attempts != 2 {
		t.Errorf("a non-restart state update disarmed the supervision ladder: %+v", after.Supervision)
	}
}
