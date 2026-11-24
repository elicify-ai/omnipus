// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 9.
// TestVisibilityStartingLevels_Upgrade: the one-time "tool-loading
// conversion" (C-19 step 3) derives every tool's and skill's starting level
// from the whole pre-upgrade request pipeline — Dataset D7 is the literal
// table ("the spec's oracle").
//
// Oracle: skills-tools-redesign-spec.md C-6 (starting levels paragraph) and
// Dataset D7 rows 1–16; S-41. Levels come from the OLD switches
// (tools.manifest.compressed, preview_all_lazy, tools.mcp.discovery.enabled,
// each server's deferred) — never from the new resolver. The MCP level is
// stored per server as visibility["mcp_<server>_*"] (C-6); skills start at
// name_and_description (D7 row 14); a second start writes nothing (row 15).
//
// The conversion does not exist at this commit. Per the qa-lead RED rule the
// test fails LOUD with BLOCKED until it lands; visibilityConversionPlanOf is
// the test-local seam the fix wires to. D7 row 16's request-surface half
// (ToolSearch "offered in the normal request after the conversion, absent
// before") is order 56 (TestToolSurface_ConversionToolSearch); this test
// pins only the stored level.

import (
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
)

// mcpServerOldCfg is one server's pre-upgrade state.
type mcpServerOldCfg struct {
	id       string
	deferred *bool // nil = unset
	offline  bool  // server offline at upgrade time
}

// conversionOldState is the pre-upgrade switch state D7 reads.
type conversionOldState struct {
	name             string
	compressed       bool // tools.manifest.compressed
	previewAllLazy   bool // tools.manifest.preview_all_lazy
	discoveryEnabled bool // tools.mcp.discovery.enabled
	discoverySet     bool // whether the key is present at all
	staticTool       string
	servers          []mcpServerOldCfg
	skills           []string
	want             map[string]string // sandbox.tool_visibility entries to write
}

func d7BoolPtr(b bool) *bool { return &b }

// d7Table is Dataset D7 rows 1–16, transcribed from the spec.
var d7Table = []conversionOldState{
	{
		name:       "D7#1 compressed on, 36-list tool read_file -> full",
		compressed: true, staticTool: "read_file",
		want: map[string]string{"read_file": "full"},
	},
	{
		name:       "D7#2 compressed on, serve_web -> name_and_description",
		compressed: true, staticTool: "serve_web",
		want: map[string]string{"serve_web": "name_and_description"},
	},
	{
		name:       "D7#3 compressed on, browser_evaluate -> hidden",
		compressed: true, staticTool: "browser_evaluate",
		want: map[string]string{"browser_evaluate": "hidden"},
	},
	{
		name:       "D7#4 old switch on lifts the search-only tool to name_and_description",
		compressed: true, previewAllLazy: true, staticTool: "browser_evaluate",
		want: map[string]string{"browser_evaluate": "name_and_description"},
	},
	{
		name:       "D7#5 compressed on, deferred server unset -> hidden (server key)",
		compressed: true, discoveryEnabled: true, discoverySet: true,
		servers: []mcpServerOldCfg{{id: "github"}},
		want:    map[string]string{"mcp_github_*": "hidden"},
	},
	{
		name:       "D7#6 compressed on, registered visible but lazy -> hidden (server key)",
		compressed: true, discoveryEnabled: true, discoverySet: true,
		servers: []mcpServerOldCfg{{id: "github", deferred: d7BoolPtr(false)}},
		want:    map[string]string{"mcp_github_*": "hidden"},
	},
	{
		name:       "D7#7 visible + preview -> name_and_description (server key)",
		compressed: true, previewAllLazy: true, discoveryEnabled: true, discoverySet: true,
		servers: []mcpServerOldCfg{{id: "github", deferred: d7BoolPtr(false)}},
		want:    map[string]string{"mcp_github_*": "name_and_description"},
	},
	{
		name:       "D7#8 registered hidden stays hidden (server key)",
		compressed: true, previewAllLazy: true, discoveryEnabled: true, discoverySet: true,
		servers: []mcpServerOldCfg{{id: "github"}},
		want:    map[string]string{"mcp_github_*": "hidden"},
	},
	{
		name:       "D7#9 compressed off, any static tool -> full",
		staticTool: "browser_evaluate",
		want:       map[string]string{"browser_evaluate": "full"},
	},
	{
		name:             "D7#10 compressed off, effectively deferred -> hidden (server key)",
		discoveryEnabled: true, discoverySet: true,
		servers: []mcpServerOldCfg{{id: "github", deferred: d7BoolPtr(true)}},
		want:    map[string]string{"mcp_github_*": "hidden"},
	},
	{
		name:    "D7#11 discovery off -> full (server key)",
		servers: []mcpServerOldCfg{{id: "github", deferred: d7BoolPtr(true)}},
		want:    map[string]string{"mcp_github_*": "full"},
	},
	{
		name:             "D7#12 not deferred -> full (server key)",
		discoveryEnabled: true, discoverySet: true,
		servers: []mcpServerOldCfg{{id: "github", deferred: d7BoolPtr(false)}},
		want:    map[string]string{"mcp_github_*": "full"},
	},
	{
		name:       "D7#13 server offline at upgrade: key written from stored config",
		compressed: true, discoveryEnabled: true, discoverySet: true,
		servers: []mcpServerOldCfg{{id: "github", offline: true}},
		want:    map[string]string{"mcp_github_*": "hidden"},
	},
	{
		name:       "D7#14 every skill -> name_and_description",
		compressed: true, skills: []string{"plan", "deep-research"},
		want: map[string]string{"plan": "name_and_description", "deep-research": "name_and_description"},
	},
	{
		name:       "D7#16 ToolSearch -> locked full",
		staticTool: "ToolSearch",
		want:       map[string]string{"ToolSearch": "full"},
	},
}

// visibilityConversionPlanOf is the test-local seam for the C-19 step 3
// conversion plan. The GREEN step wires it to the production entry point;
// it stays nil here so the RED run fails with BLOCKED instead of panicking.
// Raw pre-upgrade inputs only: no case name or expected output reaches the
// production conversion. These are provisional test bindings, not spec APIs.
var visibilityConversionPlanOf func(compressed, previewAllLazy, discoveryEnabled, discoverySet bool, staticTool string, servers map[string]*bool, offline map[string]bool, skills []string) map[string]string

func invokeVisibilityConversionPlan(old conversionOldState) map[string]string {
	servers, offline := map[string]*bool{}, map[string]bool{}
	for _, server := range old.servers {
		servers[server.id], offline[server.id] = server.deferred, server.offline
	}
	return visibilityConversionPlanOf(old.compressed, old.previewAllLazy, old.discoveryEnabled, old.discoverySet, old.staticTool, servers, offline, old.skills)
}

// TestVisibilityStartingLevels_Upgrade drives Dataset D7 rows 1–16.
// Traces to: BDD S-41; spec C-6 (starting levels), C-19 step 3; Dataset D7.
func TestVisibilityStartingLevels_Upgrade(t *testing.T) {
	nativebinding.Require(t, "pkg/config", visibilityConversionPlanOf,
		"C-6/C-19/S-41/D7: native starting-level conversion; post-GREEN boot-path re-audit required")
	for _, tc := range d7Table {
		t.Run(tc.name, func(t *testing.T) {
			got := invokeVisibilityConversionPlan(tc)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("conversion plan = %v, want %v (Dataset D7: %s)", got, tc.want, tc.name)
			}
			// D7#15: a second start recomputes the same plan and writes
			// nothing new — the plan must be a pure function of the old
			// state (replay-safe, C-19 step 2).
			again := invokeVisibilityConversionPlan(tc)
			if !reflect.DeepEqual(got, again) {
				t.Fatalf("second plan differs: %v vs %v (Dataset D7 row 15: nothing written)", again, got)
			}
		})
	}
}

// TestVisibilityStartingLevels_Upgrade_StartupWritesTheConversion is the
// native-path binding obligation for visibilityConversionPlanOf (CHECK round 4,
// false-green table row 9). TestVisibilityStartingLevels_Upgrade hands the
// conversion raw old switches and compares the returned map; its "second start
// writes nothing" check (D7 row 15) only calls the same pure function twice. It
// cannot show that the real C-19 upgrade (step 3) writes the converted levels,
// or that a second start leaves them alone. This can only be checked after
// GREEN, so it is a loud BLOCKED leaf (never a skip); the test author replaces
// it at test-side reconciliation with a real upgrade run on a seeded home that
// carries the D7 old switches, read back from config.json after two starts, and
// CHECK then mutates the bound function and requires it to fail. Real-path
// observers of the stored levels today: none cited (the
// TestSkillPermissionsUpgrade_StopAtEveryStep fixture seeds compressed and
// discovery switches but its persisted-value assertions do not name the
// visibility maps); TestToolSurface_ConversionToolSearch observes only D7
// row 16's request surface.
func TestVisibilityStartingLevels_Upgrade_StartupWritesTheConversion(t *testing.T) {
	t.Fatal("BLOCKED: the real C-19 upgrade (step 3) is not shown to write the " +
		"starting levels of the function visibilityConversionPlanOf binds to " +
		"into config.json from the old switches, nor to leave them untouched on " +
		"a second start — required by C-6, C-19 step 3, S-41 and Dataset D7 " +
		"(rows 1-16); a conversion function only this adapter calls must never " +
		"turn this green")
}
