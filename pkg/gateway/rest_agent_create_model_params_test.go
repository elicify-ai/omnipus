// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// T1 regression coverage: POST /api/v1/agents silently dropped model_params,
// the exact same ADR-037 anti-pattern commit 2b057e15 (Q1) fixed for
// PUT /api/v1/agents/{id} — decoded fine (both AgentCreateRequestMain.yaml
// and AgentCreateRequestSubagent.yaml carry model_params), but createAgent
// never read req/vreq.ModelParams at all, so the response was 201 and
// config.AgentConfig.ModelParams stayed nil on disk, and every subsequent
// GET echoed model_params: null.
//
// This file covers both variants that carry model_params on create (Main
// and Subagent — AgentCreateRequestSubagent3p has no model_params property
// at all, matching its lack of shell_policy/tools_cfg/voice).
package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestCreateAgent_ModelParams_Persisted_Main proves the create-path fix for
// a Main agent: POST with model_params -> 201, response echoes it, the
// persisted entity record on disk carries it, and an independent GET echoes
// it too.
//
// Mutation check: reverting the create-path persist (the modelParamsIn ->
// ac.ModelParams assignment in createAgent) makes this fail at the
// "persisted entity record" and "GET echo" assertions — the create would
// still return 201 (the exact silent-drop bug), just like PUT did before
// 2b057e15.
func TestCreateAgent_ModelParams_Persisted_Main(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Model Params Main","type":"Main","soul":"mp-main-soul","model_params":{"max_tokens":48,"temperature":0.2}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())

	require.NotNil(t, created.ModelParams, "create response must echo model_params, not null")
	require.NotNil(t, created.ModelParams.MaxTokens)
	assert.Equal(t, 48, *created.ModelParams.MaxTokens, "create response must echo max_tokens")
	require.NotNil(t, created.ModelParams.Temperature)
	assert.InDelta(t, 0.2, *created.ModelParams.Temperature, 0.0001, "create response must echo temperature")

	// Persisted entity record on disk (agentstore.Store, ADR-054 D2), not
	// just the in-memory create response.
	store := agentstore.New(api.homePath)
	rec, err := store.Get(created.Id)
	require.NoError(t, err)
	require.NotNil(t, rec.ModelParams, "persisted entity record must carry model_params")
	require.NotNil(t, rec.ModelParams.MaxTokens)
	assert.Equal(t, 48, *rec.ModelParams.MaxTokens)
	require.NotNil(t, rec.ModelParams.Temperature)
	assert.InDelta(t, 0.2, *rec.ModelParams.Temperature, 0.0001)

	// A completely separate GET must echo the same values.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.Id, nil)
	api.HandleAgents(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "get body: %s", wGet.Body.String())
	got := decodeAgentResp(t, wGet.Body.Bytes())
	require.NotNil(t, got.ModelParams, "GET must echo model_params, not null")
	require.NotNil(t, got.ModelParams.MaxTokens)
	assert.Equal(t, 48, *got.ModelParams.MaxTokens)
	require.NotNil(t, got.ModelParams.Temperature)
	assert.InDelta(t, 0.2, *got.ModelParams.Temperature, 0.0001)
}

// TestCreateAgent_ModelParams_Persisted_Subagent is the same round trip for
// the Subagent (native worker) variant — AgentCreateRequestSubagent also
// carries model_params on the wire.
func TestCreateAgent_ModelParams_Persisted_Subagent(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Model Params Sub","type":"Subagent","description":"mp subagent regression","soul":"mp-sub-soul","model_params":{"max_tokens":777,"temperature":0.6}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	require.Equal(t, gen.AgentTypeSubagent, created.Type)

	require.NotNil(t, created.ModelParams, "create response must echo model_params, not null")
	require.NotNil(t, created.ModelParams.MaxTokens)
	assert.Equal(t, 777, *created.ModelParams.MaxTokens)
	require.NotNil(t, created.ModelParams.Temperature)
	assert.InDelta(t, 0.6, *created.ModelParams.Temperature, 0.0001)

	store := agentstore.New(api.homePath)
	rec, err := store.Get(created.Id)
	require.NoError(t, err)
	require.NotNil(t, rec.ModelParams, "persisted entity record must carry model_params")
	require.NotNil(t, rec.ModelParams.MaxTokens)
	assert.Equal(t, 777, *rec.ModelParams.MaxTokens)
	require.NotNil(t, rec.ModelParams.Temperature)
	assert.InDelta(t, 0.6, *rec.ModelParams.Temperature, 0.0001)

	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.Id, nil)
	api.HandleAgents(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "get body: %s", wGet.Body.String())
	got := decodeAgentResp(t, wGet.Body.Bytes())
	require.NotNil(t, got.ModelParams, "GET must echo model_params, not null")
	require.NotNil(t, got.ModelParams.MaxTokens)
	assert.Equal(t, 777, *got.ModelParams.MaxTokens)
	require.NotNil(t, got.ModelParams.Temperature)
	assert.InDelta(t, 0.6, *got.ModelParams.Temperature, 0.0001)
}
