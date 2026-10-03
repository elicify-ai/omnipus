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

// TestComplete_StopThatCausedThisCompletionLandsStopped proves the OTHER
// half of the Stop rule under the corrected sub-agent control-plane ADR
// (supersedes this test's former name ...LandsTerminal and its terminal
// oracle; changed-test list entry, justification: ADR D6/F0929-7 — stopped
// is deliberately NON-terminal and resumable, and the parent decides from
// the D6 notice instead of a blocking frontier row).
//
// When a stop stops a session that HAS a live turn, the turn unwinds with
// context.Canceled and the completion path is the last writer that could
// overwrite the landed stop. The stop must survive it as `stopped` with the
// stamped stop_note intact, and the parent must receive exactly one D6
// stopped-child notice — never a legacy terminal/fatal completion, and no
// second wake across retries.
//
// The distinction is the PRE-DELIVERY snapshot: a note already present
// before this completion is the reason for it, not a race against it. The
// sibling test above pins the race half; deleting either one leaves the
// rule half-enforced.
func TestComplete_StopThatCausedThisCompletionLandsStopped(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-stop-cause")
	wakeCount := observeU1ParentNoticeWakes(t, al, parentID)
	rec := launchRunningChild(t, al, parentID, "call-complete-stop-cause")

	lifecycle := al.GetSessionLifecycleStore()
	if err := lifecycle.Mutate(rec.SessionID, func(r *session.LifecycleRecord) error {
		r.SteeredBy.ReportingTarget = session.ReportingTarget{Channel: "webchat", ChatID: parentID}
		return nil
	}); err != nil {
		t.Fatalf("Mutate(reporting target): %v", err)
	}

	// The stop stamps the in-flight fence AND the lasting stop_note in one
	// mutation, then cancels the live turn; the record stays `running`
	// until the turn unwinds (steer_cancel.go::stampStop). Reload so the
	// snapshot the completion works from carries both, exactly as in
	// production.
	canceller := NewSteerCanceller(lifecycle)
	if _, err := canceller.CancelSubtree(context.Background(), rec.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}
	stamped, err := lifecycle.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load after cancel: %v", err)
	}
	if stamped.Terminal() {
		t.Fatalf("premise: want a live-turn child still non-terminal after the stamp (the turn unwinds into the completion), got state=%q", stamped.State)
	}
	if stamped.Stop == nil || stamped.Stop.Generation != stamped.Generation {
		t.Fatalf("premise: want a live current-generation Stop marker before completion, got %+v", stamped.Stop)
	}
	// This child is the call's own DIRECT target, so its note cause is
	// `stop`; `cascade` is reserved for swept descendants (steer_cancel.go).
	if stamped.StopNote == nil || stamped.StopNote.Cause != session.StopCauseStop {
		t.Fatalf("premise: stop_note after the stamp = %+v, want cause %q (D2: stampStop writes fence and note in one mutation)", stamped.StopNote, session.StopCauseStop)
	}

	// The turn unwinds with context.Canceled, as a cancelled live turn does.
	if completeErr := al.completeSteeredTurn(context.Background(), stamped,
		turnResult{finalContent: ""}, context.Canceled); completeErr != nil {
		t.Fatalf("completeSteeredTurn(stamped live turn): %v", completeErr)
	}

	// D6: the completion caused by the stop lands `stopped` — deliberately
	// NON-terminal and resumable — at the same generation, with the spent
	// fence cleared and the lasting note retained (D2).
	got, err := lifecycle.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if got.State != session.LifecycleStopped || got.Terminal() {
		t.Fatalf("a stop-caused completion left the child state=%q terminal=%v, want stopped non-terminal (D6/F0929-7: stopped is resumable, never final)", got.State, got.Terminal())
	}
	if got.Generation != stamped.Generation {
		t.Errorf("generation moved %d -> %d on the late completion write, want unchanged (D6: stop keeps the generation)", stamped.Generation, got.Generation)
	}
	if got.Stop != nil && got.Stop.Generation == got.Generation {
		t.Errorf("the spent Stop marker survived the landing (state=%q) — persistLocked rejects that shape, so the write could not have landed", got.State)
	}
	if got.StopNote == nil || got.StopNote.Cause != session.StopCauseStop {
		t.Fatalf("stop_note after the landing = %+v, want the retained stop note (D2: landing clears the fence, retains the note)", got.StopNote)
	}

	// D6: exactly one deduplicated stopped-child notice reaches the direct
	// parent — carrying cause/actor, the RESUME/redirect/decide actions and
	// the owner-first advice for a human stop — with exactly one wake, and
	// no legacy terminal/fatal completion beside it. The helper also errors
	// on any legacy terminal/fatal message this child produced (D2 CRIT-001:
	// a Stop-fenced completion publishes no final).
	noticeID, _ := assertU1StoppedChildNotice(t, al, parentID, rec, string(session.StopCauseStop), "dan")
	if got := wakeCount(noticeID); got != 1 {
		t.Errorf("working-parent wakes for the stopped-child notice = %d, want exactly 1 (D6)", got)
	}
}
