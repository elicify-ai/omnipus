// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Q1 regression coverage: PUT /api/v1/agents/{id} silently dropped
// model_params. contracts/components/schemas/AgentUpdateRequest.yaml
// (~:133-152) names `model_params` (temperature, max_tokens, top_p) as the
// correct wire key, but before this fix config.AgentConfig had no field to
// decode it into — the request returned 200, nothing was written to the
// persisted entity record, and every GET echoed model_params: null. That is
// the exact ADR-037 anti-pattern CLAUDE.md bans: a control that looks like
// it worked and changed nothing.
//
// This file covers all three angles: persistence + echo round-trip
// (TestUpdateAgent_ModelParams_PersistAndEcho), the turn-time effect — the
// override must actually reach the AgentInstance a fresh reload/boot would
// build, not just sit in config.json (TestUpdateAgent_ModelParams_AppliedToEffectiveParams,
// mirroring rest_default_agent_singleton_test.go's fresh-registry pattern
// since this lightweight test harness never wires a real reload function),
// and the top_p carve-out — no provider adapter in this codebase implements
// nucleus sampling, so accepting it silently would just move the same
// anti-pattern one layer down (TestUpdateAgent_ModelParams_TopPRejected).
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// newModelParamsTestAPI builds a minimal restAPI with one ordinary
// (non-worker, non-locked) agent "agent-a" seeded both into cfg.Agents.List
// (for AgentLoop construction) and into the real entity store (required for
// updateAgent's persist step — see seedAgentEntities's doc comment). Returns
// the api, its backing *config.Config, and the tmpDir used as homePath.
func newModelParamsTestAPI(t *testing.T) (*restAPI, *config.Config, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: "agent-a", Name: "Agent A"},
			},
		},
	}
	seedTestAgents(cfg)
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	require.NoError(t, os.WriteFile(cfgPath, marshalConfigForDisk(t, cfg), 0o600))

	msgBus := bus.NewMessageBus()
	provider := &restMockProvider{}
	al := mustAgentLoop(t, cfg, msgBus, provider)
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	return api, cfg, tmpDir
}

func putAgentModelParams(t *testing.T, api *restAPI, id, body string) (*httptest.ResponseRecorder, gen.Agent) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(body))
	api.HandleAgents(w, r)
	var resp gen.Agent
	if w.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	}
	return w, resp
}

func getAgent(t *testing.T, api *restAPI, id string) (*httptest.ResponseRecorder, gen.Agent) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+id, nil)
	api.HandleAgents(w, r)
	var resp gen.Agent
	if w.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	}
	return w, resp
}
