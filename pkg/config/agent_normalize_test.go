// Omnipus — proves NormalizeAgentRoster applies both agent-roster
// normalization passes (NormalizeFallbacks + migrateAgentPrimaryProvider) to
// a roster that was populated OUTSIDE loadConfigInternal's own load-time
// loop — i.e. exactly the shape pkg/gateway's
// populateAgentsListFromEntityStore[Strict] bridge (and cmd/omnipus's
// equivalent loaders) hands it after reading agents back from the per-entity
// store (ADR-054).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeAgentRoster_AppliesBothPasses proves an entity-loaded agent
// with (a) a combined "provider/model" primary slug and (b) a legacy
// no-provider fallback entry gets BOTH normalized in one call — the exact
// gap identified against pkg/gateway/rest.go:3234-3242, which persists a
// FallbackModel with an empty Provider and relies on "the next load" to
// resolve it. Before this helper existed, nothing did that for
// entity-loaded agents.
func TestNormalizeAgentRoster_AppliesBothPasses(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{
			{
				Name:     "glm-5.2",
				Model:    "z-ai/glm-5.2",
				Provider: "openrouter",
				APIBase:  "https://openrouter.ai/api/v1",
			},
		},
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID:             "mia",
					Model:          &AgentModelConfig{Primary: "openrouter/google/gemini-2.5-flash"},
					FallbackModels: FallbackModelSlice{{Model: "glm-5.2"}},
				},
			},
		},
	}

	NormalizeAgentRoster(cfg)

	mc := cfg.Agents.List[0].Model
	require.NotNil(t, mc)
	assert.Equal(t, "openrouter", mc.Provider,
		"primary-model provider split (migrateAgentPrimaryProvider) must run on entity-loaded agents")
	assert.Equal(t, "google/gemini-2.5-flash", mc.Primary)

	require.Len(t, cfg.Agents.List[0].FallbackModels, 1)
	assert.Equal(t, "openrouter", cfg.Agents.List[0].FallbackModels[0].Provider,
		"fallback-model provider resolution (NormalizeFallbacks) must run on entity-loaded agents")
	assert.Equal(t, "glm-5.2", cfg.Agents.List[0].FallbackModels[0].Model)
}

// TestNormalizeAgentRoster_IdempotentAndAlreadyResolved proves a second pass
// is a no-op, and that an already-fully-resolved agent (Provider set on both
// Model and every FallbackModel entry) is left completely unchanged.
func TestNormalizeAgentRoster_IdempotentAndAlreadyResolved(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID:             "jim",
					Model:          &AgentModelConfig{Primary: "claude-sonnet-4.6", Provider: "anthropic"},
					FallbackModels: FallbackModelSlice{{Model: "gpt-4o", Provider: "openai"}},
				},
			},
		},
	}

	NormalizeAgentRoster(cfg)
	NormalizeAgentRoster(cfg)

	assert.Equal(t, "anthropic", cfg.Agents.List[0].Model.Provider)
	assert.Equal(t, "claude-sonnet-4.6", cfg.Agents.List[0].Model.Primary)
	require.Len(t, cfg.Agents.List[0].FallbackModels, 1)
	assert.Equal(t, "openai", cfg.Agents.List[0].FallbackModels[0].Provider)
	assert.Equal(t, "gpt-4o", cfg.Agents.List[0].FallbackModels[0].Model)
}

// TestNormalizeAgentRoster_NilAndEmptyNoop proves a nil cfg and an empty
// roster are both safe no-ops (the fresh-install / no-agents-yet case).
func TestNormalizeAgentRoster_NilAndEmptyNoop(t *testing.T) {
	assert.NotPanics(t, func() { NormalizeAgentRoster(nil) })

	cfg := &Config{}
	assert.NotPanics(t, func() { NormalizeAgentRoster(cfg) })
	assert.Empty(t, cfg.Agents.List)
}
