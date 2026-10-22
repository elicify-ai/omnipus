// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Founder ruling: /clear clears what is on screen and moves the context
// forward. Every chat-view read starts at the last clear marker and says whether
// earlier entries exist; the archive is untouched. Paging D10 replaces only the
// carrier/continuation authority, not these exact content/identity assertions.

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

func messageIDs(entries []generated.Message) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Id)
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
	page := historyMigrationPage(t, sid, w.Body.Bytes(), "latest", true, false)
	require.Equal(t, []string{"marker", "new-q"}, messageIDs(page.Entries))
	require.True(t, page.HasEarlier, "the old true header's meaning now belongs to the generated page")
	require.NotNil(t, page.NextBefore, "earlier history has a real exclusive boundary")
	require.Equal(t, page.Entries[0].HistoryPosition, page.NextBefore)
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
	page := historyMigrationPage(t, sid, w.Body.Bytes(), "latest", false, false)
	require.Equal(t, []string{"q"}, messageIDs(page.Entries))
	require.False(t, page.HasEarlier, "the old false header's meaning is retained")
	require.Nil(t, page.NextBefore)
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
	detail := historyMigrationDetail(t, sid, w.Body.Bytes(), true)
	require.Equal(t, []string{"marker", "new-q"}, messageIDs(*detail.Messages))
	require.NotNil(t, detail.HasEarlier)
	require.True(t, *detail.HasEarlier)
	require.NotNil(t, detail.NextBefore)
}

func TestGetSession_DetailOmitsTheFlagWhenNothingIsEarlier(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid, nil)
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "has_earlier_messages", "the retired flag remains absent")
	detail := historyMigrationDetail(t, sid, w.Body.Bytes(), false)
	require.Empty(t, *detail.Messages)
	require.False(t, *detail.HasEarlier)
	require.Nil(t, detail.NextBefore)
}

func TestAttach_SnapshotReplaysSinceTheLastClearMarker(t *testing.T) {
	f := newAttachFixture(t, "turn-clear", "msg-clear")
	seedClearedSession(t, f.store, f.sid)

	wc := f.newConn("chat-clear")
	f.h.handleAttachSession(context.Background(), "chat-clear", f.sid, nil, wc)
	frames := drainWire(t, wc)

	snap := indexOfType(frames, "session_snapshot")
	require.GreaterOrEqual(t, snap, 0, "a first load is a snapshot")
	// Section R1: the visible divider and the question each draw one row;
	// metadata-only cancellation is a separate zero-row case.
	parsed := historyMigrationSnapshot(t, f.sid, frames[snap].raw, true, 2, 2)
	require.NotNil(t, parsed.NextBefore, "the snapshot says earlier messages exist with a real boundary")
	require.True(t, parsed.HasEarlier)

	var replayed strings.Builder
	var ids []string
	for _, fr := range frames {
		replayed.Write(fr.raw)
		if fr.Type == "replay_message" {
			require.NotNil(t, fr.ID, "both selected visible sources retain their original identity")
			ids = append(ids, *fr.ID)
			if *fr.ID == "marker" {
				require.Equal(t, "system", fr.Role)
				require.Equal(t, session.ClearMarkerText, fr.Content, "the actual visible divider is not omitted")
			}
		}
	}
	require.Equal(t, []string{"marker", "new-q"}, ids, "two drawn rows must be the actual divider and question, in order")
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
	parsed := historyMigrationSnapshot(t, f.sid, frames[snap].raw, false, 1, 1)
	require.False(t, parsed.HasEarlier)
	require.Nil(t, parsed.NextBefore)
	var replayed strings.Builder
	for _, fr := range frames {
		replayed.Write(fr.raw)
	}
	require.Contains(t, replayed.String(), "plain question", "instrument check: the replay carries the transcript")
}

// D3/D10 and the explicit clear-view regression requirement: the closed clear
// remains the default boundary, but an explicit exclusive before crosses it.
func TestGetSessionMessages_ExplicitBeforeCrossesTheLastClearMarker(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	seedClearedSession(t, api.agentLoop.GetSessionStore(), sid)
	latest := historyMigrationPage(t, sid, pagingRequest(api, sid, "").Body.Bytes(), "latest", true, false)
	require.Equal(t, []string{"marker", "new-q"}, messageIDs(latest.Entries))
	require.NotNil(t, latest.NextBefore)
	response := pagingRequest(api, sid, "before="+*latest.NextBefore)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	older := historyMigrationPage(t, sid, response.Body.Bytes(), "older", false, true)
	require.Equal(t, []string{"old-q", "old-a", "clear-row"}, messageIDs(older.Entries), "archive content still exists and is explicitly reachable")
	require.NotContains(t, messageIDs(older.Entries), "marker", "before is exclusive")
	require.NotContains(t, messageIDs(older.Entries), "new-q")
	require.Nil(t, older.NextBefore)
	require.Equal(t, older.Entries[len(older.Entries)-1].HistoryPosition, older.NextAfter)
}
