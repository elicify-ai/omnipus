// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W1 historical direct-parent notice RED pack, part 2 — TWO STOPS, ONE
// GENERATION. ADR-20260928 sub-agent control plane (frozen asset cd20cf8b):
//
//   - D4: each accepted stop gets a per-child monotonic seq — two accepted
//     stops of one child hold seq 1 and seq 2.
//   - D6 round-3 MAJ-001: EVERY transition into stopped persists one notice
//     for its DIRECT parent; the id is (parent, child, child_generation,
//     stop_seq); the notice says cause/actor/time. Two transitions therefore
//     produce two distinct notices whose contents are their own transitions.
//   - D2 CRIT-001: an explicit same-generation RESUME clears the active
//     note/fence but never the landed history (the ledger is append-only).
//   - D2 stop table / MIN-007: a stop of an already-landed stopped child is
//     idempotent — no ledger line, no notice.
//   - F0929-7 / D6 routing: the notice goes to the direct parent only; the
//     grandparent is not notified in the child's place.
//
// Scenario oracle (derived from the ADR before touching this tree): both
// stops interrupt a REAL live turn, so both land through the production
// completion path. Expected: ledger = [seq 1, seq 2] strictly advancing;
// parent inbox = exactly two notices, stopped-notice:P:C:<gen>:1 and
// ...:2, each matching its OWN transition's cause/actor/at; zero
// stopped-notice traffic in the grandparent's inbox; one wake per notice;
// and the legacy `<child>:<gen>:final` interrupted error (the known baseline
// RED, w1-cherry/go-test-stop-caused-lands-stopped.log) still asserted absent
// as a distinct row — never weakened.
//
// Known-red reasons at this pin (verified by reading, before any run):
//   - pkg/agent/stopped_notice.go::ensureCurrentStopNote only keeps a note
//     whose Seq equals the generation; the second stop's stamped note carries
//     the real seq 2, so the hook REWRITES it to the generation stand-in
//     (By system, At time.Now) before composing the notice id — which
//     collapses into stop 1's id and is deduplicated away, and its content is
//     a reconstruction.
//   - The rewritten note's seq then fails
//     pkg/session/lifecycle_control_ledger_writer.go::verifyLandedTuple
//     (control id mismatch on seq 1's idempotent retry), so the second stop's
//     landed history is never recorded either.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestW1HistoricalLedgerNotice_TwoSameGenerationStopsDeliverTwoDistinctOriginalNotices(t *testing.T) {
	al, provider, release := w1hSetup(t)
	defer release()
	lifecycle := al.GetSessionLifecycleStore()

	// Grandparent G -> parent P (steered child of G, no live turn of its own
	// needed) -> child C (a real live turn). Stopping C must notify P only.
	grandparentID := newTestSteeringSession(t, al, "ws-w1-two-stops")
	parentRec := launchRunningChild(t, al, grandparentID, "call-w1-two-stops-parent")
	parentID := parentRec.SessionID

	rec := w1hLaunchLiveChild(t, al, provider.entered, parentID, "call-w1-two-stops-child")
	childID, generation := rec.SessionID, rec.Generation
	owner := w1hOwner("w1-two-stops-owner")
	wakes := w1hObserveWakes(t, al)
	canceller := NewSteerCanceller(lifecycle)

	// ---- Stop 1: lands, one notice with the transition's own content. ----
	report, err := canceller.StopTurns(context.Background(), childID, owner, false, al.SteerGenerationCancel)
	if err != nil {
		t.Fatalf("first StopTurns: %v", err)
	}
	if !w1hWaitFor(t, 15*time.Second, "child lands stopped (stop 1)", func() bool {
		cur, loadErr := lifecycle.Load(childID)
		return loadErr == nil && cur.State == session.LifecycleStopped
	}) {
		t.Fatalf("stop 1 never landed the child stopped")
	}
	// The landing flips State first and appends the landed history right
	// after the state mutation (D2: the ledger line is part of the same
	// landing, but not visible at the instant State flips). Wait for the
	// asserted event with the bounded wait this test uses for the stop-1
	// notice below — an oracle race measured on a pristine archive (State
	// read as stopped, history still empty). The length assertion below stays
	// exact (== 1), so a missing or duplicate line still fails with its
	// diagnostics.
	w1hWaitFor(t, 10*time.Second, "stop-1 landed history visible", func() bool {
		landed, listErr := lifecycle.ListStoppedTransitions(childID)
		return listErr == nil && len(landed) >= 1
	})
	got, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after stop 1): %v", err)
	}
	if len(got) != 1 {
		// Failure diagnostics: the report, the landed record's stop fields,
		// and the RAW control-ledger file — so a silent no-history landing is
		// attributable (CHECK reuses this on every rerun).
		cur, _ := lifecycle.Load(childID)
		rawLedger, _ := os.ReadFile(filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle", "controls", childID+".jsonl"))
		t.Fatalf("landed history after stop 1 = %s, want exactly one transition; report={reached:%v unreachable:%v skippedTerminal:%v}; record={state:%s stop:%+v note:%+v effect:%+v}; raw ledger: %s",
			w1hFormatTransitions(got), report.Reached, report.Unreachable, report.SkippedTerminal,
			cur.State, cur.Stop, cur.StopNote, cur.StopEffect, string(rawLedger))
	}
	tr1 := got[0]
	// The actor spelling for a human principal is the system's actor
	// convention human:<id> (the same spelling the existing U1 pack's
	// fixtures and the baseline RED log carry); the ORACLE is that the
	// transition names the principal that ordered the stop.
	wantActor := "human:" + owner.ID
	if tr1.StopSeq != 1 || tr1.Generation != generation || tr1.ParentSessionID != parentID || tr1.Cause != session.StopCauseStop || tr1.Actor != wantActor {
		t.Fatalf("first transition = %+v, want {seq:1 gen:%d parent:%q cause:stop actor:%q} (D4/D6)",
			tr1, generation, parentID, wantActor)
	}
	id1 := w1hNoticeID(parentID, childID, generation, tr1.StopSeq)
	if !w1hWaitFor(t, 10*time.Second, "stop-1 notice durable", func() bool {
		return len(w1hNoticesWithID(t, al, parentID, id1)) == 1
	}) {
		t.Fatalf("stop 1 delivered no direct-parent notice with id %s — the setup for the two-notice oracle is incomplete", id1)
	}
	for _, msg := range w1hNoticesWithID(t, al, parentID, id1) {
		w1hAssertNoticeMatchesTransition(t, msg, parentID, tr1)
	}
	if ids := w1hStoppedNoticeIDsIn(t, al, grandparentID); len(ids) != 0 {
		t.Errorf("grandparent inbox holds stopped-notice traffic %v — D6 routes the notice to the DIRECT parent only", ids)
	}
	finalID := childID + ":" + strconv.Itoa(generation) + ":final"
	if got := w1hNoticesWithID(t, al, parentID, finalID); len(got) != 0 {
		t.Errorf("stop 1 also delivered the legacy %s terminal/fatal completion (%d entries) — a stopped child must not hand back a final beside its D6 notice (baseline RED, distinct row)", finalID, len(got))
	}

	// ---- Explicit same-generation RESUME: note cleared, history kept. ----
	resumed, err := canceller.Revive(context.Background(), childID, owner)
	if err != nil {
		t.Fatalf("Revive (explicit RESUME): %v", err)
	}
	resumedRec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after RESUME): %v", err)
	}
	if resumed != generation || resumedRec.Generation != generation {
		t.Fatalf("RESUME moved the generation: returned %d record %d, want %d (D2 CRIT-001 same-generation resume)", resumed, resumedRec.Generation, generation)
	}
	if resumedRec.State != session.LifecycleQueued || resumedRec.StopNote != nil {
		t.Fatalf("record after RESUME = state %q note %v, want queued with the active note cleared", resumedRec.State, resumedRec.StopNote)
	}
	still, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after RESUME): %v", err)
	}
	if len(still) != 1 || still[0].StopSeq != tr1.StopSeq {
		t.Fatalf("landed history after RESUME = %s, want exactly the pre-resume transition (founder Q2=A: the historical event survives a same-generation RESUME)", w1hFormatTransitions(still))
	}

	// ---- Second real turn, second accepted stop. ----
	if _, dispatchErr := NewSteerLauncher(al).Dispatch(context.Background(), childID, generation); dispatchErr != nil {
		t.Fatalf("Dispatch(after RESUME): %v", dispatchErr)
	}
	select {
	case <-provider.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the resumed child never reached its provider — no second live turn to stop")
	}
	if _, secondStopErr := canceller.StopTurns(context.Background(), childID, owner, false, al.SteerGenerationCancel); secondStopErr != nil {
		t.Fatalf("second StopTurns: %v", secondStopErr)
	}
	if !w1hWaitFor(t, 15*time.Second, "child lands stopped (stop 2)", func() bool {
		cur, loadErr := lifecycle.Load(childID)
		return loadErr == nil && cur.State == session.LifecycleStopped
	}) {
		t.Fatalf("stop 2 never landed the child stopped")
	}
	// Same ordering as stop 1: State flips before the landed history line and
	// the direct-parent notice are published. Wait for each asserted event
	// with the 10s bound used for the stop-1 notice; every assertion below
	// stays exact (== 2 entries / distinct ids / one notice / one wake), so a
	// missing, deduplicated or duplicated event still fails.
	w1hWaitFor(t, 10*time.Second, "stop-2 landed history visible", func() bool {
		landed, listErr := lifecycle.ListStoppedTransitions(childID)
		return listErr == nil && len(landed) >= 2
	})
	childNoticePrefix := "stopped-notice:" + parentID + ":" + childID + ":"
	w1hWaitFor(t, 10*time.Second, "stop-2 notice durable", func() bool {
		n := 0
		for _, id := range w1hStoppedNoticeIDsIn(t, al, parentID) {
			if strings.HasPrefix(id, childNoticePrefix) {
				n++
			}
		}
		return n >= 2
	})

	// ORACLE (D6), deliberately independent of the ledger's state: two real
	// landed transitions of one child must have produced TWO DISTINCT notices
	// in the direct parent's inbox. The per-transition id/content rows below
	// need the ledger's seqs; THIS row fails today on the dedup alone.
	childPrefix := "stopped-notice:" + parentID + ":" + childID + ":"
	distinct := map[string]struct{}{}
	childNoticeCount := 0
	for _, id := range w1hStoppedNoticeIDsIn(t, al, parentID) {
		if strings.HasPrefix(id, childPrefix) {
			childNoticeCount++
			distinct[id] = struct{}{}
		}
	}
	if childNoticeCount != 2 || len(distinct) != 2 {
		t.Errorf("direct-parent inbox holds %d stopped-notice entries for the child with %d distinct id(s) (%v), want 2 entries with 2 distinct ids — "+
			"two landed transitions are two D6 identities (parent, child, generation, stop_seq); the second stop's notice must not be deduplicated into the first",
			childNoticeCount, len(distinct), w1hStoppedNoticeIDsIn(t, al, parentID))
	}

	// ORACLE (D4): the ledger holds BOTH transitions, strictly advancing.
	both, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after stop 2): %v", err)
	}
	if len(both) != 2 {
		t.Errorf("landed history after two real stops = %s, want two transitions with strictly advancing seqs — "+
			"the second stop is its own accepted control and its own historical event (D4)", w1hFormatTransitions(both))
	} else {
		tr2 := both[1]
		if tr2.StopSeq <= tr1.StopSeq {
			t.Errorf("second transition seq = %d, first = %d, want strictly greater (D4 monotonic per-child seq)", tr2.StopSeq, tr1.StopSeq)
		}
		if tr2.Generation != generation || tr2.ParentSessionID != parentID || tr2.Cause != session.StopCauseStop || tr2.Actor != wantActor {
			t.Errorf("second transition = %+v, want {gen:%d parent:%q cause:stop actor:%q}", tr2, generation, parentID, wantActor)
		}
		// ORACLE (D6): TWO distinct notices, each matching its OWN transition.
		id2 := w1hNoticeID(parentID, childID, generation, tr2.StopSeq)
		notices2 := w1hNoticesWithID(t, al, parentID, id2)
		if len(notices2) != 1 {
			t.Errorf("parent inbox holds %d notice(s) with id %s, want exactly 1 — "+
				"the second landed transition is a distinct D6 identity and must not be deduplicated into the first stop's notice", len(notices2), id2)
		}
		for _, msg := range notices2 {
			w1hAssertNoticeMatchesTransition(t, msg, parentID, tr2)
		}
		w1hWaitFor(t, 10*time.Second, "stop-2 wake observed", func() bool { return wakes.count(id2) >= 1 })
		if c := wakes.count(id2); c != 1 {
			t.Errorf("wakes for %s = %d, want 1 (the working parent is woken once per notice)", id2, c)
		}
	}
	if ids := w1hStoppedNoticeIDsIn(t, al, grandparentID); len(ids) != 0 {
		t.Errorf("grandparent inbox holds stopped-notice traffic %v after both stops — D6 routes notices to the DIRECT parent only", ids)
	}

	// POSITIVE (D2 stop table / MIN-007): a repeat stop of the already-landed
	// child writes no third line and no third notice, and leaves the retained
	// note untouched.
	before, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(before repeat stop): %v", err)
	}
	if before.StopNote == nil {
		t.Fatalf("retained note vanished before the repeat stop — the landed stopped record must keep its note")
	}
	retained := *before.StopNote
	if _, repeatStopErr := canceller.StopTurns(context.Background(), childID, owner, false, al.SteerGenerationCancel); repeatStopErr != nil {
		t.Fatalf("repeat StopTurns on an already stopped child: %v", repeatStopErr)
	}
	after, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after repeat stop): %v", err)
	}
	if after.State != session.LifecycleStopped {
		t.Errorf("state after repeat stop = %q, want still stopped", after.State)
	}
	if after.StopNote == nil {
		t.Fatalf("the repeat stop dropped the retained note — MIN-007 requires the retained note untouched")
	} else if got := *after.StopNote; got != retained {
		t.Errorf("the repeat stop rewrote the retained note: %+v -> %+v (MIN-007: no ledger line, no note rewrite)", retained, got)
	}
	afterTransitions, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after repeat stop): %v", err)
	}
	if len(afterTransitions) != 2 {
		t.Errorf("landed history after the repeat stop = %s, want still exactly two transitions (a repeat stop of a landed child writes no line)", w1hFormatTransitions(afterTransitions))
	}
	for _, id := range []string{id1} {
		if got := len(w1hNoticesWithID(t, al, parentID, id)); got != 1 {
			t.Errorf("notice count for %s after the repeat stop = %d, want still 1", id, got)
		}
	}
}
