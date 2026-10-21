// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// sf1Do sends one REST request through HandleSessions and returns status+body.
func sf1Do(env *u1Env, method, path, body string) (int, string) {
	var rd *bytes.Reader
	if body != "" {
		rd = bytes.NewReader([]byte(body))
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	r.URL.Path = path
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	env.api.HandleSessions(w, r)
	return w.Code, w.Body.String()
}

// sf1Block makes path unreadable/unwritable for the test and returns the restore.
// sf1Block makes path unusable for EVERY caller — see
// blockPathForEveryCaller for why a permission bit does not qualify. The
// returned func restores the original bytes/contents early; t.Cleanup does the
// same at test end, and both are safe to call twice.
// sf1BlockParent blocks the DIRECTORY that holds path instead of path itself.
// It is needed where the reader tolerates a directory where a file is expected:
// the transcript reader lists the session directory and SKIPS directory
// entries, so replacing transcript.jsonl with a directory yields an empty,
// error-free read. Making the session directory a regular file raises ENOTDIR
// for every caller — root and Windows included — which is the read fault this
// case needs. (That the transcript reader silently treats a directory at
// transcript.jsonl as an empty transcript is reported as a product finding.)
func sf1BlockParent(t *testing.T, path string) func() {
	t.Helper()
	return blockPathForEveryCaller(t, filepath.Dir(path))
}

func sf1Block(t *testing.T, path string) func() {
	t.Helper()
	return blockPathForEveryCaller(t, path)
}

func sf1AssertFixedStorageMessage(t *testing.T, env *u1Env, code int, body, op string, forbidden ...string) {
	t.Helper()
	assert.Equal(t, http.StatusInternalServerError, code, body)
	assert.Contains(t, body, "could not be "+op+" because session storage failed", body)
	assert.Contains(t, body, "retry")
	sf1AssertNoInternalDetail(t, env, body, forbidden...)
}

func sf1NewChat(t *testing.T, env *u1Env) string {
	t.Helper()
	meta, err := env.store(t).NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, env.store(t).AppendTranscript(meta.ID, session.TranscriptEntry{
		ID: "leak-e1", Role: "user", Content: "hi", AgentID: "mia", Timestamp: time.Now().UTC(),
	}))
	return meta.ID
}

// D7a/D10 and the explicit Q2 migration: authorized metadata remains usable
// when history fails. The error text never reveals paths/identity; the caller's
// own contracted session/agent identity is legitimate metadata, not a leak.
// Security-lead must independently confirm this migrated security oracle.
func TestSF1_UnreadableTranscriptOfAValidMainLeaksNothing(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JSF1WS0000000000000000H"
	env.seedWorkspace(t, ws, false, "mia")
	id := u1MainID(ws, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
	require.Equal(t, http.StatusOK, code, "precondition: the main opens")
	require.NoError(t, env.store(t).AppendTranscript(id, session.TranscriptEntry{
		ID: "leak-m1", Role: "user", Content: "hello", AgentID: "mia", Timestamp: time.Now().UTC(),
	}))
	sf1BlockParent(t, filepath.Join(env.store(t).BaseDir(), id, "transcript.jsonl"))

	code, body := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
	require.Equal(t, http.StatusOK, code, "authorized metadata is independent of unreadable history: %s", body)
	var detail generated.SessionDetail
	require.NoError(t, json.Unmarshal([]byte(body), &detail))
	require.Equal(t, id, detail.Session.Id)
	require.Equal(t, "mia", detail.Session.AgentId)
	require.Equal(t, "unavailable", detail.HistoryState.Status)
	require.NotNil(t, detail.HistoryState.Reason)
	require.Equal(t, "storage_error", *detail.HistoryState.Reason)
	require.NotNil(t, detail.HistoryState.Message)
	require.Equal(t, "This session could not be read because session storage failed. Check disk space and permissions, then retry. Details are in the server log.", *detail.HistoryState.Message)
	// All original identity/path bans still apply to the sanitized error text.
	sf1AssertNoInternalDetail(t, env, *detail.HistoryState.Message, ws, "mia", "transcript")
	// Security-approved exact-node identity exception; every other field keeps
	// the legacy identity/transcript bans, and original whole-body paths/cause
	// bans are never masked. NewMain + this sole mia append has participant mia.
	sf1AssertAuthorizedIdentityPolicy(t, env, body, sf1IdentityFixture{sessionID: id, agentID: "mia", workspaceID: ws, agentIDs: []string{"mia"}})
	for _, value := range []any{detail.Messages, detail.PageInfo, detail.HasEarlier, detail.HasLater, detail.NextBefore, detail.NextAfter} {
		require.Nil(t, value, "unavailable detail must not fabricate history/page/continuation fields")
	}
	message, serverError := validateBodyAgainstSchema("SessionDetail", []byte(body))
	require.False(t, serverError)
	require.Empty(t, message)
	code, body = sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id+"/messages", "")
	sf1AssertFixedStorageMessage(t, env, code, body, "read", ws, "mia", "transcript")
	require.JSONEq(t, `{"error":"This session could not be read because session storage failed. Check disk space and permissions, then retry. Details are in the server log."}`, body, "exact fixed refusal, never a permitted prefix followed by raw OS error text")
	sf1AssertNoPhysicalStorageDetail(t, env, body)
}

func sf1AssertNoPhysicalStorageDetail(t *testing.T, env *u1Env, body string) {
	t.Helper()
	require.Empty(t, sf1PrivacyFragmentsFound(t, []byte(body), sf1PhysicalFragments(t, env)), "original whole response must hide physical storage/cause data in both serialized and decoded strings/keys")
}

func TestSF1_UnauthorizedUnreadableHistoryNeverReturnsMetadata(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JSF1AUTH000000000000000H"
	env.seedWorkspace(t, ws, false, "mia")
	id := u1MainID(ws, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
	require.Equal(t, http.StatusOK, code)
	require.NoError(t, env.store(t).AppendTranscript(id, session.TranscriptEntry{ID: "private-auth-row", Role: "user", Content: "PRIVATE_AUTH_HISTORY", AgentID: "mia", Timestamp: time.Now().UTC()}))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "sf1-auth-secret")
	// Positive control: the same real authentication wrapper reaches metadata
	// for the authorized caller; an always-refusing wrapper cannot pass this test.
	allowed := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+id, nil)
	allowed.Header.Set("Authorization", "Bearer sf1-auth-secret")
	control := httptest.NewRecorder()
	env.api.withAuth(env.api.HandleSessions)(control, allowed)
	require.Equal(t, http.StatusOK, control.Code)
	historyMigrationDetail(t, id, control.Body.Bytes(), false)
	sf1BlockParent(t, filepath.Join(env.store(t).BaseDir(), id, "transcript.jsonl"))
	for _, endpoint := range []string{"/api/v1/sessions/" + id, "/api/v1/sessions/" + id + "/messages"} {
		for _, tc := range []struct{ header, message string }{
			{"", "unauthorized: missing Bearer token"}, {"Bearer wrong", "unauthorized: invalid Bearer token"},
		} {
			request := httptest.NewRequest(http.MethodGet, endpoint, nil)
			request.Header.Set("Authorization", tc.header)
			response := httptest.NewRecorder()
			env.api.withAuth(env.api.HandleSessions)(response, request)
			pagingRefusal(t, response, http.StatusUnauthorized, tc.message)
			sf1AssertNoInternalDetail(t, env, response.Body.String(), id, ws, "mia", "transcript", "PRIVATE_AUTH_HISTORY")
			require.NotContains(t, response.Body.String(), "history_state", "no unavailable-history authorization bypass")
		}
	}
}

// A session whose metadata is unreadable (not merely absent) is a storage
// failure with the fixed message; an absent session stays a plain 404.
func TestSF1_UnreadableMetadataLeaksNothingAndAbsentStays404(t *testing.T) {
	env := u1NewEnv(t, false)
	// A session folder with damaged metadata that was never opened (so no cache
	// can answer for it): the read fails with a parse error, not "absent".
	const id = "session_leakdamagedmeta0001"
	dir := filepath.Join(env.store(t).BaseDir(), id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{not json"), 0o600))

	code, body := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
	sf1AssertFixedStorageMessage(t, env, code, body, "read", id)

	code, body = sf1Do(env, http.MethodGet, "/api/v1/sessions/no-such-session-xyz", "")
	assert.Equal(t, http.StatusNotFound, code)
	assert.JSONEq(t, `{"error":"session not found"}`, body)
	sf1AssertNoInternalDetail(t, env, body)
}

func TestSF1_RenameStorageFailureLeaksNothing(t *testing.T) {
	env := u1NewEnv(t, false)
	id := sf1NewChat(t, env)
	sf1Block(t, filepath.Join(env.store(t).BaseDir(), id))
	code, body := sf1Do(env, http.MethodPut, "/api/v1/sessions/"+id, `{"title":"new title"}`)
	sf1AssertFixedStorageMessage(t, env, code, body, "renamed", id)
}

func TestSF1_DeleteStorageFailureLeaksNothing(t *testing.T) {
	env := u1NewEnv(t, false)
	id := sf1NewChat(t, env)
	sf1Block(t, env.store(t).BaseDir())
	code, body := sf1Do(env, http.MethodDelete, "/api/v1/sessions/"+id, "")
	sf1AssertFixedStorageMessage(t, env, code, body, "deleted", id)
}

func TestSF1_CreateStorageFailureLeaksNothing(t *testing.T) {
	env := u1NewEnv(t, false)
	sf1Block(t, env.store(t).BaseDir())
	code, body := sf1Do(env, http.MethodPost, "/api/v1/sessions", `{"type":"chat","agent_id":"mia"}`)
	sf1AssertFixedStorageMessage(t, env, code, body, "created")
}

// Deleting a session whose metadata is readable but whose lifecycle journal is
// not: the Stop that precedes the delete fails. The refusal stays (nothing is
// deleted) and the client gets the fixed message only.
func TestSF1_DeleteWithUnreadableLifecycleJournalLeaksNothingAndRefuses(t *testing.T) {
	env := u1NewEnv(t, false)
	lc := session.NewLifecycleStore(t.TempDir())
	env.api.agentLoop.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lc)
	id := sf1NewChat(t, env)
	require.NoError(t, lc.Persist(&session.LifecycleRecord{
		SessionID: id, Generation: 1, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: "ws-leak", AgentID: "mia",
	}))
	sf1Block(t, filepath.Join(lc.Dir(), id+".jsonl"))

	code, body := sf1Do(env, http.MethodDelete, "/api/v1/sessions/"+id, "")
	assert.Equal(t, http.StatusInternalServerError, code, body)
	assert.Contains(t, body, "Stop before delete failed; nothing was deleted")
	assert.Contains(t, body, "retry")
	sf1AssertNoInternalDetail(t, env, body, id, lc.Dir())
	_, err := os.Stat(filepath.Join(env.store(t).BaseDir(), id))
	assert.NoError(t, err, "the delete refusal is kept: the session folder is still there")
}

// Both failure shapes of the Stop (a returned error and a root error) get the
// same fixed message, never the raw text.
func TestSF1_StopBeforeDeleteFailureShapesNeverEchoTheCause(t *testing.T) {
	const secret = "/secret/place/session_x.jsonl: permission denied"
	cases := map[string]func(context.Context, agent.StopRequest) (agent.StopResult, error){
		"error": func(context.Context, agent.StopRequest) (agent.StopResult, error) {
			return agent.StopResult{}, errors.New(secret)
		},
		"root error": func(context.Context, agent.StopRequest) (agent.StopResult, error) {
			return agent.StopResult{RootErr: errors.New(secret)}, nil
		},
	}
	for name, stop := range cases {
		t.Run(name, func(t *testing.T) {
			env := u1NewEnv(t, false)
			env.api.stopSession = stop
			id := sf1NewChat(t, env)
			code, body := sf1Do(env, http.MethodDelete, "/api/v1/sessions/"+id, "")
			assert.Equal(t, http.StatusInternalServerError, code, body)
			assert.Contains(t, body, "Stop before delete failed; nothing was deleted")
			assert.NotContains(t, body, "secret")
			assert.NotContains(t, body, "permission denied")
			sf1AssertNoInternalDetail(t, env, body, id)
			_, err := os.Stat(filepath.Join(env.store(t).BaseDir(), id))
			assert.NoError(t, err, "nothing was deleted")
		})
	}
}

// Round 5 sibling handlers: GET /agents/{id}/sessions and DELETE /sessions/all.
func TestSF1_ListAgentSessionsStorageFailureLeaksNothing(t *testing.T) {
	env := u1NewEnv(t, false)
	sf1NewChat(t, env)
	sf1Block(t, env.store(t).BaseDir())
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/mia/sessions", nil)
	r.URL.Path = "/api/v1/agents/mia/sessions"
	env.api.HandleAgents(w, r)
	assert.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "could not list sessions")
	assert.Contains(t, w.Body.String(), "retry")
	sf1AssertNoInternalDetail(t, env, w.Body.String(), "unified_store", "permission denied", "mia")
}

// The clear-all warning is fixed text per failed agent. (A real per-agent
// legacy store failing cannot be produced with this fixture - its agents use the
// shared store - so the warning text itself is what is pinned.)
func TestSF1_ClearAllSessionsWarningIsFixedText(t *testing.T) {
	env := u1NewEnv(t, false)
	got := clearSessionsWarning("mia")
	assert.Contains(t, got, "mia")
	assert.Contains(t, got, "see the server log")
	sf1AssertNoInternalDetail(t, env, got, "permission denied", "unified_store", "remove session dir")
}

// jsonServerFailure never echoes its cause.
func TestJSONServerFailureNeverEchoesTheCause(t *testing.T) {
	env := u1NewEnv(t, false)
	w := httptest.NewRecorder()
	jsonServerFailure(w, http.StatusInternalServerError, "could not save config",
		errors.New(env.home+"/config.json: permission denied"))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "could not save config")
	sf1AssertNoInternalDetail(t, env, w.Body.String(), "config.json", "permission denied")
}
