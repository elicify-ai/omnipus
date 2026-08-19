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

// seedInstalledSkills creates dir with three installed skills in it and returns
// their names, so a destructive call can be measured against real content
// rather than an empty directory.
func seedInstalledSkills(t *testing.T, dir string) []string {
	t.Helper()
	names := []string{"pdf", "docker-compose", "github"}
	for _, name := range names {
		skillDir := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(skillDir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(skillDir, "SKILL.md"),
			[]byte("---\nname: "+name+"\ndescription: a seeded skill\n---\n\nBody.\n"),
			0o644,
		))
	}
	return names
}

// TestInstallSkillTool_DestructiveSlugsRefused is the end-to-end proof for the
// install_skill wipe.
//
// force=true made install_skill os.RemoveAll(filepath.Join(skillsDir, slug))
// BEFORE resolving the registry, guarded only by a denylist that rejected "/",
// "\\" and ".." — and not ".". filepath.Join(skillsDir, ".") is skillsDir, so
// one call with slug="." deleted every installed skill on the box and then
// returned an error about the registry, making the destruction look like a
// failed install.
//
// The assertion is on the DIRECTORY, not on the error: a refusal that still
// deleted the skills would be no fix at all.
func TestInstallSkillTool_DestructiveSlugsRefused(t *testing.T) {
	for _, slug := range []string{".", "..", "./", "../", " .", ". ", "...", ".hidden"} {
		t.Run(slug, func(t *testing.T) {
			globalSkills := t.TempDir()
			seeded := seedInstalledSkills(t, globalSkills)

			registryMgr := skills.NewRegistryManager()
			registryMgr.AddRegistry(fakeSkillRegistry{})

			result := NewInstallSkillTool(registryMgr, globalSkills).Execute(
				context.Background(),
				map[string]any{"slug": slug, "registry": "fake", "force": true},
			)

			assert.True(t, result.IsError, "install_skill(slug=%q) must be refused", slug)
			assert.Contains(t, result.ForLLM, "invalid slug")

			entries, err := os.ReadDir(globalSkills)
			require.NoError(t, err, "the skills directory itself must still exist")
			require.Len(t, entries, len(seeded),
				"install_skill(slug=%q) destroyed installed skills: %v", slug, entries)
			for _, name := range seeded {
				_, statErr := os.Stat(filepath.Join(globalSkills, name, "SKILL.md"))
				assert.NoError(t, statErr, "skill %q must survive install_skill(slug=%q)", name, slug)
			}
		})
	}
}

// TestInstallSkillTool_DestructiveRegistryNamesRefused covers the second
// identifier install_skill validates. It is not used as a path today, but it
// flows through the same validator, and a registry name is operator-controlled
// data that reaches a registry lookup.
func TestInstallSkillTool_DestructiveRegistryNamesRefused(t *testing.T) {
	for _, registry := range []string{".", "..", "../clawhub", "claw hub"} {
		globalSkills := t.TempDir()
		seeded := seedInstalledSkills(t, globalSkills)

		result := NewInstallSkillTool(skills.NewRegistryManager(), globalSkills).Execute(
			context.Background(),
			map[string]any{"slug": "some-skill", "registry": registry, "force": true},
		)
		assert.True(t, result.IsError, "install_skill(registry=%q) must be refused", registry)
		assert.Contains(t, result.ForLLM, "invalid registry")

		entries, err := os.ReadDir(globalSkills)
		require.NoError(t, err)
		require.Len(t, entries, len(seeded),
			"install_skill(registry=%q) destroyed installed skills", registry)
	}
}

// TestInstallSkillTool_ForceReinstallStillWorks is the positive control for
// both tests above. The refusals mean nothing if the tool no longer installs:
// this drives a real install into a seeded directory, then a force reinstall
// over the top of it, and asserts the OTHER skills survive — i.e. the fix
// narrowed what os.RemoveAll can reach, it did not disable the code path.
func TestInstallSkillTool_ForceReinstallStillWorks(t *testing.T) {
	globalSkills := t.TempDir()
	seeded := seedInstalledSkills(t, globalSkills)

	registryMgr := skills.NewRegistryManager()
	registryMgr.AddRegistry(fakeSkillRegistry{})
	tool := NewInstallSkillTool(registryMgr, globalSkills)

	// A fresh install of a well-formed slug.
	result := tool.Execute(context.Background(), map[string]any{
		"slug": "new-skill", "registry": "fake",
	})
	require.False(t, result.IsError, "install must succeed, got: %s", result.ForLLM)
	_, err := os.Stat(filepath.Join(globalSkills, "new-skill", "SKILL.md"))
	require.NoError(t, err, "the installed skill must be on disk")

	// A force reinstall over an existing skill — the branch that holds the
	// os.RemoveAll — must still replace exactly that one directory.
	result = tool.Execute(context.Background(), map[string]any{
		"slug": "pdf", "registry": "fake", "force": true,
	})
	require.False(t, result.IsError, "force reinstall must succeed, got: %s", result.ForLLM)
	_, err = os.Stat(filepath.Join(globalSkills, "pdf", "SKILL.md"))
	require.NoError(t, err, "the reinstalled skill must be on disk")

	for _, name := range seeded {
		_, statErr := os.Stat(filepath.Join(globalSkills, name, "SKILL.md"))
		assert.NoError(t, statErr, "force reinstall must not touch the other skills (%q)", name)
	}
}
