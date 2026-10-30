// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, issue #1471: Sessions API — PUT/DELETE on any sub-path renames or
// deletes the whole chat.
//
// ORACLE: issue #1471 "Expected" plus contracts/openapi.yaml — /sessions/{id}
// carries GET (getSession), PUT (renameSession) and DELETE (deleteSession);
// /sessions/{id}/messages is a defined GET-only sub-resource (getSessionMessages)
// and /sessions/{id}/tool-results/{ref} a defined GET-only sub-resource
// (getToolResult). No expected status or header below was read off the
// implementation:
//
//	PUT/DELETE bare /sessions/{id}      → allowed (contract: renameSession / deleteSession)
//	PUT/DELETE .../messages|messages/0  → 405  (issue primary rule "any sub-path
//	                                      returns 405": messages is a DEFINED
//	                                      GET-only sub-resource, so a non-GET on it
//	                                      is Method Not Allowed, RFC 9110 §15.5.5;
//	                                      the issue names messages/{position} — the
//	                                      #1452 D2 per-message address — explicitly)
//	PUT/DELETE .../unknown              → 404  (issue: "or 404 for an unknown
//	                                      sub-resource"; no such sub-resource exists
//	                                      in the contract)
//	every 405 answer                    → Allow: GET (CHECK-agreed rule; the contract
//	                                      makes both sub-resources GET-only, so GET
//	                                      is the one permitted method to advertise)
//	GET .../messages                    → 200  (control: contract-defined getSessionMessages)
//	GET .../messages/0                  → 404  (issue: "GET on an unknown sub-path
//	                                      returns 404"; no messages/{position} route
//	                                      exists on this base — when #1452 D2 lands
//	                                      this case becomes a defined GET and the
//	                                      expected value must be revisited then)
//	GET .../unknown                     → 404  (issue: "GET on an unknown sub-path returns 404")
//
// Router-level tool-results coverage (CHECK F1): GET
// /api/v1/sessions/{id}/tool-results/{ref} must return the stored JSON for a
// real stored ref, 404 for a missing or foreign ref, and 400 for a malformed
// ref — driven through HandleSessions, never by calling the child handler
// directly (CHECK m4 survived precisely because the older tests bypassed the
// dispatcher). GET .../tool-results without a ref is delegated to the child
// handler and rejected there (CHECK inventory row), never served as the
// session. Wrong methods on tool-results are 405 with Allow: GET like every
// other known sub-resource.
//
// After every refused request the session must still exist (GET detail 200) and
// keep its original title — the bug is that a sub-path request currently acts on
// the WHOLE session instead of being refused.

package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// sessionsSubPathRenameTitle is the title the PUT cases attempt to write. A
// refused PUT must leave the session's persisted title exactly as it was.
const sessionsSubPathRenameTitle = "1471-renamed-title"

// sessionsSubPathRequestBody is the rename body the PUT cases send.
const sessionsSubPathRequestBody = `{"title":"` + sessionsSubPathRenameTitle + `"}`

// sessionsSubPathToolResultBody is the JSON body seeded into the REAL
// tool-result store; the router must hand back exactly these bytes.
const sessionsSubPathToolResultBody = `{"output":"1471-stored-tool-result"}`

// sessionsSubPathAllowGet is the one permitted method both defined
// sub-resources advertise on a 405 (contract: getSessionMessages and
// getToolResult are GET-only; CHECK-agreed rule).
const sessionsSubPathAllowGet = "GET"

// subPathDo sends one REST request through HandleSessions and returns the full
// recorder, so assertions can observe the status, the HEADERS (the required
// Allow header on a 405 — CHECK F2) and the body.
func subPathDo(env *u1Env, method, path, body string) *httptest.ResponseRecorder {
	var rd io.Reader = bytes.NewReader(nil)
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rd)
	r.URL.Path = path
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	env.api.HandleSessions(w, r)
	return w
}

// subPathNewChat mints one fresh chat (real store) and returns its id and
// original title. One session per case keeps survival checks independent: a
// bug that deletes the session must only ever fail its own case.
func subPathNewChat(t *testing.T, env *u1Env) (string, string) {
	t.Helper()
	meta, err := env.store(t).NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	original, err := env.store(t).GetMeta(meta.ID)
	require.NoError(t, err)
	return meta.ID, original.Title
}

// subPathAssertSessionIntact asserts the survival invariant through the
// user-facing door (GET detail is 200) AND the persisted record (title
// unchanged).
func subPathAssertSessionIntact(t *testing.T, env *u1Env, id, originalTitle string) {
	t.Helper()
	rec := subPathDo(env, http.MethodGet, "/api/v1/sessions/"+id, "")
	assert.Equal(t, http.StatusOK, rec.Code,
		"session must still exist after the refused request; GET detail said %d: %s", rec.Code, rec.Body.String())
	meta, err := env.store(t).GetMeta(id)
	require.NoError(t, err, "session meta must stay readable after the refused request")
	assert.Equal(t, originalTitle, meta.Title,
		"session title must be unchanged after the refused request")
}

func TestSessionsSubPath(t *testing.T) {
	env := u1NewEnv(t, false)

	cases := []struct {
		name     string
		method   string
		subPath  string
		body     string
		expected int
		allow    string // exact Allow header required on a 405 (CHECK F2); "" = no assertion
		refused  bool   // session must still exist, unrenamed, afterwards
	}{
		// --- Controls: the documented behaviour that must keep working. ---
		{
			name: "GET bare session returns detail", method: http.MethodGet,
			subPath: "", expected: http.StatusOK,
		},
		{
			name: "GET messages sub-resource returns the history page", method: http.MethodGet,
			subPath: "messages", expected: http.StatusOK,
		},
		{
			name: "PUT bare session renames it", method: http.MethodPut,
			subPath: "", body: sessionsSubPathRequestBody, expected: http.StatusOK,
		},
		{
			name: "DELETE bare session deletes it", method: http.MethodDelete,
			subPath: "", expected: http.StatusOK,
		},
		// --- Refused cases: issue #1471 + CHECK F2 (Allow header). ---
		{
			name: "PUT messages sub-path does not rename the session", method: http.MethodPut,
			subPath: "messages", body: sessionsSubPathRequestBody,
			expected: http.StatusMethodNotAllowed, allow: sessionsSubPathAllowGet, refused: true,
		},
		{
			name: "PUT per-message address does not rename the session", method: http.MethodPut,
			subPath: "messages/0", body: sessionsSubPathRequestBody,
			expected: http.StatusMethodNotAllowed, allow: sessionsSubPathAllowGet, refused: true,
		},
		{
			name: "PUT unknown sub-path returns not found", method: http.MethodPut,
			subPath: "unknown", body: sessionsSubPathRequestBody,
			expected: http.StatusNotFound, refused: true,
		},
		{
			name: "DELETE messages sub-path does not delete the session", method: http.MethodDelete,
			subPath:  "messages",
			expected: http.StatusMethodNotAllowed, allow: sessionsSubPathAllowGet, refused: true,
		},
		{
			name: "DELETE per-message address does not delete the session", method: http.MethodDelete,
			subPath:  "messages/0",
			expected: http.StatusMethodNotAllowed, allow: sessionsSubPathAllowGet, refused: true,
		},
		{
			name: "DELETE unknown sub-path returns not found", method: http.MethodDelete,
			subPath:  "unknown",
			expected: http.StatusNotFound, refused: true,
		},
		{
			name: "GET per-message address without the D2 route returns not found", method: http.MethodGet,
			subPath:  "messages/0",
			expected: http.StatusNotFound, refused: true,
		},
		{
			name: "GET unknown sub-path returns not found", method: http.MethodGet,
			subPath:  "unknown",
			expected: http.StatusNotFound, refused: true,
		},
		// --- The other defined sub-resource: tool-results/{ref} (CHECK F1/F2). ---
		{
			// The ref is a syntactically valid placeholder: the method table must
			// refuse the request before any ref is read.
			name: "PUT tool-results ref does not rename the session", method: http.MethodPut,
			subPath: "tool-results/reffor405", body: sessionsSubPathRequestBody,
			expected: http.StatusMethodNotAllowed, allow: sessionsSubPathAllowGet, refused: true,
		},
		{
			name: "DELETE tool-results ref does not delete the session", method: http.MethodDelete,
			subPath:  "tool-results/reffor405",
			expected: http.StatusMethodNotAllowed, allow: sessionsSubPathAllowGet, refused: true,
		},
		{
			// CHECK inventory row with no router-level control: the ref-less
			// address is still delegated to the child handler and rejected there
			// (its documented 400-invalid-input answer) — never served as the
			// session and never a silent fall-through.
			name: "GET tool-results without a ref is rejected by the child handler", method: http.MethodGet,
			subPath:  "tool-results",
			expected: http.StatusBadRequest, refused: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, originalTitle := subPathNewChat(t, env)
			path := "/api/v1/sessions/" + id
			if tc.subPath != "" {
				path += "/" + tc.subPath
			}

			rec := subPathDo(env, tc.method, path, tc.body)
			assert.Equal(t, tc.expected, rec.Code,
				"%s %s: body=%s", tc.method, path, rec.Body.String())
			if tc.allow != "" {
				assert.Equal(t, tc.allow, rec.Header().Get("Allow"),
					"405 must advertise the sub-resource's permitted method (%s %s): body=%s",
					tc.method, path, rec.Body.String())
			}

			switch {
			case tc.refused:
				subPathAssertSessionIntact(t, env, id, originalTitle)
			case tc.method == http.MethodPut:
				// Positive control: the rename actually happened, on the wire
				// and in the store.
				var renamed generated.Session
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &renamed), "body=%s", rec.Body.String())
				assert.Equal(t, sessionsSubPathRenameTitle, renamed.Title, "renamed title on the wire")
				meta, err := env.store(t).GetMeta(id)
				require.NoError(t, err)
				assert.Equal(t, sessionsSubPathRenameTitle, meta.Title, "renamed title persisted")
			case tc.method == http.MethodDelete:
				// Positive control: the session is really gone.
				after := subPathDo(env, http.MethodGet, "/api/v1/sessions/"+id, "")
				assert.Equal(t, http.StatusNotFound, after.Code,
					"deleted session must no longer resolve; GET detail said %d: %s", after.Code, after.Body.String())
			case tc.method == http.MethodGet && tc.subPath == "messages":
				// Positive control: the history page names this session.
				var page generated.SessionHistoryResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page), "body=%s", rec.Body.String())
				assert.Equal(t, id, page.SessionId, "history page envelope names the addressed session")
			}
		})
	}
}

// TestSessionsSubPathToolResults is the router-level control for the existing
// tool-results sub-route (CHECK F1): every request goes through
// HandleSessions, so a dispatcher that breaks or drops the tool-results
// delegation fails these cases even though the child handler itself is fine.
// Storage is real — results are seeded with the same store the handler reads.
func TestSessionsSubPathToolResults(t *testing.T) {
	env := u1NewEnv(t, false)
	id, originalTitle := subPathNewChat(t, env)

	store := newToolResultStore(env.home)
	storedRef, ok := store.saveJSON(id, []byte(sessionsSubPathToolResultBody))
	require.True(t, ok, "SETUP: seeding the real tool-result store must succeed")

	t.Run("GET stored ref returns the stored JSON through the router", func(t *testing.T) {
		rec := subPathDo(env, http.MethodGet, "/api/v1/sessions/"+id+"/tool-results/"+storedRef, "")
		assert.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"),
			"the stored result is JSON; body=%s", rec.Body.String())
		assert.Equal(t, sessionsSubPathToolResultBody, rec.Body.String(),
			"the router must return exactly the seeded bytes for the addressed ref")
		subPathAssertSessionIntact(t, env, id, originalTitle)
	})

	t.Run("GET missing ref returns not found", func(t *testing.T) {
		missingRef := strings.Repeat("b", 32) // well-formed hex shape, nothing stored there
		rec := subPathDo(env, http.MethodGet, "/api/v1/sessions/"+id+"/tool-results/"+missingRef, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
		subPathAssertSessionIntact(t, env, id, originalTitle)
	})

	t.Run("GET foreign ref returns not found", func(t *testing.T) {
		foreignRef, ok := store.saveJSON("session-foreign-1471", []byte(sessionsSubPathToolResultBody))
		require.True(t, ok, "SETUP: seeding the foreign session's result must succeed")
		rec := subPathDo(env, http.MethodGet, "/api/v1/sessions/"+id+"/tool-results/"+foreignRef, "")
		assert.Equal(t, http.StatusNotFound, rec.Code,
			"a ref stored under another session must not resolve here; body=%s", rec.Body.String())
		subPathAssertSessionIntact(t, env, id, originalTitle)
	})

	t.Run("GET malformed ref returns bad request", func(t *testing.T) {
		rec := subPathDo(env, http.MethodGet, "/api/v1/sessions/"+id+"/tool-results/bad/ref", "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
		subPathAssertSessionIntact(t, env, id, originalTitle)
	})
}
