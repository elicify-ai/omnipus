// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestDeleteProvider_ClearedPrimariesInheritExistingDefault is #1620 T2.
// Oracle: the founder's rule says affected agents fall back to the configured
// installation default; ADR-068 FR-013 still requires clearing their OWN pair,
// never pinning it to another provider. Agent.yaml describes the API model as
// resolved from defaults. The older no-default regression remains unchanged:
// TestDeleteProvider_DependentsLeftWithoutModel has no default to inherit.
func TestDeleteProvider_ClearedPrimariesInheritExistingDefault(t *testing.T) {
	defaultPair := config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"}
	cfg := providerDeleteBaseConfig(defaultPair,
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
		config.AgentConfig{ID: "scout", Name: "Scout",
			Model: &config.AgentModelConfig{Provider: "openrouter", Primary: "z-ai/glm-5"}},
		config.AgentConfig{ID: "writer", Name: "Writer",
			Model: &config.AgentModelConfig{Provider: "openrouter", Primary: "z-ai/glm-5"}},
	)
	api, home, _ := newProviderDeleteAPI(t, cfg)

	// Positive control: both agents really start on the provider being removed.
	before := make(map[string]gen.Agent)
	for _, agent := range listAgentsResp(t, api) {
		before[agent.Id] = agent
	}
	for _, id := range []string{"scout", "writer"} {
		require.Contains(t, before, id, "the API control must observe every affected agent")
		agent := before[id]
		require.NotNil(t, agent.Model)
		require.Equal(t, "z-ai/glm-5", *agent.Model)
		require.NotNil(t, agent.Provider)
		require.Equal(t, "openrouter", *agent.Provider)
		require.False(t, agent.NeedsModel)
	}

	w := doProviderDelete(t, api, "openrouter", "", nil, true)

	require.Equal(t, http.StatusOK, w.Code, "removing a non-default provider must succeed")
	var result gen.ProviderDeleteResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, gen.ProviderDeleteResponse{
		Deleted: true,
		Dependents: []gen.ProviderDependent{
			{Id: "scout", Name: "Scout", Role: gen.ProviderDependentRolePrimary},
			{Id: "writer", Name: "Writer", Role: gen.ProviderDependentRolePrimary},
		},
		DefaultChanged: false,
	}, result, "removal must report the cleared primaries without changing the installation default")
	assert.Equal(t, []string{"anthropic"}, diskProviderIDs(t, home))
	assert.Equal(t, defaultPair, api.agentLoop.GetConfig().Agents.Defaults.DefaultModel)

	persisted := diskConfig(t, home)
	agents, ok := persisted["agents"].(map[string]any)
	require.True(t, ok, "the persisted config must contain agents.defaults.default_model")
	defaults, ok := agents["defaults"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"provider": "anthropic", "model": "claude-sonnet-4.6"}, defaults["default_model"])

	// Persisted own bindings stay empty; the GET independently reports the
	// effective model. This catches both silent re-pinning and a false
	// needs_model=true after clearing a binding while a default is available.
	after := make(map[string]gen.Agent)
	for _, agent := range listAgentsResp(t, api) {
		after[agent.Id] = agent
	}
	store := agentstore.New(home)
	for _, id := range []string{"scout", "writer"} {
		record, err := store.Get(id)
		require.NoError(t, err)
		ownModel := config.AgentModelConfig{}
		if record.Model != nil {
			ownModel = *record.Model
		}
		assert.Equal(t, config.AgentModelConfig{}, ownModel, "agent %s must have no own model, not a copy of the default", id)

		require.Contains(t, after, id, "every affected agent must remain in the agents API")
		agent := after[id]
		assert.False(t, agent.NeedsModel, "agent %s inherits an available default and can still run", id)
		require.NotNil(t, agent.Model, "agent %s must expose its effective model", id)
		assert.Equal(t, "claude-sonnet-4.6", *agent.Model, "agent %s must expose the configured default model, not its removed primary", id)
	}
}
