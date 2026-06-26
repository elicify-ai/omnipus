package tools

import (
	"fmt"
	"regexp"

	"github.com/dapicom-ai/omnipus/pkg/utils"
)

const (
	MaxRegexPatternLength = 200
)

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

// Lightweight internal type used as corpus document for BM25.
type searchDoc struct {
	Name        string
	Description string
}

// bm25CachedEngine wraps a BM25Engine with the registry version it was built
// from. Instances are immutable once published via atomic pointer, so they can
// be read lock-free. A nil engine means "no hidden tools at this version"
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
// and any code that doesn't hold a ToolsTool instance.
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
