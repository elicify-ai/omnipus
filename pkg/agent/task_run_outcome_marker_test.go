// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED test for the task-run reasoning-only classification (spec section 1
// C4, section 8.1 reader row for pkg/agent/task_run_loop.go::turnWasReasoningOnly,
// section 16 item 21 backend half, FR-018): the classification keys on the
// assistant entry's typed outcome:"no_answer" marker - thinking entries are
// not read for the classification, and the old truncation inference no longer
// counts.
//
// RED status: FAILS today - turnWasReasoningOnly still keys on
// e.Truncated && e.TruncationReason == "max_output_tokens" && Content == ""
// (task_run_loop.go::turnWasReasoningOnly), so case 1 returns false where the
// marker is present and case 2 returns true where the marker is absent.

func wpcTaskStoreWithEntries(t *testing.T, entries []session.TranscriptEntry) (string, *session.UnifiedStore) {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeTask, "", "wpc-outcome-agent")
	require.NoError(t, err)
	for i, e := range entries {
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, e), "entry %d", i)
	}
	return meta.ID, store
}

func TestTurnWasReasoningOnly_KeysOnOutcomeMarker(t *testing.T) {
	notice := "The model (openai · gpt-5.2) did not respond"

	t.Run("marker entry counts as reasoning-only", func(t *testing.T) {
		sessionID, store := wpcTaskStoreWithEntries(t, []session.TranscriptEntry{
			{Role: "user", Content: "do the thing", Timestamp: time.Now().UTC()},
			{
				ID: "wpc-marker-1", Type: session.EntryTypeMessage, Role: "assistant",
				Content: notice, Outcome: session.OutcomeNoAnswer,
				Timestamp: time.Now().UTC(),
			},
		})
		assert.True(t, turnWasReasoningOnly(sessionID, store, ""),
			"an assistant entry carrying outcome:no_answer IS a reasoning-only try (C4) - "+
				"the classification keys on the marker")
	})

	t.Run("truncation inference alone no longer counts", func(t *_testing_placeholder) {}, func(t *testing.T) {
		sessionID, store := wpcTaskStoreWithEntries(t, []session.TranscriptEntry{
			{Role: "user", Content: "do the thing", Timestamp: time.Now().UTC()},
			{
				ID: "wpc-trunc-1", Type: session.EntryTypeMessage, Role: "assistant",
				Content: "", Truncated: true,
				TruncationReason: truncationReasonMaxOutputTokens,
				Timestamp:        time.Now().UTC(),
			},
		})
		assert.False(t, turnWasReasoningOnly(sessionID, store, ""),
			"the old truncation inference must NOT classify a reasoning-only try (C4: "+
				"turnWasReasoningOnly keys on the marker instead of truncation inference)")
	})

	t.Run("marker fires regardless of truncation fields", func(t *testing.T) {
		sessionID, store := wpcTaskStoreWithEntries(t, []session.TranscriptEntry{
			{Role: "user", Content: "do the thing", Timestamp: time.Now().UTC()},
			{
				ID: "wpc-marker-2", Type: session.EntryTypeMessage, Role: "assistant",
				Content: notice, Outcome: session.OutcomeNoAnswer,
				Truncated: false, TruncationReason: "",
				Timestamp: time.Now().UTC(),
			},
		})
		assert.True(t, turnWasReasoningOnly(sessionID, store, ""),
			"the marker alone decides; truncated/truncation_reason are not consulted")
	})

	t.Run("thinking entries are not read for the classification", func(t *testing.T) {
		// A thinking-only transcript (no assistant entry at all): the last
		// assistant-role entry walk must find nothing, and a thinking entry
		// - role-less per C3 - must never satisfy the classification.
		sessionID, store := wpcTaskStoreWithEntries(t, []session.TranscriptEntry{
			{Role: "user", Content: "do the thing", Timestamp: time.Now().UTC()},
			{
				Type: session.EntryTypeThinking, Role: "", Content: "",
				ThinkingText: "reasoning that produced no answer " + thinkingSentinel,
				Timestamp:    time.Now().UTC(),
			},
		})
		assert.False(t, turnWasReasoningOnly(sessionID, store, ""),
			"a thinking entry is not answer content and never satisfies the classification (section 8.1)")
	})

	t.Run("real answer never classifies", func(t *testing.T) {
		assert.False(t, turnWasReasoningOnly("", nil, "real answer"),
			"a non-empty resp short-circuits to false (existing behavior)")
	})
}
