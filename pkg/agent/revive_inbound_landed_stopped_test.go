// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for the #890 follow-up (call site #3 of 6):
// pkg/agent/revive_inbound.go::(*AgentLoop).inboundRevivable, line ~65:
//
//	return rec.Terminal() || rec.Stopped()
//
// LifecycleRecord.Stopped() (pkg/session/lifecycle_edge.go) is defined
// today as ONLY the live, current-generation Stop fence. It does not
// recognize a record that has already LANDED session.LifecycleStopped with
// its fence cleared — the shape lifecycle_bridge.go::TransitionSession
// leaves once a stop lands ("landing clears the fence but keeps the note").
// ADR-20260928-sub-agent-control-plane.md line ~636 documents the intended
// fix: "Stopped() checks landed state OR current fence".
//
// inboundRevivable's own doc comment says it reports whether a record is
// "terminal or durably stopped at its current generation — the two states
// ADR-093 D4 lets an ordinary human message revive." A record that has
// landed LifecycleStopped IS durably stopped — more durably than the live
// fence case, since the stop has already been carried out — so the oracle
// here (ADR-093 D4's own text plus the ADR line ~636 fix) says
// inboundRevivable must return true. Today it returns false, because
// neither Terminal() nor the unwidened Stopped() recognizes this shape.
//
// Production code is untouched here — RED only (elicify-test-writing skill,
// qa-lead RED duty).

package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestInboundRevivable_LandedAndClearedStoppedRecordIsRevivable is RED for
// revive_inbound.go::inboundRevivable. Oracle: ADR-093 D4's own text
// ("terminal or durably stopped ... the two states ... lets an ordinary
// human message revive") plus ADR-20260928-sub-agent-control-plane.md line
// ~636's intended Stopped() redefinition — never the current implementation.
func TestInboundRevivable_LandedAndClearedStoppedRecordIsRevivable(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	id := newTestSteeringSession(t, al, adr093Workspace)

	rec := adr093Record(id, 1, session.LifecycleStopped)
	rec.StopNote = &session.StopNote{
		At:    time.Now(),
		By:    "human:tester",
		Seq:   1,
		Cause: session.StopCauseStop,
	}
	// rec.Stop is deliberately left nil: this is the LANDED-and-CLEARED
	// shape (TransitionSession clears the current-generation fence the
	// moment it lands LifecycleStopped), not the live in-flight fence.
	adr093Persist(t, al, rec)

	if got := al.inboundRevivable(id); !got {
		t.Fatalf("inboundRevivable(%s) = %v, want true — the record has landed state=%q with a persisted stop_note and its current-generation fence already cleared; "+
			"ADR-093 D4 calls this \"durably stopped\" and inboundRevivable's own doc comment says exactly that state is revivable, but "+
			"rec.Terminal() is false (LifecycleStopped is deliberately non-terminal) and rec.Stopped() is ALSO currently false (no live fence), "+
			"so a human message into this session cannot revive it until Stopped() recognizes landed state too (ADR-20260928-sub-agent-control-plane.md line ~636)",
			id, got, session.LifecycleStopped)
	}
}
