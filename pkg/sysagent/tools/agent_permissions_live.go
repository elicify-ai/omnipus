// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

// Skills & Tools C-26 point 7 ("A permission change", writer
// set_agent_permissions): after the write is durable and before the
// response, the agent's running allowances for every tool or skill value the
// write changed are ended. The writer puts the stored values into the live
// config through the agent loop's MutateConfig, which ends the allowances of
// every permission a mutation changes.

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// applyStoredPermissionsLive copies the agent's stored tool and skill values
// into its live config entry. Without a wired MutateConfig (no running agent
// loop) there is nothing live to change. An agent with no live entry has no
// running allowances to end.
func applyStoredPermissionsLive(d *Deps, store *agentstore.Store, id string) error {
	if d == nil || d.MutateConfig == nil {
		return nil
	}
	rec, err := store.Get(id)
	if err != nil {
		return fmt.Errorf("the permissions were saved, but the running agent could not be updated: %w", err)
	}
	err = d.MutateConfig(func(cfg *config.Config) error {
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == id {
				cfg.Agents.List[i].Tools = rec.Tools
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("the permissions were saved, but the running agent could not be updated: %w", err)
	}
	return nil
}
