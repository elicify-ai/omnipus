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
func (s *PairingAttempts) ReadyForSnapshot(instance string, cfg *config.Config) (string, PairingStartStamp, bool) {
	revision := ChannelRevision(cfg, instance)
	order := s.LockChannel(instance)
	defer order.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := order.connector
	stamp := PairingStartStamp{Generation: c.configGeneration, Revision: revision}
	if !order.matchesConfigLocked(cfg) || c.retired || c.deleting || c.hasStopping() {
		return "", stamp, false
	}
	rec := c.latest
	if rec != nil && rec.Generation == c.configGeneration && rec.Generation == c.generation && rec.startFenced && inFlight(rec.Phase) {
		stamp.Cancelled, stamp.AttemptID = true, rec.AttemptID
		return "", stamp, true // early Stop fence is expressly idle, never anonymous permission
	}
	if c.configGeneration != 0 && c.generation != c.configGeneration {
		if rec != nil && rec.Generation == c.configGeneration && rec.Phase == PairingPhaseTerminal && rec.Outcome == PairingOutcomeStopped {
			stamp.Cancelled, stamp.AttemptID = true, rec.AttemptID // idle only, never a late connection/pairing
			return "", stamp, true
		}
		return "", stamp, false // an uncommitted later admission or another resource
	}
	if rec != nil && rec.Generation == c.generation && rec.Phase == PairingPhaseReady && !rec.startFenced && rec.Revision == revision {
		stamp.AttemptID = rec.AttemptID
		return rec.AttemptID, stamp, true
	}
	if rec != nil && inFlight(rec.Phase) {
		return "", stamp, false // refusing an own token never authorizes a tokenless start
	}
	return "", stamp, true // legitimate linked-device/idle start, not a fallback admission
}

// PairingStartStamp carries one dispatch's original accepted identity. A Stop
// can leave an enabled connector idle without turning its old start into a new
// connection or a fresh unowned pairing.
// not-wire-format: private Go lifecycle context metadata.
type PairingStartStamp struct {
	Generation uint64 `json:"-"`
	Revision   string `json:"-"`
	AttemptID  string `json:"-"`
	Cancelled  bool   `json:"-"`
}

// AcceptsStartStamp is a guarded effect check. A cancelled dispatch may build
// idle state only; the native adapter must not connect that dispatch's client.
func (o *PairingOrder) AcceptsStartStamp(token string, stamp PairingStartStamp) bool {
	o.store.mu.Lock()
	defer o.store.mu.Unlock()
	c := o.connector
	if c.retired || c.deleting || c.hasStopping() || stamp.Generation != c.configGeneration {
		return false
	}
	rec := c.attempts[stamp.AttemptID]
	if stamp.Cancelled {
		if token != "" || rec == nil || rec != c.latest || rec.Generation != stamp.Generation {
			return false
		}
		if rec.startFenced && inFlight(rec.Phase) && rec.Generation == c.generation {
			return true // cancelled idle construction only, before deferred BeginStop
		}
		return rec.Phase == PairingPhaseTerminal && rec.Outcome == PairingOutcomeStopped && effectivePairingGeneration(rec) == c.generation
	}
	if token != "" {
		return token == stamp.AttemptID && rec != nil && rec == c.latest && rec.Generation == c.generation && rec.Phase == PairingPhaseReady && !rec.startFenced && rec.Revision == stamp.Revision
	}
	if c.latest != nil && inFlight(c.latest.Phase) {
		return false
	}
	return stamp.AttemptID == "" && (c.generation == stamp.Generation || c.configGeneration == 0)
}
