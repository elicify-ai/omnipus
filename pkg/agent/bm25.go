// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"math"
	"sort"
)

// bm25Hit is a ranked document result from bm25Rank.
type bm25Hit struct {
	Index int     // index into the docs slice passed to bm25Rank
	Score float64 // BM25 relevance score (always > 0)
}

// bm25Rank ranks docs by BM25 relevance to query, returning hits sorted by
// descending score (ties preserve input order via stable sort), excluding
// docs with zero or negative scores, and capped at limit (0 = unlimited).
// Tokenization uses retroTokenize (lowercased, split on non-alphanumeric).
// Parameters match the retro/bleve BM25 defaults: k1=1.2, b=0.75.
func bm25Rank(query string, docs []string, limit int) []bm25Hit {
	qTerms := retroTokenize(query)
	if len(qTerms) == 0 || len(docs) == 0 {
		return nil
	}

	// Tokenize each doc and gather corpus statistics.
	docTokens := make([][]string, len(docs))
	docFreq := make(map[string]int) // number of docs containing each term (for IDF)
	var totalLen int
	for i, d := range docs {
		toks := retroTokenize(d)
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

	n := float64(len(docs))
	avgdl := float64(totalLen) / n
	if avgdl == 0 {
		avgdl = 1
	}

	hits := make([]bm25Hit, 0, len(docs))
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
			// Standard BM25 IDF (non-negative form) + TF saturation with length norm.
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			score += idf * (f * (retroBM25K1 + 1)) /
				(f + retroBM25K1*(1-retroBM25B+retroBM25B*dl/avgdl))
		}
		if score > 0 {
			hits = append(hits, bm25Hit{Index: i, Score: score})
		}
	}

	if len(hits) == 0 {
		return nil
	}

	sort.SliceStable(hits, func(a, b int) bool {
		return hits[a].Score > hits[b].Score
	})

	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// rankTurnsBM25 ranks turnTexts by BM25 relevance to query, returning ranked
// hits (each hit's Index maps back into turnTexts). This is a thin caller over
// bm25Rank; the recall_conversation tool passes one concatenated text per
// conversation Turn.
func rankTurnsBM25(query string, turnTexts []string, limit int) []bm25Hit {
	return bm25Rank(query, turnTexts, limit)
}
