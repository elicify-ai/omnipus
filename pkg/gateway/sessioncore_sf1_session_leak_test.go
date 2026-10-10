// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
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
func sf1Block(t *testing.T, path string, mode os.FileMode) func() {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err, "instrument check: %s must exist before it is blocked", path)
	orig := info.Mode().Perm()
	require.NoError(t, os.Chmod(path, mode))
	restore := func() { _ = os.Chmod(path, orig) }
	t.Cleanup(restore)
	return restore
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

// A valid main whose transcript cannot be read answers with the fixed message
// on both reads of it (detail and messages) - never the raw filesystem error.
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
	sf1Block(t, filepath.Join(env.store(t).BaseDir(), id, "transcript.jsonl"), 0o000)

	code, body := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
	sf1AssertFixedStorageMessage(t, env, code, body, "read", ws, "mia", "transcript")
	code, body = sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id+"/messages", "")
	sf1AssertFixedStorageMessage(t, env, code, body, "read", ws, "mia", "transcript")
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
	sf1Block(t, filepath.Join(env.store(t).BaseDir(), id), 0o500)
	code, body := sf1Do(env, http.MethodPut, "/api/v1/sessions/"+id, `{"title":"new title"}`)
	sf1AssertFixedStorageMessage(t, env, code, body, "renamed", id)
}

func TestSF1_DeleteStorageFailureLeaksNothing(t *testing.T) {
	env := u1NewEnv(t, false)
	id := sf1NewChat(t, env)
	sf1Block(t, env.store(t).BaseDir(), 0o500)
	code, body := sf1Do(env, http.MethodDelete, "/api/v1/sessions/"+id, "")
	sf1AssertFixedStorageMessage(t, env, code, body, "deleted", id)
}

func TestSF1_CreateStorageFailureLeaksNothing(t *testing.T) {
	env := u1NewEnv(t, false)
	sf1Block(t, env.store(t).BaseDir(), 0o500)
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
	sf1Block(t, filepath.Join(lc.Dir(), id+".jsonl"), 0o000)

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
