// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package channels

import "github.com/elicify-ai/omnipus/pkg/config"

// PairingRepairOrigin identifies the client which reported logout. Native
// repair must validate it before disposing any session/connection state.
// not-wire-format: private resource metadata.
type PairingRepairOrigin struct {
	Generation uint64         `json:"-"`
	Config     *config.Config `json:"-"`
}

// pairingRestartIntent extends the existing forced-restart entry, not a new
// registry. A delayed continuation cannot replace a later accepted restart.
type pairingRestartIntent struct {
	attemptID  string
	generation uint64
	revision   string
	config     uint64
}

func (o *PairingOrder) restartIntent(attemptID string, cfg *config.Config) (pairingRestartIntent, bool) {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	c := o.connector
	if c.retired || c.deleting {
		return pairingRestartIntent{}, false
	}
	if attemptID == "" {
		if !o.matchesConfigLocked(cfg) {
			return pairingRestartIntent{}, false
		}
		return pairingRestartIntent{generation: c.generation, revision: c.revision, config: c.configGeneration}, true
	}
	rec := c.attempts[attemptID]
	if rec == nil || rec != c.latest || rec.Phase != PairingPhaseReady || rec.startFenced || rec.Generation != c.generation || c.hasStopping() {
		return pairingRestartIntent{}, false
	}
	return pairingRestartIntent{attemptID: attemptID, generation: rec.Generation, revision: rec.Revision, config: c.configGeneration}, true
}

func (o *PairingOrder) matchesConfigLocked(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	stamp, _ := cfg.ChannelOrderGeneration(o.instance)
	return o.connector.configGeneration == 0 || stamp == o.connector.configGeneration
}

// MatchesConfig is only a check within an already-held guard. Capture the exact
// resource or publish state before releasing it, never resolve by name later.
func (o *PairingOrder) MatchesConfig(cfg *config.Config) bool {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	return o.matchesConfigLocked(cfg)
}

func (o *PairingOrder) acceptsRestart(intent pairingRestartIntent, cfg *config.Config) bool {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	c := o.connector
	if !o.matchesConfigLocked(cfg) || c.retired || c.deleting || intent.generation != c.generation || intent.config != c.configGeneration {
		return false
	}
	if intent.attemptID == "" {
		return true
	}
	rec := c.latest
	return rec != nil && rec.AttemptID == intent.attemptID && rec.Generation == intent.generation && rec.Revision == intent.revision && rec.Phase == PairingPhaseReady && !rec.startFenced && !c.hasStopping()
}

// ReadyForSnapshot pins a token only for the snapshot which made it ready.
// Activate revalidates this exact immutable token at the native effect.
func (s *PairingAttempts) ReadyForSnapshot(instance string, cfg *config.Config) (string, bool) {
	revision := ChannelRevision(cfg, instance)
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := order.connector
	if !order.matchesConfigLocked(cfg) || c.retired || c.deleting || c.hasStopping() {
		return "", false
	}
	rec := c.latest
	if rec != nil && !c.hasStopping() && rec.Generation == c.generation && rec.Phase == PairingPhaseReady && !rec.startFenced && rec.Revision == revision {
		return rec.AttemptID, true
	}
	return "", true // legitimate linked-device/idle start, not a fallback admission
}
