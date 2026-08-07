// ADR-057-U18-inverted: this file pinned the PRE-ADR-057 contract — REST
// cold-load session reads withheld delegation-child transcript entries
// (ParentSpawnCallID != "") exactly like the live-reconnect replay path did.
// D1/W11 (FR-034/FR-035/FR-038) retires that filter outright: a delegated
// child now owns its own real store-backed session (FR-005), so under the
// post-cutover design a child's own entries never land in another session's
// transcript to begin with — but for a PRE-cutover transcript (or any
// transcript carrying a legacy ParentSpawnCallID-tagged entry), no read
// boundary may reintroduce a visibility filter (FR-038, BDD-40: "a
// pre-cutover session shows previously-hidden delegate narration" is the
// accepted outcome, not a bug). This file is inverted, not deleted
// (FR-072/US-17 AS-1) — the assertions below now require BOTH entries to be
// returned unfiltered, matching BDD-37's "each read boundary returns the
// child's transcript unfiltered" for every one of the four read boundaries
// this suite already covered.
//
// Traces to: pkg/gateway/rest.go (getSession/getSessionMessages, U18),
// pkg/session/daypartition.go TranscriptEntry.ParentSpawnCallID (retained
// for provenance, FR-036) — the retired IsDelegateChildEntry predicate this
// file originally named no longer exists (FR-034).

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
// assistant entry and one legacy delegation-child-shaped entry
// (ParentSpawnCallID set) to the given session, mirroring the shape a
// pre-cutover spawnSubTurn/turn.go produced: the child carries the same
// Role/Content/AgentID/TurnID/Model shape as any other assistant entry,
// distinguishable only by ParentSpawnCallID (now provenance-only, FR-036).
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
		"seeding the delegation-child-shaped entry must succeed")
}

// TestGetSessionMessages_ReturnsDelegateChildEntriesUnfiltered is the
// inverted REST cold-load regression test for GET /api/v1/sessions/{id}/messages
// (ADR-057 FR-034/FR-035/FR-038, BDD-37/BDD-40): a transcript with a parent
// entry plus a ParentSpawnCallID-tagged entry must now return BOTH on the
// wire — the pre-ADR-057 filter that withheld the second entry is deleted,
// not reapplied.
func TestGetSessionMessages_ReturnsDelegateChildEntriesUnfiltered(t *testing.T) {
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

	// Positive lower bound (binding rule 4) before asserting anything about
	// which entries survived: both seeded entries must actually be present.
	require.Len(t, entries, 2,
		"both the parent entry and the ParentSpawnCallID-tagged entry must be "+
			"returned unfiltered (FR-034/FR-038) — got %d entries: %+v", len(entries), entries)

	var sawParent, sawChild bool
	for _, e := range entries {
		switch e.ID {
		case "parent-msg-1":
			sawParent = true
		case "child-msg-1":
			sawChild = true
			assert.Equal(t, "delegate-call-1", e.ParentSpawnCallID,
				"ParentSpawnCallID is retained as provenance (FR-036), not stripped")
			assert.Contains(t, e.Content, "external-cli permission",
				"the tagged entry's content must reach the wire unfiltered")
		}
	}
	assert.True(t, sawParent, "the parent entry must still be present")
	assert.True(t, sawChild, "the ParentSpawnCallID-tagged entry must no longer be withheld")
}

// TestGetSession_ReturnsDelegateChildEntriesUnfiltered is the inverted REST
// cold-load regression test for GET /api/v1/sessions/{id}: the same
// unfiltered contract must apply to the session-detail envelope's `messages`
// field.
func TestGetSession_ReturnsDelegateChildEntriesUnfiltered(t *testing.T) {
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

	require.Len(t, detail.Messages, 2,
		"both the parent entry and the ParentSpawnCallID-tagged entry must be "+
			"present, unfiltered, in the session-detail envelope's messages — "+
			"got %d entries: %+v", len(detail.Messages), detail.Messages)

	var sawParent, sawChild bool
	for _, e := range detail.Messages {
		switch e.ID {
		case "parent-msg-1":
			sawParent = true
		case "child-msg-1":
			sawChild = true
			assert.Contains(t, e.Content, "external-cli permission",
				"the delegate-shaped entry's raw narration must reach the session-detail messages unfiltered")
		}
	}
	assert.True(t, sawParent, "the parent entry must still be present")
	assert.True(t, sawChild, "the ParentSpawnCallID-tagged entry must no longer be withheld")
}
