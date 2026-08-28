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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
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
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	return w
}

// TestUpdateAgent_ContextWindowOverride_IsPersistedAndEchoed is the DoD test:
// it fails before the fix (the field is dropped and the response carries none
// of the four window fields) and passes after it.
func TestUpdateAgent_ContextWindowOverride_IsPersistedAndEchoed(t *testing.T) {
	api := newContextWindowAgentAPI(t)

	w := putAgentJSON(t, api, "agent-a", `{"context_window_override":32768}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.ContextWindowOverride,
		"the PUT response must echo the override it just persisted (the form reads it back)")
	assert.Equal(t, 32768, *resp.ContextWindowOverride)
	require.NotNil(t, resp.ContextWindowEffective,
		"context_window_effective must be derived from ResolveWindow on every response")
	assert.Equal(t, 32768, *resp.ContextWindowEffective)
	require.NotNil(t, resp.ContextWindowSource)
	assert.Equal(t, gen.AgentContextWindowSourceOperator, *resp.ContextWindowSource)
	require.NotNil(t, resp.ContextWindowClamped)
	assert.False(t, *resp.ContextWindowClamped)

	// It actually reached AgentConfig — ResolveWindow's rung 1 reads this and
	// nothing else.
	live := api.agentLoop.GetConfig()
	require.NotNil(t, live)
	var stored *int
	for i := range live.Agents.List {
		if live.Agents.List[i].ID == "agent-a" {
			stored = live.Agents.List[i].ContextWindowOverride
		}
	}
	require.NotNil(t, stored, "AgentConfig.ContextWindowOverride must be persisted, not silently dropped")
	assert.Equal(t, 32768, *stored)

	// A subsequent GET round-trips the same four fields.
	g := httptest.NewRecorder()
	api.getAgent(g, "agent-a")
	require.Equal(t, http.StatusOK, g.Code, "body: %s", g.Body.String())
	var got gen.Agent
	require.NoError(t, json.Unmarshal(g.Body.Bytes(), &got))
	require.NotNil(t, got.ContextWindowOverride)
	assert.Equal(t, 32768, *got.ContextWindowOverride)
	require.NotNil(t, got.ContextWindowEffective)
	assert.Equal(t, 32768, *got.ContextWindowEffective)
	require.NotNil(t, got.ContextWindowSource)
	assert.Equal(t, gen.AgentContextWindowSourceOperator, *got.ContextWindowSource)
}

// TestUpdateAgent_ContextWindowOverride_NullClears covers "send null to clear"
// and the "absent leaves unchanged" half of the same contract sentence.
func TestUpdateAgent_ContextWindowOverride_NullClears(t *testing.T) {
	api := newContextWindowAgentAPI(t)

	require.Equal(t, http.StatusOK,
		putAgentJSON(t, api, "agent-a", `{"context_window_override":32768}`).Code)

	// An unrelated write must NOT clear it.
	keep := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":25}`)
	require.Equal(t, http.StatusOK, keep.Code, "body: %s", keep.Body.String())
	var kept gen.Agent
	require.NoError(t, json.Unmarshal(keep.Body.Bytes(), &kept))
	require.NotNil(t, kept.ContextWindowOverride, "an omitted field must leave the override untouched")
	assert.Equal(t, 32768, *kept.ContextWindowOverride)

	// Explicit null clears it, and the effective window falls back down the
	// ladder (no override → the cloud floor for an unsized cloud row).
	cleared := putAgentJSON(t, api, "agent-a", `{"context_window_override":null}`)
	require.Equal(t, http.StatusOK, cleared.Code, "body: %s", cleared.Body.String())
	var out gen.Agent
	require.NoError(t, json.Unmarshal(cleared.Body.Bytes(), &out))
	assert.Nil(t, out.ContextWindowOverride, "an explicit null must clear the override")
	require.NotNil(t, out.ContextWindowSource)
	assert.NotEqual(t, gen.AgentContextWindowSourceOperator, *out.ContextWindowSource,
		"with the override cleared the window must come from a lower rung")

	live := api.agentLoop.GetConfig()
	for i := range live.Agents.List {
		if live.Agents.List[i].ID == "agent-a" {
			assert.Nil(t, live.Agents.List[i].ContextWindowOverride)
		}
	}
}

// TestUpdateAgent_ContextWindowOverride_RejectsNonPositive — the contract's
// `minimum: 1`.
func TestUpdateAgent_ContextWindowOverride_RejectsNonPositive(t *testing.T) {
	api := newContextWindowAgentAPI(t)
	w := putAgentJSON(t, api, "agent-a", `{"context_window_override":0}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "context_window_override")
}
