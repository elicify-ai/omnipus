// Tests for GET /api/v1/skills metadata enrichment (description/author/version/
// source) and the built-in deletion guard. These verify the data path from the
// REST handler through AgentLoop.ListSkillsDetailed() to the skills loader, and
// that the DELETE handler rejects built-in skills with 403.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSystemSkillsDetectedByName verifies the embedded default skills are
// treated as built-in (system) by NAME, regardless of directory. They are
// seeded into the GLOBAL skills dir on first boot (loader source "global"), so
// name-based detection is what surfaces them as built-in and protects them from
// deletion — the directory check alone would mis-classify them as Community.
func TestSystemSkillsDetectedByName(t *testing.T) {
	defaults := skills.DefaultSkillNames()
	require.NotEmpty(t, defaults, "expected embedded default skills")
	for _, n := range defaults {
		assert.True(t, isSystemSkill(n), "embedded default %q must be a system skill", n)
	}
	assert.False(t, isSystemSkill("totally-not-a-default-skill"), "non-default must not be system")

	// skillSource short-circuits to "builtin" for a default-named skill even
	// when the loader would report it as global — this is what makes the DELETE
	// guard (skillSource(name)=="builtin" → 403) protect seeded system skills.
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	assert.Equal(t, "builtin", api.skillSource(defaults[0]),
		"a default-named skill must resolve to builtin source (delete-protected)")
}

// seedSkill writes a minimal valid SKILL.md (frontmatter + body) under
// dir/<name>/SKILL.md so the loader recognizes it as an installed skill.
func seedSkill(t *testing.T, dir, name, frontmatter, body string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	content := "---\n" + frontmatter + "\n---\n\n" + body + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))
}

// newTestRestAPIWithSkillsDirs builds a restAPI whose default agent's skills
// loader reads builtin skills from builtinDir and global skills from a temp
// global dir. The env vars are set BEFORE the agent loop is constructed so the
// ContextBuilder picks them up.
func newTestRestAPIWithSkillsDirs(t *testing.T, builtinDir string) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv(config.EnvBuiltinSkills, builtinDir)

	tmpDir := t.TempDir()
	// Isolate OMNIPUS_HOME so the global skills dir resolves to an (empty)
	// <tmpDir>/skills instead of the developer's real ~/.omnipus/skills. Without
	// this, getGlobalConfigDir() falls back to ~/.omnipus and any skills seeded
	// there in a prior session leak into the GET /skills listing, inflating the
	// expected count (hermeticity bug, not behavior). EnvBuiltinSkills only
	// overrides the *builtin* root; the *global* root is keyed off OMNIPUS_HOME.
	t.Setenv("OMNIPUS_HOME", tmpDir)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			// An explicitly registered agent. Skill listing and agent-create
			// validation both resolve through GetDefaultAgent, which returns nil
			// for an empty roster now that the "main" sentinel is gone
			// (ADR-064) — ListSkillsDetailed then returns no skills at all.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	// Enable the ClawHub marketplace so search/install handlers exercise the
	// registry path rather than the "no marketplace enabled" 409 gate.
	cfg.Tools.Skills.Marketplaces = []config.MarketplaceConfig{
		{Name: "clawhub", Type: "clawhub", Enabled: true, BaseURL: "https://clawhub.ai"},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
		taskLock:      task.TaskFileLock,
	}
}

// TestDeleteBuiltinSkillRejected verifies DELETE of a builtin skill returns 403.
func TestDeleteBuiltinSkillRejected(t *testing.T) {
	builtinDir := t.TempDir()
	seedSkill(t, builtinDir, "summarize",
		"name: summarize\ndescription: Summarize arbitrary text.",
		"# summarize\n\nSummarize text.")

	api := newTestRestAPIWithSkillsDirs(t, builtinDir)

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/summarize", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "built-in skills cannot be removed")
}

// TestDeleteNonBuiltinSkillNotRejectedByGuard verifies the builtin guard does
// NOT fire for a skill that is not present as a builtin. The skill is installed
// in the global skills dir and removed by the installer; the response is 200.
func TestDeleteNonBuiltinSkillNotRejectedByGuard(t *testing.T) {
	builtinDir := t.TempDir() // empty: no builtins

	api := newTestRestAPIWithSkillsDirs(t, builtinDir)

	// Install a user skill under <home>/skills/<name>/ — the installer's Uninstall
	// target (a.homePath + "/skills"). It is NOT a builtin, so the guard must not
	// fire and the installer should remove it (200).
	userSkillDir := filepath.Join(api.homePath, "skills", "my-tool")
	require.NoError(t, os.MkdirAll(userSkillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(userSkillDir, "SKILL.md"),
		[]byte("---\nname: my-tool\ndescription: A user-installed tool.\n---\n\n# my-tool\n\nDo a thing.\n"), 0o644))

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/my-tool", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	// The guard must not produce a 403. The installer governs the final outcome.
	assert.NotEqual(
		t,
		http.StatusForbidden,
		w.Code,
		"non-builtin must not hit the 403 guard; body: %s",
		w.Body.String(),
	)
}
