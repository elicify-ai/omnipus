// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-20260928 sub-agent control plane (asset cd20cf8b), D4/D6 — the
// per-session control ledger's write half: the stop-control allocator and
// the durable landed-stop history writer.
//
// D4: every accepted control gets a per-child monotonic seq (assigned under
// the child's lifecycle lock) and a unique control_id, and the ledger line
// is durable BEFORE the effect it orders. AcceptStopControl is that one
// non-reentrant store operation for the stop verb: it holds the session's
// lifecycle lock once, refuses the D2 idempotent no-op cases WITHOUT
// touching the ledger, allocates seq/control_id, appends the acceptance
// intent, and only then stamps and persists the record via persistLocked —
// the same lock hold. A stamp or persist refusal after a durable intent is
// exactly D4's crash state: a queued intent W3 boot reconciliation finishes,
// never a half-stamped stop.
//
// RecordLandedStop is D6's historical landed-stop projection: it appends a
// landed_stop line for an already-accepted control once the lifecycle state
// is durably stopped. It is NOT the final public receipt — the applied
// state waits for the D6 direct-parent notice's durability and is written
// by a later unit. A queued intent alone is never a landed stop, and this
// method never marks one.
//
// The ledger file lives in a dedicated "controls" subdirectory of the
// lifecycle dir: both flat-directory scanners over that dir
// (lifecycle.go::scanSessionIDs, boot_sweep.go::SteerBootRecovery.sessionIDs)
// read *.jsonl FILES and skip directories, so a sidecar beside the records
// would phantom-register as a session id. No scanner was patched to hide
// the sidecar; the directory is the non-collision.
//
// not-wire-format: internal storage only. The SPA receipt remains the
// generated ControlReceipt field. No new gateway byte is defined here.
package session

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// controlVerbStop is the ledger's verb literal for the stop control (the
// D4 verb enum's "stop"). Other verbs are later units on this ledger.
const controlVerbStop = "stop"

// controlStateQueued is the acceptance state of a control whose effect has
// not been finalized. A landed stop keeps this state: the final "applied"
// receipt cannot precede the D6 direct-parent notice's durability (D4/D6),
// so the landed-stop history carries its own landed_stop projection instead
// of borrowing the final state.
const controlStateQueued = "queued"

// StopEffectTarget is the execution-identity value the stop selected
// (D2 round-4 R4-MAJ-001). W2a carries the typed parameter with honest
// zero values until the runtime identity carrier lands: Exec/RunID are
// absent (never fabricated) and BootSeq stays 0 (omitted on disk) until
// the boot-epoch store exists. Generation is the child's generation the
// stop targets — always real.
//
// not-wire-format: internal storage only.
type StopEffectTarget struct {
	Generation int    `json:"generation"`
	Exec       uint64 `json:"exec,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	BootSeq    uint64 `json:"boot_seq,omitempty"`
}

// Selected reports whether the stop captured a concrete execution. The zero
// target is a real state — a stop of a child that never admitted a run —
// and is distinct from "the identity carrier has not landed yet"; the
// carrier's later integration fills Exec/RunID in the same type.
func (t StopEffectTarget) Selected() bool { return t.Exec != 0 || t.RunID != "" }

// StopEffect is the lifecycle-internal stop targeting metadata recorded
// WITH the fence/note (D2 round-4 R4-MAJ-001): which accepted control this
// stop is, and which execution it selected. It is how a delayed stop effect
// proves it is still carrying its own control's target, and how a landing
// resolves the ledger intent its landed-stop history belongs to. An explicit
// RESUME clears it atomically with the note (D2 CRIT-001); landing retains
// it so the landing half can write the landed-stop history.
//
// not-wire-format: internal storage only — not part of the generated
// SessionLifecycleRecord schema, like FinalDelivery.
type StopEffect struct {
	ControlID string           `json:"control_id"`
	Target    StopEffectTarget `json:"target"`
}

// ControlGrant identifies one accepted control: the per-child monotonic
// sequence and the unique control id, both assigned under the child's
// lifecycle lock (D4).
type ControlGrant struct {
	Seq       int64
	ControlID string
}

// StopControlIntent is the acceptance-time content of a stop control (D4's
// ledger line: actor/cause/accepted_at beside the allocated seq/control_id).
type StopControlIntent struct {
	Cause StopCause
	Actor string
	// AcceptedAt is the acceptance instant. Zero means "now" — the store
	// stamps its own clock rather than inventing a caller-supplied one.
	AcceptedAt time.Time
}

// StopAcceptOutcome reports what AcceptStopControl did. The two Already
// outcomes and Terminal are D2/MIN-007's idempotent refusals: nothing was
// allocated, nothing was appended, the record is untouched.
type StopAcceptOutcome uint8

const (
	// StopAcceptGranted: seq/control_id allocated, intent line durable, and
	// (when the error is nil) the record stamped and persisted in the same
	// lock hold. A non-nil error with this outcome means the intent IS on
	// the ledger but the stamp/persist was refused — D4's queued-intent
	// crash state, returned visibly.
	StopAcceptGranted StopAcceptOutcome = iota + 1
	// StopAcceptAlreadyStamped: a live current-generation fence exists —
	// the idempotent in-flight shape. No ledger line, no seq.
	StopAcceptAlreadyStamped
	// StopAcceptAlreadyLanded: the record already landed LifecycleStopped
	// (fence spent, note retained). "Already stopped" (D2 stop table,
	// MIN-007): no ledger line, no seq, no note rewrite, no new fence.
	StopAcceptAlreadyLanded
	// StopAcceptTerminal: the record is terminal (done/failed). No ledger
	// line, no seq.
	StopAcceptTerminal
	// StopAcceptMissing: no lifecycle record exists for the session.
	StopAcceptMissing
)

// LandedStop is the durable historical record of one stop that actually
// landed (D6): written ONLY after the lifecycle state stopped is durable,
// keyed to the control that ordered it. ParentSessionID is the original
// direct parent at landing time — empty for a root stop, which is history
// the W1 notice reader skips, never a parent it invents. At is the stop's
// ORIGINAL note instant, never a reconstruction.
//
// A FENCE-LESS stop (Correction C3) carries ControlID empty and names its
// landing execution in RunID/BootSeq instead — it fabricates no accepted
// Stop control, and its ledger line's sequence (allocated by
// RecordFencelessLandedStopLocked, never supplied by the caller) is the
// transition's stop_seq. Seq is set by the store for both shapes: the
// fenced writer copies the accepted control's sequence, the fence-less
// writer allocates the next monotonic one.
//
// not-wire-format: internal storage only.
type LandedStop struct {
	Seq             int64
	ControlID       string
	ParentSessionID string
	Generation      int
	Cause           StopCause
	Actor           string
	At              time.Time
	RunID           string
	BootSeq         uint64
}

// newControlID mints a unique control id: a ULID under the "ctl_" prefix,
// the same minting pattern as daypartition.go::NewSessionID. Uniqueness
// comes from the ULID; the call site allocates it under the child's
// lifecycle lock as D4 requires.
func newControlID() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now()), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("session: control ledger: generate control id: %w", err)
	}
	return "ctl_" + id.String(), nil
}

// appendControlLineLocked appends one line to the session's control ledger.
// The caller MUST hold the session's lifecycle lock (the ledger is atomic
// per session under that lock); AppendJSONL creates the "controls"
// subdirectory itself and fsyncs the append.
func appendControlLineLocked(s *LifecycleStore, sessionID string, line controlLedgerLine) error {
	return fileutil.AppendJSONL(s.controlLedgerPath(sessionID), line)
}

// controlSeqHighWaterLocked returns the highest seq on ANY line of the
// session's ledger — accepted, landed, superseded, applied alike. The
// per-child monotonic guarantee (D4) reads the durable truth, not a cache,
// so a fresh store open (or another process's compaction that preserved the
// high-water) cannot reuse a sequence.
func (s *LifecycleStore) controlSeqHighWaterLocked(sessionID string) (int64, error) {
	lines, err := s.readControlLedgerLocked(sessionID)
	if err != nil {
		return 0, err
	}
	var high int64
	for _, line := range lines {
		if line.Seq > high {
			high = line.Seq
		}
	}
	return high, nil
}

// readControlLedgerLocked reads and parses the session's whole ledger under
// the caller's lock. A missing file is an empty ledger. The torn-tail rule
// is the reader's own (lifecycle_control_ledger.go::parseControlLedger):
// only an unterminated final line is skipped; a newline-terminated line
// that does not parse is a visible error.
func (s *LifecycleStore) readControlLedgerLocked(sessionID string) ([]controlLedgerLine, error) {
	raw, err := os.ReadFile(s.controlLedgerPath(sessionID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: control ledger: open %q: %w", sessionID, err)
	}
	return parseControlLedger(sessionID, raw)
}

// AcceptStopControl is the stop verb's D4 acceptance: ONE non-reentrant
// store operation that allocates the control's seq and control_id under the
// session's lifecycle lock, appends the acceptance line to the per-session
// control ledger FIRST, and only then runs stamp and persists the stamped
// record via persistLocked — all inside the same lock hold the brief's
// ledger-first ordering requires (LifecycleStore.Mutate cannot be used here:
// it reacquires the lock).
//
// The D2 idempotent refusals are checked before anything is allocated:
// an in-flight current-generation fence (AlreadyStamped), an already-landed
// stopped record (AlreadyLanded, MIN-007's "already stopped" — no ledger
// line, no seq, the retained note untouched), a terminal record (Terminal)
// and a missing record (Missing) all return without writing.
//
// stamp receives the record COPY to mutate (the caller writes the fence, the
// lasting note with Seq = grant.Seq, and the StopEffect) plus the grant and
// the captured target. A stamp error — or a persistLocked refusal after the
// intent was appended — leaves the durable queued intent on the ledger for
// boot reconciliation (D4 crash semantics) and returns the error visibly
// with StopAcceptGranted: the intent exists, the fence does not.
//
// The returned generation is the record's generation for every outcome that
// read one (the caller's live-effect step needs it even on the idempotent
// no-ops, which never run stamp).
func (s *LifecycleStore) AcceptStopControl(
	sessionID string,
	intent StopControlIntent,
	stamp func(rec *LifecycleRecord, grant ControlGrant, target StopEffectTarget) error,
) (StopAcceptOutcome, ControlGrant, int, error) {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return 0, ControlGrant{}, 0, err
	}
	if !IsValidStopCause(intent.Cause) {
		return 0, ControlGrant{}, 0, fmt.Errorf("session: control ledger: invalid stop cause %q", intent.Cause)
	}
	if intent.Actor == "" {
		return 0, ControlGrant{}, 0, fmt.Errorf("session: control ledger: stop control requires an actor")
	}
	acceptedAt := intent.AcceptedAt
	if acceptedAt.IsZero() {
		acceptedAt = time.Now().UTC()
	}

	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	cur, found, err := s.tail(sessionID)
	if err != nil {
		return 0, ControlGrant{}, 0, err
	}
	if !found {
		return StopAcceptMissing, ControlGrant{}, 0, nil
	}
	generation := cur.Generation
	if cur.Terminal() {
		return StopAcceptTerminal, ControlGrant{}, generation, nil
	}
	if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
		return StopAcceptAlreadyStamped, ControlGrant{}, generation, nil
	}
	if cur.State == LifecycleStopped {
		return StopAcceptAlreadyLanded, ControlGrant{}, generation, nil
	}

	highWater, err := s.controlSeqHighWaterLocked(sessionID)
	if err != nil {
		return 0, ControlGrant{}, generation, err
	}
	controlID, err := newControlID()
	if err != nil {
		return 0, ControlGrant{}, generation, err
	}
	grant := ControlGrant{Seq: highWater + 1, ControlID: controlID}
	// The execution the stop selected, copied under this same lock from the
	// admission already stamped on the record. Exec stays unset: nothing here
	// mints a second id. An unadmitted record keeps an empty run and a zero
	// boot sequence — those are not filled from the generation.
	target := StopEffectTarget{Generation: generation}
	if cur.ExecutionID != nil && cur.ExecutionID.RunID != "" && cur.ExecutionID.BootSeq != 0 {
		target.RunID = cur.ExecutionID.RunID
		target.BootSeq = cur.ExecutionID.BootSeq
	}
	line := controlLedgerLine{
		Seq:        grant.Seq,
		ControlID:  grant.ControlID,
		Verb:       controlVerbStop,
		State:      controlStateQueued,
		AcceptedAt: acceptedAt,
		Generation: generation,
		Cause:      intent.Cause,
		Actor:      intent.Actor,
		StopEffect: &StopEffect{ControlID: grant.ControlID, Target: target},
	}
	if err := appendControlLineLocked(s, sessionID, line); err != nil {
		return 0, ControlGrant{}, generation, fmt.Errorf("session: control ledger: append acceptance for %q: %w", sessionID, err)
	}

	if stamp != nil {
		next := *cur
		if err := stamp(&next, grant, target); err != nil {
			return StopAcceptGranted, grant, generation, fmt.Errorf(
				"session: control ledger: stop stamp refused for %q seq %d (durable intent kept for boot reconciliation): %w",
				sessionID, grant.Seq, err)
		}
		if err := s.persistLocked(&next); err != nil {
			return StopAcceptGranted, grant, generation, fmt.Errorf(
				"session: control ledger: stop persist refused for %q seq %d (durable intent kept for boot reconciliation): %w",
				sessionID, grant.Seq, err)
		}
	}
	return StopAcceptGranted, grant, generation, nil
}

// RecordLandedStop appends the landed-stop history line for one accepted
// stop control — the D6 historical landed-stop projection a W1 notice
// publisher discovers. It is called ONLY after the lifecycle record is
// durably stopped (the caller's landing half guarantees that ordering; this
// method deliberately does not re-derive it).
//
// The append is idempotent by seq: a retry after a crash between the landed
// lifecycle and this line verifies the recorded tuple and returns nil
// instead of duplicating. A seq with no accepted intent, or a landed tuple
// diverging from its intent (control id, generation, cause, actor), is a
// visible error — never a silent rewrite and never a fabricated history.
func (s *LifecycleStore) RecordLandedStop(sessionID string, landed LandedStop) error {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	return s.RecordLandedStopLocked(sessionID, landed)
}

// RecordLandedStopLocked is RecordLandedStop's under-lock half.
//
// The caller MUST already hold the session's lifecycle lock — from inside a
// LifecycleStore.Mutate callback, or between Lock and Unlock. It exists for
// the transition that would otherwise DESTROY the last reconstructable
// landed-history tuple: D2 CRIT-001's resume clears the stop note and the
// stop-effect metadata, so a resume whose record still carries an unrecorded
// landed history must persist that history IN THE SAME LOCK HOLD before the
// clear — no window in which the tuple is gone and the ledger does not yet
// carry it. If the append fails, the resume is refused visibly and the
// durable note+effect keep the tuple recoverable for the retry.
func (s *LifecycleStore) RecordLandedStopLocked(sessionID string, landed LandedStop) error {
	if landed.Seq <= 0 {
		return fmt.Errorf("session: control ledger: landed stop seq %d is not a positive control sequence", landed.Seq)
	}
	if landed.ControlID == "" {
		return fmt.Errorf("session: control ledger: landed stop requires control_id")
	}
	if landed.Generation < 1 {
		return fmt.Errorf("session: control ledger: landed stop generation must be >= 1, got %d", landed.Generation)
	}
	if landed.Actor == "" {
		return fmt.Errorf("session: control ledger: landed stop requires an actor")
	}
	if landed.At.IsZero() {
		return fmt.Errorf("session: control ledger: landed stop requires the stop's original at instant")
	}
	if !IsValidStopCause(landed.Cause) {
		return fmt.Errorf("session: control ledger: invalid landed stop cause %q", landed.Cause)
	}

	lines, err := s.readControlLedgerLocked(sessionID)
	if err != nil {
		return err
	}
	var intent *controlLedgerLine
	for i := range lines {
		line := lines[i]
		if line.Seq != landed.Seq {
			continue
		}
		if line.LandedStop != nil {
			// Idempotent retry: the history line already exists. The tuple
			// must match — a divergent retry is a visible failure, not a
			// second history.
			if err := verifyLandedTuple(line, landed); err != nil {
				return err
			}
			return nil
		}
		if line.ControlID == landed.ControlID && intent == nil {
			stored := line
			intent = &stored
		}
	}
	if intent == nil {
		return fmt.Errorf("session: control ledger: no accepted stop control seq %d (control %q) for %q — a landed stop without a durable intent is a divergent tuple",
			landed.Seq, landed.ControlID, sessionID)
	}
	if err := verifyLandedTuple(*intent, landed); err != nil {
		return err
	}

	acceptedAt := intent.AcceptedAt
	if acceptedAt.IsZero() {
		acceptedAt = landed.At
	}
	history := intent
	history.LandedStop = &landedStopRecord{
		ParentSessionID: landed.ParentSessionID,
		Generation:      landed.Generation,
		Cause:           landed.Cause,
		Actor:           landed.Actor,
		At:              landed.At,
	}
	history.State = controlStateQueued
	history.AcceptedAt = acceptedAt
	if err := appendControlLineLocked(s, sessionID, *history); err != nil {
		return fmt.Errorf("session: control ledger: append landed stop for %q seq %d: %w", sessionID, landed.Seq, err)
	}
	return nil
}

// verifyLandedTuple refuses a landed-stop record that does not describe the
// same control its ledger intent accepted: control id, generation, cause
// and actor must agree. This is the "visible failure on divergent tuple"
// rule — a mismatched landing is a caller bug, never a history to write.
func verifyLandedTuple(line controlLedgerLine, landed LandedStop) error {
	if line.ControlID != landed.ControlID {
		return fmt.Errorf("session: control ledger: divergent landed stop tuple: seq %d accepted control %q, landing claims %q",
			line.Seq, line.ControlID, landed.ControlID)
	}
	if line.Generation != landed.Generation {
		return fmt.Errorf("session: control ledger: divergent landed stop tuple: seq %d accepted at generation %d, landing claims %d",
			line.Seq, line.Generation, landed.Generation)
	}
	if line.Cause != landed.Cause {
		return fmt.Errorf("session: control ledger: divergent landed stop tuple: seq %d accepted cause %q, landing claims %q",
			line.Seq, line.Cause, landed.Cause)
	}
	if line.Actor != landed.Actor {
		return fmt.Errorf("session: control ledger: divergent landed stop tuple: seq %d accepted actor %q, landing claims %q",
			line.Seq, line.Actor, landed.Actor)
	}
	return nil
}

// RecordFencelessLandedStopLocked appends the landed-stop history line for a
// FENCE-LESS landing (ADR-20260928 Correction C3): a stop that actually
// landed with no accepted Stop control behind it — a legacy cancel
// disposition, a lifetime-budget expiry, a restart stop — so its transition
// is durable history like any fenced stop's, and its direct-parent notice is
// discovered from the ledger instead of from the record's (resumable,
// clearable) stop note. The line fabricates NO control: ControlID must be
// empty, and the landing execution's identity (RunID/BootSeq, the admission
// the landing carried out) rides the landed projection instead.
//
// The transition's stop_seq is ALLOCATED here — the next monotonic control
// sequence after the session ledger's high water, under the caller's lock,
// the same discipline AcceptStopControl applies to fenced controls — so two
// fence-less stops in one generation never share one identity, and the
// sequence space stays one per-child progression across both shapes. The
// allocated seq is returned; the caller stamps it wherever its landing
// records the stop (the synthesized note carries it as its display copy).
//
// The caller MUST already hold the session's lifecycle lock — from inside a
// LifecycleStore.Mutate callback, or between Lock and Unlock — because the
// whole point is the landing writing its history IN THE SAME lock hold that
// persists the stopped state; a landing whose history cannot be recorded is
// refused by its caller rather than persisted note-only (the note-derived
// fallback that used to recover such a stop's notice is retired).
//
// NOT idempotent by tuple: every call allocates a fresh sequence, so a
// caller that re-runs a landing after a crash between this append and the
// record persist writes a second line for the same logical stop. That is
// the accepted crash-window shape — both lines describe a stop that truly
// landed in the ledger's order, and a doubled notice is preferred over a
// lost one.
func (s *LifecycleStore) RecordFencelessLandedStopLocked(sessionID string, landed LandedStop) (int64, error) {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return 0, err
	}
	if landed.ControlID != "" {
		return 0, fmt.Errorf("session: control ledger: a fence-less landed stop must not claim control %q — it fabricates no accepted control", landed.ControlID)
	}
	if landed.Seq != 0 {
		return 0, fmt.Errorf("session: control ledger: a fence-less landed stop does not supply seq %d — the store allocates it", landed.Seq)
	}
	if landed.Generation < 1 {
		return 0, fmt.Errorf("session: control ledger: landed stop generation must be >= 1, got %d", landed.Generation)
	}
	if landed.Actor == "" {
		return 0, fmt.Errorf("session: control ledger: landed stop requires an actor")
	}
	if landed.At.IsZero() {
		return 0, fmt.Errorf("session: control ledger: landed stop requires the stop's original at instant")
	}
	if !IsValidStopCause(landed.Cause) {
		return 0, fmt.Errorf("session: control ledger: invalid landed stop cause %q", landed.Cause)
	}

	highWater, err := s.controlSeqHighWaterLocked(sessionID)
	if err != nil {
		return 0, err
	}
	seq := highWater + 1
	line := controlLedgerLine{
		Seq:        seq,
		Verb:       controlVerbStop,
		State:      controlStateQueued,
		AcceptedAt: landed.At,
		Generation: landed.Generation,
		Cause:      landed.Cause,
		Actor:      landed.Actor,
		LandedStop: &landedStopRecord{
			ParentSessionID: landed.ParentSessionID,
			Generation:      landed.Generation,
			Cause:           landed.Cause,
			Actor:           landed.Actor,
			At:              landed.At,
			RunID:           landed.RunID,
			BootSeq:         landed.BootSeq,
		},
	}
	if err := appendControlLineLocked(s, sessionID, line); err != nil {
		return 0, fmt.Errorf("session: control ledger: append fence-less landed stop for %q: %w", sessionID, err)
	}
	return seq, nil
}
