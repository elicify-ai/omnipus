// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// Founder ruling: /clear clears what is on screen and moves the context
// forward. Every chat-view read starts at the last clear marker and says whether
// earlier entries exist; the archive is untouched.

func seedClearedSession(t *testing.T, store *session.UnifiedStore, sid string) {
	t.Helper()
	at := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	for i, e := range []session.TranscriptEntry{
		{ID: "old-q", Role: "user", Content: "old question"},
		{ID: "old-a", Role: "assistant", Content: "old answer"},
		{ID: "clear-row", Role: "user", Content: "/clear"},
		{ID: "marker", Type: session.EntryTypeSystem, Role: "system", Content: session.ClearMarkerText},
		{ID: "new-q", Role: "user", Content: "new question"},
	} {
		e.AgentID = "mia"
		e.Timestamp = at.Add(time.Duration(i) * time.Second)
		require.NoError(t, store.AppendTranscriptStrict(sid, e))
	}
}

func messageIDs(entries []session.TranscriptEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

func TestGetSessionMessages_StartsAtTheLastClearMarker(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	seedClearedSession(t, api.agentLoop.GetSessionStore(), sid)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages", nil)
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var entries []session.TranscriptEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))
	require.Equal(t, []string{"marker", "new-q"}, messageIDs(entries))
	require.Equal(t, "true", w.Header().Get("X-Has-Earlier-Messages"))
}

func TestGetSessionMessages_NoMarkerIsWholeAndFlagFalse(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: "q", Role: "user", Content: "hi", AgentID: "mia", Timestamp: time.Now().UTC()}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages", nil)
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var entries []session.TranscriptEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))
	require.Equal(t, []string{"q"}, messageIDs(entries))
	require.Equal(t, "false", w.Header().Get("X-Has-Earlier-Messages"))
}

func TestGetSession_DetailStartsAtTheLastClearMarker(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	seedClearedSession(t, api.agentLoop.GetSessionStore(), sid)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid, nil)
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var detail struct {
		Messages   []session.TranscriptEntry `json:"messages"`
		HasEarlier *bool                     `json:"has_earlier_messages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.Equal(t, []string{"marker", "new-q"}, messageIDs(detail.Messages))
	require.NotNil(t, detail.HasEarlier)
	require.True(t, *detail.HasEarlier)
}

func TestGetSession_DetailOmitsTheFlagWhenNothingIsEarlier(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid, nil)
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "has_earlier_messages", "additive: absent when false")
}

func TestAttach_SnapshotReplaysSinceTheLastClearMarker(t *testing.T) {
	f := newAttachFixture(t, "turn-clear", "msg-clear")
	seedClearedSession(t, f.store, f.sid)

	wc := f.newConn("chat-clear")
	f.h.handleAttachSession(context.Background(), "chat-clear", f.sid, nil, wc)
	frames := drainWire(t, wc)

	snap := indexOfType(frames, "session_snapshot")
	require.GreaterOrEqual(t, snap, 0, "a first load is a snapshot")
	var parsed struct {
		HasEarlier *bool `json:"has_earlier_messages"`
	}
	require.NoError(t, json.Unmarshal(frames[snap].raw, &parsed))
	require.NotNil(t, parsed.HasEarlier, "the snapshot says earlier messages exist")
	require.True(t, *parsed.HasEarlier)

	var replayed strings.Builder
	for _, fr := range frames {
		replayed.Write(fr.raw)
	}
	require.NotContains(t, replayed.String(), "old question", "cleared content is not replayed")
	require.NotContains(t, replayed.String(), "old answer")
	require.Contains(t, replayed.String(), "new question")
}

func TestAttach_SnapshotWithoutAMarkerCarriesNoFlag(t *testing.T) {
	f := newAttachFixture(t, "turn-noclear", "msg-noclear")
	require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
		ID: "q", Role: "user", Content: "plain question", AgentID: "mia", Timestamp: time.Now().UTC(),
	}))

	wc := f.newConn("chat-noclear")
	f.h.handleAttachSession(context.Background(), "chat-noclear", f.sid, nil, wc)
	frames := drainWire(t, wc)
	snap := indexOfType(frames, "session_snapshot")
	require.GreaterOrEqual(t, snap, 0)
	require.NotContains(t, string(frames[snap].raw), "has_earlier_messages")
	var replayed strings.Builder
	for _, fr := range frames {
		replayed.Write(fr.raw)
	}
	require.Contains(t, replayed.String(), "plain question", "instrument check: the replay carries the transcript")
}
