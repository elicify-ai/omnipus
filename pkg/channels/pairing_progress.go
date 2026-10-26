// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package channels

import "context"

// PairingProgress is an immutable, private event envelope. Accept it under the
// channel order, emit it after unlocking, and revalidate it at each delivery.
// not-wire-format: only generated WhatsAppPairingFrame crosses the gateway.
type PairingProgress struct {
	Attempt    PairingAttempt              `json:"-"`
	Generation uint64                      `json:"-"`
	Sequence   uint64                      `json:"-"`
	Status     PairingStatus               `json:"-"`
	QR         string                      `json:"-"`
	Message    string                      `json:"-"`
	Dispose    func(context.Context) error `json:"-"` // captured resource, never lookup by channel name
}

// Progress accepts a native event, including the terminal transition for an
// adapter which has not already completed its own attempt. No callback runs
// while either store lock is held.
func (o *PairingOrder) Progress(attemptID string, status PairingStatus, qr, message string) (PairingProgress, bool) {
	s := o.store
	s.mu.Lock()
	defer s.mu.Unlock()
	c := o.connector
	rec := c.attempts[attemptID]
	if attemptID == "" {
		rec = c.latest // compatibility entry point; native continuations name their token
	}
	if c.retired || c.deleting {
		return PairingProgress{}, false
	}
	if rec == nil {
		if attemptID != "" || c.latest != nil {
			return PairingProgress{}, false
		}
		return o.progressLocked(PairingAttempt{Instance: o.instance, Generation: c.generation}, status, qr, message), true
	}
	if rec != c.latest || effectivePairingGeneration(rec) != c.generation {
		return PairingProgress{}, false
	}
	switch status {
	case PairingStatusWaiting, PairingStatusCode:
		if rec.Phase != PairingPhaseActive || rec.startFenced {
			return PairingProgress{}, false
		}
	default:
		if rec.Phase == PairingPhaseActive {
			rec.Phase, rec.Outcome, rec.cancel = PairingPhaseTerminal, pairingStatusOutcome(status), nil
		}
		if rec.Phase != PairingPhaseTerminal && (rec.Phase != PairingPhaseStopping || rec.disposal != pairingDisposalReplacement || status != PairingStatusError) {
			return PairingProgress{}, false
		}
	}
	return o.progressLocked(copyAttempt(rec), status, qr, message), true
}

// Transition stamps a captured state transition. A historical transition still
// carries its own audit identity but gets no public sequence after replacement.
func (o *PairingOrder) Transition(attempt PairingAttempt, status PairingStatus, message string) PairingProgress {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	return o.progressLocked(attempt, status, "", message)
}

func (o *PairingOrder) progressLocked(attempt PairingAttempt, status PairingStatus, qr, message string) PairingProgress {
	generation := attempt.Generation
	if attempt.StopGeneration != 0 {
		generation = attempt.StopGeneration
	}
	p := PairingProgress{Attempt: attempt, Generation: generation, Status: status, QR: qr, Message: message}
	c := o.connector
	if (!c.retired && !c.deleting || c.isDeletionProgress(generation, attempt.AttemptID, status)) && generation == c.generation && (attempt.AttemptID == "" && c.latest == nil || c.latest != nil && c.latest.AttemptID == attempt.AttemptID) {
		c.sequence++
		if c.sequence == 0 {
			panic("pairing progress sequence exhausted")
		}
		p.Sequence = c.sequence
		c.progressAttempt, c.progressStatus = attempt.AttemptID, status
		if attempt.AttemptID != c.interruptNoticeAttemptID {
			c.interruptNoticeAttemptID = "" // newer progress expires the notice
		}
	}
	return p
}

// progressLockedAdmissionInterrupt stamps the stale-ready interruption an
// accepted admission just applied (chief ruling C+D, security review F3). The
// closed attempt is no longer the connector's latest — the successor reserved
// in the same boundary is — so this is the one predecessor notice that stamps
// against the successor's generation and records itself for delivery;
// AcceptsProgress relaxes its latest-attempt rule for exactly this recorded
// envelope, and any newer progress expires it (the sequence equality fails
// and progressLocked clears the record). Caller holds the store mutex.
func (o *PairingOrder) progressLockedAdmissionInterrupt(closed PairingAttempt) PairingProgress {
	c := o.connector
	p := PairingProgress{Attempt: closed, Generation: c.generation, Status: PairingStatusError, Message: PairingInterruptedMessage}
	c.sequence++
	if c.sequence == 0 {
		panic("pairing progress sequence exhausted")
	}
	p.Sequence = c.sequence
	c.progressAttempt, c.progressStatus = closed.AttemptID, PairingStatusError
	c.interruptNoticeAttemptID = closed.AttemptID
	return p
}

// AcceptsProgress is observation within an ALREADY HELD order. The caller must
// update its cache or enqueue before Unlock; the boolean is not a later permit.
func (o *PairingOrder) AcceptsProgress(generation, sequence uint64, attemptID string, status PairingStatus) bool {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	c := o.connector
	if generation != c.generation || sequence == 0 || sequence != c.sequence || attemptID != c.progressAttempt || status != c.progressStatus {
		return false
	}
	if (c.retired || c.deleting) && c.latest == nil {
		// EndDelete may prune history before its accepted notification emits.
		// Only that exact current terminal envelope remains deliverable; a
		// recreation advances generation and invalidates this metadata too.
		return status == PairingStatusError && attemptID != ""
	}
	if (c.retired || c.deleting) && !c.isDeletionProgress(generation, attemptID, status) {
		return false
	}
	if attemptID == "" {
		return c.latest == nil
	}
	if c.latest == nil || c.latest.AttemptID != attemptID || effectivePairingGeneration(c.latest) != generation {
		// An admission's own stale-ready interruption is current work, not a
		// superseded predecessor (chief ruling C+D, security review F3): the
		// successor reserved in the same boundary is already latest, and the
		// notice is stamped against the successor's generation. Only this
		// one recorded error envelope passes — the record self-expires at
		// the next stamped progress, and no predecessor's QR or waiting
		// frame can ever shape-match it.
		if status != PairingStatusError || attemptID == "" || attemptID != c.interruptNoticeAttemptID {
			return false
		}
		return true
	}
	if status == PairingStatusCode || status == PairingStatusWaiting {
		return c.latest.Phase == PairingPhaseActive && !c.latest.startFenced
	}
	return true
}

// The deleting intent's own terminal notification is current, not progress
// from its retired client. All earlier generations and all QR frames stay
// rejected, including after this id is recreated.
func (c *pairingConnector) isDeletionProgress(generation uint64, attemptID string, status PairingStatus) bool {
	rec := c.latest
	return status == PairingStatusError && rec != nil && rec.AttemptID == attemptID && rec.StopGeneration == generation && rec.Outcome == PairingOutcomeInterrupted &&
		(rec.Phase == PairingPhaseTerminal || rec.Phase == PairingPhaseStopping && rec.disposal == pairingDisposalReplacement)
}

func pairingStatusOutcome(status PairingStatus) PairingOutcome {
	switch status {
	case PairingStatusLinked:
		return PairingOutcomeLinked
	case PairingStatusTimeout:
		return PairingOutcomeTimeout
	case PairingStatusStopped:
		return PairingOutcomeStopped
	default:
		return PairingOutcomeError
	}
}
