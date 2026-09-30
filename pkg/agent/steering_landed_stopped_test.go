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

	childID := "steered-child-landed-stopped"
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

	rec := adr093Record(parentID, 1, session.LifecycleStopped)
	rec.StopNote = &session.StopNote{
		At:    time.Now(),
		By:    "human:tester",
		Seq:   1,
		Cause: session.StopCauseStop,
	}
	// rec.Stop deliberately nil — landed and cleared.
	adr093Persist(t, al, rec)

	// handled==false today means the message falls through to the plain
	// enqueue path (no active turn to wait on, so this call returns
	// promptly without needing a parked provider or a background
	// goroutine): the bug this test targets is a synchronous decision, not
	// a dispatch race.
	if err := al.enqueueSteeringFromMessage(adr093HumanMessage("Right, carry on with the plan.", parentID)); err != nil {
		t.Fatalf("enqueueSteeringFromMessage: %v", err)
	}

	got := adr093Load(t, al, parentID)
	if got.Generation != 2 || got.State != session.LifecycleRunning {
		t.Fatalf("record after a human message into a landed-and-cleared stopped record = generation %d state %q, want generation 2 running — "+
			"ADR-093 D4: a human message into a durably-stopped session revives it (next generation via resumed_from), but reviveInactiveInbound's "+
			"`!rec.Terminal() && !rec.Stopped()` gate (steering.go:347) does not recognize state=%q with the fence already cleared as stopped, so "+
			"handled comes back false and the message was silently enqueued into a steering queue no live turn will ever drain "+
			"(ADR-20260928-sub-agent-control-plane.md line ~636: Stopped() must check landed state OR current fence)",
			got.Generation, got.State, session.LifecycleStopped)
	}
}
