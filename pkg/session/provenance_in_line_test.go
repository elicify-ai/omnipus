// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// The authenticated principal of an accepted user message is a private member
// of the message's OWN transcript line (session-core Decision A / effects design
// D6): one append, no side file, no write-phase marker, no rewrite.
package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvenance_SourceLivesInTheMessageLineAndNowhereElse(t *testing.T) {
	s := newTestStore(t)
	meta, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	id := meta.ID
	dir := filepath.Join(s.baseDir, id)

	require.NoError(t, s.AppendTranscript(id, TranscriptEntry{ID: "agent-1", Role: "assistant", Content: "hi", Timestamp: time.Now().UTC()}))
	require.NoError(t, s.AppendTranscriptWithProvenance(id, TranscriptEntry{ID: "user-1", Role: "user", Content: "first", Timestamp: time.Now().UTC()}, "alice"))
	require.NoError(t, s.AppendTranscriptWithProvenance(id, TranscriptEntry{ID: "user-2", Role: "user", Content: "second", Timestamp: time.Now().UTC()}, ""))

	raw, err := os.ReadFile(filepath.Join(dir, "transcript.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.Len(t, lines, 3, "exactly one line per message: no paired second write")
	assert.NotContains(t, lines[0], `"source"`, "a generic writer cannot mint provenance")
	assert.Contains(t, lines[1], `"source":{"kind":"user","principal":"alice"}`)
	assert.Contains(t, lines[2], `"source":{"kind":"anonymous"}`, "an empty principal is anonymous, never an invented human")
	for _, l := range lines {
		assert.NotContains(t, l, "provenance_pending", "the write-phase marker is gone")
	}
	_, statErr := os.Stat(filepath.Join(dir, "provenance.jsonl"))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "no side file")

	got, ok, err := s.LookupMessageProvenance(id, "user-1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, MessageProvenance{MessageID: "user-1", SessionID: id, Content: "first", Principal: "alice", Ordinal: 1}, got)
	got, ok, err = s.LookupMessageProvenance(id, "user-2")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, int64(2), got.Ordinal)
	assert.Empty(t, got.Principal)
	_, ok, err = s.LookupMessageProvenance(id, "agent-1")
	require.NoError(t, err)
	assert.False(t, ok, "an agent line has no provenance")

	// The reader never exposes the private source.
	entries, err := s.ReadTranscript(id)
	require.NoError(t, err)
	enc, err := json.Marshal(entries)
	require.NoError(t, err)
	assert.NotContains(t, string(enc), "alice")
	assert.NotContains(t, string(enc), `"source"`)
	assert.NotContains(t, string(enc), "provenance_pending", "A6: the REST seam never leaks the retired write-phase marker")
}

func TestProvenance_TornPairedLineIsInvisibleAndTheNextAppendStartsClean(t *testing.T) {
	s := newTestStore(t)
	meta, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	id := meta.ID
	path := filepath.Join(s.baseDir, id, "transcript.jsonl")
	require.NoError(t, s.AppendTranscript(id, TranscriptEntry{ID: "a", Role: "assistant", Content: "x", Timestamp: time.Now().UTC()}))

	// A power loss mid-write leaves half a line with no newline.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"id":"torn","role":"user","content":"half a messa`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	entries, err := s.ReadTranscript(id)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the torn message is neither shown nor admitted")

	require.NoError(t, s.AppendTranscriptWithProvenance(id, TranscriptEntry{ID: "next", Role: "user", Content: "whole", Timestamp: time.Now().UTC()}, "bob"))
	_, ok, err := s.LookupMessageProvenance(id, "next")
	require.NoError(t, err)
	assert.True(t, ok, "the next paired append lands on its own line")
	_, ok, err = s.LookupMessageProvenance(id, "torn")
	require.NoError(t, err)
	assert.False(t, ok)
	entries, err = s.ReadTranscript(id)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "next", entries[1].ID)
}
