// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W1 historical direct-parent notice RED pack, part 1 — ORDER.
// ADR-20260928 sub-agent control plane (frozen asset cd20cf8b):
//
//   - D2 CRIT-001: "A producer must not call the upward deliverer before this
//     commit." For a stop disposition the commit is the stopped LANDING — the
//     single lifecycle mutation that spends the fence and keeps the note. No
//     direct-parent inbox append and no parent wake may precede a landed
//     LifecycleStopped event in the control ledger.
//   - D6 round-3 MAJ-001: the notice persists "before the control is
//     applied" and its id is (parent, child, child_generation, stop_seq);
//     a working parent is woken once by THAT notice — by nothing earlier.
//
// Oracle derivations (from the ADR, not from this tree): the first accepted
// stop of a fresh child holds per-child seq 1 (D4: monotonic per-child seq).
// Its landed event is the ledger line with landed_stop present; until such a
// line exists, LifecycleStore.ListStoppedTransitions returns empty — the
// brief's own authority that a bare accepted intent is NOT an event.
//
// RED-1 (TestW1HistoricalLedgerNotice_NoNoticeOrWakeWithoutLandedStoppedEvent)
// gates the production stop's live effect on the normal injected
// GenerationCancelFunc seam, proves at the barrier that no landed event
// exists, then makes the CHILD's lifecycle JSONL unwritable (a plain file
// permission on a normal dependency — no test-only hook) so the landing's
// Mutate must fail, and releases the gate. The stop may then deliver NOTHING
// to the parent: no notice, no wake. Vacuity guard: the sibling positive
// control proves the exact W1 prepare hook reaches the parent append on this
// wiring when the lifecycle is writable, so the faulted run's silence would
// be the FIXED behaviour, not a broken instrument; the fault sits strictly
// AFTER the note stamp (which the barrier already persisted) and strictly
// BEFORE the landing mutation.
//
// Known-red reason at this pin (verified by reading, before any run):
// pkg/agent/steer_completion.go::deliverSteeredCompletion's prepare half
// calls ensureCurrentStopNote -> deliverStoppedChildNotice BEFORE
// commitSteeredCompletion lands the stop — the notice and wake precede the
// landed event whenever the parent inbox is writable.
package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestW1HistoricalLedgerNotice_PositiveControl_NoticeReachesParentAppendAndStopLands
// is the instrument proof, not the finding: on the identical wiring with NO
// fault, the W1 prepare hook (note stamp -> parent notice) demonstrably
// reaches the parent inbox append, the stop lands, and the ledger records the
// transition — so every later "the parent inbox stayed silent" verdict is
// measuring a real fault effect and not a dead harness. This subtest must
// stay green before AND after the backend fix.
func TestW1HistoricalLedgerNotice_PositiveControl_NoticeReachesParentAppendAndStopLands(t *testing.T) {
	al, provider, release := w1hSetup(t)
	defer release()
	lifecycle := al.GetSessionLifecycleStore()

	parentID := newTestSteeringSession(t, al, "ws-w1-order-positive")
	rec := w1hLaunchLiveChild(t, al, provider.entered, parentID, "call-w1-order-positive")
	childID, generation := rec.SessionID, rec.Generation

	_, err := NewSteerCanceller(lifecycle).StopTurns(context.Background(), childID, w1hOwner("w1-order-owner"), false, al.SteerGenerationCancel)
	if err != nil {
		t.Fatalf("StopTurns: %v", err)
	}
	if !w1hWaitFor(t, 15*time.Second, "child lands stopped", func() bool {
		cur, err := lifecycle.Load(childID)
		return err == nil && cur.State == session.LifecycleStopped
	}) {
		cur, _ := lifecycle.Load(childID)
		t.Fatalf("the stop never landed the child stopped: state=%v — the completion path did not run to its landing", cur.State)
	}

	var transitions []session.StoppedTransition
	if !w1hWaitFor(t, 10*time.Second, "landed-stop history line", func() bool {
		got, err := lifecycle.ListStoppedTransitions(childID)
		if err != nil {
			t.Fatalf("ListStoppedTransitions: %v", err)
		}
		transitions = got
		return len(got) == 1
	}) {
		t.Fatalf("the landed stop never recorded its historical event; ListStoppedTransitions = %s", w1hFormatTransitions(transitions))
	}
	tr := transitions[0]
	if tr.StopSeq != 1 || tr.Generation != generation || tr.ParentSessionID != parentID || tr.Cause != session.StopCauseStop {
		t.Fatalf("first landed transition = %+v, want {seq:1 gen:%d parent:%q cause:stop} (D4 monotonic per-child seq; D6 direct parent)", tr, generation, parentID)
	}

	wantID := w1hNoticeID(parentID, childID, generation, tr.StopSeq)
	if !w1hWaitFor(t, 10*time.Second, "direct-parent notice durable", func() bool {
		return len(w1hNoticesWithID(t, al, parentID, wantID)) == 1
	}) {
		t.Fatalf("the W1 prepare hook never reached the parent inbox append for %s — the faulted sibling's silence would be vacuous", wantID)
	}
	notices := w1hNoticesWithID(t, al, parentID, wantID)
	w1hAssertNoticeMatchesTransition(t, notices[0], parentID, tr)
}

// TestW1HistoricalLedgerNotice_NoNoticeOrWakeWithoutLandedStoppedEvent is
// RED-1: with the child's lifecycle persist failed after the stamped note and
// before the landing, the stop owns nothing published — the parent inbox must
// not gain the D6 notice and the working parent must not be woken, because
// ListStoppedTransitions still holds no landed event for the child.
func TestW1HistoricalLedgerNotice_NoNoticeOrWakeWithoutLandedStoppedEvent(t *testing.T) {
	al, provider, release := w1hSetup(t)
	defer release()
	lifecycle := al.GetSessionLifecycleStore()

	parentID := newTestSteeringSession(t, al, "ws-w1-order-red")
	rec := w1hLaunchLiveChild(t, al, provider.entered, parentID, "call-w1-order-red")
	childID, generation := rec.SessionID, rec.Generation
	wakes := w1hObserveWakes(t, al)

	// Gate the cascade at its live-effect step — the normal
	// GenerationCancelFunc injection the gateway Stop path supplies. While
	// the gate blocks, the stop is accepted (ledger intent) and stamped
	// (fence + note) but nothing has landed and no live effect has fired.
	var gateOnce sync.Once
	barrier := make(chan struct{})
	releaseGate := make(chan struct{})
	gate := func(ctx context.Context, sessionID string, gen int) (GenerationCancelResult, error) {
		gateOnce.Do(func() { close(barrier) })
		<-releaseGate
		return al.SteerGenerationCancel(ctx, sessionID, gen)
	}
	stopped := make(chan error, 1)
	go func() {
		_, err := NewSteerCanceller(lifecycle).StopTurns(context.Background(), childID, w1hOwner("w1-order-owner"), false, gate)
		stopped <- err
	}()
	select {
	case <-barrier:
	case <-time.After(15 * time.Second):
		t.Fatal("the stop never reached its live-cancel barrier — the stamp half never completed")
	}

	stamped, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(child at barrier): %v", err)
	}
	if stamped.State != session.LifecycleRunning || stamped.Stop == nil || stamped.Stop.Generation != stamped.Generation || stamped.StopNote == nil {
		t.Fatalf("barrier record = state %q fence %v note %v, want running with a current fence and stamped note — the acceptance stamp is the precondition this test faults after", stamped.State, stamped.Stop, stamped.StopNote)
	}
	if transitions, err := lifecycle.ListStoppedTransitions(childID); err != nil {
		t.Fatalf("ListStoppedTransitions(at barrier): %v", err)
	} else if len(transitions) != 0 {
		t.Fatalf("barrier ledger already holds landed events (%s) — a bare accepted stop intent must not be an event (W2a brief)", w1hFormatTransitions(transitions))
	}

	// Instrument: the parent's real inbox is writable at fault time.
	w1hProbeMessage(t, al, parentID, childID)
	// The fault: the CHILD's lifecycle JSONL becomes unwritable — the normal
	// persist dependency of the landing mutation, sitting strictly after the
	// (already persisted) note stamp and strictly before the landing.
	w1hFaultWrites(t, w1hChildLifecyclePath(t, al, childID))

	close(releaseGate)
	if err := <-stopped; err != nil {
		t.Fatalf("StopTurns returned an error: %v", err)
	}
	// Give the released turn's completion every chance to publish wrongly.
	w1hWaitFor(t, 10*time.Second, "parent notice appearing", func() bool {
		return len(w1hNoticesWithID(t, al, parentID, w1hNoticeID(parentID, childID, generation, 1))) > 0
	})
	time.Sleep(2 * time.Second) // grace so a late wake cannot hide behind the poll window

	// ORACLE (D2 CRIT-001, D6): no landed event -> no notice -> no wake.
	transitions, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after release): %v", err)
	}
	if len(transitions) != 0 {
		t.Errorf("landed-stop history = %s, want empty — the faulted lifecycle must not produce a landed event", w1hFormatTransitions(transitions))
	}
	wantID := w1hNoticeID(parentID, childID, generation, 1)
	if got := w1hNoticesWithID(t, al, parentID, wantID); len(got) != 0 {
		t.Errorf("parent inbox holds %d notice(s) with id %s while ListStoppedTransitions is EMPTY — "+
			"D2 CRIT-001 forbids calling the upward deliverer before the stopped landing commits "+
			"(the W1 prepare hook delivered the notice before the landed event existed); bodies: %+v",
			len(got), wantID, got)
	}
	for _, tr := range transitions {
		id := w1hNoticeID(parentID, childID, tr.Generation, tr.StopSeq)
		if c := wakes.count(id); c == 0 {
			t.Errorf("landed transition %s exists but its notice never woke the working parent (D6: a working parent is woken once by the notice) — count %d", id, c)
		}
	}
	// D6: the wake rides the notice, and the notice rides the landed event.
	// With no landed event, any wake for the child's notice id is a violation
	// on its own — whether or not an (illegal) notice was appended.
	if c := wakes.count(wantID); c != 0 {
		t.Errorf("parent was woken %d time(s) for %s while ListStoppedTransitions holds no landed event — D6's wake rides the notice, never precedes it", c, wantID)
	}
	cur, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(child after release): %v", err)
	}
	if cur.State == session.LifecycleStopped {
		t.Errorf("child landed stopped despite the unwritable lifecycle persist — the fault was not in the landing's dependency")
	}
}
