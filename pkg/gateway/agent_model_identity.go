// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"slices"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// agentModelIdentity copies the stored fields a running agent's model is built
// from: the primary model, its pinned provider, the legacy fallback list, and
// the provider-aware fallback chain. agent.NewAgentInstance derives the agent's
// Model, Candidates and provider pool from exactly these, so a change to any of
// them means the running instance must be rebuilt before it serves the change.
// The slices are cloned so a later in-place edit of the record cannot alter a
// snapshot taken before it.
func agentModelIdentity(ac *config.AgentConfig) (config.AgentModelConfig, config.FallbackModelSlice) {
	if ac == nil {
		return config.AgentModelConfig{}, nil
	}
	var model config.AgentModelConfig
	if ac.Model != nil {
		model = config.AgentModelConfig{
			Primary:   ac.Model.Primary,
			Provider:  ac.Model.Provider,
			Fallbacks: slices.Clone(ac.Model.Fallbacks),
		}
	}
	return model, slices.Clone(ac.FallbackModels)
}

// sameAgentModelIdentity reports whether two agentModelIdentity snapshots
// describe the same model. A nil and an empty fallback list are the same (both
// mean "no fallbacks"), matching how NewAgentInstance treats them.
func sameAgentModelIdentity(
	aModel config.AgentModelConfig, aFallbacks config.FallbackModelSlice,
	bModel config.AgentModelConfig, bFallbacks config.FallbackModelSlice,
) bool {
	return aModel.Primary == bModel.Primary &&
		aModel.Provider == bModel.Provider &&
		slices.Equal(aModel.Fallbacks, bModel.Fallbacks) &&
		slices.Equal(aFallbacks, bFallbacks)
}
