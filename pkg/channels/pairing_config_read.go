// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package channels

import "github.com/elicify-ai/omnipus/pkg/config"

// ConfigOrderInstances includes deletion tombstones and captured resources.
func (s *PairingAttempts) ConfigOrderInstances() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.connectors))
	for id := range s.connectors {
		ids = append(ids, id)
	}
	return ids
}

// CarrySavedConfigOrders is for a NEW unpublished candidate read from actual
// saved bytes under the config-write lock, never a continuation's old pointer.
// Only exact identified entries inherit an existing saved-write generation.
func (s *PairingAttempts) CarrySavedConfigOrders(candidate *config.Config) {
	for _, id := range s.ConfigOrderInstances() {
		fingerprint := channelConfigFingerprint(candidate, id)
		s.mu.Lock()
		c := s.connectors[id]
		generation, matches := c.configGeneration, c.configFingerprint == fingerprint
		s.mu.Unlock()
		if matches {
			candidate.CarryChannelOrderGeneration(id, generation)
		}
	}
}

// BindSavedConfigRead belongs ONLY at the actual saved-file read boundary,
// under the config-write lock. Known app writes keep their original generation;
// an external settings change is accepted/fenced here. No old reload snapshot
// may be relabelled by calling this later. Publication still validates its stamp.
func (s *PairingAttempts) BindSavedConfigRead(candidate *config.Config) ([]PairingProgress, error) {
	ids := s.ConfigOrderInstances()
	for id := range candidate.Channels {
		ids = append(ids, id)
	}
	orders := s.LockChannels(ids)
	defer func() {
		for i := len(orders) - 1; i >= 0; i-- {
			orders[i].Unlock()
		}
	}()
	commit := &PairingConfigCommit{}
	for _, order := range orders {
		id := order.instance
		fingerprint := channelConfigFingerprint(candidate, id)
		s.mu.Lock()
		c := order.connector
		generation, known, matches := c.configGeneration, c.configGeneration != 0, c.configFingerprint == fingerprint
		s.mu.Unlock()
		if !known || matches {
			// Bootstrap observes existing saved configuration, not new user
			// intent. It neither steals nor clears an in-flight admission.
			candidate.CarryChannelOrderGeneration(id, generation)
			continue
		}
		inst, exists := candidate.Channels[id]
		intent, err := order.PrepareConfig(ChannelRevision(candidate, id), exists, inst.Enabled, true, "")
		if err != nil {
			return nil, err
		}
		intent.fingerprint = fingerprint
		candidate.SetChannelOrderGeneration(id, intent.Generation)
		commit.orders = append(commit.orders, order)
		commit.intents = append(commit.intents, intent)
	}
	return commit.Commit(false), nil // durable bytes, not yet live-applied
}
