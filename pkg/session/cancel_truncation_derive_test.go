// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// A cancel marks its turn's last assistant entry truncated WITHOUT rewriting any
// earlier line (session-core U2 / FR-006, effects design D8): the transcript
// reader derives the flag from the turn_canceled record appended right after.
package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func appendTurnEntry(t *testing.T, s *UnifiedStore, id, role, turn string) {
	t.Helper()
	require.NoError(t, s.AppendTranscript(id, TranscriptEntry{
		ID: role + "-" + turn, Role: role, Content: role + " " + turn, TurnID: turn, Timestamp: time.Now().UTC(),
	}))
}

func TestReadTranscript_CancelTruncatesOnlyItsTurnsLastAssistantEntry(t *testing.T) {
	s := newTestStore(t)
	meta, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	id := meta.ID

	appendTurnEntry(t, s, id, "user", "T1")
	appendTurnEntry(t, s, id, "assistant", "T1") // a clean, completed turn
	appendTurnEntry(t, s, id, "user", "T2")
	appendTurnEntry(t, s, id, "assistant", "T2") // the turn that gets canceled
	path := filepath.Join(s.baseDir, id, "transcript.jsonl")
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	require.NoError(t, s.AppendTranscript(id, TranscriptEntry{
		ID: id + "_canceled", Type: EntryTypeTurnCancelled, TurnID: "T2", CancelMethod: "graceful", Timestamp: time.Now().UTC(),
	}))

	entries, err := s.ReadTranscript(id)
	require.NoError(t, err)
	byID := map[string]TranscriptEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	assert.True(t, byID["assistant-T2"].Truncated, "the canceled turn's last assistant entry reads as truncated")
	assert.Equal(t, "cancelled", byID["assistant-T2"].TruncationReason)
	assert.False(t, byID["assistant-T1"].Truncated, "a cancel on T2 must not mark the completed turn T1")
	assert.False(t, byID["user-T2"].Truncated)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after[:len(before)], "every earlier byte is untouched: the cancel only appended (FR-006)")
}

func TestReadTranscript_CancelWithoutAssistantEntryMarksNothing(t *testing.T) {
	s := newTestStore(t)
	meta, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	appendTurnEntry(t, s, meta.ID, "user", "T1")
	require.NoError(t, s.AppendTranscript(meta.ID, TranscriptEntry{
		ID: "c", Type: EntryTypeTurnCancelled, TurnID: "T1", Timestamp: time.Now().UTC(),
	}))
	entries, err := s.ReadTranscript(meta.ID)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, e.Truncated, "entry %s", e.ID)
	}
}

func TestReadTranscript_CancelKeepsAnExistingTruncationReason(t *testing.T) {
	s := newTestStore(t)
	meta, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	require.NoError(t, s.AppendTranscript(meta.ID, TranscriptEntry{
		ID: "a", Role: "assistant", TurnID: "T1", Truncated: true, TruncationReason: "max_output_tokens", Timestamp: time.Now().UTC(),
	}))
	require.NoError(t, s.AppendTranscript(meta.ID, TranscriptEntry{
		ID: "c", Type: EntryTypeTurnCancelled, TurnID: "T1", Timestamp: time.Now().UTC(),
	}))
	entries, err := s.ReadTranscript(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, "max_output_tokens", entries[0].TruncationReason, "a recorded reason is not overwritten")
}
