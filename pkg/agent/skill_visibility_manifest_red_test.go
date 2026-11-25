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
// The stored levels and the one visibility authority do not exist at this
// commit (today's shape comes from the fixed tier lists,
// tools.ToolManifestTier). Per the qa-lead RED rule the test fails LOUD
// with BLOCKED until the authority lands; levelTurnPlanOf is the test-local
// seam the fix wires to.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
)

// levelTurnPlan is the visibility authority's outcome for one tool at one
// stored level, as the next new-chat request build sees it (D2's "Expected
// Output" columns).
type levelTurnPlan struct {
	inRequest    bool   // the tool's full definition is sent every turn
	manifestLine string // the one-line listing when not in the request ("" = none)
	searchable   bool   // ToolSearch can still find (and load) it
}

// levelTurnPlanOf is the test-local seam for the C-15 authority. GREEN wires
// it to the production entry point; nil keeps the RED run loud (BLOCKED).
var levelTurnPlanOf func(level, name, description string) (inRequest bool, manifestLine string, searchable bool)

// Test-owned representation only; the named native function returns raw facts,
// not this private test type. Require checks that binding before invocation.
func readNativeLevelTurnPlan(level, name, description string) levelTurnPlan {
	inRequest, manifestLine, searchable := levelTurnPlanOf(level, name, description)
	return levelTurnPlan{inRequest: inRequest, manifestLine: manifestLine, searchable: searchable}
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
	nativebinding.Require(t, "pkg/agent", levelTurnPlanOf,
		"C-1/C-15/D2: native visibility authority; post-GREEN consumer re-audit required")
	const name = "serve_web"
	const shortDescription = "Serve the workspace."

	t.Run("D2#1 full sends the definition every turn", func(t *testing.T) {
		got := readNativeLevelTurnPlan("full", name, shortDescription)
		if !got.inRequest {
			t.Fatalf("full level must send the definition (S-38, C-1): %+v", got)
		}
	})

	t.Run("D2#2 name_and_description lists one line, description cut to 140 characters", func(t *testing.T) {
		got := readNativeLevelTurnPlan("name_and_description", name, longDescription)
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
		got := readNativeLevelTurnPlan("name_and_description", name, "")
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
		got := readNativeLevelTurnPlan("name_only", name, shortDescription)
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
		got := readNativeLevelTurnPlan("hidden", name, shortDescription)
		if got.inRequest || got.manifestLine != "" {
			t.Fatalf("hidden must not be listed (S-38, C-1): %+v", got)
		}
		if !got.searchable {
			t.Fatalf("hidden must stay findable by ToolSearch (S-38, C-1)")
		}
	})

	t.Run("D2#6 effective deny removes the tool from request and search", func(t *testing.T) {
		denied := readNativeLevelTurnPlan("deny", name, shortDescription)
		if denied.inRequest || denied.manifestLine != "" || denied.searchable {
			t.Fatalf("an effective deny overrides every level (S-40, C-1): %+v", denied)
		}
	})
}

// TestManifest_LevelsShapeTurn_RealTurnReadsTheLevelAuthority is the
// native-path binding obligation for levelTurnPlanOf (CHECK round 4,
// false-green table row 2). TestManifest_LevelsShapeTurn reads the output of
// one adapter function: a function only that adapter calls passes it while the
// real turn builds its request, its ToolSearch membership and its skill menu
// some other way. C-15 and FR-070 require ONE visibility authority for all
// three. This can only be checked after GREEN, so it is a loud BLOCKED leaf
// (never a skip); the test author replaces it at test-side reconciliation with
// real-loop checks, and CHECK then mutates the bound function and requires
// them to fail. Real-loop observers in this pack today: TestVisibilityTransitions
// (D11 rows 1, 2, 3, 6) and TestToolSearch_FindsSkills.
func TestManifest_LevelsShapeTurn_RealTurnReadsTheLevelAuthority(t *testing.T) {
	t.Fatal("BLOCKED: the real turn is not shown to take its request manifest, " +
		"its ToolSearch membership and its skill menu from the one function " +
		"levelTurnPlanOf binds to — required by C-15 and FR-070 (manifest, search " +
		"membership and skill menu MUST read one visibility authority); wiring the " +
		"seam, or adding a production function nothing calls, must never turn this green")
}
