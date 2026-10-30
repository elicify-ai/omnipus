// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, issue #1471: Sessions API — PUT/DELETE on any sub-path renames or
// deletes the whole chat.
//
// ORACLE: issue #1471 "Expected" plus contracts/openapi.yaml — /sessions/{id}
// carries GET (getSession), PUT (renameSession) and DELETE (deleteSession);
// /sessions/{id}/messages is a defined GET-only sub-resource (getSessionMessages).
// No expected status below was read off the implementation:
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
//	GET .../messages                    → 200  (control: contract-defined getSessionMessages)
//	GET .../messages/0                  → 404  (issue: "GET on an unknown sub-path
//	                                      returns 404"; no messages/{position} route
//	                                      exists on this base — when #1452 D2 lands
//	                                      this case becomes a defined GET and the
//	                                      expected value must be revisited then)
//	GET .../unknown                     → 404  (issue: "GET on an unknown sub-path returns 404")
//
// After every refused request the session must still exist (GET detail 200) and
// keep its original title — the bug is that a sub-path request currently acts on
// the WHOLE session instead of being refused.

package gateway

import (
	"encoding/json"
	"net/http"
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

func TestSessionsSubPath(t *testing.T) {
	env := u1NewEnv(t, false)

	// newChat mints one fresh chat per case so survival checks are independent:
	// a bug that deletes the session must only ever fail its own case.
	newChat := func(t *testing.T) (string, string) {
		t.Helper()
		meta, err := env.store(t).NewSession(session.SessionTypeChat, "webchat", "mia")
		require.NoError(t, err)
		original, err := env.store(t).GetMeta(meta.ID)
		require.NoError(t, err)
		return meta.ID, original.Title
	}

	// stillIntact asserts the survival invariant through the user-facing door
	// (GET detail is 200) AND the persisted record (title unchanged).
	stillIntact := func(t *testing.T, id, originalTitle string) {
		t.Helper()
		code, body := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
		assert.Equal(t, http.StatusOK, code,
			"session must still exist after the refused request; GET detail said %d: %s", code, body)
		meta, err := env.store(t).GetMeta(id)
		require.NoError(t, err, "session meta must stay readable after the refused request")
		assert.Equal(t, originalTitle, meta.Title,
			"session title must be unchanged after the refused request")
	}

	cases := []struct {
		name     string
		method   string
		subPath  string
		body     string
		expected int
		refused  bool // session must still exist, unrenamed, afterwards
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
		// --- Refused cases: issue #1471. ---
		{
			name: "PUT messages sub-path does not rename the session", method: http.MethodPut,
			subPath: "messages", body: sessionsSubPathRequestBody,
			expected: http.StatusMethodNotAllowed, refused: true,
		},
		{
			name: "PUT per-message address does not rename the session", method: http.MethodPut,
			subPath: "messages/0", body: sessionsSubPathRequestBody,
			expected: http.StatusMethodNotAllowed, refused: true,
		},
		{
			name: "PUT unknown sub-path returns not found", method: http.MethodPut,
			subPath: "unknown", body: sessionsSubPathRequestBody,
			expected: http.StatusNotFound, refused: true,
		},
		{
			name: "DELETE messages sub-path does not delete the session", method: http.MethodDelete,
			subPath:  "messages",
			expected: http.StatusMethodNotAllowed, refused: true,
		},
		{
			name: "DELETE per-message address does not delete the session", method: http.MethodDelete,
			subPath:  "messages/0",
			expected: http.StatusMethodNotAllowed, refused: true,
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, originalTitle := newChat(t)
			path := "/api/v1/sessions/" + id
			if tc.subPath != "" {
				path += "/" + tc.subPath
			}

			code, body := sf1Do(env, tc.method, path, tc.body)
			assert.Equal(t, tc.expected, code,
				"%s %s: body=%s", tc.method, path, body)

			switch {
			case tc.refused:
				stillIntact(t, id, originalTitle)
			case tc.method == http.MethodPut:
				// Positive control: the rename actually happened, on the wire
				// and in the store.
				var renamed generated.Session
				require.NoError(t, json.Unmarshal([]byte(body), &renamed), "body=%s", body)
				assert.Equal(t, sessionsSubPathRenameTitle, renamed.Title, "renamed title on the wire")
				meta, err := env.store(t).GetMeta(id)
				require.NoError(t, err)
				assert.Equal(t, sessionsSubPathRenameTitle, meta.Title, "renamed title persisted")
			case tc.method == http.MethodDelete:
				// Positive control: the session is really gone.
				code, body := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
				assert.Equal(t, http.StatusNotFound, code,
					"deleted session must no longer resolve; GET detail said %d: %s", code, body)
			case tc.method == http.MethodGet && tc.subPath == "messages":
				// Positive control: the history page names this session.
				var page generated.SessionHistoryResponse
				require.NoError(t, json.Unmarshal([]byte(body), &page), "body=%s", body)
				assert.Equal(t, id, page.SessionId, "history page envelope names the addressed session")
			}
		})
	}
}
