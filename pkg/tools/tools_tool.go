// Omnipus — tools: unified discovery and load tool (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ToolsTool is the single infra tool that replaces the former load_tool,
// search_tools_bm25, and search_tools_regex trio. It exposes two actions:
//
//   - action="search" — BM25 (default) or regex search over the hidden/lazy
//     registry corpus. READ-ONLY: does NOT promote any tools.
//   - action="load"   — fetch full schemas and make the named tools callable
//     (promotes hidden/MCP tools via PromoteTools AND records them in the
//     per-session loaded-set via markToolsLoaded).
//
// See docs/internal/design/unified-tools-tool-2026-06.md for the full spec.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// ToolsTool is the unified tool-discovery and load infra tool.
//
// It holds the BM25 engine cache (moved from the former BM25SearchTool) so
// the cache survives across action=search calls on the same instance.
type ToolsTool struct {
	BaseTool
	registry         *ToolRegistry
	ttl              int
	maxSearchResults int
	canLoad          func(ctx context.Context, name string) bool
	markLoaded       func(ctx context.Context, names []string) (loaded map[string]any, rejected []string)

	// BM25 engine cache (version-keyed, atomic-pointer + mutex for rebuild).
	cacheMu sync.Mutex
	cached  atomic.Pointer[bm25CachedEngine]
}

// NewToolsTool constructs the unified tools tool. SetResolver must be called
// before any action="load" Execute call (search works without a resolver).
func NewToolsTool(r *ToolRegistry, ttl int, maxSearchResults int) *ToolsTool {
	return &ToolsTool{registry: r, ttl: ttl, maxSearchResults: maxSearchResults}
}

// SetResolver wires the per-agent, per-session callbacks required by action="load".
// Identical contract to the former LoadTool.SetResolver.
func (t *ToolsTool) SetResolver(
	canLoad func(ctx context.Context, name string) bool,
	markLoaded func(ctx context.Context, names []string) (map[string]any, []string),
) {
	t.canLoad = canLoad
	t.markLoaded = markLoaded
}

func (t *ToolsTool) Name() string           { return "tools" }
func (t *ToolsTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ToolsTool) Category() ToolCategory { return CategoryToolDiscovery }

func (t *ToolsTool) Description() string {
	return "Discover and load tools. " +
		"Use action='search' to find tools by keyword/pattern (read-only); " +
		"action='load' to fetch a tool's parameters and make it callable."
}

func (t *ToolsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"search", "load"},
				"description": "The action to perform: 'search' finds tools by keyword/pattern; 'load' fetches schemas and makes tools callable.",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (required for action='search').",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"bm25", "regex"},
				"description": "Search mode for action='search'. Defaults to 'bm25' (natural language). Use 'regex' for pattern matching.",
			},
			"names": map[string]any{
				"type":        "array",
				"description": "Tool name(s) to load (required for action='load').",
				"items": map[string]any{
					"type": "string",
				},
				"minItems": 1,
			},
		},
		"required": []string{"action"},
	}
}

// Execute dispatches on the action parameter.
func (t *ToolsTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action, ok := args["action"].(string)
	if !ok || action == "" {
		return ErrorResult("tools: missing required parameter 'action' (must be 'search' or 'load')")
	}
	switch action {
	case "search":
		return t.execSearch(ctx, args)
	case "load":
		return t.execLoad(ctx, args)
	default:
		return ErrorResult(fmt.Sprintf("tools: unknown action %q — must be 'search' or 'load'", action))
	}
}

// ── search action ────────────────────────────────────────────────────────────

func (t *ToolsTool) execSearch(_ context.Context, args map[string]any) *ToolResult {
	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return ErrorResult("tools(search): missing required parameter 'query' — must be a non-empty string")
	}

	mode := "bm25"
	if m, ok := args["mode"].(string); ok && m != "" {
		mode = m
	}

	switch mode {
	case "bm25":
		return t.searchBM25(query)
	case "regex":
		return t.searchRegex(query)
	default:
		return ErrorResult(fmt.Sprintf("tools(search): unknown mode %q — must be 'bm25' or 'regex'", mode))
	}
}

func (t *ToolsTool) searchBM25(query string) *ToolResult {
	if t.registry == nil {
		return SilentResult("No tools found matching the query.")
	}
	logger.DebugCF("discovery", "tools(search/bm25)", map[string]any{"query": query})

	cached := t.getOrBuildEngine()
	if cached == nil {
		logger.DebugCF("discovery", "tools(search/bm25): no hidden tools available", nil)
		return SilentResult("No tools found matching the query.")
	}

	ranked := cached.engine.Search(query, t.maxSearchResults)
	if len(ranked) == 0 {
		logger.DebugCF("discovery", "tools(search/bm25): no matches", map[string]any{"query": query})
		return SilentResult("No tools found matching the query.")
	}

	results := make([]ToolSearchResult, len(ranked))
	for i, r := range ranked {
		results[i] = ToolSearchResult{
			Name:        r.Document.Name,
			Description: r.Document.Description,
		}
	}

	logger.InfoCF("discovery", "tools(search/bm25) completed",
		map[string]any{"query": query, "results": len(results)})
	return formatSearchResponse(results)
}

func (t *ToolsTool) searchRegex(pattern string) *ToolResult {
	if t.registry == nil {
		return SilentResult("No tools found matching the query.")
	}
	if len(pattern) > MaxRegexPatternLength {
		logger.WarnCF("discovery", "tools(search/regex): pattern too long",
			map[string]any{"len": len(pattern)})
		return ErrorResult(fmt.Sprintf("Pattern too long: max %d characters allowed", MaxRegexPatternLength))
	}

	logger.DebugCF("discovery", "tools(search/regex)", map[string]any{"pattern": pattern})

	_, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		logger.WarnCF("discovery", "tools(search/regex): invalid pattern",
			map[string]any{"pattern": pattern, "error": err.Error()})
		return ErrorResult(fmt.Sprintf("Invalid regex pattern syntax: %v. Please fix your regex and try again.", err))
	}

	results, err := t.registry.SearchRegex(pattern, t.maxSearchResults)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Invalid regex pattern syntax: %v. Please fix your regex and try again.", err))
	}
	if len(results) == 0 {
		return SilentResult("No tools found matching the query.")
	}

	logger.InfoCF("discovery", "tools(search/regex) completed",
		map[string]any{"pattern": pattern, "results": len(results)})
	return formatSearchResponse(results)
}

// formatSearchResponse serializes search results as JSON — READ-ONLY, no promote.
func formatSearchResponse(results []ToolSearchResult) *ToolResult {
	if len(results) == 0 {
		return SilentResult("No tools found matching the query.")
	}

	b, err := json.Marshal(results)
	if err != nil {
		return ErrorResult("tools(search): failed to format search results: " + err.Error())
	}

	msg := fmt.Sprintf(
		"Found %d tools:\n%s\n\nCall tools with action='load' and the name(s) to fetch their parameters and make them callable.",
		len(results),
		string(b),
	)
	return SilentResult(msg)
}

// ── load action ──────────────────────────────────────────────────────────────

func (t *ToolsTool) execLoad(ctx context.Context, args map[string]any) *ToolResult {
	if t.canLoad == nil || t.markLoaded == nil {
		return ErrorResult("tools(load): resolver not set — internal configuration error")
	}

	// Parse names array.
	raw, ok := args["names"]
	if !ok {
		return ErrorResult("tools(load): missing required parameter 'names'")
	}
	var names []string
	switch v := raw.(type) {
	case []string:
		names = v
	case []any:
		names = make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return ErrorResult(fmt.Sprintf("tools(load): 'names' must be an array of strings, got element of type %T", item))
			}
			names = append(names, s)
		}
	default:
		return ErrorResult(fmt.Sprintf("tools(load): 'names' must be an array of strings, got %T", raw))
	}

	if len(names) == 0 {
		return ErrorResult("tools(load): 'names' must have at least 1 element")
	}

	// Validate each name against the agent's policy-filtered registry.
	accepted := make([]string, 0, len(names))
	preRejected := make([]string, 0)
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			preRejected = append(preRejected, "(empty string) — invalid name")
			continue
		}
		if !t.canLoad(ctx, n) {
			preRejected = append(preRejected, n+" — unknown or not available for this agent")
			continue
		}
		accepted = append(accepted, n)
	}

	if len(accepted) == 0 {
		msg := fmt.Sprintf("tools(load): no valid tools to load. Rejected: %s", strings.Join(preRejected, "; "))
		return ErrorResult(msg)
	}

	// Promote (un-hide hidden/MCP tools) AND mark loaded for in-process lazy tools.
	// Both are applied unconditionally:
	//   - PromoteTools is a no-op for already-visible tools.
	//   - markLoaded is harmless for already-promoted tools.
	// Together they make both hidden-MCP and in-process-lazy tools callable.
	if t.registry != nil {
		t.registry.PromoteTools(accepted, t.ttl)
		logger.InfoCF("discovery", "tools(load): promoted tools",
			map[string]any{"tools": accepted, "ttl": t.ttl})
	}

	schemas, postRejected := t.markLoaded(ctx, accepted)

	// Merge all rejected names.
	allRejected := append(preRejected, postRejected...)

	// Compute the final loaded set.
	loadedNames := make([]string, 0, len(schemas))
	for n := range schemas {
		loadedNames = append(loadedNames, n)
	}

	if len(loadedNames) == 0 {
		msg := fmt.Sprintf("tools(load): failed to load requested tools. Rejected: %s", strings.Join(allRejected, "; "))
		return ErrorResult(msg)
	}

	// Sort for determinism.
	sort.Strings(loadedNames)
	sort.Strings(allRejected)

	result := map[string]any{
		"loaded":   loadedNames,
		"schemas":  schemas,
		"rejected": allRejected,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return ErrorResult(fmt.Sprintf("tools(load): failed to encode result: %v", err))
	}

	return SilentResult(string(encoded))
}

// ── BM25 engine cache (mirrors former BM25SearchTool.getOrBuildEngine) ───────

// getOrBuildEngine returns a cached BM25 engine, rebuilding only when the
// registry version has changed. Lock-free fast path; cacheMu serializes rebuild.
func (t *ToolsTool) getOrBuildEngine() *bm25CachedEngine {
	if c := t.cached.Load(); c != nil && c.version == t.registry.Version() {
		return cachedEngineOrNil(c)
	}

	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()

	// Snapshot + version under a single registry RLock (no TOCTOU).
	snap := t.registry.SnapshotHiddenTools()

	// Re-check after acquiring cacheMu (another goroutine may have rebuilt).
	if c := t.cached.Load(); c != nil && c.version == snap.Version {
		return cachedEngineOrNil(c)
	}

	docs := snapshotToSearchDocs(snap)
	var cached *bm25CachedEngine
	if len(docs) == 0 {
		cached = &bm25CachedEngine{engine: nil, version: snap.Version}
	} else {
		cached = &bm25CachedEngine{engine: buildBM25Engine(docs), version: snap.Version}
		logger.DebugCF("discovery", "tools BM25 engine rebuilt",
			map[string]any{"docs": len(docs), "version": snap.Version})
	}
	t.cached.Store(cached)
	return cachedEngineOrNil(cached)
}
