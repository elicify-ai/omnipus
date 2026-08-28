package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Dummy tool to fill the registry in our tests.
type mockSearchableTool struct {
	name string
	desc string
}

func (m *mockSearchableTool) Name() string        { return m.name }
func (m *mockSearchableTool) Description() string { return m.desc }
func (m *mockSearchableTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}

func (m *mockSearchableTool) Scope() ToolScope       { return ScopeGeneral }
func (m *mockSearchableTool) Category() ToolCategory { return CategoryCore }
func (m *mockSearchableTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	return SilentResult("mock executed: " + m.name)
}

// Helper to initialize a populated ToolRegistry
func setupPopulatedRegistry() *ToolRegistry {
	reg := NewToolRegistry()

	// A full-tier visible core tool — MUST NOT be found by BM25 searches
	// (full-tier tools are always callable; no need to discover them via BM25).
	// Using "read_file" which is ManifestFull per fullManifestToolNames.
	reg.Register(&mockSearchableTool{
		name: "read_file",
		desc: "Read the contents of a file from disk",
	})

	// Hidden tools (must be found by searches)
	reg.RegisterHidden(&mockSearchableTool{
		name: "mcp_read_file",
		desc: "Read the contents of a system file",
	})
	reg.RegisterHidden(&mockSearchableTool{
		name: "mcp_list_dir",
		desc: "List directories and files in the system",
	})
	reg.RegisterHidden(&mockSearchableTool{
		name: "mcp_fetch_net",
		desc: "Fetch data from a network database",
	})

	return reg
}

// newSearchTool builds a ToolsTool configured for search tests.
func newSearchTool(reg *ToolRegistry, ttl, maxN int) *ToolsTool {
	return NewToolsTool(reg, ttl, maxN)
}

// execQueryNoResolver calls tools{query:q} on a ToolsTool with no resolver set.
// The search runs but no auto-load can happen (returns match list without loaded/schemas).
func execQueryNoResolver(tt *ToolsTool, ctx context.Context, query string) *ToolResult {
	return tt.Execute(ctx, map[string]any{
		"query": query,
	})
}

func TestToolsTool_Query_EmptyQuery_Error(t *testing.T) {
	reg := setupPopulatedRegistry()
	tt := newSearchTool(reg, 3, 10)
	ctx := context.Background()

	for _, q := range []string{"", "   ", "\t"} {
		res := tt.Execute(ctx, map[string]any{"query": q})
		if !res.IsError {
			t.Errorf(
				"Empty/whitespace query %q must be rejected (neither names nor query provided), got: %v",
				q,
				res.ForLLM,
			)
		}
	}
}

func TestToolsTool_Query_NoMatch_SilentResult(t *testing.T) {
	reg := setupPopulatedRegistry()
	tt := newSearchTool(reg, 3, 10)
	ctx := context.Background()

	res := execQueryNoResolver(tt, ctx, "aliens spaceships")
	if res.IsError || !strings.Contains(res.ForLLM, "No tools found matching") {
		t.Errorf("Expected 'no tools found', got: %v", res.ForLLM)
	}
}

func TestToolsTool_Query_BM25_MatchesHiddenTool(t *testing.T) {
	reg := setupPopulatedRegistry()
	tt := newSearchTool(reg, 3, 10)
	ctx := context.Background()

	res := execQueryNoResolver(tt, ctx, "read files")
	if res.IsError {
		t.Fatalf("Unexpected error: %v", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "mcp_read_file") {
		t.Errorf("Expected 'mcp_read_file' in BM25 results")
	}
}

func TestToolsTool_Query_AutoLoadsTopHit(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_findme", desc: "find me with search"})

	// Wire a resolver so the auto-load path actually runs.
	var markedLoaded []string
	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(
		func(_ context.Context, name string) (bool, string) {
			if name == "mcp_findme" {
				return true, ""
			}
			return false, name + " — not available in test"
		},
		func(_ context.Context, names []string) (map[string]any, []string) {
			markedLoaded = append(markedLoaded, names...)
			sc := make(map[string]any, len(names))
			for _, n := range names {
				sc[n] = map[string]any{"name": n}
			}
			return sc, nil
		},
	)

	ctx := context.Background()
	res := tt.Execute(ctx, map[string]any{"query": "find me"})
	if res.IsError {
		t.Fatalf("query failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "mcp_findme") {
		t.Errorf("result must contain mcp_findme; got: %s", res.ForLLM)
	}

	// The response must contain 'loaded' key (auto-loaded top hit).
	if !strings.Contains(res.ForLLM, `"loaded"`) {
		t.Errorf("response must include 'loaded' field; got: %s", res.ForLLM)
	}

	// markLoaded must have been called for the top hit.
	found := false
	for _, n := range markedLoaded {
		if n == "mcp_findme" {
			found = true
		}
	}
	if !found {
		t.Errorf("markLoaded must have been called for mcp_findme; called with: %v", markedLoaded)
	}

	// The tool must now be Get-able (promoted by PromoteTools in the auto-load path).
	_, ok := reg.Get("mcp_findme")
	if !ok {
		t.Error("top-hit tool must be Get-able after auto-load (PromoteTools called)")
	}
}

func TestToolsTool_Query_DeniedTopHitFallsThrough(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_denied", desc: "denied tool"})
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_allowed", desc: "allowed tool"})

	// canLoad denies the first hit (mcp_denied) but allows mcp_allowed.
	var loadedName string
	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(
		func(_ context.Context, name string) (bool, string) {
			if name == "mcp_allowed" {
				return true, ""
			}
			return false, name + " — denied in test"
		},
		func(_ context.Context, names []string) (map[string]any, []string) {
			sc := make(map[string]any)
			for _, n := range names {
				sc[n] = map[string]any{"name": n}
				loadedName = n
			}
			return sc, nil
		},
	)

	ctx := context.Background()
	// Query that matches both tools but we care only that the allowed one eventually loads.
	res := tt.Execute(ctx, map[string]any{"query": "denied allowed tool"})
	if res.IsError {
		t.Fatalf("query unexpectedly failed: %s", res.ForLLM)
	}
	// Should not have loaded the denied one.
	if loadedName == "mcp_denied" {
		t.Errorf("denied top hit must be skipped; got loadedName=%s", loadedName)
	}
}

func TestToolsTool_Query_DoesNotPromote_WithoutResolver(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_findme", desc: "find me with search"})

	tt := NewToolsTool(reg, 5, 10)
	// No resolver set.

	ctx := context.Background()
	res := tt.Execute(ctx, map[string]any{"query": "find me"})
	if res.IsError {
		t.Fatalf("tools(query) failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "mcp_findme") {
		t.Errorf("tools(query) must return the matching tool name; got: %s", res.ForLLM)
	}

	// Tool must NOT be promoted (no resolver → no PromoteTools called).
	reg.mu.RLock()
	entry := reg.tools["mcp_findme"]
	reg.mu.RUnlock()
	if entry != nil && entry.TTL != 0 {
		t.Errorf("tools(query) without resolver must NOT promote (TTL must stay 0); got TTL=%d", entry.TTL)
	}

	// Tool must NOT be Get-able (not promoted).
	_, ok := reg.Get("mcp_findme")
	if ok {
		t.Error("tools(query) without resolver must NOT make the tool Get-able (no promote)")
	}
}

func TestSearchBM25_ZeroMaxResults(t *testing.T) {
	reg := setupPopulatedRegistry()

	res := reg.SearchBM25("read file", 0)
	if len(res) != 0 {
		t.Errorf("Expected 0 results with maxSearchResults=0, got %d", len(res))
	}
}

func TestToolRegistry_SearchBM25LimitsAndCoreFiltering(t *testing.T) {
	reg := NewToolRegistry()

	// Add 1 visible lazy tool (ManifestLazy tier — SHOULD appear in BM25),
	// 1 visible full-tier tool (ManifestFull — MUST NOT appear in BM25),
	// and 10 hidden tools.
	// "core_match_lazy" is not a real tool name so ToolManifestTier returns ManifestLazy.
	reg.Register(&mockSearchableTool{"core_match_lazy", "I am visible lazy with match"})
	// "read_file" is ManifestFull — must be excluded from BM25 corpus.
	reg.Register(&mockSearchableTool{"read_file", "Read file with match"})
	for i := 0; i < 10; i++ {
		reg.RegisterHidden(&mockSearchableTool{
			name: fmt.Sprintf("hidden_match_%d", i),
			desc: "this has a match",
		})
	}

	t.Run("BM25 limits and full-tier filtering", func(t *testing.T) {
		// Search with BM25 and a limit of maxSearchResults = 3.
		// The corpus now includes hidden tools AND visible lazy-tier tools.
		res := reg.SearchBM25("match", 3)

		if len(res) != 3 {
			t.Errorf("Expected exactly 3 results due to limit, got %d", len(res))
		}

		for _, r := range res {
			// Full-tier tools (ManifestFull) must NEVER appear in BM25 results
			// (they're always callable, no need to discover them via search).
			if r.Name == "read_file" {
				t.Errorf("SearchBM25 must not return full-tier tool %q (ManifestFull excluded from corpus)", r.Name)
			}
		}
		// core_match_lazy (visible, ManifestLazy) MAY appear — that's the new
		// widened behavior. We don't assert it must appear since BM25 ranking
		// may not surface it in the top-3 with 10 hidden competitors.
	})
}

func TestGet_HiddenToolTTLLifecycle(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "hidden_tool", desc: "test"})

	// TTL=0 at registration → not gettable
	_, ok := reg.Get("hidden_tool")
	if ok {
		t.Error("Expected hidden tool with TTL=0 to NOT be gettable")
	}

	// Promote → gettable
	reg.PromoteTools([]string{"hidden_tool"}, 3)
	_, ok = reg.Get("hidden_tool")
	if !ok {
		t.Error("Expected promoted hidden tool to be gettable")
	}

	// Tick down to 0 → not gettable again
	reg.TickTTL() // 3→2
	reg.TickTTL() // 2→1
	reg.TickTTL() // 1→0
	_, ok = reg.Get("hidden_tool")
	if ok {
		t.Error("Expected hidden tool with TTL ticked to 0 to NOT be gettable")
	}

	// Core tools remain always gettable
	reg.Register(&mockSearchableTool{name: "core_tool", desc: "core"})
	_, ok = reg.Get("core_tool")
	if !ok {
		t.Error("Expected core tool to always be gettable")
	}
}

func TestBM25CacheInvalidation(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "tool_alpha", desc: "alpha functionality"})

	tt := newSearchTool(reg, 5, 10)
	ctx := context.Background()

	// First search should find tool_alpha
	res := execQueryNoResolver(tt, ctx, "alpha")
	if !strings.Contains(res.ForLLM, "tool_alpha") {
		t.Fatalf("Expected 'tool_alpha' in first search, got: %v", res.ForLLM)
	}

	// Register a new hidden tool
	reg.RegisterHidden(&mockSearchableTool{name: "tool_beta", desc: "beta functionality"})

	// Cache should be invalidated; new tool should be findable
	res = execQueryNoResolver(tt, ctx, "beta")
	if !strings.Contains(res.ForLLM, "tool_beta") {
		t.Errorf("Expected 'tool_beta' after cache invalidation, got: %v", res.ForLLM)
	}
}

func TestPromoteTools_ConcurrentWithTickTTL(t *testing.T) {
	reg := NewToolRegistry()
	for i := 0; i < 20; i++ {
		reg.RegisterHidden(&mockSearchableTool{
			name: fmt.Sprintf("concurrent_tool_%d", i),
			desc: "concurrent test tool",
		})
	}

	names := make([]string, 20)
	for i := 0; i < 20; i++ {
		names[i] = fmt.Sprintf("concurrent_tool_%d", i)
	}

	// Hammer PromoteTools and TickTTL concurrently to detect races
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			reg.PromoteTools(names, 5)
		}
		close(done)
	}()

	for i := 0; i < 1000; i++ {
		reg.TickTTL()
	}
	<-done
}

// TestToolsTool_Query_ResponseContainsInstructions verifies that a query result
// with no resolver set tells the model how to load the tool by name.
func TestToolsTool_Query_ResponseContainsInstructions(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_foo", desc: "foo tool"})
	tt := newSearchTool(reg, 5, 10)
	ctx := context.Background()

	res := execQueryNoResolver(tt, ctx, "foo")
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "UNLOCKED") {
		t.Error("search response must not say 'UNLOCKED' (old auto-promote behavior removed)")
	}
	// Must tell model to use 'names' to load.
	if !strings.Contains(res.ForLLM, "names") {
		t.Errorf("query response must tell model to use 'names' to load; got: %s", res.ForLLM)
	}
}

// TestToolsTool_NamesWinsOverQuery proves names path wins when both are provided.
func TestToolsTool_NamesWinsOverQuery(t *testing.T) {
	avail := map[string]struct{}{"create_agent": {}}
	tt := newWiredToolsTool(avail)
	ctx := context.Background()

	// Provide both names and query — names must win.
	res := tt.Execute(ctx, map[string]any{
		"names": []any{"create_agent"},
		"query": "some query that would not match anything",
	})
	if res.IsError {
		t.Fatalf("names+query: expected load success, got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "create_agent") {
		t.Errorf("names+query: expected create_agent in result; got: %s", res.ForLLM)
	}
	// Must be the load result shape (contains 'loaded' key), not a search result.
	if !strings.Contains(res.ForLLM, `"loaded"`) {
		t.Errorf("names+query: result must be from load path (contains 'loaded'); got: %s", res.ForLLM)
	}
}

// TestToolsTool_Query_AutoLoad_MakesToolCallable proves that after tools{query:...}
// auto-loads the top hit, the tool is now callable (promoted) in the registry.
func TestToolsTool_Query_AutoLoad_MakesToolCallable(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_target", desc: "target tool for callable test"})

	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(
		func(_ context.Context, name string) (bool, string) { return true, "" },
		func(_ context.Context, names []string) (map[string]any, []string) {
			sc := make(map[string]any)
			for _, n := range names {
				sc[n] = map[string]any{"name": n}
			}
			return sc, nil
		},
	)

	// Before query: not callable.
	_, ok := reg.Get("mcp_target")
	if ok {
		t.Fatal("mcp_target must not be callable before query")
	}

	ctx := context.Background()
	res := tt.Execute(ctx, map[string]any{"query": "target tool callable"})
	if res.IsError {
		t.Fatalf("query failed: %s", res.ForLLM)
	}

	// After query with auto-load: must be callable.
	_, ok = reg.Get("mcp_target")
	if !ok {
		t.Error("mcp_target must be callable (Get-able) after tools{query:...} auto-loads it")
	}
}

// TestDiscoveryTool_ByNameAndByDescriptionUnchanged pins ADR-071 D1 / spec
// FR-010 (W-D1 test 12): the rename is name-only — both the by-name ("names")
// and by-description ("query") paths behave identically to their pre-rename
// mechanics on the SAME tool instance, now answering to "ToolSearch". This is
// the integration-level parity check the spec's Independent Test for User
// Story 2 asks for ("Rename nothing else. Confirm the capability answers to
// its new name for both the by-name and by-description paths").
func TestDiscoveryTool_ByNameAndByDescriptionUnchanged(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_by_name", desc: "loadable directly by exact name"})
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_by_desc", desc: "discoverable only by describing intent"})

	var markedLoaded []string
	available := map[string]struct{}{"mcp_by_name": {}, "mcp_by_desc": {}}
	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(
		func(_ context.Context, name string) (bool, string) {
			if _, ok := available[name]; ok {
				return true, ""
			}
			return false, name + " — not available in test"
		},
		func(_ context.Context, names []string) (map[string]any, []string) {
			markedLoaded = append(markedLoaded, names...)
			sc := make(map[string]any, len(names))
			for _, n := range names {
				sc[n] = map[string]any{"name": n}
			}
			return sc, nil
		},
	)

	if got := tt.Name(); got != "ToolSearch" {
		t.Fatalf("Name() = %q, want %q", got, "ToolSearch")
	}

	ctx := context.Background()

	// By-name path: "names" resolves and loads the exact tool, unchanged.
	byName := tt.Execute(ctx, map[string]any{"names": []any{"mcp_by_name"}})
	if byName.IsError {
		t.Fatalf("by-name path failed under the renamed tool: %s", byName.ForLLM)
	}
	if !strings.Contains(byName.ForLLM, "mcp_by_name") {
		t.Errorf("by-name result must mention the loaded tool; got: %s", byName.ForLLM)
	}
	if _, ok := reg.Get("mcp_by_name"); !ok {
		t.Error("by-name path must promote the hidden tool (PromoteTools), same as before the rename")
	}

	// By-description path: "query" still ranks, auto-loads the top hit, and
	// returns the full match list — same shape as before the rename.
	byDesc := tt.Execute(ctx, map[string]any{"query": "describing intent"})
	if byDesc.IsError {
		t.Fatalf("by-description path failed under the renamed tool: %s", byDesc.ForLLM)
	}
	if !strings.Contains(byDesc.ForLLM, "mcp_by_desc") {
		t.Errorf("by-description result must mention the matched tool; got: %s", byDesc.ForLLM)
	}
	if !strings.Contains(byDesc.ForLLM, `"loaded"`) {
		t.Errorf("by-description result must include the auto-loaded 'loaded' field; got: %s", byDesc.ForLLM)
	}
	if _, ok := reg.Get("mcp_by_desc"); !ok {
		t.Error("by-description path must promote its auto-loaded top hit, same as before the rename")
	}

	if len(markedLoaded) != 2 {
		t.Errorf("markLoaded must have been called for both tools across the two paths; got: %v", markedLoaded)
	}
}
