// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// Issue #947: the plan-engine boot sweep cannot terminalise a steered record
// that carries a CURRENT-generation Stop marker. sweepToFailedInterrupted
// copies the record wholesale (including Stop), sets failed(interrupted) and
// persists — and LifecycleStore.persistLocked rejects a TERMINAL record that
// carries a current-generation stop marker, so the sweep warn-logs and
// continues, leaving the record non-terminal forever. Every boot re-fails the
// same way (observed ~15 consecutive boots on the founder instance:
// "boot sweep: could not sweep session to failed(interrupted) … terminal
// record (state \"failed\") cannot carry a current-generation stop marker").
//
// The sibling sweep, SteerBootRecovery.failInterrupted (same file), guards the
// same case correctly — this test pins the plan-engine sweep to the same
// behaviour. The invariant being restored: a boot sweep that decides a record
// must become failed(interrupted) must actually land that write; the stop
// marker's lifecycle rule (spent when the terminal state it caused lands) is
// the sweep's job to honour, not an excuse to strand the record.
//
// RED under current code: the record stays running and is missing from
// res.SweptToFailed. GREEN after the fix: swept to failed(interrupted).
func TestBootSweep_SweepsRecordCarryingCurrentGenStopMarker(t *testing.T) {
	h := newBootSweepHarness(t)

	// A steered worker stranded mid-flight by a crash, with a Stop marker
	// stamped at the CURRENT generation — the shape the 09-26/27 incident
	// produced (stampStop on a queued/running child, then process death
	// before any terminal write spent the marker).
	now := time.Now().UTC()
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-947-stop", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		Stop: &session.Stop{
			At:         now.Add(-time.Minute),
			Generation: 1, // == Generation: LIVE stop marker
			By:         session.Principal{Kind: session.PrincipalKindHuman, ID: "admin"},
		},
		CreatedAt: now.Add(-2 * time.Hour),
	})

	res := h.pe.runBootSweep(context.Background())

	if res.Scanned != 1 {
		t.Errorf("Scanned = %d, want 1", res.Scanned)
	}
	if len(res.SweptToFailed) != 1 {
		t.Fatalf("SweptToFailed = %v, want [sess-947-stop]: the sweep must terminalise a record carrying a current-gen stop marker (issue #947)", res.SweptToFailed)
	}

	rec, err := h.ls.Load("sess-947-stop")
	if err != nil {
		t.Fatalf("load swept record: %v", err)
	}
	if rec.State != session.LifecycleFailed {
		t.Errorf("state = %q, want failed", rec.State)
	}
	if rec.FailedReason != failedReasonInterrupted {
		t.Errorf("failed_reason = %q, want %q", rec.FailedReason, failedReasonInterrupted)
	}
	// The stop marker must not survive as a live current-generation marker on
	// a terminal record (persistLocked's own rule) — spent, not stranded.
	if rec.Stop != nil && rec.Stop.Generation == rec.Generation {
		t.Errorf("swept record still carries a live current-gen stop marker (gen %d); it must be spent when the terminal state lands", rec.Stop.Generation)
	}
	// Same generation (no follow_up mint on a sweep) — matches the sibling
	// test TestBootSweep_NonTerminalToFailedInterrupted.
	if rec.Generation != 1 {
		t.Errorf("generation = %d, want 1 (a sweep never mints)", rec.Generation)
	}
}
