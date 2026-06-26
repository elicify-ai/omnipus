// Omnipus — multi-MCP tool-search fidelity tests at the index level (T2)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These tests cover §6 of docs/internal/specs/tool-test-plan-2026-06.md:
// multi-server discovery, ranking fidelity, version-keyed cache refresh,
// promotion TTL lifecycle, cross-server regex, and concurrent register+search.
//
// All tests are pure in-process: RegisterHidden of mockSearchableTool or
// NewMCPTool wrappers — no real subprocesses. Deterministic.
//
// After the tools-tool unification, searches go through ToolsTool
// (action="search") which is READ-ONLY (no promote). Promotion is only done
// by ToolsTool action="load" or direct PromoteTools calls.
//
// Build tags: -tags goolm,stdjson CGO_ENABLED=0 (inherited from the package).

package tools

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMCPToolForTest constructs a lightweight *MCPTool using a nil MCPManager.
// These tools are registered as hidden and used only for index/search tests
// (Execute is never called, so nil manager is safe here).
func newMCPToolForTest(serverName, toolName, toolDesc string) *MCPTool {
	return NewMCPTool(nil, serverName, &mcp.Tool{
		Name:        toolName,
		Description: toolDesc,
	})
}

// setupMultiMCPRegistry builds a ToolRegistry with hidden tools from two
// MCP servers:
//
//   - alpha: "search_repos" — "Search repositories"
//   - beta:  "query_docs"   — "Query documentation"
//
// The naming follows the mcp_<server>_<tool> convention produced by MCPTool.Name().
// Actual names after sanitization: "mcp_alpha_search_repos", "mcp_beta_query_docs".
func setupMultiMCPRegistry() *ToolRegistry {
	reg := NewToolRegistry()
	reg.RegisterHidden(newMCPToolForTest("alpha", "search_repos", "Search repositories"))
	reg.RegisterHidden(newMCPToolForTest("beta", "query_docs", "Query documentation"))
	return reg
}

// newMultiMCPTool returns a ToolsTool wrapping the given registry.
func newMultiMCPTool(reg *ToolRegistry) *ToolsTool {
	return NewToolsTool(reg, 5, 10)
}

// ─── Part B §6 (1) — Two-server BM25 ranking ───────────────────────────────

// TestMultiMCP_BM25_AlphaTopForSearchRepos proves that a BM25 query for
// "search repositories" ranks mcp_alpha_search_repos as the top result and
// does not return mcp_beta_query_docs as the top result.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 1)
func TestMultiMCP_BM25_AlphaTopForSearchRepos(t *testing.T) {
	reg := setupMultiMCPRegistry()
	tt := newMultiMCPTool(reg)
	ctx := context.Background()

	res := execSearchBM25(tt, ctx, "search repositories")
	if res.IsError {
		t.Fatalf("BM25 search(search repositories): unexpected error: %s", res.ForLLM)
	}

	if !strings.Contains(res.ForLLM, "mcp_alpha_search_repos") {
		t.Errorf("BM25 search(search repositories): expected mcp_alpha_search_repos in results; got: %s", res.ForLLM)
	}

	// Differentiation: the result must contain alpha tool. For the top-ranked
	// check, verify alpha appears BEFORE beta (if beta appears at all).
	alphaIdx := strings.Index(res.ForLLM, "mcp_alpha_search_repos")
	betaIdx := strings.Index(res.ForLLM, "mcp_beta_query_docs")
	if betaIdx != -1 && alphaIdx > betaIdx {
		t.Errorf("BM25 search(search repositories): alpha should rank before beta; alphaIdx=%d betaIdx=%d", alphaIdx, betaIdx)
	}
}

// TestMultiMCP_BM25_BetaTopForQueryDocs proves that a BM25 query for
// "query documentation" ranks mcp_beta_query_docs as the top result.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 1)
func TestMultiMCP_BM25_BetaTopForQueryDocs(t *testing.T) {
	reg := setupMultiMCPRegistry()
	tt := newMultiMCPTool(reg)
	ctx := context.Background()

	res := execSearchBM25(tt, ctx, "query documentation")
	if res.IsError {
		t.Fatalf("BM25 search(query documentation): unexpected error: %s", res.ForLLM)
	}

	if !strings.Contains(res.ForLLM, "mcp_beta_query_docs") {
		t.Errorf("BM25 search(query documentation): expected mcp_beta_query_docs in results; got: %s", res.ForLLM)
	}

	// Differentiation: beta appears before alpha (if alpha appears at all).
	betaIdx := strings.Index(res.ForLLM, "mcp_beta_query_docs")
	alphaIdx := strings.Index(res.ForLLM, "mcp_alpha_search_repos")
	if alphaIdx != -1 && betaIdx > alphaIdx {
		t.Errorf("BM25 search(query documentation): beta should rank before alpha; betaIdx=%d alphaIdx=%d", betaIdx, alphaIdx)
	}
}

// TestMultiMCP_BM25_Differentiation is the explicit differentiation test:
// the same registry with two different queries must produce two different
// top-ranked tools. This directly catches a hardcoded or non-functional ranker.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 1)
func TestMultiMCP_BM25_Differentiation(t *testing.T) {
	reg := setupMultiMCPRegistry()
	tt := newMultiMCPTool(reg)
	ctx := context.Background()

	res1 := execSearchBM25(tt, ctx, "search repositories")
	res2 := execSearchBM25(tt, ctx, "query documentation")

	if res1.IsError || res2.IsError {
		t.Fatalf("BM25 differentiation: unexpected error: %s / %s", res1.ForLLM, res2.ForLLM)
	}

	// The two results must differ (different top tools).
	if res1.ForLLM == res2.ForLLM {
		t.Errorf("BM25 differentiation: two different queries returned identical results — ranker is broken or registry is empty")
	}

	// "search repositories" query must find alpha, not only beta.
	if !strings.Contains(res1.ForLLM, "mcp_alpha_search_repos") {
		t.Errorf("BM25(search repositories): expected alpha in results; got: %s", res1.ForLLM)
	}

	// "query documentation" query must find beta, not only alpha.
	if !strings.Contains(res2.ForLLM, "mcp_beta_query_docs") {
		t.Errorf("BM25(query documentation): expected beta in results; got: %s", res2.ForLLM)
	}
}

// ─── Part B §6 (2) — Ranking with overlapping descriptions ────────────────

// TestMultiMCP_BM25_OverlappingDescriptions proves that with two "search"
// tools and one "list" tool, the BM25 ranker correctly identifies the most
// relevant tool first and the weaker match still appears (not dropped).
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 2)
func TestMultiMCP_BM25_OverlappingDescriptions(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(newMCPToolForTest("gamma", "search_files", "Search files in the repository"))
	reg.RegisterHidden(newMCPToolForTest("delta", "search_logs", "Search log entries"))
	reg.RegisterHidden(newMCPToolForTest("epsilon", "list_items", "List items in a collection"))

	tt := newMultiMCPTool(reg)
	ctx := context.Background()

	// Query strongly matching gamma.
	res := execSearchBM25(tt, ctx, "search repository files")
	if res.IsError {
		t.Fatalf("BM25 overlapping(search repository files): error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "mcp_gamma_search_files") {
		t.Errorf("BM25 overlapping: expected mcp_gamma_search_files in results; got: %s", res.ForLLM)
	}
	// gamma must appear before delta (both match "search").
	gammaIdx := strings.Index(res.ForLLM, "mcp_gamma_search_files")
	deltaIdx := strings.Index(res.ForLLM, "mcp_delta_search_logs")
	if deltaIdx != -1 && gammaIdx > deltaIdx {
		t.Errorf("BM25 overlapping: gamma should rank before delta for 'search repository files'; gammaIdx=%d deltaIdx=%d", gammaIdx, deltaIdx)
	}

	// Query for "list" — epsilon must appear.
	res2 := execSearchBM25(tt, ctx, "list items")
	if res2.IsError {
		t.Fatalf("BM25 overlapping(list items): error: %s", res2.ForLLM)
	}
	if !strings.Contains(res2.ForLLM, "mcp_epsilon_list_items") {
		t.Errorf("BM25 overlapping: expected mcp_epsilon_list_items for 'list items'; got: %s", res2.ForLLM)
	}
}

// ─── Part B §6 (3) — Version-keyed cache refresh ──────────────────────────

// TestMultiMCP_BM25_CacheRefreshOnNewServer proves that after a search builds
// the BM25 cache at Version V1, registering a third server's hidden tool
// increments the registry version (V2), and the next BM25 search rebuilds the
// cache and finds the new tool.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 3)
func TestMultiMCP_BM25_CacheRefreshOnNewServer(t *testing.T) {
	reg := setupMultiMCPRegistry() // alpha + beta
	tt := newMultiMCPTool(reg)
	ctx := context.Background()

	// Build cache at V1 by running a search.
	v1 := reg.Version()
	res1 := execSearchBM25(tt, ctx, "search repositories")
	if res1.IsError {
		t.Fatalf("BM25 cache V1 search: %s", res1.ForLLM)
	}

	// Register a third server's tool — increments version to V2.
	reg.RegisterHidden(newMCPToolForTest("gamma", "ingest_data", "Ingest data into the pipeline"))
	v2 := reg.Version()
	if v2 <= v1 {
		t.Fatalf("registry version must increase after RegisterHidden: v1=%d v2=%d", v1, v2)
	}

	// The next search must find the new tool (cache rebuilt at V2).
	res2 := execSearchBM25(tt, ctx, "ingest data pipeline")
	if res2.IsError {
		t.Fatalf("BM25 cache V2 search: %s", res2.ForLLM)
	}
	if !strings.Contains(res2.ForLLM, "mcp_gamma_ingest_data") {
		t.Errorf("BM25 cache refresh: expected mcp_gamma_ingest_data after V2 registration; got: %s", res2.ForLLM)
	}
}

// ─── Part B §6 (4) — Promotion TTL lifecycle (multi-server) ───────────────

// TestMultiMCP_PromotionTTL_Lifecycle proves the full promotion TTL lifecycle
// for hidden tools from two MCP servers via direct PromoteTools calls (not via
// search, since search is now read-only).
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 4)
func TestMultiMCP_PromotionTTL_Lifecycle(t *testing.T) {
	reg := setupMultiMCPRegistry() // alpha=search_repos, beta=query_docs
	ttl := 3

	alphaName := "mcp_alpha_search_repos"
	betaName := "mcp_beta_query_docs"

	// Step 1: Before promotion — both tools hidden (TTL=0, not Get-able).
	_, alphaVisible := reg.Get(alphaName)
	_, betaVisible := reg.Get(betaName)
	if alphaVisible {
		t.Error("before promotion: alpha must not be Get-able (TTL=0)")
	}
	if betaVisible {
		t.Error("before promotion: beta must not be Get-able (TTL=0)")
	}

	// Step 2: Direct promotion of alpha (as load action would do).
	reg.PromoteTools([]string{alphaName}, ttl)

	// Alpha must now be Get-able (promoted).
	_, alphaVisible = reg.Get(alphaName)
	if !alphaVisible {
		t.Errorf("after PromoteTools: alpha must be Get-able")
	}

	// Beta was not promoted — must stay hidden.
	_, betaVisible = reg.Get(betaName)
	if betaVisible {
		t.Errorf("beta must stay hidden (only alpha was promoted)")
	}

	// Step 3: Tick TTL down to 0 — alpha must become hidden again.
	for i := 0; i < ttl; i++ {
		reg.TickTTL()
	}

	_, alphaVisible = reg.Get(alphaName)
	if alphaVisible {
		t.Errorf("after %d TickTTL calls: alpha must be hidden (TTL=0) again", ttl)
	}
}

// TestMultiMCP_PromotionTTL_MatchedGetable_ThenHidden proves the simplest
// lifecycle: promoted tool is Get-able, then hidden after TTL expires.
//
// Content test: verifies the actual TTL field value.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 4)
func TestMultiMCP_PromotionTTL_MatchedGetable_ThenHidden(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(newMCPToolForTest("alpha", "search_repos", "Search repositories"))

	const ttl = 2
	// Promote directly (as tools{action:load} would do).
	reg.PromoteTools([]string{"mcp_alpha_search_repos"}, ttl)

	// Verify actual TTL stored.
	reg.mu.RLock()
	entry := reg.tools["mcp_alpha_search_repos"]
	reg.mu.RUnlock()
	if entry == nil {
		t.Fatal("mcp_alpha_search_repos not found in registry after RegisterHidden")
	}
	if entry.TTL != ttl {
		t.Errorf("TTL after PromoteTools: got %d, want %d", entry.TTL, ttl)
	}

	// Get-able while TTL > 0.
	_, ok := reg.Get("mcp_alpha_search_repos")
	if !ok {
		t.Error("promoted tool must be Get-able")
	}

	// Tick down to 0.
	reg.TickTTL() // 2→1
	reg.TickTTL() // 1→0

	reg.mu.RLock()
	entry = reg.tools["mcp_alpha_search_repos"]
	reg.mu.RUnlock()
	if entry.TTL != 0 {
		t.Errorf("TTL after 2 ticks: got %d, want 0", entry.TTL)
	}

	_, ok = reg.Get("mcp_alpha_search_repos")
	if ok {
		t.Error("tool with TTL=0 must not be Get-able")
	}
}

// ─── Part B §6 (5) — tools(search/regex) across servers ────────────────────

// TestMultiMCP_Regex_AcrossServers proves that tools(action=search, mode=regex)
// matches tool names AND descriptions across both MCP servers.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 5)
func TestMultiMCP_Regex_AcrossServers(t *testing.T) {
	reg := setupMultiMCPRegistry() // alpha=search_repos, beta=query_docs
	tt := newMultiMCPTool(reg)
	ctx := context.Background()

	// Pattern matching alpha by name.
	res := execSearchRegex(tt, ctx, "search_repos")
	if res.IsError {
		t.Fatalf("regex across servers (alpha name): %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "mcp_alpha_search_repos") {
		t.Errorf("regex(search_repos): expected mcp_alpha_search_repos; got: %s", res.ForLLM)
	}

	// Pattern matching beta by description keyword.
	res2 := execSearchRegex(tt, ctx, "documentation")
	if res2.IsError {
		t.Fatalf("regex across servers (beta desc): %s", res2.ForLLM)
	}
	if !strings.Contains(res2.ForLLM, "mcp_beta_query_docs") {
		t.Errorf("regex(documentation): expected mcp_beta_query_docs; got: %s", res2.ForLLM)
	}

	// Pattern matching BOTH servers (the word "mcp" appears in all tool names).
	res3 := execSearchRegex(tt, ctx, "mcp")
	if res3.IsError {
		t.Fatalf("regex across servers (both): %s", res3.ForLLM)
	}
	if !strings.Contains(res3.ForLLM, "mcp_alpha_search_repos") {
		t.Errorf("regex(mcp): expected alpha in results; got: %s", res3.ForLLM)
	}
	if !strings.Contains(res3.ForLLM, "mcp_beta_query_docs") {
		t.Errorf("regex(mcp): expected beta in results; got: %s", res3.ForLLM)
	}
}

// TestMultiMCP_Regex_InvalidPattern proves that an invalid regex pattern
// returns an error and does not promote any tools.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 5)
func TestMultiMCP_Regex_InvalidPattern(t *testing.T) {
	reg := setupMultiMCPRegistry()
	tt := newMultiMCPTool(reg)
	ctx := context.Background()

	res := execSearchRegex(tt, ctx, "[unclosed")
	if !res.IsError {
		t.Errorf("invalid regex must return error; got: %s", res.ForLLM)
	}
	// Must mention the syntax issue.
	if !strings.Contains(res.ForLLM, "Invalid regex pattern syntax") {
		t.Errorf("error message must mention 'Invalid regex pattern syntax'; got: %s", res.ForLLM)
	}

	// No tools must be promoted after an invalid-pattern error.
	reg.mu.RLock()
	alphaEntry := reg.tools["mcp_alpha_search_repos"]
	betaEntry := reg.tools["mcp_beta_query_docs"]
	reg.mu.RUnlock()
	if alphaEntry != nil && alphaEntry.TTL > 0 {
		t.Errorf("invalid regex must not promote alpha; TTL=%d", alphaEntry.TTL)
	}
	if betaEntry != nil && betaEntry.TTL > 0 {
		t.Errorf("invalid regex must not promote beta; TTL=%d", betaEntry.TTL)
	}
}

// TestMultiMCP_Regex_OversizedPattern proves that a pattern longer than
// MaxRegexPatternLength is rejected with a clear error.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 5)
func TestMultiMCP_Regex_OversizedPattern(t *testing.T) {
	reg := setupMultiMCPRegistry()
	tt := newMultiMCPTool(reg)
	ctx := context.Background()

	longPattern := strings.Repeat("a", MaxRegexPatternLength+1) // 201 chars
	res := execSearchRegex(tt, ctx, longPattern)
	if !res.IsError {
		t.Errorf("oversized pattern (len=%d) must return error; got: %s", len(longPattern), res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Pattern too long") {
		t.Errorf("oversized error must say 'Pattern too long'; got: %s", res.ForLLM)
	}
}

// TestMultiMCP_Regex_EmptyPattern proves that an empty (whitespace-only)
// pattern is rejected with a clear error.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 5)
func TestMultiMCP_Regex_EmptyPattern(t *testing.T) {
	reg := setupMultiMCPRegistry()
	tt := newMultiMCPTool(reg)
	ctx := context.Background()

	for _, pattern := range []string{"", "   ", "\t"} {
		res := execSearchRegex(tt, ctx, pattern)
		if !res.IsError {
			t.Errorf("empty/whitespace pattern %q must return error; got: %s", pattern, res.ForLLM)
		}
	}
}

// ─── Part B §6 (6) — Concurrent register + search (no race) ──────────────

// TestMultiMCP_Concurrent_RegisterAndSearch proves there is no data race
// between concurrent RegisterHidden calls and concurrent BM25 or Regex search calls.
//
// Run with: go test -tags goolm,stdjson -race -run TestMultiMCP_Concurrent_RegisterAndSearch
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 6)
func TestMultiMCP_Concurrent_RegisterAndSearch(t *testing.T) {
	reg := NewToolRegistry()
	// Seed some initial tools from two servers.
	reg.RegisterHidden(newMCPToolForTest("alpha", "search_repos", "Search repositories"))
	reg.RegisterHidden(newMCPToolForTest("beta", "query_docs", "Query documentation"))

	bm25tt := NewToolsTool(reg, 5, 20)
	regextt := NewToolsTool(reg, 5, 20)
	ctx := context.Background()

	const (
		writers  = 4
		bm25ers  = 4
		regexers = 4
		iters    = 50
	)

	var wg sync.WaitGroup

	// Writers: register new hidden tools from a "gamma" server concurrently.
	for w := 0; w < writers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				reg.RegisterHidden(newMCPToolForTest("gamma",
					"tool_"+string(rune('a'+w))+"_"+string(rune('a'+i%26)),
					"Gamma tool for concurrent testing"))
			}
		}()
	}

	// BM25 searchers: run searches concurrently.
	for b := 0; b < bm25ers; b++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				res := execSearchBM25(bm25tt, ctx, "search repositories")
				// We don't assert on results here (registry is in flux) — only race detection matters.
				_ = res
			}
		}()
	}

	// Regex searchers: run searches concurrently.
	for r := 0; r < regexers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				res := execSearchRegex(regextt, ctx, "mcp")
				_ = res
			}
		}()
	}

	wg.Wait()
	// If we reach here without the race detector firing, the test passes.
}

// TestMultiMCP_Concurrent_PromoteAndSearch proves there is no data race
// between concurrent PromoteTools/TickTTL and concurrent searches.
//
// Run with: go test -tags goolm,stdjson -race -run TestMultiMCP_Concurrent_PromoteAndSearch
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 6)
func TestMultiMCP_Concurrent_PromoteAndSearch(t *testing.T) {
	reg := NewToolRegistry()
	for i := 0; i < 10; i++ {
		reg.RegisterHidden(newMCPToolForTest("server",
			"tool_"+string(rune('a'+i)),
			"concurrent test tool"))
	}

	tt := NewToolsTool(reg, 5, 20)
	ctx := context.Background()

	// Collect all registered hidden tool names.
	names := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		mock := newMCPToolForTest("server", "tool_"+string(rune('a'+i)), "")
		names = append(names, mock.Name())
	}

	var wg sync.WaitGroup

	// Promoters.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			reg.PromoteTools(names, 5)
		}
	}()

	// Tickers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			reg.TickTTL()
		}
	}()

	// Searchers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			res := execSearchBM25(tt, ctx, "concurrent test")
			_ = res
		}
	}()

	wg.Wait()
}

// ─── Persistence / content test ───────────────────────────────────────────

// TestMultiMCP_Persistence_SearchThenLoadContent proves that after a search
// finds a tool and then load promotes it, Get returns the actual tool with the
// correct Name and Description.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (T2, point 4)
func TestMultiMCP_Persistence_SearchThenLoadContent(t *testing.T) {
	reg := setupMultiMCPRegistry()

	// Step 1: Search finds the tool (read-only, not promoted).
	tt := newMultiMCPTool(reg)
	ctx := context.Background()
	res := execSearchBM25(tt, ctx, "search repositories")
	if res.IsError {
		t.Fatalf("search failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "mcp_alpha_search_repos") {
		t.Fatalf("search must find mcp_alpha_search_repos; got: %s", res.ForLLM)
	}

	// After search alone: NOT promoted (search is read-only).
	_, ok := reg.Get("mcp_alpha_search_repos")
	if ok {
		t.Error("tool must NOT be Get-able after search alone (search is read-only)")
	}

	// Step 2: Load promotes the tool.
	reg.PromoteTools([]string{"mcp_alpha_search_repos"}, 5)

	// After promote: Get-able.
	gotTool, ok := reg.Get("mcp_alpha_search_repos")
	if !ok {
		t.Fatal("mcp_alpha_search_repos must be Get-able after PromoteTools")
	}

	// Content test: actual Name must match.
	if gotTool.Name() != "mcp_alpha_search_repos" {
		t.Errorf("Name(): got %q, want %q", gotTool.Name(), "mcp_alpha_search_repos")
	}

	// Content test: Description must contain the original description text.
	if !strings.Contains(gotTool.Description(), "Search repositories") {
		t.Errorf("Description(): got %q, must contain 'Search repositories'", gotTool.Description())
	}
}

// TestMultiMCP_Rejection_UnknownToolNotPromoted proves that promoting a name
// that was never registered is silently ignored (no panic, no phantom entry).
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §6 (error scenarios)
func TestMultiMCP_Rejection_UnknownToolNotPromoted(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(newMCPToolForTest("alpha", "search_repos", "Search repositories"))

	// Promote a phantom name — must not panic or create a new entry.
	reg.PromoteTools([]string{"mcp_phantom_nonexistent_xyz"}, 5)

	_, ok := reg.Get("mcp_phantom_nonexistent_xyz")
	if ok {
		t.Error("phantom tool must not be Get-able after PromoteTools on unknown name")
	}

	// Real tool unaffected.
	_, ok = reg.Get("mcp_alpha_search_repos")
	if ok {
		t.Error("real tool (TTL=0) must not be Get-able without explicit promotion")
	}
}

// ─── Search is read-only: multi-server assertion ───────────────────────────

// TestMultiMCP_SearchIsReadOnly_NeverPromotes proves that after searching across
// two MCP servers (finding both), neither tool has TTL > 0 (not promoted).
// This is the critical behavioral change from the old auto-promote-on-search.
func TestMultiMCP_SearchIsReadOnly_NeverPromotes(t *testing.T) {
	reg := setupMultiMCPRegistry()
	tt := newMultiMCPTool(reg)
	ctx := context.Background()

	// Search for all mcp tools.
	res := execSearchRegex(tt, ctx, "mcp")
	if res.IsError {
		t.Fatalf("search failed: %s", res.ForLLM)
	}

	// Neither tool must have TTL > 0 after a search-only call.
	reg.mu.RLock()
	alphaEntry := reg.tools["mcp_alpha_search_repos"]
	betaEntry := reg.tools["mcp_beta_query_docs"]
	reg.mu.RUnlock()

	if alphaEntry != nil && alphaEntry.TTL > 0 {
		t.Errorf("tools(search) must NOT promote mcp_alpha_search_repos; TTL=%d", alphaEntry.TTL)
	}
	if betaEntry != nil && betaEntry.TTL > 0 {
		t.Errorf("tools(search) must NOT promote mcp_beta_query_docs; TTL=%d", betaEntry.TTL)
	}

	// Also verify response tells model to call action='load'.
	if !strings.Contains(res.ForLLM, "action='load'") {
		t.Errorf("search response must mention action='load'; got: %s", res.ForLLM)
	}
}
