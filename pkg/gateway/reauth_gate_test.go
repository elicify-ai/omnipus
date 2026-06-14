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

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// reauthGateAdminUser is the username re-auth-gated tests authenticate as. It is
// distinct from the bcrypt-password user in rest_integrations_auth_test.go so
// these helpers can mint a consent token directly via the in-memory store
// without round-tripping a password (mint is keyed purely by username string).
const reauthGateAdminUser = "reauth-admin"

// withReAuthAdmin attaches BOTH (a) an admin *config.UserConfig under
// UserContextKey{} and the admin RoleContextKey{} role, and (b) a freshly
// minted, valid single-use re-auth consent token under the X-Reauth-Token
// header. Tests that exercise a re-auth-gated PUT handler directly (bypassing
// withAuth/withReAuth middleware) use this so the handler's requireReAuth gate
// passes. The token is minted straight from the API's in-memory reauth store —
// no password verification is exercised here; that path is covered by the
// dedicated re-auth handler tests.
func withReAuthAdmin(t *testing.T, api *restAPI, r *http.Request) *http.Request {
	t.Helper()
	token, err := api.reauthStoreOrInit().mint(reauthGateAdminUser)
	require.NoError(t, err, "minting a re-auth consent token must not fail")
	r.Header.Set(reAuthHeader, token)
	user := &config.UserConfig{Username: reauthGateAdminUser, Role: config.UserRoleAdmin}
	ctx := context.WithValue(r.Context(), UserContextKey{}, user)
	ctx = context.WithValue(ctx, RoleContextKey{}, config.UserRoleAdmin)
	return r.WithContext(ctx)
}

// withReAuthAdminNoToken attaches the admin user + role to the request context
// but DELIBERATELY omits the re-auth consent token — used to prove the gate
// rejects a sensitive mutation that lacks the token.
func withReAuthAdminNoToken(r *http.Request) *http.Request {
	user := &config.UserConfig{Username: reauthGateAdminUser, Role: config.UserRoleAdmin}
	ctx := context.WithValue(r.Context(), UserContextKey{}, user)
	ctx = context.WithValue(ctx, RoleContextKey{}, config.UserRoleAdmin)
	return r.WithContext(ctx)
}

// --- Performance (Spec-3 FR-6.6 / Spec-6 FR-12.2) ---

// TestPerformancePUT_RequiresReAuth proves the max-parallel-agents PUT rejects a
// request that carries the admin user but no re-auth consent token (403).
func TestPerformancePUT_RequiresReAuth(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":4}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"performance PUT without a re-auth token must be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")
}

// TestPerformancePUT_WithReAuth_Succeeds proves the same PUT succeeds once a
// valid consent token is supplied.
func TestPerformancePUT_WithReAuth_Succeeds(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":4}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	require.Equal(t, http.StatusOK, w.Code, "valid re-auth must allow the change; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 4, resp["max_parallel_agents"])
}

// TestPerformancePUT_Unauthenticated_Rejected proves the handler rejects a PUT
// with no authenticated user in context (401), independent of the token gate.
func TestPerformancePUT_Unauthenticated_Rejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":4}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- Sandbox config (Spec-6 FR-12.2) ---

// TestSandboxConfigPUT_RequiresReAuth proves the sandbox-config PUT rejects a
// request that carries the admin user but no re-auth consent token (403).
func TestSandboxConfigPUT_RequiresReAuth(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config",
		strings.NewReader(`{"mode":"permissive"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandleSandboxConfig(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"sandbox-config PUT without a re-auth token must be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")
}

// TestSandboxConfigPUT_WithReAuth_Succeeds proves the same PUT succeeds once a
// valid consent token is supplied.
func TestSandboxConfigPUT_WithReAuth_Succeeds(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config",
		strings.NewReader(`{"mode":"permissive"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleSandboxConfig(w, r)
	require.Equal(t, http.StatusOK, w.Code, "valid re-auth must allow the change; body=%s", w.Body.String())
}

// --- Global tool policies (Spec-3 FR-3.3 / Spec-6 FR-12.2) ---

// TestToolPoliciesPUT_RequiresReAuth proves the global tool-policy PUT rejects a
// request that carries the admin role/user but no re-auth consent token (403).
func TestToolPoliciesPUT_RequiresReAuth(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies",
		strings.NewReader(`{"default_policy":"ask","policies":{"exec":"deny"}}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"tool-policies PUT without a re-auth token must be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "re-typing your password")
}

// TestToolPoliciesPUT_WithReAuth_Succeeds proves the same PUT succeeds once a
// valid consent token is supplied.
func TestToolPoliciesPUT_WithReAuth_Succeeds(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies",
		strings.NewReader(`{"default_policy":"ask","policies":{"exec":"deny"}}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)
	require.Equal(t, http.StatusOK, w.Code, "valid re-auth must allow the change; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ask", resp["default_policy"])
}
