// Omnipus — ToolSearch: unified discovery and load infra tool (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ToolsTool provides the unified tool discovery and loading infrastructure.
// It is the single infra tool that replaces the former search_tools_bm25
// and search_tools_regex tools. Intent is inferred from which parameter is present:
//
//   - ToolSearch{ names: [string] } — LOAD those tools: fetch schemas + make callable.
//   - ToolSearch{ query: string }   — SEARCH (BM25) over the hidden/lazy corpus,
//     auto-load the top hit, return schemas + full match list.
//
// See docs/internal/design/unified-tools-tool-2026-06.md for the full spec.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// CanLoadAlreadyAvailablePrefix is a typed sentinel prefix returned by the
// agent-loop canLoad callback when a full/infra tier tool is policy-allowed
// but does not need to be loaded (it is always callable). execLoad compares
// against this constant instead of a raw substring to eliminate cross-package
// prose coupling. The prefix is followed by a human-readable explanation.
//
// loop.go builds the reason as: CanLoadAlreadyAvailablePrefix + " — just call it directly"
// execLoad recognizes it via: strings.HasPrefix(reason, CanLoadAlreadyAvailablePrefix)
const CanLoadAlreadyAvailablePrefix = "already available"

// ToolsTool is the unified tool-discovery and load infra tool.
//
// It holds the BM25 engine cache (moved from the former BM25SearchTool) so
// the cache survives across query calls on the same instance.
type ToolsTool struct {
	BaseTool
	registry         *ToolRegistry
	ttl              int
	maxSearchResults int
	// canLoad reports whether name is loadable for the calling agent.
	// Returns (true, "") when loading is allowed.
	// Returns (false, reason) when loading is denied; reason describes why
	// (e.g. "denied by policy", "unknown tool (did you mean 'X'?)").
	canLoad    func(ctx context.Context, name string) (ok bool, reason string)
	markLoaded func(ctx context.Context, names []string) (loaded map[string]any, rejected []string)

	// BM25 engine cache (version-keyed, atomic-pointer + mutex for rebuild).
	cacheMu sync.Mutex
	cached  atomic.Pointer[bm25CachedEngine]
}

// NewToolsTool constructs the unified tools tool. SetResolver must be called
// before any names-path Execute call (query/search works without a resolver).
func NewToolsTool(r *ToolRegistry, ttl int, maxSearchResults int) *ToolsTool {
	return &ToolsTool{registry: r, ttl: ttl, maxSearchResults: maxSearchResults}
}

// SetResolver wires the per-agent, per-session callbacks required by the load path.
// Identical contract to the former LoadTool.SetResolver.
//
// canLoad must return (true, "") when the tool is loadable, or (false, reason)
// when it is not. The reason string is surfaced verbatim in error messages.
func (t *ToolsTool) SetResolver(
	canLoad func(ctx context.Context, name string) (ok bool, reason string),
	markLoaded func(ctx context.Context, names []string) (map[string]any, []string),
) {
	t.canLoad = canLoad
	t.markLoaded = markLoaded
}

func (t *ToolsTool) Name() string           { return "ToolSearch" }
func (t *ToolsTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ToolsTool) Category() ToolCategory { return CategoryToolDiscovery }

func (t *ToolsTool) Description() string {
	return "Find and load tools so you can call them. " +
		"To load tools you know by name, pass 'names'. " +
		"To find a tool by what it does, pass 'query' — the best match is loaded automatically. " +
		"After loading, call the tool directly."
}

func (t *ToolsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"names": map[string]any{
				"type":        "array",
				"description": "Tool name(s) to load by exact name. When present (and non-empty), the load path runs; 'query' is ignored.",
				"items": map[string]any{
					"type": "string",
				},
				"minItems": 1,
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Describe what the tool does (BM25 search). Used when 'names' is absent or empty. The best match is loaded automatically.",
			},
		},
	}
}

// Execute dispatches based on which parameter is present.
// Precedence: names (non-empty) → load path; query (non-empty) → search+auto-load path; else error.
func (t *ToolsTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	// Parse names — may be []string or []any.
	names, namesErr := parseNamesArg(args)
	if namesErr != nil {
		return ErrorResult(fmt.Sprintf("ToolSearch: 'names' must be an array of strings: %v", namesErr))
	}

	// Precedence: names present and non-empty → load path.
	if len(names) > 0 {
		return t.execLoad(ctx, names)
	}

	// Next: query present and non-empty → search+auto-load path.
	query, _ := args["query"].(string)
	if strings.TrimSpace(query) != "" {
		return t.execSearchAndLoad(ctx, query)
	}

	// Neither provided.
	return ErrorResult(
		"ToolSearch: provide either 'names' (to load tools by name) or 'query' (to find a tool by what it does)",
	)
}

// ── search+auto-load path ────────────────────────────────────────────────────

func (t *ToolsTool) execSearchAndLoad(ctx context.Context, query string) *ToolResult {
	if t.registry == nil {
		return SilentResult("No tools found matching the query.")
	}
	logger.DebugCF("discovery", "ToolSearch(query/bm25)", map[string]any{"query": query})

	cached := t.getOrBuildEngine()
	if cached == nil {
		logger.DebugCF("discovery", "ToolSearch(query/bm25): no hidden tools available", nil)
		return SilentResult("No tools found matching the query.")
	}

	ranked := cached.engine.Search(query, t.maxSearchResults)
	if len(ranked) == 0 {
		logger.DebugCF("discovery", "ToolSearch(query/bm25): no matches", map[string]any{"query": query})
		return SilentResult("No tools found matching the query.")
	}

	// Build the full match list (name + description) for all ranked results.
	matches := make([]ToolSearchResult, len(ranked))
	for i, r := range ranked {
		matches[i] = ToolSearchResult{
			Name:        r.Document.Name,
			Description: r.Document.Description,
		}
	}

	logger.InfoCF("discovery", "ToolSearch(query/bm25) completed",
		map[string]any{"query": query, "results": len(matches)})

	// Auto-load the top hit. Walk the ranked list until one passes canLoad.
	var loadedName string
	var loadedSchema map[string]any
	var schemas map[string]any

	if t.canLoad != nil && t.markLoaded != nil {
		for _, m := range matches {
			name := m.Name
			if ok, reason := t.canLoad(ctx, name); !ok {
				logger.DebugCF("discovery", "ToolSearch(query): top hit not loadable, trying next",
					map[string]any{"name": name, "reason": reason})
				continue
			}
			// Promote hidden/MCP tool so it enters GetAll().
			if t.registry != nil {
				t.registry.PromoteTools([]string{name}, t.ttl)
			}
			loaded, _ := t.markLoaded(ctx, []string{name})
			if schema, ok := loaded[name]; ok {
				loadedName = name
				loadedSchema = map[string]any{name: schema}
				schemas = loadedSchema
				break
			}
		}
	}

	// Build response JSON.
	resp := map[string]any{
		"matches": matches,
	}
	if loadedName != "" {
		resp["loaded"] = []string{loadedName}
		resp["schemas"] = schemas
	}

	encoded, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(fmt.Sprintf("ToolSearch(query): failed to encode result: %v", err))
	}

	var msg string
	if loadedName != "" {
		msg = fmt.Sprintf(
			"Found %d tools. Loaded the best match '%s' (schema included) — call it now, or load a different one by passing its name in 'names'.\n%s",
			len(matches),
			loadedName,
			string(encoded),
		)
	} else {
		// No resolver or all results denied by policy.
		b, _ := json.Marshal(matches)
		msg = fmt.Sprintf(
			"Found %d tools:\n%s\n\nTo use one, call `ToolSearch` with its exact name in `names` (or describe what you need in `query`) to load it, then call it.",
			len(matches),
			string(b),
		)
	}

	return SilentResult(msg)
}

// ── load path ─────────────────────────────────────────────────────────────────

func (t *ToolsTool) execLoad(ctx context.Context, names []string) *ToolResult {
	if t.canLoad == nil || t.markLoaded == nil {
		return ErrorResult("ToolSearch(load): resolver not set — internal configuration error")
	}

	// Full-tier and infra tools are already in the model's callable set — loading
	// them is a no-op success (C2 fix: avoid confusing the model with an error).
	// BUT we must first verify policy: a policy-denied full-tier tool must return
	// an error (F4 fix). We call canLoad to check policy; the real agent-loop
	// canLoad returns (false, "already available…") for allowed full-tier tools
	// and (false, "denied…") for policy-denied ones. We interpret the reason to
	// distinguish the two cases. (canLoad always returns false for full-tier since
	// these tools are never truly "loaded" — they are always present.)
	var fullTierHints []string
	var fullTierRejections []string
	var lazyNames []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			lazyNames = append(lazyNames, n) // will be rejected below as empty
			continue
		}
		tier := ToolManifestTier(n)
		if tier == ManifestFull || tier == ManifestInfra {
			ok, reason := t.canLoad(ctx, n)
			// The real agent-loop canLoad returns (false, CanLoadAlreadyAvailablePrefix+"…")
			// for policy-allowed full-tier tools and (false, "…denied…") for denied ones.
			// Unit-test canLoads may return (true, "") for the allowed case.
			// Treat either (true, _) or (false, CanLoadAlreadyAvailablePrefix) as no-op success;
			// everything else is a policy denial.
			if ok || strings.HasPrefix(reason, CanLoadAlreadyAvailablePrefix) {
				fullTierHints = append(
					fullTierHints,
					fmt.Sprintf("'%s' is already available — just call it directly", n),
				)
			} else {
				// Denied by policy or genuinely unknown at the full-tier level.
				fullTierRejections = append(fullTierRejections, reason)
			}
		} else {
			lazyNames = append(lazyNames, n)
		}
	}

	// If every requested name was full-tier:
	//   - All allowed → no-op success.
	//   - Some denied → error listing all rejections.
	//   - Mix of allowed + denied → surface the denials; the allowed ones are
	//     described in the hints but we still return an error so the model knows
	//     about the denied tools.
	if len(lazyNames) == 0 {
		if len(fullTierRejections) > 0 {
			// Include any "already available" hints so the model still knows
			// about the tools it CAN call.
			allMsgs := append(fullTierRejections, fullTierHints...)
			return ErrorResult(fmt.Sprintf("ToolSearch(load): %s", strings.Join(allMsgs, "; ")))
		}
		if len(fullTierHints) > 0 {
			return SilentResult(strings.Join(fullTierHints, "\n"))
		}
	}

	// Validate each lazy name against the agent's policy-filtered registry.
	// Seed preRejected with any full-tier denials so a mixed batch (denied
	// full-tier + allowed lazy) always surfaces the denial (Fix B).
	accepted := make([]string, 0, len(lazyNames))
	preRejected := make([]string, 0, len(fullTierRejections))
	preRejected = append(preRejected, fullTierRejections...)
	for _, n := range lazyNames {
		if n == "" {
			preRejected = append(preRejected, "(empty string) — invalid name")
			continue
		}
		if ok, reason := t.canLoad(ctx, n); !ok {
			preRejected = append(preRejected, reason)
			continue
		}
		accepted = append(accepted, n)
	}

	if len(accepted) == 0 {
		msg := fmt.Sprintf("ToolSearch(load): no valid tools to load. Rejected: %s", strings.Join(preRejected, "; "))
		return ErrorResult(msg)
	}

	// Promote (un-hide hidden/MCP tools) AND mark loaded for in-process lazy tools.
	// Both are applied unconditionally:
	//   - PromoteTools is a no-op for already-visible tools.
	//   - markLoaded is harmless for already-promoted tools.
	// Together they make both hidden-MCP and in-process-lazy tools callable.
	if t.registry != nil {
		t.registry.PromoteTools(accepted, t.ttl)
		logger.InfoCF("discovery", "ToolSearch(load): promoted tools",
			map[string]any{"tools": accepted, "ttl": t.ttl})
	}

	schemas, postRejected := t.markLoaded(ctx, accepted)

	// Merge all rejected names (includes full-tier denials seeded above).
	allRejected := append(preRejected, postRejected...)

	// Compute the final loaded set.
	loadedNames := make([]string, 0, len(schemas))
	for n := range schemas {
		loadedNames = append(loadedNames, n)
	}

	if len(loadedNames) == 0 {
		msg := fmt.Sprintf(
			"ToolSearch(load): failed to load requested tools. Rejected: %s",
			strings.Join(allRejected, "; "),
		)
		return ErrorResult(msg)
	}

	// Sort for determinism.
	sort.Strings(loadedNames)
	sort.Strings(allRejected)

	// Include full-tier "already available" acknowledgements alongside the
	// loaded lazy tools so the model learns those names are callable too.
	alreadyAvailable := make([]string, len(fullTierHints))
	copy(alreadyAvailable, fullTierHints)

	result := map[string]any{
		"loaded":            loadedNames,
		"schemas":           schemas,
		"rejected":          allRejected,
		"already_available": alreadyAvailable,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return ErrorResult(fmt.Sprintf("ToolSearch(load): failed to encode result: %v", err))
	}

	return SilentResult(string(encoded))
}

// ── BM25 engine cache (mirrors former BM25SearchTool.getOrBuildEngine) ───────

// getOrBuildEngine returns a cached BM25 engine, rebuilding only when the
// registry version has changed. Lock-free fast path; cacheMu serializes rebuild.
//
// The corpus now includes BOTH hidden tools (RegisterHidden) AND visible lazy
// built-in tools (Register + ManifestLazy tier) so that a BM25 query can find
// any tool the model needs to load — not just hidden/MCP tools.
func (t *ToolsTool) getOrBuildEngine() *bm25CachedEngine {
	if c := t.cached.Load(); c != nil && c.version == t.registry.Version() {
		return cachedEngineOrNil(c)
	}

	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()

	// Snapshot + version under a single registry RLock (no TOCTOU).
	// SnapshotSearchableTools includes hidden tools AND visible lazy-tier tools.
	snap := t.registry.SnapshotSearchableTools()

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
		logger.DebugCF("discovery", "ToolSearch BM25 engine rebuilt",
			map[string]any{"docs": len(docs), "version": snap.Version})
	}
	t.cached.Store(cached)
	return cachedEngineOrNil(cached)
}

// parseNamesArg extracts the 'names' field from args, accepting []string or []any.
// Returns (nil, nil) when the field is absent. Returns an error only when the field
// is present but has the wrong type or contains a non-string element.
func parseNamesArg(args map[string]any) ([]string, error) {
	raw, ok := args["names"]
	if !ok || raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case []string:
		return v, nil
	case []any:
		names := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("element of type %T is not a string", item)
			}
			names = append(names, s)
		}
		return names, nil
	default:
		return nil, fmt.Errorf("expected array of strings, got %T", raw)
	}
}
