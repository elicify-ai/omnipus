// Omnipus — manifest classification + builder tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"strings"
	"testing"
)

// --- ToolManifestTier classification ---

func TestToolManifestTier_FullSet(t *testing.T) {
	fullNames := FullManifestToolNames()
	if len(fullNames) == 0 {
		t.Fatal("FullManifestToolNames returned empty slice")
	}
	for _, n := range fullNames {
		got := ToolManifestTier(n)
		if got != ManifestFull {
			t.Errorf("ToolManifestTier(%q) = %v, want ManifestFull", n, got)
		}
	}
}

func TestToolManifestTier_FullSetExact(t *testing.T) {
	// Every name from the spec must be ManifestFull.
	specFull := []string{
		"read_file", "write_file", "edit_file", "list_directory",
		"bash", "search_web", "fetch_url", "send_message",
		"hand_off", "return_to_default", "remember", "recall_memory", "set_todos",
		"navigate", "create_task", "list_tasks", "update_task", "delegate",
	}
	for _, n := range specFull {
		if ToolManifestTier(n) != ManifestFull {
			t.Errorf("ToolManifestTier(%q) should be ManifestFull per spec", n)
		}
	}
	// FullManifestToolNames must contain exactly the spec set.
	got := FullManifestToolNames()
	if len(got) != len(specFull) {
		t.Errorf("FullManifestToolNames() len = %d, want %d", len(got), len(specFull))
	}
}

func TestIsFullManifestTool(t *testing.T) {
	for _, n := range FullManifestToolNames() {
		if !IsFullManifestTool(n) {
			t.Errorf("IsFullManifestTool(%q) = false, want true", n)
		}
	}
	if IsFullManifestTool("create_agent") {
		t.Error("IsFullManifestTool(create_agent) = true, want false")
	}
	if IsFullManifestTool("ToolSearch") {
		t.Error("IsFullManifestTool(ToolSearch) = true, want false (it is Infra)")
	}
}

func TestToolManifestTier_InfraSet(t *testing.T) {
	// The unified `ToolSearch` tool is the sole infra tool.
	infraNames := []string{"ToolSearch"}
	for _, n := range infraNames {
		got := ToolManifestTier(n)
		if got != ManifestInfra {
			t.Errorf("ToolManifestTier(%q) = %v, want ManifestInfra", n, got)
		}
	}
	// Old standalone names must be ManifestLazy (no longer infra).
	for _, n := range []string{"search_tools_bm25", "search_tools_regex"} {
		got := ToolManifestTier(n)
		if got != ManifestLazy {
			t.Errorf("ToolManifestTier(%q) = %v, want ManifestLazy (no longer infra)", n, got)
		}
	}
}

// TestInfraManifestToolNames_ContainsRenamedTool pins the D1 rename
// (ADR-071 D1, spec FR-010, W-D1 test 7) at the classifier map key:
// InfraManifestToolNames() must expose "ToolSearch" and must never expose
// the retired "load_tool" name, so a future edit that reverts the map key
// (or reintroduces the old literal alongside it) fails this test rather than
// silently reopening the rename.
func TestInfraManifestToolNames_ContainsRenamedTool(t *testing.T) {
	names := InfraManifestToolNames()
	found := false
	for _, n := range names {
		if n == "ToolSearch" {
			found = true
		}
		if n == "load_tool" {
			t.Errorf("InfraManifestToolNames() contains retired name %q", n)
		}
	}
	if !found {
		t.Errorf("InfraManifestToolNames() = %v, want it to contain \"ToolSearch\"", names)
	}
}

func TestToolManifestTier_LazySet(t *testing.T) {
	// Sample of tools that must be ManifestLazy per the spec.
	// Note: navigate/create_task/list_tasks/update_task/delegate were promoted
	// to Full-tier and must NOT appear in this list.
	lazySample := []string{
		"create_agent",
		"browser_navigate",
		"send_email",
		"list_agents",
		"browser_screenshot",
		"install_skill",
		"workspace_shell",
	}
	for _, n := range lazySample {
		got := ToolManifestTier(n)
		if got != ManifestLazy {
			t.Errorf("ToolManifestTier(%q) = %v, want ManifestLazy", n, got)
		}
	}
}

func TestFullManifestToolNames_Sorted(t *testing.T) {
	names := FullManifestToolNames()
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("FullManifestToolNames not sorted at index %d: %q > %q", i, names[i-1], names[i])
		}
	}
}

func TestFullManifestToolNames_IndependentCopy(t *testing.T) {
	a := FullManifestToolNames()
	b := FullManifestToolNames()
	// Mutating the first result must not affect the second.
	if len(a) > 0 {
		a[0] = "__mutated__"
	}
	if len(b) > 0 && b[0] == "__mutated__" {
		t.Error("FullManifestToolNames returned the same slice twice (not independent copy)")
	}
}

// --- BuildCompressedManifest ---

// fakeManifestTool is a minimal Tool implementation for manifest builder tests.
type fakeManifestTool struct {
	BaseTool
	name string
	desc string
	cat  ToolCategory
}

func (f *fakeManifestTool) Name() string           { return f.name }
func (f *fakeManifestTool) Description() string    { return f.desc }
func (f *fakeManifestTool) Category() ToolCategory { return f.cat }
func (f *fakeManifestTool) Scope() ToolScope       { return ScopeGeneral }
func (f *fakeManifestTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}

func (f *fakeManifestTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	return NewToolResult("ok")
}

func TestBuildCompressedManifest_Basic(t *testing.T) {
	toolList := []Tool{
		// lazy tools in two categories
		&fakeManifestTool{name: "create_agent", desc: "Create a new agent.", cat: CategoryAgents},
		&fakeManifestTool{name: "list_agents", desc: "List agents.", cat: CategoryAgents},
		&fakeManifestTool{name: "browser_navigate", desc: "Navigate to a URL.", cat: CategoryBrowser},
		// full tool — must be excluded
		&fakeManifestTool{name: "read_file", desc: "Read a file.", cat: CategoryFilesystem},
		// infra tool — must be excluded
		&fakeManifestTool{name: "ToolSearch", desc: "Discover and load tools.", cat: CategoryToolDiscovery},
	}

	got := BuildCompressedManifest(toolList, nil)

	if got == "" {
		t.Fatal("BuildCompressedManifest returned empty string, expected manifest content")
	}
	if !strings.Contains(got, "# More tools") {
		t.Error("manifest missing '# More tools' header")
	}
	if !strings.Contains(got, "create_agent") {
		t.Error("manifest missing create_agent")
	}
	if !strings.Contains(got, "list_agents") {
		t.Error("manifest missing list_agents")
	}
	if !strings.Contains(got, "browser_navigate") {
		t.Error("manifest missing browser_navigate")
	}
	// Full and infra tools must not appear as manifest ENTRIES (indented bullet lines).
	if strings.Contains(got, "  - read_file") {
		t.Error("manifest must NOT contain full-tier tool read_file as an entry")
	}
	if strings.Contains(got, "  - ToolSearch") {
		t.Error("manifest must NOT contain infra tool 'ToolSearch' as an entry")
	}
	// The manifest header prose uses the new tool name.
	if strings.Contains(got, "action='load'") {
		t.Error("manifest header must NOT mention \"action='load'\" (removed: no action param)")
	}
	if !strings.Contains(got, "ToolSearch") {
		t.Errorf("manifest header must mention 'ToolSearch'; got: %s", got)
	}
	if !strings.Contains(got, "names") || !strings.Contains(got, "query") {
		t.Errorf("manifest header must mention 'names' and 'query' (new param-inferred wording); got: %s", got)
	}
}

func TestBuildCompressedManifest_ExcludesLoaded(t *testing.T) {
	toolList := []Tool{
		&fakeManifestTool{name: "create_agent", desc: "Create a new agent.", cat: CategoryAgents},
		&fakeManifestTool{name: "list_agents", desc: "List agents.", cat: CategoryAgents},
	}
	loaded := map[string]bool{"create_agent": true}
	got := BuildCompressedManifest(toolList, loaded)

	if strings.Contains(got, "create_agent") {
		t.Error("manifest must not include already-loaded tool create_agent")
	}
	if !strings.Contains(got, "list_agents") {
		t.Error("manifest must still include unloaded list_agents")
	}
}

func TestBuildCompressedManifest_EmptyWhenAllExcluded(t *testing.T) {
	// Only full and infra tools — manifest must be empty.
	toolList := []Tool{
		&fakeManifestTool{name: "read_file", desc: "Read.", cat: CategoryFilesystem},
		&fakeManifestTool{name: "ToolSearch", desc: "Discover and load tools.", cat: CategoryToolDiscovery},
	}
	got := BuildCompressedManifest(toolList, nil)
	if got != "" {
		t.Errorf("BuildCompressedManifest should return empty string when no lazy tools remain, got: %q", got)
	}
}

func TestBuildCompressedManifest_EmptyInput(t *testing.T) {
	got := BuildCompressedManifest(nil, nil)
	if got != "" {
		t.Errorf("BuildCompressedManifest(nil, nil) should return \"\", got %q", got)
	}
	got = BuildCompressedManifest([]Tool{}, map[string]bool{})
	if got != "" {
		t.Errorf("BuildCompressedManifest([], {}) should return \"\", got %q", got)
	}
}

func TestBuildCompressedManifest_GroupsByCategory(t *testing.T) {
	toolList := []Tool{
		&fakeManifestTool{name: "z_agents_tool", desc: "Agents desc.", cat: CategoryAgents},
		&fakeManifestTool{name: "a_browser_tool", desc: "Browser desc.", cat: CategoryBrowser},
	}
	got := BuildCompressedManifest(toolList, nil)

	agentsIdx := strings.Index(got, "## agents")
	browserIdx := strings.Index(got, "## browser")
	if agentsIdx == -1 {
		t.Error("manifest missing ## agents section")
	}
	if browserIdx == -1 {
		t.Error("manifest missing ## browser section")
	}
	// agents < browser lexicographically, so agents section comes first.
	if agentsIdx > browserIdx {
		t.Errorf("categories not sorted: agents index %d > browser index %d", agentsIdx, browserIdx)
	}
}

func TestBuildCompressedManifest_SortedNamesWithinCategory(t *testing.T) {
	toolList := []Tool{
		&fakeManifestTool{name: "z_tool", desc: "Z tool.", cat: CategoryAgents},
		&fakeManifestTool{name: "a_tool", desc: "A tool.", cat: CategoryAgents},
		&fakeManifestTool{name: "m_tool", desc: "M tool.", cat: CategoryAgents},
	}
	got := BuildCompressedManifest(toolList, nil)

	aIdx := strings.Index(got, "a_tool")
	mIdx := strings.Index(got, "m_tool")
	zIdx := strings.Index(got, "z_tool")
	if aIdx == -1 || mIdx == -1 || zIdx == -1 {
		t.Fatalf("manifest missing expected tools: %q", got)
	}
	if aIdx >= mIdx || mIdx >= zIdx {
		t.Errorf("tools within category not sorted: a=%d m=%d z=%d in:\n%s", aIdx, mIdx, zIdx, got)
	}
}

func TestBuildCompressedManifest_TruncatesMultiLineDescription(t *testing.T) {
	toolList := []Tool{
		&fakeManifestTool{
			name: "multi_line_tool",
			desc: "First line only.\nThis second line must NOT appear.",
			cat:  CategoryAgents,
		},
	}
	got := BuildCompressedManifest(toolList, nil)

	if strings.Contains(got, "second line must NOT appear") {
		t.Error("manifest must truncate multi-line description to first line only")
	}
	if !strings.Contains(got, "First line only.") {
		t.Error("manifest missing first line of description")
	}
}

func TestBuildCompressedManifest_TruncatesLongDescription(t *testing.T) {
	long := strings.Repeat("x", 200)
	toolList := []Tool{
		&fakeManifestTool{name: "long_tool", desc: long, cat: CategoryAgents},
	}
	got := BuildCompressedManifest(toolList, nil)

	// Find the entry line.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "long_tool") {
			// The description portion (after " — ") should be at most maxManifestLineLen chars.
			parts := strings.SplitN(line, " — ", 2)
			if len(parts) == 2 && len(parts[1]) > maxManifestLineLen {
				t.Errorf("description not truncated: len=%d > maxManifestLineLen=%d", len(parts[1]), maxManifestLineLen)
			}
			return
		}
	}
	t.Error("long_tool not found in manifest")
}

func TestBuildCompressedManifest_Deterministic(t *testing.T) {
	// Same input produces the same output on repeated calls.
	toolList := []Tool{
		&fakeManifestTool{name: "b_tool", desc: "B.", cat: CategoryBrowser},
		&fakeManifestTool{name: "a_tool", desc: "A.", cat: CategoryAgents},
		&fakeManifestTool{name: "c_tool", desc: "C.", cat: CategoryAgents},
	}
	first := BuildCompressedManifest(toolList, nil)
	second := BuildCompressedManifest(toolList, nil)
	if first != second {
		t.Errorf("BuildCompressedManifest is not deterministic:\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestBuildCompressedManifest_AllLoadedReturnsEmpty(t *testing.T) {
	toolList := []Tool{
		&fakeManifestTool{name: "create_agent", desc: "Create.", cat: CategoryAgents},
		&fakeManifestTool{name: "list_agents", desc: "List.", cat: CategoryAgents},
	}
	loaded := map[string]bool{"create_agent": true, "list_agents": true}
	got := BuildCompressedManifest(toolList, loaded)
	if got != "" {
		t.Errorf("all tools loaded: expected empty manifest, got %q", got)
	}
}

// scopeCoreFullTierTools is the set of full-tier tools that are ScopeCore sysagent
// tools registered per-agent via the sysagent layer (pkg/sysagent/tools/), NOT via
// GeneralBuiltinMetadata(). They are absent from the general builtin catalog by
// design — the catalog is a general-purpose capability reference; ScopeCore tools
// are wired into the agent loop via registerCoreAgentSysTools and related helpers.
// Listing them here keeps TestManifestNamesResolveInCatalog honest about the
// distinction without requiring a circular import from pkg/tools → pkg/sysagent.
var scopeCoreFullTierTools = map[string]bool{
	"navigate": true, // pkg/sysagent/tools/navigate.go — ScopeCore, registered by wireExecToolDeps
}

// TestManifestNamesResolveInCatalog guards against the silent-rename class of
// regression (the §7 tool-rename bug): if a full- or infra-tier tool is renamed
// without updating the manifest name maps, ToolManifestTier silently demotes it
// to lazy (full tools) or breaks force-include (infra tools). This test fails
// loudly when a manifest name no longer corresponds to a registered builtin.
//
// ScopeCore tools (e.g. navigate) are exempt from the GeneralBuiltinMetadata check
// because they are registered via the sysagent layer, not the general builtin
// catalog. They are listed in scopeCoreFullTierTools above.
func TestManifestNamesResolveInCatalog(t *testing.T) {
	present := make(map[string]bool)
	for _, tool := range GeneralBuiltinMetadata() {
		present[tool.Name()] = true
	}
	for _, name := range FullManifestToolNames() {
		if scopeCoreFullTierTools[name] {
			// ScopeCore tool registered via sysagent layer, not GeneralBuiltinMetadata.
			// Verify it is listed in scopeCoreFullTierTools to keep the exemption explicit.
			continue
		}
		if !present[name] {
			t.Errorf(
				"full-tier manifest tool %q is not a registered builtin (renamed or removed?) — update fullManifestToolNames or add to scopeCoreFullTierTools if it is a ScopeCore sysagent tool",
				name,
			)
		}
	}
	for _, name := range InfraManifestToolNames() {
		if !present[name] {
			t.Errorf(
				"infra-tier manifest tool %q is not a registered builtin (renamed or removed?) — update infraManifestToolNames",
				name,
			)
		}
	}
}

// TestManifestTier_PromotedTools_C2 is test C2 from the tier-promotion fix:
// assert that the four everyday tools promoted from Lazy to Full-tier are
// correctly classified, and that the infra loader is ManifestInfra (not Full).
// This is the coverage gap that let the original bug slip: no test explicitly
// asserted these four names were Full-tier before they were promoted.
func TestManifestTier_PromotedTools_C2(t *testing.T) {
	// The four tools promoted to Full-tier by the "everyday tools" fix.
	fullTierExpected := []string{
		"navigate",
		"create_task",
		"list_tasks",
		"update_task",
	}
	for _, name := range fullTierExpected {
		got := ToolManifestTier(name)
		if got != ManifestFull {
			t.Errorf("C2: ToolManifestTier(%q) = %v, want ManifestFull (promoted tool)", name, got)
		}
		if !IsFullManifestTool(name) {
			t.Errorf("C2: IsFullManifestTool(%q) = false, want true (promoted tool)", name)
		}
	}

	// The infra loader must be ManifestInfra (not Full, not Lazy).
	got := ToolManifestTier("ToolSearch")
	if got != ManifestInfra {
		t.Errorf("C2: ToolManifestTier(\"ToolSearch\") = %v, want ManifestInfra", got)
	}
	if IsFullManifestTool("ToolSearch") {
		t.Error("C2: IsFullManifestTool(\"ToolSearch\") = true, want false (it is ManifestInfra, not Full)")
	}

	// Confirm none of the four was accidentally also infra.
	for _, name := range fullTierExpected {
		if ToolManifestTier(name) == ManifestInfra {
			t.Errorf("C2: ToolManifestTier(%q) = ManifestInfra, want ManifestFull", name)
		}
	}
}

// TestInfraManifestToolNames_Set asserts the infra accessor returns the expected
// sorted set (single source of truth consumed by the loop's force-include).
// The set is just {"ToolSearch"}.
func TestInfraManifestToolNames_Set(t *testing.T) {
	got := InfraManifestToolNames()
	want := []string{"ToolSearch"}
	if len(got) != len(want) {
		t.Fatalf("InfraManifestToolNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("InfraManifestToolNames()[%d] = %q, want %q", i, got[i], want[i])
		}
		if ToolManifestTier(got[i]) != ManifestInfra {
			t.Errorf("ToolManifestTier(%q) != ManifestInfra", got[i])
		}
	}
}
