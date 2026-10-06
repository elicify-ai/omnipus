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

// TestBootSweep_SweepsRecordCarryingCurrentGenStopMarker keeps issue #947's
// ordinary-record control: the sweep spends its current fence and lands
// failed(interrupted). Frozen ADR-20260928 D2 CRIT-001/D8.2/D8.3 withdraw that
// outcome for a STEERED record. Its in-flight fence belongs to
// SteerBootRecovery, not to this second writer; every field/journal byte stays.
func TestBootSweep_SweepsRecordCarryingCurrentGenStopMarker(t *testing.T) {
	h := newBootSweepHarness(t)
	now := time.Now().UTC()
	for _, id := range []string{"sess-947-stop", "sess-947-ordinary-stop"} {
		rec := &session.LifecycleRecord{
			SessionID: id, Generation: 1, State: session.LifecycleRunning,
			WorkspaceID: "ws", AgentID: "agent-1",
			OwnerScopeKind: session.OwnerScopeHuman,
			Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task-947"},
			Stop: &session.Stop{
				At: now.Add(-time.Minute), Generation: 1,
				By: session.Principal{Kind: session.PrincipalKindHuman, ID: "admin"},
			},
			CreatedAt: now.Add(-2 * time.Hour),
		}
		if id == "sess-947-stop" {
			rec.Origin = &session.Origin{Kind: session.OriginKindDelegate}
			rec.SteeredBy = &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"}
		}
		persistLifecycle(t, h.ls, rec)
	}
	steeredBefore := snapshotBootSweepRecord(t, h.ls, "sess-947-stop")

	res := h.pe.runBootSweep(context.Background())
	assertBootSweepRecordUntouched(t, h.ls, "sess-947-stop", steeredBefore)
	if res.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2 (one ordinary, one steered)", res.Scanned)
	}
	if len(res.SweptToFailed) != 1 || res.SweptToFailed[0] != "sess-947-ordinary-stop" {
		t.Fatalf("SweptToFailed = %v, want [sess-947-ordinary-stop] only — D8.3 forbids sweeping the steered current-fence record", res.SweptToFailed)
	}

	rec, err := h.ls.Load("sess-947-ordinary-stop")
	if err != nil {
		t.Fatalf("load ordinary swept record: %v", err)
	}
	if rec.State != session.LifecycleFailed || rec.FailedReason != failedReasonInterrupted {
		t.Errorf("ordinary state/reason = %q/%q, want failed/%q (unchanged issue #947 control)", rec.State, rec.FailedReason, failedReasonInterrupted)
	}
	if rec.Stop != nil {
		t.Errorf("ordinary swept record retains Stop %+v, want nil (the current-generation fence is spent)", rec.Stop)
	}
	if rec.Generation != 1 {
		t.Errorf("ordinary generation = %d, want 1 (a sweep never mints)", rec.Generation)
	}
}
