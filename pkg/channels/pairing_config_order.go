// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package channels

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// PairingConfigIntent belongs to one exact local write while its order is held.
// A reserved number may be skipped on write failure; current state never moves
// before persistence succeeds. No continuation may use this to mint a new stamp.
// not-wire-format: private Go transaction metadata, never configuration JSON.
type PairingConfigIntent struct {
	Generation  uint64 `json:"-"`
	previous    uint64
	revision    string
	fingerprint string
	exists      bool
	enabled     bool
	changed     bool
	attemptID   string
}

func (o *PairingOrder) PrepareConfig(revision string, exists, enabled, changed bool, attemptID string) (PairingConfigIntent, error) {
	s := o.store
	s.mu.Lock()
	defer s.mu.Unlock()
	c := o.connector
	if exists && c.deleting {
		return PairingConfigIntent{}, ErrPairingDeleting
	}
	intent := PairingConfigIntent{Generation: c.configGeneration, previous: c.generation,
		revision: revision, exists: exists, enabled: enabled, changed: changed, attemptID: attemptID}
	if attemptID != "" {
		rec := c.attempts[attemptID]
		if rec == nil || rec != c.latest || rec.Phase != PairingPhasePending || rec.Generation != c.generation || rec.startFenced {
			return PairingConfigIntent{}, ErrPairingConflict
		}
		intent.Generation = rec.Generation
	} else if changed {
		intent.Generation = s.nextGenerationLocked()
	}
	return intent, nil
}

// CommitConfig is called only after this exact write is durable. applied=false
// fences obsolete work but cannot make a new admission ready on stale live state.
// Return immutable notifications; the caller emits them after releasing orders.
func (o *PairingOrder) CommitConfig(intent PairingConfigIntent, applied bool) ([]PairingAttempt, bool) {
	s := o.store
	s.mu.Lock()
	defer s.mu.Unlock()
	c := o.connector
	if intent.previous != c.generation {
		return nil, false
	}
	if !intent.changed && intent.attemptID == "" {
		return nil, true
	}
	if intent.Generation != c.generation {
		c.generation, c.sequence = intent.Generation, 0
	}
	c.revision, c.configGeneration, c.configFingerprint = intent.revision, intent.Generation, intent.fingerprint
	c.retired = !intent.exists
	c.deleting = !intent.exists
	if intent.attemptID != "" {
		rec := c.attempts[intent.attemptID]
		if rec == nil || rec != c.latest || rec.Generation != intent.Generation || rec.Phase != PairingPhasePending {
			return nil, false
		}
		if rec.startFenced {
			// The explicit Stop owns this exact pending operation's outcome.
			// It will admit its stopping effect after the writer releases us.
			rec.Revision = intent.revision
			return nil, false
		}
		if applied && intent.exists && intent.enabled {
			rec.Phase, rec.Revision = PairingPhaseReady, intent.revision
			return nil, true
		}
		rec.Phase, rec.Outcome = PairingPhaseTerminal, PairingOutcomeInterrupted
		return []PairingAttempt{copyAttempt(rec)}, false
	}
	rec := c.latest
	if rec == nil {
		return nil, true
	}
	switch rec.Phase {
	case PairingPhasePending, PairingPhaseReady:
		rec.Phase, rec.Outcome, rec.StopGeneration = PairingPhaseTerminal, PairingOutcomeInterrupted, intent.Generation
		return []PairingAttempt{copyAttempt(rec)}, true
	case PairingPhaseActive:
		rec.Outcome, rec.StopGeneration = PairingOutcomeInterrupted, intent.Generation
		if rec.cancel != nil {
			rec.Phase, rec.disposal = PairingPhaseStopping, pairingDisposalReplacement
		} else {
			rec.Phase = PairingPhaseTerminal
		}
		return []PairingAttempt{copyAttempt(rec)}, true
	}
	return nil, true
}

func channelConfigFingerprint(cfg *config.Config, instance string) string {
	inst, exists := cfg.Channels[instance]
	if !exists {
		return "absent"
	}
	raw, err := json.Marshal(inst)
	if err != nil {
		panic("pairing config fingerprint: " + err.Error())
	}
	return string(raw)
}

// BindReadySnapshot records the exact already-accepted snapshot of an internal
// direct admission. It cannot promote another attempt or change a generation.
func (o *PairingOrder) BindReadySnapshot(cfg *config.Config) {
	fingerprint := channelConfigFingerprint(cfg, o.instance)
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	o.connector.configFingerprint = fingerprint
}

// ChannelConfigChanges returns the affected instance ids, sorted for bulk lock
// acquisition. Public equality is not operation identity; an admitted retry is
// included separately even when its saved bytes do not change.
func ChannelConfigChanges(before, after *config.Config) []string {
	ids := make(map[string]bool)
	for id, old := range before.Channels {
		if next, exists := after.Channels[id]; !exists || !reflect.DeepEqual(old, next) {
			ids[id] = true
		}
	}
	for id := range after.Channels {
		if _, exists := before.Channels[id]; !exists {
			ids[id] = true
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// PairingConfigCommit is local transaction state, not a second registry.
// not-wire-format: the orders and intents never leave the Go process.
type PairingConfigCommit struct {
	orders  []*PairingOrder
	intents []PairingConfigIntent
}

// PreparePairingConfig runs with every affected order already held. It stamps
// the exact unpublished candidate, without advancing current state before save.
func PreparePairingConfig(before, candidate *config.Config, orders []*PairingOrder, reservations map[string]string) (*PairingConfigCommit, error) {
	changed := make(map[string]bool)
	for _, id := range ChannelConfigChanges(before, candidate) {
		changed[id] = true
	}
	for id := range reservations {
		changed[id] = true
	}
	commit := &PairingConfigCommit{}
	for _, order := range orders {
		id := order.instance
		if !changed[id] {
			continue
		}
		inst, exists := candidate.Channels[id]
		intent, err := order.PrepareConfig(ChannelRevision(candidate, id), exists, inst.Enabled, true, reservations[id])
		if err != nil {
			return nil, err
		}
		intent.fingerprint = channelConfigFingerprint(candidate, id)
		candidate.SetChannelOrderGeneration(id, intent.Generation)
		commit.orders = append(commit.orders, order)
		commit.intents = append(commit.intents, intent)
		delete(changed, id)
	}
	if len(changed) != 0 {
		return nil, errors.New("channel mutation has no ordering guard; nothing saved")
	}
	return commit, nil
}

// Commit accepts notifications inside the write's order, before any later
// admission. Emit the returned immutable envelopes only after ALL locks release.
func (c *PairingConfigCommit) Commit(applied bool) []PairingProgress {
	var progress []PairingProgress
	for i, order := range c.orders {
		changed, _ := order.CommitConfig(c.intents[i], applied)
		for _, attempt := range changed {
			progress = append(progress, order.Transition(attempt, PairingStatusError, PairingInterruptedMessage))
		}
	}
	return progress
}

// CheckConfigSnapshot prevents a tool from overwriting a durable-but-unapplied
// save using stale live state. It takes only the short state mutex, never an
// order; callers serialize config writes before entering their mutator.
func (s *PairingAttempts) CheckConfigSnapshot(cfg *config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.connectors {
		stamp, _ := cfg.ChannelOrderGeneration(id)
		if c.configGeneration != 0 && stamp != c.configGeneration {
			return errors.New("saved channel configuration is not applied; reload it before writing")
		}
	}
	return nil
}

// BeginResourceTeardown pins an already accepted resource's cleanup; a reload
// continuation does not mint a new intent. Explicit Stop barriers are untouched.
func (s *PairingAttempts) BeginResourceTeardown(instance, attemptID string) (PairingAttempt, bool) {
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := order.connector
	rec := c.attempts[attemptID]
	if rec == nil {
		return PairingAttempt{}, false
	}
	if rec.Phase == PairingPhaseStopping && rec.disposal == pairingDisposalReplacement {
		return copyAttempt(rec), true
	}
	if c.retired || rec != c.latest || rec.Generation != c.generation || rec.Phase != PairingPhaseActive {
		return PairingAttempt{}, false
	}
	rec.Phase, rec.Outcome = PairingPhaseStopping, PairingOutcomeInterrupted
	rec.StopGeneration, rec.disposal = c.generation, pairingDisposalReplacement
	return copyAttempt(rec), true
}

// FinishReplacement releases only the exact resource a settings/delete effect
// captured. It cannot release an earlier explicit Stop's independent barrier.
func (s *PairingAttempts) FinishReplacement(instance, attemptID string, generation uint64, teardownErr error) (PairingAttempt, bool) {
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.attemptLocked(instance, attemptID)
	if rec == nil || rec.Phase != PairingPhaseStopping || rec.disposal != pairingDisposalReplacement || rec.StopGeneration != generation {
		return PairingAttempt{}, false
	}
	if teardownErr != nil {
		rec.stopFailed = true
		return copyAttempt(rec), false
	}
	rec.Phase, rec.Outcome, rec.cancel, rec.stopFailed = PairingPhaseTerminal, PairingOutcomeInterrupted, nil, false
	return copyAttempt(rec), true
}
