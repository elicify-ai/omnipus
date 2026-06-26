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

	// A core tool (NOT to be found by searches)
	reg.Register(&mockSearchableTool{
		name: "core_search",
		desc: "I am a visible core tool for searching files",
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
func newSearchTool(reg *ToolRegistry, ttl, max int) *ToolsTool {
	return NewToolsTool(reg, ttl, max)
}

// execSearchRegex calls tools{action:search,mode:regex,query:pattern}.
func execSearchRegex(tt *ToolsTool, ctx context.Context, pattern string) *ToolResult {
	return tt.Execute(ctx, map[string]any{
		"action": "search",
		"mode":   "regex",
		"query":  pattern,
	})
}

// execSearchBM25 calls tools{action:search,mode:bm25,query:q} (or just query — default bm25).
func execSearchBM25(tt *ToolsTool, ctx context.Context, query string) *ToolResult {
	return tt.Execute(ctx, map[string]any{
		"action": "search",
		"query":  query,
	})
}

func TestToolsTool_Search_Regex_Execute(t *testing.T) {
	reg := setupPopulatedRegistry()
	tt := newSearchTool(reg, 5, 10)
	ctx := context.Background()

	t.Run("Empty Pattern Error", func(t *testing.T) {
		res := tt.Execute(ctx, map[string]any{"action": "search", "mode": "regex"})
		if !res.IsError || !strings.Contains(res.ForLLM, "missing required parameter") {
			t.Errorf("Expected missing query error, got: %v", res.ForLLM)
		}
	})

	t.Run("Invalid Regex Syntax", func(t *testing.T) {
		res := execSearchRegex(tt, ctx, "[unclosed")
		if !res.IsError || !strings.Contains(res.ForLLM, "Invalid regex pattern syntax") {
			t.Errorf("Expected regex syntax error, got: %v", res.ForLLM)
		}
	})

	t.Run("No Match Found", func(t *testing.T) {
		res := execSearchRegex(tt, ctx, "alien")
		if res.IsError || !strings.Contains(res.ForLLM, "No tools found matching") {
			t.Errorf("Expected 'no tools found' message, got: %v", res.ForLLM)
		}
	})

	t.Run("Successful Match — Read-Only (no promote)", func(t *testing.T) {
		res := execSearchRegex(tt, ctx, "system")

		if res.IsError {
			t.Fatalf("Unexpected error: %v", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "mcp_read_file") {
			t.Errorf("Expected 'mcp_read_file' in results")
		}
		// Must NOT promote tools (search is read-only).
		reg.mu.RLock()
		defer reg.mu.RUnlock()
		if reg.tools["mcp_read_file"] != nil && reg.tools["mcp_read_file"].TTL != 0 {
			t.Errorf("tools(search/regex) must NOT promote mcp_read_file (TTL must be 0, got %d)",
				reg.tools["mcp_read_file"].TTL)
		}
		if reg.tools["mcp_fetch_net"] != nil && reg.tools["mcp_fetch_net"].TTL != 0 {
			t.Errorf("tools(search/regex) must NOT promote mcp_fetch_net (TTL must be 0, got %d)",
				reg.tools["mcp_fetch_net"].TTL)
		}
	})
}

func TestToolsTool_Search_BM25_Execute(t *testing.T) {
	reg := setupPopulatedRegistry()
	tt := newSearchTool(reg, 3, 10)
	ctx := context.Background()

	t.Run("Empty Query Error", func(t *testing.T) {
		res := tt.Execute(ctx, map[string]any{"action": "search", "query": "   "})
		if !res.IsError || !strings.Contains(res.ForLLM, "missing required parameter") {
			t.Errorf("Expected missing query error, got: %v", res.ForLLM)
		}
	})

	t.Run("No Match Found", func(t *testing.T) {
		res := execSearchBM25(tt, ctx, "aliens spaceships")
		if res.IsError || !strings.Contains(res.ForLLM, "No tools found matching") {
			t.Errorf("Expected 'no tools found', got: %v", res.ForLLM)
		}
	})

	t.Run("Successful Match — Read-Only (no promote)", func(t *testing.T) {
		res := execSearchBM25(tt, ctx, "read files")

		if res.IsError {
			t.Fatalf("Unexpected error: %v", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "mcp_read_file") {
			t.Errorf("Expected 'mcp_read_file' in BM25 results")
		}
		// Must NOT promote (search is read-only after tools-tool unification).
		reg.mu.RLock()
		defer reg.mu.RUnlock()
		if reg.tools["mcp_read_file"] != nil && reg.tools["mcp_read_file"].TTL != 0 {
			t.Errorf("tools(search/bm25) must NOT promote mcp_read_file (TTL must be 0, got %d)",
				reg.tools["mcp_read_file"].TTL)
		}
	})
}

func TestToolsTool_Search_Regex_PatternTooLong(t *testing.T) {
	reg := setupPopulatedRegistry()
	tt := newSearchTool(reg, 5, 10)
	ctx := context.Background()

	longPattern := strings.Repeat("a", MaxRegexPatternLength+1)
	res := execSearchRegex(tt, ctx, longPattern)
	if !res.IsError || !strings.Contains(res.ForLLM, "Pattern too long") {
		t.Errorf("Expected pattern too long error, got: %v", res.ForLLM)
	}
}

func TestSearchRegex_ZeroMaxResults(t *testing.T) {
	reg := setupPopulatedRegistry()

	res, err := reg.SearchRegex("mcp", 0)
	if err != nil {
		t.Fatalf("SearchRegex failed: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("Expected 0 results with maxSearchResults=0, got %d", len(res))
	}
}

func TestSearchBM25_ZeroMaxResults(t *testing.T) {
	reg := setupPopulatedRegistry()

	res := reg.SearchBM25("read file", 0)
	if len(res) != 0 {
		t.Errorf("Expected 0 results with maxSearchResults=0, got %d", len(res))
	}
}

func TestSearchRegex_DeterministicOrder(t *testing.T) {
	reg := NewToolRegistry()
	for i := 0; i < 20; i++ {
		reg.RegisterHidden(&mockSearchableTool{
			name: fmt.Sprintf("tool_%02d", i),
			desc: "searchable tool",
		})
	}

	// Run the same search multiple times and verify order is stable
	var firstRun []string
	for attempt := 0; attempt < 10; attempt++ {
		res, err := reg.SearchRegex("searchable", 20)
		if err != nil {
			t.Fatalf("SearchRegex failed: %v", err)
		}

		names := make([]string, len(res))
		for i, r := range res {
			names[i] = r.Name
		}

		if attempt == 0 {
			firstRun = names
		} else {
			for i, name := range names {
				if name != firstRun[i] {
					t.Fatalf("Non-deterministic order at attempt %d, index %d: got %q, want %q",
						attempt, i, name, firstRun[i])
				}
			}
		}
	}
}

func TestToolRegistry_SearchLimitsAndCoreFiltering(t *testing.T) {
	reg := NewToolRegistry()

	// Add 1 Core and 10 Hidden, all containing the word "match"
	reg.Register(&mockSearchableTool{"core_match", "I am core with match"})
	for i := 0; i < 10; i++ {
		reg.RegisterHidden(&mockSearchableTool{
			name: fmt.Sprintf("hidden_match_%d", i),
			desc: "this has a match",
		})
	}

	t.Run("Regex limits and core filtering", func(t *testing.T) {
		// Search with Regex and a limit of maxSearchResults = 4
		res, err := reg.SearchRegex("match", 4)
		if err != nil {
			t.Fatalf("SearchRegex failed: %v", err)
		}

		if len(res) != 4 {
			t.Errorf("Expected exactly 4 results due to limit, got %d", len(res))
		}

		for _, r := range res {
			if r.Name == "core_match" {
				t.Errorf("SearchRegex returned a Core tool, which should be excluded")
			}
		}
	})

	t.Run("BM25 limits and core filtering", func(t *testing.T) {
		// Search with BM25 and a limit of maxSearchResults = 3
		res := reg.SearchBM25("match", 3)

		if len(res) != 3 {
			t.Errorf("Expected exactly 3 results due to limit, got %d", len(res))
		}

		for _, r := range res {
			if r.Name == "core_match" {
				t.Errorf("SearchBM25 returned a Core tool, which should be excluded")
			}
		}
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
	res := execSearchBM25(tt, ctx, "alpha")
	if !strings.Contains(res.ForLLM, "tool_alpha") {
		t.Fatalf("Expected 'tool_alpha' in first search, got: %v", res.ForLLM)
	}

	// Register a new hidden tool
	reg.RegisterHidden(&mockSearchableTool{name: "tool_beta", desc: "beta functionality"})

	// Cache should be invalidated; new tool should be findable
	res = execSearchBM25(tt, ctx, "beta")
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

// TestToolsTool_Search_ResponseMentionsLoad verifies that search results tell
// the model to use action='load', not the old "UNLOCKED" banner.
func TestToolsTool_Search_ResponseMentionsLoad(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_foo", desc: "foo tool"})
	tt := newSearchTool(reg, 5, 10)
	ctx := context.Background()

	res := execSearchBM25(tt, ctx, "foo")
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "UNLOCKED") {
		t.Error("search response must not say 'UNLOCKED' (old auto-promote behavior removed)")
	}
	if !strings.Contains(res.ForLLM, "action='load'") {
		t.Errorf("search response must tell model to use action='load'; got: %s", res.ForLLM)
	}
}
