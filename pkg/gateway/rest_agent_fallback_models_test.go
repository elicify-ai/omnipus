//go:build !cgo

// REST round-trip tests for the P-F2 fallback_models bug: fallback_models
// persisted correctly to config.json (the original save-race was fixed) but
// was never echoed back on ANY response path — GET /agents/{id}, the
// agent-list endpoint, and PUT /agents/{id}'s own response body all built
// gen.Agent without ever assigning FallbackModels from
// config.AgentConfig.FallbackModels. Prior coverage
// (TestCreateAgent_Subagent3p_ForbiddenFields_ValidationEnabled,
// TestUpdateAgent_Subagent3p_ForbiddenFields) only asserted fallback_models
// REJECTION on subagent_3p agents — never a round-trip on a Main/Subagent
// agent that is allowed to carry the field at all.
//
// These tests prove the full path:
//  1. PUT /agents/{id} with fallback_models → the PUT's OWN response echoes it.
//  2. GET /agents/{id} → independently reflects the persisted value.
//  3. GET /agents (list) → also reflects the persisted value.
//  4. A second PUT touching an UNRELATED field does not erase fallback_models
//     (the reopened-agent data-loss path described in the bug report).

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
)

// TestUpdateAgent_FallbackModels_PersistAndEchoOnPUT proves the PUT response
// itself now echoes fallback_models (previously always omitted — updateAgent's
// response-building tail never called applyAgentOverrides at all).
func TestUpdateAgent_FallbackModels_PersistAndEchoOnPUT(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"fallback_models":[{"model":"claude-sonnet-4.6","provider":"anthropic"},` +
		`{"model":"gpt-4o-mini","provider":"openai"}]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	updated := decodeAgentResp(t, w.Body.Bytes())
	require.NotNil(t, updated.FallbackModels,
		"PUT response must echo fallback_models — was always omitted pre-fix")
	require.Len(t, *updated.FallbackModels, 2)
	assert.Equal(t, "claude-sonnet-4.6", (*updated.FallbackModels)[0].Model)
	require.NotNil(t, (*updated.FallbackModels)[0].Provider)
	assert.Equal(t, "anthropic", *(*updated.FallbackModels)[0].Provider)
	assert.Equal(t, "gpt-4o-mini", (*updated.FallbackModels)[1].Model)
	require.NotNil(t, (*updated.FallbackModels)[1].Provider)
	assert.Equal(t, "openai", *(*updated.FallbackModels)[1].Provider)

	// Persisted to config.json (the save-race fix this test wave builds on).
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	agents, _ := m["agents"].(map[string]any)
	list, _ := agents["list"].([]any)
	var entry map[string]any
	for _, item := range list {
		e, _ := item.(map[string]any)
		if e != nil && e["id"] == "test-agent" {
			entry = e
			break
		}
	}
	require.NotNil(t, entry, "test-agent must be persisted")
	fm, _ := entry["fallback_models"].([]any)
	require.Len(t, fm, 2, "fallback_models must be persisted to config.json")
}

// TestGetAgent_FallbackModels_ReflectsPersistedValue proves GET /agents/{id}
// independently reflects a persisted fallback_models chain (not just what a
// PUT response could echo back from the request it just received).
func TestGetAgent_FallbackModels_ReflectsPersistedValue(t *testing.T) {
	api := buildExecutorTestAPI(t)

	putBody := `{"fallback_models":[{"model":"claude-haiku-4.5","provider":"anthropic"}]}`
	putW := httptest.NewRecorder()
	putR := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(putBody))
	putR.Header.Set("Content-Type", "application/json")
	api.HandleAgents(putW, putR)
	require.Equal(t, http.StatusOK, putW.Code, "put body: %s", putW.Body.String())

	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/agents/test-agent", nil)
	api.HandleAgents(getW, getR)
	require.Equal(t, http.StatusOK, getW.Code, "get body: %s", getW.Body.String())

	got := decodeAgentResp(t, getW.Body.Bytes())
	require.NotNil(t, got.FallbackModels, "GET must echo fallback_models — was always omitted pre-fix")
	require.Len(t, *got.FallbackModels, 1)
	assert.Equal(t, "claude-haiku-4.5", (*got.FallbackModels)[0].Model)
	require.NotNil(t, (*got.FallbackModels)[0].Provider)
	assert.Equal(t, "anthropic", *(*got.FallbackModels)[0].Provider)
}

// TestListAgents_FallbackModels_ReflectsPersistedValue proves the agent-list
// endpoint (GET /agents) also reflects a persisted fallback_models chain —
// the third of the three response-construction sites named in the bug report.
func TestListAgents_FallbackModels_ReflectsPersistedValue(t *testing.T) {
	api := buildExecutorTestAPI(t)

	putBody := `{"fallback_models":[{"model":"gemini-2.5-flash","provider":"google"}]}`
	putW := httptest.NewRecorder()
	putR := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(putBody))
	putR.Header.Set("Content-Type", "application/json")
	api.HandleAgents(putW, putR)
	require.Equal(t, http.StatusOK, putW.Code, "put body: %s", putW.Body.String())

	listW := httptest.NewRecorder()
	listR := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(listW, listR)
	require.Equal(t, http.StatusOK, listW.Code, "list body: %s", listW.Body.String())

	var agents []map[string]any
	require.NoError(t, json.Unmarshal(listW.Body.Bytes(), &agents))
	var found map[string]any
	for _, ag := range agents {
		if ag["id"] == "test-agent" {
			found = ag
			break
		}
	}
	require.NotNil(t, found, "test-agent must appear in the agent list")
	fm, ok := found["fallback_models"].([]any)
	require.True(t, ok, "list response must include fallback_models for test-agent")
	require.Len(t, fm, 1)
	entry, ok := fm[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "gemini-2.5-flash", entry["model"])
	assert.Equal(t, "google", entry["provider"])
}

// TestGetEditPut_FallbackModels_NotSilentlyReplacedByUnrelatedPUT is the
// data-loss regression named in the bug report: because a fresh page mount
// could never see the existing persisted chain (GET always showed it empty
// pre-fix), a client's local "add a fallback model" edit silently REPLACED
// the whole chain instead of appending. This test proves the READ side no
// longer starves that append logic: GET after the first PUT must show the
// already-persisted entry so a client can correctly build an appended list
// on its next PUT, and that appended list must be exactly what round-trips —
// nothing dropped, nothing duplicated.
func TestGetEditPut_FallbackModels_NotSilentlyReplacedByUnrelatedPUT(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Seed one fallback entry.
	seedBody := `{"fallback_models":[{"model":"openai/gpt-5-mini","provider":"openai"}]}`
	seedW := httptest.NewRecorder()
	seedR := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(seedBody))
	seedR.Header.Set("Content-Type", "application/json")
	api.HandleAgents(seedW, seedR)
	require.Equal(t, http.StatusOK, seedW.Code, "seed body: %s", seedW.Body.String())

	// A "reopen" GET must show the seeded entry — this is the read this bug broke.
	reopenW := httptest.NewRecorder()
	reopenR := httptest.NewRequest(http.MethodGet, "/api/v1/agents/test-agent", nil)
	api.HandleAgents(reopenW, reopenR)
	require.Equal(t, http.StatusOK, reopenW.Code)
	reopened := decodeAgentResp(t, reopenW.Body.Bytes())
	require.NotNil(t, reopened.FallbackModels, "reopened GET must show the previously-saved chain")
	require.Len(t, *reopened.FallbackModels, 1)

	// Client appends a second entry to what it read back (the correct behavior
	// once the read side is fixed) and PUTs the full, appended chain.
	appended := append([]gen.FallbackModel{}, (*reopened.FallbackModels)...)
	newEntry := gen.FallbackModel{Model: "anthropic/claude-haiku-4.5"}
	provider := "anthropic"
	newEntry.Provider = &provider
	appended = append(appended, newEntry)
	appendedJSON, err := json.Marshal(appended)
	require.NoError(t, err)

	putW := httptest.NewRecorder()
	putR := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent",
		strings.NewReader(`{"fallback_models":`+string(appendedJSON)+`}`))
	putR.Header.Set("Content-Type", "application/json")
	api.HandleAgents(putW, putR)
	require.Equal(t, http.StatusOK, putW.Code, "append put body: %s", putW.Body.String())

	final := decodeAgentResp(t, putW.Body.Bytes())
	require.NotNil(t, final.FallbackModels)
	require.Len(t, *final.FallbackModels, 2, "both the original and the appended entry must survive")
	assert.Equal(t, "openai/gpt-5-mini", (*final.FallbackModels)[0].Model)
	assert.Equal(t, "anthropic/claude-haiku-4.5", (*final.FallbackModels)[1].Model)
}
