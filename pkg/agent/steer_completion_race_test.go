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

// TestComplete_StopThatCausedThisCompletionLandsTerminal proves the OTHER
// half of the Stop rule, and the half that used to hang forever.
//
// When the cascade stops a session that HAS a live turn, it stamps the Stop
// marker and cancels the turn's context. The turn unwinds with
// context.Canceled and completeSteeredTurn is the only writer that can land
// its terminal state. Before the fix, the blanket refusal
// "cur.Stop != nil && cur.Stop.Generation == cur.Generation" fired on the
// marker the cascade had JUST stamped, so the write was refused and treated
// as a legitimate no-op: the record stayed `running` with its marker
// forever, and hasRunningOrQueuedDescendant kept the parent waiting.
//
// That is the most common Stop path -- a session that was actually working --
// and it is the exact hang ADR-091 exists to remove. The never-ran path
// (terminaliseNeverRanStop) was the only one that terminalised correctly.
//
// The distinction is the PRE-DELIVERY snapshot: a marker already present
// before Deliver is the reason for this completion, not a race against it.
// The sibling test above pins the race half; deleting either one leaves the
// rule half-enforced.
func TestComplete_StopThatCausedThisCompletionLandsTerminal(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-stop-cause")
	rec := launchRunningChild(t, al, parentID, "call-complete-stop-cause")

	lifecycle := al.GetSessionLifecycleStore()
	if err := lifecycle.Mutate(rec.SessionID, func(r *session.LifecycleRecord) error {
		r.SteeredBy.ReportingTarget = session.ReportingTarget{Channel: "webchat", ChatID: parentID}
		return nil
	}); err != nil {
		t.Fatalf("Mutate(reporting target): %v", err)
	}

	// The cascade stamps the marker, then cancels the turn. Reload so the
	// snapshot completeSteeredTurn works from carries the marker, exactly
	// as it does in production.
	canceller := NewSteerCanceller(lifecycle)
	if _, err := canceller.CancelSubtree(context.Background(), rec.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}
	stopped, err := lifecycle.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load after cancel: %v", err)
	}
	if stopped.Terminal() {
		t.Skip("the cascade already terminalised this child; the live-turn path is what this test covers")
	}
	if stopped.Stop == nil || stopped.Stop.Generation != stopped.Generation {
		t.Fatalf("premise failed: want a live current-generation Stop marker before completion, got %+v", stopped.Stop)
	}

	// The turn unwinds with context.Canceled, as a cancelled live turn does.
	if completeErr := al.completeSteeredTurn(context.Background(), stopped,
		turnResult{finalContent: ""}, context.Canceled); completeErr != nil {
		t.Fatalf("completeSteeredTurn(stopped live turn): %v", completeErr)
	}

	got, err := lifecycle.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if !got.Terminal() {
		t.Fatalf("a stopped session with a live turn was left NON-terminal (state=%q) — "+
			"its parent's hasRunningOrQueuedDescendant will wait for it forever, "+
			"which is the hang ADR-091 removes", got.State)
	}
	if got.Stop != nil && got.Stop.Generation == got.Generation {
		t.Fatalf("the spent Stop marker survived onto a terminal record (state=%q, stop.gen=%d) — "+
			"persistLocked rejects that shape, so the write could not have landed",
			got.State, got.Stop.Generation)
	}
}
