// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package channels

import (
	"context"
	"sort"
)

type pairingDisposal uint8

const (
	pairingDisposalNone pairingDisposal = iota
	pairingDisposalStop
	pairingDisposalReplacement
)

// PairingOrder holds one channel's ordering boundary, not the short store
// state mutex. Obtain it before a local config commit or effect and release it
// before manager acquisition, native cancellation, callbacks or waits.
// not-wire-format: private Go synchronization metadata.
type PairingOrder struct {
	store     *PairingAttempts
	instance  string
	connector *pairingConnector
}

// LockChannel returns the stable manager-owned entry under its ordering guard.
// Retiring a channel never discards this guard or reuses a generation.
func (s *PairingAttempts) LockChannel(instance string) *PairingOrder {
	s.mu.Lock()
	c := s.connectorLocked(instance)
	s.mu.Unlock()
	return s.lockConnectorOrder(instance, c)
}

// LockExistingChannel observes an existing real-channel/tombstone order. It
// never allocates a connector for passive traffic carrying an unknown id.
func (s *PairingAttempts) LockExistingChannel(instance string) (*PairingOrder, bool) {
	s.mu.Lock()
	c := s.connectors[instance]
	s.mu.Unlock()
	if c == nil {
		return nil, false
	}
	return s.lockConnectorOrder(instance, c), true
}

func (s *PairingAttempts) lockConnectorOrder(instance string, c *pairingConnector) *PairingOrder {
	if !c.guard.TryLock() {
		s.orderHookMu.RLock()
		hook := s.orderContended
		s.orderHookMu.RUnlock()
		if hook != nil {
			hook(instance) // no state, hook-registry or ordering lock is held
		}
		c.guard.Lock()
	}
	return &PairingOrder{store: s, instance: instance, connector: c}
}

func (o *PairingOrder) Unlock() { o.connector.guard.Unlock() }

// SetOrderContendedHookForTest installs a passive acknowledgement after a
// failed guard TryLock and before waiting. Nil is the production default.
// Tests install before work and clear after joining every participant.
func (s *PairingAttempts) SetOrderContendedHookForTest(hook func(instance string)) {
	s.orderHookMu.Lock()
	s.orderContended = hook
	s.orderHookMu.Unlock()
}

// LockChannels acquires a bulk writer's affected entries in stable order.
func (s *PairingAttempts) LockChannels(instances []string) []*PairingOrder {
	ids := append([]string(nil), instances...)
	sort.Strings(ids)
	out := make([]*PairingOrder, 0, len(ids))
	for i, id := range ids {
		if i == 0 || id != ids[i-1] {
			out = append(out, s.LockChannel(id))
		}
	}
	return out
}

func (s *PairingAttempts) nextGenerationLocked() uint64 {
	if s.nextGeneration == ^uint64(0) {
		panic("pairing ordering generation exhausted")
	}
	s.nextGeneration++
	return s.nextGeneration
}

func (o *PairingOrder) advanceLocked() uint64 {
	o.connector.generation = o.store.nextGenerationLocked()
	o.connector.sequence = 0
	o.connector.progressAttempt, o.connector.progressStatus = "", ""
	return o.connector.generation
}

func effectivePairingGeneration(rec *pairingAttemptRecord) uint64 {
	if rec.StopGeneration != 0 {
		return rec.StopGeneration
	}
	return rec.Generation
}

func (c *pairingConnector) hasStopping() bool {
	for _, rec := range c.attempts {
		if rec.Phase == PairingPhaseStopping {
			return true
		}
	}
	return false
}

// Generation is an observation, never permission for an unlocked later effect.
func (o *PairingOrder) Generation() uint64 {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	return o.connector.generation
}

// Admit is the guarded primitive used by a writer already holding this order.
func (o *PairingOrder) Admit(kind PairingAdmission, owner *PairingOwner) (PairingAttempt, error) {
	return o.AdmitContext(context.Background(), kind, owner)
}

func (o *PairingOrder) AdmitContext(ctx context.Context, kind PairingAdmission, owner *PairingOwner) (PairingAttempt, error) {
	rec, _, err := o.store.admitOrdered(o, ctx, kind, owner, nil)
	return rec, err
}

// AdmitClosingStaleReady admits like AdmitContext and, under the SAME held
// ordering boundary, closes the ready attempt when servedContains() reports
// that the served configuration no longer contains the connector — the
// attempt can provably never start (security review F1, chief ruling C+D).
// When the connector is still served, an in-flight ready attempt refuses the
// admission untouched: the served snapshot cannot prove it is newer than a
// durable commit, so the conservative choice is to refuse. The close is
// applied only when this very admission is granted: a refused admission (a
// foreign Retry its ownership check refuses) interrupts nothing. The
// non-nil second return is the stamped interruption envelope: the caller
// publishes it AFTER releasing this order — the event bus's synchronous
// gateway tap re-enters the order, so nothing may publish while it is held
// (security review F3). The caller must hold this order (LockChannel).
func (o *PairingOrder) AdmitClosingStaleReady(ctx context.Context, kind PairingAdmission, owner *PairingOwner, servedContains func() bool) (PairingAttempt, *PairingProgress, error) {
	return o.store.admitOrdered(o, ctx, kind, owner, servedContains)
}

func (o *PairingOrder) MarkReady(attemptID, revision string) bool {
	return o.store.markReadyOrdered(o, attemptID, revision)
}

// CommittedStart reports whether this exact accepted write has already been
// consumed. Phase advancement is not cancellation and must never rearm/restart
// the same resource. A Stop fence, replacement or absent token still refuses it.
func (o *PairingOrder) CommittedStart(attemptID string) bool {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	c := o.connector
	rec := c.attempts[attemptID]
	if c.retired || c.deleting || rec == nil || rec != c.latest || rec.startFenced || rec.retryRetired || rec.Generation != c.generation || rec.Generation != c.configGeneration || rec.StopGeneration != 0 || rec.Revision == "" {
		return false
	}
	if rec.Phase == PairingPhaseActive {
		return true
	}
	if rec.Phase != PairingPhaseTerminal {
		return false
	}
	switch rec.Outcome {
	case PairingOutcomeLinked, PairingOutcomeNoPairing, PairingOutcomeStartFailed, PairingOutcomeError, PairingOutcomeTimeout:
		return true
	default:
		return false
	}
}

func (o *PairingOrder) Release(attemptID string, outcome PairingOutcome) bool {
	return o.store.releaseOrdered(o, attemptID, outcome)
}

// AcceptsResource checks an already-captured native resource at its local
// connect/publication boundary. It cannot admit, rebind or revive an attempt.
func (o *PairingOrder) AcceptsResource(generation uint64, attemptID string) bool {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	c := o.connector
	if c.retired || c.deleting || c.generation != generation {
		return false
	}
	if attemptID == "" {
		return c.latest == nil || !inFlight(c.latest.Phase)
	}
	rec := c.attempts[attemptID]
	if rec == nil || rec != c.latest || rec.Generation != generation || rec.startFenced || rec.StopGeneration != 0 {
		return false
	}
	return rec.Phase == PairingPhaseActive || rec.Phase == PairingPhaseTerminal && (rec.Outcome == PairingOutcomeLinked || rec.Outcome == PairingOutcomeNoPairing)
}

// FenceStarts records cancellation of the exact operations selected by Stop.
// It does not wait for a config-writing guard or change ownership/generation;
// BeginStop admits the actual stopping effect under that guard afterwards.
// Response expiry and normal tool/turn completion never call this method.
func (s *PairingAttempts) FenceStarts(selected []PairingAttempt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, attempt := range selected {
		rec := s.attemptLocked(attempt.Instance, attempt.AttemptID)
		if rec != nil && rec.Generation == attempt.Generation && rec.Owner != nil && attempt.Owner != nil && *rec.Owner == *attempt.Owner {
			rec.startFenced = true
		}
	}
}
