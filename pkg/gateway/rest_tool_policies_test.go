// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Skills & Tools redesign (#1963, epic #1519), fix round 4, Part B row 6.
//
// This file used to drive the retired `GET/PUT /security/tool-policies` handler
// (`HandleToolPolicies`) directly. C-22 row 6 retires that handler with its schemas, and C-2
// states "The legacy `PUT /security/tool-policies` is retired by this package (C-22), so `PATCH`
// is the only writer of these maps." Every case below that described a property of the global
// tool-policy ceiling is therefore re-driven through the real route table built by
// registerAdditionalEndpoints (newPermissionsAPI, patchPermissions, getPermissions), with its
// original oracle kept. Nothing here references the retired handler or its generated types, so the
// file still compiles once the backend deletes them.
//
// Oracles: skills-tools-redesign-spec.md C-2 (the PATCH rules and the "complete stored maps" read
// model), C-22 row 6, Hard Constraint 6 (no `default_policy` on the wire).
//
// Retired with the old surface, each citing C-22 row 6 and C-2:
//   - TestHandleToolPolicies_PUT_IncompleteCoverageRejected: it replaced the stored ceiling with a
//     sparse map and expected the coverage guard to refuse. PATCH names only the keys to change and
//     "there is no delete through PATCH", so a request cannot uncover a tool; the property that
//     remains ("a key the request does not name is never written") is asserted by
//     TestPermissionsPatch_ToolPolicies_ReturnsPersistedValues and by S-82 in
//     permissions_patch_per_key_red_test.go. The spec names no coverage guard for PATCH (reported
//     as a spec silence).
//   - TestHandleToolPolicies_PUT_SparseMapAccepted: a sparse map is now the only request shape;
//     the persisted-values and read-back tests below send sparse maps and assert 200.
//
// The file name is kept because sibling test files cite it (rest_pending_restart_test.go).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
// rest_rate_limits_test.go, rest_session_scope_test.go, and others outside
// this cluster's scope). Unit tests that call handlers directly (bypassing
// withAuth) use this so the handler sees an authenticated caller for audit
// attribution.
func withAdminRole(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: "admin"})
	return r.WithContext(ctx)
}

// permStoredSandbox reads the `sandbox` object straight from config.json (the stored state, not
// the live copy). It returns an empty map when the file has no sandbox section.
func permStoredSandbox(t *testing.T, configPath string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err, "config.json must be readable")
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk), "config.json must be valid JSON")
	sandbox, _ := onDisk["sandbox"].(map[string]any)
	if sandbox == nil {
		return map[string]any{}
	}
	return sandbox
}

// permStoredToolPolicy reads sandbox.tool_policies[tool] from config.json, nil when absent.
func permStoredToolPolicy(t *testing.T, configPath, tool string) any {
	t.Helper()
	policies, _ := permStoredSandbox(t, configPath)["tool_policies"].(map[string]any)
	return policies[tool]
}

// permDecodeMember decodes one map member of a 200 read-model or PATCH response.
func permDecodeMember(t *testing.T, w *httptest.ResponseRecorder, member string) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	m, ok := resp[member].(map[string]any)
	require.True(t, ok, "C-2: %s is a complete stored map, an object and never null; got %T", member, resp[member])
	return m
}

// TestPermissionsGet_ReadModelShape carries TestHandleToolPolicies_GET_EmptyState's invariant onto
// the replacement read model (C-2): every map member is an object, never null, and there is no
// default_policy on the wire (Hard Constraint 6). The retired test's "policies is empty" state is
// not carried: C-2's self-heal keeps the maps complete at every start.
func TestPermissionsGet_ReadModelShape(t *testing.T) {
	api, routes := newPermissionsAPI(t)
	w := getPermissions(t, api, routes)
	require.Equal(t, http.StatusOK, w.Code, "GET /security/permissions must exist (C-2); body: %s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	_, hasDefaultPolicy := resp["default_policy"]
	assert.False(t, hasDefaultPolicy, "default_policy no longer exists on the wire")
	for _, member := range []string{"policies", "skill_policies", "visibility", "skill_visibility"} {
		_, ok := resp[member].(map[string]any)
		assert.True(t, ok, "C-2: %s must be an object, never null; got %T", member, resp[member])
	}
	assert.Contains(t, []any{"allow", "ask", "deny"}, resp["skill_trust"], "C-3: skill_trust is one of allow, ask, deny")
}

// TestPermissionsPatch_ToolPolicies_ReturnsPersistedValues carries
// TestHandleToolPolicies_PUT_ReturnsPersistedValues: the write is accepted, echoed back, and now
// also leaves every key the request did not name untouched (C-2: "A key the request does not name
// is never written"), which is the property the retired sparse-map test pinned.
func TestPermissionsPatch_ToolPolicies_ReturnsPersistedValues(t *testing.T) {
	api, routes := newPermissionsAPI(t)
	before := permDecodeMember(t, getPermissions(t, api, routes), "policies")

	// "exec" was retired and unified into "bash" (ADR-036): use the current tool names.
	w := patchPermissions(t, api, routes, `{"policies":{"bash":"deny","search_web":"allow"}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	_, hasDefaultPolicy := resp["default_policy"]
	assert.False(t, hasDefaultPolicy, "default_policy no longer exists on the wire")
	policies, ok := resp["policies"].(map[string]any)
	require.True(t, ok, "policies must be an object, got %T", resp["policies"])
	assert.Equal(t, "deny", policies["bash"])
	assert.Equal(t, "allow", policies["search_web"])
	for tool, value := range before {
		if tool == "bash" || tool == "search_web" {
			continue
		}
		assert.Equal(t, value, policies[tool], "C-2: %q was not named by the request, so it must not change", tool)
	}
	assert.Equal(t, "deny", permStoredToolPolicy(t, api.configPath(), "bash"), "the write reached config.json")
}

// TestPermissionsPatch_ToolPolicies_ReadBack carries TestHandleToolPolicies_PUT_ReadBack: a write
// is visible to a later read and is in config.json on disk (the legacy comment: write+read
// round-trip through the atomic config writer). browser_evaluate ships as allow, so ask is a real
// change.
func TestPermissionsPatch_ToolPolicies_ReadBack(t *testing.T) {
	api, routes := newPermissionsAPI(t)

	put := patchPermissions(t, api, routes, `{"policies":{"browser_evaluate":"ask"}}`)
	require.Equal(t, http.StatusOK, put.Code, "PATCH must succeed: %s", put.Body)

	policies := permDecodeMember(t, getPermissions(t, api, routes), "policies")
	assert.Equal(t, "ask", policies["browser_evaluate"])
	assert.Equal(t, "ask", permStoredToolPolicy(t, api.configPath(), "browser_evaluate"), "config.json carries the write")
}

// TestPermissionsPatch_ToolPolicies_InvalidValueRejected carries
// TestHandleToolPolicies_PUT_InvalidPerToolPolicy. The retired test sent the retired tool name
// "exec", so its 400 could have come from the key or the value; this one sends a valid key with an
// invalid value so only the value check can produce the 400, and asserts nothing was stored.
func TestPermissionsPatch_ToolPolicies_InvalidValueRejected(t *testing.T) {
	api, routes := newPermissionsAPI(t)
	before := getPermissions(t, api, routes)
	require.Equal(t, http.StatusOK, before.Code, "instrument: the read model answers; body: %s", before.Body.String())

	w := patchPermissions(t, api, routes, `{"policies":{"bash":"maybe"}}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())

	after := getPermissions(t, api, routes)
	assert.Equal(t, before.Body.String(), after.Body.String(), "a refused request stores nothing (C-2)")
}

// TestPermissionsPatch_ToolPolicies_UnknownOrWildcardKeyRejected carries
// TestHandleToolPolicies_PUT_UnknownKeyRejected, the regression for a garbage or wildcard tool
// key being accepted and persisted. C-2 validates tool keys "as the PUT validates them today"
// (ValidateSubmittedToolPolicyMap) and says "one bad key → 400 naming the first bad key, nothing
// saved". The retired test sent both bad keys at once and required the body to name both; the
// spec names only the first, so each bad key is sent alone beside a valid bash write, and the valid
// key must not be stored either (the whole request is validated before anything is written).
func TestPermissionsPatch_ToolPolicies_UnknownOrWildcardKeyRejected(t *testing.T) {
	for _, tc := range []struct{ name, badKey string }{
		{"wildcard", "*"},
		{"unknown tool name", "not_a_real_tool_xyz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, routes := newPermissionsAPI(t)
			before := getPermissions(t, api, routes)
			require.Equal(t, http.StatusOK, before.Code, "instrument: the read model answers; body: %s", before.Body.String())

			w := patchPermissions(t, api, routes, `{"policies":{"`+tc.badKey+`":"allow","bash":"deny"}}`)
			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.badKey, "the 400 names the bad key (C-2)")

			after := getPermissions(t, api, routes)
			assert.Equal(t, before.Body.String(), after.Body.String(),
				"nothing from the refused request is stored, not even its valid bash key (C-2: validated before write)")
		})
	}
}

// TestPermissionsPatch_BadJSONRejected carries TestHandleToolPolicies_PUT_BadJSON. The spec fixes
// no wording for malformed JSON (reported as a silence), so only the status is pinned, plus that
// nothing was stored.
func TestPermissionsPatch_BadJSONRejected(t *testing.T) {
	api, routes := newPermissionsAPI(t)
	before := getPermissions(t, api, routes)
	require.Equal(t, http.StatusOK, before.Code, "instrument: the read model answers; body: %s", before.Body.String())

	w := patchPermissions(t, api, routes, `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())

	after := getPermissions(t, api, routes)
	assert.Equal(t, before.Body.String(), after.Body.String(), "a refused request stores nothing")
}

// TestPermissions_UnsupportedMethodsAnswer405AndStoreNothing carries
// TestHandleToolPolicies_MethodNotAllowed and TestHandleSkillTrust_MethodNotAllowed onto the
// replacement route. C-2 makes PATCH "the only writer of these maps", so a PUT, POST or DELETE
// carrying a policy change must store nothing (spec-derived). The spec does not name the status
// for an unsupported method: 405 is carried from both retired tests and is Inferred, reported as a
// spec silence. The leading GET is the instrument that tells a method check from a missing route.
func TestPermissions_UnsupportedMethodsAnswer405AndStoreNothing(t *testing.T) {
	api, routes := newPermissionsAPI(t)
	before := getPermissions(t, api, routes)
	require.Equal(t, http.StatusOK, before.Code, "instrument: the route exists, so a 405 below is the method check; body: %s", before.Body.String())

	for _, method := range []string{http.MethodPut, http.MethodPost, http.MethodDelete} {
		r := httptest.NewRequest(method, "/api/v1/security/permissions", strings.NewReader(`{"policies":{"bash":"deny"}}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+permissionsTestBearer)
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "method %s should return 405; body: %s", method, w.Body.String())
	}

	after := getPermissions(t, api, routes)
	assert.Equal(t, before.Body.String(), after.Body.String(), "C-2: PATCH is the only writer, so no other method changes anything")
}
