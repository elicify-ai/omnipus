// Omnipus — Part 2: BM25 corpus widening + exact-miss fallback tests (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Part 2 tests: BM25 search corpus should include visible lazy built-in tools
// (in addition to hidden/MCP tools). An exact-miss in execLoad should fall back
// to BM25/fuzzy and return an actionable suggestion.

package tools

import (
	"context"
	"strings"
	"testing"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// setupWideCorpusRegistry builds a registry that has:
//   - a visible (core) lazy built-in: "create_agent" (not hidden, registered via Register)
//   - a hidden MCP tool: "mcp_fetch_something"
//   - a visible (core) tool that is ManifestFull (should still be searchable but
//     the caller's canLoad will gate what it returns)
func setupWideCorpusRegistry() *ToolRegistry {
	reg := NewToolRegistry()
	// Visible lazy built-in (matches what agents register via Register, not RegisterHidden).
	// Must be ManifestLazy so it appears in the corpus.
	reg.Register(&mockSearchableTool{name: "create_agent_visible", desc: "Create a new agent in the system"})
	reg.Register(&mockSearchableTool{name: "update_task_visible", desc: "Update an existing task with new parameters"})
	// Hidden MCP tool.
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_hidden_notify", desc: "Send a hidden notification via MCP"})
	return reg
}

// ── Test: query now finds visible lazy built-in tools ────────────────────────

// TestBM25_QueryFindsVisibleLazyBuiltin verifies that a BM25 query returns
// a visible (non-hidden) lazy built-in tool registered via Register().
// Before the fix, only hidden tools (RegisterHidden) were in the BM25 corpus.
func TestBM25_QueryFindsVisibleLazyBuiltin(t *testing.T) {
	reg := setupWideCorpusRegistry()
	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(permissiveCanLoad, stubMarkLoaded) // §3.2.2: a resolver is required to see any match

	res := tt.Execute(context.Background(), map[string]any{
		"query": "create agent",
	})

	if res.IsError {
		t.Fatalf("query failed unexpectedly: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "create_agent_visible") {
		t.Errorf(
			"BM25 corpus must include visible lazy built-ins; 'create_agent_visible' not in result: %s",
			res.ForLLM,
		)
	}
}

// TestBM25_QueryFindsHiddenToolStillWorks verifies that hidden (MCP) tools are
// still found after the corpus is widened (regression guard).
func TestBM25_QueryFindsHiddenToolStillWorks(t *testing.T) {
	reg := setupWideCorpusRegistry()
	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(permissiveCanLoad, stubMarkLoaded) // §3.2.2: a resolver is required to see any match

	res := tt.Execute(context.Background(), map[string]any{
		"query": "hidden notification",
	})

	if res.IsError {
		t.Fatalf("hidden tool query failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "mcp_hidden_notify") {
		t.Errorf("hidden tool mcp_hidden_notify must still be searchable after corpus widening: %s", res.ForLLM)
	}
}

// TestBM25_QueryFindsUpdateTask verifies that "task_update" query finds "update_task_visible"
// (token-set match: same tokens regardless of order).
func TestBM25_QueryFindsUpdateTask(t *testing.T) {
	reg := setupWideCorpusRegistry()
	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(permissiveCanLoad, stubMarkLoaded) // §3.2.2: a resolver is required to see any match

	res := tt.Execute(context.Background(), map[string]any{
		"query": "update task",
	})

	if res.IsError {
		t.Fatalf("update task query failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "update_task_visible") {
		t.Errorf("'update task' query must surface update_task_visible; got: %s", res.ForLLM)
	}
}

// ── Test: exact-miss in execLoad falls back to suggestion ────────────────────
//
// These tests wire a canLoad that calls FindClosestToolName internally (similar
// to what the real agent loop does at loop.go:1876-1878) so the rejection
// reason already includes a suggestion. The execLoad path must propagate that
// suggestion through to the error message. Additionally, execLoad must do its
// own BM25 augmentation when canLoad provides no suggestion.

// registryAwareCanLoad builds a canLoad that returns a "did you mean X?"
// suggestion by searching the registry — mirrors the real loop.go behavior
// so tests exercise the same suggestion path as production.
func registryAwareCanLoad(
	reg *ToolRegistry,
	available map[string]struct{},
) func(ctx context.Context, name string) (bool, string) {
	return func(_ context.Context, name string) (bool, string) {
		if _, ok := available[name]; ok {
			return true, ""
		}
		// Check if name is in the registry at all (just not in available set).
		allTools := reg.GetAll()
		for _, t := range allTools {
			if t.Name() == name {
				return false, name + " — denied by policy"
			}
		}
		// Genuinely unknown: suggest closest.
		if suggestion := FindClosestToolName(allTools, name); suggestion != "" {
			return false, name + " — unknown tool (did you mean '" + suggestion + "'?)"
		}
		return false, name + " — unknown tool name"
	}
}

// TestExactMissFallbackSuggestsUpdateTask proves that loading "task_update"
// (wrong name, transposed tokens) returns a suggestion pointing at "update_task".
// The suggestion comes from FindClosestToolName (token-set match).
func TestExactMissFallbackSuggestsUpdateTask(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&mockSearchableTool{name: "update_task", desc: "Update an existing task"})

	available := map[string]struct{}{"update_task": {}}
	tt := NewToolsTool(reg, 5, 10)
	markLoaded := func(_ context.Context, names []string) (map[string]any, []string) {
		return map[string]any{}, nil
	}
	tt.SetResolver(registryAwareCanLoad(reg, available), markLoaded)

	r := tt.Execute(context.Background(), map[string]any{
		"names": []any{"task_update"},
	})

	if !r.IsError {
		t.Errorf("exact-miss 'task_update': expected error, got success: %s", r.ForLLM)
		return
	}
	if !strings.Contains(r.ForLLM, "update_task") {
		t.Errorf("exact-miss 'task_update': error must suggest 'update_task'; got: %s", r.ForLLM)
	}
}

// TestExactMissFallbackSuggestsBrowserTool proves that loading "browser"
// (wrong name, partial match) returns a browser tool suggestion via BM25 or fuzzy.
func TestExactMissFallbackSuggestsBrowserTool(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&mockSearchableTool{name: "browser_navigate", desc: "Navigate the browser to a URL"})
	reg.Register(&mockSearchableTool{name: "browser_click", desc: "Click on a browser element"})

	available := map[string]struct{}{
		"browser_navigate": {},
		"browser_click":    {},
	}
	tt := NewToolsTool(reg, 5, 10)
	markLoaded := func(_ context.Context, names []string) (map[string]any, []string) {
		return map[string]any{}, nil
	}
	tt.SetResolver(registryAwareCanLoad(reg, available), markLoaded)

	r := tt.Execute(context.Background(), map[string]any{
		"names": []any{"browser"},
	})

	if !r.IsError {
		t.Errorf("exact-miss 'browser': expected error, got: %s", r.ForLLM)
		return
	}
	// BM25 search over "browser" should surface browser_navigate or browser_click.
	if !strings.Contains(r.ForLLM, "browser_navigate") && !strings.Contains(r.ForLLM, "browser_click") {
		t.Errorf("exact-miss 'browser': error must suggest a real browser tool via BM25/fuzzy; got: %s", r.ForLLM)
	}
}

// TestExactMissFallbackSuggestsViaFuzzy proves that a one-character transposition
// typo of "browser_navigate" suggests the correct tool via the fuzzy fallback.
func TestExactMissFallbackSuggestsViaFuzzy(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&mockSearchableTool{name: "browser_navigate", desc: "Navigate the browser to a URL"})

	available := map[string]struct{}{"browser_navigate": {}}
	tt := NewToolsTool(reg, 5, 10)
	markLoaded := func(_ context.Context, names []string) (map[string]any, []string) {
		return map[string]any{}, nil
	}
	tt.SetResolver(registryAwareCanLoad(reg, available), markLoaded)

	r := tt.Execute(context.Background(), map[string]any{
		"names": []any{"browser_naviagte"}, //nolint:misspell // deliberate typo
	})

	if !r.IsError {
		t.Errorf("typo 'browser_naviagte': expected error, got: %s", r.ForLLM)
		return
	}
	if !strings.Contains(r.ForLLM, "browser_navigate") {
		t.Errorf("typo 'browser_naviagte': error must suggest 'browser_navigate'; got: %s", r.ForLLM)
	}
}

// ── Test: cache version invalidation when visible tools added ─────────────────

// TestBM25_CacheInvalidationOnVisibleRegistration proves that the BM25 cache is
// invalidated when a new visible (non-hidden) tool is registered, ensuring the
// corpus stays current.
func TestBM25_CacheInvalidationOnVisibleRegistration(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&mockSearchableTool{name: "tool_alpha_v", desc: "alpha functionality visible"})

	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(permissiveCanLoad, stubMarkLoaded) // §3.2.2: a resolver is required to see any match
	ctx := context.Background()

	// First search should find tool_alpha_v.
	res := tt.Execute(ctx, map[string]any{"query": "alpha visible"})
	if res.IsError {
		t.Fatalf("first query failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "tool_alpha_v") {
		t.Fatalf("first query: expected 'tool_alpha_v' in results, got: %s", res.ForLLM)
	}

	// Register another visible tool.
	reg.Register(&mockSearchableTool{name: "tool_beta_v", desc: "beta functionality visible"})

	// Cache should be invalidated; new tool should be findable.
	res = tt.Execute(ctx, map[string]any{"query": "beta visible"})
	if res.IsError {
		t.Fatalf("second query failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "tool_beta_v") {
		t.Errorf(
			"cache invalidation: expected 'tool_beta_v' in results after visible registration; got: %s",
			res.ForLLM,
		)
	}
}

// ── Test: policy-denied visible lazy tool excluded from BM25 corpus ───────────

// TestBM25_PolicyDeniedVisibleToolExcluded proves that a visible tool that is
// denied by the caller's policy does NOT appear in BM25 search results when
// the caller has a resolver that denies it.
//
// Note: the corpus itself contains all lazy tools (including denied ones for
// search discoverability), but canLoad and auto-load will correctly deny them.
// This test verifies the auto-load skip-past-denied behavior works for visible
// lazy tools just like for hidden tools.
func TestBM25_PolicyDeniedVisibleToolExcludedFromAutoLoad(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&mockSearchableTool{name: "denied_visible_tool", desc: "denied but visible tool"})
	reg.Register(&mockSearchableTool{name: "allowed_visible_tool", desc: "allowed and visible tool"})

	var loadedName string
	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(
		func(_ context.Context, name string) (bool, string) {
			if name == "allowed_visible_tool" {
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
	res := tt.Execute(ctx, map[string]any{"query": "denied allowed visible tool"})
	if res.IsError {
		t.Fatalf("query failed: %s", res.ForLLM)
	}
	// Must not have loaded the denied one.
	if loadedName == "denied_visible_tool" {
		t.Errorf("denied visible tool must not be auto-loaded; got loadedName=%s", loadedName)
	}
}
