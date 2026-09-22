// rest_skills_test.go: tests for installed skills and the skill marketplace

package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest.go tests 2026-09-15 ---

// --- E13: listSkills type assertion test ---

// TestListSkillsEmpty verifies that GET /api/v1/skills returns an empty array
// when no skills are configured.
// BDD: Given no skills are installed,
// When GET /api/v1/skills is called,
// Then 200 with an empty array (not null).
// Traces to: wave5a-wire-ui-spec.md — Scenario: Skills list empty (E13)
func TestListSkillsEmpty(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	r.URL.Path = "/api/v1/skills"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	body := strings.TrimSpace(w.Body.String())
	// Response must be a JSON array (empty or populated).
	assert.True(t, strings.HasPrefix(body, "["),
		"skills response must be a JSON array, got: %q", body)
}

// TestListSkillsTypedResponse verifies that skillResponse fields are correctly
// typed and populated when skills are present in startup info.
// This tests the type assertion logic in listSkills().
// Traces to: wave5a-wire-ui-spec.md — Scenario: Skill response shape (E13)
func TestListSkillsTypedResponse(t *testing.T) {
	// listSkills pulls data from agentLoop.GetStartupInfo()["skills"].(map[string]any).
	// With the default test config (no skills loaded), the result is an empty array.
	// This test verifies the empty-map path and that we never return null.
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	r.URL.Path = "/api/v1/skills"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var skills []gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &skills),
		"skills response must unmarshal into []gen.Skill")

	// All returned skills must have required fields.
	for i, s := range skills {
		assert.NotEmpty(t, s.Id, "skills[%d].id must not be empty", i)
		assert.NotEmpty(t, s.Name, "skills[%d].name must not be empty", i)
		assert.NotEmpty(t, s.Version, "skills[%d].version must not be empty", i)
		assert.NotEmpty(t, s.Status, "skills[%d].status must not be empty", i)
	}
}

// TestListSkillsRedirectsMethodNotAllowed verifies that non-GET methods return 405.
// Traces to: wave5a-wire-ui-spec.md — E13: skills method validation
func TestListSkillsMethodNotAllowed(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/skills", nil)
	r.URL.Path = "/api/v1/skills"
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestDeleteSkillRemovesFromGlobalSkillsDir is a regression test for the
// workspace-root mismatch found alongside the remove_skill defect. Under
// ADR-046 FR-009, install_skill (the chat tool) targets the single, fixed,
// install-wide GLOBAL skills directory ($OMNIPUS_HOME/skills,
// pkg/agent.globalSkillsDir) — never any individual agent's own workspace —
// so deleteSkill's installer MUST search that same $OMNIPUS_HOME/skills
// directory (i.e. be rooted at a.homePath, since NewSkillInstaller joins
// "skills" onto its root itself) for a delete of a real installed skill to
// ever succeed. An earlier version of this handler resolved the installer
// against a specific agent's Workspace instead, which was correct for the
// PRE-ADR-046 per-agent-workspace model but points at a directory
// install_skill no longer writes into post-ADR-046.
// Traces to: UAT defect 1 (remove_skill / skill-management identity mismatch).
func TestDeleteSkillRemovesFromGlobalSkillsDir(t *testing.T) {
	// Not using newTestRestAPIWithHome here: it mints its OWN internal
	// t.TempDir() for homePath, but OMNIPUS_HOME (read by
	// pkg/agent.getGlobalConfigDir(), independently of cfg.Agents.Defaults.Home)
	// must be set to that SAME directory, and it must be set before the agent
	// loop (and its default agent's SkillsLoader) is constructed — a chicken-
	// and-egg the shared helper can't satisfy since it doesn't expose its
	// tmpDir until after construction. Build the same shape inline instead,
	// driven by one tmpDir we control from the start.
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		taskLock:      task.TaskFileLock,
	}

	// Install a skill exactly the way install_skill's tool does: a directory
	// named after the slug under the global skills dir ($OMNIPUS_HOME/skills).
	slug := "docker-compose"
	skillDir := filepath.Join(tmpDir, "skills", slug)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: docker-compose\ndescription: manage compose stacks\n---\n"), 0o644))
	revision, err := skills.NewSkillWriter(filepath.Join(tmpDir, "skills")).SkillRevision(slug)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/"+slug+"?revision="+revision, nil)
	r.URL.Path = "/api/v1/skills/" + slug
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code, "response body: %s", w.Body.String())

	_, statErr := os.Stat(skillDir)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "skill directory should have been removed")
}

// TestDeleteSkillRejectsBuiltinSkill verifies the 403 guard that protects the
// embedded default skills (seeded into the global skills dir on first boot)
// from being deleted through the API — the frontend disables the button, but
// the backend is the enforcing gate.
func TestDeleteSkillRejectsBuiltinSkill(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)
	api := newTestRestAPIWithHome(t)

	names := skills.DefaultSkillNames()
	require.NotEmpty(t, names, "the embedded default skill set must be non-empty")
	builtinName := names[0]

	// Seed it on disk exactly where the global skills dir would hold it, so
	// a permissive resolver couldn't accidentally 404 its way to a false pass.
	skillDir := filepath.Join(tmpDir, "skills", builtinName)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte(fmt.Sprintf("---\nname: %s\ndescription: builtin\n---\n", builtinName)), 0o644))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/"+builtinName, nil)
	r.URL.Path = "/api/v1/skills/" + builtinName
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code, "response body: %s", w.Body.String())
	// Must survive the rejected delete.
	_, statErr := os.Stat(skillDir)
	assert.NoError(t, statErr, "builtin skill directory must not be removed")
}

// TestDeleteSkillNotFoundForUnknownSkill verifies the 404 path still works
// once the workspace root is fixed (i.e. the fix doesn't turn every delete
// into a false-positive success).
func TestDeleteSkillNotFoundForUnknownSkill(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/does-not-exist?revision=reviewed-absent", nil)
	r.URL.Path = "/api/v1/skills/does-not-exist"
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestSkillMarketplaceStatusEnabledByDefault verifies GET /skills/marketplace
// reports enabled:true when ClawHub is enabled (the production default).
// BDD: Given ClawHub is enabled,
// When GET /api/v1/skills/marketplace is called,
// Then 200 with enabled:true and a clawhub registry marked enabled.
func TestSkillMarketplaceStatusEnabledByDefault(t *testing.T) {
	api := newTestRestAPIWithClawHub(t, true, "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/marketplace", nil)
	r.URL.Path = "/api/v1/skills/marketplace"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var status gen.SkillMarketplaceStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	assert.True(t, status.Enabled, "marketplace must be enabled when ClawHub is enabled")
	require.Len(t, status.Registries, 1, "only the clawhub registry is reported when no github token")
	assert.Equal(t, "clawhub", status.Registries[0].Name)
	assert.True(t, status.Registries[0].Enabled)
}

// TestSkillMarketplaceStatusGithubTokenEnables verifies a configured GitHub
// registry token enables the marketplace even when ClawHub is disabled, and that
// the github registry is reported.
func TestSkillMarketplaceStatusGithubTokenEnables(t *testing.T) {
	api := newTestRestAPIWithClawHub(t, false, "GITHUB_TOKEN")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/marketplace", nil)
	r.URL.Path = "/api/v1/skills/marketplace"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var status gen.SkillMarketplaceStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	assert.True(t, status.Enabled, "marketplace must be enabled via the github token")

	byName := map[string]bool{}
	for _, reg := range status.Registries {
		byName[reg.Name] = reg.Enabled
	}
	enabled, ok := byName["clawhub"]
	require.True(t, ok, "clawhub registry must always be reported")
	assert.False(t, enabled, "clawhub must report disabled")
	enabled, ok = byName["github"]
	require.True(t, ok, "github registry must be reported when a token ref is set")
	assert.True(t, enabled, "github must report enabled")
}

// TestSkillMarketplaceStatusDisabled verifies GET /skills/marketplace reports
// enabled:false when ClawHub is disabled and no GitHub token is configured.
// BDD: Given ClawHub is disabled and no GitHub token is set,
// When GET /api/v1/skills/marketplace is called,
// Then 200 with enabled:false (clawhub still listed, disabled).
func TestSkillMarketplaceStatusDisabled(t *testing.T) {
	api := newTestRestAPIWithClawHub(t, false, "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/marketplace", nil)
	r.URL.Path = "/api/v1/skills/marketplace"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var status gen.SkillMarketplaceStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	assert.False(t, status.Enabled, "marketplace must be disabled when neither registry is enabled")
	require.Len(t, status.Registries, 1)
	assert.Equal(t, "clawhub", status.Registries[0].Name)
	assert.False(t, status.Registries[0].Enabled)
}

// TestSearchSkillsConflictWhenMarketplaceDisabled verifies that searching the
// marketplace is refused with 409 when no marketplace is enabled — before any
// registry call is attempted.
// BDD: Given no skill marketplace is enabled,
// When GET /api/v1/skills/search?q=x is called,
// Then 409 with "no skill marketplace is enabled".
func TestSearchSkillsConflictWhenMarketplaceDisabled(t *testing.T) {
	api := newTestRestAPIWithClawHub(t, false, "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=web", nil)
	r.URL.Path = "/api/v1/skills/search"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "no skill marketplace is enabled")
}

// TestInstallSkillConflictWhenMarketplaceDisabled verifies that installing a
// skill by slug is refused with 409 when no marketplace is enabled — before any
// registry call is attempted.
// BDD: Given no skill marketplace is enabled,
// When POST /api/v1/skills/install with a valid slug is called,
// Then 409 with "no skill marketplace is enabled".
func TestInstallSkillConflictWhenMarketplaceDisabled(t *testing.T) {
	api := newTestRestAPIWithClawHub(t, false, "")

	slug := "web-search"
	body, err := json.Marshal(gen.SkillInstallRequest{Slug: &slug})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", bytes.NewReader(body))
	r.URL.Path = "/api/v1/skills/install"
	r.Header.Set("Content-Type", "application/json")
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "no skill marketplace is enabled")
}

// TestSearchSkillsMapsResults verifies a stubbed registry's results are mapped
// onto the SkillSearchResult wire type field-for-field.
func TestSearchSkillsMapsResults(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	fake := &fakeSkillRegistry{
		results: []skills.SearchResult{
			{
				Slug:         "web-search",
				DisplayName:  "Web Search",
				Summary:      "Search the web.",
				Version:      "1.4.0",
				Score:        0.92,
				RegistryName: "clawhub",
				OwnerHandle:  "octofleet",
			},
		},
	}
	api.skillRegistry = fake

	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=web&limit=5", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var out []gen.SkillSearchResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)

	got := out[0]
	assert.Equal(t, "web-search", got.Slug)
	require.NotNil(t, got.DisplayName)
	assert.Equal(t, "Web Search", *got.DisplayName)
	require.NotNil(t, got.Summary)
	assert.Equal(t, "Search the web.", *got.Summary)
	require.NotNil(t, got.Version)
	assert.Equal(t, "1.4.0", *got.Version)
	require.NotNil(t, got.Score)
	assert.InDelta(t, 0.92, *got.Score, 0.0001)
	require.NotNil(t, got.RegistryName)
	assert.Equal(t, "clawhub", *got.RegistryName)
	require.NotNil(t, got.OwnerHandle)
	assert.Equal(t, "octofleet", *got.OwnerHandle)

	// The handler forwarded the query and clamped limit correctly.
	assert.Equal(t, "web", fake.lastQuery)
	assert.Equal(t, 5, fake.lastLimit)
}

// TestSearchSkillsLimitClampedAndDefaulted verifies limit defaults to 20 and is
// capped at 50.
func TestSearchSkillsLimitClampedAndDefaulted(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	fake := &fakeSkillRegistry{}
	api.skillRegistry = fake

	// No limit → default 20.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=x", nil)
	api.HandleSkills(httptest.NewRecorder(), r)
	assert.Equal(t, 20, fake.lastLimit)

	// Over-cap → clamped to 50.
	r = httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=x&limit=999", nil)
	api.HandleSkills(httptest.NewRecorder(), r)
	assert.Equal(t, 50, fake.lastLimit)
}

// TestSearchSkillsEmptyQuery400 verifies an empty/missing q is a 400.
func TestSearchSkillsEmptyQuery400(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{}

	for _, url := range []string{"/api/v1/skills/search", "/api/v1/skills/search?q=", "/api/v1/skills/search?q=%20%20"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		api.HandleSkills(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code, "url %q must be 400; body: %s", url, w.Body.String())
	}
}

// TestSearchSkillsBadLimit400 verifies a non-numeric or non-positive limit is 400.
func TestSearchSkillsBadLimit400(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{}

	for _, url := range []string{"/api/v1/skills/search?q=x&limit=abc", "/api/v1/skills/search?q=x&limit=0", "/api/v1/skills/search?q=x&limit=-3"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		api.HandleSkills(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code, "url %q must be 400; body: %s", url, w.Body.String())
	}
}

// TestSearchSkillsRegistryError502 verifies a registry failure is surfaced as a
// 502 (not a 500), with a clear message.
func TestSearchSkillsRegistryError502(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{searchErr: assertError("registry down")}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=web", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusBadGateway, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "registry unavailable")
}

// TestSearchSkillsNoRegistry502 verifies a nil registry yields 502, not a panic
// or 500.
func TestSearchSkillsNoRegistry502(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = nil

	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=web", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusBadGateway, w.Code, "body: %s", w.Body.String())
}

// TestInstallSkillRejectsBadSlug verifies install rejects a path-traversal slug
// with 400 before touching the registry.
func TestInstallSkillRejectsBadSlug(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{}

	body := `{"slug":"../etc/passwd"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestInstallSkillMissingSlug400 verifies an empty slug is a 400.
func TestInstallSkillMissingSlug400(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{}

	body := `{"slug":""}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestInstallSkillSuccess verifies a valid slug installs via the stubbed
// registry and returns the installed skill.
func TestInstallSkillSuccess(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{
		installRes: &skills.InstallResult{Version: "2.0.0", Summary: "Does a thing.", Verified: true},
	}

	body := `{"slug":"cool-skill"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var skill gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &skill))
	assert.Equal(t, "cool-skill", skill.Id)
	assert.Equal(t, "2.0.0", skill.Version)
	assert.True(t, skill.Verified)
}

func TestInstallSkillReturnsSavedStateWhenBackupCleanupIsIncomplete(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{installRes: &skills.InstallResult{Version: "2.0.0"}}
	original := publishRESTSkill
	publishRESTSkill = func(string, string, string, string) (skills.PublishOutcome, error) {
		return skills.PublishOutcome{Revision: strings.Repeat("a", 64), PersistenceStatus: "complete", ActivationStatus: "active", ChangedFields: []string{"installed"}, Warning: "previous package cleanup is incomplete"}, nil
	}
	t.Cleanup(func() { publishRESTSkill = original })

	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", strings.NewReader(`{"slug":"cool-skill"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var skill gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &skill))
	require.Equal(t, strings.Repeat("a", 64), skill.Revision)
	require.NotNil(t, skill.PersistenceStatus)
	require.Equal(t, gen.SkillPersistenceStatusComplete, *skill.PersistenceStatus)
	require.NotNil(t, skill.ActivationStatus)
	require.Equal(t, gen.SkillActivationStatusActive, *skill.ActivationStatus)
	require.NotNil(t, skill.Message)
	require.Contains(t, *skill.Message, "cleanup is incomplete")
}

func TestInstallSkillReturnsPartialStateWhenPreviousPackageCannotBeRestored(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{installRes: &skills.InstallResult{Version: "2.0.0"}}
	original := publishRESTSkill
	publishRESTSkill = func(string, string, string, string) (skills.PublishOutcome, error) {
		return skills.PublishOutcome{
			PersistenceStatus: skills.PersistencePartial,
			ActivationStatus:  skills.ActivationNotAttempted,
			ChangedFields:     []string{"installed"},
			ErrorStage:        skills.PublicationErrorStageRestorePrevious,
			Message:           "replacement publication failed and the previous package could not be restored; no live package is available",
		}, errors.New("private publish cause; private restore cause")
	}
	t.Cleanup(func() { publishRESTSkill = original })

	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", strings.NewReader(`{"slug":"cool-skill"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	var payload map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, map[string]any{
		"activation_status":  "not_attempted",
		"changed_fields":     []any{"installed"},
		"error_stage":        "restore_previous",
		"message":            "replacement publication failed and the previous package could not be restored; no live package is available",
		"persistence_status": "partial",
	}, payload)
	require.NotContains(t, w.Body.String(), "private publish cause")
	require.NotContains(t, w.Body.String(), "private restore cause")
}

func TestInstallSkillFromAuthorizedMarkdownUpload(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	uploadDir := filepath.Join(api.homePath, "uploads", "owner-session")
	require.NoError(t, os.MkdirAll(uploadDir, 0o755))
	uploadPath := filepath.Join(uploadDir, "local-skill.md")
	content := "---\nname: local-skill\ndescription: Use when a local uploaded skill is requested.\n---\n\nBody.\n"
	require.NoError(t, os.WriteFile(uploadPath, []byte(content), 0o644))
	store := media.NewFileMediaStore()
	api.mediaStore = store
	ref, err := store.Store(uploadPath, media.MediaMeta{Filename: "local-skill.md", Source: "upload:webchat", CleanupPolicy: media.CleanupPolicyForgetOnly}, "upload:owner-session")
	require.NoError(t, err)

	body, err := json.Marshal(map[string]any{"upload_id": ref})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var skill gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &skill))
	require.Equal(t, "local-skill", skill.Id)
	require.NotEmpty(t, skill.Revision)
	got, err := os.ReadFile(filepath.Join(api.homePath, "skills", "local-skill", "SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, content, string(got))
}

func TestInstallSkillRejectsPathInsteadOfOpaqueUploadRef(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	body, err := json.Marshal(map[string]any{"upload_id": "uploads/session/skill.md"})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestListSkillsBuiltinEnriched verifies a seeded built-in skill is returned by
// GET /api/v1/skills with a non-empty description, source=builtin, verified=true,
// author=Omnipus, and the frontmatter version.
func TestListSkillsBuiltinEnriched(t *testing.T) {
	builtinDir := t.TempDir()
	seedSkill(t, builtinDir, "daily-briefing",
		"name: daily-briefing\ndescription: Summarize the day for the operator.\nversion: 1.2.3",
		"# daily-briefing\n\nProduce a concise daily briefing.")

	api := newTestRestAPIWithSkillsDirs(t, builtinDir)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var skills []gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &skills))
	require.Len(t, skills, 1)

	s := skills[0]
	assert.Equal(t, "daily-briefing", s.Id)
	assert.Equal(t, "daily-briefing", s.Name)
	assert.Equal(t, gen.SkillStatusActive, s.Status)
	assert.True(t, s.Verified, "builtin skills are Omnipus-team-verified")

	require.NotNil(t, s.Source)
	assert.Equal(t, gen.SkillSourceBuiltin, *s.Source)

	require.NotNil(t, s.Description)
	assert.Equal(t, "Summarize the day for the operator.", *s.Description)

	require.NotNil(t, s.Author)
	assert.Equal(t, "Omnipus", *s.Author)

	assert.Equal(t, "1.2.3", s.Version)
}

func TestListSkillsRevisionReadFailureReturnsVisibleErrorWithoutPlaceholder(t *testing.T) {
	builtinDir := t.TempDir()
	seedSkill(t, builtinDir, "daily-briefing",
		"name: daily-briefing\ndescription: Summarize the day for the operator.",
		"# daily-briefing\n")
	api := newTestRestAPIWithSkillsDirs(t, builtinDir)
	original := readListedSkillRevision
	readListedSkillRevision = func(string, string) (string, error) { return "", errors.New("private disk detail") }
	t.Cleanup(func() { readListedSkillRevision = original })

	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "could not read installed skill state")
	assert.NotContains(t, w.Body.String(), "unavailable")
	assert.NotContains(t, w.Body.String(), "private disk detail")
}

// TestListSkillsVersionDefaultsWhenAbsent verifies a builtin skill without a
// version frontmatter key falls back to "0.0.0".
func TestListSkillsVersionDefaultsWhenAbsent(t *testing.T) {
	builtinDir := t.TempDir()
	seedSkill(t, builtinDir, "plan",
		"name: plan\ndescription: Plan a task before executing.",
		"# plan\n\nPlan before acting.")

	api := newTestRestAPIWithSkillsDirs(t, builtinDir)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var skills []gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &skills))
	require.Len(t, skills, 1)
	assert.Equal(t, "0.0.0", skills[0].Version)
	require.NotNil(t, skills[0].Source)
	assert.Equal(t, gen.SkillSourceBuiltin, *skills[0].Source)
}

// TestListSkillsDisplayNameSeparateFromID verifies a builtin skill whose
// frontmatter carries a proper English display name surfaces with Id=slug and
// Name=display, is still detected as a system skill (keyed on the slug), and
// DELETE is rejected with 403. This is the Part B id/name-separation contract.
func TestListSkillsDisplayNameSeparateFromID(t *testing.T) {
	builtinDir := t.TempDir()
	// Directory slug is "daily-briefing"; frontmatter name is the display name.
	seedSkill(t, builtinDir, "daily-briefing",
		"name: Daily Briefing\ndescription: Summarize the day for the operator.\nversion: 1.2.3",
		"# Daily Briefing\n\nProduce a concise daily briefing.")

	api := newTestRestAPIWithSkillsDirs(t, builtinDir)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var listed []gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	require.Len(t, listed, 1)

	s := listed[0]
	assert.Equal(t, "daily-briefing", s.Id, "Id must be the slug")
	assert.Equal(t, "Daily Briefing", s.Name, "Name must be the display name")
	assert.True(t, s.Verified, "embedded default must be verified")
	require.NotNil(t, s.Source)
	assert.Equal(t, gen.SkillSourceBuiltin, *s.Source, "must surface as builtin")

	// System detection + delete guard are keyed on the slug (Id), not the
	// display name — DefaultSkillNames returns slugs.
	assert.Equal(t, "builtin", api.skillSource("daily-briefing"))

	// DELETE by slug must be rejected with 403.
	rd := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/daily-briefing", nil)
	wd := httptest.NewRecorder()
	api.HandleSkills(wd, rd)
	assert.Equal(t, http.StatusForbidden, wd.Code, "body: %s", wd.Body.String())
	assert.Contains(t, wd.Body.String(), "built-in skills cannot be removed")
}

// TestHandleSkills_IncludesArgumentHint verifies that GET /api/v1/skills
// surfaces the SKILL.md frontmatter argument-hint as argument_hint on the
// wire Skill type (F3/FR-006/FR-014/R3).
//
// Traces to: FR-006, FR-014, R3, SC-008.
func TestHandleSkills_IncludesArgumentHint(t *testing.T) {
	builtinDir := t.TempDir()

	// Skill WITH an argument-hint declaration.
	// Note: "[topic]" must be quoted in YAML because bare [topic] parses as a
	// YAML sequence, not a string. The SKILL.md convention is to quote the hint.
	seedSkill(t, builtinDir, "web-research",
		`name: web-research`+"\n"+`description: Search the web.`+"\n"+`argument-hint: "[topic]"`,
		"# web-research\n\nSearch the web for a given topic.")

	// Skill WITHOUT an argument-hint declaration.
	seedSkill(t, builtinDir, "summarize",
		"name: summarize\ndescription: Summarize text.",
		"# summarize\n\nSummarize arbitrary text.")

	api := newTestRestAPIWithSkillsDirs(t, builtinDir)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var listed []gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	require.Len(t, listed, 2)

	// Index by id for order-independent assertions.
	byID := make(map[string]gen.Skill, len(listed))
	for _, s := range listed {
		byID[s.Id] = s
	}

	// web-research: argument_hint must be "[topic]".
	wr, ok := byID["web-research"]
	require.True(t, ok, "web-research must be in the listing")
	require.NotNil(t, wr.ArgumentHint, "web-research must carry argument_hint")
	assert.Equal(t, "[topic]", *wr.ArgumentHint) // YAML-quoted in frontmatter → string "[topic]"

	// summarize: argument_hint must be absent (nil pointer).
	sum, ok := byID["summarize"]
	require.True(t, ok, "summarize must be in the listing")
	assert.Nil(t, sum.ArgumentHint, "summarize must not carry argument_hint when not declared")
}
