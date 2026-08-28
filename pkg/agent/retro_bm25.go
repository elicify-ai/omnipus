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

// BM25 parameters. These are bleve's own BM25 constants — search.BM25_k1 = 1.2
// and search.BM25_b = 0.75 in bleve v2.6.1 — so retrospective recall saturates
// and length-normalises the same way long-term memory recall does.
//
// That equivalence is REAL only since ADR-068 D21.1. Before it, bleve was
// scoring TF-IDF (its default) while this comment claimed the two subsystems
// matched, so the "same scale and semantics" argument these constants exist to
// support was resting on a premise that was simply false. It now holds, with
// three divergences that are worth naming rather than rounding away. They are
// listed worst-first, and the first is large enough that "same scale" remains
// an overstatement even now:
//
//   - bleve's tf is sqrt(freq), not freq (TermQueryScorer.Score), while
//     bm25Rank uses the raw count. A retro mentioning a term nine times is
//     tf=9 here and tf=3 there, so the two rankers saturate at very different
//     rates over the same text.
//   - bleve's length normalisation divides by an avgFieldLength computed as
//     ceil(FieldCardinality/DocCount) — the number of DISTINCT terms in the
//     field dictionary, not the mean document length (bm25ScoreMetrics).
//     bm25Rank uses the true mean token count. Same b, different denominator.
//   - bleve's numerator is tf*k1 where bm25Rank's is tf*(k1+1). That one is a
//     constant factor, so it never reorders results WITHIN either ranker, but
//     it does mean the absolute scores are not interchangeable.
//
// And on top of the arithmetic, the tokenizers differ (ADR-068 D21.5): bleve
// applies the "en" analyzer with Porter stemming and stopword removal, while
// retroTokenize splits on every non-alphanumeric rune and does neither.
//
// The IDF form is the one thing that matches exactly: both compute
// log(1 + (N-df+0.5)/(df+0.5)) — bleve in TermQueryScorer.computeIDF, this
// package in bm25Rank.
//
// So: same FAMILY of ranking function, same k1 and b, and — since D21.1 — both
// genuinely BM25. Scores from the two subsystems still must not be compared
// numerically or merged into one ordering. If that is ever wanted, it needs
// rank fusion (D21.3's RRF), which is rank-based precisely because normalising
// one of these scores against the other has no principled answer.
const (
	retroBM25K1 = 1.2
	retroBM25B  = 0.75
)

// rankRetrosBM25 ranks retrospectives by BM25 relevance of their rendered text to
// query, descending, keeping only retros that match at least one query term,
// capped at limit (ties broken newest-first). This gives retro recall a BM25
// ranking of the same family as the one bleve now provides for long-term
// memories — see the parameter block above for the three ways the two rankers
// still diverge and why their scores are not interchangeable, and note that
// bleve only produces BM25 at all because ADR-068 D21.1
// set the scoring model; it defaults to TF-IDF. Retros are ranked here rather
// than through bleve so they are never added to the persistent room index,
// keeping long-term recall unpolluted by retro documents and avoiding a
// per-query index build over the small, time-bounded retro corpus.
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
