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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
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

// TestUpdateAgent_ModelParams_PersistAndEcho reproduces the reported defect
// (PUT {"model_params":{"max_tokens":48}} -> 200, entity record unchanged,
// GET echoes model_params: null) and asserts the fixed round trip: the PUT
// response echoes the value, the entity record on disk carries it, and a
// SEPARATE GET (independent of the PUT response) echoes it too.
//
// Mutation check: reverting the `if req.ModelParams != nil { ... }` persist
// block in updateAgent (pkg/gateway/rest.go) makes this fail at the
// "persisted entity record" and "GET echo" assertions (PUT still returns
// 200 — that is exactly the silent-drop bug).
func TestUpdateAgent_ModelParams_PersistAndEcho(t *testing.T) {
	api, _, tmpDir := newModelParamsTestAPI(t)

	w, putResp := putAgentModelParams(t, api, "agent-a",
		`{"model_params":{"max_tokens":48,"temperature":0.25}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	require.NotNil(t, putResp.ModelParams, "PUT response must echo model_params, not null")
	require.NotNil(t, putResp.ModelParams.MaxTokens)
	assert.Equal(t, 48, *putResp.ModelParams.MaxTokens, "PUT response must echo the persisted max_tokens")
	require.NotNil(t, putResp.ModelParams.Temperature)
	assert.InDelta(t, 0.25, *putResp.ModelParams.Temperature, 0.0001, "PUT response must echo the persisted temperature")

	// Persisted entity record on disk (agentstore.Store, not config.json —
	// ADR-054 D2) must carry the same values, independent of what the PUT
	// handler's in-memory response happened to construct.
	store := agentstore.New(tmpDir)
	rec, err := store.Get("agent-a")
	require.NoError(t, err)
	require.NotNil(t, rec.ModelParams, "persisted entity record must carry model_params")
	require.NotNil(t, rec.ModelParams.MaxTokens)
	assert.Equal(t, 48, *rec.ModelParams.MaxTokens)
	require.NotNil(t, rec.ModelParams.Temperature)
	assert.InDelta(t, 0.25, *rec.ModelParams.Temperature, 0.0001)

	// A completely separate GET must echo the same values — this is the
	// half of the bug the repro in the task description called out
	// explicitly ("GET returns model_params: null").
	wGet, getResp := getAgent(t, api, "agent-a")
	require.Equal(t, http.StatusOK, wGet.Code)
	require.NotNil(t, getResp.ModelParams, "GET must echo model_params, not null")
	require.NotNil(t, getResp.ModelParams.MaxTokens)
	assert.Equal(t, 48, *getResp.ModelParams.MaxTokens)
	require.NotNil(t, getResp.ModelParams.Temperature)
	assert.InDelta(t, 0.25, *getResp.ModelParams.Temperature, 0.0001)
}

// TestUpdateAgent_ModelParams_PersistAndEcho_PartialPatch verifies the
// field-level merge semantics documented at the persist site: a PUT that
// sends only max_tokens must not clobber a temperature set by an earlier
// PUT (mirrors the existing ShellPolicy partial-patch behavior in the same
// handler).
func TestUpdateAgent_ModelParams_PersistAndEcho_PartialPatch(t *testing.T) {
	api, _, _ := newModelParamsTestAPI(t)

	w1, _ := putAgentModelParams(t, api, "agent-a", `{"model_params":{"temperature":0.9}}`)
	require.Equal(t, http.StatusOK, w1.Code)

	w2, resp2 := putAgentModelParams(t, api, "agent-a", `{"model_params":{"max_tokens":777}}`)
	require.Equal(t, http.StatusOK, w2.Code)

	require.NotNil(t, resp2.ModelParams)
	require.NotNil(t, resp2.ModelParams.MaxTokens)
	assert.Equal(t, 777, *resp2.ModelParams.MaxTokens)
	require.NotNil(t, resp2.ModelParams.Temperature, "a partial patch (max_tokens only) must not clobber the previously-set temperature")
	assert.InDelta(t, 0.9, *resp2.ModelParams.Temperature, 0.0001)
}

// TestUpdateAgent_ModelParams_AppliedToEffectiveParams is the DoD test for
// the "applied on the next turn" half of Q1: persistence alone is not
// enough (that would just move the ADR-037 anti-pattern from "not
// persisted" to "persisted but ignored"). It follows
// rest_default_agent_singleton_test.go's precedent exactly: this
// lightweight harness never wires a real reload function, so after the PUT
// it reads a.agentLoop.GetConfig() (already reflects the persisted write —
// updateConfigJSONLocked's refreshConfigAndRewireServices reloads
// config.json and swaps it in) and builds a FRESH agent.AgentRegistry from
// it — the same call (agent.NewAgentRegistry -> pkg/agent/instance.go's
// NewAgentInstance) a real reload/boot or updateAgent's own fastAgentUpsert
// path would make. The resulting AgentInstance's MaxTokens/Temperature are
// exactly what pkg/agent/loop.go's turn-time Chat call reads (ts.agent.MaxTokens/
// ts.agent.Temperature) — asserting on them here proves the override reaches
// the seam the next turn's provider call is built from, without needing to
// drive pkg/agent/loop.go itself (which is owned by a concurrent change in
// this working tree).
//
// Mutation check: reverting the agentCfg.ModelParams override read in
// pkg/agent/instance.go's NewAgentInstance (the maxTokens/temperature ladder
// right after the maxIter block) makes this fail — the resolved instance
// falls back to agents.defaults (4096 / 0.7) instead of the per-agent
// override.
func TestUpdateAgent_ModelParams_AppliedToEffectiveParams(t *testing.T) {
	api, _, _ := newModelParamsTestAPI(t)

	w, _ := putAgentModelParams(t, api, "agent-a",
		`{"model_params":{"max_tokens":321,"temperature":0.05}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	freshCfg := api.agentLoop.GetConfig()
	require.NotNil(t, freshCfg)

	provider := &restMockProvider{}
	freshRegistry := agent.NewAgentRegistry(freshCfg, provider)
	t.Cleanup(freshRegistry.Close)

	inst, ok := freshRegistry.GetAgent("agent-a")
	require.True(t, ok, "agent-a must resolve in a freshly-built registry")
	require.NotNil(t, inst)

	assert.Equal(t, 321, inst.MaxTokens,
		"a freshly-constructed AgentInstance must honor the per-agent model_params.max_tokens override, "+
			"not silently fall back to agents.defaults.max_tokens (4096)")
	assert.InDelta(t, 0.05, inst.Temperature, 0.0001,
		"a freshly-constructed AgentInstance must honor the per-agent model_params.temperature override, "+
			"not silently fall back to agents.defaults.temperature (0.7)")
}

// TestUpdateAgent_ModelParams_TopPRejected asserts the top_p carve-out: the
// wire schema (AgentUpdateRequest.yaml) carries model_params.top_p, but no
// provider adapter in this codebase implements nucleus sampling and there is
// no agents.defaults equivalent to fall back to either. Persisting it
// anyway (200, silently never honored on any turn) would be exactly the
// ADR-037 anti-pattern this fix exists to close, just moved one layer down.
// The handler must refuse it outright instead.
func TestUpdateAgent_ModelParams_TopPRejected(t *testing.T) {
	api, _, tmpDir := newModelParamsTestAPI(t)

	w, _ := putAgentModelParams(t, api, "agent-a", `{"model_params":{"top_p":0.9}}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "top_p")

	// Nothing must have been written — a rejected request must not
	// half-persist a sibling field it was never asked to touch.
	store := agentstore.New(tmpDir)
	rec, err := store.Get("agent-a")
	require.NoError(t, err)
	assert.Nil(t, rec.ModelParams, "a rejected top_p request must not persist anything")
}
