// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// UAT E-7 root cause: PUT /api/v1/agents/{id} saved a model or provider change
// and answered 200 while the running agent kept serving its previous model
// ("model saved to config but could not be applied to the running agent").
//
// The oracle in every test here is the LIVE registry instance, never the PUT
// response alone: needs_model and degraded_reason on the response are derived
// from the saved config, so they can say "changed" while the running agent has
// not changed at all. That is exactly how the defect stayed green.
//
// Spec: ADR-067 US-6.AC3 (re-pointing an agent through the agent update path
// takes effect without a restart; a switch onto an unknown provider leaves the
// agent refusing turns), ADR-068 FR-014 (needs_model), CLAUDE.md's ADR-037 rule
// (a control that reports saved but changes nothing is banned).

package gateway

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/require"
)

// pinOnlyOpenAIProvider rewrites the on-disk config so its providers list holds
// a single dedicated OpenAI row. An explicit providers list suppresses the
// default passthrough seeding, so a model slug that no configured provider
// offers has no route at all.
func pinOnlyOpenAIProvider(t *testing.T, api *restAPI) {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["providers"] = []map[string]any{
		{
			"provider":   "openai",
			"model_name": "gpt-4o",
			"model":      "openai/gpt-4o",
			"models":     []string{"gpt-4o", "gpt-4o-mini"},
			"api_base":   "https://api.openai.com/v1",
			"api_key":    "sk-test-dummy",
		},
	}
	out, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), out, 0o600))
}

// liveAgent returns the agent instance the registry currently serves turns
// from.
func liveAgent(t *testing.T, api *restAPI, id string) *agent.AgentInstance {
	t.Helper()
	inst, ok := api.agentLoop.GetRegistry().GetAgent(id)
	require.True(t, ok, "agent %q must be registered", id)
	require.NotNil(t, inst)
	return inst
}

// storedPrimaryModel reads the primary model from the agent's saved entity
// record, the source every later boot or reload builds the agent from.
func storedPrimaryModel(t *testing.T, api *restAPI, id string) string {
	t.Helper()
	rec, err := agentstore.New(api.homePath).Get(id)
	require.NoError(t, err)
	if rec.Model == nil {
		return ""
	}
	return rec.Model.Primary
}
