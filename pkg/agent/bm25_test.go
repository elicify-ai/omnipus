// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBM25Core_SharedByTurnAndRetroCallers (T18) asserts:
//   - bm25Rank ranks a short dense match above a long sparse one (length normalization).
//   - bm25Rank drops a doc with no query-term match (score == 0 excluded).
//   - bm25Rank respects the limit cap.
//   - rankTurnsBM25 returns the same ordering as bm25Rank for the same texts.
func TestBM25Core_SharedByTurnAndRetroCallers(t *testing.T) {
	// short_marker appears once in a 3-token doc → high density.
	// long_marker appears once in a 20-token doc → lower BM25 score (length penalty).
	docs := []string{
		"short_marker alpha", // index 0: dense match
		"long_marker we covered scheduling tooling meetings retrospectives action items planning priorities status updates", // index 1: sparse match
		"completely unrelated gardening notes", // index 2: no match for query
	}

	t.Run("bm25Rank length normalization and zero-score filter", func(t *testing.T) {
		// Query terms hit docs 0 and 1 but not 2.
		hits := bm25Rank("short_marker long_marker", docs, 10)
		require.Len(t, hits, 2, "no-match doc is excluded; two matching docs returned")
		assert.Equal(t, 0, hits[0].Index, "dense short doc ranks first (length normalization)")
		assert.Equal(t, 1, hits[1].Index, "sparse long doc ranks second")
		assert.Greater(t, hits[0].Score, hits[1].Score, "dense doc has strictly higher score")
		assert.Greater(t, hits[0].Score, 0.0, "scores are positive")
		assert.Greater(t, hits[1].Score, 0.0, "scores are positive")
	})

	t.Run("bm25Rank respects limit cap", func(t *testing.T) {
		hits := bm25Rank("short_marker long_marker", docs, 1)
		require.Len(t, hits, 1, "limit=1 returns at most 1 hit")
		assert.Equal(t, 0, hits[0].Index, "the top-ranked doc is kept")
	})

	t.Run("bm25Rank with empty query returns nil", func(t *testing.T) {
		hits := bm25Rank("", docs, 10)
		assert.Nil(t, hits)
	})

	t.Run("bm25Rank with no matching doc returns nil", func(t *testing.T) {
		hits := bm25Rank("xyzzy_nomatch", docs, 10)
		assert.Nil(t, hits)
	})

	t.Run("rankTurnsBM25 returns same ordering as bm25Rank", func(t *testing.T) {
		// Use a query that matches two of the three docs.
		query := "short_marker long_marker"
		direct := bm25Rank(query, docs, 10)
		viaTurns := rankTurnsBM25(query, docs, 10)

		require.Equal(t, len(direct), len(viaTurns), "same number of hits")
		for i := range direct {
			assert.Equal(t, direct[i].Index, viaTurns[i].Index,
				"hit %d: Index must match", i)
			assert.Equal(t, direct[i].Score, viaTurns[i].Score,
				"hit %d: Score must match", i)
		}
	})

	t.Run("rankTurnsBM25 nil inputs", func(t *testing.T) {
		assert.Nil(t, rankTurnsBM25("foo", nil, 10))
		assert.Nil(t, rankTurnsBM25("", docs, 10))
	})
}
