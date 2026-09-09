// Omnipus — Skills Menu Summary Tests (ADR-072 D1.1, D4.1, D4.2)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Replaces the former summary_cap_test.go: ADR-072 D1.1 deletes
// maxSkillsInSummary and its truncation footer outright (not resized), so
// that file's "the cap fires and keeps the right survivors" assertions now
// assert behaviour this feature deliberately removes. The regressions worth
// keeping (allow-filtering, no footer on a small set) are folded in below
// alongside the new required tests.

package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildSkillsSummary_FiltersByGrant covers ADR-072 D4's menu door
// (T2 / spec #4): a skill the acting agent is not granted must not appear in
// the menu, while a granted one does.
func TestBuildSkillsSummary_FiltersByGrant(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")
	createSkillDir(t, builtin, "granted-skill", "granted-skill", "the only granted skill here")
	createSkillDir(t, builtin, "ungranted-skill", "ungranted-skill", "installed but never granted")

	sl := NewSkillsLoader(tmp, "", builtin)
	allow := func(id string) bool { return id == "granted-skill" }
	out := sl.BuildSkillsSummaryFunc(allow)

	assert.Contains(t, out, "<name>granted-skill</name>")
	assert.NotContains(t, out, "<name>ungranted-skill</name>",
		"an ungranted skill must never appear in the menu")
}

// TestBuildSkillsSummary_NoCapNoTruncation covers ADR-072 D1.1 / T7a (spec
// #5): a catalogue well past the old 20-entry cap is listed in full, with no
// truncation footer anywhere in the output.
func TestBuildSkillsSummary_NoCapNoTruncation(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")
	const total = 25 // deliberately past the deleted maxSkillsInSummary=20 cap
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		createSkillDir(t, builtin, name, name, "desc "+name)
	}

	sl := NewSkillsLoader(tmp, "", builtin)
	out := sl.BuildSkillsSummaryFunc(nil)

	assert.Equal(t, total, strings.Count(out, "<skill>"),
		"every eligible skill must be listed; the menu has no cap")
	assert.NotContains(t, out, "not shown above")
	assert.NotContains(t, out, "find_skills",
		"the truncation footer's find_skills signpost must be gone along with the cap")
}

// TestBuildSkillsSummary_GrantedSurvivesLargeMount covers ADR-072 D4.1/T7b
// (spec #6) — the defect that motivated removing the cap in the first place:
// a mount contributing far more skills than the old cap must not crowd an
// operator's explicitly granted registry skills out of the menu. Both sets
// are present in full.
func TestBuildSkillsSummary_GrantedSurvivesLargeMount(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("granted-%d", i)
		createSkillDir(t, builtin, name, name, "an explicitly granted registry skill")
	}
	sl := NewSkillsLoader(tmp, "", builtin)
	allow := func(id string) bool { return strings.HasPrefix(id, "granted-") }

	mountRoot := t.TempDir()
	const mountSkillCount = 30
	for i := 0; i < mountSkillCount; i++ {
		slug := fmt.Sprintf("project-skill-%02d", i)
		dir := filepath.Join(mountRoot, ".claude", "skills", slug)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		content := "---\nname: " + slug + "\ndescription: a project skill from the big mount\n---\n\n# " + slug
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644))
	}
	projectShelf, collisions := MergeProjectSkills([]ProjectMount{{Name: "big-mount", Root: mountRoot}})
	require.Empty(t, collisions)
	require.Len(t, projectShelf, mountSkillCount)

	out := sl.BuildSkillsSummaryFuncWithProject(allow, projectShelf)

	assert.Equal(t, 3+mountSkillCount, strings.Count(out, "<skill>"))
	for i := 0; i < 3; i++ {
		assert.Contains(t, out, fmt.Sprintf("<name>granted-%d</name>", i))
	}
	for i := 0; i < mountSkillCount; i++ {
		assert.Contains(t, out, fmt.Sprintf("<name>project-skill-%02d</name>", i))
	}
}

// TestBuildSkillsSummary_OmitsLocationField covers ADR-072 FR-006/N1
// (spec #7): the menu must never carry a filesystem location for any entry.
func TestBuildSkillsSummary_OmitsLocationField(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")
	createSkillDir(t, builtin, "any-skill", "any-skill", "a perfectly ordinary skill")

	sl := NewSkillsLoader(tmp, "", builtin)
	out := sl.BuildSkillsSummaryFunc(nil)

	require.Contains(t, out, "<skill>", "sanity: something was rendered")
	assert.NotContains(t, out, "<location>")
	assert.NotContains(t, out, builtin, "the skills root path itself must not leak into the menu")
}

// TestBuildSkillsSummaryFunc_NoFooterWhenSmall is the surviving regression
// from the deleted cap tests: a small eligible set produces no truncation
// footer (nothing was ever truncated).
func TestBuildSkillsSummaryFunc_NoFooterWhenSmall(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")
	createSkillDir(t, builtin, "solo-skill", "solo-skill", "the only one")

	sl := NewSkillsLoader(tmp, "", builtin)
	out := sl.BuildSkillsSummaryFunc(nil)

	assert.Equal(t, 1, strings.Count(out, "<skill>"))
	assert.NotContains(t, out, "not shown above")
	assert.NotContains(t, out, "find_skills")
}

// TestBuildSkillsSummaryFunc_AllowlistAppliesEvenWithoutCap is the other
// surviving regression: an allowlist admitting far fewer skills than the
// installed catalogue still filters correctly now that there is no cap to
// (previously) also apply.
func TestBuildSkillsSummaryFunc_AllowlistAppliesEvenWithoutCap(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")

	allowedName := "skill-allowed"
	createSkillDir(t, builtin, allowedName, allowedName, "the only allowed one")
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("skill-denied-%02d", i)
		createSkillDir(t, builtin, name, name, "desc "+name)
	}

	sl := NewSkillsLoader(tmp, "", builtin)
	allow := func(id string) bool { return id == allowedName }
	out := sl.BuildSkillsSummaryFunc(allow)

	assert.Equal(t, 1, strings.Count(out, "<skill>"))
	assert.Contains(t, out, "<name>"+allowedName+"</name>")
	assert.NotContains(t, out, "not shown above")
}
