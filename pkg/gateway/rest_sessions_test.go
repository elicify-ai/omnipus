// rest_sessions_test.go: tests for session CRUD, messages, and the session wire shapes

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest.go tests 2026-09-15 ---

// ---------------------------------------------------------------------------
// T-G1: DeleteSession_HeartbeatGuard
// ---------------------------------------------------------------------------

// TestDeleteSession_HeartbeatGuard verifies:
//   - DELETE on a heartbeat session whose workspace heartbeat is ENABLED → 409
//     AND an audit entry with event="session.delete.blocked" is written (C-1).
//   - DELETE on a heartbeat session whose workspace heartbeat is DISABLED → 200.
//   - DELETE on a normal chat session → 200 (regression).
func TestDeleteSession_HeartbeatGuard(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	auditDir := filepath.Join(api.homePath, "system")

	const agentID = "mia"

	// ── subcase 1: protected heartbeat session ── //
	t.Run("enabled heartbeat blocks delete and emits audit", func(t *testing.T) {
		// Create the heartbeat session.
		meta := createHeartbeatSessionForAgent(t, api.agentLoop, agentID, "01JXHBTESTWSID0000000001")

		// Write workspace with enabled heartbeat pointing at this session.
		seedWorkspaceWithHeartbeat(t, api.homePath, agentID, meta.ID)

		w := deleteSessionViaAPI(t, api, meta.ID)
		assert.Equal(t, http.StatusConflict, w.Code,
			"DELETE protected heartbeat session must return 409; body=%s", w.Body.String())
		assert.Contains(t, w.Body.String(), "heartbeat",
			"409 body must mention heartbeat")

		// Flush audit log and check for the blocked event.
		require.NoError(t, api.auditor.Close())
		entries := readAuditEvents(t, auditDir)
		found := false
		for _, e := range entries {
			if e["event"] == "session.delete.blocked" {
				found = true
				assert.Equal(t, audit.DecisionDeny, e["decision"])
				details, _ := e["details"].(map[string]any)
				require.NotNil(t, details, "audit entry must have details")
				assert.Equal(t, meta.ID, details["session_id"])
				assert.Equal(t, agentID, details["agent_id"])
				assert.Equal(t, "heartbeat enabled", details["reason"])
			}
		}
		assert.True(t, found, "audit entry session.delete.blocked must be written; entries=%v", entries)
	})

	// ── subcase 2: disabled heartbeat allows delete ── //
	t.Run("disabled heartbeat allows delete", func(t *testing.T) {
		api2, _ := buildHeartbeatTestAPI(t)
		const agent2 = "mia"
		meta2 := createHeartbeatSessionForAgent(t, api2.agentLoop, agent2, "01JXHBTESTWSID0000000002")

		// Workspace with disabled heartbeat (session_id does NOT match the live one).
		wsDir2 := filepath.Join(api2.homePath, "workspaces")
		require.NoError(t, os.MkdirAll(wsDir2, 0o700))
		wsID2 := "01JXHBTESTWSID0000000002"
		ws2 := workspace.Workspace{
			ID: wsID2, Name: "WS2", Status: "active",
			CoreTeam: []string{agent2},
			MemberConfigs: map[string]workspace.MemberConfig{
				agent2: {Heartbeat: &workspace.MemberHeartbeat{
					Enabled:         false,
					IntervalMinutes: 10,
					Body:            "body",
					SessionID:       meta2.ID,
				}},
			},
			CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
		}
		data, err := json.MarshalIndent(ws2, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(wsDir2, wsID2+".json"), data, 0o600))

		w2 := deleteSessionViaAPI(t, api2, meta2.ID)
		assert.Equal(t, http.StatusOK, w2.Code,
			"DELETE session with disabled heartbeat must return 200; body=%s", w2.Body.String())
	})

	// ── subcase 3: normal chat session always deletable ── //
	t.Run("normal chat session is deletable", func(t *testing.T) {
		api3, _ := buildHeartbeatTestAPI(t)
		const agent3 = "mia"
		store3 := api3.agentLoop.GetAgentStore(agent3)
		require.NotNil(t, store3)
		chatMeta, err := store3.NewSession("chat", "webchat", agent3)
		require.NoError(t, err)

		w3 := deleteSessionViaAPI(t, api3, chatMeta.ID)
		assert.Equal(t, http.StatusOK, w3.Code,
			"DELETE normal chat session must return 200; body=%s", w3.Body.String())
	})
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

// TestGetSessionMessages_JudgeVerdictEntry_PopulatesWireVerdict proves the
// RV2 fix: a judge_verdict transcript entry's Content (raw
// json.Marshal(task.JudgeVerdict)) is parsed and attached as the wire
// Message.verdict object, and the response validates against Message.yaml
// (which requires verdict to conform to JudgeVerdict.yaml when present).
func TestGetSessionMessages_JudgeVerdictEntry_PopulatesWireVerdict(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	verdict := task.JudgeVerdict{
		ID:           "verdict-coldload-1",
		Scope:        "task",
		TaskID:       "task-xyz",
		Round:        1,
		Met:          false,
		Model:        "test-judge-model",
		JudgedAt:     "2026-07-19T12:05:00Z",
		JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{
			{CriterionID: "c1", Met: false, Reason: "missing evidence"},
		},
	}
	payload, err := json.Marshal(verdict)
	require.NoError(t, err)

	require.NoError(t, store.AppendTranscript(sessionID, session.TranscriptEntry{
		ID:        "verdict-entry-1",
		Type:      session.EntryTypeJudgeVerdict,
		Role:      "system",
		Content:   string(payload),
		AgentID:   "judge",
		Timestamp: time.Date(2026, 7, 19, 12, 5, 0, 0, time.UTC),
	}), "seeding a judge_verdict entry must succeed")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	var found map[string]any
	for _, e := range entries {
		if e["type"] == "judge_verdict" {
			found = e
			break
		}
	}
	require.NotNil(t, found, "must find the judge_verdict entry in the response")

	v, ok := found["verdict"].(map[string]any)
	require.True(t, ok, "judge_verdict entry must carry a populated 'verdict' object, got: %v", found)
	assert.Equal(t, "verdict-coldload-1", v["id"])
	assert.Equal(t, "task", v["scope"])
	assert.Equal(t, "task-xyz", v["task_id"])
	assert.Equal(t, false, v["met"])
	assert.Equal(t, "judge", v["judge_agent_id"])
	assert.Equal(t, float64(1), v["round"])

	// Bonus: the whole entry must validate against Message.yaml (verdict
	// conforms to JudgeVerdict.yaml, additionalProperties:false honored).
	schema := loadMessageSchema(t)
	assert.NoError(t, schema.Validate(any(found)), "judge_verdict entry with populated verdict must validate against Message.yaml")
}

// TestGetSessionMessages_JudgeVerdictEntry_MalformedContent_OmitsVerdict
// proves a judge_verdict entry with unparseable Content degrades gracefully:
// the entry still round-trips (id/type/content preserved) with no "verdict"
// key, rather than the whole response failing.
func TestGetSessionMessages_JudgeVerdictEntry_MalformedContent_OmitsVerdict(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	require.NoError(t, store.AppendTranscript(sessionID, session.TranscriptEntry{
		ID:        "bad-verdict-entry",
		Type:      session.EntryTypeJudgeVerdict,
		Role:      "system",
		Content:   "not valid json",
		AgentID:   "judge",
		Timestamp: time.Now().UTC(),
	}), "seeding a malformed judge_verdict entry must still succeed at the store layer")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	var found map[string]any
	for _, e := range entries {
		if e["id"] == "bad-verdict-entry" {
			found = e
			break
		}
	}
	require.NotNil(t, found, "the entry must still round-trip even with malformed content")
	_, hasVerdict := found["verdict"]
	assert.False(t, hasVerdict, "verdict must be absent (not a broken/partial object) when Content fails to parse")
}

// --- HandleSessions tests ---

// TestHandleSessionsList verifies that GET /api/v1/sessions returns 200 with an empty list
// when no sessions have been created yet.
// BDD: Given no sessions exist, When GET /api/v1/sessions is called,
// Then the response has status 200 and an empty array.
func TestHandleSessionsList(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	api.HandleSessions(w, r)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestHandleSessionsGetNotFound verifies 404 when session ID does not exist in any agent store.
// BDD: Given no sessions exist,
// When GET /api/v1/sessions/unknown-id is called,
// Then the response has status 404.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Session not found returns 404 (US-15 AC3)
func TestHandleSessionsGetNotFound(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/session_does_not_exist", nil)
	api.HandleSessions(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --------------------------------------------------------------------------
// Rename session tests
// --------------------------------------------------------------------------

// TestHandleSessions_Rename verifies that PUT /api/v1/sessions/{id} with a
// valid title returns 200 and the updated meta with the new title.
//
// BDD: Given a session exists with no title,
// When PUT /api/v1/sessions/{id} {"title": "My Renamed Session"} is called,
// Then 200 with the session meta containing title "My Renamed Session".
//
// Traces to: pkg/gateway/rest.go renameSession (Milestone 2)
func TestHandleSessions_Rename(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	// Given — create a session.
	sessionID := createTestSession(t, api)

	// When — rename it.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/sessions/"+sessionID,
		strings.NewReader(`{"title":"My Renamed Session"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(w, r)

	// Then — 200 with updated title.
	require.Equal(t, http.StatusOK, w.Code,
		"PUT rename must return 200; body=%s", w.Body.String())

	var meta session.UnifiedMeta
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &meta))
	assert.Equal(t, "My Renamed Session", meta.Title,
		"response meta must have the new title")
	assert.Equal(t, sessionID, meta.ID,
		"response meta must have the correct session ID")
}

// TestHandleSessions_RenameDifferentTitles verifies that renaming with two
// different title values produces two different responses — proving the handler
// is not hardcoded.
//
// Traces to: pkg/gateway/rest.go renameSession (Milestone 2) — differentiation test
func TestHandleSessions_RenameDifferentTitles(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	// Create two sessions.
	id1 := createTestSession(t, api)
	id2 := createTestSession(t, api)

	// Rename session 1 to "Alpha".
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPut, "/api/v1/sessions/"+id1,
		strings.NewReader(`{"title":"Alpha"}`))
	r1.Header.Set("Content-Type", "application/json")
	r1.URL.Path = "/api/v1/sessions/" + id1
	api.HandleSessions(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code)

	var m1 session.UnifiedMeta
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &m1))

	// Rename session 2 to "Beta".
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/sessions/"+id2,
		strings.NewReader(`{"title":"Beta"}`))
	r2.Header.Set("Content-Type", "application/json")
	r2.URL.Path = "/api/v1/sessions/" + id2
	api.HandleSessions(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)

	var m2 session.UnifiedMeta
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &m2))

	// Titles must differ — not hardcoded.
	assert.Equal(t, "Alpha", m1.Title, "session 1 must have title 'Alpha'")
	assert.Equal(t, "Beta", m2.Title, "session 2 must have title 'Beta'")
	assert.NotEqual(t, m1.Title, m2.Title,
		"two different rename calls must produce two different titles")
}

// TestHandleSessions_RenameEmptyTitle verifies that PUT with an empty title
// returns 400 Bad Request.
//
// BDD: Given a session exists,
// When PUT /api/v1/sessions/{id} {"title": ""} is called,
// Then 400 Bad Request.
//
// Traces to: pkg/gateway/rest.go renameSession empty-title guard (Milestone 2)
func TestHandleSessions_RenameEmptyTitle(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/sessions/"+sessionID,
		strings.NewReader(`{"title":""}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"PUT with empty title must return 400; body=%s", w.Body.String())
}

// TestHandleSessions_RenameBlankishTitleRejected proves a session title made
// only of whitespace or INVISIBLE runes is rejected, not just the empty string.
//
// A bare `req.Title == ""` check passed all of these, producing a session that
// renders blank in the sidebar and cannot be found by name. This is the same
// class of hole UAT round 2 found on plan/task titles (seven zero-width
// codepoints sailed through `strings.TrimSpace`, which only strips
// `unicode.IsSpace`), so this shares their predicate — task.HasVisibleContent —
// rather than growing a second, weaker rule that drifts out of sync.
//
// Traces to: pkg/gateway/rest.go renameSession title validation.
func TestHandleSessions_RenameBlankishTitleRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		// The invisible codepoints are written as JSON \uXXXX escapes, not literal
		// bytes: a literal U+FEFF in Go source is an "illegal byte order mark"
		// compile error, and the rest would be invisible in a diff. encoding/json
		// decodes them into the real runes before validation runs.
		{"spaces", `{"title":"   "}`},
		{"tabs and newlines", `{"title":"\t\n \r\n"}`},
		{"nbsp", `{"title":"\u00a0\u00a0"}`},
		{"zero-width space", `{"title":"\u200b\u200b\u200b"}`},
		{"zero-width non-joiner", `{"title":"\u200c"}`},
		{"zero-width joiner", `{"title":"\u200d"}`},
		{"word joiner", `{"title":"\u2060"}`},
		{"BOM", `{"title":"\ufeff"}`},
		{"soft hyphen", `{"title":"\u00ad"}`},
		{"braille blank", `{"title":"\u2800\u2800"}`},
		{"mixed invisibles", `{"title":" \u200b\ufeff\u00ad "}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			sessionID := createTestSession(t, api)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/sessions/"+sessionID,
				strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/sessions/" + sessionID
			api.HandleSessions(w, r)

			assert.Equal(t, http.StatusBadRequest, w.Code,
				"PUT with an invisible-only title (%s) must return 400; body=%s",
				tc.name, w.Body.String())
		})
	}
}

// TestHandleSessions_RenameTitleTrimmed proves surrounding whitespace is
// stripped rather than persisted, so " Real Title " and "Real Title" are not
// two visually identical but distinct sessions.
func TestHandleSessions_RenameTitleTrimmed(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sessionID := createTestSession(t, api)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/sessions/"+sessionID,
		strings.NewReader(`{"title":"   Real Title   "}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(w, r)

	assert.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"Real Title"`,
		"stored title must be trimmed; body=%s", w.Body.String())
}

// TestHandleSessions_RenameNotFound verifies that renaming a non-existent
// session returns 404.
//
// BDD: Given no session with ID "ghost-session-id" exists,
// When PUT /api/v1/sessions/ghost-session-id {"title": "x"} is called,
// Then 404 Not Found.
//
// Traces to: pkg/gateway/rest.go renameSession resolveSessionStore nil path (Milestone 2)
func TestHandleSessions_RenameNotFound(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/sessions/ghost-session-id",
		strings.NewReader(`{"title":"anything"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/sessions/ghost-session-id"
	api.HandleSessions(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"PUT rename for non-existent session must return 404; body=%s", w.Body.String())
}

// TestHandleSessions_RenamePersistence verifies that after a successful rename,
// GET /api/v1/sessions/{id} returns the updated title — proving the write is durable.
//
// BDD: Given a session is renamed to "Persisted Title",
// When GET /api/v1/sessions/{id} is called,
// Then the title in the response is "Persisted Title".
//
// Traces to: pkg/gateway/rest.go renameSession + getSession (Milestone 2)
func TestHandleSessions_RenamePersistence(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)

	// Rename.
	wRename := httptest.NewRecorder()
	rRename := httptest.NewRequest(http.MethodPut, "/api/v1/sessions/"+sessionID,
		strings.NewReader(`{"title":"Persisted Title"}`))
	rRename.Header.Set("Content-Type", "application/json")
	rRename.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(wRename, rRename)
	require.Equal(t, http.StatusOK, wRename.Code,
		"rename must succeed; body=%s", wRename.Body.String())

	// Read back via GET.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID, nil)
	rGet.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code,
		"GET after rename must return 200; body=%s", wGet.Body.String())

	var detail struct {
		Session  session.UnifiedMeta       `json:"session"`
		Messages []session.TranscriptEntry `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &detail))
	assert.Equal(t, "Persisted Title", detail.Session.Title,
		"GET must return the persisted title after rename")
}

// --------------------------------------------------------------------------
// Delete session tests
// --------------------------------------------------------------------------

// TestHandleSessions_Delete verifies that DELETE /api/v1/sessions/{id} returns
// 200 with {"success": true}.
//
// BDD: Given a session exists,
// When DELETE /api/v1/sessions/{id} is called,
// Then 200 with body {"success": true}.
//
// Traces to: pkg/gateway/rest.go deleteSession (Milestone 2)
func TestHandleSessions_Delete(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+sessionID, nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"DELETE must return 200; body=%s", w.Body.String())

	var resp map[string]bool
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["success"], "DELETE response must contain success:true")
}

// TestHandleSessions_DeleteNotFound verifies that DELETE for a non-existent
// session ID returns 404.
//
// BDD: Given no session with ID "nonexistent-id" exists,
// When DELETE /api/v1/sessions/nonexistent-id is called,
// Then 404 Not Found.
//
// Traces to: pkg/gateway/rest.go deleteSession resolveSessionStore nil path (Milestone 2)
func TestHandleSessions_DeleteNotFound(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/nonexistent-id", nil)
	r.URL.Path = "/api/v1/sessions/nonexistent-id"
	api.HandleSessions(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"DELETE for non-existent session must return 404; body=%s", w.Body.String())
}

// TestHandleSessions_DeletedSessionGone verifies that after deletion, GET
// returns 404 — confirming the directory is actually removed, not just hidden.
//
// BDD: Given a session is deleted via DELETE,
// When GET /api/v1/sessions/{id} is called after deletion,
// Then 404 Not Found.
//
// Traces to: pkg/gateway/rest.go deleteSession + getSession (Milestone 2) — persistence test
func TestHandleSessions_DeletedSessionGone(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)

	// Delete.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+sessionID, nil)
	rDel.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(wDel, rDel)
	require.Equal(t, http.StatusOK, wDel.Code,
		"DELETE must return 200; body=%s", wDel.Body.String())

	// GET after delete — must 404.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID, nil)
	rGet.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(wGet, rGet)

	assert.Equal(t, http.StatusNotFound, wGet.Code,
		"GET after DELETE must return 404 — session must be truly gone, not just flagged")
}

// TestHandleSessions_DeleteRemovedFromList verifies that after deletion the
// session no longer appears in GET /api/v1/sessions list.
//
// Traces to: pkg/gateway/rest.go deleteSession + listSessions (Milestone 2) — persistence test
func TestHandleSessions_DeleteRemovedFromList(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)

	// Verify it appears in the list before deletion.
	wList1 := httptest.NewRecorder()
	rList1 := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	rList1.URL.Path = "/api/v1/sessions"
	api.HandleSessions(wList1, rList1)
	require.Equal(t, http.StatusOK, wList1.Code)

	// ADR-057 FR-091 (grill2 M2-10): listSessions now always returns the
	// named gen.SessionPage envelope ({"sessions": [...], ...}), not a bare
	// array — unwrap it before the identical field-level assertions below.
	var page1 struct {
		Sessions []session.UnifiedMeta `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(wList1.Body.Bytes(), &page1))
	sessions1 := page1.Sessions
	found := false
	for _, s := range sessions1 {
		if s.ID == sessionID {
			found = true
			break
		}
	}
	require.True(t, found, "session must appear in list before deletion")

	// Delete.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+sessionID, nil)
	rDel.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(wDel, rDel)
	require.Equal(t, http.StatusOK, wDel.Code)

	// Verify it no longer appears in the list.
	wList2 := httptest.NewRecorder()
	rList2 := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	rList2.URL.Path = "/api/v1/sessions"
	api.HandleSessions(wList2, rList2)
	require.Equal(t, http.StatusOK, wList2.Code)

	var page2 struct {
		Sessions []session.UnifiedMeta `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(wList2.Body.Bytes(), &page2))
	sessions2 := page2.Sessions
	for _, s := range sessions2 {
		assert.NotEqual(t, sessionID, s.ID,
			"deleted session must not appear in list after DELETE")
	}
}

// TestGetSessionMessages_ToolCallEntries_PassesWireSchema reproduces the
// production bug at the handler level. Without the Message.yaml enum fix
// applied in this branch, this test fails — the JSON emitted by
// getSessionMessages contains `"type":"tool_call"` which Message.yaml
// rejected pre-fix.
//
// Traces to: pkg/gateway/rest.go getSessionMessages (Bug-1 reproducer).
func TestGetSessionMessages_ToolCallEntries_PassesWireSchema(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)

	// Seed a tool_call entry — the exact shape that broke on the public IP
	// build (an entry with type="tool_call" and a non-empty tool_calls array).
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	toolCallEntry := session.TranscriptEntry{
		ID:        "call_test_001",
		Type:      session.EntryType("tool_call"),
		Timestamp: time.Date(2026, 5, 21, 4, 20, 0, 0, time.UTC),
		AgentID:   "main",
		ToolCalls: []session.ToolCall{
			{
				ID:         "call_test_001",
				Tool:       "write_file",
				Status:     "success",
				DurationMS: 3,
				Parameters: map[string]any{"path": "/tmp/x.txt", "content": "hi"},
			},
		},
	}
	require.NoError(t, store.AppendTranscript(sessionID, toolCallEntry),
		"seeding a tool_call entry must succeed")

	// Call the real REST handler.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"GET /sessions/{id}/messages must return 200; got %d body=%s",
		w.Code, w.Body.String())

	// Parse the response and validate every entry against Message.yaml.
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries),
		"response must be a JSON array of message objects")
	require.NotEmpty(t, entries, "must have at least one entry seeded")

	schema := loadMessageSchema(t)
	for i, entry := range entries {
		raw, err := json.Marshal(entry)
		require.NoError(t, err)
		validationErr := schema.Validate(any(entry))
		assert.NoErrorf(t, validationErr,
			"entry[%d] must validate against Message.yaml; raw=%s", i, string(raw))
	}

	// And specifically: the tool_call entry must have type="tool_call" on the wire.
	foundToolCall := false
	for _, entry := range entries {
		if entry["type"] == "tool_call" {
			foundToolCall = true
			break
		}
	}
	assert.True(t, foundToolCall,
		"the seeded tool_call entry must round-trip through the handler with type=\"tool_call\"")
}

// TestGetSessionMessages_InterruptedToolCall_PassesWireSchema is the Bug-1
// regression test for the hotfix/v0.1.1 live re-verification finding: a
// ToolCall record carrying status="interrupted" (written by spawnSubTurn,
// pkg/agent/subturn.go:989, onto the spawning delegate/spawn tool call's own
// persisted record when the parent turn is canceled mid-flight) failed the
// SPA's mandatory zod validation on reload because ToolCall.yaml's status
// enum never listed "interrupted" — even though the equivalent live-WS
// frame (SubagentEndFrame.yaml) already did. Any session containing an
// interrupted delegation was therefore permanently unloadable
// ("Could not load messages... Backend response failed validation").
//
// Traces to: contracts/components/schemas/ToolCall.yaml status enum.
func TestGetSessionMessages_InterruptedToolCall_PassesWireSchema(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	interruptedEntry := session.TranscriptEntry{
		ID:        "call_interrupted_001",
		Type:      session.EntryType("tool_call"),
		Timestamp: time.Date(2026, 7, 12, 4, 20, 0, 0, time.UTC),
		AgentID:   "main",
		ToolCalls: []session.ToolCall{
			{
				ID:         "call_interrupted_001",
				Tool:       "delegate",
				Status:     "interrupted",
				DurationMS: 1500,
				Parameters: map[string]any{"target_agent_id": "jim"},
			},
		},
	}
	require.NoError(t, store.AppendTranscript(sessionID, interruptedEntry),
		"seeding an interrupted tool_call entry must succeed")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"GET /sessions/{id}/messages must return 200 for a session containing an "+
			"interrupted delegation; got %d body=%s", w.Code, w.Body.String())

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries),
		"response must be a JSON array of message objects")
	require.NotEmpty(t, entries, "must have at least one entry seeded")

	// This is the exact assertion that failed pre-fix: the SPA's zod schema
	// (generated 1:1 from ToolCall.yaml) rejected the whole payload on an
	// unrecognized "interrupted" enum value. Validating against the compiled
	// Message.yaml (which embeds ToolCall.yaml via tool_calls[]) reproduces
	// that same rejection at the Go level.
	schema := loadMessageSchema(t)
	foundInterrupted := false
	for i, entry := range entries {
		raw, err := json.Marshal(entry)
		require.NoError(t, err)
		validationErr := schema.Validate(any(entry))
		assert.NoErrorf(t, validationErr,
			"entry[%d] must validate against Message.yaml (status=\"interrupted\" must be a "+
				"declared ToolCall.status enum value); raw=%s", i, string(raw))
		if toolCalls, ok := entry["tool_calls"].([]any); ok {
			for _, tc := range toolCalls {
				tcMap, ok := tc.(map[string]any)
				if ok && tcMap["status"] == "interrupted" {
					foundInterrupted = true
				}
			}
		}
	}
	assert.True(t, foundInterrupted,
		"the seeded interrupted tool call must round-trip through the handler with status=\"interrupted\"")
}

// TestGetSessionMessages_TurnCanceledEntry_PassesWireSchema covers the
// second EntryType missing from the wire schema pre-fix. A real canceled
// turn produces an entry with Type=turn_canceled plus cancel-specific
// fields (TurnID, CancelledByUser, etc.). The post-fix schema accepts both
// the new enum value AND the previously-undefined fields.
//
// Traces to: pkg/agent/cancel.go (FR-15) → pkg/session/daypartition.go EntryTypeTurnCancelled.
func TestGetSessionMessages_TurnCanceledEntry_PassesWireSchema(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	cancelEntry := session.TranscriptEntry{
		ID:                   "cancel_entry_001",
		Type:                 session.EntryTypeTurnCancelled,
		Timestamp:            time.Date(2026, 5, 21, 4, 25, 0, 0, time.UTC),
		AgentID:              "main",
		TurnID:               "turn-T3",
		CancelledByUser:      "admin",
		CancelledByChannel:   "webchat",
		CancelMethod:         "graceful",
		DescendantsCancelled: []string{"turn-T3-sub-1"},
	}
	require.NoError(t, store.AppendTranscript(sessionID, cancelEntry))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	schema := loadMessageSchema(t)
	for i, entry := range entries {
		validationErr := schema.Validate(any(entry))
		raw, err := json.Marshal(entry)
		require.NoError(t, err)
		assert.NoErrorf(t, validationErr,
			"entry[%d] must validate against Message.yaml; raw=%s", i, string(raw))
	}

	// Verify the cancel-specific fields are actually on the wire (not stripped
	// by Go's json:"omitempty" tags). If a future TranscriptEntry refactor drops
	// these fields the SPA's cancel UI would silently regress.
	foundCancel := false
	for _, entry := range entries {
		if entry["type"] == "turn_canceled" {
			foundCancel = true
			assert.Equal(t, "turn-T3", entry["turn_id"],
				"turn_id field must round-trip")
			assert.Equal(t, "admin", entry["canceled_by_user"],
				"canceled_by_user field must round-trip")
			assert.Equal(t, "graceful", entry["cancel_method"],
				"cancel_method field must round-trip")
			break
		}
	}
	assert.True(t, foundCancel, "seeded turn_canceled entry must round-trip")
}

// TestGetSessionMessages_TruncatedEntry_PassesWireSchema is the ADR-087 D2/§7.1
// contract fixture: seeds a real assistant TranscriptEntry with Truncated=true
// AND TruncationReason="max_output_tokens" set, so the Message.yaml
// additionalProperties:false check actually exercises both new fields.
// Before this fixture, no test in this file ever set Truncated at all — an
// omitempty field with no populated fixture ships green without ever being
// validated (the false-green pattern this file's own header describes).
//
// Traces to: contracts/components/schemas/Message.yaml (truncated,
// truncation_reason) and pkg/session/daypartition.go's TranscriptEntry.
func TestGetSessionMessages_TruncatedEntry_PassesWireSchema(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	truncatedEntry := session.TranscriptEntry{
		ID:               "msg_truncated_001",
		Role:             "assistant",
		Content:          "Here is the first part of a long answer that got cut off",
		Timestamp:        time.Date(2026, 9, 13, 4, 20, 0, 0, time.UTC),
		AgentID:          "main",
		TurnID:           "turn-T9",
		Truncated:        true,
		TruncationReason: "max_output_tokens",
	}
	require.NoError(t, store.AppendTranscript(sessionID, truncatedEntry),
		"seeding a truncated assistant entry must succeed")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"GET /sessions/{id}/messages must return 200; got %d body=%s",
		w.Code, w.Body.String())

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries),
		"response must be a JSON array of message objects")
	require.NotEmpty(t, entries, "must have at least one entry seeded")

	schema := loadMessageSchema(t)
	for i, entry := range entries {
		raw, err := json.Marshal(entry)
		require.NoError(t, err)
		validationErr := schema.Validate(any(entry))
		assert.NoErrorf(t, validationErr,
			"entry[%d] must validate against Message.yaml; raw=%s", i, string(raw))
	}

	// The exact assertion this fixture exists for: both new fields must
	// round-trip onto the wire unchanged, not be stripped by omitempty or
	// dropped by the handler's pass-through marshal.
	foundTruncated := false
	for _, entry := range entries {
		if entry["id"] == "msg_truncated_001" {
			foundTruncated = true
			assert.Equal(t, true, entry["truncated"],
				"truncated field must round-trip as true")
			assert.Equal(t, "max_output_tokens", entry["truncation_reason"],
				"truncation_reason field must round-trip")
		}
	}
	assert.True(t, foundTruncated, "the seeded truncated entry must round-trip through the handler")
}

// TestGetSession_TranscriptWithCancelledTurn_PassesSessionDetailSchema covers
// the envelope shape jsonSessionDetail emits — {session, messages,
// agent_removed?}. The Session.partitions array regression we fixed earlier
// in this branch landed in this exact envelope; this test pins the second
// half of the contract (the messages array) against the same shape.
//
// Traces to: pkg/gateway/rest.go jsonSessionDetail.
func TestGetSession_TranscriptWithMixedEntries_PassesSessionDetailSchema(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	mixed := []session.TranscriptEntry{
		{
			ID:        "msg_user_1",
			Role:      "user",
			Content:   "hello",
			Timestamp: time.Date(2026, 5, 21, 4, 20, 0, 0, time.UTC),
			AgentID:   "main",
		},
		{
			ID:        "msg_asst_1",
			Role:      "assistant",
			Content:   "hi back",
			Timestamp: time.Date(2026, 5, 21, 4, 20, 1, 0, time.UTC),
			AgentID:   "main",
		},
		{
			ID:        "call_001",
			Type:      session.EntryType("tool_call"),
			Timestamp: time.Date(2026, 5, 21, 4, 20, 2, 0, time.UTC),
			AgentID:   "main",
			ToolCalls: []session.ToolCall{
				{ID: "call_001", Tool: "write_file", Status: "success"},
			},
		},
		{
			ID:              "cancel_001",
			Type:            session.EntryTypeTurnCancelled,
			Timestamp:       time.Date(2026, 5, 21, 4, 20, 3, 0, time.UTC),
			AgentID:         "main",
			TurnID:          "turn-1",
			CancelledByUser: "admin",
			CancelMethod:    "graceful",
		},
	}
	for _, e := range mixed {
		require.NoError(t, store.AppendTranscript(sessionID, e))
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID, nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"GET /sessions/{id} must return 200; got %d body=%s", w.Code, w.Body.String())

	// Validate the envelope against SessionDetail.yaml.
	thisFile := gatewayTestCallerFile(t)
	contractsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "contracts", "components", "schemas")
	loader := newYAMLSchemaLoader(t)
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(loader)
	schema, err := compiler.Compile("file://" + filepath.Join(contractsDir, "SessionDetail.yaml"))
	require.NoError(t, err, "must compile SessionDetail.yaml")

	var envelope any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))

	validationErr := schema.Validate(envelope)
	assert.NoError(t, validationErr,
		"jsonSessionDetail envelope must validate against SessionDetail.yaml; "+
			"body=%s", w.Body.String())
}

// TestGetSessions_ScheduledTypeFilter_RoundTrip verifies that a session of type
// "scheduled" (minted server-side by NewScheduledSession / fired schedule) is:
//  1. Returned by GET /api/v1/sessions with type="scheduled" in the wire JSON.
//  2. Returned by GET /api/v1/sessions/{id} with the correct type.
//  3. NOT returned by GET /api/v1/sessions?type=chat (exclusive filter).
//  4. Returned by GET /api/v1/sessions?type=scheduled (positive filter).
//
// This pins the FIX 1 from the contract/test review: the filter enum in
// contracts/openapi.yaml was missing "scheduled", so clients couldn't request
// the ?type=scheduled filter even though such sessions exist. It also pins the
// wire round-trip: gen.SessionType already includes SessionTypeScheduled (added
// in 64e44c51), so the session must survive with type="scheduled" through the
// handler, not be dropped or coerced.
//
// Traces to: contracts/openapi.yaml GET /sessions type filter param enum.
func TestGetSessions_ScheduledTypeFilter_RoundTrip(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	// Mint a scheduled session directly through the store (server-minted path —
	// POST /sessions intentionally rejects "scheduled" from clients).
	scheduledMeta, err := store.NewScheduledSession("main")
	require.NoError(t, err, "NewScheduledSession must succeed")
	require.NotEmpty(t, scheduledMeta.ID, "scheduled session must have an ID")
	require.Equal(t, session.SessionTypeScheduled, scheduledMeta.Type,
		"NewScheduledSession must stamp type=scheduled")

	// Also mint a chat session so we can verify the exclusive filter.
	chatMeta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err, "NewSession(chat) must succeed")

	// 1. GET /api/v1/sessions (no filter) — both sessions appear.
	wAll := httptest.NewRecorder()
	rAll := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	rAll.URL.Path = "/api/v1/sessions"
	api.HandleSessions(wAll, rAll)
	require.Equal(t, http.StatusOK, wAll.Code,
		"GET /sessions must return 200; body=%s", wAll.Body.String())

	// ADR-057 FR-091 (grill2 M2-10): listSessions now always returns the
	// named gen.SessionPage envelope ({"sessions": [...], ...}), not a bare
	// array — unwrap it before the same map[string]any assertions below.
	var allPage struct {
		Sessions []map[string]any `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(wAll.Body.Bytes(), &allPage),
		"GET /sessions response must decode as a SessionPage envelope")
	allSessions := allPage.Sessions

	foundScheduledInAll := false
	for _, s := range allSessions {
		if s["id"] == scheduledMeta.ID {
			foundScheduledInAll = true
			assert.Equal(t, "scheduled", s["type"],
				"scheduled session must appear with type=\"scheduled\" in full list")
		}
	}
	assert.True(t, foundScheduledInAll,
		"scheduled session (id=%s) must appear in unfiltered GET /sessions", scheduledMeta.ID)

	// 2. GET /api/v1/sessions?type=scheduled — only the scheduled session.
	wSched := httptest.NewRecorder()
	rSched := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?type=scheduled", nil)
	rSched.URL.Path = "/api/v1/sessions"
	api.HandleSessions(wSched, rSched)
	require.Equal(t, http.StatusOK, wSched.Code,
		"GET /sessions?type=scheduled must return 200; body=%s", wSched.Body.String())

	var schedPage struct {
		Sessions []map[string]any `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(wSched.Body.Bytes(), &schedPage),
		"GET /sessions?type=scheduled response must decode as a SessionPage envelope")
	scheduledSessions := schedPage.Sessions

	require.NotEmpty(t, scheduledSessions,
		"GET /sessions?type=scheduled must return at least one session")
	for _, s := range scheduledSessions {
		assert.Equal(t, "scheduled", s["type"],
			"GET /sessions?type=scheduled must return ONLY sessions with type=scheduled; got %v", s["type"])
	}
	foundScheduledInFilter := false
	for _, s := range scheduledSessions {
		if s["id"] == scheduledMeta.ID {
			foundScheduledInFilter = true
		}
	}
	assert.True(t, foundScheduledInFilter,
		"the minted scheduled session must appear in ?type=scheduled results")

	// 3. GET /api/v1/sessions?type=chat — must NOT include the scheduled session.
	wChat := httptest.NewRecorder()
	rChat := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?type=chat", nil)
	rChat.URL.Path = "/api/v1/sessions"
	api.HandleSessions(wChat, rChat)
	require.Equal(t, http.StatusOK, wChat.Code,
		"GET /sessions?type=chat must return 200; body=%s", wChat.Body.String())

	var chatPage struct {
		Sessions []map[string]any `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(wChat.Body.Bytes(), &chatPage),
		"GET /sessions?type=chat response must decode as a SessionPage envelope")
	chatSessions := chatPage.Sessions

	for _, s := range chatSessions {
		assert.NotEqual(t, scheduledMeta.ID, s["id"],
			"scheduled session must NOT appear in ?type=chat results")
	}
	foundChatInFilter := false
	for _, s := range chatSessions {
		if s["id"] == chatMeta.ID {
			foundChatInFilter = true
			assert.Equal(t, "chat", s["type"],
				"chat session must have type=chat in filtered results")
		}
	}
	assert.True(t, foundChatInFilter,
		"the minted chat session must appear in ?type=chat results")

	// 4. GET /api/v1/sessions/{id} — scheduled session round-trips with correct type.
	wDet := httptest.NewRecorder()
	rDet := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+scheduledMeta.ID, nil)
	rDet.URL.Path = "/api/v1/sessions/" + scheduledMeta.ID
	api.HandleSessions(wDet, rDet)
	require.Equal(t, http.StatusOK, wDet.Code,
		"GET /sessions/{id} for scheduled session must return 200; body=%s", wDet.Body.String())

	var detail map[string]any
	require.NoError(t, json.Unmarshal(wDet.Body.Bytes(), &detail),
		"GET /sessions/{id} response must be a JSON object")

	sessionObj, ok := detail["session"].(map[string]any)
	require.True(t, ok, "SessionDetail envelope must have a 'session' field; got %v", detail)
	assert.Equal(t, "scheduled", sessionObj["type"],
		"scheduled session detail must have type=\"scheduled\" on the wire")
}

// TestUnifiedMetaToGenSession_MapsUpdatedAt pins the regression where the session
// wire mapper omitted UpdatedAt, so every session serialized Go's zero time
// ("0001-01-01T00:00:00Z") — breaking ListSessions sort order and the SPA's
// relative-time display.
func TestUnifiedMetaToGenSession_MapsUpdatedAt(t *testing.T) {
	created := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 4, 2, 12, 30, 0, 0, time.UTC)
	m := &session.UnifiedMeta{
		SessionMeta: session.SessionMeta{
			ID:        "sess-1",
			AgentID:   "jim",
			Channel:   "webchat",
			Title:     "hello",
			Status:    session.SessionStatus("active"),
			CreatedAt: created,
			UpdatedAt: updated,
		},
		Type: session.SessionTypeChat,
	}
	s := unifiedMetaToGenSession(m)
	require.False(t, s.UpdatedAt.IsZero(),
		"UpdatedAt must not be the Go zero time — the wire mapper used to drop it")
	assert.True(t, s.UpdatedAt.Equal(updated),
		"UpdatedAt should map from meta: got %v, want %v", s.UpdatedAt, updated)
	assert.True(t, s.CreatedAt.Equal(created), "CreatedAt should still map correctly")
}

// TestListSessions_ExcludesVerifierByDefault verifies GET /api/v1/sessions
// (no include_verifier param) omits verifier-type sessions while still
// returning ordinary chat sessions.
//
// BDD: Given a chat session and a verifier session both exist,
// When GET /api/v1/sessions is called with no include_verifier param,
// Then only the chat session appears in the response.
//
// Traces to: FR-036, US-13 Acceptance 6, Test 25.
func TestListSessions_ExcludesVerifierByDefault(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	chatMeta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	verifierMeta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	api.listSessions(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	got := decodeSessionList(t, w.Body.Bytes())

	ids := sessionIDs(got)
	assert.Contains(t, ids, chatMeta.ID, "chat session must be present")
	assert.NotContains(t, ids, verifierMeta.ID, "verifier session must be excluded by default")
}

// TestListSessions_IncludesVerifierWithParam verifies GET
// /api/v1/sessions?include_verifier=true surfaces verifier-type sessions
// alongside ordinary ones.
//
// BDD: Given a chat session and a verifier session both exist,
// When GET /api/v1/sessions?include_verifier=true is called,
// Then both sessions appear in the response.
//
// Traces to: FR-036, US-13 Acceptance 6, Test 25.
func TestListSessions_IncludesVerifierWithParam(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	chatMeta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	verifierMeta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?include_verifier=true", nil)
	api.listSessions(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	got := decodeSessionList(t, w.Body.Bytes())

	ids := sessionIDs(got)
	assert.Contains(t, ids, chatMeta.ID, "chat session must still be present")
	assert.Contains(t, ids, verifierMeta.ID, "verifier session must be included with include_verifier=true")

	// The returned verifier entry must round-trip its type as "verifier" on
	// the wire (not silently coerced/omitted).
	for _, s := range got {
		if s.ID == verifierMeta.ID {
			assert.Equal(t, "verifier", s.Type, "wire type must be verifier")
		}
	}
}

// TestListSessions_TypeFilterVerifierAloneStillExcluded verifies that an
// explicit ?type=verifier filter, WITHOUT include_verifier=true, still
// excludes verifier sessions — per openapi.yaml's listSessions description:
// "Verifier-role sessions ... are excluded by default regardless of the type
// filter unless include_verifier=true is passed." Combining both params is
// required to actually retrieve them via the type filter.
//
// Traces to: FR-036, contracts/openapi.yaml listSessions description.
func TestListSessions_TypeFilterVerifierAloneStillExcluded(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	verifierMeta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	// type=verifier alone -> still excluded.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?type=verifier", nil)
	api.listSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	got := decodeSessionList(t, w.Body.Bytes())
	assert.Empty(t, got, "type=verifier alone must NOT surface verifier sessions; body=%s", w.Body.String())

	// type=verifier + include_verifier=true -> now included.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?type=verifier&include_verifier=true", nil)
	api.listSessions(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "body=%s", w2.Body.String())
	got2 := decodeSessionList(t, w2.Body.Bytes())
	require.Len(t, got2, 1, "type=verifier + include_verifier=true must return exactly the verifier session; body=%s", w2.Body.String())
	assert.Equal(t, verifierMeta.ID, got2[0].ID)
}

// TestListSessions_IncludeVerifierFalseExplicit verifies that an explicit
// include_verifier=false behaves identically to the param being absent
// (belt-and-braces on the strconv.ParseBool default-false path).
func TestListSessions_IncludeVerifierFalseExplicit(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	verifierMeta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?include_verifier=false", nil)
	api.listSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	got := decodeSessionList(t, w.Body.Bytes())
	assert.NotContains(t, sessionIDs(got), verifierMeta.ID)
}

// TestCreateSessionHTTP_RejectsWorker verifies RESIDUAL PATH 5: an explicit worker
// agent_id on POST /api/v1/sessions returns 400 — a worker cannot back a chat
// session. A base agent (control) returns 201.
func TestCreateSessionHTTP_RejectsWorker(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)

	// Worker agent_id → 400.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"agent_id":"hans"}`))
	api.createSessionHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"a worker agent_id must be rejected with 400 when creating a session")
	assert.Contains(t, strings.ToLower(w.Body.String()), "worker",
		"the error must explain a worker cannot back a session")

	// Control: base agent → 201.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"agent_id":"mia"}`))
	api.createSessionHTTP(w2, r2)
	require.Equal(t, http.StatusCreated, w2.Code,
		"a base agent must be able to back a session (control)")
}

// TestFirstChatTargetAgentID_SkipsWorker verifies RESIDUAL PATH 6: the last-resort
// fallback never lands on a worker even when the worker appears first in the list.
// It returns the first chat-target agent instead.
func TestFirstChatTargetAgentID_SkipsWorker(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				// Worker appears first but must be skipped — workers are never chat targets.
				{ID: "hans", Type: config.AgentTypeWorker},
				{ID: "mia"},
			},
		},
	}
	got := firstChatTargetAgentID(cfg)
	assert.NotEqual(t, "hans", got, "firstChatTargetAgentID must never return a worker")
	assert.Equal(t, "mia", got, "firstChatTargetAgentID must return the first chat-target agent")
}

// TestFirstChatTargetAgentID_AllWorkersReturnsEmpty verifies the degenerate case:
// when every agent is a worker, the fallback returns "" rather than a
// worker (the caller then surfaces a "no agent configured" error).
func TestFirstChatTargetAgentID_AllWorkersReturnsEmpty(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "w1", Type: config.AgentTypeWorker},
				{ID: "w2", Type: config.AgentTypeWorker},
			},
		},
	}
	assert.Equal(t, "", firstChatTargetAgentID(cfg),
		"when all agents are workers, the fallback must return empty, never a worker")
}
