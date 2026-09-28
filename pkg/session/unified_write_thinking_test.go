// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED test for the store write path (spec section 8.1 row for
// pkg/session/unified_write.go: "Persist in order (the write path) - thinking
// entries persist in round order alongside every other entry") and the
// passthrough reader rows it guards (pkg/agent/boot_sweep.go,
// pkg/agent/loop_inbound.go, pkg/agent/loop_wire.go, pkg/agent/loop.go:
// "Passthrough - order-preserving transcript reads; entries persist
// untouched").
//
// RED status: FAILS to compile until the pinned typed fields exist on
// TranscriptEntry (ThinkingText, ElapsedMS, ProviderSummary, Outcome) and
// the "thinking" entry type constant exists - the sanctioned RED for a
// new-API test.

func TestAppendTranscriptRoundTrip_ThinkingEntriesPersistInOrder(t *testing.T) {
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "wpc-roundtrip-agent")
	require.NoError(t, err)

	now := time.Now().UTC()
	want := []TranscriptEntry{
		{Role: "user", Content: "kick off the turn", Timestamp: now},
		{
			ID: "wpc-think-1", Type: EntryTypeThinking,
			ThinkingText:    "first round reasoning probe " + "sk-live-abcd1234EFGH",
			TurnID:          "turn-1",
			ElapsedMS:       1234,
			ThinkingTokens:  77,
			ProviderSummary: true,
			Timestamp:       now,
		},
		{
			ID: "wpc-ans-1", Type: EntryTypeMessage, Role: "assistant",
			Content: "the answer", TurnID: "turn-1", Timestamp: now,
		},
		{
			ID: "wpc-think-2", Type: EntryTypeThinking,
			ThinkingText: "second round reasoning",
			TurnID:       "turn-2",
			ElapsedMS:    900,
			Timestamp:    now,
		},
		{
			ID: "wpc-notice-1", Type: EntryTypeMessage, Role: "assistant",
			Content:   "The model (openai · gpt-5.2) did not respond",
			Outcome:   OutcomeNoAnswer,
			TurnID:    "turn-2",
			Timestamp: now,
		},
	}
	for i, e := range want {
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, e), "entry %d", i)
	}

	got, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Len(t, got, len(want), "all five entries persist, in order")

	for i := range want {
		assert.Equal(t, want[i].ID, got[i].ID, "entry %d order/id", i)
		assert.Equal(t, want[i].Type, got[i].Type, "entry %d type", i)
		assert.Equal(t, want[i].ThinkingText, got[i].ThinkingText, "entry %d thinking_text", i)
		assert.Equal(t, want[i].TurnID, got[i].TurnID, "entry %d turn_id", i)
		assert.Equal(t, want[i].ElapsedMS, got[i].ElapsedMS, "entry %d elapsed_ms", i)
		assert.Equal(t, want[i].ThinkingTokens, got[i].ThinkingTokens, "entry %d thinking_tokens", i)
		assert.Equal(t, want[i].ProviderSummary, got[i].ProviderSummary, "entry %d provider_summary", i)
		assert.Equal(t, want[i].Outcome, got[i].Outcome, "entry %d outcome", i)
		assert.Equal(t, want[i].Role, got[i].Role, "entry %d role", i)
		assert.Equal(t, want[i].Content, got[i].Content, "entry %d content", i)
	}

	// The passthrough rows' oracle, stated positively: the thinking entry
	// replays byte-for-byte intact - nothing trimmed, reordered, folded or
	// dropped by a persist/read cycle.
	th := got[1]
	assert.Equal(t, EntryTypeThinking, th.Type)
	assert.NotEmpty(t, th.ThinkingText, "the stored thinking row replays its text intact")
	assert.Empty(t, th.Role, "a thinking entry replays with no role (C3)")
}
