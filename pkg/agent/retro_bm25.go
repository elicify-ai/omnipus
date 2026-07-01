// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"sort"
	"strings"
	"unicode"
)

// BM25 parameters — match bleve's defaults (used for long-term memory recall) so
// retrospective recall ranks on the same scale and semantics.
const (
	retroBM25K1 = 1.2
	retroBM25B  = 0.75
)

// rankRetrosBM25 ranks retrospectives by BM25 relevance of their rendered text to
// query, descending, keeping only retros that match at least one query term,
// capped at limit (ties broken newest-first). This gives retro recall the same
// BM25 similarity ranking bleve provides for long-term memories, WITHOUT adding
// retros to the persistent room index — keeping them isolated so long-term recall
// is never polluted by retro documents, and avoiding a per-query index build over
// the small, time-bounded retro corpus.
//
// rankRetrosBM25 is a thin typed caller over bm25Rank (bm25.go); the BM25 math
// lives there so recall_conversation can rank conversation Turns via the same core.
func rankRetrosBM25(retros []Retro, query string, limit int) []Retro {
	if len(retros) == 0 {
		return nil
	}

	// Build the plain-text corpus from each retro's searchable text.
	docs := make([]string, len(retros))
	for i, r := range retros {
		docs[i] = retroSearchText(r)
	}

	hits := bm25Rank(query, docs, 0) // no limit yet — we need to apply the timestamp tie-break first
	if len(hits) == 0 {
		return nil
	}

	// Re-apply the original tie-break: equal scores → newest-first by Timestamp.
	// bm25Rank returns a stable-sorted slice (equal scores preserve input order),
	// so this stable secondary sort gives byte-for-byte identical output to the
	// original monolithic sort.
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].Score != hits[b].Score {
			return hits[a].Score > hits[b].Score
		}
		return retros[hits[a].Index].Timestamp.After(retros[hits[b].Index].Timestamp)
	})

	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}

	out := make([]Retro, len(hits))
	for i, h := range hits {
		out[i] = retros[h.Index]
	}
	return out
}

// retroTokenize lowercases and splits on non-alphanumeric runes — a simple
// analyzer sufficient for the small, time-bounded retro corpus.
func retroTokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}
