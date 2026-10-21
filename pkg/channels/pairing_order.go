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
	return o.store.admitOrdered(o, ctx, kind, owner)
}

func (o *PairingOrder) MarkReady(attemptID, revision string) bool {
	return o.store.markReadyOrdered(o, attemptID, revision)
}

func (o *PairingOrder) Release(attemptID string, outcome PairingOutcome) bool {
	return o.store.releaseOrdered(o, attemptID, outcome)
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
