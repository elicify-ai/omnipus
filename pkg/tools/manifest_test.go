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
		"exec", "search_web", "fetch_url", "send_message",
		"hand_off", "return_to_default", "remember", "recall_memory", "set_todos",
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
	if IsFullManifestTool("load_tool") {
		t.Error("IsFullManifestTool(load_tool) = true, want false (it is Infra)")
	}
}

func TestToolManifestTier_InfraSet(t *testing.T) {
	infraNames := []string{"load_tool", "search_tools_bm25", "search_tools_regex"}
	for _, n := range infraNames {
		got := ToolManifestTier(n)
		if got != ManifestInfra {
			t.Errorf("ToolManifestTier(%q) = %v, want ManifestInfra", n, got)
		}
	}
}

func TestToolManifestTier_LazySet(t *testing.T) {
	// Sample of tools that must be ManifestLazy per the spec.
	lazySample := []string{
		"create_agent",
		"browser_navigate",
		"create_task",
		"send_email",
		"navigate",
		"list_agents",
		"browser_screenshot",
		"install_skill",
		"workspace_shell",
		"spawn",
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
	tools := []Tool{
		// lazy tools in two categories
		&fakeManifestTool{name: "create_agent", desc: "Create a new agent.", cat: CategoryAgents},
		&fakeManifestTool{name: "list_agents", desc: "List agents.", cat: CategoryAgents},
		&fakeManifestTool{name: "browser_navigate", desc: "Navigate to a URL.", cat: CategoryBrowser},
		// full tool — must be excluded
		&fakeManifestTool{name: "read_file", desc: "Read a file.", cat: CategoryFilesystem},
		// infra tool — must be excluded
		&fakeManifestTool{name: "load_tool", desc: "Load tools.", cat: CategoryToolDiscovery},
		// search_tools infra — must be excluded
		&fakeManifestTool{name: "search_tools_bm25", desc: "BM25 search.", cat: CategoryToolDiscovery},
	}

	got := BuildCompressedManifest(tools, nil)

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
	// Note: "load_tool" appears in the header prose ("call load_tool with its name(s)"),
	// so we check for the entry format "  - load_tool" not bare substring.
	if strings.Contains(got, "  - read_file") {
		t.Error("manifest must NOT contain full-tier tool read_file as an entry")
	}
	if strings.Contains(got, "  - load_tool") {
		t.Error("manifest must NOT contain infra tool load_tool as an entry")
	}
	if strings.Contains(got, "  - search_tools_bm25") {
		t.Error("manifest must NOT contain infra tool search_tools_bm25 as an entry")
	}
}

func TestBuildCompressedManifest_ExcludesLoaded(t *testing.T) {
	tools := []Tool{
		&fakeManifestTool{name: "create_agent", desc: "Create a new agent.", cat: CategoryAgents},
		&fakeManifestTool{name: "list_agents", desc: "List agents.", cat: CategoryAgents},
	}
	loaded := map[string]bool{"create_agent": true}
	got := BuildCompressedManifest(tools, loaded)

	if strings.Contains(got, "create_agent") {
		t.Error("manifest must not include already-loaded tool create_agent")
	}
	if !strings.Contains(got, "list_agents") {
		t.Error("manifest must still include unloaded list_agents")
	}
}

func TestBuildCompressedManifest_EmptyWhenAllExcluded(t *testing.T) {
	// Only full and infra tools — manifest must be empty.
	tools := []Tool{
		&fakeManifestTool{name: "read_file", desc: "Read.", cat: CategoryFilesystem},
		&fakeManifestTool{name: "load_tool", desc: "Load.", cat: CategoryToolDiscovery},
	}
	got := BuildCompressedManifest(tools, nil)
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
	tools := []Tool{
		&fakeManifestTool{name: "z_agents_tool", desc: "Agents desc.", cat: CategoryAgents},
		&fakeManifestTool{name: "a_browser_tool", desc: "Browser desc.", cat: CategoryBrowser},
	}
	got := BuildCompressedManifest(tools, nil)

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
	tools := []Tool{
		&fakeManifestTool{name: "z_tool", desc: "Z tool.", cat: CategoryAgents},
		&fakeManifestTool{name: "a_tool", desc: "A tool.", cat: CategoryAgents},
		&fakeManifestTool{name: "m_tool", desc: "M tool.", cat: CategoryAgents},
	}
	got := BuildCompressedManifest(tools, nil)

	aIdx := strings.Index(got, "a_tool")
	mIdx := strings.Index(got, "m_tool")
	zIdx := strings.Index(got, "z_tool")
	if aIdx == -1 || mIdx == -1 || zIdx == -1 {
		t.Fatalf("manifest missing expected tools: %q", got)
	}
	if !(aIdx < mIdx && mIdx < zIdx) {
		t.Errorf("tools within category not sorted: a=%d m=%d z=%d in:\n%s", aIdx, mIdx, zIdx, got)
	}
}

func TestBuildCompressedManifest_TruncatesMultiLineDescription(t *testing.T) {
	tools := []Tool{
		&fakeManifestTool{
			name: "multi_line_tool",
			desc: "First line only.\nThis second line must NOT appear.",
			cat:  CategoryAgents,
		},
	}
	got := BuildCompressedManifest(tools, nil)

	if strings.Contains(got, "second line must NOT appear") {
		t.Error("manifest must truncate multi-line description to first line only")
	}
	if !strings.Contains(got, "First line only.") {
		t.Error("manifest missing first line of description")
	}
}

func TestBuildCompressedManifest_TruncatesLongDescription(t *testing.T) {
	long := strings.Repeat("x", 200)
	tools := []Tool{
		&fakeManifestTool{name: "long_tool", desc: long, cat: CategoryAgents},
	}
	got := BuildCompressedManifest(tools, nil)

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
	tools := []Tool{
		&fakeManifestTool{name: "b_tool", desc: "B.", cat: CategoryBrowser},
		&fakeManifestTool{name: "a_tool", desc: "A.", cat: CategoryAgents},
		&fakeManifestTool{name: "c_tool", desc: "C.", cat: CategoryAgents},
	}
	first := BuildCompressedManifest(tools, nil)
	second := BuildCompressedManifest(tools, nil)
	if first != second {
		t.Errorf("BuildCompressedManifest is not deterministic:\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestBuildCompressedManifest_AllLoadedReturnsEmpty(t *testing.T) {
	tools := []Tool{
		&fakeManifestTool{name: "create_agent", desc: "Create.", cat: CategoryAgents},
		&fakeManifestTool{name: "list_agents", desc: "List.", cat: CategoryAgents},
	}
	loaded := map[string]bool{"create_agent": true, "list_agents": true}
	got := BuildCompressedManifest(tools, loaded)
	if got != "" {
		t.Errorf("all tools loaded: expected empty manifest, got %q", got)
	}
}
