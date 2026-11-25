// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Skills & Tools redesign (#1963, epic #1519), fix round 4, Part B row 6.
//
// This file used to drive the retired `GET/PUT /security/skill-trust` handler (`HandleSkillTrust`).
// C-3 and C-22 row 6 retire that route and its three schemas: "Trust is read and written as
// `PermissionSettings.skill_trust` (`allow`/`ask`/`deny`; any other value → 400 "Use allow, ask or
// deny.")". Every retired case is re-driven through `PATCH/GET /security/permissions` on the real
// route table (newPermissionsAPI, patchPermissions, getPermissions) with its original oracle kept;
// the legacy wire words block_unverified / warn_unverified / allow_all become deny / ask / allow
// (C-3 migration table, Dataset D3 rows 1-3). Nothing here references the retired handler or its
// generated types, so the file still compiles once the backend deletes them.
//
// Retired with the old surface, each citing C-22 row 6 and C-3:
//   - TestHandleSkillTrust_HotReload: asserted `requires_restart=false`, a member of the retired
//     SkillTrustUpdateResponse. PermissionSettings has no such member; whether a change reached the
//     running agents is the 200 / 503 contract of C-2, asserted in
//     rest_tool_policies_activation_test.go.
//   - TestHandleSkillTrust_AllowAllWarning: asserted a `warning` string in the retired response. The
//     Allow warning is page copy now ("Allow turns off the check for unverified skills.", C-3;
//     Dataset D3 row 8; S-32), covered by src/components/skills/SkillsToolsPage.trust.test.tsx.
//   - TestHandleSkillTrust_MethodNotAllowed: the route is gone; the replacement route's method
//     behaviour is TestPermissions_UnsupportedMethodsAnswer405AndStoreNothing.
//
// The file name is kept because sibling test files cite it.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// skillTrustPatch PATCHes {"skill_trust": level} through the real route table and returns the
// response recorder. The body is marshalled so an empty or odd level is still valid JSON.
func skillTrustPatch(t *testing.T, api *restAPI, routes routeTable, level string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"skill_trust": level})
	require.NoError(t, err)
	return patchPermissions(t, api, routes, string(body))
}

// skillTrustRead GETs the read model and returns its skill_trust member.
func skillTrustRead(t *testing.T, api *restAPI, routes routeTable) any {
	t.Helper()
	w := getPermissions(t, api, routes)
	require.Equal(t, http.StatusOK, w.Code, "GET /security/permissions must exist (C-2); body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp["skill_trust"]
}

// TestPermissionsPatch_SkillTrust_AllThreeValuesAccepted carries
// TestHandleSkillTrust_AllThreeValuesAccepted: each canonical value is accepted and reported back.
// The 200 body is the complete stored PermissionSettings (C-2), so the retired response's
// saved / requires_restart / applied_level members become `skill_trust` equal to the sent value,
// checked on the PATCH response and again on a later read.
func TestPermissionsPatch_SkillTrust_AllThreeValuesAccepted(t *testing.T) {
	for _, level := range []string{"allow", "ask", "deny"} {
		t.Run(level, func(t *testing.T) {
			api, routes := newPermissionsAPI(t)
			w := skillTrustPatch(t, api, routes, level)

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, level, resp["skill_trust"], "the 200 carries the complete stored settings, trust included (C-2)")
			assert.Equal(t, level, skillTrustRead(t, api, routes), "a later read returns the stored trust")
		})
	}
}

// TestPermissionsPatch_SkillTrust_OtherValuesRefused carries the retired Uppercase / MixedCase /
// Invalid / Empty cases (all four answered 400) and adds the retired wire words: C-3 and spec
// error list ("`skill_trust` other than `allow`/`ask`/`deny` → 400, "Use allow, ask or deny."").
// Letter case is rejected as today (Dataset D3 row 7). The empty string is "other than
// allow/ask/deny" read literally; Dataset D3 has no empty PATCH row, so that leg is Inferred
// (reported as a spec silence). After every refusal the stored trust is still ask, so nothing was
// stored (C-2). block_unverified / allow_all join warn_unverified (pinned by
// TestPermissionsPatch_RefusesLegacyTrustValues, D3 row 6).
func TestPermissionsPatch_SkillTrust_OtherValuesRefused(t *testing.T) {
	for _, tc := range []struct{ name, level string }{
		{"all caps ALLOW", "ALLOW"},
		{"mixed case Ask", "Ask"},
		{"unknown word nope", "nope"},
		{"empty string", ""},
		{"retired block_unverified", "block_unverified"},
		{"retired allow_all", "allow_all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, routes := newPermissionsAPI(t)
			require.Equal(t, "ask", skillTrustRead(t, api, routes), "setup/instrument: trust starts as ask (C-3, D3 row 4)")

			w := skillTrustPatch(t, api, routes, tc.level)
			requireErrContains(t, w, http.StatusBadRequest, "Use allow, ask or deny.")

			assert.Equal(t, "ask", skillTrustRead(t, api, routes), "a refused change stores nothing (C-2)")
		})
	}
}

// TestPermissionsPatch_SkillTrust_PersistsToDisk carries TestHandleSkillTrust_PersistsToDisk: a
// successful write reaches config.json as sandbox.skill_trust, stored as the new word (C-3:
// "`sandbox.skill_trust` stores `allow`/`ask`/`deny`").
func TestPermissionsPatch_SkillTrust_PersistsToDisk(t *testing.T) {
	api, routes := newPermissionsAPI(t)
	w := skillTrustPatch(t, api, routes, "deny")
	require.Equal(t, http.StatusOK, w.Code, "PATCH must succeed: %s", w.Body)

	sandbox := permStoredSandbox(t, api.configPath())
	require.NotEmpty(t, sandbox, "sandbox section must be written to disk")
	assert.Equal(t, "deny", sandbox["skill_trust"])
}

// TestPermissionsPatch_SkillTrust_EmitsAuditEntry carries TestHandleSkillTrust_EmitsAuditEntry,
// which called audit.EmitSecuritySettingChange itself and so proved only the emitter. This one
// drives the real PATCH and reads the audit file (C-2 audit rule: "one EmitSecuritySettingChange
// per changed key with actor, old and new value ... resources ... `sandbox.skill_trust`. A key sent
// with its stored value writes no entry."). The old value is seeded explicitly rather than assumed
// from a shipped default.
func TestPermissionsPatch_SkillTrust_EmitsAuditEntry(t *testing.T) {
	api, routes := newPermissionsAPI(t)
	auditDir := attachTestAuditor(t, api)

	seed := skillTrustPatch(t, api, routes, "allow")
	require.Equal(t, http.StatusOK, seed.Code, "setup: %s", seed.Body.String())
	before := len(readAuditEntries(t, auditDir, audit.EventSecuritySettingChange))

	w := skillTrustPatch(t, api, routes, "deny")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	entries := readAuditEntries(t, auditDir, audit.EventSecuritySettingChange)
	require.Len(t, entries, before+1, "exactly one change entry for the one changed key")
	entry := entries[before]
	assert.Equal(t, "sandbox.skill_trust", entry["resource"])
	assert.Equal(t, "allow", entry["old_value"])
	assert.Equal(t, "deny", entry["new_value"])
	assert.Equal(t, envTokenName, entry["actor"], "the authenticated caller is the actor")

	w = skillTrustPatch(t, api, routes, "deny")
	require.Equal(t, http.StatusOK, w.Code, "an equal-value write is a valid no-op: %s", w.Body.String())
	assert.Equal(t, entries, readAuditEntries(t, auditDir, audit.EventSecuritySettingChange),
		"a key sent with its stored value writes no entry (C-2)")
}

// TestPermissionsGet_SkillTrust_DefaultIsAsk carries TestHandleSkillTrust_GET_DefaultLevel, whose
// oracle was only "level is not empty". The spec fixes the value: stored empty means ask
// (C-3: "an empty value is treated as `ask`"; Dataset D3 row 4), served as `allow`/`ask`/`deny`.
func TestPermissionsGet_SkillTrust_DefaultIsAsk(t *testing.T) {
	api, routes := newPermissionsAPI(t)
	assert.Equal(t, "ask", skillTrustRead(t, api, routes))
}
