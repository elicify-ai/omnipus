// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 17.
// TestInstallSkillCategoryParity: install_skill takes category — a valid
// category is stored with the installed skill (S-71), an unknown one is
// refused naming the allowed categories with nothing installed (S-72).
//
// Oracle: skills-tools-redesign-spec.md S-71 ("install_skill installs
// weather with category: lifestyle … weather shows in the LIFESTYLE
// group"), S-72 ("called with category: games … refused naming the allowed
// categories and nothing is installed"), C-5 (Skill.category) and the
// generated SkillCategory enum (the 13 R32 values). The file-level carrier
// of a skill's category is the SKILL.md frontmatter metadata.category
// (spec §810: "a skill file with metadata.category: research"), so the
// stored-or-refused contract is pinned on the installed SKILL.md. The group
// surface itself (list_skills grouping) is the menu work of orders 11/29.
//
// install_skill ignores unknown arguments at this commit
// (pkg/tools/skills_install.go), so a category is silently dropped — the
// RED reason for S-71; and nothing refuses an unknown category — the RED
// reason for S-72.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"

	"github.com/stretchr/testify/require"
)

// TestInstallSkillCategoryParity (order 17).
// Traces to: BDD S-71, S-72; spec C-5; Dataset D5 rows 5-6.
func TestInstallSkillCategoryParity(t *testing.T) {
	t.Run("S-71 a valid category is stored with the installed skill", func(t *testing.T) {
		globalSkills := filepath.Join(t.TempDir(), "skills")
		require.NoError(t, os.MkdirAll(globalSkills, 0o755))
		registryMgr := skills.NewRegistryManager()
		registryMgr.AddRegistry(fakeSkillRegistry{}) // installs the slug's SKILL.md
		tool := NewInstallSkillTool(registryMgr, globalSkills)
		res := tool.Execute(context.Background(), map[string]any{
			"slug":     "weather",
			"registry": "fake",
			"category": "lifestyle",
		})
		require.False(t, res.IsError, "setup install failed: %s", res.ForLLM)
		raw, err := os.ReadFile(filepath.Join(globalSkills, "weather", "SKILL.md"))
		require.NoError(t, err, "the installed skill must exist")
		require.Contains(t, string(raw), "category: lifestyle",
			"S-71: the operator's category is stored with the skill; SKILL.md: %s", string(raw))
	})

	t.Run("S-72 an unknown category is refused naming the allowed ones", func(t *testing.T) {
		globalSkills := filepath.Join(t.TempDir(), "skills")
		require.NoError(t, os.MkdirAll(globalSkills, 0o755))
		registryMgr := skills.NewRegistryManager()
		registryMgr.AddRegistry(fakeSkillRegistry{})
		tool := NewInstallSkillTool(registryMgr, globalSkills)
		res := tool.Execute(context.Background(), map[string]any{
			"slug":     "weather",
			"registry": "fake",
			"category": "games",
		})
		require.True(t, res.IsError,
			"S-72: an unknown category must be refused; got success: %s", res.ForLLM)
		for _, allowed := range []string{"tools", "research", "lifestyle", "uncategorised"} {
			require.Contains(t, strings.ToLower(res.ForLLM), allowed,
				"the refusal names the allowed categories (S-72): %s", res.ForLLM)
		}
		_, statErr := os.Stat(filepath.Join(globalSkills, "weather"))
		require.True(t, os.IsNotExist(statErr),
			"S-72: nothing is installed after the refusal")
	})
}
