// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W2a history-failure pack, witness B (pkg/session half) — the per-session
// control ledger's parse and identity contract, exercised directly on the
// store with real files (no agent harness). Every expected value derives
// from the frozen ADR asset cd20cf8b (D4 ledger-first acceptance with a
// per-child monotonic seq; D6 landed-stop history as the landed_stop
// projection, keyed (parent, child, generation, stop_seq)) and the dispatch
// brief's witness-B rules — never from observed output of the code under
// test:
//
//   - a NEWLINE-TERMINATED malformed ledger line is a visible error for the
//     history reader AND for the next seq allocation — never silently
//     skipped;
//   - a genuinely UNTERMINATED torn last line (crash mid-append) may be
//     skipped, but invents no landed stop and does not raise the seq
//     high-water;
//   - a bare queued intent (no landed_stop projection) never surfaces as
//     historical landed history;
//   - RecordLandedStop is idempotent for the exact tuple and refuses a
//     divergent one visibly;
//   - the seq high-water stays strictly monotonic across fresh store
//     reopens (the durable truth, not a cache).
//
// None of these need permission tricks, so there is no euid guard here.

package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// historyFailureAcceptedAt is the acceptance instant every fixture in this
// file uses — a fixed, spec-derived value so identity assertions are exact.
var historyFailureAcceptedAt = time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)

const (
	historyFailureActor  = "human:histfail-owner"
	historyFailureParent = "histfail-parent"
)

// persistHistoryFailureRecord creates the child's real lifecycle record so
// AcceptStopControl finds a stop-eligible (queued, no fence, non-terminal)
// tail.
func persistHistoryFailureRecord(t *testing.T, store *LifecycleStore, id string) {
	t.Helper()
	if err := store.Persist(&LifecycleRecord{
		SessionID:      id,
		Generation:     1,
		State:          LifecycleQueued,
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "ws-histfail",
		AgentID:        "agent-1",
	}); err != nil {
		t.Fatalf("Persist(%q): %v", id, err)
	}
}

// acceptHistoryFailureIntent accepts a stop control with the store's real
// D4 acceptance path. stamp is nil: the intent alone is durable, the record
// stays stop-eligible — exactly the D4 crash shape whose parse/identity
// rules this witness pins.
func acceptHistoryFailureIntent(t *testing.T, store *LifecycleStore, id string) ControlGrant {
	t.Helper()
	outcome, grant, _, err := store.AcceptStopControl(id, StopControlIntent{
		Cause:      StopCauseStop,
		Actor:      historyFailureActor,
		AcceptedAt: historyFailureAcceptedAt,
	}, nil)
	if err != nil {
		t.Fatalf("AcceptStopControl(%q): %v", id, err)
	}
	if outcome != StopAcceptGranted {
		t.Fatalf("AcceptStopControl(%q) outcome = %d, want StopAcceptGranted", id, outcome)
	}
	if grant.Seq <= 0 || grant.ControlID == "" {
		t.Fatalf("AcceptStopControl granted seq %d control %q, want a positive seq and a control id (D4)", grant.Seq, grant.ControlID)
	}
	return grant
}

// recordHistoryFailureLanded writes the landed history for grant with the
// exact tuple the acceptance carries.
func recordHistoryFailureLanded(t *testing.T, store *LifecycleStore, id string, grant ControlGrant) LandedStop {
	t.Helper()
	landed := LandedStop{
		Seq:             grant.Seq,
		ControlID:       grant.ControlID,
		ParentSessionID: historyFailureParent,
		Generation:      1,
		Cause:           StopCauseStop,
		Actor:           historyFailureActor,
		At:              historyFailureAcceptedAt.Add(2 * time.Second),
	}
	if err := store.RecordLandedStop(id, landed); err != nil {
		t.Fatalf("RecordLandedStop(%q seq %d): %v", id, grant.Seq, err)
	}
	return landed
}

// historyFailureLedgerLineCount returns the number of physical lines in the
// session's control ledger (the file the reader/allocation must refuse to
// change silently).
func historyFailureLedgerLineCount(t *testing.T, dir, id string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "controls", id+".jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(controls/%s.jsonl): %v", id, err)
	}
	count := 0
	for _, b := range raw {
		if b == '\n' {
			count++
		}
	}
	return count
}

// appendHistoryFailureRaw appends raw bytes to the session's control ledger
// (the test's own real corruption channel — no production write path is
// involved).
func appendHistoryFailureRaw(t *testing.T, dir, id, raw string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, "controls", id+".jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("append to controls/%s.jsonl: %v", id, err)
	}
	defer f.Close()
	if _, err := f.WriteString(raw); err != nil {
		t.Fatalf("write to controls/%s.jsonl: %v", id, err)
	}
}

// TestControlLedgerHistory_TerminatedMalformedTailIsVisibleErrorForReaderAndNextAllocation
// pins the visible-error rule: after a known-valid intent + landed event
// (the instrument proving the file parses), one newline-terminated malformed
// line must make BOTH the history reader and the next seq allocation fail
// visibly, and must not be silently swallowed or skipped.
func TestControlLedgerHistory_TerminatedMalformedTailIsVisibleErrorForReaderAndNextAllocation(t *testing.T) {
	dir := t.TempDir()
	store := NewLifecycleStore(dir)
	id := "histfail-terminated-tail"
	persistHistoryFailureRecord(t, store, id)
	grant := acceptHistoryFailureIntent(t, store, id)
	recordHistoryFailureLanded(t, store, id, grant)

	// Instrument: the known-valid lines parse and surface exactly one landed
	// event — the corruption below is the ONLY thing that can turn the
	// reader red.
	events, err := store.ListStoppedTransitions(id)
	if err != nil {
		t.Fatalf("instrument: the healthy ledger failed to parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("instrument: healthy ledger returned %d events, want 1: %+v", len(events), events)
	}

	appendHistoryFailureRaw(t, dir, id, "{ not json }\n")

	if _, err := store.ListStoppedTransitions(id); err == nil {
		t.Errorf("a newline-terminated malformed ledger line was read without error — the history reader must fail " +
			"visibly instead of silently degrading (a skipped landed event would lose a stop from history)")
	}
	// The next allocation reads the same durable truth (D4: the high-water
	// comes from the ledger): the parse failure must refuse the allocation
	// visibly rather than allocate a seq on top of an unreadable ledger.
	outcome, divergentGrant, _, allocErr := store.AcceptStopControl(id, StopControlIntent{
		Cause:      StopCauseStop,
		Actor:      historyFailureActor,
		AcceptedAt: historyFailureAcceptedAt,
	}, nil)
	if allocErr == nil {
		t.Errorf("a stop control was accepted (outcome %d seq %d) while the ledger does not parse — the next "+
			"allocation must fail visibly instead of allocating over an unreadable high-water", outcome, divergentGrant.Seq)
	}
	if divergentGrant.Seq != 0 || divergentGrant.ControlID != "" {
		t.Errorf("a refused allocation returned grant {seq %d control %q}, want the zero grant", divergentGrant.Seq, divergentGrant.ControlID)
	}
	if got := historyFailureLedgerLineCount(t, dir, id); got != 3 {
		t.Errorf("the control ledger grew from 3 to %d lines around the refused allocation — a parse failure must "+
			"write nothing (no intent, no repair line)", got)
	}
}

// TestControlLedgerHistory_TornUnterminatedTailSkippedNotInvented pins the
// torn-tail rule: a genuinely unterminated final line (a crash mid-append)
// is skipped, invents no landed stop, and does not raise the seq high-water
// — the next allocation continues from the last VALID line.
func TestControlLedgerHistory_TornUnterminatedTailSkippedNotInvented(t *testing.T) {
	dir := t.TempDir()
	store := NewLifecycleStore(dir)
	id := "histfail-torn-tail"
	persistHistoryFailureRecord(t, store, id)
	grant := acceptHistoryFailureIntent(t, store, id)
	landed := recordHistoryFailureLanded(t, store, id, grant)

	// A torn copy of a plausible LANDED line, cut mid-JSON with NO trailing
	// newline: if the reader invented history from it, a phantom stop with a
	// ghost parent would appear.
	torn := fmt.Sprintf(`{"seq":%d,"control_id":"ctl_torn","verb":"stop","state":"queued","accepted_at":%q,`+
		`"landed_stop":{"parent_session_id":"ghost-parent","generation":1,"ca`, grant.Seq+7, historyFailureAcceptedAt.Format(time.RFC3339Nano))
	appendHistoryFailureRaw(t, dir, id, torn)

	events, err := store.ListStoppedTransitions(id)
	if err != nil {
		t.Fatalf("an unterminated torn tail made the whole ledger unreadable: %v — only the torn line itself may be skipped", err)
	}
	if len(events) != 1 {
		t.Fatalf("the torn tail produced %d events, want exactly the 1 real event: %+v — a torn append invents no landed stop", len(events), events)
	}
	if events[0].ControlID != grant.ControlID || events[0].StopSeq != uint64(grant.Seq) || events[0].ParentSessionID != historyFailureParent || !events[0].At.Equal(landed.At) {
		t.Errorf("the surviving event = %+v, want the real landed tuple {seq %d control %q parent %q at %s}",
			events[0], grant.Seq, grant.ControlID, historyFailureParent, landed.At)
	}
	// The high-water comes from parseable lines only: the torn line's seq
	// (+7) must not raise it, and the next allocation must still succeed
	// with exactly high-water+1.
	_, nextGrant, _, allocErr := store.AcceptStopControl(id, StopControlIntent{
		Cause:      StopCauseStop,
		Actor:      historyFailureActor,
		AcceptedAt: historyFailureAcceptedAt,
	}, nil)
	if allocErr != nil {
		t.Fatalf("the next allocation over a torn tail failed: %v", allocErr)
	}
	if nextGrant.Seq != grant.Seq+1 {
		t.Errorf("the next allocation returned seq %d, want %d — the torn line must not raise the seq high-water (D4 monotonicity reads parseable truth)",
			nextGrant.Seq, grant.Seq+1)
	}
}

// TestControlLedgerHistory_BareIntentNeverSurfacesAsLandedEvent pins the
// intent/history split: a durable queued acceptance intent with NO
// landed_stop projection is not history — the reader returns zero events
// from a ledger that provably has a line.
func TestControlLedgerHistory_BareIntentNeverSurfacesAsLandedEvent(t *testing.T) {
	dir := t.TempDir()
	store := NewLifecycleStore(dir)
	id := "histfail-bare-intent"
	persistHistoryFailureRecord(t, store, id)
	acceptHistoryFailureIntent(t, store, id)

	// Instrument: the ledger physically carries the intent line, so the
	// empty result below is a decision, not an empty file.
	if got := historyFailureLedgerLineCount(t, dir, id); got != 1 {
		t.Fatalf("instrument: the control ledger carries %d lines, want the 1 acceptance intent", got)
	}

	events, err := store.ListStoppedTransitions(id)
	if err != nil {
		t.Fatalf("ListStoppedTransitions over a bare intent: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("a bare queued intent surfaced as %d landed event(s): %+v — only the landed_stop projection is "+
			"history (D6); an intent alone is never a landed stop", len(events), events)
	}
}

// TestControlLedgerHistory_LandedStopIdempotentExactDivergentRefused pins
// RecordLandedStop's identity contract: the exact tuple is an idempotent
// no-op (still one event); a retry diverging in control id, generation,
// cause or actor is refused visibly; nothing divergent is ever written.
func TestControlLedgerHistory_LandedStopIdempotentExactDivergentRefused(t *testing.T) {
	dir := t.TempDir()
	store := NewLifecycleStore(dir)
	id := "histfail-idempotent"
	persistHistoryFailureRecord(t, store, id)
	grant := acceptHistoryFailureIntent(t, store, id)
	landed := recordHistoryFailureLanded(t, store, id, grant)

	want := StoppedTransition{
		SessionID:       id,
		ParentSessionID: historyFailureParent,
		Generation:      1,
		StopSeq:         uint64(grant.Seq),
		ControlID:       grant.ControlID,
		Cause:           StopCauseStop,
		Actor:           historyFailureActor,
		At:              landed.At,
	}

	// Exact retry: idempotent, one event, original values.
	if err := store.RecordLandedStop(id, landed); err != nil {
		t.Errorf("exact-tuple RecordLandedStop retry: %v, want nil (idempotent by seq)", err)
	}
	events, err := store.ListStoppedTransitions(id)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after retry): %v", err)
	}
	if len(events) != 1 || events[0] != want {
		t.Fatalf("after the exact retry the history = %+v, want exactly the original event %+v", events, want)
	}

	// Divergent retries: every divergence is a visible refusal.
	cases := []struct {
		name string
		mut  func(*LandedStop)
	}{
		{"control", func(l *LandedStop) { l.ControlID = "ctl_divergent" }},
		{"generation", func(l *LandedStop) { l.Generation = 2 }},
		{"cause", func(l *LandedStop) { l.Cause = StopCauseCascade }},
		{"actor", func(l *LandedStop) { l.Actor = "agent:someone-else" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			divergent := landed
			tc.mut(&divergent)
			if err := store.RecordLandedStop(id, divergent); err == nil {
				t.Errorf("a landed-stop retry diverging in %s was accepted — divergence must be refused visibly, "+
					"never rewritten into history", tc.name)
			}
			events, err := store.ListStoppedTransitions(id)
			if err != nil {
				t.Fatalf("ListStoppedTransitions(after divergent %s retry): %v", tc.name, err)
			}
			if len(events) != 1 || events[0] != want {
				t.Errorf("after the divergent %s retry the history = %+v, want exactly the original event — a "+
					"refused retry must write nothing", tc.name, events)
			}
		})
	}
}

// TestControlLedgerHistory_SeqHighWaterStrictlyMonotonicAcrossReopens pins
// D4's monotonicity across store lifetimes: each fresh open allocates from
// the durable ledger truth, never below or equal to it.
func TestControlLedgerHistory_SeqHighWaterStrictlyMonotonicAcrossReopens(t *testing.T) {
	dir := t.TempDir()
	store := NewLifecycleStore(dir)
	id := "histfail-monotonic"
	persistHistoryFailureRecord(t, store, id)

	first := acceptHistoryFailureIntent(t, store, id)
	recordHistoryFailureLanded(t, store, id, first)

	second := acceptHistoryFailureIntent(t, NewLifecycleStore(dir), id)
	if second.Seq <= first.Seq {
		t.Errorf("after a fresh reopen the second acceptance got seq %d, first was %d — the high-water must come "+
			"from the durable ledger so a reopen can never reuse or repeat a sequence (D4)", second.Seq, first.Seq)
	}
	if second.ControlID == first.ControlID {
		t.Errorf("two accepted controls share control id %q — each acceptance must mint a unique id (D4)", first.ControlID)
	}

	third := acceptHistoryFailureIntent(t, NewLifecycleStore(dir), id)
	if third.Seq <= second.Seq {
		t.Errorf("after a second reopen the third acceptance got seq %d, second was %d — strictly monotonic across "+
			"every store lifetime", third.Seq, second.Seq)
	}
	if third.ControlID == second.ControlID || third.ControlID == first.ControlID {
		t.Errorf("accepted control ids repeat: %q %q %q — ids must be unique per acceptance", first.ControlID, second.ControlID, third.ControlID)
	}

	// The landed event for the first control must still read exactly once —
	// the later intents (no landed projection) add no history.
	events, err := NewLifecycleStore(dir).ListStoppedTransitions(id)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(final reopen): %v", err)
	}
	if len(events) != 1 || events[0].StopSeq != uint64(first.Seq) {
		t.Errorf("history after three acceptances = %+v, want exactly the 1 landed event for seq %d — bare intents "+
			"never become history", events, first.Seq)
	}
}
