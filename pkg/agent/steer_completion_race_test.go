// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 fix lane 1, Finding D (HIGH, three reviewers found this
// independently): completeSteeredTurn used to be a raw Load -> Deliver
// (I/O) -> mutate the PRE-Deliver snapshot in memory -> Persist — the exact
// stale-write-back shape already fixed on the dispatch path
// (steer_dispatch_race_test.go), with a WIDER window because Deliver does
// real I/O (an inbox append, transcript writes, a parent wake). These two
// tests mirror that file's own pair exactly, injecting the race at
// completeStateWriteTestHook — fired immediately before completeSteeredTurn's
// terminal-state write, after Deliver has already run — instead of
// dispatchStateWriteTestHook.
package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestComplete_StopLandingDuringDeliverySurvivesAndIsNotOverwritten proves a
// Stop pressed while Deliver is still running is NOT erased by
// completeSteeredTurn's write-back: the durable record that Stop was ever
// pressed must survive, and the session must not be silently marked
// completed/failed over it.
func TestComplete_StopLandingDuringDeliverySurvivesAndIsNotOverwritten(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	rec := launchRunningChild(t, al, parentID, "call-complete-stop-race")
	// A bare test parent's PeerID is empty; give the child's reporting
	// target a routable address so Deliver's wake attempt actually runs
	// (matching the shape of the real race window, not skip it early).
	if err := al.GetSessionLifecycleStore().Mutate(rec.SessionID, func(r *session.LifecycleRecord) error {
		r.SteeredBy.ReportingTarget = session.ReportingTarget{Channel: "webchat", ChatID: parentID}
		return nil
	}); err != nil {
		t.Fatalf("Mutate(reporting target): %v", err)
	}

	canceller := NewSteerCanceller(al.GetSessionLifecycleStore())
	var once sync.Once
	completeStateWriteTestHook = func(hookSessionID string) {
		if hookSessionID != rec.SessionID {
			return
		}
		once.Do(func() {
			if _, err := canceller.CancelSubtree(context.Background(), rec.SessionID,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
				t.Errorf("CancelSubtree inside the delivery window: %v", err)
			}
		})
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	if err := al.completeSteeredTurn(context.Background(), rec, turnResult{finalContent: "finished before the Stop landed"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn(Stop mid-window): %v", err)
	}

	got, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if got.Stop == nil {
		t.Fatalf("the Stop marker was erased from disk by completeSteeredTurn's write-back — "+
			"after this there is no record that Stop was ever pressed (state=%q)", got.State)
	}
	if got.Stop.Generation != got.Generation {
		t.Errorf("Stop.Generation = %d, want %d (the record's current generation)", got.Stop.Generation, got.Generation)
	}
	if got.State == session.LifecycleCompleted || got.State == session.LifecycleFailed {
		t.Errorf("persisted State = %q, want the Stop-during-delivery race to refuse the write, not overwrite a stamped Stop with a terminal disposition", got.State)
	}
}

// TestComplete_ReviveLandingDuringDeliveryIsNotRolledBack proves the second
// outcome of the same read-modify-write: a Revive that mints a newer
// generation while Deliver is still running must not be undone by the stale
// write-back. A record's generation moving DOWN makes every later
// generation-carrying cancel miss.
func TestComplete_ReviveLandingDuringDeliveryIsNotRolledBack(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	rec := launchRunningChild(t, al, parentID, "call-complete-revive-race")
	if err := al.GetSessionLifecycleStore().Mutate(rec.SessionID, func(r *session.LifecycleRecord) error {
		r.SteeredBy.ReportingTarget = session.ReportingTarget{Channel: "webchat", ChatID: parentID}
		return nil
	}); err != nil {
		t.Fatalf("Mutate(reporting target): %v", err)
	}
	originalGen := rec.Generation

	canceller := NewSteerCanceller(al.GetSessionLifecycleStore())
	var once sync.Once
	completeStateWriteTestHook = func(hookSessionID string) {
		if hookSessionID != rec.SessionID {
			return
		}
		once.Do(func() {
			if _, err := canceller.CancelSubtree(context.Background(), rec.SessionID,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
				t.Errorf("CancelSubtree inside the delivery window: %v", err)
				return
			}
			if _, err := canceller.Revive(context.Background(), rec.SessionID,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
				t.Errorf("Revive inside the delivery window: %v", err)
			}
		})
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	if err := al.completeSteeredTurn(context.Background(), rec, turnResult{finalContent: "finished before the revival landed"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn(revived mid-window): %v", err)
	}

	got, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if got.Generation != originalGen+1 {
		t.Fatalf("record generation = %d, want %d — completeSteeredTurn's stale write-back rolled the revival backwards", got.Generation, originalGen+1)
	}
	if got.State != session.LifecycleRunning {
		t.Errorf("persisted State = %q, want running (Revive's own write) — a stale completion must not overwrite a live revival", got.State)
	}
}
