// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression tests for ADR-066 D2 rung 1 on the agent API.
//
// The bug: PUT /api/v1/agents/{id} decoded context_window_override and threw
// it away — 200 OK, "Saved", AgentConfig.ContextWindowOverride still nil, so
// ResolveWindow's rung 1 never fired. Symmetrically, no response path ever
// populated context_window_override / _effective / _source / _clamped, so the
// Advanced panel's field came back blank and the "Effective window · source"
// row never rendered. That is the ADR-037 "reports Saved, changes nothing"
// anti-pattern CLAUDE.md records as a release blocker.

package gateway

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// newContextWindowAgentAPI builds a restAPI over one ordinary agent with no
// window override set.
func newContextWindowAgentAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

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
		Context: config.DefaultContextSettings(),
	}
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", marshalConfigForDisk(t, cfg), 0o600))

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)
	return api
}

func putAgentJSON(t *testing.T, api *restAPI, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	return w
}
