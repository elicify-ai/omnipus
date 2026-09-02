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

	// "exec" was retired and unified into "bash" (ADR-036) -- use the current
	// tool name so this test is not itself asserting a stale-name gap the
	// key-validity check (TestHandleToolPolicies_PUT_UnknownKeyRejected) now
	// closes.
	body := `{"policies":{"bash":"deny","search_web":"allow"}}`
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
	assert.Equal(t, "deny", policies["bash"])
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
	policies, policiesOk := resp["policies"].(map[string]any)
	require.True(t, policiesOk, "response policies field must be an object")
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

// TestHandleToolPolicies_PUT_UnknownKeyRejected is the regression test for the
// "PUT accepted a wildcard/garbage tool-policy key" gap found alongside the
// full-catalog UAT round: this endpoint validated every submitted VALUE
// (allow/ask/deny) but never validated that submitted KEYS were real,
// known static builtin tool names. A caller could submit a literal "*" or a
// typo'd tool name with a valid-looking value and have it silently accepted
// and persisted into sandbox.tool_policies (confirmed live before the fix:
// 200, both keys echoed back unchanged). The agent-level tools-write
// endpoints already rejected this shape via
// config.ValidateSubmittedToolPolicyMap's Invalid list; this endpoint now
// does too — but, unlike the agent-level endpoints, it does NOT require
// full catalog coverage from the submitted map alone (a sparse global
// ceiling is the intended shape; withToolPolicyCoverageGuard, tested
// separately above, still enforces that overall coverage stays complete).
func TestHandleToolPolicies_PUT_UnknownKeyRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	body := `{"policies":{"*":"allow","bash":"deny","not_a_real_tool_xyz":"ask"}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "*")
	assert.Contains(t, w.Body.String(), "not_a_real_tool_xyz")

	// Nothing from the rejected request must have been persisted.
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/security/tool-policies", nil)
	getR = withAdminRole(getR)
	getW := httptest.NewRecorder()
	api.HandleToolPolicies(getW, getR)
	assert.NotContains(t, getW.Body.String(), "not_a_real_tool_xyz",
		"a rejected PUT must not leave any of its keys persisted")
}

// TestHandleToolPolicies_PUT_SparseMapAccepted proves the fix above is scoped
// to key VALIDITY only, not completeness: a sparse but entirely-valid-keyed
// global map (naming real tools, just not all of them) must still be
// accepted — a global ceiling deliberately need not cover every tool on its
// own (e.g. the seeded "worker" agent's own map is built to rely on it for
// most tools, by design). newTestRestAPIWithHome seeds an empty agent list,
// so the separate coverage guard (tested above) trivially passes here too;
// this test is specifically about the key-validity check, not coverage.
func TestHandleToolPolicies_PUT_SparseMapAccepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	body := `{"policies":{"bash":"deny"}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)

	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
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
