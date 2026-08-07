// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- FR-148/INV-6: boot replay must reach the live apply's end state --------
//
// ReplayAtBoot marks an intent DONE the moment applyIntentRecord returns nil,
// and a done intent is never replayed again. So "returned nil" is a permanent
// claim that every write in the record landed. These tests assert the two
// outcomes that claim has to be worth: a replayed correction leaves an
// ORDERED plan, and a replay that could not finish leaves a record a later
// boot can still finish — never one that says "applied" over a broken plan.

// withIntentLog wires a real intent log into the harness and returns it.
func withIntentLog(t *testing.T, h *planEngineHarness) *plan.IntentLog {
	t.Helper()
	il, err := plan.NewIntentLog(filepath.Join(t.TempDir(), "plan_intents"))
	if err != nil {
		t.Fatalf("new intent log: %v", err)
	}
	h.pe.mu.Lock()
	h.pe.intentLog = il
	h.pe.mu.Unlock()
	return il
}

// commitIntent writes rec to the log and flips it to `committed` WITHOUT
// applying it — the exact durable state a crash (or a failed in-process apply)
// leaves behind, and the only state ReplayAtBoot replays forward.
func commitIntent(t *testing.T, il *plan.IntentLog, rec plan.IntentRecord) {
	t.Helper()
	if err := il.AppendIntent(rec); err != nil {
		t.Fatalf("append intent: %v", err)
	}
	if err := il.MarkCommitted(rec.PlanID, rec.IntentID); err != nil {
		t.Fatalf("mark committed: %v", err)
	}
}

// intentStatus returns the current status of intentID in planID's log.
func intentStatus(t *testing.T, il *plan.IntentLog, planID, intentID string) plan.IntentStatus {
	t.Helper()
	records, err := il.List(planID)
	if err != nil {
		t.Fatalf("list intents: %v", err)
	}
	for i := range records {
		if records[i].IntentID == intentID {
			return records[i].Status
		}
	}
	t.Fatalf("intent %q not found in plan %q's log", intentID, planID)
	return ""
}

// parkedPlanForCorrection creates a plan parked at awaiting_supervision with a
// durable unmet signature and a live supervision wake — the only state from
// which a correction is ever committed.
func parkedPlanForCorrection(t *testing.T, h *planEngineHarness, id string) {
	t.Helper()
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: id, Title: "Parked plan", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateRunning, PlanPhase: plan.PhaseAwaitingSupervision,
		LastUnmetTerminalSignature: "sig-unmet",
		Supervision: &plan.Supervision{
			WakeAt:           "2026-07-19T12:00:00Z",
			WakeError:        "a wake failure from the park being corrected",
			Attempts:         2,
			CorrectionRounds: 1,
			SessionID:        "sess-adjudication",
		},
	})
}

// tailMember builds a tail member body exactly as pkg/tools/plan_correct.go's
// parseTailMembers does — in particular with an EMPTY BlockedBy. The edge list
// is carried separately on the intent record and is the ONLY place a tail
// member's ordering exists.
func tailMember(id, planID string) task.Task {
	return task.Task{
		ID: id, Title: id, WorkspaceID: "ws", PlanID: planID,
		Action: task.ActionLLM, Status: task.StatusNext, CreatedBy: "plansupervisor",
	}
}

// TestIntentReplay_WiresTailEdges is the core outcome test: a correction whose
// tail members were created but whose edges were never wired (the in-process
// apply died between the two) must come back from boot ORDERED.
//
// Before the fix the replay created members and patched the phase and stopped
// there, then marked the intent done — so a sequenced tail came back with
// every member `next`, running concurrently, while the durable record said the
// correction had been fully applied. Nothing would ever revisit it.
func TestIntentReplay_WiresTailEdges(t *testing.T) {
	h := newTestPlanEngine(t)
	il := withIntentLog(t, h)
	parkedPlanForCorrection(t, h, "p1")

	commitIntent(t, il, plan.IntentRecord{
		IntentID: "rev-1", PlanID: "p1",
		Members: []task.Task{tailMember("m1", "p1"), tailMember("m2", "p1")},
		Edges:   []plan.IntentEdge{{FromTaskID: "m1", ToTaskID: "m2"}},
		Revision: plan.RevisionEntry{
			RevisionID: "rev-1", PlanID: "p1", Verb: plan.RevisionAppend,
			TailAdds: []string{"m1", "m2"}, CreatedAt: time.Now().UTC(),
		},
		Patch: plan.IntentRecordPatch{
			ClearLastUnmetTerminalSignature: true, PlanPhase: plan.PhaseDispatching,
		},
		CreatedAt: time.Now().UTC(),
	})

	h.pe.replayIntentLogs()

	m2, err := h.tasks.Get("m2")
	if err != nil {
		t.Fatalf("get m2 after replay: %v", err)
	}
	if len(m2.BlockedBy) != 1 || m2.BlockedBy[0] != "m1" {
		t.Fatalf("m2.BlockedBy = %v, want [m1] — the replayed tail is UNORDERED, so sequenced work runs "+
			"concurrently while the intent log records the correction as applied", m2.BlockedBy)
	}
	if m2.Status != task.StatusBlocked {
		t.Errorf("m2.Status = %q, want blocked behind m1", m2.Status)
	}
	m1, err := h.tasks.Get("m1")
	if err != nil {
		t.Fatalf("get m1 after replay: %v", err)
	}
	if m1.Status != task.StatusNext {
		t.Errorf("m1.Status = %q, want next (the head of the tail is dispatchable)", m1.Status)
	}

	// The plan-record half of the apply still happens.
	p, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan after replay: %v", err)
	}
	if p.EffectivePlanPhase() != plan.PhaseDispatching {
		t.Errorf("plan_phase = %q, want dispatching", p.EffectivePlanPhase())
	}
	if p.LastUnmetTerminalSignature != "" {
		t.Errorf("last_unmet_terminal_signature = %q, want cleared (a correction is new activity)",
			p.LastUnmetTerminalSignature)
	}
	// Only NOW is the intent done — because the whole record landed.
	if got := intentStatus(t, il, "p1", "rev-1"); got != plan.IntentDone {
		t.Errorf("intent status = %q, want %q", got, plan.IntentDone)
	}
}

// TestIntentReplay_IncompleteApplyIsNotMarkedDone pins the other half of the
// contract: when the replay CANNOT complete the record, the intent must stay
// `committed` so a later boot finishes it. An edge whose blocker no longer
// exists (removed between commit and boot) is the reviewer's own scenario.
//
// The failure mode this replaces is the dangerous one: silently skipping the
// unwireable edge, returning nil, and stamping the durable log with "applied"
// over a plan whose tail has no ordering at all.
func TestIntentReplay_IncompleteApplyIsNotMarkedDone(t *testing.T) {
	h := newTestPlanEngine(t)
	il := withIntentLog(t, h)
	parkedPlanForCorrection(t, h, "p1")

	commitIntent(t, il, plan.IntentRecord{
		IntentID: "rev-broken", PlanID: "p1",
		Members: []task.Task{tailMember("m1", "p1")},
		// m-ghost was removed between commit and boot: AddDependency cannot
		// wire this edge.
		Edges: []plan.IntentEdge{{FromTaskID: "m-ghost", ToTaskID: "m1"}},
		Revision: plan.RevisionEntry{
			RevisionID: "rev-broken", PlanID: "p1", Verb: plan.RevisionAppend,
			TailAdds: []string{"m1"}, CreatedAt: time.Now().UTC(),
		},
		Patch: plan.IntentRecordPatch{
			ClearLastUnmetTerminalSignature: true, PlanPhase: plan.PhaseDispatching,
		},
		CreatedAt: time.Now().UTC(),
	})

	h.pe.replayIntentLogs()

	if got := intentStatus(t, il, "p1", "rev-broken"); got != plan.IntentCommitted {
		t.Fatalf("intent status = %q, want %q — an intent the replay could not fully apply must stay "+
			"replayable, never be recorded as applied", got, plan.IntentCommitted)
	}
	// And the plan must not have been advanced as though the correction landed.
	p, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if p.EffectivePlanPhase() == plan.PhaseDispatching {
		t.Errorf("plan_phase = dispatching after a FAILED replay: the plan was returned to work with an " +
			"unordered tail")
	}
}

// TestIntentReplay_TargetedRetryResetsTheMember pins the second write the
// replay used to drop. A targeted_retry's whole payload IS the member reset —
// it adds no members and wires no edges — so a replay that only patched the
// phase produced "the plan is dispatching again" over a member still sitting
// at `failed`, with nothing dispatchable at all.
func TestIntentReplay_TargetedRetryResetsTheMember(t *testing.T) {
	h := newTestPlanEngine(t)
	il := withIntentLog(t, h)
	parkedPlanForCorrection(t, h, "p1")
	mustCreateTask(t, h.tasks, &task.Task{
		ID: "m-failed", Title: "transient failure", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusFailed, Result: "flaky network",
	})

	commitIntent(t, il, plan.IntentRecord{
		IntentID: "rev-retry", PlanID: "p1",
		Revision: plan.RevisionEntry{
			RevisionID: "rev-retry", PlanID: "p1", Verb: plan.RevisionTargetedRetry,
			RetriedMemberID: "m-failed", CreatedAt: time.Now().UTC(),
		},
		Patch: plan.IntentRecordPatch{
			ClearLastUnmetTerminalSignature: true, PlanPhase: plan.PhaseDispatching,
		},
		CreatedAt: time.Now().UTC(),
	})

	h.pe.replayIntentLogs()

	m, err := h.tasks.Get("m-failed")
	if err != nil {
		t.Fatalf("get retried member: %v", err)
	}
	if m.Status != task.StatusNext {
		t.Fatalf("retried member is %q after replay, want next — the plan was returned to dispatching with "+
			"nothing to dispatch", m.Status)
	}
	if m.AttemptCount != 0 || m.Result != "" {
		t.Errorf("retried member kept its previous attempt: attempts=%d result=%q", m.AttemptCount, m.Result)
	}
}

// TestIntentReplay_AbandonTerminatesThePlan pins the third dropped write.
// `abandon` (ADR-055/FR-046b) is the adjudicated honest exit: the record says
// the Definition of Done is unreachable and the plan is over. Replaying it as
// a no-op left a plan its own durable record called abandoned sitting parked
// at awaiting_supervision — waiting for an adjudicator that had already given
// its verdict — with the intent marked done so nothing would ever revisit it.
func TestIntentReplay_AbandonTerminatesThePlan(t *testing.T) {
	h := newTestPlanEngine(t)
	il := withIntentLog(t, h)
	parkedPlanForCorrection(t, h, "p1")

	commitIntent(t, il, plan.IntentRecord{
		IntentID: "rev-abandon", PlanID: "p1",
		Revision: plan.RevisionEntry{
			RevisionID: "rev-abandon", PlanID: "p1", Verb: plan.RevisionAbandon,
			FalsifiedAssumption: "the upstream API exposes a bulk endpoint",
			Reason:              "no correction reaches this DoD from here",
			CreatedAt:           time.Now().UTC(),
		},
		// abandon carries no phase patch at all — it does not return the plan
		// to work.
		CreatedAt: time.Now().UTC(),
	})

	h.pe.replayIntentLogs()

	p, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan after replay: %v", err)
	}
	if p.State != plan.StateFailed {
		t.Fatalf("plan is %q/%q after replaying a committed abandon, want failed — the record says it was "+
			"abandoned, the plan says it is still waiting", p.State, p.EffectivePlanPhase())
	}
	if p.FailedReason != plan.FailedReasonDoDUnreachable {
		t.Errorf("failed_reason = %q, want %q", p.FailedReason, plan.FailedReasonDoDUnreachable)
	}
	if p.HandoverText == "" {
		t.Error("abandoned plan has no handover; the falsified assumption never reached the record")
	}
	if got := intentStatus(t, il, "p1", "rev-abandon"); got != plan.IntentDone {
		t.Errorf("intent status = %q, want %q once the plan is genuinely terminated", got, plan.IntentDone)
	}
}

// TestIntentReplay_AbandonLeavesATerminalPlanAlone is the idempotency guard on
// the abandon path: `failed -> failed` is a legal no-op in the plan matrix, so
// a re-run would happily rewrite a plan that reached `failed` some other way
// (a user Stop) into dod_unreachable. A terminal plan is never touched.
func TestIntentReplay_AbandonLeavesATerminalPlanAlone(t *testing.T) {
	h := newTestPlanEngine(t)
	il := withIntentLog(t, h)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Already stopped", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateFailed, FailedReason: plan.FailedReasonStoppedByUser,
		HandoverText: "stopped by the user",
	})

	commitIntent(t, il, plan.IntentRecord{
		IntentID: "rev-abandon", PlanID: "p1",
		Revision: plan.RevisionEntry{
			RevisionID: "rev-abandon", PlanID: "p1", Verb: plan.RevisionAbandon,
			FalsifiedAssumption: "an assumption", CreatedAt: time.Now().UTC(),
		},
		CreatedAt: time.Now().UTC(),
	})

	h.pe.replayIntentLogs()

	p, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if p.FailedReason != plan.FailedReasonStoppedByUser {
		t.Errorf("failed_reason = %q, want the original %q left untouched", p.FailedReason, plan.FailedReasonStoppedByUser)
	}
	if p.HandoverText != "stopped by the user" {
		t.Errorf("handover_text = %q, want the original terminal record preserved", p.HandoverText)
	}
	if got := intentStatus(t, il, "p1", "rev-abandon"); got != plan.IntentDone {
		t.Errorf("intent status = %q, want %q (nothing left to do)", got, plan.IntentDone)
	}
}

// TestIntentReplay_DisarmsTheSupervisionWake covers the bookkeeping the live
// path performs in the same locked body as the commit
// (countCorrectionAndClearWake). Without it, a replayed plan resumes
// dispatching carrying the corrected park's wake receipt and attempt count —
// which pre-spends the FR-022 ladder for the NEXT park and, because the park
// boundary is read off wake_at, hands that next park the corrected park's
// adjudication session.
func TestIntentReplay_DisarmsTheSupervisionWake(t *testing.T) {
	h := newTestPlanEngine(t)
	il := withIntentLog(t, h)
	parkedPlanForCorrection(t, h, "p1")

	commitIntent(t, il, plan.IntentRecord{
		IntentID: "rev-1", PlanID: "p1",
		Members: []task.Task{tailMember("m1", "p1")},
		Revision: plan.RevisionEntry{
			RevisionID: "rev-1", PlanID: "p1", Verb: plan.RevisionAppend,
			TailAdds: []string{"m1"}, CreatedAt: time.Now().UTC(),
		},
		Patch: plan.IntentRecordPatch{
			ClearLastUnmetTerminalSignature: true, PlanPhase: plan.PhaseDispatching,
		},
		CreatedAt: time.Now().UTC(),
	})

	h.pe.replayIntentLogs()

	p, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if p.Supervision == nil {
		t.Fatal("supervision object dropped by the replay")
	}
	if p.Supervision.WakeAt != "" {
		t.Errorf("supervision.wake_at = %q, want cleared — the plan has LEFT the supervision-eligible phase set",
			p.Supervision.WakeAt)
	}
	if p.Supervision.WakeError != "" {
		t.Errorf("supervision.wake_error = %q, want cleared", p.Supervision.WakeError)
	}
	if p.Supervision.Attempts != 0 {
		t.Errorf("supervision.attempts = %d, want 0 — the next park must get a full ladder", p.Supervision.Attempts)
	}
	// correction_rounds and session_id survive, per Supervision's per-field
	// lifetime rule. correction_rounds is NOT incremented by the replay: an
	// increment is not idempotent and this path has no per-revision marker to
	// make it one.
	if p.Supervision.CorrectionRounds != 1 {
		t.Errorf("supervision.correction_rounds = %d, want the on-record 1 (cumulative, never reset, and "+
			"never non-idempotently incremented by a replay)", p.Supervision.CorrectionRounds)
	}
	if p.Supervision.SessionID != "sess-adjudication" {
		t.Errorf("supervision.session_id = %q, want it preserved", p.Supervision.SessionID)
	}
}

// TestIntentReplay_UncommittedIntentIsNotApplied is the negative control: the
// widened apply must still run ONLY for committed-not-done intents. An
// uncommitted intent is discarded — no members, no edges, no plan write —
// which is the "exact pre-append DAG" guarantee (INV-6).
func TestIntentReplay_UncommittedIntentIsNotApplied(t *testing.T) {
	h := newTestPlanEngine(t)
	il := withIntentLog(t, h)
	parkedPlanForCorrection(t, h, "p1")

	if err := il.AppendIntent(plan.IntentRecord{
		IntentID: "rev-uncommitted", PlanID: "p1",
		Members: []task.Task{tailMember("m1", "p1"), tailMember("m2", "p1")},
		Edges:   []plan.IntentEdge{{FromTaskID: "m1", ToTaskID: "m2"}},
		Revision: plan.RevisionEntry{
			RevisionID: "rev-uncommitted", PlanID: "p1", Verb: plan.RevisionAbandon,
			FalsifiedAssumption: "never committed", CreatedAt: time.Now().UTC(),
		},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append intent: %v", err)
	}

	h.pe.replayIntentLogs()

	if _, err := h.tasks.Get("m1"); err == nil {
		t.Error("an uncommitted intent's members were created; the pre-append DAG was not preserved")
	}
	p, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if p.State != plan.StateRunning || p.EffectivePlanPhase() != plan.PhaseAwaitingSupervision {
		t.Errorf("an uncommitted abandon changed the plan: state=%q phase=%q",
			p.State, p.EffectivePlanPhase())
	}
	if got := intentStatus(t, il, "p1", "rev-uncommitted"); got != plan.IntentUncommitted {
		t.Errorf("intent status = %q, want %q", got, plan.IntentUncommitted)
	}
}
