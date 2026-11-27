package agent

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 10.
// TestManifest_LevelsShapeTurn: what the agent is sent for one tool, given
// its stored level — Dataset D2 rows 1–6.
//
// Oracle: skills-tools-redesign-spec.md C-1 (the level meanings table),
// C-15 ("one visibility authority": manifest building reads the stored
// levels through one function; the fixed lists in pkg/tools/manifest.go are
// deleted), BDD S-38, S-40. The 140-character cap of the lazily-listed line
// is C-1's "name and the first line of its description (at most 140
// characters)".
//
// D2 rows 7–10 are covered elsewhere: row 7 (old switch off → static tools
// Full) is the conversion of Dataset D7 (orders 9 and 56); rows 8–9
// (mid-chat transitions) are order 37 (TestVisibilityTransitions); row 10
// (locked PATCH refused) is order 8 (Dataset D6#5).
//
// The stored levels and the one visibility authority do not exist at the
// landing base (that shape came from the fixed tier lists,
// tools.ToolManifestTier). Per the qa-lead RED rule the test fails LOUD with
// BLOCKED until the authority lands.
//
// Part L, Ruling A (team-lead, 2026-11-26): levelTurnPlanOf is bound to the
// function the real turn calls, tools.ToolSurface.Plan (module pkg/tools, not
// pkg/agent), and not to a test-only wrapper beside it: a second copy of the
// C-15 logic is what FR-066 and FR-070 forbid. The datasets are unchanged;
// only the adapter around the call changed. The production struct types do not
// exist on a tree without the backend, so the seam is an `any` checked by
// tests/helpers/nativebinding and called by field name through
// tests/helpers/structcall: this file still compiles there and the run shows
// BLOCKED instead of a build error. skills_tools_d9_levelplan_bindings_test.go
// binds it. That the real turn builds its request, its ToolSearch membership
// and its skill menu from this same function is
// TestManifest_LevelsShapeTurn_RealTurnReadsTheLevelAuthority
// (skill_visibility_realturn_red_test.go).

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/elicify-ai/omnipus/tests/helpers/structcall"
)

// levelTurnPlan is the visibility authority's outcome for one tool at one
// stored level, as the next new-chat request build sees it (D2's "Expected
// Output" columns).
type levelTurnPlan struct {
	inRequest    bool   // the tool's full definition is sent every turn
	manifestLine string // the one-line listing when not in the request ("" = none)
	searchable   bool   // ToolSearch can still find (and load) it
}

// levelTurnPlanOf is the test-local seam for the C-15 authority. The binding is
// tools.ToolSurface.Plan, a method of the stored levels taking (name,
// description, denied); it stays nil here so the RED run fails loud (BLOCKED).
var levelTurnPlanOf any

// readNativeLevelTurnPlan asks the bound authority what the turn does with one
// tool stored at level. denied is the tool's effective deny (C-1: deny is not a
// level; the tool's policy decides it and it overrides every level), so a row
// that is not a deny passes false. The test-owned levelTurnPlan is built here
// from raw facts; the native function returns its own struct.
func readNativeLevelTurnPlan(t *testing.T, level, name, description string, denied bool) levelTurnPlan {
	t.Helper()
	got := structcall.Call(t, levelTurnPlanOf,
		structcall.Fields{"Levels": map[string]string{name: level}},
		name, description, denied)
	return levelTurnPlan{
		inRequest:    got.Bool(t, "InRequest"),
		manifestLine: got.Str(t, "ManifestLine"),
		searchable:   got.Bool(t, "Searchable"),
	}
}

// capOverflowMarker sits at character 141 of the row-2 description, where
// nothing past the C-1 140-character cap may reach the rendered line.
const capOverflowMarker = "CAP-OVERFLOW-MARKER-9Z"

// longDescription is one line with an exact boundary: 140 filler characters,
// then the marker, so any leak past the cap is visible.
var longDescription = strings.Repeat("x", 140) + capOverflowMarker + " trailing"

// TestManifest_LevelsShapeTurn drives Dataset D2 rows 1–6.
// Traces to: BDD S-38, S-40; spec C-1, C-15; Dataset D2.
func TestManifest_LevelsShapeTurn(t *testing.T) {
	nativebinding.Require(t, "pkg/tools", levelTurnPlanOf,
		"C-1/C-15/D2: the native visibility authority (the binding is ToolSurface.Plan); that the real turn reads it is TestManifest_LevelsShapeTurn_RealTurnReadsTheLevelAuthority")
	const name = "serve_web"
	const shortDescription = "Serve the workspace."

	t.Run("D2#1 full sends the definition every turn", func(t *testing.T) {
		got := readNativeLevelTurnPlan(t, "full", name, shortDescription, false)
		if !got.inRequest {
			t.Fatalf("full level must send the definition (S-38, C-1): %+v", got)
		}
	})

	t.Run("D2#2 name_and_description lists one line, description cut to 140 characters", func(t *testing.T) {
		got := readNativeLevelTurnPlan(t, "name_and_description", name, longDescription, false)
		if got.inRequest {
			t.Fatalf("name_and_description must not send the definition (C-1): %+v", got)
		}
		if got.manifestLine == "" {
			t.Fatalf("name_and_description must be listed as one line (S-38, C-1)")
		}
		if !strings.Contains(got.manifestLine, name) {
			t.Fatalf("the line must carry the name: %q", got.manifestLine)
		}
		// The first 140 characters of the description are pure filler, so the
		// rendered line must carry them and must NOT carry the marker at 141+.
		filler := longDescription[:140]
		if !strings.Contains(got.manifestLine, filler) {
			t.Fatalf("the line must carry the description up to the 140-character cap: %q", got.manifestLine)
		}
		if strings.Contains(got.manifestLine, capOverflowMarker) {
			t.Fatalf("the line leaks past the 140-character cap (C-1): %q", got.manifestLine)
		}
	})

	t.Run("D2#3 empty description lists the name only", func(t *testing.T) {
		got := readNativeLevelTurnPlan(t, "name_and_description", name, "", false)
		if got.inRequest {
			t.Fatalf("name_and_description must not send the definition (C-1): %+v", got)
		}
		if !strings.Contains(got.manifestLine, name) {
			t.Fatalf("the line must carry the name: %q", got.manifestLine)
		}
		if strings.ContainsAny(got.manifestLine, "\n") || len(got.manifestLine) > len(name)+3 {
			t.Fatalf("an empty description must render the name alone (S-38): %q", got.manifestLine)
		}
	})

	t.Run("D2#4 name_only lists the name alone", func(t *testing.T) {
		got := readNativeLevelTurnPlan(t, "name_only", name, shortDescription, false)
		if got.inRequest {
			t.Fatalf("name_only must not send the definition (C-1): %+v", got)
		}
		if !strings.Contains(got.manifestLine, name) {
			t.Fatalf("the line must carry the name: %q", got.manifestLine)
		}
		if strings.Contains(got.manifestLine, shortDescription) {
			t.Fatalf("name_only must not carry the description (C-1): %q", got.manifestLine)
		}
	})

	t.Run("D2#5 hidden is not listed but ToolSearch finds it", func(t *testing.T) {
		got := readNativeLevelTurnPlan(t, "hidden", name, shortDescription, false)
		if got.inRequest || got.manifestLine != "" {
			t.Fatalf("hidden must not be listed (S-38, C-1): %+v", got)
		}
		if !got.searchable {
			t.Fatalf("hidden must stay findable by ToolSearch (S-38, C-1)")
		}
	})

	t.Run("D2#6 effective deny removes the tool from request and search", func(t *testing.T) {
		// S-40: an effective deny overrides every level, so each of the four
		// levels a tool can be stored at is checked, not one representative.
		for _, level := range []string{"full", "name_and_description", "name_only", "hidden"} {
			denied := readNativeLevelTurnPlan(t, level, name, shortDescription, true)
			if denied.inRequest || denied.manifestLine != "" || denied.searchable {
				t.Fatalf("an effective deny overrides the %s level (S-40, C-1): %+v", level, denied)
			}
		}
	})
}
