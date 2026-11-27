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
// categories and nothing is installed"), C-5, and the storage rule:
//   "The category and the install-time verification result are stored in
//    Omnipus's own data, OUTSIDE the skill folder (R32: never written into
//    the skill file)."
// so the installed SKILL.md must stay byte-identical to the registry's
// original (the spec's own trip-notes oracle: "contains no category field
// added by Omnipus"), and the category is read through Omnipus's metadata
// store, never by grepping the skill file. The group surface itself
// (list_skills grouping) is the menu work of orders 11/29.
//
// CHECK round 1 finding 5: the earlier S-71 half required
// "category: lifestyle" INSIDE SKILL.md — a write the spec explicitly
// forbids — which contradicted C-5/C-21 and
// skill_category_once_red_test.go::TestWriteSkillWorkflow_PublishesCategorisedSkill.
//
// RED today: install_skill ignores unknown arguments at this commit
// (pkg/tools/skills_install.go), so no category store exists — the metadata
// seam below is nil and S-71 fails loud (BLOCKED); nothing refuses an
// unknown category — the RED reason for S-72.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"

	"github.com/stretchr/testify/require"
)

// readInstalledSkillCategory is the test-local seam for Omnipus's own
// category metadata store (C-5: outside the skill folder). It returns the
// stored category of the named installed skill, or an error when the store
// cannot be read.
//
// Part L: the seam is typed exactly as the production reader
// pkg/skills/install_records.go::SkillCategoryOf (skillsDir, slug) (string,
// error), the function the skill listing reads, so the binding is a direct
// named production function. The earlier form took a testing.TB, which no
// production function can have (Go assigns only identical function types), so
// it could never be bound. skill_install_category_bindings_test.go binds it;
// nil keeps a tree without that function loud (BLOCKED), never a skip.
var readInstalledSkillCategory func(skillsDir, slug string) (category string, err error)

// fakeRegistrySkillMD is the exact SKILL.md fakeSkillRegistry writes — the
// original bytes the category write must leave untouched.
const fakeRegistrySkillMD = "---\nname: weather\ndescription: a fake test skill for install_skill's global-dir test\n---\n\nBody.\n"

// TestInstallSkillCategoryParity (order 17).
// Traces to: BDD S-71, S-72; spec C-5; Dataset D5 rows 5-6.
func TestInstallSkillCategoryParity(t *testing.T) {
	t.Run("S-71 a valid category is stored with the installed skill, outside the skill file", func(t *testing.T) {
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
		require.Equal(t, fakeRegistrySkillMD, string(raw),
			"C-5/R32: the category is never written into the skill file — the installed "+
				"SKILL.md stays byte-identical to the registry's original")

		nativebinding.Require(t, "pkg/skills", readInstalledSkillCategory,
			"C-5/S-71: actual persisted metadata reader (pkg/skills SkillCategoryOf); post-GREEN inventory/store re-audit required")
		category, err := readInstalledSkillCategory(globalSkills, "weather")
		require.NoError(t, err, "S-71: the stored category of the installed skill is readable")
		require.Equal(t, "lifestyle", category,
			"S-71: the operator's category is stored with the installed skill — "+
				"neither absent nor the uncategorised default (C-5)")
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
