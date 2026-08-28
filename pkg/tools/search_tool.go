package tools

import (
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// ToolSearchResult represents the result returned to the LLM.
// Parameters are omitted from the JSON response to save context tokens;
// the LLM will see full schemas via ToProviderDefs after promotion.
type ToolSearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
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
//
// docCount (ADR-071 §3.2.2) is the corpus size at build time — the number of
// documents backing engine. execSearchAndLoad passes it as Search's topK so
// ranking runs over the WHOLE corpus rather than just the first
// maxSearchResults, which the policy filter needs to build a truthful
// policy-loadable list. This costs nothing extra: BM25Engine's own doc
// comment states all indexing work happens inside Search() on every call
// regardless of topK — topK only sizes the final min-heap extraction.
type bm25CachedEngine struct {
	engine   *utils.BM25Engine[searchDoc]
	version  uint64
	docCount int
}

// snapshotToSearchDocs converts a HiddenToolSnapshot to BM25 searchDoc slice.
func snapshotToSearchDocs(snap HiddenToolSnapshot) []searchDoc {
	docs := make([]searchDoc, len(snap.Docs))
	for i, d := range snap.Docs {
		docs[i] = searchDoc(d)
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

// SearchBM25 ranks searchable tools against query using BM25 via utils.BM25Engine.
// The corpus includes hidden/MCP tools AND visible lazy-tier built-in tools,
// matching the widened corpus used by ToolsTool.getOrBuildEngine.
// This non-cached variant rebuilds the engine on every call. Used by tests
// and any code that doesn't hold a ToolsTool instance.
func (r *ToolRegistry) SearchBM25(query string, maxSearchResults int) []ToolSearchResult {
	snap := r.SnapshotSearchableTools()
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
