// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-092 D9 §6 drift guard.
//
// docs/internal/specs/adr-092-auto-for-other-tools-design.md §6 lists seven
// checks the classifier table (pkg/tools/auto_approve.go, lane L1) must
// keep satisfying against the gateway's own real startup catalog
// (buildCentralBuiltinRegistry — the SAME function boot calls, per
// grep_tools_wire_test.go's precedent: assert on the function's OUTPUT, not
// on source containing a call). Each check is a pure function over data so
// it can be run twice: once against the real table (must pass) and once
// against a deliberately mutated copy (must fail) — the mutation self-check
// T13 requires, proving the guard can actually catch the drift it claims to
// catch rather than vacuously passing.
package gateway

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// goldenAskList28 is a literal, reviewable copy of the 28 catalog tool names
// the founder's ruled choices file
// (/Users/danielpiatkowski/Desktop/auto-approve-choices.json, saved
// 2026-09-23T14:20:18Z) marks "asks" — every non-MCP entry with
// choice=="asks". Moving a tool off this list, or onto it, needs a
// deliberate edit HERE as well as in pkg/tools/auto_approve.go's table
// (§6 point 6): that is the point of the golden copy.
var goldenAskList28 = []string{
	"add_mcp_server",
	"browser_evaluate",
	"browser_upload_file",
	"configure_channel",
	"configure_provider",
	"create_agent",
	"create_skill",
	"delete_agent",
	"delete_task",
	"delete_task_in_workspace",
	"delete_workspace",
	"disable_channel",
	"edit_skill",
	"enable_channel",
	"environment_setup",
	"install_skill",
	"remove_mcp_server",
	"remove_skill",
	"reply",
	"request_mount",
	"run_doctor",
	"send_email",
	"serve_web",
	"set_config",
	"test_channel",
	"test_provider",
	"update_agent",
	"update_workspace",
}

// catalogToolNames returns every name buildCentralBuiltinRegistry(nil) —
// the SAME function the gateway calls at boot for GET /api/v1/tools — puts
// in the process-wide builtin catalog. This is the coverage universe for
// checks 1, 2 and 4.
func catalogToolNames(t *testing.T) []string {
	t.Helper()
	reg, _ := buildCentralBuiltinRegistry(nil)
	all := reg.All()
	names := make([]string, 0, len(all))
	for _, tl := range all {
		names = append(names, tl.Name())
	}
	sort.Strings(names)
	return names
}

// --- Check 1: coverage — every catalog tool has an explicit table entry ---

func checkCoverage(catalogNames []string, table map[string]tools.AutoApproveClass) []string {
	var errs []string
	for _, name := range catalogNames {
		if _, ok := table[name]; !ok {
			errs = append(errs, "tool \""+name+"\" has no explicit Auto-approve classification entry; add an explicit entry; the default is ASKS")
		}
	}
	return errs
}

func TestAutoApproveClassification_CoversEveryCatalogTool(t *testing.T) {
	names := catalogToolNames(t)
	table := tools.AutoApproveClassTable()
	if errs := checkCoverage(names, table); len(errs) > 0 {
		t.Errorf("classification table coverage gap(s):\n%s", strings.Join(errs, "\n"))
	}
}

func TestAutoApproveClassification_MutationSelfCheck_CoverageCatchesRemoval(t *testing.T) {
	names := catalogToolNames(t)
	table := tools.AutoApproveClassTable()
	if errs := checkCoverage(names, table); len(errs) != 0 {
		t.Fatalf("precondition failed: the real table already fails coverage: %v", errs)
	}
	mutated := make(map[string]tools.AutoApproveClass, len(table))
	for k, v := range table {
		mutated[k] = v
	}
	delete(mutated, "grep") // grep is a real catalog tool (TestCentralBuiltinRegistry_CarriesGrep)
	if errs := checkCoverage(names, mutated); len(errs) == 0 {
		t.Fatal("mutation self-check failed: deleting the \"grep\" entry did not make the coverage check fail")
	}
}

// --- Check 2: no stale entries — every table key is a real catalog tool, or bash ---

func checkNoStaleEntries(catalogNames []string, table map[string]tools.AutoApproveClass) []string {
	known := make(map[string]bool, len(catalogNames)+1)
	for _, n := range catalogNames {
		known[n] = true
	}
	known["bash"] = true // §3.7: bash keeps its own mechanism, and IS in the catalog too.
	var errs []string
	for name := range table {
		if !known[name] {
			errs = append(errs, "table entry \""+name+"\" names no tool in the gateway's builtin catalog and is not \"bash\"")
		}
	}
	sort.Strings(errs)
	return errs
}

func TestAutoApproveClassification_NoStaleTableEntries(t *testing.T) {
	names := catalogToolNames(t)
	table := tools.AutoApproveClassTable()
	if errs := checkNoStaleEntries(names, table); len(errs) > 0 {
		t.Errorf("stale classification table entries:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAutoApproveClassification_MutationSelfCheck_NoStaleEntriesCatchesGhostEntry(t *testing.T) {
	names := catalogToolNames(t)
	table := tools.AutoApproveClassTable()
	if errs := checkNoStaleEntries(names, table); len(errs) != 0 {
		t.Fatalf("precondition failed: the real table already has stale entries: %v", errs)
	}
	mutated := make(map[string]tools.AutoApproveClass, len(table)+1)
	for k, v := range table {
		mutated[k] = v
	}
	mutated["no_such_tool_ever_registered"] = tools.AutoRuns
	if errs := checkNoStaleEntries(names, mutated); len(errs) == 0 {
		t.Fatal("mutation self-check failed: a ghost table entry did not make the no-stale-entries check fail")
	}
}

// --- Check 3: every key in the global policy ceiling has an entry ---

func checkCeilingCoverage(ceilingNames []string, table map[string]tools.AutoApproveClass) []string {
	var errs []string
	for _, name := range ceilingNames {
		if _, ok := table[name]; !ok {
			errs = append(errs, "sandbox.tool_policies ceiling key \""+name+"\" has no Auto-approve classification entry")
		}
	}
	return errs
}

func ceilingToolNames() []string {
	ceiling := config.DefaultToolPolicyCeiling()
	names := make([]string, 0, len(ceiling))
	for name := range ceiling {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestAutoApproveClassification_CoversEveryGlobalCeilingKey(t *testing.T) {
	names := ceilingToolNames()
	if len(names) == 0 {
		t.Fatal("config.DefaultToolPolicyCeiling() returned no keys — the ceiling seed is broken, not that there is nothing to check")
	}
	table := tools.AutoApproveClassTable()
	if errs := checkCeilingCoverage(names, table); len(errs) > 0 {
		t.Errorf("global tool-policy ceiling keys missing an Auto-approve entry:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAutoApproveClassification_MutationSelfCheck_CeilingCoverageCatchesRemoval(t *testing.T) {
	names := ceilingToolNames()
	table := tools.AutoApproveClassTable()
	if errs := checkCeilingCoverage(names, table); len(errs) != 0 {
		t.Fatalf("precondition failed: the real table already fails ceiling coverage: %v", errs)
	}
	mutated := make(map[string]tools.AutoApproveClass, len(table))
	for k, v := range table {
		mutated[k] = v
	}
	delete(mutated, names[0])
	if errs := checkCeilingCoverage(names, mutated); len(errs) == 0 {
		t.Fatalf("mutation self-check failed: deleting ceiling key %q did not make the ceiling-coverage check fail", names[0])
	}
}

// --- Check 4: every AutoRunsIfArgs tool's live catalog instance implements the classifier ---

func checkClassifiersExist(instances map[string]tools.Tool, table map[string]tools.AutoApproveClass) []string {
	var errs []string
	names := make([]string, 0, len(table))
	for name, class := range table {
		if class == tools.AutoRunsIfArgs {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		inst, ok := instances[name]
		if !ok {
			errs = append(errs, "AutoRunsIfArgs tool \""+name+"\" has no live catalog instance to check")
			continue
		}
		if _, ok := inst.(tools.AutoApproveClassifier); !ok {
			errs = append(errs, "AutoRunsIfArgs tool \""+name+"\"'s live instance does not implement tools.AutoApproveClassifier")
		}
	}
	return errs
}

func catalogInstancesByName(t *testing.T) map[string]tools.Tool {
	t.Helper()
	reg, _ := buildCentralBuiltinRegistry(nil)
	out := make(map[string]tools.Tool, len(reg.All()))
	for _, tl := range reg.All() {
		out[tl.Name()] = tl
	}
	return out
}

func TestAutoApproveClassification_RunsIfArgsToolsImplementClassifier(t *testing.T) {
	instances := catalogInstancesByName(t)
	table := tools.AutoApproveClassTable()
	if errs := checkClassifiersExist(instances, table); len(errs) > 0 {
		t.Errorf("AutoRunsIfArgs classifier gap(s):\n%s", strings.Join(errs, "\n"))
	}
}

func TestAutoApproveClassification_MutationSelfCheck_ClassifiersExistCatchesMissingImpl(t *testing.T) {
	instances := catalogInstancesByName(t)
	table := tools.AutoApproveClassTable()
	if errs := checkClassifiersExist(instances, table); len(errs) != 0 {
		t.Skipf("precondition not met: a real AutoRunsIfArgs tool does not implement the classifier yet (expected until every lane lands): %v", errs)
	}
	mutated := make(map[string]tools.Tool, len(instances))
	for k, v := range instances {
		mutated[k] = v
	}
	mutated["read_file"] = stubNonClassifierTool{name: "read_file"}
	if errs := checkClassifiersExist(mutated, table); len(errs) == 0 {
		t.Fatal("mutation self-check failed: substituting a non-classifier stub for read_file did not make the classifiers-exist check fail")
	}
}

// stubNonClassifierTool is a minimal tools.Tool that deliberately does NOT
// implement tools.AutoApproveClassifier, for the check-4 mutation self-check.
type stubNonClassifierTool struct{ name string }

func (s stubNonClassifierTool) Name() string                 { return s.name }
func (s stubNonClassifierTool) Description() string          { return "stub" }
func (s stubNonClassifierTool) Parameters() map[string]any    { return map[string]any{} }
func (s stubNonClassifierTool) Scope() tools.ToolScope        { return tools.ScopeGeneral }
func (s stubNonClassifierTool) Category() tools.ToolCategory  { return tools.CategoryFilesystem }
func (s stubNonClassifierTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return tools.ErrorResult("stub: not callable")
}

// --- Check 5: safe default — an unknown name asks, and AutoAsks is the zero value ---

func TestAutoApproveClassification_UnknownToolDefaultsToAsks(t *testing.T) {
	if got := tools.AutoApproveClassOf("no_such_tool"); got != tools.AutoAsks {
		t.Errorf("AutoApproveClassOf(\"no_such_tool\") = %v, want AutoAsks", got)
	}
	if tools.AutoApproveClass(0) != tools.AutoAsks {
		t.Fatalf("AutoAsks is no longer the zero value of AutoApproveClass — an unclassified tool would silently stop asking")
	}
}

func TestAutoApproveClassification_MutationSelfCheck_SafeDefaultCatchesWrongExpectation(t *testing.T) {
	// A check written to expect the WRONG class for an unknown tool would
	// itself never fire — this proves such a check function CAN fail, i.e.
	// the assertion in the test above is not vacuously true.
	checkExpectsRuns := func() bool { return tools.AutoApproveClassOf("no_such_tool") == tools.AutoRuns }
	if checkExpectsRuns() {
		t.Fatal("mutation self-check failed: an unknown tool classified as AutoRuns would have gone undetected")
	}
}

// --- Check 6: ask-list lock — the golden 28 names equal the table's AutoAsks set exactly ---

func checkAskListLock(table map[string]tools.AutoApproveClass, golden []string) []string {
	goldenSet := make(map[string]bool, len(golden))
	for _, n := range golden {
		goldenSet[n] = true
	}
	tableAsks := make(map[string]bool)
	for name, class := range table {
		if class == tools.AutoAsks {
			tableAsks[name] = true
		}
	}
	var errs []string
	for n := range goldenSet {
		if !tableAsks[n] {
			errs = append(errs, "golden ask-list name \""+n+"\" is missing from the table's ASKS set")
		}
	}
	for n := range tableAsks {
		if !goldenSet[n] {
			errs = append(errs, "table ASKS tool \""+n+"\" is not on the golden ask-list")
		}
	}
	sort.Strings(errs)
	return errs
}

func TestAutoApproveClassification_AskListMatchesGoldenCopy(t *testing.T) {
	if len(goldenAskList28) != 28 {
		t.Fatalf("goldenAskList28 has %d names, want 28 (founder file tally)", len(goldenAskList28))
	}
	table := tools.AutoApproveClassTable()
	if errs := checkAskListLock(table, goldenAskList28); len(errs) > 0 {
		t.Errorf("ask-list drift against the founder-file golden copy:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAutoApproveClassification_MutationSelfCheck_AskListLockCatchesMovedTool(t *testing.T) {
	table := tools.AutoApproveClassTable()
	if errs := checkAskListLock(table, goldenAskList28); len(errs) != 0 {
		t.Fatalf("precondition failed: the real table already drifts from the golden ask-list: %v", errs)
	}
	// Move "delete_task" off the ask-list without touching the golden copy —
	// simulates someone flipping the table without the deliberate two-place edit.
	mutated := make(map[string]tools.AutoApproveClass, len(table))
	for k, v := range table {
		mutated[k] = v
	}
	if mutated["delete_task"] != tools.AutoAsks {
		t.Fatal("test fixture assumption broken: delete_task is not classified AutoAsks in the real table")
	}
	mutated["delete_task"] = tools.AutoRuns
	if errs := checkAskListLock(mutated, goldenAskList28); len(errs) == 0 {
		t.Fatal("mutation self-check failed: moving delete_task off ASKS did not make the ask-list-lock check fail")
	}
}

// --- Check 7: no MCP names in the table ---

func checkNoMCPNames(table map[string]tools.AutoApproveClass) []string {
	var errs []string
	for name := range table {
		if strings.HasPrefix(name, "mcp_") {
			errs = append(errs, "table entry \""+name+"\" starts with \"mcp_\"; MCP tools are classified by annotation (§4), never listed here")
		}
	}
	sort.Strings(errs)
	return errs
}

func TestAutoApproveClassification_NoMCPNamesInTable(t *testing.T) {
	table := tools.AutoApproveClassTable()
	if errs := checkNoMCPNames(table); len(errs) > 0 {
		t.Errorf("MCP-prefixed names found in the static table:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAutoApproveClassification_MutationSelfCheck_NoMCPNamesCatchesLeakedEntry(t *testing.T) {
	table := tools.AutoApproveClassTable()
	if errs := checkNoMCPNames(table); len(errs) != 0 {
		t.Fatalf("precondition failed: the real table already has MCP-prefixed entries: %v", errs)
	}
	mutated := make(map[string]tools.AutoApproveClass, len(table)+1)
	for k, v := range table {
		mutated[k] = v
	}
	mutated["mcp_github-mcp_create_issue"] = tools.AutoRuns
	if errs := checkNoMCPNames(mutated); len(errs) == 0 {
		t.Fatal("mutation self-check failed: an mcp_-prefixed table entry did not make the no-MCP-names check fail")
	}
}

// sysToolsSanity guards against catalogToolNames silently returning an
// empty/near-empty set (e.g. a nil systools.Deps causing every system tool
// constructor to bail) making every check above vacuously pass.
func TestAutoApproveClassification_CatalogSanity(t *testing.T) {
	names := catalogToolNames(t)
	if len(names) < 100 {
		t.Fatalf("buildCentralBuiltinRegistry(nil) only produced %d tool names; the ADR-092 D9 founder file classifies 110 catalog tools plus bash — this suite would pass vacuously against a near-empty catalog", len(names))
	}
	// Sanity that the system family specifically is present (it carries most
	// of the 28-name ask-list).
	sysNames := make(map[string]bool)
	for _, tl := range systools.AllTools(nil) {
		sysNames[tl.Name()] = true
	}
	if !sysNames["set_config"] {
		t.Fatal("systools.AllTools(nil) is missing \"set_config\" — the system family the ask-list golden copy depends on is not what this test assumed")
	}
}
