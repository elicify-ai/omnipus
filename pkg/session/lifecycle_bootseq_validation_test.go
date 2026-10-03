package session

// RED pack V — persist-time stop_note.boot_seq validation (prerequisite E).
//
// Oracles derive from the specification, never from an implementation:
//   - a-boot-durability-correction-20261002T1130 REPORT §6 (the exactly-one
//     correction, superseding the 1100 report §1 R1/R2 phrasing):
//       R1-range — ACTOR-AGNOSTIC: any populated BootSeq (uint64 != 0;
//       omitempty makes 0 absent) above math.MaxInt64 must be refused at
//       persist, because the wire (contracts/components/schemas/StopNote.yaml
//       boot_seq: integer, format: int64, minimum 1) and Hard Constraint #8
//       make an int64 the only legal cross-boundary type.
//       R1-requiredness — ACTOR-KEYED (ADR-20260928 @cd20cf8b D8.3: the
//       boot-restart note literal is {at, by: "restart", seq, cause:
//       "restart", boot_seq}): By == StopActorRestart with BootSeq == 0 must
//       be refused.
//       R2-preservation — deliberately NO new restrictions: (a) the C3
//       writer shape By=StopActorSystem + Cause=StopCauseRestart with no
//       BootSeq (pkg/agent/task_executor_judge.go::supersedeTaskSession)
//       stays valid; (b) By=StopActorRestart with a cause other than restart
//       is NOT forbidden — no actor⇒cause coupling rule may be introduced;
//       (c) a non-restart actor MAY populate a legal BootSeq.
//   - Enforcement point: validateLifecycleRecordForPersist via the store's
//     single write funnel persistLocked (correction §6 [REC] — the ADR is
//     silent on the point; this pack exercises the public Persist).
//
// Status at this pin (a2eb202dedfb3…): validateLifecycleRecordForPersist
// (pkg/session/lifecycle.go) checks the cause enum (…:583) and that a
// stopped record carries a note (…:575) but has NO BootSeq check, so V1, V3,
// V3b and the MaxInt64+1 boundary are behavioral REDs today — they compile,
// run, and fail with "expected persist error, got nil". V2, V4, the MaxInt64
// boundary and the R2 pin are anti-over-blocking green pins: green today,
// and they must REMAIN green after the validation lands.
//
// Mutation targets for CHECK (not run in RED): drop the range check (V3/V3b/
// max-plus-one die), `>` mutated to `>=` (the MaxInt64-accepted boundary
// dies), drop the restart requiredness (V1 dies), requiredness keyed on the
// wrong actor (V1 dies), an actor⇒cause rule added (the R2 pin dies), C3
// shape rejected (V4 dies).

import (
	"math"
	"strings"
	"testing"
	"time"
)

// persistNoteRecord persists one LifecycleStopped record carrying the given
// StopNote through the store's real single write funnel (Persist →
// persistLocked → validateLifecycleRecordForPersist). The record shape
// mirrors the committed fixture in lifecycle_stopped_test.go.
func persistNoteRecord(t *testing.T, note *StopNote) error {
	t.Helper()
	store := NewLifecycleStore(t.TempDir())
	rec := &LifecycleRecord{
		SessionID:      "bootseq-note",
		Generation:     1,
		State:          LifecycleStopped,
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "workspace-1",
		AgentID:        "agent-1",
		StopNote:       note,
	}
	return store.Persist(rec)
}

// TestLifecyclePersist_RestartNoteWithoutBootSeqRejected (V1, behavioral
// RED today): a boot-restart note without a boot epoch violates D8.3's note
// literal and must be refused at persist, with an error naming boot_seq
// (correction §6 R1-requiredness).
func TestLifecyclePersist_RestartNoteWithoutBootSeqRejected(t *testing.T) {
	err := persistNoteRecord(t, &StopNote{
		At:    time.Now().UTC(),
		By:    StopActorRestart,
		Seq:   1,
		Cause: StopCauseRestart,
		// BootSeq deliberately zero: the boot that wrote this note never
		// minted, which is exactly what R1-requiredness exists to catch.
	})
	if err == nil {
		t.Fatal("persist of a by=restart note with BootSeq=0 must be refused, got nil error (R1-requiredness, correction §6)")
	}
	if !strings.Contains(err.Error(), "boot_seq") {
		t.Fatalf("refusal must name boot_seq (correction §6: error naming both fields), got: %v", err)
	}
}

// TestLifecyclePersist_RestartNoteWithBootSeqOneAccepted (V2, green pin):
// the minimal legal boot-restart note — BootSeq 1 is the wire minimum —
// must persist (correction §6: boot-restart notes carry boot_seq >= 1).
// Anti-over-blocking guard: stays green after the validation lands.
func TestLifecyclePersist_RestartNoteWithBootSeqOneAccepted(t *testing.T) {
	err := persistNoteRecord(t, &StopNote{
		At:      time.Now().UTC(),
		By:      StopActorRestart,
		Seq:     1,
		Cause:   StopCauseRestart,
		BootSeq: 1,
	})
	if err != nil {
		t.Fatalf("persist of a legal by=restart note with BootSeq=1 must succeed, got: %v", err)
	}
}

// TestLifecyclePersist_BootSeqMaxInt64Accepted (boundary max, green pin):
// MaxInt64 is the top representable wire value (format: int64), so a note
// at exactly MaxInt64 is inside R1-range and must persist. Kills the `>`
// mutated to `>=` off-by-one in CHECK.
func TestLifecyclePersist_BootSeqMaxInt64Accepted(t *testing.T) {
	err := persistNoteRecord(t, &StopNote{
		At:      time.Now().UTC(),
		By:      StopActorSystem,
		Seq:     1,
		Cause:   StopCauseTimeout,
		BootSeq: math.MaxInt64,
	})
	if err != nil {
		t.Fatalf("persist with BootSeq=MaxInt64 (top legal wire value) must succeed, got: %v", err)
	}
}

// TestLifecyclePersist_BootSeqMaxInt64PlusOneRejected (boundary max+1,
// behavioral RED today): the first value above the int64 wire range must be
// refused for a restart-actor note (correction §6 R1-range; Hard
// Constraint #8).
func TestLifecyclePersist_BootSeqMaxInt64PlusOneRejected(t *testing.T) {
	err := persistNoteRecord(t, &StopNote{
		At:      time.Now().UTC(),
		By:      StopActorRestart,
		Seq:     1,
		Cause:   StopCauseRestart,
		BootSeq: uint64(math.MaxInt64) + 1,
	})
	if err == nil {
		t.Fatal("persist with BootSeq=MaxInt64+1 must be refused (outside the int64 wire), got nil error")
	}
	if !strings.Contains(err.Error(), "boot_seq") {
		t.Fatalf("refusal must name boot_seq, got: %v", err)
	}
}

// TestLifecyclePersist_BootSeqAboveMaxInt64RejectedOnRestartActor (V3,
// behavioral RED today): MaxUint64 on a by=restart note is far outside the
// int64 wire and must be refused (correction §6 R1-range; 1100 §5 V3 with
// the corrected rationale).
func TestLifecyclePersist_BootSeqAboveMaxInt64RejectedOnRestartActor(t *testing.T) {
	err := persistNoteRecord(t, &StopNote{
		At:      time.Now().UTC(),
		By:      StopActorRestart,
		Seq:     1,
		Cause:   StopCauseRestart,
		BootSeq: math.MaxUint64,
	})
	if err == nil {
		t.Fatal("persist with BootSeq=MaxUint64 on a by=restart note must be refused, got nil error (R1-range)")
	}
	if !strings.Contains(err.Error(), "boot_seq") {
		t.Fatalf("refusal must name boot_seq, got: %v", err)
	}
}

// TestLifecyclePersist_BootSeqAboveMaxInt64RejectedOnSystemActor (V3b,
// behavioral RED today): the same out-of-range value on a by=system note
// must ALSO be refused — this is the pin that makes R1-range
// actor-agnostic; V3 alone does not (correction §7 V3b).
func TestLifecyclePersist_BootSeqAboveMaxInt64RejectedOnSystemActor(t *testing.T) {
	err := persistNoteRecord(t, &StopNote{
		At:      time.Now().UTC(),
		By:      StopActorSystem,
		Seq:     1,
		Cause:   StopCauseTimeout,
		BootSeq: math.MaxUint64,
	})
	if err == nil {
		t.Fatal("persist with BootSeq=MaxUint64 on a by=system note must be refused too (R1-range is actor-agnostic), got nil error")
	}
	if !strings.Contains(err.Error(), "boot_seq") {
		t.Fatalf("refusal must name boot_seq, got: %v", err)
	}
}

// TestLifecyclePersist_SystemRestartNoteWithoutBootSeqAccepted (V4, green
// pin — the C3 anti-misvalidation guard): the goal loop's attempt
// supersession writes by=system + cause=restart with NO BootSeq
// (task_executor_judge.go::supersedeTaskSession); R1-requiredness keys on
// the by=restart ACTOR, so this shape must keep persisting. If this ever
// goes red, the validation read cause=restart as proof of a boot restart —
// exactly what lifecycle_edge.go::StopActorRestart's doc forbids.
func TestLifecyclePersist_SystemRestartNoteWithoutBootSeqAccepted(t *testing.T) {
	err := persistNoteRecord(t, &StopNote{
		At:    time.Now().UTC(),
		By:    StopActorSystem,
		Seq:   1,
		Cause: StopCauseRestart,
		// No BootSeq: this writer predates the boot-epoch stamping and must
		// not be forced to carry one (correction §6 R2-preservation a).
	})
	if err != nil {
		t.Fatalf("persist of the C3 shape (by=system, cause=restart, no BootSeq) must keep succeeding, got: %v", err)
	}
}

// TestLifecyclePersist_RestartActorMayCarryNonRestartCause (R2 pin, green
// pin): the correction deliberately introduces NO actor⇒cause coupling — a
// by=restart note with a non-restart cause is valid today (the validator
// only enum-checks the cause) and must stay valid (correction §6
// R2-preservation b). BootSeq is populated so R1-requiredness is not in
// play; only a fabricated actor⇒cause rule could refuse this record.
func TestLifecyclePersist_RestartActorMayCarryNonRestartCause(t *testing.T) {
	err := persistNoteRecord(t, &StopNote{
		At:      time.Now().UTC(),
		By:      StopActorRestart,
		Seq:     1,
		Cause:   StopCauseStop,
		BootSeq: 7,
	})
	if err != nil {
		t.Fatalf("persist of by=restart + cause=stop must succeed (no actor⇒cause rule may be introduced, correction §6 R2), got: %v", err)
	}
}
