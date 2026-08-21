// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the defect where a real 10-minute session recorded
// tokens_in=0 with 575,539 tokens booked entirely as output, and a per-model
// breakdown whose in/out sat at 0 beside a non-zero total. Cost attribution
// was computed from that split, so it could not have been right.

func assistantEntryWithSplit(model string, prompt, completion, total int) TranscriptEntry {
	return TranscriptEntry{
		ID:               "e-" + model,
		Role:             "assistant",
		Content:          "hi",
		Timestamp:        time.Now().UTC(),
		Model:            model,
		Tokens:           total,
		PromptTokens:     prompt,
		CompletionTokens: completion,
	}
}

// TestAppendTranscriptStrict_RecordsInputOutputSplit exercises the PRODUCTION
// write path (AppendTranscriptStrict is what pkg/agent/turn.go calls), not the
// legacy PartitionStore path.
func TestAppendTranscriptStrict_RecordsInputOutputSplit(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)

	require.NoError(t, store.AppendTranscriptStrict(meta.ID,
		assistantEntryWithSplit("z-ai/glm-5.2", 800, 200, 1000)))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 800, got.Stats.TokensIn, "tokens_in must reflect the provider's prompt tokens")
	assert.Equal(t, 200, got.Stats.TokensOut, "tokens_out must be completion tokens, not the whole total")
	assert.Equal(t, 1000, got.Stats.TokensTotal)

	mt, ok := got.Stats.ByModel["z-ai/glm-5.2"]
	require.True(t, ok, "per-model breakdown must exist")
	assert.Equal(t, 800, mt.In, "per-model in was structurally always 0 before this fix")
	assert.Equal(t, 200, mt.Out, "per-model out was structurally always 0 before this fix")
	assert.Equal(t, 1000, mt.Total)
}

// The reconciliation invariant. Without it a future change could repopulate
// tokens_out with the turn total again and the numbers would silently
// double-count input.
//
// This test previously used entries with ZERO cache tokens, which made it
// vacuous: it would have passed even if cache read/write were still (as the
// stale doc comments once claimed) a SUBSET of tokens_out rather than an
// ADDITIVE component of tokens_total, since 0 is a subset of anything. Every
// entry here carries non-zero CacheReadTokens/CacheWriteTokens, and Tokens
// (the provider-reported total) is set to prompt + completion + cache_read +
// cache_write — the real additive relation (see
// pkg/providers/protocoltypes/types.go's UsageInfo doc) — so this only
// passes if the code actually sums all four components into TokensTotal.
func TestAppendTranscriptStrict_InOutReconcilesWithTotal(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)

	const prompt, completion, cacheRead, cacheWrite = 700, 300, 150, 50
	const total = prompt + completion + cacheRead + cacheWrite // 1200

	for i := 0; i < 3; i++ {
		entry := TranscriptEntry{
			ID:               fmt.Sprintf("e-cache-%d", i),
			Role:             "assistant",
			Content:          "hi",
			Timestamp:        time.Now().UTC(),
			Model:            "m1",
			Tokens:           total,
			PromptTokens:     prompt,
			CompletionTokens: completion,
			CacheReadTokens:  cacheRead,
			CacheWriteTokens: cacheWrite,
		}
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, entry))
	}

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 3*cacheRead, got.Stats.TokensCacheRead, "cache_read must accumulate, not stay 0")
	assert.Equal(t, 3*cacheWrite, got.Stats.TokensCacheWrite, "cache_write must accumulate, not stay 0")

	sum := got.Stats.TokensIn + got.Stats.TokensOut + got.Stats.TokensCacheRead + got.Stats.TokensCacheWrite
	assert.Equal(t, got.Stats.TokensTotal, sum,
		"in + out + cache must reconcile with total (in=%d out=%d cache_read=%d cache_write=%d total=%d)",
		got.Stats.TokensIn, got.Stats.TokensOut, got.Stats.TokensCacheRead, got.Stats.TokensCacheWrite, got.Stats.TokensTotal)
}

// Multi-model sessions must attribute to the right model. The session that
// exposed this bug used two models, and the split has to survive that.
func TestAppendTranscriptStrict_SplitIsPerModel(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)

	require.NoError(t, store.AppendTranscriptStrict(meta.ID, assistantEntryWithSplit("model-a", 100, 10, 110)))
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, assistantEntryWithSplit("model-b", 500, 50, 550)))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 100, got.Stats.ByModel["model-a"].In)
	assert.Equal(t, 10, got.Stats.ByModel["model-a"].Out)
	assert.Equal(t, 500, got.Stats.ByModel["model-b"].In)
	assert.Equal(t, 50, got.Stats.ByModel["model-b"].Out)
	assert.Equal(t, 600, got.Stats.TokensIn)
	assert.Equal(t, 60, got.Stats.TokensOut)
}

// Back-compat: transcripts written before the split existed carry only a total.
// Those must keep aggregating exactly as they did, or historical sessions would
// suddenly report 0 output tokens.
func TestAppendTranscriptStrict_LegacyEntryWithoutSplitKeepsOldBehaviour(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)

	legacy := TranscriptEntry{
		ID:        "legacy-1",
		Role:      "assistant",
		Content:   "hi",
		Timestamp: time.Now().UTC(),
		Model:     "old-model",
		Tokens:    1234, // total only; no PromptTokens/CompletionTokens
	}
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, legacy))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 1234, got.Stats.TokensOut, "a legacy entry must still book its total as output")
	assert.Equal(t, 0, got.Stats.TokensIn, "an unrecorded split must stay 0 rather than be fabricated")
	assert.Equal(t, 1234, got.Stats.ByModel["old-model"].Total)
}

// TestAppendTranscriptStrict_HasSplitBoundary pins the hasSplit boundary
// (accumulateEntryStats in entry_stats.go: hasSplit := prompt>0 ||
// completion>0) for the two asymmetric cases where exactly one half of the
// split is genuinely zero rather than unrecorded.
//
// Decision (documented here and in accumulateEntryStats' doc comment): a
// genuinely-zero component is booked AS ZERO, not treated as "missing" and
// backfilled from the turn total. This is intentional, not a token-loss
// regression versus the pre-split behaviour: production callers always
// populate PromptTokens, CompletionTokens, CacheReadTokens, and
// CacheWriteTokens together from the SAME provider UsageInfo response
// (pkg/providers/protocoltypes/types.go), whose TotalTokens is documented to
// equal PromptTokens + CompletionTokens + CacheReadTokens + CacheWriteTokens.
// So when completion really is 0 (e.g. a turn that sent a large prompt and
// stopped before producing any completion tokens), the rest of Tokens is
// necessarily accounted for by cache — never silently dropped — and falling
// back to booking the whole total into TokensOut whenever ONE split
// component reads zero would reintroduce the over-counting bug this
// convention exists to fix, just gated on a boundary condition instead of
// unconditionally.
func TestAppendTranscriptStrict_HasSplitBoundary(t *testing.T) {
	cases := []struct {
		name             string
		prompt           int
		completion       int
		total            int
		wantTokensIn     int
		wantTokensOut    int
		wantHasSplitDocs string
	}{
		{
			name:             "prompt only, completion genuinely zero",
			prompt:           5000,
			completion:       0,
			total:            5200, // remainder (200) is unaccounted cache in this synthetic case
			wantTokensIn:     5000,
			wantTokensOut:    0,
			wantHasSplitDocs: "hasSplit is true (prompt>0); completion books as the real 0, not total",
		},
		{
			name:             "completion only, prompt genuinely zero",
			prompt:           0,
			completion:       300,
			total:            1000, // remainder (700) is unaccounted cache in this synthetic case
			wantTokensIn:     0,
			wantTokensOut:    300,
			wantHasSplitDocs: "hasSplit is true (completion>0); prompt books as the real 0, not total",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newUnifiedStoreForTest(t)
			meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
			require.NoError(t, err)

			entry := TranscriptEntry{
				ID:               "e-boundary",
				Role:             "assistant",
				Content:          "hi",
				Timestamp:        time.Now().UTC(),
				Model:            "m1",
				Tokens:           tc.total,
				PromptTokens:     tc.prompt,
				CompletionTokens: tc.completion,
			}
			require.NoError(t, store.AppendTranscriptStrict(meta.ID, entry))

			got, err := store.GetMeta(meta.ID)
			require.NoError(t, err)

			assert.Equal(t, tc.wantTokensIn, got.Stats.TokensIn, tc.wantHasSplitDocs)
			assert.Equal(t, tc.wantTokensOut, got.Stats.TokensOut, tc.wantHasSplitDocs)
			assert.Equal(t, tc.total, got.Stats.TokensTotal, "TokensTotal always books the provider-reported total verbatim")
		})
	}
}

// TestAppendTranscript_RecordsInputOutputSplit exercises the NON-strict
// UnifiedStore.AppendTranscript entry point (unified.go). Every other test
// in this file goes through AppendTranscriptStrict (what pkg/agent/turn.go
// actually calls in production); this test proves the extracted
// accumulateEntryStats helper (entry_stats.go) is wired identically at its
// second call site.
func TestAppendTranscript_RecordsInputOutputSplit(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)

	require.NoError(t, store.AppendTranscript(meta.ID,
		assistantEntryWithSplit("z-ai/glm-5.2", 800, 200, 1000)))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 800, got.Stats.TokensIn)
	assert.Equal(t, 200, got.Stats.TokensOut)
	assert.Equal(t, 1000, got.Stats.TokensTotal)

	mt, ok := got.Stats.ByModel["z-ai/glm-5.2"]
	require.True(t, ok, "per-model breakdown must exist")
	assert.Equal(t, 800, mt.In)
	assert.Equal(t, 200, mt.Out)
	assert.Equal(t, 1000, mt.Total)
}
