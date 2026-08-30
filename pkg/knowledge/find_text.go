// Omnipus — ADR-068 D15.3: adapts *Index to knowledge_find's TextSearcher
// seam (pkg/records/knowledgefind.TextSearcher), the bleve half of a query.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ---------------------------------------------------------------------------
// WHY THIS LIVES IN pkg/knowledge, NOT pkg/gateway
//
// The other two knowledge_find dependencies a host must build — the
// properties-index Store and the RelationResolver — are built in
// pkg/vaultprops, which imports BOTH pkg/knowledge and pkg/records/propindex.
// pkg/knowledge cannot import pkg/vaultprops back (that is the cycle
// pkg/vaultprops's own doc comment explains), so the TOOL ADAPTER that wires
// all of knowledge_find's dependencies together lives in pkg/gateway, which
// already depends on every package involved.
//
// This ONE dependency is different: term search, near-miss vocabulary and
// source-hash lookup are pure *Index operations with no properties-index or
// relation-resolution involved, and vocabulary.go's prefixTerms (the term
// dictionary reader with per-term counts) is unexported — reachable only from
// inside this package. So the adapter for THIS ONE seam is built here, as an
// ordinary exported type, and pkg/gateway's wiring simply constructs one and
// hands it to knowledgefind.Deps.Text. It imports pkg/records/knowledgefind
// only for its two small result types (TextHit, and generated.VaultTermCount
// via pkg/api/generated) — knowledgefind imports neither pkg/knowledge nor
// pkg/tools, so this import is acyclic and does not reach a language model,
// consistent with this package's tool-adapter/logic split (see
// knowledge_describe.go's header for that guard).
// ---------------------------------------------------------------------------

package knowledge

import (
	"context"
	"fmt"
	"sort"

	bleveIndexAPI "github.com/blevesearch/bleve_index_api"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
)

// FindTextSearcher adapts one already-open *Index to
// knowledgefind.TextSearcher. The caller owns the Index's lifetime (opened
// and closed exactly as knowledge_search and knowledge_graph already do
// around one call), so this type holds no state of its own beyond the
// pointer.
type FindTextSearcher struct {
	ix *Index
}

// NewFindTextSearcher wraps an already-open index.
func NewFindTextSearcher(ix *Index) *FindTextSearcher {
	return &FindTextSearcher{ix: ix}
}

// Compile-time proof that *FindTextSearcher satisfies knowledgefind's own
// interface, so a signature drift on either side fails the build instead of
// surfacing as a runtime assignment error at the one call site that wires it.
var _ knowledgefind.TextSearcher = (*FindTextSearcher)(nil)

// Search is the plain-word half of a query, ranked.
//
// knowledge_find's PathPrefix scoping (FR-060) is applied by the CALLER over
// propindex's Selector, not here — this collection's *Index already IS one
// workspace's one mounted collection (the same per-collection open every
// other knowledge_* tool performs), so there is no narrower boundary for a
// text search within it to enforce.
func (s *FindTextSearcher) Search(_ context.Context, words string, limit int) ([]knowledgefind.TextHit, error) {
	if s == nil || s.ix == nil {
		return nil, fmt.Errorf("knowledge: FindTextSearcher.Search called with no index open")
	}
	hits, err := s.ix.Search(words, limit)
	if err != nil {
		return nil, err
	}
	out := make([]knowledgefind.TextHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, knowledgefind.TextHit{
			Path:       h.Path,
			SourceHash: h.SourceHash,
			Score:      h.Score,
		})
	}
	return out, nil
}

// SourceHash returns the hash the TEXT index holds for one note, for
// FR-020c's per-row freshness comparison against the properties index.
//
// fieldPath is mapped with bleve's "keyword" analyzer (index.go's
// buildIndexMapping: "pathField.Analyzer = keyword"), so a MatchQuery on it
// is a whole-string exact match, not a tokenised/fuzzy one — the same
// mechanism SearchField already uses for every other exact field lookup in
// this package.
func (s *FindTextSearcher) SourceHash(_ context.Context, path string) (string, bool, error) {
	if s == nil || s.ix == nil {
		return "", false, fmt.Errorf("knowledge: FindTextSearcher.SourceHash called with no index open")
	}
	hits, err := s.ix.SearchField(fieldPath, path, 1)
	if err != nil {
		return "", false, err
	}
	if len(hits) == 0 {
		return "", false, nil
	}
	return hits[0].SourceHash, true, nil
}

// NearestTerms reports the vocabulary the index actually holds near a term
// that matched nothing (FR-114), WITH document counts — the one respect in
// which this cannot simply call NearMissVocabulary (vocabulary.go), whose
// contract deliberately discards the count bleve's term dictionary already
// carries on every entry. Rather than have two independent walks of the same
// term dictionary that could silently diverge, this shares NearMissVocabulary's
// exact prefix strategy (foldTokens, vocabularyPrefixMin, the same three
// fields) and asks the dictionary for the count field prefixTerms already
// throws away.
func (s *FindTextSearcher) NearestTerms(_ context.Context, words string, limit int) ([]generated.VaultTermCount, error) {
	if s == nil || s.ix == nil {
		return nil, fmt.Errorf("knowledge: FindTextSearcher.NearestTerms called with no index open")
	}
	if limit <= 0 {
		limit = VocabularySuggestionLimit
	}
	terms := foldTokens(words)
	if len(terms) == 0 {
		return nil, nil
	}

	internal, err := s.ix.idx.Advanced()
	if err != nil {
		return nil, fmt.Errorf("knowledge: vocabulary index: %w", err)
	}
	reader, err := internal.Reader()
	if err != nil {
		return nil, fmt.Errorf("knowledge: vocabulary reader: %w", err)
	}
	defer func() {
		if cerr := reader.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	seen := make(map[string]bool, limit)
	counts := make(map[string]int, limit)
	order := make([]string, 0, limit)

	for _, term := range terms {
		if len(term) < vocabularyPrefixMin {
			continue
		}
		prefix := term
		if len(prefix) > vocabularyPrefixMin+2 {
			prefix = prefix[:vocabularyPrefixMin+2]
		}
		for _, field := range []string{fieldBody, fieldTitle, fieldName} {
			found, ferr := prefixTermCounts(reader, field, prefix, limit)
			if ferr != nil {
				return nil, ferr
			}
			for _, f := range found {
				if f.Term == term || seen[f.Term] {
					continue
				}
				seen[f.Term] = true
				counts[f.Term] += f.Count
				order = append(order, f.Term)
			}
		}
	}

	sort.Strings(order)
	if len(order) > limit {
		order = order[:limit]
	}
	out := make([]generated.VaultTermCount, 0, len(order))
	for _, term := range order {
		out = append(out, generated.VaultTermCount{Term: term, Documents: counts[term]})
	}
	return out, err
}

// termCount is one term dictionary entry, kept local to this file — the
// count is what distinguishes this walk from vocabulary.go's prefixTerms,
// which this function deliberately does not call for that reason.
type termCount struct {
	Term  string
	Count int
}

// prefixTermCounts is prefixTerms (vocabulary.go) with the count field it
// discards preserved. Both read the same dictionary the same way; keeping
// this as a sibling rather than editing prefixTerms itself avoids changing a
// function knowledge_search's zero-hit path already depends on.
func prefixTermCounts(reader bleveIndexAPI.IndexReader, field, prefix string, limit int) (out []termCount, err error) {
	dict, err := reader.FieldDictPrefix(field, []byte(prefix))
	if err != nil {
		return nil, fmt.Errorf("knowledge: term dictionary for %q: %w", field, err)
	}
	defer func() {
		if cerr := dict.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("knowledge: close term dictionary for %q: %w", field, cerr)
			out = nil
		}
	}()
	for len(out) < limit {
		entry, nerr := dict.Next()
		if nerr != nil {
			return nil, fmt.Errorf("knowledge: read term dictionary for %q: %w", field, nerr)
		}
		if entry == nil {
			break
		}
		out = append(out, termCount{Term: entry.Term, Count: int(entry.Count)})
	}
	return out, nil
}
