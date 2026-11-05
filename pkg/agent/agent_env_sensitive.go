// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// bindAgentEnvSensitive gives cfg the stored-record read of every external
// worker's env values and refreshes its masking set (r17 SR-2). Called for
// the config at construction and for every config before it is published.
func (al *AgentLoop) bindAgentEnvSensitive(cfg *config.Config) {
	if cfg == nil {
		return
	}
	cfg.SetAgentEnvSensitiveSource(al.storedAgentEnvValues)
	cfg.RefreshAgentEnvSensitiveValues()
}

// storedAgentEnvValues reads every stored agent record's env_overrides
// values. Skipped (unreadable) records are an error so the caller keeps its
// previous values for them rather than unmasking.
func (al *AgentLoop) storedAgentEnvValues() ([]string, error) {
	if al.homePath == "" {
		return nil, nil
	}
	agents, skipped, err := agentstore.New(al.homePath).List()
	var values []string
	for i := range agents {
		sub := agents[i].Subagents
		if sub == nil || sub.Executor == nil {
			continue
		}
		for _, v := range sub.Executor.EnvOverrides {
			if v != "" {
				values = append(values, v)
			}
		}
	}
	if err != nil {
		return values, err
	}
	if len(skipped) > 0 {
		return values, fmt.Errorf("%d agent records could not be read", len(skipped))
	}
	return values, nil
}
