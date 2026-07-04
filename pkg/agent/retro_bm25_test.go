// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRankRetrosBM25_RanksAndFilters proves retro recall is BM25-ranked: a short,
// dense match outranks a long, sparse one (length normalization), and a
// non-matching retro is excluded.
func TestRankRetrosBM25_RanksAndFilters(t *testing.T) {
	now := time.Now().UTC()
	retros := []Retro{
		{Recap: "flock alpha_marker", Timestamp: now}, // short, strong match
		{
			Recap:     "we covered scheduling, tooling, flock, and beta_marker at great length in the retro meeting today",
			Timestamp: now.Add(-time.Hour),
		}, // long, weak match
		{
			Recap:     "unrelated scheduling notes only",
			Timestamp: now.Add(-2 * time.Hour),
		}, // no match
	}
	got := rankRetrosBM25(retros, "flock", 10)
	require.Len(t, got, 2, "only the two flock-matching retros are returned; the non-matching one is excluded")
	assert.Contains(
		t,
		got[0].Recap,
		"alpha_marker",
		"the shorter, denser match ranks first (BM25 length normalization)",
	)
	assert.Contains(t, got[1].Recap, "beta_marker", "the longer, weaker match ranks second")
}

// TestRankRetrosBM25_EdgeCases covers empty inputs, no-match, and the limit cap.
func TestRankRetrosBM25_EdgeCases(t *testing.T) {
	assert.Empty(t, rankRetrosBM25(nil, "x", 10), "no retros → empty")
	assert.Empty(t, rankRetrosBM25([]Retro{{Recap: "hi"}}, "", 10), "empty query → empty")
	assert.Empty(t, rankRetrosBM25([]Retro{{Recap: "hi there"}}, "nomatch", 10), "no query-term match → empty")

	many := make([]Retro, 5)
	for i := range many {
		many[i] = Retro{Recap: "flock discussion"}
	}
	assert.Len(t, rankRetrosBM25(many, "flock", 2), 2, "respects the limit cap")
}
