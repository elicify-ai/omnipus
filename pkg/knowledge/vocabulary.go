// Omnipus — zero-hit vocabulary, and the expansion we deliberately do not do
// (ADR-068 D21.4, FR-114/FR-115).
//
// # The decision this file encodes
//
// When a search returns nothing, the tempting move is pseudo-relevance feedback:
// take the top results, harvest their terms, re-run a widened query, and hand
// back something. RM3 is the standard form of it. D21.4 forbids it as a DEFAULT
// for two independent reasons, either sufficient on its own:
//
//  1. PRF assumes first-pass precision and amplifies error when it is absent. It
//     takes the top results on faith. If the first pass was wrong, the second
//     pass is confidently wronger — and the zero-hit case is precisely the case
//     where there is no first pass to have faith in.
//
//  2. Silently expanding a query answers a question nobody asked. A caller who
//     searched for one thing and received results for a broader thing has been
//     given a wrong answer through a channel that carries no way to say so. That
//     is the D13 honesty contract broken by us, in the same shape this whole
//     package exists to prevent.
//
// So on zero hits we report VOCABULARY, not results: "no matches; nearest
// indexed terms: prospect, prospecting, prospects". The caller — an agent in a
// loop, or a person — reformulates. That respects the agentic loop instead of
// pre-empting it, and it tells the truth about WHY the query failed, which a
// widened result set actively conceals.
//
// The distinction that makes this safe: these are terms the index ACTUALLY
// HOLDS, read out of the term dictionary. They are a fact about the corpus, not
// a guess about intent. Nothing here reformulates, re-runs, or returns a
// document the caller did not ask for.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"fmt"
	"sort"
	"strings"

	bleveIndexAPI "github.com/blevesearch/bleve_index_api"
)

// VocabularySuggestionLimit bounds how many near-miss terms are offered per
// query term. A long list is not more helpful — it is a different way of
// refusing to answer.
const VocabularySuggestionLimit = 8

// vocabularyPrefixMin is the shortest prefix used to probe the term dictionary.
//
// Below three characters a prefix matches a large share of any English
// dictionary, so the "nearest terms" would be a sample of the corpus rather than
// a neighbourhood of the query. That is worse than saying nothing, because it
// looks like a finding.
const vocabularyPrefixMin = 3

// NearMissVocabulary returns terms the index actually holds that are near the
// query's terms, for FR-115's zero-hit report.
//
// "Near" is deliberately shallow: a shared prefix of at least
// vocabularyPrefixMin characters. It catches the case that motivates the
// requirement — a morphological miss, `prospect` vs `prospects` vs
// `prospecting` — without pretending to a similarity model. Edit distance would
// be the obvious upgrade and it is NOT done here on purpose: a fuzzy neighbour
// list invites the caller to treat it as a corrected query, which is the
// silent-expansion failure arriving one layer up.
//
// Returns an empty slice when nothing is near. An empty list is a truthful
// answer — the corpus holds no similar term — and it must not be padded.
func (ix *Index) NearMissVocabulary(query string) (result []string, err error) {
	terms := foldTokens(query)
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
		// Closing the reader is not hygiene here: scorch pins the index snapshot
		// a reader holds, so a leaked reader keeps segments alive and the index
		// grows on disk for as long as the process runs.
		if cerr := reader.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	seen := make(map[string]bool, VocabularySuggestionLimit)
	out := make([]string, 0, VocabularySuggestionLimit)

	for _, term := range terms {
		if len(term) < vocabularyPrefixMin {
			continue
		}
		prefix := term
		if len(prefix) > vocabularyPrefixMin+2 {
			// Probe on a shortened prefix so a MISSPELLED tail can still find
			// its neighbours: "prospcts" shares only "prosp" with "prospects".
			prefix = prefix[:vocabularyPrefixMin+2]
		}
		for _, field := range []string{fieldBody, fieldTitle, fieldName} {
			found, ferr := prefixTerms(reader, field, prefix, VocabularySuggestionLimit)
			if ferr != nil {
				return nil, ferr
			}
			for _, f := range found {
				if f == term || seen[f] {
					// A term equal to what was searched is not a suggestion —
					// offering it back would imply the query was misspelled when
					// the real answer is that the term exists and matches
					// nothing under the current filters.
					continue
				}
				seen[f] = true
				out = append(out, f)
			}
		}
	}

	sort.Strings(out)
	if len(out) > VocabularySuggestionLimit {
		out = out[:VocabularySuggestionLimit]
	}
	return out, nil
}

// prefixTerms reads up to limit terms with the given prefix out of one field's
// term dictionary.
//
// A dictionary iterator holds resources, so it is closed on every path including
// the error one — a returned error that also leaked an iterator would turn a
// recoverable failure into a growing index.
func prefixTerms(reader bleveIndexAPI.IndexReader, field, prefix string, limit int) (out []string, err error) {
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
		out = append(out, entry.Term)
	}
	return out, nil
}

// ZeroHitStatement renders FR-115's sentence, or the empty string when there is
// nothing truthful to add.
//
// It never suggests a reformulation and never claims the caller made a mistake.
// It reports two facts: nothing matched, and these terms are in the index.
func ZeroHitStatement(terms []string) string {
	if len(terms) == 0 {
		// The honest zero-zero case. Inventing an encouragement here ("try a
		// broader query") is the same impulse as expanding the query, minus the
		// results.
		return "No matches, and no similar terms are present in this collection."
	}
	return "No matches. Nearest indexed terms: " + strings.Join(terms, ", ") + "."
}
