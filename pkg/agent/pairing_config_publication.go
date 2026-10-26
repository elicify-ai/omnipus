// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import "github.com/elicify-ai/omnipus/pkg/config"

// SwapWrittenConfig publishes a fresh saved-file load, not an old continuation.
// The writer holds config-write -> affected channel orders. Merge only unchanged
// channels' metadata under al.mu so an unrelated direct admission is preserved.
func (al *AgentLoop) SwapWrittenConfig(candidate *config.Config) {
	al.mu.Lock()
	candidate.MergeUnchangedChannelOrders(al.cfg)
	al.cfg = candidate
	al.configGen.Add(1)
	al.mu.Unlock()
}

// SwapReadConfig publishes an identified, newly loaded saved snapshot. Unlike
// a writer it never merges/rebinds metadata from a later live observation.
func (al *AgentLoop) SwapReadConfig(candidate *config.Config) error {
	unlock, err := al.lockPairingConfigPublication(candidate)
	if err != nil {
		return err
	}
	al.SwapConfig(candidate)
	unlock()
	return nil
}

// lockPairingConfigPublication is the final effect check after reload's costly
// preparation, immediately before its config/registry pointer publication.
func (al *AgentLoop) lockPairingConfigPublication(candidate *config.Config) (func(), error) {
	store := al.pairingAttempts()
	if store == nil {
		return func() {}, nil
	}
	ids := store.ConfigOrderInstances()
	for id := range candidate.Channels {
		ids = append(ids, id)
	}
	orders := store.LockChannels(ids)
	unlock := func() {
		for i := len(orders) - 1; i >= 0; i-- {
			orders[i].Unlock()
		}
	}
	if err := store.CheckConfigSnapshot(candidate); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}
