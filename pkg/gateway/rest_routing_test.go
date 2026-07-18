//go:build !cgo

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// newRoutingTestAPI creates a restAPI with two custom agents and a minimal config.json
// suitable for the routing/default-flag tests. agentA starts as default=true,
// agentB starts as default=false.
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
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: "agent-a", Name: "Agent A", Default: true},
				{ID: "agent-b", Name: "Agent B", Default: false},
			},
		},
	}

	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	return api, cfgPath
}

// --- updateAgent default-flag tests ---

// TestUpdateAgent_SetDefaultClearsOthers verifies the single-default invariant:
// setting default=true on agent-b must clear default on agent-a.
//
// BDD: Given agent-a is default=true and agent-b is default=false,
//
//	When PUT /api/v1/agents/agent-b with {"default": true},
//	Then agent-b.Default becomes true,
//	And agent-a.Default becomes false (cleared).
func TestUpdateAgent_SetDefaultClearsOthers(t *testing.T) {
	api, _ := newRoutingTestAPI(t)

	body := `{"default": true}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-b", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "setting default on agent-b must succeed")

	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Default, "response.default must be non-nil")
	assert.True(t, *resp.Default, "agent-b must now be default=true")

	// Verify agent-a is no longer default.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-a", nil)
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var respA gen.Agent
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&respA))
	require.NotNil(t, respA.Default, "agent-a.default must be non-nil after invariant enforcement")
	assert.False(t, *respA.Default, "agent-a must be default=false after agent-b became default")
}

// TestUpdateAgent_SetDefaultFalseOnlyAffectsTarget verifies that setting
// default=false on agent-a does not change agent-b.
//
// BDD: Given agent-a is default=true and agent-b is default=false,
//
//	When PUT /api/v1/agents/agent-a with {"default": false},
//	Then agent-a.Default becomes false,
//	And agent-b.Default remains false (untouched).
func TestUpdateAgent_SetDefaultFalseOnlyAffectsTarget(t *testing.T) {
	api, _ := newRoutingTestAPI(t)

	body := `{"default": false}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-a", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Default)
	assert.False(t, *resp.Default, "agent-a must be default=false")

	// agent-b must remain false (untouched).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-b", nil)
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var respB gen.Agent
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&respB))
	require.NotNil(t, respB.Default)
	assert.False(t, *respB.Default, "agent-b must remain false when only agent-a was updated")
}

// TestUpdateAgent_AbsentDefaultFieldChangesNothing verifies that omitting the
// default field from the request leaves all Default flags unchanged.
//
// BDD: Given agent-a is default=true,
//
//	When PUT /api/v1/agents/agent-a with {"model": "gpt-4"} (no default field),
//	Then agent-a.Default remains true.
func TestUpdateAgent_AbsentDefaultFieldChangesNothing(t *testing.T) {
	api, _ := newRoutingTestAPI(t)

	body := `{"model": "gpt-4"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-a", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	// agent-a must still be default.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-a", nil)
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	require.NotNil(t, resp.Default)
	assert.True(t, *resp.Default, "agent-a must remain default=true when default field was absent")
}

// TestUpdateAgent_RejectsWorkerAsDefault verifies that PUT {"default":true} on a
// worker agent is rejected with 400 — a worker can never be the routing default
// (it is not a chat target).
//
// BDD: Given a worker agent and a base default agent,
//
//	When PUT /api/v1/agents/<worker> with {"default": true},
//	Then the request fails with 400 and the base agent remains default.
func TestUpdateAgent_RejectsWorkerAsDefault(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true},
				{ID: "worker", Name: "Worker", Type: config.AgentTypeWorker},
			},
		},
	}
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/worker", strings.NewReader(`{"default": true}`))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"setting a worker as default must be rejected with 400")
	assert.Contains(t, strings.ToLower(w.Body.String()), "worker",
		"the error must explain a worker cannot be the default")
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
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: "bot-agent", Name: "Bot Agent", Default: true},
				{ID: "other-agent", Name: "Other Agent"},
			},
		},
	}

	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
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

// TestSetChannelRouting_RejectsWorkerTarget verifies M1/MIN-002: PUT
// /api/v1/channels/{id}/routing targeting a worker agent is rejected with 422 —
// a worker is not a chat target and cannot be a channel's default agent. A
// control PUT targeting a base agent must still succeed (200).
//
// BDD: Given a base agent and a worker agent,
//
//	When PUT /api/v1/channels/telegram/routing with {"default_agent_id": "<worker>"},
//	Then the request fails with 422 (MIN-002, standardized from 400) and the error mentions workers/chat targets;
//	And a control PUT with the base agent returns 200.
func TestSetChannelRouting_RejectsWorkerTarget(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true},
				{ID: "worker", Name: "Worker", Type: config.AgentTypeWorker},
			},
		},
	}
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Worker target → 400.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/telegram/routing",
		strings.NewReader(`{"default_agent_id": "worker"}`),
	)
	api.HandleChannels(w, r)
	// MIN-002: worker rejection standardized to 422 (was 400).
	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"a worker target for channel routing must be rejected with 422 (MIN-002)")
	assert.Contains(t, strings.ToLower(w.Body.String()), "worker",
		"the error must explain a worker cannot be a channel's default agent")

	// Confirm no binding was persisted for telegram.
	liveCfg := api.agentLoop.GetConfig()
	assert.Less(t, channelWildcardIdx(liveCfg.Bindings, "telegram"), 0,
		"rejected worker target must not persist a binding")

	// Control: base agent target → 200.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/telegram/routing",
		strings.NewReader(`{"default_agent_id": "mia"}`),
	)
	api.HandleChannels(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "a base agent target must succeed (control)")
	var resp gen.ChannelRouting
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	require.NotNil(t, resp.DefaultAgentId)
	assert.Equal(t, "mia", *resp.DefaultAgentId)
}

// TestUpdateAgent_SetDefaultAlreadyDefault verifies that setting default=true
// on an agent that is already the default is idempotent: it stays default=true
// and no other agent changes.
//
// BDD: Given agent-a is default=true and agent-b is default=false,
//
//	When PUT /api/v1/agents/agent-a with {"default": true} (agent-a is already default),
//	Then agent-a.Default remains true,
//	And agent-b.Default remains false (invariant check: no spurious clear).
//
// Traces to: sprint/258-jun-2026 — single-default invariant, idempotent case.
func TestUpdateAgent_SetDefaultAlreadyDefault(t *testing.T) {
	api, _ := newRoutingTestAPI(t)

	body := `{"default": true}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-a", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "setting default on already-default agent must succeed")

	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Default)
	assert.True(t, *resp.Default, "agent-a must still be default=true")

	// agent-b must still be false (idempotent PUT must not disturb other agents).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-b", nil)
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var respB gen.Agent
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&respB))
	require.NotNil(t, respB.Default)
	assert.False(t, *respB.Default, "agent-b must remain false (idempotent default on agent-a)")
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

// TestUpdateAgent_DiskSingleDefault verifies the disk-level single-default invariant:
// after PUT /api/v1/agents/{id} with {default:true}, re-reading config.json from disk
// must show EXACTLY ONE agent with "default":true.
//
// BDD: Given agent-a is default=true and agent-b is default=false in config.json,
//
//	When PUT /api/v1/agents/agent-b with {"default": true},
//	Then re-reading config.json from disk shows exactly one agent has "default":true,
//	And that agent is agent-b.
//
// Traces to: sprint/258-jun-2026 — updateAgent single-default invariant, disk-level.
func TestUpdateAgent_DiskSingleDefault(t *testing.T) {
	api, cfgPath := newRoutingTestAPI(t)

	body := `{"default": true}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-b", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT agent-b default=true must succeed")

	// Re-read config.json from disk.
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	var diskCfg config.Config
	require.NoError(t, json.Unmarshal(raw, &diskCfg))

	defaultCount := 0
	defaultAgentID := ""
	for _, a := range diskCfg.Agents.List {
		if a.Default {
			defaultCount++
			defaultAgentID = a.ID
		}
	}
	assert.Equal(t, 1, defaultCount, "exactly one agent must have default=true on disk")
	assert.Equal(t, "agent-b", defaultAgentID, "agent-b must be the default agent on disk")
}

// TestUpdateAgent_IdempotentDefault verifies that PUT default=true on an agent that is
// already the default is a no-op: still exactly one default, same agent, others unchanged.
//
// BDD: Given agent-a is already default=true,
//
//	When PUT /api/v1/agents/agent-a with {"default": true} again,
//	Then config.json on disk still has exactly one agent with default=true,
//	And that agent is still agent-a (idempotent).
//
// Traces to: sprint/258-jun-2026 — updateAgent single-default invariant, idempotent case.
func TestUpdateAgent_IdempotentDefault(t *testing.T) {
	api, cfgPath := newRoutingTestAPI(t)

	// agent-a is already default=true in newRoutingTestAPI.
	body := `{"default": true}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-a", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT agent-a default=true (already default) must succeed")

	// Re-read config.json from disk.
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	var diskCfg config.Config
	require.NoError(t, json.Unmarshal(raw, &diskCfg))

	defaultCount := 0
	defaultAgentID := ""
	for _, a := range diskCfg.Agents.List {
		if a.Default {
			defaultCount++
			defaultAgentID = a.ID
		}
	}
	// Must still be exactly one default — not zero (cleared) and not two (doubled).
	assert.Equal(t, 1, defaultCount, "idempotent PUT must keep exactly one default on disk")
	assert.Equal(t, "agent-a", defaultAgentID, "agent-a must remain the default agent on disk")
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
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: "bot-agent", Name: "Bot Agent"}, // Default=false (zero-value)
			},
		},
	}

	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

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
