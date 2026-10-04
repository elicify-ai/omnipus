// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W2a history-failure RED pack, witness A (pkg/agent half) — the stop
// landing's control-ledger history append under a REAL append failure.
// Every expected value below is derived from the frozen ADR asset
// cd20cf8b (D2 stop fence vs lasting note, D4 ledger-first + monotonic
// per-child seq, D6 landed-stop history keyed (parent, child, generation,
// stop_seq)) and the founder's Q2=A ruling (a pending stopped-child notice
// survives a same-generation RESUME as historical history) — never from
// observed output of the code under test.
//
// The failure is real, not a mock: the session's EXISTING controls/
// <child>.jsonl is chmod'd 0400 between the durable fence and the landing
// (the injected GenerationCancelFunc gate is the same seam the T27
// family uses), so fileutil.AppendJSONL's O_WRONLY|O_APPEND open gets
// EACCES while os.ReadFile still succeeds. The lifecycle record file is a
// DIFFERENT file (the lifecycle dir root), and each test proves with its
// own probe that the chmod hit the history append and not the lifecycle
// record — the stop must still land durably.
//
// Permission legs are skipped ONLY under euid 0 (root bypasses file mode
// checks, so no EACCES is obtainable) — named in the skip reason, never
// faked into a pass or a fake red.

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
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// historyFailureOwner is the principal every stop in this pack is ordered
// by; the spec'd stop_note.by format is "human:<id>" (StopNote/StopActor-
// FromPrincipal vocabulary), so the expected actor string derives from the
// spec, not from a struct read.
var historyFailureOwner = steer.Principal{Kind: steer.PrincipalKindHuman, ID: "histfail-owner"}

const historyFailureActor = "human:histfail-owner"

// historyFailurePaths returns the control-ledger path and the lifecycle
// record path for childID under al's real lifecycle store. The controls
// subdirectory layout is the W2a reader's documented non-collision design
// (pkg/session/lifecycle_control_ledger.go::controlLedgerPath).
func historyFailurePaths(t *testing.T, al *AgentLoop, childID string) (controlsPath, recordPath string) {
	t.Helper()
	lifecycleDir := filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle")
	return filepath.Join(lifecycleDir, "controls", childID+".jsonl"),
		filepath.Join(lifecycleDir, childID+".jsonl")
}

// historyFailureGateStop runs the production StopTurns with the live-turn
// adapter replaced by a gate: the cascade stamps the durable fence/note
// (and the ledger acceptance intent), then blocks in the adapter BEFORE any
// live effect. betweenFenceAndLanding runs in that window — the landing
// cannot proceed until it returns — so the test breaks the history append
// channel BEFORE the landing happens. Returns the joined report/error.
func historyFailureGateStop(
	t *testing.T, al *AgentLoop, childID string, generation int,
	betweenFenceAndLanding func(t *testing.T),
) (report steer.CancelReport, err error) {
	t.Helper()
	lifecycle := al.GetSessionLifecycleStore()
	gate := make(chan struct{})
	gated := func(ctx context.Context, id string, gen int) (GenerationCancelResult, error) {
		<-gate
		return al.SteerGenerationCancel(ctx, id, gen)
	}
	type stopCall struct {
		report steer.CancelReport
		err    error
	}
	done := make(chan stopCall, 1)
	go func() {
		r, e := NewSteerCanceller(lifecycle).StopTurns(context.Background(), childID, historyFailureOwner, false, gated)
		done <- stopCall{r, e}
	}()

	// Wait until the acceptance is durable: the current-generation fence and
	// the lasting note (D2's same-mutation stamp) are visible on the record.
	// The goroutine is parked in the gate here — the fence being visible
	// while the landing has not run is exactly the D4 crash window.
	deadline := time.Now().Add(10 * time.Second)
	for {
		cur, loadErr := lifecycle.Load(childID)
		if loadErr != nil {
			t.Fatalf("Load(child) while waiting for the stop fence: %v", loadErr)
		}
		if cur.Stop != nil && cur.Stop.Generation == generation && cur.StopNote != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stop fence never became durable within the deadline: state=%q stop=%v note=%v",
				cur.State, cur.Stop, cur.StopNote)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if betweenFenceAndLanding != nil {
		betweenFenceAndLanding(t)
	}
	close(gate)
	call := <-done
	return call.report, call.err
}

// historyFailureBlockLedger chmods the EXISTING control ledger to 0400 and
// proves the instrument: the append channel (O_WRONLY|O_APPEND) now fails
// with a permission error while the lifecycle record file is still
// writable — the failure is scoped to the history append, exactly the D4
// crash shape (durable intent, unappendable history).
func historyFailureBlockLedger(t *testing.T, controlsPath, recordPath string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission-specific legs require a non-root euid: root bypasses file-mode checks, " +
			"so no EACCES is obtainable and the W2a history-failure witness cannot run honestly")
	}
	if err := os.Chmod(controlsPath, 0o400); err != nil {
		t.Fatalf("chmod(controls, 0400): %v", err)
	}
	f, err := os.OpenFile(controlsPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		f.Close()
		t.Fatalf("instrument failed: the control ledger is still writable after chmod 0400 — "+
			"the history-append failure cannot be induced on this filesystem: %s", controlsPath)
	}
	if !os.IsPermission(err) {
		t.Fatalf("instrument failed: opening the control ledger O_WRONLY|O_APPEND returned %v, want a permission error", err)
	}
	lf, err := os.OpenFile(recordPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("instrument failed: the lifecycle record file is ALSO unwritable — the chmod would block the "+
			"stop landing itself instead of only the history append: %v", err)
	}
	lf.Close()
}

// historyFailureUnblockLedger restores the control ledger so t.TempDir
// cleanup and later stops behave; a failed restore is fatal — a 0400 file
// left behind would poison the package's temp dir teardown.
func historyFailureUnblockLedger(t *testing.T, controlsPath string) {
	t.Helper()
	if err := os.Chmod(controlsPath, 0o600); err != nil {
		t.Fatalf("restore chmod(controls, 0600): %v", err)
	}
}

// TestHistoryFailure_StopLandsUnderUnwritableLedger_ResumeRefusedThenExactlyOneOriginalEvent
// is the recovery spine: the stop lands durably with the history append
// failing; an explicit same-generation RESUME is refused VISIBLY while the
// ledger stays unwritable and the note+effect tuple stays untouched; after
// repair the RESUME succeeds and the history holds EXACTLY ONE original
// event (original seq/control/cause/actor/At, real parent), which survives
// a fresh store reopen, is idempotent under retry, refuses a divergent
// retry, and is followed by a second accepted stop with a strictly greater
// seq and its own distinct event.
//
// Oracles: D2 (RESUME clears note/fence/effect atomically, same generation;
// the landed note is the last reconstructable tuple so the resume must not
// destroy it unrecorded), D4 (per-child monotonic seq; distinct control ids
// per accepted control), D6 (history keyed (parent, child, generation,
// stop_seq), At = the ORIGINAL note instant), founder Q2=A (history
// outlives the cleared note).
func TestHistoryFailure_StopLandsUnderUnwritableLedger_ResumeRefusedThenExactlyOneOriginalEvent(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	lifecycle := al.GetSessionLifecycleStore()

	parentID := newTestSteeringSession(t, al, "ws-histfail-recovery")
	rec := launchRunningChild(t, al, parentID, "call-histfail-recovery")
	childID, generation := rec.SessionID, rec.Generation
	controlsPath, recordPath := historyFailurePaths(t, al, childID)

	report, stopErr := historyFailureGateStop(t, al, childID, generation, func(t *testing.T) {
		historyFailureBlockLedger(t, controlsPath, recordPath)
	})

	// The stop itself lands durably: the lifecycle record file is writable,
	// only the history append is blocked. Nothing in the brief or the ADR
	// lets the test pretend StopTurns returned an error — the refusal is
	// asserted where the spec puts it (below), never invented here.
	if stopErr != nil {
		t.Fatalf("StopTurns under a blocked history append: %v (the stop landing itself must still succeed: "+
			"the lifecycle record file is writable, only controls/ is not)", stopErr)
	}
	reached := false
	for _, id := range report.Reached {
		if id == childID {
			reached = true
		}
	}
	if !reached {
		t.Fatalf("the stop did not reach the child: report.Reached=%v (the fence was durable before the gate)",
			report.Reached)
	}

	landed, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(landed): %v", err)
	}
	if landed.State != session.LifecycleStopped {
		t.Fatalf("state after the landing = %q, want %q (the stop must land even while the history append fails)",
			landed.State, session.LifecycleStopped)
	}
	if landed.Generation != generation {
		t.Fatalf("generation after the landing = %d, want %d (a stop never moves the generation)", landed.Generation, generation)
	}
	if landed.StopNote == nil {
		t.Fatalf("the landed record has no stop note — the durable tuple (note+effect) is the recovery anchor the " +
			"refused RESUME must preserve")
	}
	note := *landed.StopNote
	if note.At.IsZero() || note.Seq == 0 || note.Cause != session.StopCauseStop || note.By != historyFailureActor {
		t.Fatalf("landed stop note = {at=%s by=%q seq=%d cause=%q}, want a non-zero original instant, a real "+
			"control-ledger seq, cause %q and actor %q (D6's history tuple source)",
			note.At, note.By, note.Seq, note.Cause, session.StopCauseStop, historyFailureActor)
	}
	if landed.StopEffect == nil || landed.StopEffect.ControlID == "" || landed.StopEffect.Target.Generation != generation {
		t.Fatalf("landed stop effect = %+v, want a control id and target generation %d (the acceptance the history "+
			"belongs to)", landed.StopEffect, generation)
	}
	controlID := landed.StopEffect.ControlID

	// The reader must NOT invent a landed event from the queued intent: the
	// intent line is on the ledger, but only its landed_stop projection is
	// history (D6; founder Q2's "historical events" are landed, never bare
	// intents).
	transitions, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions right after the landing: %v", err)
	}
	if len(transitions) != 0 {
		t.Fatalf("the reader invented %d landed event(s) from a stop whose history append failed: %+v — "+
			"a queued intent is never a landed stop (D6)", len(transitions), transitions)
	}

	// While the ledger stays unwritable, the explicit same-generation RESUME
	// must fail VISIBLY and leave the tuple untouched (D2 CRIT-001: clearing
	// the note without its history is a loss, never a success).
	resumeErr := func() error {
		_, err := NewSteerCanceller(lifecycle).Revive(context.Background(), childID, historyFailureOwner)
		return err
	}()
	if resumeErr == nil {
		t.Fatalf("RESUME succeeded while the landed-stop history could not be written — the resume must be refused " +
			"visibly so the durable note+effect keep the tuple recoverable")
	}
	if !strings.Contains(resumeErr.Error(), controlID) && !strings.Contains(resumeErr.Error(), strconv.FormatInt(int64(note.Seq), 10)) {
		t.Errorf("the RESUME refusal does not name the history it refuses to lose: %v (a visible refusal must "+
			"identify the unrecorded control — id %q or seq %d — not fail anonymously)", resumeErr, controlID, note.Seq)
	}
	afterRefusal, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after refused RESUME): %v", err)
	}
	if afterRefusal.State != session.LifecycleStopped || afterRefusal.StopNote == nil || afterRefusal.StopEffect == nil {
		t.Fatalf("the refused RESUME changed the record: state=%q note=%v effect=%v, want the stopped record "+
			"untouched with the original note+effect", afterRefusal.State, afterRefusal.StopNote, afterRefusal.StopEffect)
	}
	kept := *afterRefusal.StopNote
	if !kept.At.Equal(note.At) || kept.By != note.By || kept.Seq != note.Seq || kept.Cause != note.Cause {
		t.Errorf("the refused RESUME rewrote the stop note: before={at=%s by=%q seq=%d cause=%s} after={at=%s by=%q "+
			"seq=%d cause=%s} — a refused resume must leave the tuple byte-identical",
			note.At, note.By, note.Seq, note.Cause, kept.At, kept.By, kept.Seq, kept.Cause)
	}
	if afterRefusal.StopEffect.ControlID != controlID {
		t.Errorf("the refused RESUME changed the stop effect's control id: %q -> %q",
			controlID, afterRefusal.StopEffect.ControlID)
	}

	// Repair the channel; the retried RESUME succeeds and writes the history
	// in the same lock hold as the clear (D2 CRIT-001).
	historyFailureUnblockLedger(t, controlsPath)
	resumedGeneration, err := NewSteerCanceller(lifecycle).Revive(context.Background(), childID, historyFailureOwner)
	if err != nil {
		t.Fatalf("RESUME after repair: %v", err)
	}
	if resumedGeneration != generation {
		t.Errorf("RESUME returned generation %d, want %d (a stopped child resumes on the SAME generation, D2)", resumedGeneration, generation)
	}
	revived, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(revived): %v", err)
	}
	if revived.State != session.LifecycleQueued {
		t.Errorf("state after the repaired RESUME = %q, want %q", revived.State, session.LifecycleQueued)
	}
	if revived.StopNote != nil || revived.StopEffect != nil {
		t.Errorf("the repaired RESUME left stop metadata behind: note=%v effect=%v, want both cleared atomically with the state change (D2 CRIT-001)",
			revived.StopNote, revived.StopEffect)
	}

	// Exactly ONE original historical event — the original instant, seq,
	// control, cause, actor and the real direct parent — outliving the
	// cleared note (founder Q2=A).
	want := session.StoppedTransition{
		SessionID:       childID,
		ParentSessionID: parentID,
		Generation:      generation,
		StopSeq:         note.Seq,
		ControlID:       controlID,
		Cause:           session.StopCauseStop,
		Actor:           historyFailureActor,
		At:              note.At,
	}
	assertExactlyOneStoppedTransition(t, lifecycle, childID, want)

	// A fresh store open on the same dir returns the same single event.
	reopened := session.NewLifecycleStore(filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle"))
	assertExactlyOneStoppedTransition(t, reopened, childID, want)

	// Idempotent history retry: the exact tuple again is a no-op, not a
	// second history (D4's idempotent-by-seq rule).
	retry := session.LandedStop{
		Seq:             int64(note.Seq),
		ControlID:       controlID,
		ParentSessionID: parentID,
		Generation:      generation,
		Cause:           session.StopCauseStop,
		Actor:           historyFailureActor,
		At:              note.At,
	}
	if err := reopened.RecordLandedStop(childID, retry); err != nil {
		t.Errorf("idempotent RecordLandedStop retry with the exact tuple: %v, want nil", err)
	}
	assertExactlyOneStoppedTransition(t, reopened, childID, want)

	// A divergent retry is a visible refusal, never a silent rewrite.
	divergent := retry
	divergent.Cause = session.StopCauseCascade
	if err := reopened.RecordLandedStop(childID, divergent); err == nil {
		t.Errorf("a divergent landed-stop retry (cause %q vs accepted %q) was accepted — history divergence must be refused visibly",
			session.StopCauseCascade, session.StopCauseStop)
	}
	assertExactlyOneStoppedTransition(t, reopened, childID, want)

	// A second genuine stop of the SAME generation gets a strictly greater
	// seq and its own distinct historical event (D4; the seq is what keeps
	// D6's (parent, child, generation, stop_seq) notice key from colliding).
	if _, err := NewSteerCanceller(lifecycle).StopTurns(context.Background(), childID, historyFailureOwner, false, al.SteerGenerationCancel); err != nil {
		t.Fatalf("second StopTurns: %v", err)
	}
	second, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after second stop): %v", err)
	}
	if second.State != session.LifecycleStopped || second.StopNote == nil {
		t.Fatalf("the second stop did not land stopped with a note: state=%q note=%v", second.State, second.StopNote)
	}
	if second.StopNote.Seq <= note.Seq {
		t.Errorf("second accepted stop's seq = %d, first = %d, want strictly greater — two accepted controls of one "+
			"child at one generation must not share a seq (D4; D6's notice dedup key would drop the second stop's notice)",
			second.StopNote.Seq, note.Seq)
	}
	if second.StopEffect == nil || second.StopEffect.ControlID == controlID {
		t.Errorf("the second stop did not mint a distinct control id: first=%q second-effect=%+v (D4: each accepted "+
			"control has a unique control id)", controlID, second.StopEffect)
	}
	secondTransitions, err := lifecycle.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after second stop): %v", err)
	}
	if len(secondTransitions) != 2 {
		t.Fatalf("history after the second stop = %d events, want 2: %+v", len(secondTransitions), secondTransitions)
	}
	if secondTransitions[0].StopSeq >= secondTransitions[1].StopSeq {
		t.Errorf("history events do not advance: seq %d then %d, want strictly increasing ledger order",
			secondTransitions[0].StopSeq, secondTransitions[1].StopSeq)
	}
	if secondTransitions[1].StopSeq != second.StopNote.Seq || secondTransitions[1].At != second.StopNote.At {
		t.Errorf("the second historical event = {seq %d at %s}, want the second stop's own {seq %d at %s}",
			secondTransitions[1].StopSeq, secondTransitions[1].At, second.StopNote.Seq, second.StopNote.At)
	}
}

// TestHistoryFailure_LandedHistoryEventMissingAndUnsurfacedAtLanding is the
// pack's behavioral RED witness. Spec: when a stop lands, its D6 landed-stop
// history becomes discoverable (the W1 direct-parent notice publisher's
// only source), or the history failure is surfaced by the stop call itself
// — never confined to a log while the caller hears success (D2 round-3
// MAJ-001: a persistence failure is surfaced, never claimed as delivery).
// The ledger is provably unwritable for the whole window, so an honest
// landing CANNOT append the event here; the oracle therefore fails ONLY
// when the event is missing AND nothing was surfaced (visible refusal OR
// event), and whatever does appear is held to the D6 tuple by the negative
// controls below — no forged, duplicated or rewritten history. On the
// pre-change tree the landing swallows the append failure into a WARN log
// and returns void, the event is missing AND nothing was surfaced, and
// this test is red for exactly that reason. It deliberately does NOT
// pretend StopTurns returned an error — it reports the observed return
// values in the failure message.
func TestHistoryFailure_LandedHistoryEventMissingAndUnsurfacedAtLanding(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	lifecycle := al.GetSessionLifecycleStore()

	parentID := newTestSteeringSession(t, al, "ws-histfail-gap")
	rec := launchRunningChild(t, al, parentID, "call-histfail-gap")
	childID, generation := rec.SessionID, rec.Generation
	controlsPath, recordPath := historyFailurePaths(t, al, childID)

	report, stopErr := historyFailureGateStop(t, al, childID, generation, func(t *testing.T) {
		historyFailureBlockLedger(t, controlsPath, recordPath)
	})

	// The ledger is STILL 0400 here: the landing's append already failed
	// inside the joined goroutine, and the read below (ReadFile) works at
	// 0400 — the append could not have succeeded between join and read.
	transitions, listErr := lifecycle.ListStoppedTransitions(childID)
	historyFailureUnblockLedger(t, controlsPath)
	if listErr != nil {
		t.Fatalf("ListStoppedTransitions after the landing: %v", listErr)
	}

	// The durable tuple (note+effect) is the retry anchor: it must have
	// survived the landing whatever the history outcome (D2 CRIT-001 — the
	// landing retains stop_note in the same mutation; boot reconciliation
	// retries the history from exactly this pair). A failed append never
	// justifies a lost note/effect.
	landed, loadErr := lifecycle.Load(childID)
	if loadErr != nil {
		t.Fatalf("Load(landed): %v", loadErr)
	}
	if landed.StopNote == nil || landed.StopEffect == nil {
		t.Fatalf("the landed stop lost its durable tuple: note=%v effect=%v — boot reconciliation retries the "+
			"history from exactly this pair; a failed append never justifies dropping it (D2 CRIT-001)",
			landed.StopNote, landed.StopEffect)
	}
	note := *landed.StopNote
	if note.At.IsZero() || note.Seq == 0 || note.Cause != session.StopCauseStop || note.By != historyFailureActor {
		t.Fatalf("durable stop note = {at=%s by=%q seq=%d cause=%q}, want the original non-zero instant, a real "+
			"control-ledger seq, cause %q and actor %q (the D6 history tuple's source)",
			note.At, note.By, note.Seq, note.Cause, session.StopCauseStop, historyFailureActor)
	}
	if landed.StopEffect.ControlID == "" || landed.StopEffect.Target.Generation != generation {
		t.Fatalf("durable stop effect = %+v, want a control id and target generation %d (the acceptance the "+
			"history belongs to)", landed.StopEffect, generation)
	}

	// "Surfaced" = the stop call itself reported the history failure: a
	// returned error, or a report entry naming the child with a reason about
	// the landed-stop history / control ledger. A WARN log line is not a
	// surfacing channel a caller (or W1) can read. surfacedByReport pins the
	// specific report arm so the naming control below can tell a generic
	// unrelated error from a history-failure surfacing.
	surfacedByReport := false
	for _, u := range report.Unreachable {
		if u.ID == childID &&
			(strings.Contains(u.Reason, "landed-stop history") || strings.Contains(u.Reason, "control ledger")) {
			surfacedByReport = true
		}
	}
	surfaced := surfacedByReport || stopErr != nil

	// The oracle: with the append provably failed, this fails ONLY when the
	// D6 event is missing AND nothing was surfaced — either arm satisfies
	// the spec (visible refusal OR event).
	if len(transitions) == 0 && !surfaced {
		t.Errorf("the landed stop's history is neither discoverable nor surfaced: %d history event(s) after the "+
			"landing (want the D6 event or a visible failure), stopErr=%v, report.Reached=%v, report.Unreachable=%v — "+
			"the append failure was confined to a WARN log while the stop landing reported success (source-verified gap: "+
			"steer_cancel.go::recordLandedStopLedger logs and returns void, and landSteeredStopReport discards it)",
			len(transitions), stopErr, report.Reached, report.Unreachable)
	}

	// Negative controls on the discoverable arm: an event that appears
	// despite the blocked append must be exactly the ONE original D6 tuple —
	// no forged duplicates, no wrong seq, no rewritten instant (D6 keys one
	// notice per (parent, child, generation, stop_seq)).
	if len(transitions) > 1 {
		t.Errorf("forged duplicated history: %d landed-stop events for one accepted control, want at most 1: %+v",
			len(transitions), transitions)
	}
	if len(transitions) == 1 {
		got := transitions[0]
		if got.ParentSessionID != parentID || got.Generation != generation || got.StopSeq != note.Seq ||
			got.Cause != session.StopCauseStop || !got.At.Equal(note.At) ||
			got.ControlID != landed.StopEffect.ControlID || got.Actor != note.By {
			t.Errorf("the single landed-stop event does not match the durable tuple: got {parent=%q gen=%d seq=%d "+
				"cause=%q at=%s control=%q actor=%q}, want {parent=%q gen=%d seq=%d cause=%q at=%s control=%q "+
				"actor=%q} (D6: the history derives from the retained note and the accepted StopEffect, "+
				"At = the original instant)",
				got.ParentSessionID, got.Generation, got.StopSeq, got.Cause, got.At, got.ControlID, got.Actor,
				parentID, generation, note.Seq, session.StopCauseStop, note.At, landed.StopEffect.ControlID, note.By)
		}
	}
	if len(transitions) == 1 && transitions[0].StopSeq == 0 {
		t.Errorf("the reported history event carries no real control seq: %+v — D6's history is keyed by the "+
			"accepted control's seq, not a placeholder", transitions[0])
	}

	// Negative control on the surfaced arm: when no event exists, the
	// surfacing must NAME the ledger/history failure — a generic unrelated
	// error is not a visible surfacing — and the tuple must still sit on the
	// real lifecycle store for the later retry (the assertions above already
	// proved it did).
	if len(transitions) == 0 && surfaced && !surfacedByReport &&
		!strings.Contains(strings.ToLower(stopErr.Error()), "ledger") &&
		!strings.Contains(strings.ToLower(stopErr.Error()), "history") {
		t.Errorf("the stop call returned an error that does not name the control-ledger/landed-stop-history "+
			"failure it must surface: %v — a generic unrelated error is not a visible surfacing of the history "+
			"append failure", stopErr)
	}
}

// assertExactlyOneStoppedTransition pins the full D6 history shape: exactly
// one event, equal on every keyed field (parent, child, generation, seq,
// control, cause, actor, original At).
func assertExactlyOneStoppedTransition(t *testing.T, store *session.LifecycleStore, childID string, want session.StoppedTransition) {
	t.Helper()
	transitions, err := store.ListStoppedTransitions(childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(%q): %v", childID, err)
	}
	if len(transitions) != 1 {
		t.Fatalf("history for %q = %d events, want exactly 1: %+v", childID, len(transitions), transitions)
	}
	got := transitions[0]
	if got != want {
		t.Fatalf("history event mismatch:\n got {session=%q parent=%q gen=%d seq=%d control=%q cause=%q actor=%q at=%s}\nwant {session=%q parent=%q gen=%d seq=%d control=%q cause=%q actor=%q at=%s}",
			got.SessionID, got.ParentSessionID, got.Generation, got.StopSeq, got.ControlID, got.Cause, got.Actor, got.At,
			want.SessionID, want.ParentSessionID, want.Generation, want.StopSeq, want.ControlID, want.Cause, want.Actor, want.At)
	}
}
