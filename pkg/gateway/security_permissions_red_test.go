// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), orders 5
// and 8. These go through the real route table built by
// registerAdditionalEndpoints, so today's red is a 404 (the route does not
// exist yet) and the tests go green without edits once
// `PATCH /security/permissions` is registered (C-2).
//
// Oracles: skills-tools-redesign-spec.md C-2 (the PATCH rules), C-3 (trust
// vocabulary), C-6 (locked levels, permission-fixed tools), C-23 (known
// skill keys), Datasets D3 (rows 6, 7, 9) and D6 — every expected status
// and quoted string below is transcribed from the spec, never observed
// from an implementation.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"

	"github.com/stretchr/testify/require"
)

// routeTable is a minimal httpHandlerRegistrar that serves exact-path
// matches and 404s everything else — enough for the flat /security/*
// routes under test, and honest about routes that are not registered yet.
type routeTable map[string]http.Handler

func (m routeTable) RegisterHTTPHandler(pattern string, h http.Handler) { m[pattern] = h }

func (m routeTable) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h, ok := m[r.URL.Path]; ok {
		h.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// envTokenName is the username of the synthetic identity the legacy
// OMNIPUS_BEARER_TOKEN path authenticates as (pkg/gateway/auth.go
// envTokenUser). The re-auth consent token is minted for the same name so
// a re-auth-gated PATCH passes its gate once implemented.
const envTokenName = "_env_token"

const permissionsTestBearer = "skills-tools-permissions-test"

// newPermissionsAPI builds a restAPI with bearer-token auth (no dev-mode
// bypass, so high-blast-radius wrappers stay enforceable), registers the
// full route table, and returns both.
func newPermissionsAPI(t *testing.T) (*restAPI, routeTable) {
	t.Helper()
	api := buildExecutorTestAPI(t) // Its native OMNIPUS_HOME is the same home the routes serve.
	t.Setenv(config.EnvConfig, filepath.Join(api.homePath, "config.json"))
	installPlanSkillFixture(t, api.homePath) // C-23 known installed key used by the permission rows.
	// The fixture clears the token; restore it so through-the-mux
	// requests authenticate via the env bearer path.
	t.Setenv("OMNIPUS_BEARER_TOKEN", permissionsTestBearer)
	routes := routeTable{}
	api.registerAdditionalEndpoints(routes)
	return api, routes
}

// patchPermissions PATCHes /api/v1/security/permissions through the route
// table with bearer auth and a freshly minted re-auth consent token.
func patchPermissions(t *testing.T, api *restAPI, routes routeTable, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/security/permissions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	token, err := api.reauthStoreOrInit().mint(envTokenName)
	require.NoError(t, err, "minting the re-auth consent token must not fail")
	r.Header.Set(reAuthHeader, token)
	r.Header.Set("Authorization", "Bearer "+permissionsTestBearer)
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, r)
	return w
}

// getPermissions GETs the read model.
func getPermissions(t *testing.T, api *restAPI, routes routeTable) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/permissions", nil)
	r.Header.Set("Authorization", "Bearer "+permissionsTestBearer)
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, r)
	return w
}

// requireErrContains asserts a JSON error body with the exact status and a
// message containing the spec's quoted text. When wantText is empty only
// the status is checked (the spec fixes the status but not the wording).
func requireErrContains(t *testing.T, w *httptest.ResponseRecorder, wantCode int, wantText string) {
	t.Helper()
	require.Equal(t, wantCode, w.Code, "body: %s", w.Body.String())
	if wantText != "" {
		require.Contains(t, w.Body.String(), wantText, "error body must carry the spec's text")
	}
}

// TestPermissionsPatch_RefusesLegacyTrustValues (order 5, S-36).
// A legacy trust value sent to the new endpoint is refused with the C-3
// text, trust stays Ask, and the retired PUT /security/skill-trust answers
// 404 (C-22 row 6).
// Traces to: BDD S-36; spec C-3, C-22 row 6; Dataset D3 rows 6, 7, 9.
func TestPermissionsPatch_RefusesLegacyTrustValues(t *testing.T) {
	api, routes := newPermissionsAPI(t)

	// Start from stored trust Ask: the empty stored value means ask today
	// and the C-3 migration maps it to ask (Dataset D3 row 4).
	w := patchPermissions(t, api, routes, `{"skill_trust":"warn_unverified"}`)
	requireErrContains(t, w, http.StatusBadRequest, "Use allow, ask or deny.")

	w = patchPermissions(t, api, routes, `{"skill_trust":"Deny"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "letter case is rejected as today (Dataset D3 row 7), body: %s", w.Body.String())

	// Trust stays Ask after both refusals (read model).
	w = getPermissions(t, api, routes)
	require.Equal(t, http.StatusOK, w.Code, "GET /security/permissions must exist (C-2), body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"skill_trust":"ask"`, "trust stays ask after the refusals (S-36)")

	// The legacy endpoint is retired: PUT /security/skill-trust answers 404
	// and stores nothing (Dataset D3 row 9, C-22 row 6).
	r := httptest.NewRequest(http.MethodPut, legacySkillTrustPath, strings.NewReader(`{"skill_trust":"warn_unverified"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+permissionsTestBearer)
	r = withAdminRole(r)
	w = httptest.NewRecorder()
	routes.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code, "PUT %s is retired by this package (C-22 row 6); got %d body: %s", legacySkillTrustPath, w.Code, w.Body.String())
}

// TestPermissionsPatch_SkillAndVisibilityMembers (order 8, Dataset D6).
// Traces to: BDD S-01, S-09, S-39, S-07, S-99, S-116; spec C-2, C-6, C-20,
// C-23; Dataset D6.
func TestPermissionsPatch_SkillAndVisibilityMembers(t *testing.T) {
	t.Run("D6#1 skill_policies deny changes only that key and writes one audit entry", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		auditDir := attachTestAuditor(t, api)
		// seed one stored tool value so "only plan changed" is observable
		w := patchPermissions(t, api, routes, `{"policies":{"bash":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		before := len(readAuditEntries(t, auditDir, audit.EventSecuritySettingChange))

		w = patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		skillPolicies, ok := resp["skill_policies"].(map[string]any)
		require.True(t, ok, "C-2 skill_policies must be an object")
		require.Equal(t, "deny", skillPolicies["plan"], "S-09: the named skill is stored")
		policies, ok := resp["policies"].(map[string]any)
		require.True(t, ok, "C-2 policies must be an object")
		require.Equal(t, "ask", policies["bash"], "only the named key changed (C-2)")
		entries := readAuditEntries(t, auditDir, audit.EventSecuritySettingChange)
		require.Len(t, entries, before+1, "one EmitSecuritySettingChange per changed key (C-2)")
	})

	t.Run("D6#2 unknown skill key refused, nothing saved", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"nope":"deny"}}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		w = getPermissions(t, api, routes)
		require.NotContains(t, w.Body.String(), `"nope"`, "nothing saved (C-2)")
	})

	t.Run("D6#3 empty skill_policies member changes nothing of the skills", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		before := getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, before.Code, "setup read model: %s", before.Body.String())
		var stored map[string]any
		require.NoError(t, json.Unmarshal(before.Body.Bytes(), &stored))
		require.Contains(t, stored, "skill_policies", "C-2 returns a complete installed-skill map")
		w := patchPermissions(t, api, routes, `{"skill_policies":{},"policies":{"bash":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, stored["skill_policies"], resp["skill_policies"], "empty member preserves the complete stored map, not an invented empty map (C-2)")
		policies, ok := resp["policies"].(map[string]any)
		require.True(t, ok, "C-2 policies must be an object")
		require.Equal(t, "ask", policies["bash"])
	})

	t.Run("D6#4+18 request naming no key at all is Nothing to change", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		for _, body := range []string{`{}`, `{"policies":{},"skill_policies":{}}`} {
			w := patchPermissions(t, api, routes, body)
			requireErrContains(t, w, http.StatusBadRequest, "Nothing to change.")
		}
	})

	t.Run("D6#5 locked level refuses with the lock text", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"visibility":{"AskUserQuestion":"hidden"}}`)
		// C-6: AskUserQuestion is visibility_locked; the spec fixes the
		// status and "lock text" but not the exact wording, so assert the
		// status and a non-empty reason only.
		require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		require.NotEqual(t, "null\n", w.Body.String(), "a refused PATCH carries a reason")
	})

	t.Run("D6#6 permission-fixed tool refuses with the fixed text", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"policies":{"ToolSearch":"deny"}}`)
		requireErrContains(t, w, http.StatusBadRequest, "has no permission of its own")
	})

	t.Run("D6#7 skill_visibility accepts a valid level", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_visibility":{"plan":"full"}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	})

	t.Run("D6#8 skill key letter case applies to the lower-case key", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"PLAN":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		skillPolicies, ok := resp["skill_policies"].(map[string]any)
		require.True(t, ok, "C-2 skill_policies must be an object")
		require.Equal(t, "ask", skillPolicies["plan"], "stored lower case (C-23)")
		require.NotContains(t, skillPolicies, "PLAN", "no duplicate upper-case key stored (C-23)")
	})

	t.Run("D6#9 same skill twice in one request is listed twice", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		auditDir := attachTestAuditor(t, api)
		seed := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"allow"},"policies":{"send_email":"deny"}}`)
		require.Equal(t, http.StatusOK, seed.Code, "setup existing values: %s", seed.Body.String())
		before := getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, before.Code, "setup read model: %s", before.Body.String())
		entries := readAuditEntries(t, auditDir, audit.EventSecuritySettingChange)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"ask","PLAN":"deny"}}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "C-23 duplicate refusal: %s", w.Body.String())
		var failure gen.ErrorResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &failure))
		require.Equal(t, "`plan` is listed twice.", failure.Error, "D6#9 requires the duplicate error, not an unrelated refusal")
		after := getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, after.Code, "after read model: %s", after.Body.String())
		require.JSONEq(t, before.Body.String(), after.Body.String(), "duplicate refusal preserves complete pre-existing state")
		require.Equal(t, entries, readAuditEntries(t, auditDir, audit.EventSecuritySettingChange), "duplicate refusal writes no change audit")
	})

	t.Run("D6#10 null value is refused naming the key", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":null}}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		require.Contains(t, w.Body.String(), "plan", "400 naming the key (C-2)")
	})

	t.Run("D6#11 one bad key refuses the whole request (atomic)", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		before := getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, before.Code, "setup read model: %s", before.Body.String())
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny","nope":"deny"}}`)
		requireErrContains(t, w, http.StatusBadRequest, "`nope` is not a known skill.")
		w = getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, w.Code, "after read model: %s", w.Body.String())
		require.JSONEq(t, before.Body.String(), w.Body.String(), "C-2 complete pre-existing map is preserved, including installed plan")
	})

	t.Run("D6#12 always-ask tool cannot take allow", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"policies":{"set_channel_outbound":"allow"}}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "AlwaysAskAllowViolation (C-2), body: %s", w.Body.String())
	})

	t.Run("D6#13 two tabs changing different keys both keep their change", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"policies":{"send_email":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "tab A, body: %s", w.Body.String())
		w = patchPermissions(t, api, routes, `{"skill_policies":{"plan":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "tab B, body: %s", w.Body.String())
		w = getPermissions(t, api, routes)
		require.Contains(t, w.Body.String(), `"send_email":"deny"`, "tab A kept (CRIT-003)")
		require.Contains(t, w.Body.String(), `"plan":"ask"`, "tab B kept (CRIT-003)")
	})

	t.Run("D6#14 two tabs changing the same key: later acknowledged write wins", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"policies":{"bash":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "tab A, body: %s", w.Body.String())
		w = patchPermissions(t, api, routes, `{"policies":{"bash":"allow"}}`)
		require.Equal(t, http.StatusOK, w.Code, "tab B, body: %s", w.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		policies, ok := resp["policies"].(map[string]any)
		require.True(t, ok, "C-2 policies must be an object")
		require.Equal(t, "allow", policies["bash"], "stored allow (later wins, C-2)")
	})

	t.Run("D6#15 same-value retry saves nothing and writes no audit entry", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		auditDir := attachTestAuditor(t, api)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		before := len(readAuditEntries(t, auditDir, audit.EventSecuritySettingChange))
		w = patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "same value is valid (R2-MAJ-004), body: %s", w.Body.String())
		require.Len(t, readAuditEntries(t, auditDir, audit.EventSecuritySettingChange), before,
			"nothing written, so no audit entry (R2-MAJ-004); running agents are reloaded again by the writer (C-2)")
	})

	t.Run("D6#19 unknown project key is not a known skill", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"project:w9/notes":"ask"}}`)
		requireErrContains(t, w, http.StatusBadRequest, "project:w9/notes` is not a known skill.")
	})

	t.Run("D6#16 project key of an existing workspace project shelf is valid", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		// A real W1 record and protected external mount establish its project
		// shelf. A directory called workspaces/w1 alone is not a workspace.
		seedKeyValidationMounts(t, api, "mia")
		w := patchPermissions(t, api, routes, `{"skill_policies":{"project:w1/notes":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		skillPolicies, ok := resp["skill_policies"].(map[string]any)
		require.True(t, ok, "C-2 skill_policies must be an object")
		require.Equal(t, "ask", skillPolicies["project:w1/notes"], "C-19 project key stored")
	})

	t.Run("D6#20 atomic-write failure stores nothing and says the file could not be written", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		// Make the home directory read-only so the config write fails (the
		// atomic write goes through the home), then restore for cleanup.
		require.NoError(t, os.Chmod(api.homePath, 0o500))
		t.Cleanup(func() { _ = os.Chmod(api.homePath, 0o700) })
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny"}}`)
		requireErrContains(t, w, http.StatusInternalServerError, "Not saved: the settings file could not be written.")
	})

	t.Run("D6#17 a newly installed skill appears with its ask default on GET", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		// Install a skill through the existing upload harness, then the read
		// model must show its key with the ST-Q3 B default (C-2 self-heal).
		// An upload is unverified and the default trust is Ask, so the install goes
		// through the install review (C-3 ask row, C-13, ST-09 d): a direct request
		// answers 409. The read-model assertions below are unchanged.
		data := uat2BuildZip(t, []uat2ZipEntry{
			{name: "SKILL.md", mode: 0o644, body: "---\nname: weather\ndescription: local weather\n---\nbody"},
		})
		w := uat2ReviewThenInstallZip(t, api, "weather", data)
		require.Equal(t, http.StatusOK, w.Code, "setup: install must succeed, body: %s", w.Body.String())
		w = getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		require.Contains(t, w.Body.String(), `"weather":"ask"`, "new-skill default ask (ST-Q3 B, D6#17)")
	})
}
