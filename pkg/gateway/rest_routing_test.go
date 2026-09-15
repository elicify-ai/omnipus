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

// newRoutingTestAPI creates a restAPI with two custom agents and a minimal config.json
// suitable for the routing/default-flag tests. agentA starts as default=true,
// agentB starts as default=false.
//
// RELEASE BLOCKER fix follow-up: "default=true" is now expressed via the
// settings singleton (Agents.Defaults.DefaultAgentID), which
// registry.GetDefaultAgent/routing.resolveDefaultAgentID actually consult and
// which the wire `default` field is derived from (rest.go's listAgents/
// getAgent/updateAgent) — the per-entity AgentConfig.Default bool alongside it
// is kept only for backward display compatibility (see config.go's ADR-054
// D6.4 note) and is never read by resolution logic or echoed back on the
// wire. Both must be set here for agent-a to be genuinely "the default" under
// the fixed contract, or these tests would only coincidentally pass.
func newRoutingTestAPI(t *testing.T) (*restAPI, string) {
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
				DefaultAgentID:    "agent-a",
			},
			List: []config.AgentConfig{
				{ID: "agent-a", Name: "Agent A", Default: true},
				{ID: "agent-b", Name: "Agent B", Default: false},
			},
		},
	}

	// config.json on disk carries no agents.list content (ADR-054) — a
	// stale, misleading shape would just be silently stripped on the next
	// load anyway.
	require.NoError(t, os.WriteFile(cfgPath, marshalConfigForDisk(t, cfg), 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)
	return api, cfgPath
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
				{ID: "bot-agent", Name: "Bot Agent", Default: true},
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

// TestChannelRouting_GetNoBinding verifies GET returns null default_agent_id
// when no channel-wildcard binding exists.
//
// BDD: Given no bindings for channel "telegram",
//
//	When GET /api/v1/channels/telegram/routing,
//	Then response is 200 and default_agent_id is null.
func TestChannelRouting_GetNoBinding(t *testing.T) {
	api, _ := newChannelRoutingTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels/telegram/routing", nil)
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Nil(t, resp.DefaultAgentId, "default_agent_id must be null when no binding exists")
}

// TestChannelRouting_PutCreatesBinding verifies PUT upserts a channel-wildcard binding.
//
// BDD: Given no bindings for channel "telegram",
//
//	When PUT /api/v1/channels/telegram/routing with {"default_agent_id": "bot-agent"},
//	Then response is 200 and default_agent_id is "bot-agent",
//	And a subsequent GET returns the same value.
func TestChannelRouting_PutCreatesBinding(t *testing.T) {
	api, _ := newChannelRoutingTestAPI(t)

	body := `{"default_agent_id": "bot-agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing", strings.NewReader(body))
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT routing must return 200")

	var resp gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.DefaultAgentId)
	assert.Equal(t, "bot-agent", *resp.DefaultAgentId, "PUT response must contain the set agent ID")

	// Verify GET reads back the same binding.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/channels/telegram/routing", nil)
	api.HandleChannels(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var resp2 gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))
	require.NotNil(t, resp2.DefaultAgentId)
	assert.Equal(t, "bot-agent", *resp2.DefaultAgentId, "GET must return the agent ID after PUT")
}

// TestChannelRouting_PutNullRemovesBinding verifies PUT with null default_agent_id
// removes the channel-wildcard binding.
//
// BDD: Given a channel-wildcard binding for "telegram" → "bot-agent",
//
//	When PUT /api/v1/channels/telegram/routing with {"default_agent_id": null},
//	Then response is 200 and default_agent_id is null,
//	And a subsequent GET also returns null.
//
// assertRemoveBindingViaPut creates a telegram channel-wildcard binding, then
// PUTs removeBody (a "remove" payload such as `{"default_agent_id": null}` or
// `{"default_agent_id": ""}`) and asserts the binding is gone: the PUT returns
// 200 with a null default_agent_id, and a subsequent GET also returns null.
func assertRemoveBindingViaPut(t *testing.T, removeBody string) {
	t.Helper()
	api, _ := newChannelRoutingTestAPI(t)

	// First create a binding.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/telegram/routing",
		strings.NewReader(`{"default_agent_id": "bot-agent"}`),
	)
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	// Now remove it.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing", strings.NewReader(removeBody))
	api.HandleChannels(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "PUT remove-value must return 200")

	var resp gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	assert.Nil(t, resp.DefaultAgentId, "PUT remove-value must return null default_agent_id")

	// GET must also show null.
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest(http.MethodGet, "/api/v1/channels/telegram/routing", nil)
	api.HandleChannels(w3, r3)
	require.Equal(t, http.StatusOK, w3.Code)
	var resp3 gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w3.Body).Decode(&resp3))
	assert.Nil(t, resp3.DefaultAgentId, "GET after remove-value PUT must return null")
}

func TestChannelRouting_PutNullRemovesBinding(t *testing.T) {
	assertRemoveBindingViaPut(t, `{"default_agent_id": null}`)
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

	body := `{"default_agent_id": "nonexistent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing", strings.NewReader(body))
	api.HandleChannels(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code, "unknown agent must return 404")
}

// TestChannelRouting_BindingSurvivesReload verifies that a channel-wildcard
// binding written via PUT /routing is present in the live config after the
// atomic-write + reload cycle inside safeUpdateConfigJSON.
//
// BDD: Given PUT /api/v1/channels/discord/routing sets "other-agent",
//
//	When GetConfig().Bindings is inspected,
//	Then a wildcard binding for "discord" → "other-agent" exists.
func TestChannelRouting_BindingSurvivesReload(t *testing.T) {
	api, _ := newChannelRoutingTestAPI(t)

	body := `{"default_agent_id": "other-agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/discord/routing", strings.NewReader(body))
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	// Inspect the live config directly to confirm the binding was persisted and
	// reloaded (safeUpdateConfigJSON calls refreshConfigAndRewireServices).
	liveCfg := api.agentLoop.GetConfig()
	idx := channelWildcardIdx(liveCfg.Bindings, "discord")
	require.GreaterOrEqual(t, idx, 0, "channel-wildcard binding for discord must exist in live config")
	assert.Equal(t, "other-agent", liveCfg.Bindings[idx].AgentID)
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

// TestChannelRouting_TwoChannelsAreIsolated verifies that binding one channel
// does not affect a different channel's binding.
//
// BDD: Given PUT /api/v1/channels/telegram/routing sets "bot-agent",
//
//	When GET /api/v1/channels/discord/routing is called,
//	Then discord's default_agent_id is still null (not contaminated by telegram's binding).
//
// Traces to: sprint/258-jun-2026 — channel routing isolation.
func TestChannelRouting_TwoChannelsAreIsolated(t *testing.T) {
	api, _ := newChannelRoutingTestAPI(t)

	// Set telegram → bot-agent.
	body := `{"default_agent_id": "bot-agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing", strings.NewReader(body))
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	// Verify discord is unaffected (still null).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/channels/discord/routing", nil)
	api.HandleChannels(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)

	var resp gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	assert.Nil(t, resp.DefaultAgentId,
		"discord must have null default_agent_id — telegram's binding must not contaminate it")
}

// TestChannelRouting_PutReplaceExistingBinding verifies that a second PUT for
// the same channel replaces the binding rather than appending a duplicate.
//
// BDD: Given a binding telegram → "bot-agent",
//
//	When PUT /api/v1/channels/telegram/routing with {"default_agent_id": "other-agent"},
//	Then only one wildcard binding for telegram exists and it points to "other-agent".
func TestChannelRouting_PutReplaceExistingBinding(t *testing.T) {
	api, _ := newChannelRoutingTestAPI(t)

	// Create initial binding.
	body := `{"default_agent_id": "bot-agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing", strings.NewReader(body))
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	// Replace with a different agent.
	body2 := `{"default_agent_id": "other-agent"}`
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing", strings.NewReader(body2))
	api.HandleChannels(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)

	var resp gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	require.NotNil(t, resp.DefaultAgentId)
	assert.Equal(t, "other-agent", *resp.DefaultAgentId)

	// Confirm only one wildcard binding for telegram.
	liveCfg := api.agentLoop.GetConfig()
	count := 0
	for _, b := range liveCfg.Bindings {
		if b.Match.Channel == "telegram" && b.Match.AccountID == "*" &&
			b.Match.Peer == nil && b.Match.GuildID == "" && b.Match.TeamID == "" {
			count++
		}
	}
	assert.Equal(t, 1, count, "must have exactly one wildcard binding for telegram after replace")
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

	body := `{"default_agent_id": "bot-agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing", strings.NewReader(body))
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"PUT routing on a disabled channel must succeed (pre-configure before enable)")

	var resp gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.DefaultAgentId)
	assert.Equal(t, "bot-agent", *resp.DefaultAgentId)

	// Confirm the binding persists via GET.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/channels/telegram/routing", nil)
	api.HandleChannels(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)

	var resp2 gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))
	require.NotNil(t, resp2.DefaultAgentId)
	assert.Equal(t, "bot-agent", *resp2.DefaultAgentId,
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

// TestChannelRouting_BindingWinsOverNoGlobalDefault verifies that a channel binding
// resolves to the bound agent even when no global default agent is set.
//
// BDD: Given no agent is marked as default, and a channel-wildcard binding telegram → "bot-agent",
//
//	When PUT /api/v1/channels/telegram/routing binds "bot-agent",
//	And GET /api/v1/channels/telegram/routing is called,
//	Then the binding persists and returns "bot-agent" (not null).
//
// This pins the behavior: a channel binding is independent of the global default.
// Traces to: sprint/258-jun-2026 — routing precedence, channel binding wins.
func TestChannelRouting_BindingWinsOverNoGlobalDefault(t *testing.T) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	// No agent has Default=true.
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
				{ID: "bot-agent", Name: "Bot Agent"}, // Default=false (zero-value)
			},
		},
	}

	require.NoError(t, os.WriteFile(cfgPath, marshalConfigForDisk(t, cfg), 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)

	// Create a channel binding.
	body := `{"default_agent_id": "bot-agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing", strings.NewReader(body))
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	// GET must return the binding even though no global default is set.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/channels/telegram/routing", nil)
	api.HandleChannels(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)

	var resp gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	require.NotNil(t, resp.DefaultAgentId)
	assert.Equal(t, "bot-agent", *resp.DefaultAgentId,
		"channel binding must persist even when no global default agent is set")
}

// TestChannelRouting_PutEmptyStringTreatedAsRemove verifies that PUT with
// default_agent_id="" (empty string) is treated the same as null — removes the binding.
func TestChannelRouting_PutEmptyStringTreatedAsRemove(t *testing.T) {
	assertRemoveBindingViaPut(t, `{"default_agent_id": ""}`)
}

// TestChannelRouting_PutEmptyObjectRemovesBinding verifies that PUT with an
// empty object {} removes the channel-wildcard binding.
func TestChannelRouting_PutEmptyObjectRemovesBinding(t *testing.T) {
	assertRemoveBindingViaPut(t, `{}`)
}
