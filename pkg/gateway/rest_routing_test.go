// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for sprint-258 routing features:
//   - updateAgent single-default invariant (PUT /api/v1/agents/{id})
//   - GET/PUT /api/v1/channels/{id}/routing
//
// Traces to: sprint/258-jun-2026 task: agent default flag + channel routing endpoints.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedRoutingAgentEntities persists a matching agentstore entity record for
// every agent in agents (ADR-054: agents are per-entity records under
// entities/agents/<id>.json, not config.json's agents.list — config.LoadConfig
// unconditionally strips any agents.list content it finds on disk,
// pkg/config/legacy_agents_list.go). updateAgent and setChannelRouting persist
// via updateConfigJSONLocked, which UNCONDITIONALLY calls
// refreshConfigAndRewireServices after every successful write — this
// *replaces cfg.Agents.List wholesale* with agentstore.New(homePath).List()
// (populateAgentsListFromEntityStore, pkg/gateway/gateway.go). So after the
// FIRST write in a test, any fixture agent that exists only in the in-memory
// cfg.Agents.List literal — never a real entity record — vanishes from the
// live config. Every fixture in this file therefore seeds a matching entity
// record for each agent it registers, in addition to the in-memory
// cfg.Agents.List literal passed to mustAgentLoop (still needed for the
// AgentRegistry's initial construction and for any read that happens before
// the first write).
func seedRoutingAgentEntities(t *testing.T, homePath string, agents []config.AgentConfig) {
	t.Helper()
	store := agentstore.New(homePath)
	for _, a := range agents {
		rec := a
		require.NoError(t, store.Create(rec.ID, &rec))
	}
}

// marshalConfigForDisk marshals cfg to JSON with agents.list stripped out —
// config.json on disk carries no agent data at all now (ADR-054); a raw JSON
// round-trip avoids copying config.Config's embedded sync.RWMutex the way a
// plain `diskCfg := *cfg` struct-copy would (correctly flagged by `go vet`
// as "assignment copies lock value").
func marshalConfigForDisk(t *testing.T, cfg *config.Config) []byte {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	if agents, ok := m["agents"].(map[string]any); ok {
		delete(agents, "list")
	}
	out, err := json.Marshal(m)
	require.NoError(t, err)
	return out
}

// --- Channel routing tests ---

// newChannelRoutingTestAPI creates a restAPI with one agent, a minimal bindings
// section, and a written config.json.
func newChannelRoutingTestAPI(t *testing.T) (*restAPI, string) {
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
				{ID: "bot-agent", Name: "Bot Agent"},
				{ID: "other-agent", Name: "Other Agent"},
			},
		},
	}

	require.NoError(t, os.WriteFile(cfgPath, marshalConfigForDisk(t, cfg), 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)
	return api, cfgPath
}

// TestChannelRouting_PutUnknownAgentReturnsError verifies that setting a
// non-existent agent ID returns 404.
//
// BDD: Given no agent with ID "nonexistent",
//
//	When PUT /api/v1/channels/telegram/routing with {"default_agent_id": "nonexistent"},
//	Then response is 404.
func TestChannelRouting_PutUnknownAgentReturnsError(t *testing.T) {
	api, _ := newChannelRoutingTestAPI(t)
	writeTestWorkspaceJSON(t, api, "ops", "active", []string{"bot-agent"}, false)

	body := `{"workspace_id": "ops", "default_agent_id": "nonexistent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing", strings.NewReader(body))
	api.HandleChannels(w, r)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "an unknown bound agent must be refused (422)")
	assert.Contains(t, w.Body.String(), "not found")
}

// TestChannelRouting_PutUnknownChannelReturns404 verifies that targeting a
// channel that is not in validChannelIDs returns 404.
//
// BDD: Given "boguschan" is not a known channel,
//
//	When PUT /api/v1/channels/boguschan/routing,
//	Then response is 404.
func TestChannelRouting_PutUnknownChannelReturns404(t *testing.T) {
	api, _ := newChannelRoutingTestAPI(t)

	body := `{"default_agent_id": "bot-agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/boguschan/routing", strings.NewReader(body))
	api.HandleChannels(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code, "unknown channel must return 404")
}

// TestChannelRouting_PutDisabledChannelSucceeds verifies that setting a routing
// binding on a disabled-but-valid channel is allowed (pre-configure before enable).
//
// BDD: Given "telegram" is a valid channel but currently disabled (Enabled=false),
//
//	When PUT /api/v1/channels/telegram/routing with {"default_agent_id": "bot-agent"},
//	Then response is 200 and default_agent_id is "bot-agent",
//	And a subsequent GET /api/v1/channels/telegram/routing returns the same value.
//
// Traces to: sprint/258-jun-2026 — channel routing, disabled channel pre-configuration.
func TestChannelRouting_PutDisabledChannelSucceeds(t *testing.T) {
	// newChannelRoutingTestAPI starts with telegram disabled (Enabled not set → defaults to false).
	api, _ := newChannelRoutingTestAPI(t)
	writeTestWorkspaceJSON(t, api, "ops", "active", []string{"bot-agent"}, false)

	body := `{"workspace_id": "ops", "default_agent_id": "bot-agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing", strings.NewReader(body))
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"PUT routing on a disabled channel must succeed (pre-configure before enable)")

	var resp gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Bound)
	assert.Equal(t, "bot-agent", resp.DefaultAgentId)

	// Confirm the binding persists via GET.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/channels/telegram/routing", nil)
	api.HandleChannels(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)

	var resp2 gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))
	assert.True(t, resp2.Bound)
	assert.Equal(t, "ops", resp2.WorkspaceId)
	assert.Equal(t, "bot-agent", resp2.DefaultAgentId,
		"binding persists after pre-configuring disabled channel")
}

// TestChannelRouting_GetUnknownChannelReturns404 verifies that GET /api/v1/channels/{id}/routing
// for an unknown channel returns 404.
//
// BDD: Given "boguschan" is not a known channel,
//
//	When GET /api/v1/channels/boguschan/routing,
//	Then response is 404.
//
// Traces to: sprint/258-jun-2026 — channel routing, unknown channel GET.
func TestChannelRouting_GetUnknownChannelReturns404(t *testing.T) {
	api, _ := newChannelRoutingTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels/boguschan/routing", nil)
	api.HandleChannels(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code, "GET routing for unknown channel must return 404")
}
