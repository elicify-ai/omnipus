package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/skills"
)

// fakeSkillRegistry is a minimal skills.SkillRegistry test double that
// installs a real SKILL.md into targetDir with no network access, so
// TestInstallSkillTool_DiscoverableByDifferentAgent can drive install_skill's
// actual Execute path end-to-end without depending on a live ClawHub/GitHub
// registry.
type fakeSkillRegistry struct{}

func (fakeSkillRegistry) Name() string { return "fake" }

func (fakeSkillRegistry) Search(_ context.Context, _ string, _ int) ([]skills.SearchResult, error) {
	return nil, nil
}

func (fakeSkillRegistry) GetSkillMeta(_ context.Context, slug string) (*skills.SkillMeta, error) {
	return &skills.SkillMeta{Slug: slug, DisplayName: slug, LatestVersion: "1.0.0"}, nil
}

func (fakeSkillRegistry) DownloadAndInstall(
	_ context.Context, slug, _ string, targetDir string,
) (*skills.InstallResult, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, err
	}
	skillMD := "---\nname: " + slug + "\ndescription: a fake test skill for install_skill's global-dir test\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		return nil, err
	}
	return &skills.InstallResult{Version: "1.0.0"}, nil
}

func TestInstallSkillToolName(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	assert.Equal(t, "install_skill", tool.Name())
}

func TestInstallSkillToolMissingSlug(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	result := tool.Execute(context.Background(), map[string]any{})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "identifier is required and must be a non-empty string")
}

func TestInstallSkillToolEmptySlug(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	result := tool.Execute(context.Background(), map[string]any{
		"slug": "   ",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "identifier is required and must be a non-empty string")
}

func TestInstallSkillToolUnsafeSlug(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())

	cases := []string{
		"../etc/passwd",
		"path/traversal",
		"path\\traversal",
	}

	for _, slug := range cases {
		result := tool.Execute(context.Background(), map[string]any{
			"slug": slug,
		})
		assert.True(t, result.IsError, "slug %q should be rejected", slug)
		assert.Contains(t, result.ForLLM, "invalid slug")
	}
}

func TestInstallSkillToolAlreadyExists(t *testing.T) {
	globalSkillsDir := t.TempDir()
	skillDir := filepath.Join(globalSkillsDir, "existing-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	tool := NewInstallSkillTool(skills.NewRegistryManager(), globalSkillsDir)
	result := tool.Execute(context.Background(), map[string]any{
		"slug":     "existing-skill",
		"registry": "clawhub",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "already installed")
}

func TestInstallSkillToolRegistryNotFound(t *testing.T) {
	globalSkillsDir := t.TempDir()
	tool := NewInstallSkillTool(skills.NewRegistryManager(), globalSkillsDir)
	result := tool.Execute(context.Background(), map[string]any{
		"slug":     "some-skill",
		"registry": "nonexistent",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "registry")
	assert.Contains(t, result.ForLLM, "not found")
}

func TestInstallSkillToolParameters(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	params := tool.Parameters()

	props, ok := params["properties"].(map[string]any)
	assert.True(t, ok)
	assert.Contains(t, props, "slug")
	assert.Contains(t, props, "version")
	assert.Contains(t, props, "registry")
	assert.Contains(t, props, "force")

	required, ok := params["required"].([]string)
	assert.True(t, ok)
	assert.Contains(t, required, "slug")
	assert.Contains(t, required, "registry")
}

func TestInstallSkillToolMissingRegistry(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	result := tool.Execute(context.Background(), map[string]any{
		"slug": "some-skill",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "invalid registry")
}

// TestInstallSkillTool_DiscoverableByDifferentAgent proves the ADR-046
// FR-009 defect fix: install_skill must target the fixed GLOBAL skills
// directory ($OMNIPUS_HOME/skills) regardless of which agent's workspace
// installed it, so a skill installed via one agent is discoverable by a
// DIFFERENT agent's own SkillsLoader — via the global bucket
// skills.SkillRoots()/ListSkills() already searches (pkg/skills/loader.go),
// which requires no change on the discovery side.
func TestInstallSkillTool_DiscoverableByDifferentAgent(t *testing.T) {
	globalSkills := t.TempDir()

	registryMgr := skills.NewRegistryManager()
	registryMgr.AddRegistry(fakeSkillRegistry{})

	tool := NewInstallSkillTool(registryMgr, globalSkills)
	result := tool.Execute(context.Background(), map[string]any{
		"slug":     "shared-fake-skill",
		"registry": "fake",
	})
	require.False(t, result.IsError, "install must succeed, got: %s", result.ForLLM)

	// Confirm the skill actually landed under the GLOBAL directory, not some
	// agent-specific workspace path.
	_, statErr := os.Stat(filepath.Join(globalSkills, "shared-fake-skill", "SKILL.md"))
	require.NoError(t, statErr, "installed skill must be on disk under the global skills dir")

	// Two DIFFERENT agents — distinct workspace and builtin roots each — but
	// the SAME global skills dir, exactly as every agent's own
	// NewContextBuilder wires it (pkg/agent/context.go's globalSkillsDir()).
	loaderA := skills.NewSkillsLoader(t.TempDir(), globalSkills, t.TempDir())
	loaderB := skills.NewSkillsLoader(t.TempDir(), globalSkills, t.TempDir())

	for name, loader := range map[string]*skills.SkillsLoader{"agent-a": loaderA, "agent-b": loaderB} {
		found := false
		for _, info := range loader.ListSkills() {
			if info.ID == "shared-fake-skill" {
				found = true
				break
			}
		}
		assert.True(t, found, "%s's SkillsLoader must discover the globally-installed skill", name)
	}
}
