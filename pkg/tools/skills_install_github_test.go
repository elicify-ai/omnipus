package tools

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// #1574: install_skill could never install from a github registry. The github
// registry wants an owner/repo reference, install_skill's slug check refuses
// "/", so every call ended in a confusing "invalid slug" message. Decision
// (chief, 2026-11-12): the tool does not support github; it says so clearly,
// BEFORE slug validation, and never touches the skills directory.
const githubNotSupportedMessage = "not supported by install_skill"

// wrappedGitHubRegistry embeds a github registry, as a decorating wrapper
// (caching, metrics) would. Its dynamic type is not *skills.GitHubRegistry.
type wrappedGitHubRegistry struct{ *skills.GitHubRegistry }

// namedNilWrappedGitHub has a nil embedded registry and its own nil-safe name.
type namedNilWrappedGitHub struct{ *skills.GitHubRegistry }

func (*namedNilWrappedGitHub) Name() string { return "my-gh" }

// newGitHubRegistryNamed builds a github registry the way production does:
// from a configured marketplace through the config bridge, which injects the
// install directory. (Not from skills.GitHubRegistryConfig directly: that type's
// install-directory field is named like a retired agent-config identifier and
// pkg/config TestNoAgentConfigWorkspaceIdentifier flags new uses of it.)
func newGitHubRegistryNamed(t *testing.T, name string) *skills.GitHubRegistry {
	t.Helper()
	cfg := &config.Config{}
	cfg.Tools.Skills.Marketplaces = []config.MarketplaceConfig{
		{Name: name, Type: config.MarketplaceTypeGitHub, Enabled: true},
	}
	entries := skills.MarketplacesFromConfig(cfg, func(string) string { return "" }, nil, t.TempDir())
	rm := skills.NewRegistryManagerFromConfig(skills.RegistryConfig{Marketplaces: entries})
	reg, ok := rm.GetRegistry(name).(*skills.GitHubRegistry)
	require.True(t, ok, "the config factory must build a *skills.GitHubRegistry for type github")
	return reg
}

func TestInstallSkillTool_GitHubRegistryIsRefusedWithAClearMessage(t *testing.T) {
	cases := []struct {
		name     string
		registry string
		slug     string
		manager  func(t *testing.T) *skills.RegistryManager
	}{
		{"named github, owner/repo slug, registry not configured", "github", "owner/repo",
			func(t *testing.T) *skills.RegistryManager { return skills.NewRegistryManager() }},
		{"named github, plain slug", "github", "some-skill",
			func(t *testing.T) *skills.RegistryManager { return skills.NewRegistryManager() }},
		{"named GitHub in other case", "GitHub", "owner/repo",
			func(t *testing.T) *skills.RegistryManager { return skills.NewRegistryManager() }},
		{"configured github registry under the default name", "github", "owner/repo",
			func(t *testing.T) *skills.RegistryManager {
				rm := skills.NewRegistryManager()
				rm.AddRegistry(newGitHubRegistryNamed(t, "github"))
				return rm
			}},
		{"configured github registry under a custom name", "my-gh", "owner/repo",
			func(t *testing.T) *skills.RegistryManager {
				rm := skills.NewRegistryManager()
				rm.AddRegistry(newGitHubRegistryNamed(t, "my-gh"))
				return rm
			}},
		{"wrapped github registry, owner/repo slug", "my-gh", "owner/repo",
			func(t *testing.T) *skills.RegistryManager {
				rm := skills.NewRegistryManager()
				rm.AddRegistry(&wrappedGitHubRegistry{newGitHubRegistryNamed(t, "my-gh")})
				return rm
			}},
		{"wrapper with a nil embedded github registry, owner/repo slug", "my-gh", "owner/repo",
			func(t *testing.T) *skills.RegistryManager {
				rm := skills.NewRegistryManager()
				rm.AddRegistry(&namedNilWrappedGitHub{})
				return rm
			}},
		{"wrapper with a nil embedded github registry, plain slug", "my-gh", "some-skill",
			func(t *testing.T) *skills.RegistryManager {
				rm := skills.NewRegistryManager()
				rm.AddRegistry(&namedNilWrappedGitHub{})
				return rm
			}},
		{"wrapped github registry, plain slug", "my-gh", "some-skill",
			func(t *testing.T) *skills.RegistryManager {
				rm := skills.NewRegistryManager()
				rm.AddRegistry(&wrappedGitHubRegistry{newGitHubRegistryNamed(t, "my-gh")})
				return rm
			}},
		{"configured github registry under a custom name, plain slug", "my-gh", "some-skill",
			func(t *testing.T) *skills.RegistryManager {
				rm := skills.NewRegistryManager()
				rm.AddRegistry(newGitHubRegistryNamed(t, "my-gh"))
				return rm
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			globalSkills := t.TempDir()
			seeded := seedInstalledSkills(t, globalSkills)

			result := NewInstallSkillTool(tc.manager(t), globalSkills).Execute(
				context.Background(),
				map[string]any{"slug": tc.slug, "registry": tc.registry},
			)

			require.True(t, result.IsError, "a github install must be refused")
			assert.Contains(t, result.ForLLM, githubNotSupportedMessage)
			assert.NotContains(t, result.ForLLM, "invalid slug",
				"the refusal must name the real reason, not the slug rule")
			assert.NotContains(t, result.ForLLM, "not found",
				"a configured github registry is refused for being github, not for being missing")

			entries, err := os.ReadDir(globalSkills)
			require.NoError(t, err)
			assert.Len(t, entries, len(seeded), "nothing may be installed, removed or staged")
		})
	}
}

// The refusal is only for github. Other registries keep their existing
// behaviour: an owner/repo slug is still an invalid slug there.
func TestInstallSkillTool_NonGitHubRegistryKeepsSlugValidation(t *testing.T) {
	registryMgr := skills.NewRegistryManager()
	registryMgr.AddRegistry(fakeSkillRegistry{})

	result := NewInstallSkillTool(registryMgr, t.TempDir()).Execute(
		context.Background(),
		map[string]any{"slug": "owner/repo", "registry": "fake"},
	)

	require.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "invalid slug")
	assert.NotContains(t, result.ForLLM, githubNotSupportedMessage)
}

// A nil-manager tool still refuses a github registry by name.
func TestInstallSkillTool_NilManagerStillRefusesGitHub(t *testing.T) {
	result := NewInstallSkillTool(nil, t.TempDir()).Execute(
		context.Background(), map[string]any{"slug": "owner/repo", "registry": "github"})
	require.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, githubNotSupportedMessage)
}

// The tool text must not offer github as an install source.
func TestInstallSkillTool_DescriptionStatesGitHubIsNotSupported(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	assert.Contains(t, tool.Description(), "github")
	assert.Contains(t, tool.Description(), githubNotSupportedMessage)
}
