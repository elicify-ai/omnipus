// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const (
	deltaDefWS   = "01JDELTAGWDEFAULTWS00000001"
	deltaOtherWS = "01JDELTAGWOTHERWS000000002"
)

// deltaAdminChat creates an Admin chat in the default workspace and returns its id.
func deltaAdminChat(t *testing.T, env *u1Env) string {
	t.Helper()
	code, body := sf1Do(env, http.MethodPost, "/api/v1/sessions",
		`{"type":"chat","agent_id":"admin","workspace_id":"`+deltaDefWS+`"}`)
	require.Equal(t, http.StatusCreated, code, body)
	var s gen.Session
	require.NoError(t, json.Unmarshal([]byte(body), &s))
	return s.Id
}

// Finding 1: an existing Admin chat must not be re-bound to another workspace by
// a later frame's workspace_id (navigation FR-038).
func TestDeltaGW_AdminChatCannotBeRebound_ByAFrameOnAnExistingSession(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, deltaDefWS, true, "mia")
	env.seedWorkspace(t, deltaOtherWS, false, "mia")
	sid := deltaAdminChat(t, env)
	h := newWSHandler(bus.NewMessageBus(), env.api.agentLoop, "")
	t.Cleanup(h.Wait)

	wc := makeTestConn()
	h.handleChatMessage(context.Background(), "chat-delta", sid, "move me", "admin", nil, "", deltaOtherWS, false, wc)

	frames := drainConnFrames(wc)
	assert.Contains(t, frames, `"type":"error"`, frames)
	assert.Contains(t, frames, "default workspace", frames)
	meta, err := env.store(t).GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, deltaDefWS, meta.WorkspaceID, "the Admin chat must stay in the default workspace")
}

// Positive control for finding 1: the same frame carrying the default workspace is
// not refused and leaves the binding alone.
func TestDeltaGW_AdminChatInTheDefaultWorkspace_IsNotRefusedOnAFrame(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, deltaDefWS, true, "mia")
	sid := deltaAdminChat(t, env)
	h := newWSHandler(bus.NewMessageBus(), env.api.agentLoop, "")
	t.Cleanup(h.Wait)

	wc := makeTestConn()
	h.handleChatMessage(context.Background(), "chat-delta", sid, "hello", "admin", nil, "", deltaDefWS, false, wc)

	frames := drainConnFrames(wc)
	assert.NotContains(t, frames, "default workspace", frames)
	meta, err := env.store(t).GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, deltaDefWS, meta.WorkspaceID)
}

// breakWorkspacesDir replaces <home>/workspaces with a regular file so every read of
// it fails with a non-"not exist" error (a server fault, not "no default").
func breakWorkspacesDir(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))
}

// Finding 4: an unreadable workspaces directory is a server fault (500, fixed
// text), not the 400 Admin refusal; and the control (no workspaces yet) stays 400.
func TestDeltaGW_AdminRefusal_ServerFaultIsA500NotA400(t *testing.T) {
	env := u1NewEnv(t, false)
	breakWorkspacesDir(t, env.home)

	code, body := sf1Do(env, http.MethodPost, "/api/v1/sessions",
		`{"type":"chat","agent_id":"admin","workspace_id":"`+deltaDefWS+`"}`)

	assert.Equal(t, http.StatusInternalServerError, code, body)
	assert.Contains(t, body, "server storage or configuration failed")
	assert.NotContains(t, body, "default workspace", "must not read as the user-facing refusal")
	r5AssertClean(t, body, env.home)
}

func TestDeltaGW_AdminRefusal_ServerFaultOverWebSocketIsTheFixedText(t *testing.T) {
	env := u1NewEnv(t, false)
	breakWorkspacesDir(t, env.home)
	h := newWSHandler(bus.NewMessageBus(), env.api.agentLoop, "")
	t.Cleanup(h.Wait)

	wc := makeTestConn()
	h.handleChatMessage(context.Background(), "chat-delta", "", "hello", "admin", nil, "", deltaDefWS, false, wc)

	frames := drainConnFrames(wc)
	assert.Contains(t, frames, "server storage or configuration failed", frames)
	assert.NotContains(t, frames, "default workspace", frames)
	r5AssertClean(t, frames, env.home)
}

func TestDeltaGW_AdminRefusal_NoDefaultWorkspaceYetIsStillThe400Refusal(t *testing.T) {
	env := u1NewEnv(t, false)
	code, body := sf1Do(env, http.MethodPost, "/api/v1/sessions", `{"type":"chat","agent_id":"admin"}`)
	assert.Equal(t, http.StatusBadRequest, code, body)
	assert.Contains(t, body, "default workspace")
}

// deltaCorruptSession writes a session directory whose meta.json cannot be parsed.
func deltaCorruptSession(t *testing.T, env *u1Env, id string) {
	t.Helper()
	dir := filepath.Join(env.store(t).BaseDir(), id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{not json"), 0o600))
}

// Finding 2: delete-all must report a chat it could not even read, never say
// "cleared" while leaving it behind unmentioned.
func TestDeltaGW_ClearAll_ReportsChatsWithUnreadableMetadata(t *testing.T) {
	env := u1NewEnv(t, false)
	good := sf1NewChat(t, env)
	deltaCorruptSession(t, env, "corrupt-chat-0001")

	code, body := clearAllDo(env)

	require.Equal(t, http.StatusOK, code, body)
	var resp gen.ClearAllSessionsResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Equal(t, 1, resp.Count, "only the readable chat was removed")
	_, err := env.store(t).GetMeta(good)
	assert.Error(t, err)
	require.NotNil(t, resp.Warnings, "the unreadable chat must be reported")
	assert.Contains(t, strings.Join(*resp.Warnings, " "), "1 chat(s)")
	assert.NotContains(t, body, "corrupt-chat-0001", "no session id in the client text")
	_, statErr := os.Stat(filepath.Join(env.store(t).BaseDir(), "corrupt-chat-0001"))
	assert.NoError(t, statErr, "instrument check: the unreadable chat is still on disk")
}

// Control for finding 2: with nothing unreadable there are no warnings.
func TestDeltaGW_ClearAll_NoWarningsWhenEverythingIsReadable(t *testing.T) {
	env := u1NewEnv(t, false)
	sf1NewChat(t, env)
	code, body := clearAllDo(env)
	require.Equal(t, http.StatusOK, code, body)
	var resp gen.ClearAllSessionsResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Nil(t, resp.Warnings)
}

// Finding 9: kept main chats are counted in the response, not silently skipped.
func TestDeltaGW_ClearAll_CountsTheMainChatsItKeeps(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, deltaDefWS, true, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+u1MainID(deltaDefWS, "mia"), "")
	require.Equal(t, http.StatusOK, code)

	code, body := clearAllDo(env)

	require.Equal(t, http.StatusOK, code, body)
	var resp gen.ClearAllSessionsResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	require.NotNil(t, resp.Warnings)
	assert.Contains(t, strings.Join(*resp.Warnings, " "), "main chat")
}

// Finding 10: a helper (delegate) session of a main that delete-all keeps is part
// of that main's history and must be kept too.
func TestDeltaGW_ClearAll_KeepsHelperSessionsOfAKeptMain(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, deltaDefWS, true, "mia")
	mainID := u1MainID(deltaDefWS, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+mainID, "")
	require.Equal(t, http.StatusOK, code)
	ls := session.NewLifecycleStore(t.TempDir())
	env.api.agentLoop.SetSessionMessagingStores(nil, ls)
	helper := u18NewChild(t, env.store(t), "delta-helper-of-main", mainID)
	u18PersistLifecycleChild(t, ls, helper.ID, mainID)
	// A finished helper: linked to its main by the session's parent link only.
	finished := u18NewChild(t, env.store(t), "delta-finished-helper", mainID)
	loose := sf1NewChat(t, env)
	listed, lerr := env.store(t).ListSessions()
	require.NoError(t, lerr)
	seen := false
	for _, m := range listed {
		seen = seen || m.ID == helper.ID
	}
	require.True(t, seen, "instrument check: the helper session is one delete-all would iterate")

	code, body := clearAllDo(env)

	require.Equal(t, http.StatusOK, code, body)
	_, err := env.store(t).GetMeta(helper.ID)
	assert.NoError(t, err, "a kept main's helper session must survive delete-all")
	_, err = env.store(t).GetMeta(finished.ID)
	assert.NoError(t, err, "a kept main's finished helper session must survive delete-all")
	_, err = env.store(t).GetMeta(loose)
	assert.Error(t, err, "instrument check: an ordinary chat is still deleted")
}

// Finding 5: a workspace delete that cannot remove a main chat says so.
func TestDeltaGW_WorkspaceDelete_ReportsAMainChatItCouldNotRemove(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX directory permissions and a non-root user")
	}
	env := u1NewEnv(t, false)
	const ws = "01JDELTAGWDELETEWS0000001"
	env.seedWorkspace(t, deltaDefWS, true, "mia")
	env.seedWorkspace(t, ws, false, "mia")
	mainID := u1MainID(ws, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+mainID, "")
	require.Equal(t, http.StatusOK, code)
	base := env.store(t).BaseDir()
	require.NoError(t, os.Chmod(base, 0o500))
	t.Cleanup(func() { _ = os.Chmod(base, 0o700) })

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, env.api, ws), nil)
	env.api.handleWorkspaceDelete(w, r, ws)

	assert.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "main chat")
	r5AssertClean(t, w.Body.String(), mainID, env.home)
}

// Control for finding 5: a clean delete is still a 200.
func TestDeltaGW_WorkspaceDelete_CleanDeleteStillSucceeds(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JDELTAGWDELETEWS0000002"
	env.seedWorkspace(t, deltaDefWS, true, "mia")
	env.seedWorkspace(t, ws, false, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+u1MainID(ws, "mia"), "")
	require.Equal(t, http.StatusOK, code)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, env.api, ws), nil)
	env.api.handleWorkspaceDelete(w, r, ws)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, err := env.store(t).GetMeta(u1MainID(ws, "mia"))
	assert.Error(t, err, "the main went with the workspace")
}

// Finding 12: the default-main lookup reads every workspace file from the
// handler's own home, not a second source (the process environment).
func TestDeltaGW_OwnerHasEligibleDefaultMain_UsesTheHandlersHome(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, deltaDefWS, true, "mia")
	t.Setenv("OMNIPUS_HOME", t.TempDir()) // the environment now names a DIFFERENT home

	ok, err := env.api.ownerHasEligibleDefaultMain("mia")

	require.NoError(t, err)
	assert.True(t, ok, "mia is on the default workspace's team under the handler's own home")
}

// Finding 12 (runner half): a scheduled main-mode run resolves the default
// workspace and the owner's eligibility under the runner's OWN home.
func TestDeltaGW_ScheduledMainRun_UsesTheRunnersHome(t *testing.T) {
	runnerHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", t.TempDir()) // the environment names a DIFFERENT home
	require.NoError(t, writeWorkspaceFile(runnerHome, storedWorkspace{
		ID: "W12", Name: "W12", Status: "active", IsDefault: true, CoreTeam: []string{"mia"},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}))
	r := newRunnerOnly(t, baseConfig(), map[string]bool{"mia": true})
	r.home = runnerHome
	job := &cron.CronJob{ID: "m12", Name: "derived", AgentID: "mia", CreatedBy: "alice",
		SessionMode: cron.SessionModeMain, Payload: cron.CronPayload{Message: "do it"}}

	sid, err := r.pickSession(job, "mia")

	require.NoError(t, err)
	assert.Equal(t, "main-session-W12+mia", sid)
}
