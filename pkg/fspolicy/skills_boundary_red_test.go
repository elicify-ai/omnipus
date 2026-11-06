//go:build goolm && stdjson

package fspolicy

// RED tests — landing/skills (T9): the §6.1 app-layer skills boundary.
// Scenarios S-2.4 (registry control entries), S-2.2/S-2.3 (grant-filtered
// reads), S-2.10 read half + m-01 (dot-files inside a skill are part of the
// skill), §6.1 item 1 (HomeGuard contract), §6.1 item 2 (inventory-first
// order), §6.1 item 3 (skills leaves BaseAllowRelative) and the S-2.9
// regression pair (whole-system protection exists at the pin, V-43).
// Traces to: skills-spec-r15.md §6.1 items 1–3 + item 7, S-2.2–S-2.4,
// S-2.9 (regression), S-2.10 read half, §17.1 rows 5 and 8.
//
// RED expectation: HomeGuard lacks the two new fields, skills is still in
// BaseAllowRelative, and registry control entries escape the inventory —
// every RED assertion below fails on the baseline.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSkillsBoundary_HomeGuardContract (§6.1 item 1).
// BDD: FSPolicy.HomeGuard gains SkillFolders []string (real paths of the
// loadable set) and SkillsReadAll bool (effective edit_skill ≠ deny), filled
// only by tools.ResolveTurnFSPolicy.
// Traces to: skills-spec-r15.md §6.1 item 1, §17.1 row 5.
func TestSkillsBoundary_HomeGuardContract(t *testing.T) {
	guardType := reflect.TypeOf(HomeGuard{})

	foldersField, ok := guardType.FieldByName("SkillFolders")
	require.True(t, ok, "RED: HomeGuard.SkillFolders does not exist yet (§6.1 item 1)")
	assert.Equal(t, "[]string", foldersField.Type.String(),
		"SkillFolders holds the real paths of the loadable set")

	readAllField, ok := guardType.FieldByName("SkillsReadAll")
	require.True(t, ok, "RED: HomeGuard.SkillsReadAll does not exist yet (§6.1 item 1)")
	assert.Equal(t, "bool", readAllField.Type.String(),
		"SkillsReadAll is the effective edit_skill ≠ deny flag")
}

// TestSkillsBoundary_RegistryControlEntriesRefused (S-2.4).
// BDD: for anyone, editors included — list_directory skills/ and reads of
// skills/.staging/x, .swap/x, .hidden, plan.replace-backup/SKILL.md and any
// non-slug first component are refused (inventory denies). The skills root
// itself is refused too (§6.1 item 2's `first == ""` leg).
// Traces to: skills-spec-r15.md §6.1 item 2, S-2.4.
func TestSkillsBoundary_RegistryControlEntriesRefused(t *testing.T) {
	home := realHome(t)
	// A maximally permissive reader: SkillsReadAll semantics are asserted
	// separately; here even the widest allowance must not re-admit a
	// control entry.
	pol := FSPolicy{
		HomeGuard: HomeGuard{
			Home: home,
			Allow: []AllowRoot{
				{Path: filepath.Join(home, "skills", ".staging")},
				{Path: filepath.Join(home, "skills")},
			},
		},
		WorkDir: filepath.Join(home, "skills"),
	}

	cases := []struct {
		name string
		rel  string
	}{
		{"skills root itself", "skills"},
		{"staging scratch", filepath.Join("skills", ".staging", "x")},
		{"swap scratch", filepath.Join("skills", ".swap", "x")},
		{"hidden entry", filepath.Join("skills", ".hidden")},
		{"legacy replace-backup", filepath.Join("skills", "plan.replace-backup", "SKILL.md")},
		{"non-slug first component", filepath.Join("skills", "Not A Slug", "x")},
		{"dot seed entry", filepath.Join("skills", ".seed-abc", "SKILL.md")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, homeGuardDeny(filepath.Join(home, filepath.FromSlash(tc.rel)), pol),
				"registry control entry %s must be denied (RED: the inventory lets it through today)", tc.rel)
		})
	}
}

// TestSkillsBoundary_LoadableSetGrantFiltered (S-2.2, S-2.3).
// BDD: with SkillsReadAll=false and SkillFolders=[<home>/skills/granted] —
// a read inside the granted folder is allowed (S-2.1's read half); a read of
// an ungranted folder is denied for a non-editor (edit_skill: deny, S-2.2);
// with SkillsReadAll=true (an editor, S-2.3's read half) every valid-slug
// skill folder is readable.
// Traces to: skills-spec-r15.md §6.1 item 2, S-2.2, S-2.3.
func TestSkillsBoundary_LoadableSetGrantFiltered(t *testing.T) {
	home := realHome(t)

	t.Run("granted folder readable (non-editor)", func(t *testing.T) {
		// The realistic turn guard: base allowances (media, skills helper
		// files) as ResolveTurnFSPolicy seeds them today.
		pol := FSPolicy{HomeGuard: configuredSkillGuard(t, home, false)}
		assert.False(t, homeGuardDeny(filepath.Join(home, "skills", "granted", "SKILL.md"), pol),
			"a read inside the granted, loadable folder is allowed (RED: today the leaf rule denies it)")
	})

	t.Run("S-2.2 ungranted folder denied for a non-editor", func(t *testing.T) {
		pol := FSPolicy{HomeGuard: configuredSkillGuard(t, home, false)}
		assert.True(t, homeGuardDeny(filepath.Join(home, "skills", "other", "helper.md"), pol),
			"an ungranted skill's helper file is denied for a non-editor (edit_skill: deny)")
		assert.True(t, homeGuardDeny(filepath.Join(home, "skills", "other", "SKILL.md"), pol),
			"an ungranted skill's instruction file stays denied for a non-editor")
	})

	t.Run("S-2.3 editor (SkillsReadAll) reads every valid-slug folder", func(t *testing.T) {
		pol := FSPolicy{HomeGuard: configuredSkillGuard(t, home, true)}
		assert.False(t, homeGuardDeny(filepath.Join(home, "skills", "editable", "helper.md"), pol),
			"an editor reads any valid-slug skill folder (RED: today this passes for the wrong reason — the leaf rule never fired on helper files)")
		// The distinguishing RED leg: with the rule in place, the editor's
		// read of the instruction leaf is ALSO allowed (SkillsReadAll), which
		// the baseline denies.
		assert.False(t, homeGuardDeny(filepath.Join(home, "skills", "editable", "SKILL.md"), pol),
			"an editor reads the ungranted skill's instruction file too (SkillsReadAll=true) (RED: today the A7 leaf rule denies it)")
	})
}

// TestSkillsBoundary_InventoryFirstOrder (§6.1 item 2).
// BDD: inventoryDenies runs before every mount, path grant, setup root and
// work dir — no allowance re-admits a refused skill path.
// Traces to: skills-spec-r15.md §6.1 item 2 (order sentence).
func TestSkillsBoundary_InventoryFirstOrder(t *testing.T) {
	home := realHome(t)
	pol := FSPolicy{
		HomeGuard: HomeGuard{
			Home: home,
			Allow: []AllowRoot{
				{Path: filepath.Join(home, "skills", ".staging")},
			},
		},
		WorkDir:    filepath.Join(home, "skills", ".staging"),
		SetupRoots: []string{filepath.Join(home, "skills")},
	}
	pol.HomeGuard = configuredSkillGuard(t, home, false)
	pol.HomeGuard.Allow = []AllowRoot{{Path: filepath.Join(home, "skills"), Writable: true}}
	pol.AllowedRoots = []string{home}
	pol.PathGrants = []PathGrant{{Path: home, Access: PathGrantAccessRead | PathGrantAccessWrite}}
	assert.True(t, homeGuardDeny(filepath.Join(home, "skills", "ordinary-ungranted", "helper.md"), pol), "ordinary ungranted skill cannot be reopened by enclosing mount, operator grant, setup root or workdir")
	assert.True(t, homeGuardDeny(filepath.Join(home, "skills", ".staging", "incoming.bin"), pol),
		"an Allow entry, the turn's work dir and a setup root covering the path must not re-admit a refused registry control entry (RED: today the allowance wins)")
}

// TestSkillsBoundary_SkillsLeaveBaseAllowRelative (§6.1 item 3).
// BDD: `skills` is removed from BaseAllowRelative; the turn-free protection
// predicate therefore reports skill paths as protected.
// Traces to: skills-spec-r15.md §6.1 item 3.
func TestSkillsBoundary_SkillsLeaveBaseAllowRelative(t *testing.T) {
	assert.NotContains(t, BaseAllowRelative, "skills",
		"RED: skills is still a base allow entry (§6.1 item 3 removes it)")

	home := realHome(t)
	assert.True(t, IsProtectedHomePath(filepath.Join(home, "skills", "foo", "helper.md"), home),
		"the turn-free predicate protects skill paths once skills leaves the base allow list (RED: today it is re-admitted)")
}

// TestSkillsBoundary_DotFilesInsideSkillArePartOfIt (m-01, §6.1 item 7).
// BDD: dot-files inside a granted skill folder are part of that skill —
// whoever may read the skill may read them (they are display-hidden, not
// access-controlled).
// Traces to: skills-spec-r15.md §6.1 item 7, S-2.10 read half.
func TestSkillsBoundary_DotFilesInsideSkillArePartOfIt(t *testing.T) {
	home := realHome(t)
	pol := FSPolicy{HomeGuard: configuredSkillGuard(t, home, false)}
	assert.False(t, homeGuardDeny(filepath.Join(home, "skills", "granted", ".notes", "x.md"), pol),
		"a dot-file inside a granted skill is readable by whoever may read the skill (RED: today it is allowed only because skills is a base allow entry — the grant rule must keep it allowed after item 3 lands)")
	assert.False(t, homeGuardDeny(filepath.Join(home, "skills", "granted", ".omnipus-builtin"), pol),
		"the built-in stamp inside a granted skill is readable (part of the skill)")
}

// TestSkillsBoundary_SystemSubtreeProtected_Regression (S-2.9 — named
// regression pair, row 8; V-43: whole-system protection exists at the pin).
// BDD: read AND write of system/skill-history/ are refused for a child with
// file tools in God Mode — must PASS on the baseline and the candidate.
// Traces to: skills-spec-r15.md S-2.9, §17.1 row 8, SC-001.
func TestSkillsBoundary_SystemSubtreeProtected_Regression(t *testing.T) {
	home := realHome(t)
	history := filepath.Join(home, "system", "skill-history", "foo", "versions", "v1.json")

	pol := FSPolicy{
		HomeGuard: HomeGuard{Home: home},
		WorkDir:   filepath.Join(home, "workspaces", "ws", "work"),
	}
	assert.True(t, homeGuardDeny(history, pol),
		"regression pair: system/skill-history is protected (V-43) — must pass on baseline")
	assert.True(t, IsProtectedHomePath(history, home),
		"regression pair: the turn-free predicate protects system/skill-history too")
}

// TestSkillsBoundary_InventoryRulesCarveOuts (S-2.9's kernel-adjacent shape).
// BDD: InventoryGuarded — the operator-pattern adapter's veto — holds for
// the system subtree regardless of allowances.
// Traces to: skills-spec-r15.md §6.1 item 6.
func TestSkillsBoundary_InventoryRulesCarveOuts(t *testing.T) {
	home := realHome(t)
	pol := FSPolicy{
		HomeGuard: HomeGuard{
			Home:  home,
			Allow: []AllowRoot{{Path: filepath.Join(home, "system", "skill-history")}},
		},
	}
	assert.True(t, InventoryGuarded(pol, filepath.Join(home, "system", "skill-history", "foo", "versions", "v1.json")),
		"an operator pattern adapter must never open system/skill-history (inventory always wins)")
}

// realHome returns a realpath'd temp home (identity-based containment needs
// the resolved spelling).
func realHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(resolved, "skills", "granted"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(resolved, "skills", "granted", "SKILL.md"),
		[]byte("---\nname: granted\ndescription: fixture\n---\n\nbody\n"), 0o644))
	return resolved
}

// Supplementary predicate fixtures configure the authority they claim. Missing
// implementation fails loudly, without adding production fields from QA.
func configuredSkillGuard(t *testing.T, home string, readAll bool) HomeGuard {
	t.Helper()
	guard := BaseHomeGuard(home)
	value := reflect.ValueOf(&guard).Elem()
	folders := value.FieldByName("SkillFolders")
	all := value.FieldByName("SkillsReadAll")
	if !folders.IsValid() || !all.IsValid() {
		t.Fatal("BLOCKED: HomeGuard skill authority fields not implemented — required by r15 §6.1")
	}
	require.Equal(t, reflect.TypeOf([]string{}), folders.Type())
	require.Equal(t, reflect.Bool, all.Kind())
	folders.Set(reflect.ValueOf([]string{filepath.Join(home, "skills", "granted")}))
	all.SetBool(readAll)
	return guard
}
