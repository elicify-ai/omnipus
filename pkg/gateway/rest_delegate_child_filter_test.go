// W1 regression coverage: REST cold-load session reads must withhold
// delegation-child transcript entries (ParentSpawnCallID != "") exactly like
// the live-reconnect replay path (pkg/gateway/replay.go) already does.
//
// Root cause: a delegated sub-turn shares its parent's transcript session;
// each child entry is tagged ParentSpawnCallID. replay.go's live/reconnect
// path skips these (see IsDelegateChildEntry's doc comment,
// pkg/session/daypartition.go), but the REST cold-load path
// (getSessionMessages / getSession) previously returned the raw transcript
// via store.ReadTranscript with no such filter — so on a fresh page
// load/reopen, subagent narration (including "[external-cli permission]"
// lines) dumped as raw inline main-chat messages that a live reconnect never
// showed.
//
// Traces to: pkg/gateway/rest.go filterDelegateChildEntries,
// pkg/session/daypartition.go TranscriptEntry.IsDelegateChildEntry.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// seedParentAndChildTranscript appends one genuine top-level ("parent")
// assistant entry and one delegation-child entry (ParentSpawnCallID set) to
// the given session, mirroring the exact shape spawnSubTurn/turn.go produce:
// the child carries the same Role/Content/AgentID/TurnID/Model shape as any
// other assistant entry, distinguishable ONLY by ParentSpawnCallID.
func seedParentAndChildTranscript(t *testing.T, store *session.UnifiedStore, sessionID string) {
	t.Helper()

	const spawnCallID = "delegate-call-1"

	parentEntry := session.TranscriptEntry{
		ID:        "parent-msg-1",
		Role:      "assistant",
		Content:   "I'll delegate this research task.",
		Timestamp: time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC),
		AgentID:   "jim",
	}
	childEntry := session.TranscriptEntry{
		ID:                "child-msg-1",
		Role:              "assistant",
		Content:           "[external-cli permission] requesting read access to /tmp/report.md",
		Timestamp:         time.Date(2026, 7, 18, 10, 0, 5, 0, time.UTC),
		AgentID:           "researcher",
		Model:             "z-ai/glm-5.2",
		TurnID:            "child-turn-1",
		ParentSpawnCallID: spawnCallID,
	}

	require.NoError(t, store.AppendTranscript(sessionID, parentEntry),
		"seeding the parent entry must succeed")
	require.NoError(t, store.AppendTranscript(sessionID, childEntry),
		"seeding the delegation-child entry must succeed")
}

// TestGetSessionMessages_OmitsDelegateChildEntries is the REST cold-load
// regression test for GET /api/v1/sessions/{id}/messages: a transcript with
// a parent entry + a ParentSpawnCallID-tagged child entry must return ONLY
// the parent on the wire.
func TestGetSessionMessages_OmitsDelegateChildEntries(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	seedParentAndChildTranscript(t, store, sessionID)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"GET /sessions/{id}/messages must return 200; got %d body=%s", w.Code, w.Body.String())

	var entries []session.TranscriptEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries),
		"response must unmarshal into a list of transcript entries")

	require.Len(t, entries, 1,
		"only the parent entry may be returned — the delegation-child entry "+
			"(ParentSpawnCallID set) must be withheld, matching replay.go's live "+
			"reconnect filter; got %d entries: %+v", len(entries), entries)
	assert.Equal(t, "parent-msg-1", entries[0].ID)
	assert.Empty(t, entries[0].ParentSpawnCallID)
	assert.NotContains(t, entries[0].Content, "external-cli permission",
		"the delegate child's raw narration must never leak into the returned messages")
}

// TestGetSession_OmitsDelegateChildEntries is the REST cold-load regression
// test for GET /api/v1/sessions/{id}: the same filter must apply to the
// session-detail envelope's `messages` field.
func TestGetSession_OmitsDelegateChildEntries(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	seedParentAndChildTranscript(t, store, sessionID)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID, nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"GET /sessions/{id} must return 200; got %d body=%s", w.Code, w.Body.String())

	var detail struct {
		Messages []session.TranscriptEntry `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail),
		"response must unmarshal into a session-detail envelope")

	require.Len(t, detail.Messages, 1,
		"only the parent entry may be present in the session-detail envelope's "+
			"messages — the delegation-child entry must be withheld; got %d entries: %+v",
		len(detail.Messages), detail.Messages)
	assert.Equal(t, "parent-msg-1", detail.Messages[0].ID)
	assert.Empty(t, detail.Messages[0].ParentSpawnCallID)
	assert.NotContains(t, detail.Messages[0].Content, "external-cli permission",
		"the delegate child's raw narration must never leak into the session-detail messages")
}
