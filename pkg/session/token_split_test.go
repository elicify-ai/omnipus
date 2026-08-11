// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
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
func TestAppendTranscriptStrict_InOutReconcilesWithTotal(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		require.NoError(t, store.AppendTranscriptStrict(meta.ID,
			assistantEntryWithSplit("m1", 700, 300, 1000)))
	}

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	sum := got.Stats.TokensIn + got.Stats.TokensOut + got.Stats.TokensCacheRead + got.Stats.TokensCacheWrite
	assert.Equal(t, got.Stats.TokensTotal, sum,
		"in + out + cache must reconcile with total (in=%d out=%d total=%d)",
		got.Stats.TokensIn, got.Stats.TokensOut, got.Stats.TokensTotal)
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
