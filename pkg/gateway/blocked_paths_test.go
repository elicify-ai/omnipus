// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- matchBlockedPath unit tests ---

func TestMatchBlockedPath_NestedGatewayUsers(t *testing.T) {
	body := map[string]any{
		"gateway": map[string]any{
			"users": []any{map[string]any{"username": "evil", "role": "admin"}},
		},
	}
	path, blocked := matchBlockedPath(body, blockedPaths)
	assert.True(t, blocked, "nested gateway.users must be blocked")
	assert.Equal(t, "gateway.users", path)
}

func TestMatchBlockedPath_DotPathLiteral(t *testing.T) {
	body := map[string]any{
		"gateway.users": []any{map[string]any{"username": "evil"}},
	}
	path, blocked := matchBlockedPath(body, blockedPaths)
	assert.True(t, blocked, "dot-path literal key gateway.users must be blocked")
	assert.Equal(t, "gateway.users", path)
}

func TestMatchBlockedPath_MixedBenignAndBlocked(t *testing.T) {
	body := map[string]any{
		"gateway": map[string]any{
			"port":  float64(5000),
			"users": []any{map[string]any{"username": "evil"}},
		},
	}
	path, blocked := matchBlockedPath(body, blockedPaths)
	assert.True(t, blocked, "benign sibling must not shield the blocked path")
	assert.Equal(t, "gateway.users", path)
}

func TestMatchBlockedPath_TopLevelSandbox(t *testing.T) {
	body := map[string]any{
		"sandbox": map[string]any{},
	}
	path, blocked := matchBlockedPath(body, blockedPaths)
	assert.True(t, blocked, "top-level sandbox must be blocked")
	assert.Equal(t, "sandbox", path)
}

// TestMatchBlockedPath_NestedAgentsList pins ADR-054 §11 checklist item 7:
// agents.list must be blocked from the generic PUT /api/v1/config endpoint —
// agent CRUD goes exclusively through the agent store / dedicated
// /api/v1/agents endpoints now.
func TestMatchBlockedPath_NestedAgentsList(t *testing.T) {
	body := map[string]any{
		"agents": map[string]any{
			"list": []any{map[string]any{"id": "evil", "default": true}},
		},
	}
	path, blocked := matchBlockedPath(body, blockedPaths)
	assert.True(t, blocked, "nested agents.list must be blocked")
	assert.Equal(t, "agents.list", path)
}

// TestMatchBlockedPath_AgentsDefaultsUnblocked is the regression a reviewer
// caught in ADR-054 v2: blocking agents.list must NOT also block
// agents.defaults (a SETTING, D1 — including agents.defaults.default_agent_id,
// D6.4). Blocking the "agents" ancestor instead of "agents.list" specifically
// would make this body match matchBlockedPath's ancestor rule and reject
// every agents.defaults write.
func TestMatchBlockedPath_AgentsDefaultsUnblocked(t *testing.T) {
	body := map[string]any{
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "glm-4.7"},
		},
	}
	path, blocked := matchBlockedPath(body, blockedPaths)
	assert.False(t, blocked, "agents.defaults must remain writable: matched %q", path)
}

func TestMatchBlockedPath_UnblockedKey(t *testing.T) {
	body := map[string]any{
		"gateway": map[string]any{"port": float64(5000)},
	}
	path, blocked := matchBlockedPath(body, blockedPaths)
	assert.False(t, blocked, "gateway.port alone must not be blocked")
	assert.Equal(t, "", path)
}

func TestMatchBlockedPath_DevModeBypass(t *testing.T) {
	body := map[string]any{
		"gateway": map[string]any{"dev_mode_bypass": true},
	}
	path, blocked := matchBlockedPath(body, blockedPaths)
	assert.True(t, blocked, "nested gateway.dev_mode_bypass must be blocked")
	assert.Equal(t, "gateway.dev_mode_bypass", path)
}

// --- Integration tests: PUT /api/v1/config via updateConfig handler ---
//
// These tests call updateConfig directly to bypass the admin middleware.
// The walker is the unit under test — the middleware is orthogonal.

// readConfigOnDisk returns the raw config.json contents for the given API so
// tests can assert atomic-reject semantics (nothing persisted on 403).
func readConfigOnDisk(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err, "config.json must be readable")
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m), "config.json must be valid JSON")
	return m
}

func TestConfigPUT_CannotSetGatewayUsers_Nested(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	before := readConfigOnDisk(t, api)

	body := `{"gateway":{"users":[{"username":"evil","role":"admin"}]}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)

	require.Equal(t, http.StatusForbidden, w.Code, "nested gateway.users must be 403")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "gateway.users",
		"error message must name the blocked path so the operator knows which endpoint to use")

	after := readConfigOnDisk(t, api)
	assert.Equal(t, before, after, "rejected PUT must not mutate config.json")
}

func TestConfigPUT_CannotSetGatewayUsers_DotPathLiteral(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	before := readConfigOnDisk(t, api)

	body := `{"gateway.users":[{"username":"evil","role":"admin"}]}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)

	require.Equal(t, http.StatusForbidden, w.Code, "dot-path literal gateway.users must be 403")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "gateway.users")

	after := readConfigOnDisk(t, api)
	assert.Equal(t, before, after, "rejected PUT must not mutate config.json")
}

func TestConfigPUT_CannotSetGatewayUsers_Mixed(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	before := readConfigOnDisk(t, api)

	body := `{"gateway":{"port":5000,"users":[{"username":"evil","role":"admin"}]}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)

	require.Equal(t, http.StatusForbidden, w.Code,
		"mixed body with benign sibling must still be rejected")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "gateway.users")

	after := readConfigOnDisk(t, api)
	assert.Equal(t, before, after,
		"atomic reject: benign sibling (gateway.port) must NOT be persisted when the body contains a blocked path")
}

func TestConfigPUT_CannotSetDevModeBypass(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	before := readConfigOnDisk(t, api)

	body := `{"gateway":{"dev_mode_bypass":true}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)

	require.Equal(t, http.StatusForbidden, w.Code, "gateway.dev_mode_bypass must be 403")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "gateway.dev_mode_bypass")

	after := readConfigOnDisk(t, api)
	assert.Equal(t, before, after, "rejected PUT must not mutate config.json")
}

// TestConfigPUT_CannotSetAgentsList_Nested pins ADR-054 §11 checklist item 7
// end-to-end through the real handler.
func TestConfigPUT_CannotSetAgentsList_Nested(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	before := readConfigOnDisk(t, api)

	body := `{"agents":{"list":[{"id":"evil","default":true}]}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)

	require.Equal(t, http.StatusForbidden, w.Code, "nested agents.list must be 403")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "agents.list",
		"error message must name the blocked path so the operator knows which endpoint to use")

	after := readConfigOnDisk(t, api)
	assert.Equal(t, before, after, "rejected PUT must not mutate config.json")
}

// TestConfigPUT_AgentsDefaultsSucceeds is the regression a reviewer caught in
// ADR-054 v2: PUT /api/v1/config with {"agents":{"defaults":...}} must still
// work after agents.list is blocked.
func TestConfigPUT_AgentsDefaultsSucceeds(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := `{"agents":{"defaults":{"model_name":"glm-4.7"}}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"agents.defaults must remain writable: body=%s", w.Body.String())

	after := readConfigOnDisk(t, api)
	agents, ok := after["agents"].(map[string]any)
	require.True(t, ok, "agents section must be an object after write")
	defaults, ok := agents["defaults"].(map[string]any)
	require.True(t, ok, "agents.defaults must be an object after write")
	assert.Equal(t, "glm-4.7", defaults["model_name"], "agents.defaults.model_name must reflect the PUT body")
}

func TestConfigPUT_UnblockedKeySucceeds(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := `{"gateway":{"port":5001}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"unblocked key must pass through to safeUpdateConfigJSON: body=%s", w.Body.String())

	after := readConfigOnDisk(t, api)
	gw, ok := after["gateway"].(map[string]any)
	require.True(t, ok, "gateway section must be an object after write")
	assert.Equal(t, float64(5001), gw["port"], "gateway.port must reflect the PUT body")
}
