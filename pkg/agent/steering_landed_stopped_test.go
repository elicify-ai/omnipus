// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for the #890 follow-up — call sites #4 and #5 of 6, both in
// pkg/agent/steering.go:
//
//   - reviveInactiveInbound, line ~347: `if !rec.Terminal() && !rec.Stopped()
//     { return false, nil }`
//   - ReviveStoppedSession, line ~422: the same `if !rec.Terminal() &&
//     !rec.Stopped() { return false, nil }` decline branch.
//
// LifecycleRecord.Stopped() (pkg/session/lifecycle_edge.go) is defined
// today as ONLY the live, current-generation Stop fence. It does not
// recognize a record that has already LANDED session.LifecycleStopped with
// its fence cleared — the shape lifecycle_bridge.go::TransitionSession
// leaves once a stop lands. ADR-20260928-sub-agent-control-plane.md line
// ~636 documents the intended fix: "Stopped() checks landed state OR
// current fence".
//
// For a landed-and-cleared record, both call sites' guards evaluate
// `!false && !false` = true, i.e. the "decline, nothing to revive" branch —
// silently discarding a revival that should have proceeded. This is the
// exact "silent no-op" failure mode the dispatch brief names.
//
// Production code is untouched here — RED only (elicify-test-writing skill,
// qa-lead RED duty).

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestReviveStoppedSession_LandedAndClearedRecordRevives is RED for call
// site #5, steering.go::ReviveStoppedSession. Oracle: the function's own doc
// comment — "a Stop survives a restart; only a newer instruction revives
// the session, as a new generation" — and ADR-20260928-sub-agent-control-
// plane.md line ~636's intended Stopped() redefinition. A session that has
// already landed stopped (its cancel/timeout unwind finished) is exactly
// the durably-stopped case this function exists to revive; the oracle says
// revived must be true.
func TestReviveStoppedSession_LandedAndClearedRecordRevives(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	steererID := newTestSteeringSession(t, al, adr093Workspace)
	// The steerer needs its OWN root lifecycle record (SteeredBy nil) too:
	// SteerRecordClassifier.chainValid (steer_classify.go) walks from the
	// child's SteeredBy.SteeringSessionID and requires that ancestor's
	// lifecycle record to resolve to a genuine root before it will class the
	// child steer.ClassSteered (Runnable) rather than invalid_edge —
	// newTestSteeringSession mints only UnifiedMeta, no lifecycle record.
	adr093Persist(t, al, adr093Record(steererID, 1, session.LifecycleRunning))

	// A real chat session (backing UnifiedStore meta + transcript), not a bare
	// lifecycle-only id: appendSteeredInstruction (called inside
	// ReviveStoppedSession before the revive itself lands) needs somewhere to
	// land the new instruction — exactly the "ghost" session
	// TestReviveStoppedSession_RefusesWhenTheNewInstructionCannotLand (steering_test.go)
	// deliberately tests as a REFUSAL case. newTestSteeringSession mints only
	// UnifiedMeta/transcript, no lifecycle record, so overwriting its
	// lifecycle record below with the landed-and-cleared stopped shape is
	// safe.
	childID := newTestSteeringSession(t, al, adr093Workspace)
	// SteerRecordClassifier.Classify's R02 check requires meta.ParentSessionID
	// to agree with the child's own SteeredBy.SteeringSessionID below —
	// newTestSteeringSession does not set it (it mints a plain standalone
	// session), so stamp it explicitly.
	parentID := steererID
	if err := al.GetSessionStore().SetMeta(childID, session.MetaPatch{ParentSessionID: &parentID}); err != nil {
		t.Fatalf("SetMeta(child).ParentSessionID: %v", err)
	}
	childRec := &session.LifecycleRecord{
		SessionID:      childID,
		Generation:     1,
		State:          session.LifecycleStopped,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: steererID, RootSessionID: steererID},
		WorkspaceID:    adr093Workspace,
		AgentID:        testDefaultAgentID,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-landed-revive"},
		StopNote: &session.StopNote{
			At:    time.Now(),
			By:    "human:tester",
			Seq:   1,
			Cause: session.StopCauseTimeout,
		},
		// Stop deliberately nil — landed and cleared, not the live fence.
	}
	adr093Persist(t, al, childRec)

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}
	revived, err := al.ReviveStoppedSession(context.Background(), childID, by, "resume, please")
	if err != nil {
		t.Fatalf("ReviveStoppedSession(%s) returned an error for a landed-and-cleared stopped record: %v", childID, err)
	}
	if !revived {
		t.Fatalf("ReviveStoppedSession(%s) = (false, nil), want (true, nil) — the record has landed state=%q with a persisted stop_note and its current-generation "+
			"fence already cleared; ReviveStoppedSession's own doc comment says a durably-stopped session is exactly what it exists to revive, but "+
			"rec.Terminal() is false (LifecycleStopped is non-terminal) and rec.Stopped() is ALSO currently false (no live fence), so the `!rec.Terminal() && "+
			"!rec.Stopped()` decline branch silently no-ops the revive instead (ADR-20260928-sub-agent-control-plane.md line ~636: Stopped() must check "+
			"landed state OR current fence)", childID, session.LifecycleStopped)
	}
}

// TestReviveInactiveInbound_LandedAndClearedRecordIsRevived is RED for call
// site #4, steering.go::reviveInactiveInbound, exercised through its real
// caller enqueueSteeringFromMessage (the production entry point) rather
// than calling the unexported method directly with a hand-built route.
// Oracle: ADR-093 D4 ("a message arriving [after the stop lands] would
// otherwise be enqueued into a steering queue whose consumer is about to
// disappear for good ... only a newer instruction revives it") plus ADR-
// 20260928-sub-agent-control-plane.md line ~636. A landed-and-cleared
// record is the settled end state of exactly that scenario, so a fresh
// human message must revive it (next generation via resumed_from, running)
// rather than silently enqueue into a queue nothing will ever drain.
func TestReviveInactiveInbound_LandedAndClearedRecordIsRevived(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)

	previous := g2StoppedOrdinaryRoot(t, al, parentID)
	provider, _ := installParkedProvider(t, al)

	// Frozen control-plane ADR D2 CRIT-001: "resumes a stopped child on the
	// same generation ... only done/failed mints a next generation".
	// Admission is asynchronous: hold the real model boundary, then inspect
	// the actual running execution rather than race its queued transition.
	if err := al.enqueueSteeringFromMessage(adr093HumanMessage("Right, carry on with the plan.", parentID)); err != nil {
		t.Fatalf("enqueueSteeringFromMessage: %v", err)
	}

	adr093WaitForEntered(t, provider, 30*time.Second)
	got := adr093Load(t, al, parentID)
	if got.Generation != previous.Generation || got.State != session.LifecycleRunning {
		t.Fatalf("record after a human message into a landed-and-cleared stopped record = generation %d state %q, want the stopped generation %d running (D2/C1)",
			got.Generation, got.State, previous.Generation)
	}
	if got.ExecutionID == nil || previous.ExecutionID == nil || got.ExecutionID.RunID == previous.ExecutionID.RunID || got.ExecutionID.BootSeq != al.bootEpochFor() {
		t.Fatalf("stopped ordinary root must resume with a fresh execution in this boot: stopped=%+v resumed=%+v", previous.ExecutionID, got.ExecutionID)
	}
	if got.Stop != nil || got.StopNote != nil {
		t.Fatalf("resume did not atomically clear the spent fence/note: fence=%+v note=%+v", got.Stop, got.StopNote)
	}
}
