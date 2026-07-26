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

// TestHandleToolPolicies_GET_EmptyState verifies that GET returns the current
// shape when no tool policies have been configured. There is no
// default_policy field any more (CLAUDE.md hard constraint 6) — the response
// carries only "policies", a complete-in-intent map that is empty here
// because nothing has been configured yet.
func TestHandleToolPolicies_GET_EmptyState(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/tool-policies", nil)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	_, hasDefaultPolicy := resp["default_policy"]
	assert.False(t, hasDefaultPolicy, "default_policy no longer exists on the wire")
	// policies must be an object (not null) even when empty.
	policies, ok := resp["policies"].(map[string]any)
	require.True(t, ok, "policies must be an object, got %T", resp["policies"])
	assert.Empty(t, policies)
}

// TestHandleToolPolicies_PUT_ReturnsPersistedValues verifies that PUT accepts valid
// policy values and echoes them back in the response. newTestRestAPIWithHome
// seeds an empty agent list, so config.ValidateToolPolicyCoverage has no
// per-agent gaps to find regardless of how sparse this PUT's policies map is
// (coverage is only checked against agents that actually exist).
func TestHandleToolPolicies_PUT_ReturnsPersistedValues(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := `{"policies":{"exec":"deny","search_web":"allow"}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	// withReAuthAdmin supplies the admin user/role AND the FR-3.3 re-auth token.
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	_, hasDefaultPolicy := resp["default_policy"]
	assert.False(t, hasDefaultPolicy, "default_policy no longer exists on the wire")
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
	putBody := `{"policies":{"browser_evaluate":"ask"}}`
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
	_, hasDefaultPolicy := resp["default_policy"]
	assert.False(t, hasDefaultPolicy, "default_policy no longer exists on the wire")
	policies := resp["policies"].(map[string]any)
	assert.Equal(t, "ask", policies["browser_evaluate"])
}

// TestHandleToolPolicies_PUT_IncompleteCoverageRejected verifies that a PUT
// is rejected with 400 when the resulting global+per-agent policy state would
// leave a static builtin tool uncovered for a live agent
// (config.ValidateToolPolicyCoverage, CLAUDE.md hard constraint 6). This
// replaces the old TestHandleToolPolicies_PUT_InvalidDefaultPolicy, which
// exercised a "default_policy" field that no longer exists on the wire — with
// ValidateInbound off (this harness's default), an unrecognized field is
// silently dropped by the non-strict JSON decode rather than rejected, so
// that body no longer exercises any validation path at all. An incomplete
// policy map is now the meaningful failure mode to test instead.
func TestHandleToolPolicies_PUT_IncompleteCoverageRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// newTestRestAPIWithHome seeds an empty agent list, so coverage trivially
	// passes with no agents to check. Add a live agent with a deliberately
	// sparse builtin policy map (a single tool covered) so most known tools
	// have no per-agent entry — and, since this PUT's global map covers only
	// that same one tool, no global entry either — a genuine coverage gap.
	cfg := api.agentLoop.GetConfig()
	cfgCopy, err := cfg.Clone()
	require.NoError(t, err)
	cfgCopy.Agents.List = append(cfgCopy.Agents.List, config.AgentConfig{
		ID:   "sparse-agent",
		Name: "Sparse Agent",
		Tools: &config.AgentToolsCfg{
			Builtin: config.AgentBuiltinToolsCfg{
				Policies: map[string]config.ToolPolicy{
					"read_file": config.ToolPolicyAllow,
				},
			},
		},
	})
	api.agentLoop.SwapConfig(cfgCopy)
	// ADR-054 D2/D6: this PUT's coverage guard is expected to REJECT the
	// request (400) before ever reaching safeUpdateConfigJSON/persist, so
	// today "sparse-agent" being in-memory-only never actually gets wiped by
	// a reload in this specific test. Persist a real entity record anyway so
	// this fixture stays correct if the guard's rejection path ever changes
	// to reach persistence (which would trigger refreshConfigAndRewireServices
	// and repopulate cfg.Agents.List from the entity store).
	seedAgentEntities(t, api.homePath, cfgCopy.Agents.List)

	body := `{"policies":{"read_file":"allow"}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "coverage")
}

// TestHandleToolPolicies_PUT_InvalidPerToolPolicy verifies that an invalid
// per-tool policy value is rejected with 400.
func TestHandleToolPolicies_PUT_InvalidPerToolPolicy(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	body := `{"policies":{"exec":"maybe"}}`
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
