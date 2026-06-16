//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Wave 5 REST endpoint tests — SEC-01/02/03 sandbox status.
//
// These tests exercise:
//   - GET /api/v1/security/sandbox-status — returns 200 with valid JSON
//   - GET /api/v1/security/sandbox-status — response includes "backend" field
//   - Non-GET methods return 405

// TestHandleSandboxStatus_Returns200 verifies the endpoint returns 200 with
// a parseable JSON body in the default test configuration.
func TestHandleSandboxStatus_Returns200(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	api.HandleSandboxStatus(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "response must be valid JSON")
}

// TestHandleSandboxStatus_HasBackendField verifies the response always contains
// the "backend" field, which is guaranteed to be non-empty (at minimum "none").
func TestHandleSandboxStatus_HasBackendField(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	api.HandleSandboxStatus(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	backend, exists := body["backend"]
	assert.True(t, exists, `response must contain "backend" field`)
	backendStr, isString := backend.(string)
	assert.True(t, isString, `"backend" field must be a string`)
	assert.NotEmpty(t, backendStr, `"backend" field must not be empty`)

	// "available" must always be present as a boolean.
	_, exists = body["available"]
	assert.True(t, exists, `response must contain "available" field`)

	// "seccomp_enabled" must always be present.
	_, exists = body["seccomp_enabled"]
	assert.True(t, exists, `response must contain "seccomp_enabled" field`)

	// "kernel_level" must always be present.
	_, exists = body["kernel_level"]
	assert.True(t, exists, `response must contain "kernel_level" field`)
}

// TestHandleSandboxStatus_MethodNotAllowed verifies that POST/PUT/DELETE return 405.
func TestHandleSandboxStatus_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(method, "/api/v1/security/sandbox-status", nil)
			api.HandleSandboxStatus(w, r)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}

// --- HandleSandboxConfig tests (bug 4) ---

// TestHandleSandboxConfig_GETReturnsCurrentConfig verifies the GET path
// returns the persisted sandbox configuration in the expected JSON shape.
func TestHandleSandboxConfig_GETReturnsCurrentConfig(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/sandbox-config", nil)
	api.HandleSandboxConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	// Must contain all the editable fields even when config.json lacks them
	// (the response uses zero values / empty arrays for unset fields).
	for _, key := range []string{
		"mode", "allow_network_outbound", "allowed_paths",
		"ssrf_enabled", "ssrf_allow_internal", "applied_mode",
	} {
		_, ok := body[key]
		assert.True(t, ok, "GET /sandbox-config response must include %q", key)
	}
}

// TestHandleSandboxConfig_PUTPersistsMode verifies that writing a new mode
// via PUT is reflected in a subsequent GET and in config.json on disk.
func TestHandleSandboxConfig_PUTPersistsMode(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := `{"mode":"permissive"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config",
		strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	api.HandleSandboxConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"valid PUT must return 200; got body=%s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "permissive", resp["mode"],
		"response must echo the new mode")
	assert.Equal(t, true, resp["requires_restart"],
		"response must flag restart-required (FR-J-015 no hot-reload)")

	// GET should now return the new mode too.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/security/sandbox-config", nil)
	api.HandleSandboxConfig(w2, r2)
	var readBack map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &readBack))
	assert.Equal(t, "permissive", readBack["mode"],
		"GET must reflect the value just persisted")
}

// TestHandleSandboxConfig_PUTRejectsInvalidMode verifies that a garbage
// mode value is rejected with 400 BEFORE any disk write happens.
func TestHandleSandboxConfig_PUTRejectsInvalidMode(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := `{"mode":"bananas"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config",
		strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	api.HandleSandboxConfig(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid sandbox mode")

	// Verify no disk write happened: GET still returns the default.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/security/sandbox-config", nil)
	api.HandleSandboxConfig(w2, r2)
	var readBack map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &readBack))
	assert.NotEqual(t, "bananas", readBack["mode"],
		"a rejected mode must not leak into persisted config")
}

// TestHandleSandboxConfig_PUTPartialUpdate verifies that omitting a field
// in the request body leaves that field unchanged on disk (pointer-null
// semantics).
func TestHandleSandboxConfig_PUTPartialUpdate(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// First PUT: set both mode and ssrf_enabled.
	body1 := `{"mode":"enforce","ssrf_enabled":true}`
	r1 := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config",
		strings.NewReader(body1))
	r1.Header.Set("Content-Type", "application/json")
	r1 = withReAuthAdmin(t, api, r1)
	w1 := httptest.NewRecorder()
	api.HandleSandboxConfig(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code)

	// Second PUT: only mode — ssrf_enabled must survive.
	body2 := `{"mode":"off"}`
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config",
		strings.NewReader(body2))
	r2.Header.Set("Content-Type", "application/json")
	r2 = withReAuthAdmin(t, api, r2)
	w2 := httptest.NewRecorder()
	api.HandleSandboxConfig(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	assert.Equal(t, "off", resp["mode"])
	assert.Equal(t, true, resp["ssrf_enabled"],
		"omitting ssrf_enabled in the second PUT must not wipe the earlier value")
}

// TestHandleSandboxConfig_UnsupportedMethod verifies that verbs other than
// GET/PUT return 405. DELETE/POST/PATCH are not valid on this endpoint.
func TestHandleSandboxConfig_UnsupportedMethod(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	for _, m := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		r := httptest.NewRequest(m, "/api/v1/security/sandbox-config", nil)
		w := httptest.NewRecorder()
		api.HandleSandboxConfig(w, r)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "verb %s must 405", m)
	}
}
