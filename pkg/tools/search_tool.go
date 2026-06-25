package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/utils"
)

const (
	MaxRegexPatternLength = 200
)

type RegexSearchTool struct {
	BaseTool
	registry         *ToolRegistry
	ttl              int
	maxSearchResults int
}

func NewRegexSearchTool(r *ToolRegistry, ttl int, maxSearchResults int) *RegexSearchTool {
	return &RegexSearchTool{registry: r, ttl: ttl, maxSearchResults: maxSearchResults}
}

func (t *RegexSearchTool) Name() string {
	return "search_tools_regex"
}

func (t *RegexSearchTool) Description() string {
	return "Search available hidden tools on-demand using a regex pattern. Returns JSON schemas of discovered tools."
}

func (t *RegexSearchTool) Scope() ToolScope       { return ScopeGeneral }
func (t *RegexSearchTool) Category() ToolCategory { return CategoryToolDiscovery }

func (t *RegexSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to match tool name or description",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *RegexSearchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	pattern, ok := args["pattern"].(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		// An empty string regex (?i) will match every hidden tool,
		// dumping massive payloads into the context and burning tokens.
		return ErrorResult("Missing or invalid 'pattern' argument. Must be a non-empty string.")
	}

	if len(pattern) > MaxRegexPatternLength {
		logger.WarnCF("discovery", "Regex pattern rejected (too long)", map[string]any{"len": len(pattern)})
		return ErrorResult(fmt.Sprintf("Pattern too long: max %d characters allowed", MaxRegexPatternLength))
	}

	logger.DebugCF("discovery", "Regex search", map[string]any{"pattern": pattern})

	res, err := t.registry.SearchRegex(pattern, t.maxSearchResults)
	if err != nil {
		logger.WarnCF("discovery", "Invalid regex pattern", map[string]any{"pattern": pattern, "error": err.Error()})
		return ErrorResult(fmt.Sprintf("Invalid regex pattern syntax: %v. Please fix your regex and try again.", err))
	}

	logger.InfoCF("discovery", "Regex search completed", map[string]any{"pattern": pattern, "results": len(res)})
	return formatDiscoveryResponse(t.registry, res, t.ttl)
}

type BM25SearchTool struct {
	BaseTool
	registry         *ToolRegistry
	ttl              int
	maxSearchResults int

	// Cache: rebuilt only when the registry version changes. The cached value is
	// an immutable {engine, version} snapshot published via an atomic pointer so
	// the lock-free fast path in getOrBuildEngine is race-free; cacheMu serializes
	// only the (potentially expensive) rebuild.
	cacheMu sync.Mutex
	cached  atomic.Pointer[bm25CachedEngine]
}

func NewBM25SearchTool(r *ToolRegistry, ttl int, maxSearchResults int) *BM25SearchTool {
	return &BM25SearchTool{registry: r, ttl: ttl, maxSearchResults: maxSearchResults}
}

func (t *BM25SearchTool) Name() string {
	return "search_tools_bm25"
}

func (t *BM25SearchTool) Description() string {
	return "Search available hidden tools on-demand using natural language query describing the action you need to perform. Returns JSON schemas of discovered tools."
}

func (t *BM25SearchTool) Scope() ToolScope       { return ScopeGeneral }
func (t *BM25SearchTool) Category() ToolCategory { return CategoryToolDiscovery }

func (t *BM25SearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
		},
		"required": []string{"query"},
	}
}

func (t *BM25SearchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		// An empty string query will match every hidden tool,
		// dumping massive payloads into the context and burning tokens.
		return ErrorResult("Missing or invalid 'query' argument. Must be a non-empty string.")
	}

	logger.DebugCF("discovery", "BM25 search", map[string]any{"query": query})

	cached := t.getOrBuildEngine()
	if cached == nil {
		logger.DebugCF("discovery", "BM25 search: no hidden tools available", nil)
		return SilentResult("No tools found matching the query.")
	}

	ranked := cached.engine.Search(query, t.maxSearchResults)
	if len(ranked) == 0 {
		logger.DebugCF("discovery", "BM25 search: no matches", map[string]any{"query": query})
		return SilentResult("No tools found matching the query.")
	}

	results := make([]ToolSearchResult, len(ranked))
	for i, r := range ranked {
		results[i] = ToolSearchResult{
			Name:        r.Document.Name,
			Description: r.Document.Description,
		}
	}

	logger.InfoCF("discovery", "BM25 search completed", map[string]any{"query": query, "results": len(results)})
	return formatDiscoveryResponse(t.registry, results, t.ttl)
}

// ToolSearchResult represents the result returned to the LLM.
// Parameters are omitted from the JSON response to save context tokens;
// the LLM will see full schemas via ToProviderDefs after promotion.
type ToolSearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (r *ToolRegistry) SearchRegex(pattern string, maxSearchResults int) ([]ToolSearchResult, error) {
	if maxSearchResults <= 0 {
		return nil, nil
	}

	regex, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to compile regex pattern %q: %w", pattern, err)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var results []ToolSearchResult

	// Iterate in sorted order for deterministic results across calls.
	for _, name := range r.sortedToolNames() {
		entry := r.tools[name]
		// Search only among the hidden tools (Core tools are already visible)
		if !entry.IsCore {
			// Directly call interface methods! No reflection/unmarshalling needed.
			desc := entry.Tool.Description()

			if regex.MatchString(name) || regex.MatchString(desc) {
				results = append(results, ToolSearchResult{
					Name:        name,
					Description: desc,
				})
				if len(results) >= maxSearchResults {
					break // Stop searching once we hit the max! Saves CPU.
				}
			}
		}
	}

	return results, nil
}

func formatDiscoveryResponse(registry *ToolRegistry, results []ToolSearchResult, ttl int) *ToolResult {
	if len(results) == 0 {
		return SilentResult("No tools found matching the query.")
	}

	names := make([]string, len(results))
	for i, r := range results {
		names[i] = r.Name
	}
	registry.PromoteTools(names, ttl)
	logger.InfoCF("discovery", "Promoted tools", map[string]any{"tools": names, "ttl": ttl})

	b, err := json.Marshal(results)
	if err != nil {
		return ErrorResult("Failed to format search results: " + err.Error())
	}

	msg := fmt.Sprintf(
		"Found %d tools:\n%s\n\nSUCCESS: These tools have been temporarily UNLOCKED as native tools! In your next response, you can call them directly just like any normal tool",
		len(results),
		string(b),
	)

	return SilentResult(msg)
}

// Lightweight internal type used as corpus document for BM25.
type searchDoc struct {
	Name        string
	Description string
}

// bm25CachedEngine wraps a BM25Engine with the registry version it was built
// from. Instances are immutable once published to BM25SearchTool.cached, so they
// can be read lock-free. A nil engine means "no hidden tools at this version"
// (cached so an empty registry isn't re-snapshotted every call).
type bm25CachedEngine struct {
	engine  *utils.BM25Engine[searchDoc]
	version uint64
}

// snapshotToSearchDocs converts a HiddenToolSnapshot to BM25 searchDoc slice.
func snapshotToSearchDocs(snap HiddenToolSnapshot) []searchDoc {
	docs := make([]searchDoc, len(snap.Docs))
	for i, d := range snap.Docs {
		docs[i] = searchDoc{Name: d.Name, Description: d.Description}
	}
	return docs
}

// buildBM25Engine creates a BM25Engine from a slice of searchDocs.
func buildBM25Engine(docs []searchDoc) *utils.BM25Engine[searchDoc] {
	return utils.NewBM25Engine(
		docs,
		func(doc searchDoc) string {
			return doc.Name + " " + doc.Description
		},
	)
}

// getOrBuildEngine returns a cached BM25 engine, rebuilding it only when
// the registry version has changed (new tools registered).
func (t *BM25SearchTool) getOrBuildEngine() *bm25CachedEngine {
	// Fast path: atomic load, no lock. The cached value is immutable once stored.
	if c := t.cached.Load(); c != nil && c.version == t.registry.Version() {
		return cachedEngineOrNil(c)
	}

	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()

	// Snapshot + version are read under a single registry RLock,
	// guaranteeing consistency (no TOCTOU).
	snap := t.registry.SnapshotHiddenTools()

	// Re-check: another goroutine may have rebuilt while we waited for cacheMu.
	if c := t.cached.Load(); c != nil && c.version == snap.Version {
		return cachedEngineOrNil(c)
	}

	docs := snapshotToSearchDocs(snap)
	var cached *bm25CachedEngine
	if len(docs) == 0 {
		cached = &bm25CachedEngine{engine: nil, version: snap.Version}
	} else {
		cached = &bm25CachedEngine{engine: buildBM25Engine(docs), version: snap.Version}
		logger.DebugCF("discovery", "BM25 engine rebuilt", map[string]any{"docs": len(docs), "version": snap.Version})
	}
	t.cached.Store(cached)
	return cachedEngineOrNil(cached)
}

// cachedEngineOrNil returns c only when it holds a usable engine; a cached entry
// with a nil engine (empty registry at that version) yields nil so the caller's
// "no tools" path fires without dereferencing a nil engine.
func cachedEngineOrNil(c *bm25CachedEngine) *bm25CachedEngine {
	if c == nil || c.engine == nil {
		return nil
	}
	return c
}

// SearchBM25 ranks hidden tools against query using BM25 via utils.BM25Engine.
// This non-cached variant rebuilds the engine on every call. Used by tests
// and any code that doesn't hold a BM25SearchTool instance.
func (r *ToolRegistry) SearchBM25(query string, maxSearchResults int) []ToolSearchResult {
	snap := r.SnapshotHiddenTools()
	docs := snapshotToSearchDocs(snap)
	if len(docs) == 0 {
		return nil
	}

	ranked := buildBM25Engine(docs).Search(query, maxSearchResults)
	if len(ranked) == 0 {
		return nil
	}

	out := make([]ToolSearchResult, len(ranked))
	for i, r := range ranked {
		out[i] = ToolSearchResult{
			Name:        r.Document.Name,
			Description: r.Document.Description,
		}
	}
	return out
}
