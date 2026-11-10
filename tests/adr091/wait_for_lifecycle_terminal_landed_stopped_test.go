// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for the #890 follow-up — the test HELPER
// steered_sessions_test.go::waitForLifecycleTerminalOrDeadline (this
// package, ~line 898), which polls only rec.Terminal():
//
//	for {
//	    rec, err := store.Load(sessionID)
//	    ...
//	    if rec.Terminal() || time.Now().After(deadline) {
//	        return rec
//	    }
//	    time.Sleep(10 * time.Millisecond)
//	}
//
// session.LifecycleStopped is DELIBERATELY non-terminal (pkg/session/
// lifecycle.go's terminalLifecycleStates — see also this package's own
// assertStopLandedOn, which documents that "`stopped` replaced the retired
// terminal `cancelled` and is explicitly NON-terminal ... rec.Terminal()
// alone no longer detects this shape"). So for a record that has ALREADY
// landed stopped — with a persisted stop_note and its current-generation
// fence already cleared (the exact shape lifecycle_bridge.go::
// TransitionSession leaves once a stop lands) — rec.Terminal() is false on
// every single poll, and the loop burns its ENTIRE timeout budget before
// returning, even though the record settled before the loop ever started.
//
// ADR-20260928-sub-agent-control-plane.md line ~636 documents the intended
// fix ("Stopped() checks landed state OR current fence"); once applied,
// this helper's own stale Terminal()-only predicate needs the same
// widening (e.g. `rec.Terminal() || rec.Stopped()`) to stop wasting its
// budget on an already-settled landed-stopped record. This is a RED test
// only — no production or existing test-helper code is touched here
// (qa-lead RED duty; the helper itself may only be edited later, in
// GREEN).

package adr091_test

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestWaitForLifecycleTerminalOrDeadline_LandedStoppedReturnsPromptly is RED
// for waitForLifecycleTerminalOrDeadline's Terminal()-only predicate.
// Oracle: the helper's own doc comment ("Bounded ... a real unwind is
// expected to land many orders of magnitude inside this budget") plus
// assertStopLandedOn's own documented fact that `stopped` is a SETTLED,
// non-terminal shape — a record already in that shape needs no further
// polling at all. The expected bound (promptBound, well under the 10ms
// poll interval's own budget) is derived from that spec text, not from
// timing the current implementation.
func TestWaitForLifecycleTerminalOrDeadline_LandedStoppedReturnsPromptly(t *testing.T) {
	store := session.NewLifecycleStore(t.TempDir())
	const sessionID = "landed-stopped-already-settled"

	rec := &session.LifecycleRecord{
		SessionID:      sessionID,
		Generation:     1,
		State:          session.LifecycleStopped,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    "ws-wait-helper",
		AgentID:        "worker",
		StopNote: &session.StopNote{
			At:    time.Now(),
			By:    "human:tester",
			Seq:   1,
			Cause: session.StopCauseStop,
		},
		// Stop is deliberately nil: the current-generation fence has
		// already been cleared, exactly as TransitionSession leaves it
		// (lifecycle_bridge.go::TransitionSession) once a stop lands —
		// this record is settled BEFORE the wait even starts.
	}
	if err := store.Persist(rec); err != nil {
		t.Fatalf("persist landed-and-cleared stopped record: %v", err)
	}

	const budget = 2 * time.Second
	// Generous versus the loop's own 10ms poll interval — any value well
	// under the full budget proves the loop did not just spin to the
	// deadline.
	const promptBound = 300 * time.Millisecond

	start := time.Now()
	got := waitForLifecycleTerminalOrDeadline(t, store, sessionID, budget)
	elapsed := time.Since(start)

	if elapsed > promptBound {
		t.Fatalf("waitForLifecycleTerminalOrDeadline took %s for an ALREADY landed-and-cleared stopped record (full budget was %s) — it burned its whole timeout "+
			"instead of recognizing the record as already settled. state=%q is deliberately non-terminal (pkg/session/lifecycle.go's terminalLifecycleStates), "+
			"so the helper's `rec.Terminal()` poll condition never trips for a landed-stopped record and the loop spins to the deadline on every call "+
			"(ADR-20260928-sub-agent-control-plane.md line ~636: the same widening Stopped() needs — landed state OR current fence — applies here too)",
			elapsed, budget, session.LifecycleStopped)
	}
	if got.State != session.LifecycleStopped {
		t.Fatalf("returned record state = %q, want %q", got.State, session.LifecycleStopped)
	}
}
