// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package channels

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

// Pairing-attempt ownership (Train 2 gate r3, binding model
// pairing-ownership-model.md, founder rulings 2026-10-21: in memory only, a
// gateway restart interrupts every pairing). One manager-owned store holds,
// per connector instance, the attempts of its linked-device (QR) pairing:
// each attempt has a server-minted id that is never reused and an immutable
// owner — a chat (session + request) or nobody.
//
//   - Admission (connect_channel, a chat Retry, an ordinary enable) reserves a
//     NEW attempt: pending, then ready once its configuration committed. The
//     check and the reservation are one step under the store lock (M2).
//   - A pairing loop starts only by consuming its own admitted ready token
//     (Activate), or for automatic re-pairing after a logout through its own
//     unowned admission (ActivateRepair). A start without a token never
//     pairs: a revoked, stale or missing admission never becomes a fallback
//     unowned loop (gate r4 R4-A01/R4-A02, M2, M14). QR events carry the
//     attempt id of their own loop.
//   - Stop selects attempts by owner, then re-checks the exact attempt id and
//     owner under the lock, marks it stopping and returns that attempt's own
//     cancel handle (M6). A successor is never touched.
//   - Every terminal transition and every event is compared against the
//     attempt id (M8): an old attempt can never change or label a successor.
//
// The lock is never held across callbacks, cancel handles or waits.

// PairingPhase is an attempt's lifecycle phase.
type PairingPhase string

const (
	PairingPhasePending  PairingPhase = "pending"  // reserved; its configuration is not committed
	PairingPhaseReady    PairingPhase = "ready"    // configuration committed; waiting for its start
	PairingPhaseActive   PairingPhase = "active"   // the platform pairing loop runs
	PairingPhaseStopping PairingPhase = "stopping" // a Stop selected it and is tearing it down
	PairingPhaseTerminal PairingPhase = "terminal" // ended; see Outcome
)

// PairingOutcome is how a terminal attempt ended.
type PairingOutcome string

const (
	PairingOutcomeLinked      PairingOutcome = "linked"
	PairingOutcomeTimeout     PairingOutcome = "timeout"
	PairingOutcomeError       PairingOutcome = "error"
	PairingOutcomeStopped     PairingOutcome = "stopped"
	PairingOutcomeInterrupted PairingOutcome = "interrupted"
	PairingOutcomeNoPairing   PairingOutcome = "no_pairing"   // the device was already linked
	PairingOutcomeStartFailed PairingOutcome = "start_failed" // ended before its start (direct pre-start failure)
)

// PairingAdmission names who asks for the next attempt.
type PairingAdmission string

const (
	PairingAdmissionConnect PairingAdmission = "connect" // approved connect_channel
	PairingAdmissionRetry   PairingAdmission = "retry"   // authenticated chat Retry
	PairingAdmissionEnable  PairingAdmission = "enable"  // ordinary (Connectors) enable: unowned
)

// PairingOwner is the chat that owns an attempt.
type PairingOwner struct {
	SessionID string
	RequestID string
}

// PairingAttempt is one attempt's record (a copy; the store's own is private).
type PairingAttempt struct {
	Instance  string
	AttemptID string
	Phase     PairingPhase
	Outcome   PairingOutcome
	Revision  string
	Owner     *PairingOwner // nil = unowned; immutable once admitted
	// RestartInterrupted marks the unowned attempt a gateway start recorded
	// for a pairing a restart cut off; any chat may Retry it into a new
	// attempt of its own (founder ruling 2026-10-21).
	RestartInterrupted bool
	// Generation identifies the accepted start; StopGeneration identifies its
	// stopping effect without retagging the captured resource. Neither is wire data.
	Generation     uint64 `json:"-"`
	StopGeneration uint64 `json:"-"`
}

// Owned reports whether the attempt has a chat owner.
func (a PairingAttempt) Owned() bool { return a.Owner != nil }

// ErrPairingConflict refuses an admission: an attempt is pending, ready,
// active or stopping, or the latest attempt is not this chat's to retry.
var ErrPairingConflict = errors.New("pairing attempt conflict")

// ErrPairingStopping refuses a start while a Stop is still tearing down the
// connector's previous attempt.
var ErrPairingStopping = errors.New("a previous pairing is still stopping")

// ErrPairingDeleting refuses an admission or a start while the connector is
// being deleted (M12).
var ErrPairingDeleting = errors.New("the connector is being deleted")

type pairingAttemptRecord struct {
	PairingAttempt
	cancel func(context.Context) error // the active attempt's own teardown
	// stopFailed marks a stopping attempt whose teardown failed: it stays
	// stopping (no successor over an undisposed client) and a later Stop may
	// select it again to retry that same teardown (gate r4 T2-SF-14).
	stopFailed  bool
	startFenced bool // explicit Stop cancellation, not HTTP/tool response expiry
	// disposal belongs to the captured resource, not to the channel's newest
	// settings. A later write cannot remove an unfinished stopping barrier.
	disposal        pairingDisposal
	disposalRunning bool
	retryRetired    bool // deletion keeps historical ownership, never Retry authority
}

type pairingConnector struct {
	guard             sync.Mutex // outer ordering guard; never the short state mutex
	generation        uint64
	sequence          uint64
	progressAttempt   string // last accepted envelope survives bounded history pruning
	progressStatus    PairingStatus
	revision          string
	configGeneration  uint64 // the exact accepted config write, distinct from Stop
	configFingerprint string // exact saved entry, including disabled settings
	latest            *pairingAttemptRecord
	attempts          map[string]*pairingAttemptRecord // recent attempts by id, for late lookups
	order             []string
	deleting          bool
	retired           bool
	// interruptNoticeAttemptID records the stale-ready attempt an accepted
	// admission just closed (chief ruling C+D, security review F3): its
	// notice is stamped against the successor's generation and delivers even
	// though the successor is already latest. It self-expires — the next
	// stamped progress moves the sequence, so the notice's metadata equality
	// in AcceptsProgress fails — and any newer non-notice progress clears it.
	interruptNoticeAttemptID string
}

// pairingAttemptHistory bounds how many past attempts a connector keeps for
// attributing late events and Stop completions.
const pairingAttemptHistory = 16

// PairingAttempts is the manager-owned attempt store (in memory only).
type PairingAttempts struct {
	mu             sync.Mutex
	connectors     map[string]*pairingConnector
	nextGeneration uint64 // one lifetime counter; deleted/recreated ids never reuse a stamp
	orderHookMu    sync.RWMutex
	orderContended func(instance string) // nil in production; acknowledgement before waiting
	// afterCheck is a test seam (gate r3 T2): it runs inside admission
	// between the eligibility check and the publication, under the lock.
	afterCheck func()
}

// NewPairingAttempts returns an empty store.
func NewPairingAttempts() *PairingAttempts {
	return &PairingAttempts{connectors: make(map[string]*pairingConnector)}
}

func newAttemptID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("pairing attempt id: %v", err))
	}
	return "pair_" + hex.EncodeToString(b[:])
}

func (s *PairingAttempts) connectorLocked(instance string) *pairingConnector {
	c := s.connectors[instance]
	if c == nil {
		c = &pairingConnector{attempts: make(map[string]*pairingAttemptRecord)}
		s.connectors[instance] = c
	}
	return c
}

func (c *pairingConnector) publish(rec *pairingAttemptRecord) {
	c.latest = rec
	c.attempts[rec.AttemptID] = rec
	c.order = append(c.order, rec.AttemptID)
	for len(c.order) > pairingAttemptHistory {
		pruned := false
		for i, id := range c.order {
			if old := c.attempts[id]; old != nil && old != c.latest && old.Phase != PairingPhaseStopping {
				delete(c.attempts, id)
				c.order = append(c.order[:i], c.order[i+1:]...)
				pruned = true
				break
			}
		}
		if !pruned {
			break // a captured undisposed resource is never evicted by history pruning
		}
	}
}

func inFlight(p PairingPhase) bool {
	return p == PairingPhasePending || p == PairingPhaseReady || p == PairingPhaseActive || p == PairingPhaseStopping
}

func copyAttempt(rec *pairingAttemptRecord) PairingAttempt {
	out := rec.PairingAttempt
	if rec.Owner != nil {
		owner := *rec.Owner
		out.Owner = &owner
	}
	return out
}

// Admit reserves a new pending attempt for instance (M1–M4). An attempt in
// flight (pending, ready, active, stopping) refuses every admission, even with
// identical ids. A Retry additionally needs the latest attempt to be this
// chat's ended (timeout, error, stopped, interrupted) attempt, or the unowned
// attempt a gateway restart interrupted. owner must be set for connect and
// Retry and nil for an ordinary enable.
func (s *PairingAttempts) Admit(instance string, kind PairingAdmission, owner *PairingOwner) (PairingAttempt, error) {
	order := s.LockChannel(instance)
	defer order.Unlock()
	return order.Admit(kind, owner)
}

// admitOrdered decides and reserves under one ordering boundary (M2). The
// caller holds the channel order; the caller-supplied servedContains, when
// non-nil, is read HERE — under that same guard, never from an earlier
// snapshot — so the stale-ready decision always reflects the currently served
// configuration (security review F1, chief ruling C+D). A granted admission
// applies the stale-ready close in the same critical section; a refused one
// applies nothing, so a foreign Retry whose own ownership check would refuse
// interrupts nothing. The closed attempt's interruption envelope is stamped
// after the successor's reservation, against the successor's generation, and
// RETURNED; the caller publishes it after releasing the order — the event
// bus's synchronous gateway tap re-enters this same order (security review
// F3), so nothing may publish while it is held.
func (s *PairingAttempts) admitOrdered(order *PairingOrder, ctx context.Context, kind PairingAdmission, owner *PairingOwner, servedContains func() bool) (PairingAttempt, *PairingProgress, error) {
	instance := order.instance
	if err := ctx.Err(); err != nil {
		return PairingAttempt{}, nil, err
	}
	if instance == "" || (kind != PairingAdmissionEnable && (owner == nil || owner.SessionID == "" || owner.RequestID == "")) {
		return PairingAttempt{}, nil, ErrPairingConflict
	}
	// gate r5 R5-A05/r6; security review F1 round 3 (chief ruling C+D): the
	// admission-time close fires ONLY when the served configuration no longer
	// contains the connector — the attempt commits against a configuration
	// that no longer exists, so it can provably never start. When the
	// instance is present but its served revision differs, the served
	// snapshot may simply be behind a durable commit (the skew
	// CheckConfigSnapshot guards on the write side): the direction is
	// unknowable, so the conservative choice is to refuse and leave the
	// ready attempt untouched. The presence read happens while the channel
	// order is held and before the state mutex: config writers that could
	// change the served set all take the same channel order first.
	stale := false
	if servedContains != nil && order.connector.latest != nil && order.connector.latest.Phase == PairingPhaseReady {
		stale = !servedContains()
	}
	s.mu.Lock()
	c := order.connector
	var err error
	switch {
	case c.deleting:
		err = ErrPairingDeleting
	case c.hasStopping():
		err = ErrPairingConflict
	case c.latest != nil && inFlight(c.latest.Phase) && !stale:
		err = ErrPairingConflict
	case kind == PairingAdmissionRetry:
		if stale {
			// The close would leave latest terminal and interrupted: this
			// Retry must own that exact attempt, or nothing is interrupted.
			post := *c.latest
			post.Phase, post.Outcome = PairingPhaseTerminal, PairingOutcomeInterrupted
			if !retryEligible(&post, *owner) {
				err = ErrPairingConflict
			}
		} else if !retryEligible(c.latest, *owner) {
			err = ErrPairingConflict
		}
	}
	var closed *PairingAttempt
	if err == nil && stale {
		c.latest.Phase, c.latest.Outcome = PairingPhaseTerminal, PairingOutcomeInterrupted
		committed := copyAttempt(c.latest)
		closed = &committed
	}
	if err != nil {
		s.mu.Unlock()
		return PairingAttempt{}, nil, err
	}
	hook := s.afterCheck
	s.mu.Unlock()
	if hook != nil {
		hook() // ordering guard held; short state mutex is not held
	}
	if err := ctx.Err(); err != nil {
		return PairingAttempt{}, nil, err
	}
	id := newAttemptID()
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := &pairingAttemptRecord{PairingAttempt: PairingAttempt{
		Instance: instance, AttemptID: id, Phase: PairingPhasePending, Generation: order.advanceLocked(),
	}}
	if kind != PairingAdmissionEnable {
		o := *owner
		rec.Owner = &o
	}
	c.retired = false // an accepted explicit create/connect may recreate an id
	c.publish(rec)
	var interrupted *PairingProgress
	if closed != nil {
		// The notice is stamped AFTER the successor's reservation, against
		// the successor's generation, and recorded on the connector: this is
		// the one predecessor notice that still delivers (security review F3
		// round 3, chief ruling C+D) — see progressLockedAdmissionInterrupt
		// and the matching AcceptsProgress exception.
		p := order.progressLockedAdmissionInterrupt(*closed)
		interrupted = &p
	}
	return copyAttempt(rec), interrupted, nil
}

func retryEligible(latest *pairingAttemptRecord, owner PairingOwner) bool {
	if latest == nil || latest.retryRetired || latest.Phase != PairingPhaseTerminal {
		return false
	}
	if latest.RestartInterrupted {
		return true
	}
	if latest.Owner == nil || *latest.Owner != owner {
		return false
	}
	switch latest.Outcome {
	case PairingOutcomeTimeout, PairingOutcomeError, PairingOutcomeStopped, PairingOutcomeInterrupted,
		PairingOutcomeStartFailed: // its start failed: the same chat may try again (gate r4 T2-SF-16)
		return true
	}
	return false
}

// IsReady reports whether attemptID is instance's latest attempt and still
// ready to start — what a queued restart for that attempt requires (M2).
func (s *PairingAttempts) IsReady(instance, attemptID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connectors[instance]
	return c != nil && !c.retired && !c.deleting && !c.hasStopping() && c.latest != nil && !c.latest.startFenced && c.latest.Generation == c.generation && c.latest.AttemptID == attemptID && c.latest.Phase == PairingPhaseReady
}

// MarkReady records that attemptID's configuration committed at revision
// (the connector's configuration hash). Only a pending attempt that is still
// the latest becomes startable (M14).
func (s *PairingAttempts) MarkReady(instance, attemptID, revision string) bool {
	order := s.LockChannel(instance)
	defer order.Unlock()
	return order.MarkReady(attemptID, revision)
}

func (s *PairingAttempts) markReadyOrdered(order *PairingOrder, attemptID, revision string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := order.connector
	if c.retired || c.latest == nil || c.latest.AttemptID != attemptID || c.latest.Phase != PairingPhasePending || c.latest.Generation != c.generation {
		return false
	}
	if c.latest.startFenced {
		return false
	}
	c.latest.Phase, c.latest.Revision = PairingPhaseReady, revision
	c.revision, c.configGeneration = revision, c.latest.Generation
	return true
}

// Release ends attemptID before its start (a failed save or enable, or a
// confirmed reload that did not start it). Only a pending or ready attempt is
// released; one a start already consumed is left to that start. An owned
// attempt ends interrupted, so its chat can deliberately Retry.
func (s *PairingAttempts) Release(instance, attemptID string, outcome PairingOutcome) bool {
	order := s.LockChannel(instance)
	defer order.Unlock()
	return order.Release(attemptID, outcome)
}

func (s *PairingAttempts) releaseOrdered(order *PairingOrder, attemptID string, outcome PairingOutcome) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := order.connector
	rec := c.attempts[attemptID]
	if rec == nil || rec != c.latest || rec.Generation != c.generation || rec.startFenced || (rec.Phase != PairingPhasePending && rec.Phase != PairingPhaseReady) {
		return false
	}
	rec.Phase, rec.Outcome, rec.cancel = PairingPhaseTerminal, outcome, nil
	return true
}

func (s *PairingAttempts) attemptLocked(instance, attemptID string) *pairingAttemptRecord {
	c := s.connectors[instance]
	if c == nil {
		return nil
	}
	return c.attempts[attemptID]
}

// Phase reports attemptID's current phase.
func (s *PairingAttempts) Phase(instance, attemptID string) (PairingPhase, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.attemptLocked(instance, attemptID)
	if rec == nil {
		return "", false
	}
	return rec.Phase, true
}

// Attempt returns a copy of attemptID's record.
func (s *PairingAttempts) Attempt(instance, attemptID string) (PairingAttempt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.attemptLocked(instance, attemptID)
	if rec == nil {
		return PairingAttempt{}, false
	}
	return copyAttempt(rec), true
}

// Latest returns a copy of instance's latest attempt.
func (s *PairingAttempts) Latest(instance string) (PairingAttempt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connectors[instance]
	if c == nil || c.retired || c.latest == nil {
		return PairingAttempt{}, false
	}
	return copyAttempt(c.latest), true
}

// IsCurrent reports whether attemptID is instance's latest attempt and is
// active — the only state whose progress may be published (M8).
func (s *PairingAttempts) IsCurrent(instance, attemptID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connectors[instance]
	return c != nil && !c.retired && c.latest != nil && effectivePairingGeneration(c.latest) == c.generation && c.latest.AttemptID == attemptID && c.latest.Phase == PairingPhaseActive
}

// TakeReady returns instance's ready attempt for a start at revision: only an
// attempt committed at exactly that revision starts (M14). An attempt at
// another revision is left alone — a reload built from an older (or newer)
// configuration snapshot never consumes or interrupts an attempt it was not
// built for (gate r5 R5-A05). A stale attempt is closed by its own operation
// once its reload is confirmed, or by the exact successful settings-replacement
// write. Native dispatch uses ReadyForSnapshot to check the generation too.
func (s *PairingAttempts) TakeReady(instance, revision string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connectors[instance]
	if c == nil || c.retired || c.deleting || c.hasStopping() || c.latest == nil || c.latest.startFenced || c.latest.Generation != c.generation || c.latest.Phase != PairingPhaseReady || c.latest.Revision != revision {
		return ""
	}
	return c.latest.AttemptID
}

// InterruptStaleReady closes instance's ready attempt when it was committed at
// a revision other than currentRevision — the connector's configuration was
// replaced since, so it can never start (M14). It returns that attempt so its
// chat can be told. An attempt at the current revision is left ready.
func (s *PairingAttempts) InterruptStaleReady(instance, currentRevision string) (PairingAttempt, bool) {
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connectors[instance]
	if c == nil || c.latest == nil || c.latest.Phase != PairingPhaseReady || c.latest.Revision == currentRevision {
		return PairingAttempt{}, false
	}
	c.latest.Phase, c.latest.Outcome = PairingPhaseTerminal, PairingOutcomeInterrupted
	return copyAttempt(c.latest), true
}

// Activate makes the start's own admitted attempt active just before the
// platform pairing call and records its teardown handle. token is the ready
// attempt this start consumes; without one no pairing loop may start (an
// empty token is refused), so a revoked, stale or missing admission never
// becomes a fallback unowned loop (gate r4 R4-A01, M2, M14). A Stop still
// tearing down the previous attempt, or a delete in progress, refuses the
// start. newCancel builds the attempt's own teardown handle from its id.
func (s *PairingAttempts) Activate(instance, token string, newCancel func(attemptID string) func(context.Context) error) (PairingAttempt, error) {
	if token == "" {
		return PairingAttempt{}, ErrPairingConflict
	}
	// Capture the exact resource handle before taking the ordering/state locks.
	// The final validation below, not this capture, authorizes activation.
	cancel := newCancel(token)
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := order.connector
	if c.deleting {
		return PairingAttempt{}, ErrPairingDeleting
	}
	if c.hasStopping() {
		return PairingAttempt{}, ErrPairingStopping
	}
	rec := c.attempts[token]
	if c.retired || rec == nil || rec != c.latest || rec.Generation != c.generation || rec.startFenced || rec.Phase != PairingPhaseReady {
		return PairingAttempt{}, ErrPairingConflict
	}
	rec.Phase, rec.cancel = PairingPhaseActive, cancel
	return copyAttempt(rec), nil
}

// ActivateRepair admits and activates the automatic re-pairing loop of a
// device the platform logged out (M1: unowned). It is an admission like any
// other (M2): it refuses while the connector is being deleted, while another
// attempt is pending, ready or stopping, and never replaces a reservation.
// An active latest attempt (a loop that ended without a terminal event) is
// interrupted first.
func (s *PairingAttempts) ActivateRepair(instance string, newCancel func(attemptID string) func(context.Context) error, origins ...PairingRepairOrigin) (PairingAttempt, error) {
	id := newAttemptID()
	cancel := newCancel(id)
	order := s.LockChannel(instance)
	defer order.Unlock()
	if len(origins) > 0 && (!order.MatchesConfig(origins[0].Config) || order.Generation() != origins[0].Generation) {
		return PairingAttempt{}, ErrPairingConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := order.connector
	if c.deleting || c.retired {
		return PairingAttempt{}, ErrPairingDeleting
	}
	if c.hasStopping() {
		return PairingAttempt{}, ErrPairingStopping
	}
	if c.latest != nil {
		switch c.latest.Phase {
		case PairingPhasePending, PairingPhaseReady:
			return PairingAttempt{}, ErrPairingConflict
		case PairingPhaseActive:
			c.latest.Phase, c.latest.Outcome, c.latest.cancel = PairingPhaseTerminal, PairingOutcomeInterrupted, nil
		}
	}
	rec := &pairingAttemptRecord{
		PairingAttempt: PairingAttempt{Instance: instance, AttemptID: id, Phase: PairingPhaseActive, Generation: order.advanceLocked()},
		cancel:         cancel,
	}
	c.publish(rec)
	return copyAttempt(rec), nil
}

// RecordRestartInterrupted records, at gateway start, the unowned interrupted
// attempt of a pairing a restart cut off (founder ruling 2026-10-21): nothing
// is resumed and no ownership is inferred; any chat may Retry it.
func (s *PairingAttempts) RecordRestartInterrupted(instance string) PairingAttempt {
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connectorLocked(instance)
	rec := &pairingAttemptRecord{PairingAttempt: PairingAttempt{
		Instance: instance, AttemptID: newAttemptID(), Phase: PairingPhaseTerminal,
		Outcome: PairingOutcomeInterrupted, RestartInterrupted: true, Generation: order.advanceLocked(),
	}}
	c.publish(rec)
	return copyAttempt(rec)
}

// Complete ends an active attempt with outcome. It applies only to that exact
// attempt while it is the latest and active: a stopping attempt is finished by
// its Stop, and an old attempt never changes a successor (M8).
func (s *PairingAttempts) Complete(instance, attemptID string, outcome PairingOutcome) (PairingAttempt, bool) {
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connectors[instance]
	if c == nil || c.retired || c.latest == nil || c.latest.Generation != c.generation || c.latest.AttemptID != attemptID || c.latest.Phase != PairingPhaseActive {
		return PairingAttempt{}, false
	}
	if c.latest.startFenced && outcome == PairingOutcomeStartFailed {
		return PairingAttempt{}, false // cancelled connect must not overwrite the selected Stop
	}
	c.latest.Phase, c.latest.Outcome, c.latest.cancel = PairingPhaseTerminal, outcome, nil
	return copyAttempt(c.latest), true
}

// CloseNoPairing ends a consumed start token whose device was already linked:
// no pairing is needed, nothing is retryable (M14). It reports whether it
// closed that attempt, so the start can say so truthfully.
func (s *PairingAttempts) CloseNoPairing(instance, token string) (PairingAttempt, bool) {
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connectors[instance]
	if c == nil || c.retired || c.deleting || c.latest == nil || c.latest.startFenced || c.latest.Generation != c.generation || c.latest.AttemptID != token || (c.latest.Phase != PairingPhaseReady && c.latest.Phase != PairingPhaseActive) {
		return PairingAttempt{}, false
	}
	c.latest.Phase, c.latest.Outcome, c.latest.cancel = PairingPhaseTerminal, PairingOutcomeNoPairing, nil
	return copyAttempt(c.latest), true
}

// RestoreUnconnectedStart returns an unsuccessfully connected linked-device
// start to its manager-owned disposition ONLY after its native resource was
// disposed. A Stop fence, replacement or consumed successor cannot be undone.
func (s *PairingAttempts) RestoreUnconnectedStart(instance, token string) bool {
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := order.connector
	rec := c.attempts[token]
	if c.retired || c.deleting || rec == nil || rec != c.latest || rec.startFenced || rec.Generation != c.generation || rec.StopGeneration != 0 || rec.Phase != PairingPhaseActive {
		return false
	}
	rec.Phase, rec.cancel = PairingPhaseReady, nil
	return true
}

// OwnedBy returns every in-flight attempt sessionID owns — the selection a
// Stop captures at its admission (M6, M9). A stopping attempt is included
// only when its teardown failed, so a later Stop retries it (T2-SF-14).
func (s *PairingAttempts) OwnedBy(sessionID string) []PairingAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PairingAttempt
	for _, c := range s.connectors {
		for _, rec := range c.attempts {
			if rec.Owner == nil || rec.Owner.SessionID != sessionID {
				continue
			}
			if rec.Phase == PairingPhaseStopping {
				if rec.disposal == pairingDisposalStop && rec.stopFailed {
					out = append(out, copyAttempt(rec))
				}
				continue
			}
			if !c.retired && rec == c.latest && inFlight(rec.Phase) {
				out = append(out, copyAttempt(rec))
			}
		}
	}
	return out
}

// PairingStopSelection is the outcome of BeginStop.
type PairingStopSelection struct {
	Attempt PairingAttempt
	// Cancel is the selected attempt's own teardown handle; nil when the
	// attempt had not started (it was revoked instead).
	Cancel func(context.Context) error
}

// BeginStop re-checks a captured selection under the lock: the attempt must
// still be the latest, have the same owner and be stoppable. A started attempt
// becomes stopping and its handle is returned; a pending or ready one is
// revoked (stopped) at once. Anything else is a no-op (M6, M7).
func (s *PairingAttempts) BeginStop(selected PairingAttempt) (PairingStopSelection, bool) {
	order := s.LockChannel(selected.Instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := order.connector
	rec := c.attempts[selected.AttemptID]
	if rec == nil || rec.Generation != selected.Generation || rec.Owner == nil || selected.Owner == nil || *rec.Owner != *selected.Owner {
		return PairingStopSelection{}, false
	}
	if rec.Phase == PairingPhaseStopping {
		if rec.disposal == pairingDisposalStop && rec.StopGeneration == selected.StopGeneration && rec.stopFailed && rec.cancel != nil {
			rec.stopFailed = false
			return PairingStopSelection{Attempt: copyAttempt(rec), Cancel: rec.cancel}, true
		}
		return PairingStopSelection{}, false
	}
	if c.retired || rec != c.latest || rec.Generation != c.generation {
		return PairingStopSelection{}, false
	}
	switch rec.Phase {
	case PairingPhasePending, PairingPhaseReady:
		rec.StopGeneration = order.advanceLocked()
		rec.Phase, rec.Outcome = PairingPhaseTerminal, PairingOutcomeStopped
		return PairingStopSelection{Attempt: copyAttempt(rec)}, true
	case PairingPhaseActive:
		rec.StopGeneration = order.advanceLocked()
		rec.Phase, rec.disposal = PairingPhaseStopping, pairingDisposalStop
		return PairingStopSelection{Attempt: copyAttempt(rec), Cancel: rec.cancel}, true
	}
	return PairingStopSelection{}, false
}

// FinishStop publishes a stopping attempt's teardown result: on success it
// ends stopped (its owner stays eligible to Retry); on failure it stays
// stopping, marked for a retried teardown, so no successor starts over a
// client that was not disposed (M7, T2-SF-14).
func (s *PairingAttempts) FinishStop(instance, attemptID string, teardownErr error) (PairingAttempt, bool) {
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.attemptLocked(instance, attemptID)
	if rec == nil || rec.Phase != PairingPhaseStopping || rec.disposal != pairingDisposalStop {
		return PairingAttempt{}, false
	}
	if teardownErr != nil {
		rec.stopFailed = true
		return copyAttempt(rec), false
	}
	rec.Phase, rec.Outcome, rec.cancel, rec.stopFailed = PairingPhaseTerminal, PairingOutcomeStopped, nil, false
	return copyAttempt(rec), true
}

// RevokeForDisable ends a pending or ready admission when its connector is
// disabled, so it can never start late; its owner stays eligible (M2). It
// returns the revoked attempt, so its chat can be told.
func (s *PairingAttempts) RevokeForDisable(instance string) (PairingAttempt, bool) {
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connectors[instance]
	if c == nil || c.latest == nil {
		return PairingAttempt{}, false
	}
	generation := order.advanceLocked()
	if c.latest.Phase == PairingPhasePending || c.latest.Phase == PairingPhaseReady {
		c.latest.StopGeneration = generation
		c.latest.Phase, c.latest.Outcome = PairingPhaseTerminal, PairingOutcomeInterrupted
		return copyAttempt(c.latest), true
	}
	return PairingAttempt{}, false
}

// BeginDelete closes admission for instance while it is deleted and revokes
// its pending, ready and active attempts and all Retry eligibility (M12). The
// connector's own removal (its reload stop) tears an active loop down. It
// returns the revoked attempt, so its chat can be told.
func (s *PairingAttempts) BeginDelete(instance string) (PairingAttempt, bool) {
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := order.connector
	if c.deleting {
		return PairingAttempt{}, false
	}
	c.deleting = true
	generation := order.advanceLocked()
	if c.latest != nil && c.latest.Phase != PairingPhaseTerminal && c.latest.Phase != PairingPhaseStopping {
		c.latest.StopGeneration = generation
		c.latest.Outcome = PairingOutcomeInterrupted
		if c.latest.Phase == PairingPhaseActive && c.latest.cancel != nil {
			c.latest.Phase, c.latest.disposal = PairingPhaseStopping, pairingDisposalReplacement
		} else {
			c.latest.Phase, c.latest.cancel = PairingPhaseTerminal, nil
		}
		return copyAttempt(c.latest), true
	}
	return PairingAttempt{}, false
}

// EndDelete retires an instance after its extra state cleanup. Undisposed
// captured resources keep their own records/barriers; deletion never releases
// them. The stable order/tombstone prevents generation reuse on recreation.
func (s *PairingAttempts) EndDelete(instance string, removed bool) {
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := order.connector
	c.deleting = false
	if removed {
		c.retired = true
		for _, rec := range c.attempts {
			rec.retryRetired = true
		}
		if !c.hasStopping() {
			c.latest, c.order = nil, nil
			c.attempts = make(map[string]*pairingAttemptRecord)
		}
	}
}

type pairingTokenKey struct{}

type pairingStartIntent struct {
	token    string
	boot     bool
	stamp    PairingStartStamp
	hasStamp bool
}

// WithPairingStart carries a start's admitted pairing token (empty: none) and
// whether it is the gateway's boot start into Channel.Start.
func WithPairingStart(ctx context.Context, token string, boot bool, stamps ...PairingStartStamp) context.Context {
	intent := pairingStartIntent{token: token, boot: boot}
	if len(stamps) > 0 {
		intent.stamp, intent.hasStamp = stamps[0], true
	}
	return context.WithValue(ctx, pairingTokenKey{}, intent)
}

// PairingStartStampFrom returns the fixed private dispatch identity, if present.
func PairingStartStampFrom(ctx context.Context) (PairingStartStamp, bool) {
	intent, _ := ctx.Value(pairingTokenKey{}).(pairingStartIntent)
	return intent.stamp, intent.hasStamp
}

// PairingStartFrom reads WithPairingStart's values.
func PairingStartFrom(ctx context.Context) (token string, boot bool) {
	intent, _ := ctx.Value(pairingTokenKey{}).(pairingStartIntent)
	return intent.token, intent.boot
}

// PairingAttemptAware is implemented by channels whose linked-device pairing
// runs as store-tracked attempts (the native WhatsApp channel). The manager
// hands each such channel the store and its instance id.
type PairingAttemptAware interface {
	SetPairingAttempts(store *PairingAttempts, instance string)
}

// PairingAttemptObservable is implemented by channels that report pairing
// progress per attempt. The manager wires it like PairingObservable.
type PairingAttemptObservable interface {
	SetPairingAttemptObserver(fn func(attemptID string, status PairingStatus, qr, message string))
}
