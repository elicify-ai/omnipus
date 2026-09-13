// Omnipus — ADR-068 D15.3: the *Index operations knowledge_find's text
// search needs, in KNOWLEDGE-NATIVE types.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ---------------------------------------------------------------------------
// WHY THIS FILE RETURNS KNOWLEDGE-NATIVE TYPES, NOT knowledgefind's OWN
//
// An earlier version of this file imported pkg/records/knowledgefind
// directly and returned its TextHit/generated.VaultTermCount types, so the
// resulting adapter satisfied knowledgefind.TextSearcher with no glue code
// at the one call site that wires it. That does not compile: knowledgefind
// (find.go) imports pkg/records/propindex, and propindex's own IN-PACKAGE
// test file (memory_both_test.go) imports pkg/knowledge — so
// pkg/knowledge -> pkg/records/knowledgefind -> pkg/records/propindex ->
// (test build only) -> pkg/knowledge is a real cycle, caught only by
// `go vet`/`go test`, not by a normal build. This is the exact shape
// pkg/vaultprops/reader.go's header already documents for the
// PropertyIndexReader seam; it recurs here for the same reason.
//
// So the interface-shaped adapter (whose method signatures must literally
// say knowledgefind.TextSearcher's own return types) lives in pkg/gateway,
// which already depends on every package involved with no such cycle — see
// knowledge_find_tool.go's header there. This file supplies the three
// *Index operations that adapter wraps, using only pkg/knowledge's own
// types, so pkg/knowledge itself imports neither knowledgefind nor its
// generated wire types.
// ---------------------------------------------------------------------------

package knowledge

import (
	"fmt"
	"sort"

	bleveIndexAPI "github.com/blevesearch/bleve_index_api"
)

// TermCount is one term the index's dictionary holds, with how many
// documents contain it — vocabulary.go's NearMissVocabulary discards this
// count on purpose (D21.4: it is not offered as a query suggestion); this
// file's callers need it because knowledge_find's NearestTerms answer
// carries it (VaultTermCount.Documents: "how many indexed notes contain
// it").
type TermCount struct {
	Term      string
	Documents int
}

// SourceHashForPath returns the hash the TEXT index holds for one note, by
// its collection-relative path, for FR-020c's per-row freshness comparison
// against the properties index.
//
// fieldPath is mapped with bleve's "keyword" analyzer (index.go's
// buildIndexMapping: "pathField.Analyzer = keyword"), so a MatchQuery on it
// is a whole-string exact match, not a tokenised/fuzzy one — the same
// mechanism SearchField already uses for every other exact field lookup in
// this package.
func (ix *Index) SourceHashForPath(path string) (hash string, ok bool, err error) {
	if ix == nil {
		return "", false, fmt.Errorf("knowledge: SourceHashForPath called with no index open")
	}
	hits, err := ix.SearchField(fieldPath, path, 1)
	if err != nil {
		return "", false, err
	}
	if len(hits) == 0 {
		return "", false, nil
	}
	return hits[0].SourceHash, true, nil
}

// NearMissVocabularyWithCounts is NearMissVocabulary (vocabulary.go) with the
// document-count field its own contract deliberately discards preserved —
// see TermCount's doc for why this is a separate function rather than a
// change to that one's return shape (knowledge_search's zero-hit path
// depends on NearMissVocabulary's existing, narrower contract).
//
// Otherwise identical: the same shallow shared-prefix strategy over the
// same three fields (body, title, name), the same
// vocabularyPrefixMin/VocabularySuggestionLimit constants.
func (ix *Index) NearMissVocabularyWithCounts(query string, limit int) ([]TermCount, error) {
	if ix == nil {
		return nil, fmt.Errorf("knowledge: NearMissVocabularyWithCounts called with no index open")
	}
	if limit <= 0 {
		limit = VocabularySuggestionLimit
	}
	terms := foldProseTerms(foldTokens(query))
	if len(terms) == 0 {
		return nil, nil
	}

	internal, err := ix.idx.Advanced()
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
				counts[f.Term] += f.Documents
				order = append(order, f.Term)
			}
		}
	}

	sort.Strings(order)
	if len(order) > limit {
		order = order[:limit]
	}
	out := make([]TermCount, 0, len(order))
	for _, term := range order {
		out = append(out, TermCount{Term: term, Documents: counts[term]})
	}
	return out, err
}

// prefixTermCounts is vocabulary.go's prefixTerms with the count field it
// discards preserved. Both read the same dictionary the same way; keeping
// this as a sibling rather than editing prefixTerms itself avoids changing a
// function knowledge_search's zero-hit path already depends on.
func prefixTermCounts(reader bleveIndexAPI.IndexReader, field, prefix string, limit int) (out []TermCount, err error) {
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
		out = append(out, TermCount{Term: entry.Term, Documents: int(entry.Count)})
	}
	return out, nil
}

// TermDocumentCounts reports, for every word of a free-text query, how many
// distinct files the index holds that contain that word on its own — the
// per-term breakdown a caller needs to DECLARE a relaxed (OR-ranked) answer
// rather than present it as an exact one (UAT 2026-09-13, D-07 / D-01).
//
// WHY IT EXISTS. searchRaw tries a strict AND tier first and, when that
// finds nothing, silently falls back to an OR-ranked, typo-tolerant tier
// (KB-7a). The fallback is a deliberate design, but the UAT showed its
// silence is not: `words: "Collision zzqqxx"` — one word present, one
// absent from the whole vault — answered "COMPLETE: yes — 1 of 1 shown",
// and a single-word query answered a near-spelling (edit-distance 1) match
// as if it were exact. A caller reading either concluded every term was
// found. The hit-level FallbackMode flag says WHICH tier answered; this
// says what each word contributed, so the disclosure can be concrete
// ("Collision: 1, zzqqxx: 0") instead of a bare "loosened".
//
// Each term is counted with the SAME per-term disjunction buildAndQuery
// uses for one term (exact match across every text field plus the prose
// prefix pass), so the numbers are the AND tier's own per-term view — never
// a fuzzy count, which would restate the relaxation it is meant to expose.
// A count is the number of FILES (segments collapsed), not segments, so it
// matches what the caller sees as rows.
//
// The order is the query's own token order, deduplicated, and a query that
// tokenizes to nothing returns (nil, nil).
func (ix *Index) TermDocumentCounts(query string) ([]TermCount, error) {
	if ix == nil {
		return nil, fmt.Errorf("knowledge: TermDocumentCounts called with no index open")
	}
	terms := prefixSearchTokens(query)
	if len(terms) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(terms))
	out := make([]TermCount, 0, len(terms))
	for _, term := range terms {
		if seen[term] {
			continue
		}
		seen[term] = true
		// indexSearchMaxFetch is the same raw-hit ceiling SearchFiltered
		// works under; a term present in more files than that is reported
		// at the ceiling, which still says "present" — the only fact the
		// disclosure needs to be exact about is zero versus non-zero.
		hits, _, err := ix.runSearch(buildAndQuery([]string{term}), indexSearchMaxFetch, term)
		if err != nil {
			return nil, err
		}
		out = append(out, TermCount{Term: term, Documents: len(collapseSegmentHits(hits))})
	}
	return out, nil
}
