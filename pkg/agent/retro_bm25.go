// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"math"
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
func rankRetrosBM25(retros []Retro, query string, limit int) []Retro {
	qTerms := retroTokenize(query)
	if len(qTerms) == 0 || len(retros) == 0 {
		return nil
	}

	// Tokenize each retro and gather corpus statistics.
	docTokens := make([][]string, len(retros))
	docFreq := make(map[string]int) // # retros containing each term (for idf)
	var totalLen int
	for i, r := range retros {
		toks := retroTokenize(retroSearchText(r))
		docTokens[i] = toks
		totalLen += len(toks)
		seen := make(map[string]bool, len(toks))
		for _, t := range toks {
			if !seen[t] {
				docFreq[t]++
				seen[t] = true
			}
		}
	}

	n := float64(len(retros))
	avgdl := float64(totalLen) / n
	if avgdl == 0 {
		avgdl = 1
	}

	type scored struct {
		idx   int
		score float64
	}
	scoredDocs := make([]scored, 0, len(retros))
	for i, toks := range docTokens {
		tf := make(map[string]int, len(toks))
		for _, t := range toks {
			tf[t]++
		}
		dl := float64(len(toks))
		var score float64
		for _, qt := range qTerms {
			f := float64(tf[qt])
			if f == 0 {
				continue
			}
			df := float64(docFreq[qt])
			// Standard BM25 idf (non-negative form) + tf saturation with length norm.
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			score += idf * (f * (retroBM25K1 + 1)) /
				(f + retroBM25K1*(1-retroBM25B+retroBM25B*dl/avgdl))
		}
		if score > 0 {
			scoredDocs = append(scoredDocs, scored{idx: i, score: score})
		}
	}

	sort.SliceStable(scoredDocs, func(a, b int) bool {
		if scoredDocs[a].score != scoredDocs[b].score {
			return scoredDocs[a].score > scoredDocs[b].score
		}
		return retros[scoredDocs[a].idx].Timestamp.After(retros[scoredDocs[b].idx].Timestamp)
	})

	if limit > 0 && len(scoredDocs) > limit {
		scoredDocs = scoredDocs[:limit]
	}
	out := make([]Retro, len(scoredDocs))
	for i, s := range scoredDocs {
		out[i] = retros[s.idx]
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
