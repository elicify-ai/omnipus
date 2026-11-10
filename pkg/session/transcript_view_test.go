// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func viewAppend(t *testing.T, s *UnifiedStore, sid, id, role, content string, typ EntryType, n int) {
	t.Helper()
	_, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{
		ID: id, Type: typ, Role: role, Content: content, AgentID: "mia",
		ViewMembership: ViewMembershipChat, Timestamp: time.Date(2026, 3, 4, 12, 0, n, 0, time.UTC),
	})
	require.NoError(t, err)
}

func viewIDs(entries []TranscriptEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

// Founder ruling: /clear clears what is on screen and moves the context
// forward; the chat view starts at the last marker, the full read does not.
func TestReadTranscriptView_StartsAtTheLastClearMarker(t *testing.T) {
	s, sid := newEffectsSession(t)
	viewAppend(t, s, sid, "a", "user", "old question", "", 1)
	viewAppend(t, s, sid, "b", "assistant", "old answer", "", 2)
	viewAppend(t, s, sid, "c1", "user", "/clear", "", 3)
	viewAppend(t, s, sid, "m1", "system", ClearMarkerText, EntryTypeSystem, 4)
	viewAppend(t, s, sid, "d", "user", "new question", "", 5)

	view, err := s.ReadTranscriptView(sid)
	require.NoError(t, err)
	require.Equal(t, []string{"m1", "d"}, viewIDs(view.Entries), "the marker, then what follows")
	require.True(t, view.HasEarlier)

	full, err := s.ReadTranscript(sid)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c1", "m1", "d"}, viewIDs(full), "ReadTranscript stays the whole transcript")
}

func TestReadTranscriptView_UsesTheLastOfSeveralMarkers(t *testing.T) {
	s, sid := newEffectsSession(t)
	viewAppend(t, s, sid, "a", "user", "one", "", 1)
	viewAppend(t, s, sid, "c1", "user", "/clear", "", 2)
	viewAppend(t, s, sid, "m1", "system", ClearMarkerText, EntryTypeSystem, 3)
	viewAppend(t, s, sid, "b", "user", "two", "", 4)
	viewAppend(t, s, sid, "m2", "system", ClearMarkerText, EntryTypeSystem, 5)
	viewAppend(t, s, sid, "c", "user", "three", "", 6)

	view, err := s.ReadTranscriptView(sid)
	require.NoError(t, err)
	require.Equal(t, []string{"m2", "c"}, viewIDs(view.Entries))
	require.True(t, view.HasEarlier)
}

func TestReadTranscriptView_NoMarkerIsTheWholeTranscript(t *testing.T) {
	s, sid := newEffectsSession(t)
	viewAppend(t, s, sid, "a", "user", "one", "", 1)
	viewAppend(t, s, sid, "b", "assistant", "two", "", 2)

	view, err := s.ReadTranscriptView(sid)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, viewIDs(view.Entries))
	require.False(t, view.HasEarlier)
}

// Only the exact marker counts: type system + role system + the exact text. A
// user row that merely says it, or a different system row, does not cut.
func TestReadTranscriptView_OnlyTheExactMarkerCuts(t *testing.T) {
	s, sid := newEffectsSession(t)
	viewAppend(t, s, sid, "a", "user", ClearMarkerText, "", 1)
	viewAppend(t, s, sid, "b", "system", "Conversation context cleared (note)", EntryTypeSystem, 2)
	viewAppend(t, s, sid, "c", "assistant", ClearMarkerText, "", 3)

	view, err := s.ReadTranscriptView(sid)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, viewIDs(view.Entries))
	require.False(t, view.HasEarlier)
}

// A marker at the very start leaves nothing earlier.
func TestReadTranscriptView_MarkerFirstHasNothingEarlier(t *testing.T) {
	s, sid := newEffectsSession(t)
	viewAppend(t, s, sid, "m1", "system", ClearMarkerText, EntryTypeSystem, 1)
	viewAppend(t, s, sid, "a", "user", "after", "", 2)

	view, err := s.ReadTranscriptView(sid)
	require.NoError(t, err)
	require.Equal(t, []string{"m1", "a"}, viewIDs(view.Entries))
	require.False(t, view.HasEarlier)
}
