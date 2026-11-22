// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 7.
// TestSkillVerified_LocalChangeUnverifies: a registry skill installed with a
// matching hash is shown as Verified, and a local change to its folder makes
// it unverified again (and subject to trust).
//
// Oracle: skills-tools-redesign-spec.md C-5 `Skill.verified` redefinition —
// "true for a built-in skill, or for a skill whose install was checked
// against a registry hash AND whose folder has no local changes since; false
// otherwise" — and BDD S-34. Today the skills list hardcodes
// Verified = isBuiltin (pkg/gateway/rest_skills.go::listSkills), so the
// derivation this spec defines does not exist; that is the red.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"

	"github.com/stretchr/testify/require"
)

// TestSkillVerified_LocalChangeUnverifies (order 7, S-34).
// Traces to: BDD S-34; spec C-5 (verified redefinition).
func TestSkillVerified_LocalChangeUnverifies(t *testing.T) {
	api := newTestRestAPIWithClawHub(t, true, "")
	api.skillRegistry = &fakeSkillRegistry{installRes: &skills.InstallResult{Verified: true}}

	// Install from the registry: the fake records a hash-matched (verified)
	// install result, the C-5 precondition.
	body := `{"slug":"web-search"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.installSkill(w, r)
	require.Equal(t, http.StatusOK, w.Code, "setup: registry install must succeed, body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"verified":true`, "setup: the install was verified against the registry hash (C-5)")

	// S-34 Given: the skill is shown as Verified before any local change.
	listed := listSkillsBody(t, api)
	require.Contains(t, listed, `"verified":true`, "S-34 Given: the registry skill is shown as Verified (C-5); body: %s", listed)

	// A file in its folder changes outside Omnipus.
	skillDir := filepath.Join(api.homePath, "skills", "web-search")
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: web-search\ndescription: outside edit\n---\nCHANGED"), 0o644))

	// Then: the Skills tab shows it without Verified, and trust applies.
	listed = listSkillsBody(t, api)
	require.Contains(t, listed, `"has_local_changes":true`, "the outside change is detected (S-34)")
	require.NotContains(t, listed, `"verified":true`, "S-34 Then: the locally changed skill is no longer shown as Verified (C-5)")
}

// listSkillsBody GETs /api/v1/skills and returns the body string.
func listSkillsBody(t *testing.T, api *restAPI) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET /skills must succeed, body: %s", w.Body.String())
	return w.Body.String()
}
