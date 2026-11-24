// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skills

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 11.
// TestSkillsSummary_Levels: the per-turn skill menu follows the skill's
// stored level (E2 of C-11: listed at its level only when the outcome is
// not deny).
//
// Oracle: skills-tools-redesign-spec.md C-1 (level meanings: a skill at
// name_and_description is "listed in the skill menu with name and
// description (today's behaviour)"; at name_only "listed in the skill menu
// by name alone"; hidden "not listed") and BDD S-42, S-43. A deny removes
// the skill from the menu (C-15: "a Deny removes the skill from the menu").
// The menu-freeze rebuild half of S-43 (the generation key gains a hash of
// the agent's skill outcomes and levels) is session-cache machinery and is
// covered with the consent work (order 34/47); this test pins the rendered
// menu per level.
//
// The menu does not read levels at this commit
// (pkg/skills/loader.go::BuildSkillsSummaryFuncWithProject takes only an
// allow predicate). Per the qa-lead RED rule the test fails LOUD with
// BLOCKED until the level-aware menu lands; skillsMenuLineOf is the
// test-local seam the fix wires to.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
)

// skillsMenuLineOf is the test-local seam for the level-aware menu entry
// renderer: given the skill's stored level, its name and description, it
// returns the menu entry ("" = not listed). GREEN wires it to the
// production entry point; nil keeps the RED run loud (BLOCKED).
var skillsMenuLineOf func(level, name, description string) string

// TestSkillsSummary_Levels drives the menu per level (S-42, S-43).
// Traces to: BDD S-42, S-43; spec C-1 (skill column), C-11 E2, C-15.
func TestSkillsSummary_Levels(t *testing.T) {
	nativebinding.Require(t, "pkg/skills", skillsMenuLineOf,
		"C-1/C-11/C-15/S-42/S-43: native menu renderer; post-GREEN turn-cache re-audit required")
	const name = "deep-research"
	const description = "Multi-step web research with cited sources."

	t.Run("S-42 name_and_description lists name and description", func(t *testing.T) {
		got := skillsMenuLineOf("name_and_description", name, description)
		if !strings.Contains(got, name) || !strings.Contains(got, description) {
			t.Fatalf("menu entry must carry name and description (C-1): %q", got)
		}
	})

	t.Run("S-42 name_only lists the name alone", func(t *testing.T) {
		got := skillsMenuLineOf("name_only", name, description)
		if !strings.Contains(got, name) {
			t.Fatalf("menu entry must carry the name (C-1): %q", got)
		}
		if strings.Contains(got, description) {
			t.Fatalf("name_only must not carry the description (C-1): %q", got)
		}
	})

	t.Run("S-42 hidden is not listed", func(t *testing.T) {
		got := skillsMenuLineOf("hidden", name, description)
		if got != "" {
			t.Fatalf("a hidden skill must not be listed (C-1): %q", got)
		}
	})

	t.Run("C-15 deny removes the skill from the menu", func(t *testing.T) {
		got := skillsMenuLineOf("deny", name, description)
		if got != "" {
			t.Fatalf("a denied skill must not be listed (C-15): %q", got)
		}
	})
}

// TestSkillsSummary_Levels_RealMenuBuilderReadsTheLineAuthority is the
// native-path binding obligation for skillsMenuLineOf (CHECK round 4,
// false-green table row 17). TestSkillsSummary_Levels reads one adapter
// function; it cannot tell whether the real menu builder renders every line
// through it, so a line function only the adapter calls passes it. The
// obligation is C-11 E2 (listed at its level only when the outcome is not
// deny), C-15 and FR-070 (the skill menu reads the one visibility authority and
// rebuilds when an outcome or level changes: the menu-freeze generation key
// gains a hash of the agent's skill outcomes and levels). This can only be
// checked after GREEN, so it is a loud BLOCKED leaf (never a skip); the test
// author replaces it at test-side reconciliation, and CHECK then mutates the
// bound function and requires a real-menu test to fail. Real-loop observer for
// the level and rebuild half today: TestVisibilityTransitions row 6 in
// pkg/agent (S-43).
func TestSkillsSummary_Levels_RealMenuBuilderReadsTheLineAuthority(t *testing.T) {
	t.Fatal("BLOCKED: BuildSkillsSummaryFuncWithProject (pkg/skills/loader.go, " +
		"C-11 E2, the real menu builder) is not shown to render every skill line " +
		"through the one function skillsMenuLineOf binds to, nor to omit a skill " +
		"whose outcome is deny — required by C-1, C-11 E2, C-15, FR-070 and " +
		"S-42/S-43; a line function only this adapter calls must never turn this green")
}
