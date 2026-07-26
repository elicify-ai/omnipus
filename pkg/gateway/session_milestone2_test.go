//go:build !cgo

// Milestone 2 REST handler tests: PUT /api/v1/sessions/{id} (rename)
// and DELETE /api/v1/sessions/{id} (delete).
//
// BDD scenarios:
//   Scenario: Rename session — PUT with valid title returns 200 + updated meta
//   Scenario: Rename with empty title — PUT with empty title returns 400
//   Scenario: Rename non-existent session — PUT returns 404
//   Scenario: Rename persistence — renamed title is readable via GET
//   Scenario: Delete session — DELETE returns 200 + success:true
//   Scenario: Delete non-existent session — DELETE returns 404
//   Scenario: Deleted session gone — GET after DELETE returns 404
//
// Traces to: pkg/gateway/rest.go renameSession + deleteSession (Milestone 2)

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// createTestSession creates a session via POST /api/v1/sessions and returns its ID.
// The test fails fatally if the session cannot be created.
func createTestSession(t *testing.T, api *restAPI) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions",
		strings.NewReader(`{"agent_id":"main","type":"chat"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/sessions"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusCreated, w.Code,
		"POST /api/v1/sessions must return 201 to set up test; got %d body=%s",
		w.Code, w.Body.String())

	var meta session.UnifiedMeta
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &meta),
		"POST /api/v1/sessions response must unmarshal into UnifiedMeta")
	require.NotEmpty(t, meta.ID, "created session must have a non-empty ID")
	return meta.ID
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

	var sessions1 []session.UnifiedMeta
	require.NoError(t, json.Unmarshal(wList1.Body.Bytes(), &sessions1))
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

	var sessions2 []session.UnifiedMeta
	require.NoError(t, json.Unmarshal(wList2.Body.Bytes(), &sessions2))
	for _, s := range sessions2 {
		assert.NotEqual(t, sessionID, s.ID,
			"deleted session must not appear in list after DELETE")
	}
}
