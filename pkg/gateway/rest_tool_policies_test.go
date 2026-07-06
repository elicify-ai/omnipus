//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// withAdminRole injects an authenticated *config.UserConfig ("admin") into
// the request context. Under the single-user model there is no admin role
// to check anymore — the historical name is kept because this helper is
// called from many test files across the package (rest_retention_test.go,
// rest_rate_limits_test.go, rest_skill_trust_test.go, rest_session_scope_test.go,
// and others outside this cluster's scope). Unit tests that call handlers
// directly (bypassing withAuth) use this so the handler sees an
// authenticated caller for audit attribution.
func withAdminRole(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: "admin"})
	return r.WithContext(ctx)
}

// TestHandleToolPolicies_GET_EmptyState verifies that GET returns the default
// shape when no tool policies have been configured.
func TestHandleToolPolicies_GET_EmptyState(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/tool-policies", nil)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "allow", resp["default_policy"])
	// policies must be an object (not null) even when empty.
	policies, ok := resp["policies"].(map[string]any)
	require.True(t, ok, "policies must be an object, got %T", resp["policies"])
	assert.Empty(t, policies)
}

// TestHandleToolPolicies_PUT_ReturnsPersistedValues verifies that PUT accepts valid
// policy values and echoes them back in the response.
func TestHandleToolPolicies_PUT_ReturnsPersistedValues(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := `{"default_policy":"ask","policies":{"exec":"deny","search_web":"allow"}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	// withReAuthAdmin supplies the admin user/role AND the FR-3.3 re-auth token.
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ask", resp["default_policy"])
	policies, ok := resp["policies"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "deny", policies["exec"])
	assert.Equal(t, "allow", policies["search_web"])
}

// TestHandleToolPolicies_PUT_ReadBack verifies that the config.json is actually
// updated after PUT (write+read round-trip via safeUpdateConfigJSON).
func TestHandleToolPolicies_PUT_ReadBack(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Write.
	putBody := `{"default_policy":"deny","policies":{"browser_evaluate":"ask"}}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies", strings.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putReq = withReAuthAdmin(t, api, putReq) // admin user/role + FR-3.3 re-auth token
	putW := httptest.NewRecorder()
	api.HandleToolPolicies(putW, putReq)
	require.Equal(t, http.StatusOK, putW.Code, "PUT must succeed: %s", putW.Body)

	// Read back.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/security/tool-policies", nil)
	getW := httptest.NewRecorder()
	api.HandleToolPolicies(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &resp))
	assert.Equal(t, "deny", resp["default_policy"])
	policies := resp["policies"].(map[string]any)
	assert.Equal(t, "ask", policies["browser_evaluate"])
}

// TestHandleToolPolicies_PUT_InvalidDefaultPolicy verifies that an invalid
// default_policy value is rejected with 400.
func TestHandleToolPolicies_PUT_InvalidDefaultPolicy(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	body := `{"default_policy":"invalid"}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r) // admin user/role + FR-3.3 re-auth token
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleToolPolicies_PUT_InvalidPerToolPolicy verifies that an invalid
// per-tool policy value is rejected with 400.
func TestHandleToolPolicies_PUT_InvalidPerToolPolicy(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	body := `{"default_policy":"allow","policies":{"exec":"maybe"}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r) // admin user/role + FR-3.3 re-auth token
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleToolPolicies_PUT_BadJSON verifies that malformed JSON is rejected with 400.
func TestHandleToolPolicies_PUT_BadJSON(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies",
		strings.NewReader(`not-json`))
	r = withReAuthAdmin(t, api, r) // admin user/role + FR-3.3 re-auth token
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleToolPolicies_MethodNotAllowed verifies that unsupported HTTP methods return 405.
func TestHandleToolPolicies_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		r := httptest.NewRequest(method, "/api/v1/security/tool-policies", nil)
		w := httptest.NewRecorder()
		api.HandleToolPolicies(w, r)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "method %s should return 405", method)
	}
}
