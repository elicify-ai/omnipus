// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W2 D4 RED pack, part 1 — the real stop_seq (ADR-20260928 sub-agent
// control plane, frozen asset cd20cf8b). Every expected value below is
// derived from the ADR, never from the code under test:
//
//   - D4: "Each accepted control gets a per-child seq (monotonic, assigned
//     under the child's lifecycle lock)." Two accepted stop transitions of
//     one child therefore carry distinct, strictly increasing stop seqs.
//   - D6: the stopped-child notice dedup key is
//     (parent_id, child_id, child_generation, stop_seq) — two stops of one
//     child sharing one seq collide that key, and the second stop's parent
//     notice is silently deduplicated away.
//   - D2 CRIT-001: an explicit RESUME of a landed stopped child keeps the
//     generation and clears the note and any current fence (the existing
//     TestT27_StoppedResume_KeepsGenerationAndQueues oracle, re-pinned here
//     as this scenario's spine).
//   - D2 stop table: a stop of an ALREADY stopped child answers "already
//     stopped" and writes no ledger line (MIN-007) — the retained note is
//     untouched and no current fence is left behind.
//   - Vocabulary: a landed stopped record has no current-generation Stop
//     marker and retains its separate stop_note.
//   - D6 (goal row, MAJ-003): every session-owned active goal stays active
//     across a stop.
//
// Known-red at the pinned HEAD by design (RED, not a defect of these
// tests): SteerCanceller.stampStop (pkg/agent/steer_cancel.go) writes
// StopNote.Seq = uint64(generation), and pkg/agent/steer_frames.go's
// ControlReceipt comment documents that stand-in ("The per-session control
// ledger ... is NOT built in this PR ... Seq is stamped as a documented
// stand-in"). This file is the W2a receipt the ledger backend must turn
// green.
//
// Deferred for want of the production carrier, deliberately NOT faked here:
//   - parent-notice-id distinctness: the D6 stopped-child notice deliverer
//     is not wired in this tree (landSteeredStopReport publishes nothing
//     upward by design until that unit lands).
//   - "two distinct accepted stop identities in the finalized control
//     ledger after a fresh store open": no control-ledger type exists to
//     reference; the fresh-reopen durability half below pins the note the
//     future ledger must outlive.
package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// stopSeqStopTurn is the production single-session stop adapter the gateway
// Stop path supplies to StopTurns (websocket_stop_scope.go): the cascade
// stamps the fence/note, then this adapter carries the live effect. A
// never-ran child has no live turn, so the production never-ran finalizer
// (terminaliseNeverRanStop -> reportSteeredSessionTerminalUpward ->
// landSteeredStopReport) lands it stopped — the same path a queued child's
// real stop takes.
func stopSeqStopTurn(al *AgentLoop) GenerationCancelFunc {
	return al.SteerGenerationCancel
}

func TestStopSeq_SecondAcceptedStopAfterSameGenerationResumeHasStrictlyGreaterSeq(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	lifecycle := al.GetSessionLifecycleStore()

	parentID := newTestSteeringSession(t, al, "ws-stop-seq")
	rec := launchRunningChild(t, al, parentID, "call-stop-seq-1")
	childID := rec.SessionID
	generation := rec.Generation

	// D6 (goal row): the child's own active goal must survive every stop.
	seeded := seedActiveGoalRecord(t, childID, "delegated work goal", nil, nil)
	if gotGoal, goalErr := activeGoalForSession(childID); goalErr != nil || gotGoal == nil || gotGoal.GoalID != seeded.GoalID {
		t.Fatalf("setup: no active goal bound to the child before any stop: goal=%v err=%v", gotGoal, goalErr)
	}

	owner := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "stop-seq-owner"}
	canceller := NewSteerCanceller(lifecycle)
	stopTurn := stopSeqStopTurn(al)

	if _, err := canceller.StopTurns(context.Background(), childID, owner, false, stopTurn); err != nil {
		t.Fatalf("first StopTurns: %v", err)
	}
	first, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after first stop): %v", err)
	}
	if first.State != session.LifecycleStopped || first.Generation != generation || first.StopNote == nil {
		t.Fatalf("setup: first stop did not land stopped at generation %d with a note: state=%q generation=%d note=%v",
			generation, first.State, first.Generation, first.StopNote)
	}
	seq1 := first.StopNote.Seq
	if gotGoal, goalErr := activeGoalForSession(childID); goalErr != nil || gotGoal == nil || gotGoal.GoalID != seeded.GoalID {
		t.Errorf("the child's active goal did not survive the first stop: goal=%v err=%v (D6: a stop never ends a goal)", gotGoal, goalErr)
	}

	resumed, err := canceller.Revive(context.Background(), childID, owner)
	if err != nil {
		t.Fatalf("Revive: %v", err)
	}
	resumedRec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after resume): %v", err)
	}
	if resumed != generation || resumedRec.Generation != generation {
		t.Fatalf("setup: RESUME moved the generation: returned %d record %d, want %d (D2 CRIT-001: a stopped child resumes on the same generation)",
			resumed, resumedRec.Generation, generation)
	}
	if resumedRec.State != session.LifecycleQueued || resumedRec.StopNote != nil {
		t.Fatalf("setup: record after RESUME = state %q note %v, want queued with the note cleared", resumedRec.State, resumedRec.StopNote)
	}

	if _, err := canceller.StopTurns(context.Background(), childID, owner, false, stopTurn); err != nil {
		t.Fatalf("second StopTurns: %v", err)
	}
	second, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after second stop): %v", err)
	}
	if second.State != session.LifecycleStopped || second.Generation != generation || second.StopNote == nil {
		t.Fatalf("setup: second stop did not land stopped at generation %d with a note: state=%q generation=%d note=%v",
			generation, second.State, second.Generation, second.StopNote)
	}
	seq2 := second.StopNote.Seq
	if seq2 <= seq1 {
		t.Errorf("second accepted stop's seq = %d, first stop's seq = %d, want strictly greater — "+
			"the two stops are two accepted controls at one generation, so their stop identities collide: "+
			"D4 requires a monotonic per-child seq and D6's notice dedup key (parent, child, generation, stop_seq) "+
			"would silently drop the second stop's parent notice", seq2, seq1)
	}
	if gotGoal, goalErr := activeGoalForSession(childID); goalErr != nil || gotGoal == nil || gotGoal.GoalID != seeded.GoalID {
		t.Errorf("the child's active goal did not survive both stops: goal=%v err=%v (D6: a stop never ends a goal)", gotGoal, goalErr)
	}

	// Durability half of the ledger assertion: the second stop's identity
	// must survive a fresh store open. (The full assertion — two distinct
	// accepted stop identities in the finalized control ledger — waits on
	// the ledger backend; no such type exists yet and none is faked here.)
	reopened := session.NewLifecycleStore(filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle"))
	afterReopen, err := reopened.Load(childID)
	if err != nil {
		t.Fatalf("Load(reopened store): %v", err)
	}
	if afterReopen.StopNote == nil || afterReopen.StopNote.Seq != seq2 {
		t.Errorf("the second stop's note did not survive a fresh store open: note=%v, want seq %d",
			afterReopen.StopNote, seq2)
	}
}

func TestStopSeq_StopWhileAlreadyStoppedIsIdempotent(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	lifecycle := al.GetSessionLifecycleStore()

	parentID := newTestSteeringSession(t, al, "ws-stop-seq-idem")
	rec := launchRunningChild(t, al, parentID, "call-stop-seq-idem")
	childID, generation := rec.SessionID, rec.Generation

	owner := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "stop-seq-owner"}
	canceller := NewSteerCanceller(lifecycle)
	stopTurn := stopSeqStopTurn(al)

	if _, err := canceller.StopTurns(context.Background(), childID, owner, false, stopTurn); err != nil {
		t.Fatalf("first StopTurns: %v", err)
	}
	first, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after first stop): %v", err)
	}
	if first.State != session.LifecycleStopped || first.StopNote == nil {
		t.Fatalf("setup: first stop did not land stopped with a note: state=%q note=%v", first.State, first.StopNote)
	}
	retained := *first.StopNote

	// D2 stop table: "stopped -> 'already stopped'; no ledger line (MIN-007)".
	// The call must be accepted and change nothing durable.
	if _, err := canceller.StopTurns(context.Background(), childID, owner, false, stopTurn); err != nil {
		t.Fatalf("repeat StopTurns on an already stopped child: %v", err)
	}
	after, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(after repeat stop): %v", err)
	}
	if after.State != session.LifecycleStopped {
		t.Fatalf("state after the repeat stop = %q, want still stopped", after.State)
	}
	if after.StopNote == nil {
		t.Fatalf("the retained stop note vanished after the repeat stop — D2 requires the landed note to be untouched")
	}
	got := *after.StopNote
	if !got.At.Equal(retained.At) || got.By != retained.By || got.Seq != retained.Seq || got.Cause != retained.Cause {
		t.Errorf("the repeat stop rewrote the retained stop note: before={at=%s by=%q seq=%d cause=%s} after={at=%s by=%q seq=%d cause=%s} — "+
			"a stop of an already stopped child is \"already stopped\" and writes no ledger line (D2/MIN-007)",
			retained.At, retained.By, retained.Seq, retained.Cause,
			got.At, got.By, got.Seq, got.Cause)
	}
	if after.Stop != nil && after.Stop.Generation == after.Generation {
		t.Errorf("the repeat stop left a current-generation Stop fence on a LANDED stopped record: %+v — "+
			"a landed stopped record has no current-generation marker (Vocabulary)", after.Stop)
	}
}
