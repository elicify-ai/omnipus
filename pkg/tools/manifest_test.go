// Omnipus — manifest classification + builder tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
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

// TestToolManifestTier_FullSetExact pins the ADR-071 D3 §4.1 Tier 1 (always-
// listed) membership — 17 names, transcribed verbatim from the ADR, not
// re-derived. See "Tier membership: one source of truth" in the D3 spec: a
// count-only check (18-3+2 == 18-6+5 == 17) is NOT sufficient verification —
// it must be the exact set. bash/navigate/create_task/update_task left this
// set for the new previewed tier (Tier 2); list_mounts/send_file/
// message_parent/recall_conversation joined; switch_agent is the D4 merge
// target. hand_off/return_to_default no longer exist; navigate was later
// retired outright (total no-op, callback always nil in production) rather
// than merely demoted.
func TestToolManifestTier_FullSetExact(t *testing.T) {
	specFull := []string{
		"read_file", "write_file", "edit_file", "list_directory",
		"list_mounts", "search_web", "fetch_url", "send_message",
		"switch_agent", "send_file", "message_parent",
		"remember", "recall_memory", "recall_conversation", "set_todos",
		"list_tasks", "delegate",
	}
	for _, n := range specFull {
		if ToolManifestTier(n) != ManifestFull {
			t.Errorf("ToolManifestTier(%q) should be ManifestFull per ADR-071 D3 §4.1", n)
		}
	}
	// FullManifestToolNames must contain EXACTLY the ADR set — both directions.
	got := FullManifestToolNames()
	if len(got) != len(specFull) {
		t.Fatalf("FullManifestToolNames() len = %d, want %d (got %v)", len(got), len(specFull), got)
	}
	wantSet := make(map[string]bool, len(specFull))
	for _, n := range specFull {
		wantSet[n] = true
	}
	for _, n := range got {
		if !wantSet[n] {
			t.Errorf("FullManifestToolNames() contains unexpected name %q not in ADR-071 D3 §4.1's Tier 1 list", n)
		}
	}
	// Demoted names must NOT be Full any more.
	for _, n := range []string{"bash", "create_task", "update_task"} {
		if ToolManifestTier(n) == ManifestFull {
			t.Errorf("ToolManifestTier(%q) = ManifestFull, want ManifestLazy — ADR-071 D3 demoted it to previewed", n)
		}
	}
	// Retired names must not resolve to anything meaningful — they no longer exist.
	for _, n := range []string{"hand_off", "return_to_default", "navigate"} {
		if ToolManifestTier(n) == ManifestFull {
			t.Errorf("ToolManifestTier(%q) = ManifestFull, but this tool was retired", n)
		}
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
	// Note: list_tasks/delegate are ManifestFull and must NOT appear in this
	// list. bash/create_task/update_task were ManifestFull before ADR-071
	// D3 — they are now ManifestLazy (previewed tier) and ARE included below,
	// deliberately, as regression coverage for the demotion. navigate was a
	// fourth member of that same demotion but was later retired outright
	// rather than merely demoted, so it is no longer a real tool name and is
	// not sampled here.
	lazySample := []string{
		"create_agent",
		"browser_navigate",
		"send_email",
		"list_agents",
		"browser_screenshot",
		"install_skill",
		"workspace_shell",
		"bash",
		"create_task",
		"update_task",
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

// withPreviewAllLazy sets the ADR-071 §4.3.1(b) revert flag for the duration
// of a test and restores it afterward. Several pre-existing tests here use
// synthetic tool names (e.g. "z_agents_tool", "long_tool") to exercise
// BuildCompressedManifest's generic rendering mechanics — grouping, sorting,
// truncation — independent of which ADR-071 D3 tier a real tool name belongs
// to. Since those synthetic names default to ManifestSearchOnly (invisible)
// under the three-tier split, this restores the pre-D3 "every lazy tool is
// previewed" behavior so the mechanics under test are exercised exactly as
// they were before D3, without hardcoding a real Tier 2 name that could
// itself drift.
func withPreviewAllLazy(t *testing.T) {
	t.Helper()
	SetPreviewAllLazy(true)
	t.Cleanup(func() { SetPreviewAllLazy(false) })
}

func TestBuildCompressedManifest_Basic(t *testing.T) {
	withPreviewAllLazy(t)
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
	withPreviewAllLazy(t)
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
	withPreviewAllLazy(t)
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
	withPreviewAllLazy(t)
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
	withPreviewAllLazy(t)
	long := strings.Repeat("x", 200)
	toolList := []Tool{
		&fakeManifestTool{name: "long_tool", desc: long, cat: CategoryAgents},
	}
	got := BuildCompressedManifest(toolList, nil)

	// Find the entry line.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "long_tool") {
			// The description portion (after " — ") should be at most
			// maxManifestLineLen RUNES (not bytes — the truncation marker is a
			// multi-byte "…", so a byte-length check would spuriously fail).
			parts := strings.SplitN(line, " — ", 2)
			if len(parts) != 2 {
				t.Fatalf("could not split entry line on \" — \": %q", line)
			}
			if runeLen := utf8.RuneCountInString(parts[1]); runeLen > maxManifestLineLen {
				t.Errorf("description not truncated: rune len=%d > maxManifestLineLen=%d", runeLen, maxManifestLineLen)
			}
			if !strings.HasSuffix(parts[1], "...") {
				t.Errorf("truncated description missing truncation marker \"...\": %q", parts[1])
			}
			// The original 200 'x' characters must not appear unbroken — proves
			// the text was actually cut, not just marker-appended.
			if strings.Contains(parts[1], long) {
				t.Error("description was not truncated at all (full 200-char string present)")
			}
			return
		}
	}
	t.Error("long_tool not found in manifest")
}

// TestBuildCompressedManifest_TruncationDoesNotSplitMultiByteRune proves the
// UTF-8 safety half of the manifest-preview truncation fix: a description
// whose maxManifestLineLen-th rune boundary falls inside a multi-byte
// character (an em-dash, common in this codebase's descriptions) must not be
// cut mid-codepoint. A raw byte slice (the pre-fix behavior) would produce
// invalid UTF-8 here.
func TestBuildCompressedManifest_TruncationDoesNotSplitMultiByteRune(t *testing.T) {
	withPreviewAllLazy(t)
	// 139 ASCII runes + an em-dash at rune index 139 (0-based), then more
	// ASCII text — the em-dash straddles byte offset 139 (a 3-byte UTF-8
	// sequence), which is exactly where a byte-slice[:140] would cut it.
	desc := strings.Repeat("x", 139) + "—" + strings.Repeat("y", 50)
	toolList := []Tool{
		&fakeManifestTool{name: "utf8_tool", desc: desc, cat: CategoryAgents},
	}
	got := BuildCompressedManifest(toolList, nil)

	if !utf8.ValidString(got) {
		t.Fatal("BuildCompressedManifest produced invalid UTF-8")
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "utf8_tool") {
			parts := strings.SplitN(line, " — ", 2)
			if len(parts) != 2 {
				t.Fatalf("could not split entry line on \" — \": %q", line)
			}
			if !utf8.ValidString(parts[1]) {
				t.Errorf("truncated description is invalid UTF-8: %q", parts[1])
			}
			return
		}
	}
	t.Error("utf8_tool not found in manifest")
}

// TestVisibility_PreviewedDescriptionsFitWithoutTruncation is the contract
// this fix places on description authors (per the manifest-preview
// truncation fix): every Tier-2 (previewed) tool's Description() first line
// (the text up to the first '\n', or the whole string if there is none) must
// fit within maxManifestLineLen runes WITHOUT truncation. A tool that fails
// this must be given a short, self-contained opening line (with the fuller
// detail moved after a '\n') rather than relying on the manifest builder to
// silently cut it.
func TestVisibility_PreviewedDescriptionsFitWithoutTruncation(t *testing.T) {
	byName := make(map[string]Tool, len(GeneralBuiltinMetadata()))
	for _, tool := range GeneralBuiltinMetadata() {
		byName[tool.Name()] = tool
	}
	for _, name := range PreviewedLazyToolNames() {
		tool, ok := byName[name]
		if !ok {
			// ScopeCore tools (e.g. get_workspace) aren't in the general
			// builtin catalog — covered separately by
			// TestManifestNamesResolveInCatalog's scopeCoreFullTierTools
			// exemption. Skip here; nothing to check without an instance.
			continue
		}
		raw := tool.Description()
		line := raw
		if idx := strings.IndexByte(raw, '\n'); idx >= 0 {
			line = raw[:idx]
		}
		line = strings.TrimSpace(line)
		if n := utf8.RuneCountInString(line); n > maxManifestLineLen {
			t.Errorf(
				"previewed tool %q Description() first line is %d runes (> %d) and will be silently truncated in the manifest preview; give it a short, self-contained opening line: %q",
				name, n, maxManifestLineLen, line,
			)
		}
	}
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

// scopeCoreFullTierTools is the set of tools (originally all Full-tier, hence
// the name predating ADR-071 D3 — now also covering ScopeCore Tier 2 names)
// that are ScopeCore sysagent tools registered per-agent via the sysagent
// layer (pkg/sysagent/tools/), NOT via GeneralBuiltinMetadata(). They are
// absent from the general builtin catalog by design — the catalog is a
// general-purpose capability reference; ScopeCore tools are wired into the
// agent loop via registerCoreAgentSysTools and related helpers. Listing them
// here keeps TestManifestNamesResolveInCatalog honest about the distinction
// without requiring a circular import from pkg/tools → pkg/sysagent.
var scopeCoreFullTierTools = map[string]bool{
	"get_workspace": true, // pkg/sysagent/tools/workspace.go — ScopeCore (ADR-071 D3: now Tier 2, previewed)
}

// TestManifestNamesResolveInCatalog guards against the silent-rename class of
// regression (the §7 tool-rename bug): if a full- or infra-tier tool is renamed
// without updating the manifest name maps, ToolManifestTier silently demotes it
// to lazy (full tools) or breaks force-include (infra tools). This test fails
// loudly when a manifest name no longer corresponds to a registered builtin.
//
// ScopeCore tools (e.g. get_workspace) are exempt from the GeneralBuiltinMetadata
// check because they are registered via the sysagent layer, not the general
// builtin catalog. They are listed in scopeCoreFullTierTools above.
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
	// ADR-071 D3: the same silent-rename hazard applies to the previewed
	// (Tier 2) set — get_workspace is exempt for the same ScopeCore reason as above.
	for _, name := range PreviewedLazyToolNames() {
		if scopeCoreFullTierTools[name] {
			continue
		}
		if !present[name] {
			t.Errorf(
				"previewed-tier manifest tool %q is not a registered builtin (renamed or removed?) — update previewedLazyToolNames or add to scopeCoreFullTierTools if it is a ScopeCore sysagent tool",
				name,
			)
		}
	}
}

// TestManifestTier_PromotedTools_C2 was test C2 from the original "everyday
// tools" tier-promotion fix, which put navigate/create_task/list_tasks/
// update_task all into Full-tier. ADR-071 D3 §4.1/§4.2 SPLIT that quartet:
// list_tasks stays Full (a read the agent needs to orient itself); navigate,
// create_task and update_task move to the new previewed lazy tier (Tier 2),
// so `delegate` keeps a wider Full-tier visibility margin over the
// task-mutation verbs per ADR-053's measured ordering, and bash's permanent
// visibility advantage is removed. Rewritten (not deleted) to pin the SPLIT.
// navigate was later retired outright rather than merely demoted, so it is
// no longer sampled below — it is not a real tool name any more and
// ToolManifestVisibility("navigate") would
// now resolve to ManifestSearchOnly (the default for any unrecognized lazy
// name), not ManifestPreviewed.
func TestManifestTier_PromotedTools_C2(t *testing.T) {
	// list_tasks is the one member of the original quartet that stays Full.
	if got := ToolManifestTier("list_tasks"); got != ManifestFull {
		t.Errorf("C2/D3: ToolManifestTier(\"list_tasks\") = %v, want ManifestFull", got)
	}
	if !IsFullManifestTool("list_tasks") {
		t.Error("C2/D3: IsFullManifestTool(\"list_tasks\") = false, want true")
	}

	// create_task/update_task were demoted to previewed lazy by D3 (navigate
	// was the quartet's fourth member but is now retired, not merely demoted).
	demoted := []string{"create_task", "update_task"}
	for _, name := range demoted {
		if got := ToolManifestTier(name); got != ManifestLazy {
			t.Errorf("C2/D3: ToolManifestTier(%q) = %v, want ManifestLazy (demoted by ADR-071 D3)", name, got)
		}
		if IsFullManifestTool(name) {
			t.Errorf("C2/D3: IsFullManifestTool(%q) = true, want false (demoted by ADR-071 D3)", name)
		}
		if got := ToolManifestVisibility(name); got != ManifestPreviewed {
			t.Errorf("C2/D3: ToolManifestVisibility(%q) = %v, want ManifestPreviewed", name, got)
		}
	}

	// bash is the fifth D3 demotion (was already Full before this fix existed,
	// unrelated to the original C2 quartet, but demoted by the same D3 change).
	if got := ToolManifestTier("bash"); got != ManifestLazy {
		t.Errorf("C2/D3: ToolManifestTier(\"bash\") = %v, want ManifestLazy (demoted by ADR-071 D3)", got)
	}
	if got := ToolManifestVisibility("bash"); got != ManifestPreviewed {
		t.Errorf("C2/D3: ToolManifestVisibility(\"bash\") = %v, want ManifestPreviewed", got)
	}

	// The infra loader must be ManifestInfra (not Full, not Lazy).
	got := ToolManifestTier("ToolSearch")
	if got != ManifestInfra {
		t.Errorf("C2: ToolManifestTier(\"ToolSearch\") = %v, want ManifestInfra", got)
	}
	if IsFullManifestTool("ToolSearch") {
		t.Error("C2: IsFullManifestTool(\"ToolSearch\") = true, want false (it is ManifestInfra, not Full)")
	}

	// Confirm none of the demoted names was accidentally also infra.
	for _, name := range append(append([]string{}, demoted...), "bash", "list_tasks") {
		if ToolManifestTier(name) == ManifestInfra {
			t.Errorf("C2: ToolManifestTier(%q) = ManifestInfra, want otherwise", name)
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

// ── ADR-071 D3 drift tests ──────────────────────────────────────────────────
//
// These four tests pin the exact Tier 1/2/3 membership so any future silent
// change — a tool added without a recorded exposure level, a name moved
// between tiers without a deliberate decision — is caught at build time
// (FR-034). Per the spec's "Tier membership: one source of truth" rule, the
// literal name lists below are transcribed from ADR-071 §4.1, never
// re-derived from a count.

// tier3SearchOnlyToolNames is ADR-071 §4.1's literal Tier 3 list, transcribed
// verbatim, now 68 names: 62 after write_agent_metadata's retirement, plus
// ADR-072 D2's five new browser tools and Stream C's browser_handle_dialog (a
// redundant, unguarded second door onto
// the same files update_agent already writes through a properly-guarded
// path — see pkg/sysagent/tools/metadata.go). It exists ONLY as the third leg
// of the arithmetic check below — pkg/tools has no other reason to enumerate
// Tier 3 by name, since search-only tools resolve to ManifestSearchOnly by
// DEFAULT (everything lazy that isn't in previewedLazyToolNames), not by
// membership in an explicit set.
var tier3SearchOnlyToolNames = []string{
	"append_file", "library_list", "library_read", "request_mount", "find_skills",
	"install_skill", "browser_navigate", "browser_click", "browser_type", "browser_screenshot",
	"browser_get_text", "browser_wait", "browser_evaluate", "browser_list_tabs", "browser_switch_tab",
	"browser_close_tab", "browser_open_tab",
	// ADR-072 D2 (ADR D1.9b ruling 3) — all five new browser tools are Tier 3
	// search-only, alongside the other eleven. browser_upload_file is here
	// even though FR-029 holds its registration: the tier sets are about
	// manifest VISIBILITY of a catalog name, not about registration.
	"browser_select_option", "browser_press_key", "browser_hover", "browser_snapshot",
	"browser_upload_file",
	// ADR-072 D2 Stream C — the dialog recovery verb, Tier 3 like the rest of
	// the browser surface.
	"browser_handle_dialog",
	"create_workspace", "update_workspace", "delete_workspace",
	"list_workspaces", "read_agent_metadata", "configure_provider",
	"list_providers", "test_provider", "list_models", "run_doctor", "get_usage", "add_mcp_server",
	"remove_mcp_server", "list_mcp_servers", "create_skill", "edit_skill", "create_task_in_workspace",
	"update_task_in_workspace", "delete_task_in_workspace", "list_tasks_in_workspace", "remove_skill",
	"list_skills", "enable_channel", "configure_channel", "disable_channel", "list_channels",
	"test_channel", "get_config", "set_config", "create_agent", "update_agent", "delete_agent",
	"create_plan", "execute_plan", "run_task", "inspect_session", "plan_correct", "stop_plan",
	"run_retrospective", "read_inbox", "search_email", "read_message", "send_email", "reply",
	"delete_task",
}

// TestVisibility_TierArithmetic pins the full 17+7+68+1=93 partition (FR-032:
// navigate's retirement dropped the previewed set from 8 to 7, and
// write_agent_metadata's retirement dropped the search-only set from 63 to
// 62, and ADR-072 D2 raised it from 62 to 68 (five interaction/snapshot
// verbs plus browser_handle_dialog) — "The
// always-listed set MUST contain exactly 17 names, the previewed set exactly
// 7, the search-only set exactly 68, and the infrastructure set exactly 1"). Counts
// alone are NOT verification (two different 6-out/5-in vs 3-out/2-in diffs
// both land on 17) — this test additionally proves the four sets are
// pairwise disjoint and that every Tier 3 name resolves to ManifestLazy +
// ManifestSearchOnly (never ManifestPreviewed, never present in
// fullManifestToolNames).
func TestVisibility_TierArithmetic(t *testing.T) {
	full := FullManifestToolNames()
	previewed := PreviewedLazyToolNames()
	infra := InfraManifestToolNames()

	if len(full) != 17 {
		t.Errorf("len(FullManifestToolNames()) = %d, want 17; got %v", len(full), full)
	}
	if len(previewed) != 7 {
		t.Errorf("len(PreviewedLazyToolNames()) = %d, want 7; got %v", len(previewed), previewed)
	}
	if len(infra) != 1 {
		t.Errorf("len(InfraManifestToolNames()) = %d, want 1; got %v", len(infra), infra)
	}
	if len(tier3SearchOnlyToolNames) != 68 {
		t.Fatalf("tier3SearchOnlyToolNames has %d entries, want 68 — fixture defect, fix the test data",
			len(tier3SearchOnlyToolNames))
	}

	seen := make(map[string]string, 93) // name -> which set it was first seen in
	record := func(setName string, names []string) {
		for _, n := range names {
			if prior, ok := seen[n]; ok {
				t.Errorf("name %q appears in both %q and %q — the four sets must be pairwise disjoint", n, prior, setName)
				continue
			}
			seen[n] = setName
		}
	}
	record("full", full)
	record("previewed", previewed)
	record("infra", infra)
	record("search-only", tier3SearchOnlyToolNames)

	if len(seen) != 93 {
		t.Errorf("union of all four sets has %d unique names, want 93", len(seen))
	}

	// Every Tier 3 name must resolve to ManifestLazy + ManifestSearchOnly.
	for _, n := range tier3SearchOnlyToolNames {
		if got := ToolManifestTier(n); got != ManifestLazy {
			t.Errorf("ToolManifestTier(%q) = %v, want ManifestLazy (Tier 3 search-only)", n, got)
		}
		if got := ToolManifestVisibility(n); got != ManifestSearchOnly {
			t.Errorf("ToolManifestVisibility(%q) = %v, want ManifestSearchOnly", n, got)
		}
		if IsFullManifestTool(n) {
			t.Errorf("IsFullManifestTool(%q) = true, want false (Tier 3 name must never be Full)", n)
		}
	}
}

// TestVisibility_PreviewedSetIsExactlySeven pins ADR-071 §4.1's literal Tier 2
// list — the 7 names that still render a preview line, transcribed verbatim,
// not re-derived from a count (FR-034, matching "Tier membership: one source
// of truth"). Originally 8; navigate was retired outright (total no-op, its
// callback was nil in every production path, so nothing anywhere could ever
// receive a navigation event), dropping the set to 7.
func TestVisibility_PreviewedSetIsExactlySeven(t *testing.T) {
	want := []string{
		"list_agents", "list_jobs", "serve_web",
		"get_workspace", "bash", "create_task", "update_task",
	}
	got := PreviewedLazyToolNames()
	if len(got) != len(want) {
		t.Fatalf("PreviewedLazyToolNames() = %v (len %d), want %v (len %d)", got, len(got), want, len(want))
	}
	wantSet := make(map[string]bool, len(want))
	for _, n := range want {
		wantSet[n] = true
	}
	for _, n := range got {
		if !wantSet[n] {
			t.Errorf("PreviewedLazyToolNames() contains unexpected name %q", n)
		}
		if ToolManifestTier(n) != ManifestLazy {
			t.Errorf("previewed name %q must be ManifestLazy, got %v", n, ToolManifestTier(n))
		}
		if ToolManifestVisibility(n) != ManifestPreviewed {
			t.Errorf("previewed name %q must resolve to ManifestPreviewed", n)
		}
	}
}

// TestVisibility_SearchOnlyToolsRemainInSearchIndex proves the load-bearing
// property §4.4 exists to protect: a search-only (Tier 3) tool remains a
// member of the BM25 search corpus SnapshotSearchableTools builds, because
// that corpus admits on ToolManifestTier(name)==ManifestLazy alone — the
// second axis (ManifestVisibility) must never leak into that decision, or
// every Tier 3 tool silently vanishes from the only channel by which it is
// reachable. This is a STATIC membership property (FR-031): what the index
// contains, asserted without running a search.
func TestVisibility_SearchOnlyToolsRemainInSearchIndex(t *testing.T) {
	reg := NewToolRegistry()
	for _, n := range tier3SearchOnlyToolNames {
		reg.Register(&fakeManifestTool{name: n, desc: "search-only tool " + n, cat: CategorySystem})
	}
	// A previewed tool too, as a control — it must ALSO be in the index
	// (Tier 2 is still ManifestLazy, still searchable; only its manifest-block
	// PREVIEW LINE differs from Tier 3).
	reg.Register(&fakeManifestTool{name: "bash", desc: "shell", cat: CategoryShell})
	// A full-tier tool, as a negative control — must NOT be in the index.
	reg.Register(&fakeManifestTool{name: "delegate", desc: "delegate", cat: CategoryDelegation})

	snap := reg.SnapshotSearchableTools()
	indexed := make(map[string]bool, len(snap.Docs))
	for _, d := range snap.Docs {
		indexed[d.Name] = true
	}

	for _, n := range tier3SearchOnlyToolNames {
		if !indexed[n] {
			t.Errorf("search-only tool %q is missing from SnapshotSearchableTools' corpus — "+
				"it is now unreachable by ANY channel (not previewed, not searchable)", n)
		}
	}
	if !indexed["bash"] {
		t.Error("control: previewed tool \"bash\" must also be in the search corpus (it is still ManifestLazy)")
	}
	if indexed["delegate"] {
		t.Error("control: full-tier tool \"delegate\" must NOT be in the search corpus")
	}
}

// TestVisibility_EveryCatalogNameHasRecordedLevel mirrors the existing
// TestManifestNamesResolveInCatalog / TestCatalog_MatchesGlobalCeilingEntryForEntry
// drift-test pattern: every name GeneralBuiltinMetadata() returns must resolve
// to a DELIBERATELY recorded exposure level (Full, Previewed, or
// SearchOnly-by-omission-from-previewed — which is itself the deliberate
// default per ADR-071 §4.4), never silently fall through. Concretely: this
// test asserts no builtin name is BOTH in fullManifestToolNames AND in
// previewedLazyToolNames (which would be a contradictory double-classification
// undetectable by either drift test alone), and that every Full-tier name in
// the live catalog is a real registered builtin (already covered by
// TestManifestNamesResolveInCatalog, re-asserted here from the opposite
// direction: catalog -> classification, not classification -> catalog).
func TestVisibility_EveryCatalogNameHasRecordedLevel(t *testing.T) {
	fullSet := make(map[string]bool)
	for _, n := range FullManifestToolNames() {
		fullSet[n] = true
	}
	previewedSet := make(map[string]bool)
	for _, n := range PreviewedLazyToolNames() {
		if fullSet[n] {
			t.Errorf("%q is in BOTH fullManifestToolNames and previewedLazyToolNames — "+
				"contradictory double-classification", n)
		}
		previewedSet[n] = true
	}

	for _, tool := range GeneralBuiltinMetadata() {
		name := tool.Name()
		if name == "ToolSearch" {
			continue // infra, not subject to tier/visibility classification
		}
		tier := ToolManifestTier(name)
		switch tier {
		case ManifestFull:
			if !fullSet[name] {
				t.Errorf("catalog tool %q resolves ManifestFull but is not in FullManifestToolNames()", name)
			}
		case ManifestLazy:
			// Deliberate per ADR-071 §4.4: previewed if explicitly listed,
			// search-only by default otherwise. Both are "recorded" —
			// search-only's recording IS the absence from previewedLazyToolNames,
			// which is itself deliberate (not a silent fallthrough) because
			// FR-034 requires a build failure only for names missing from the
			// STATIC CATALOG entirely, not for lazy names defaulting to
			// search-only. Assert the visibility resolves to one of the two
			// valid values (never garbage) as the only thing left to check here.
			vis := ToolManifestVisibility(name)
			if vis != ManifestPreviewed && vis != ManifestSearchOnly {
				t.Errorf("catalog tool %q (ManifestLazy) resolves to invalid ManifestVisibility %v", name, vis)
			}
		case ManifestInfra:
			// Should not happen for a non-ToolSearch name; flag it if it does.
			t.Errorf("catalog tool %q unexpectedly resolves ManifestInfra", name)
		}
	}
}

// TestManifest_RenderedBlockIsNineteenLines proves FR-033's exact rendered
// line count for the real 7-tool, 5-category Tier 2 set: `2 + 2C + N` with
// C=5 categories and N=7 tools = 19 lines. Uses the REAL categories each of
// the 7 previewed tools resolves to in production (verified against source:
// list_agents→agents, list_jobs→tasks, serve_web→web, get_workspace→workspaces,
// bash→shell, create_task/update_task→tasks — 5 distinct categories).
// Originally 8 tools / 6 categories / 22 lines; navigate (→platform) was the
// dropped tool and platform was the dropped category, retired outright.
func TestManifest_RenderedBlockIsNineteenLines(t *testing.T) {
	toolList := []Tool{
		&fakeManifestTool{name: "list_agents", desc: "List agents.", cat: CategoryAgents},
		&fakeManifestTool{name: "list_jobs", desc: "List jobs.", cat: CategoryTasks},
		&fakeManifestTool{name: "serve_web", desc: "Serve web.", cat: CategoryWeb},
		&fakeManifestTool{name: "get_workspace", desc: "Get workspace.", cat: CategoryWorkspaces},
		&fakeManifestTool{name: "bash", desc: "Run a shell command.", cat: CategoryShell},
		&fakeManifestTool{name: "create_task", desc: "Create a task.", cat: CategoryTasks},
		&fakeManifestTool{name: "update_task", desc: "Update a task.", cat: CategoryTasks},
	}
	got := BuildCompressedManifest(toolList, nil)
	if got == "" {
		t.Fatal("BuildCompressedManifest returned empty string for the 7-tool previewed set")
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 19 {
		t.Errorf("rendered manifest block has %d lines, want 19 (FR-033: 2 + 2*5 + 7):\n%s", len(lines), got)
	}
	// Non-vacuous: no search-only tool must sneak into this rendering.
	for _, n := range tier3SearchOnlyToolNames[:5] { // sample, not the whole 67
		if strings.Contains(got, "  - "+n) {
			t.Errorf("19-line block must not contain search-only tool %q", n)
		}
	}
}
