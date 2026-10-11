// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Final-gate S2 gateway cluster (S2-CONSOLIDATED.md). One test per finding, each
// drives the real handler.

// H1 (finding 1): DELETE /agents/{id} with a stale revision must refuse BEFORE
// any destructive step. Before the fix the cleanup cascade deleted the agent's
// sessions and unassigned its tasks, and only then did DeleteState compare
// revisions and answer 409 - the agent survived with its data gone.
func TestGW_H1_StaleRevisionAgentDeleteDestroysNothing(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.agentLoop.SetReloadFunc(func() error {
		defer api.agentLoop.ClearReloadPending()
		return api.refreshConfigAndRewireServices(api.configPath())
	})
	createW := httptest.NewRecorder()
	createR := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(
		`{"name": "Stale Revision", "type": "Main", "model": "claude-sonnet-4-6", "soul": "I must survive."}`))
	createR.Header.Set("Content-Type", "application/json")
	createR.URL.Path = "/api/v1/agents"
	api.HandleAgents(createW, createR)
	require.Equal(t, http.StatusCreated, createW.Code, createW.Body.String())
	var created gen.Agent
	require.NoError(t, json.Unmarshal(createW.Body.Bytes(), &created))

	sessStore, err := session.NewUnifiedStore(filepath.Join(api.homePath, "sessions"))
	require.NoError(t, err)
	meta, err := sessStore.NewSession(session.SessionTypeChat, "webchat", created.Id)
	require.NoError(t, err)
	require.NoError(t, sessStore.Close())

	stale := strings.Repeat("0", 64)
	require.NotEqual(t, stale, created.Revision, "instrument check: the revision must really be stale")
	delW := httptest.NewRecorder()
	delR := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+created.Id+"?revision="+stale, nil)
	delR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(delW, delR)

	assert.Equal(t, http.StatusConflict, delW.Code, "a stale revision is still a conflict: %s", delW.Body.String())
	_, statErr := os.Stat(filepath.Join(api.homePath, "sessions", meta.ID))
	assert.False(t, errors.Is(statErr, os.ErrNotExist), "the agent's session must NOT be deleted by a refused delete")
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.Id, nil)
	getR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(getW, getR)
	assert.Equal(t, http.StatusOK, getW.Code, "the agent must still be there")

	// Control: the current revision still deletes, and then cleans the session.
	okW := httptest.NewRecorder()
	okR := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+created.Id+"?revision="+created.Revision, nil)
	okR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(okW, okR)
	assert.Equal(t, http.StatusOK, okW.Code, okW.Body.String())
	_, statErr = os.Stat(filepath.Join(api.homePath, "sessions", meta.ID))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "a valid delete still cleans the session")
}

// H2 (finding 2): the gateway half of the provisioning hook. A member added to a
// team by a path that did not run the workspace-save provisioning (the Admin
// create_agent tool) has no main; ensureWorkspaceMains is the hook that gives
// every eligible member one, through the same ensureMainsForTeam a save uses.
func TestGW_H2_EnsureWorkspaceMainsProvisionsAnUnprovisionedMember(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JGWGATEH2WS0000000000001"
	env.seedWorkspace(t, ws, false, "mia")
	id := u1MainID(ws, "mia")
	_, err := env.store(t).GetMeta(id)
	require.Error(t, err, "instrument check: the member starts with no main")

	require.NoError(t, env.api.ensureWorkspaceMains(ws))

	meta, err := env.store(t).GetMeta(id)
	require.NoError(t, err, "the member's main must exist after the hook")
	assert.Equal(t, "mia", meta.AgentID)
	assert.Equal(t, ws, meta.WorkspaceID)
	// An unknown workspace is a visible error, never a silent no-op.
	assert.Error(t, env.api.ensureWorkspaceMains("01JGWGATEH2NOSUCHWS00000001"))
}

// M4 (finding 4): after a member is removed from a team its main is hidden in
// lists, and must not stay reachable through the other doors either. Each door
// is driven through its real handler after the removal.
func m4Setup(t *testing.T) (env *u1Env, ws, mainID string) {
	t.Helper()
	env = u1NewEnv(t, false)
	ws = "01JGWGATEM4WS0000000000001"
	env.seedWorkspace(t, ws, false, "mia")
	mainID = u1MainID(ws, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+mainID, "")
	require.Equal(t, http.StatusOK, code, "precondition: the member's main opens while eligible")
	require.NoError(t, env.store(t).AppendTranscript(mainID, session.TranscriptEntry{
		ID: "m4-e1", Role: "user", Content: "hello", AgentID: "mia", Timestamp: time.Now().UTC(),
	}))
	env.seedWorkspace(t, ws, false) // the member is removed: empty core_team
	return env, ws, mainID
}

func TestGW_M4_RemovedMembersMainIsNotServedByMessages(t *testing.T) {
	env, _, mainID := m4Setup(t)
	code, body := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+mainID+"/messages", "")
	assert.Equal(t, http.StatusNotFound, code, "a hidden main is not readable: %s", body)
	assert.NotContains(t, body, "hello")
}

func TestGW_M4_RemovedMembersMainIsNotRenamable(t *testing.T) {
	env, _, mainID := m4Setup(t)
	code, body := sf1Do(env, http.MethodPut, "/api/v1/sessions/"+mainID, `{"title":"hijack"}`)
	assert.Equal(t, http.StatusNotFound, code, body)
}

func TestGW_M4_RemovedMembersMainIsNotListedUnderTheAgent(t *testing.T) {
	env, _, mainID := m4Setup(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/mia/sessions", nil)
	r.URL.Path = "/api/v1/agents/mia/sessions"
	env.api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), mainID, "the hidden main must not be listed under the agent")
}

func TestGW_M4_RemovedMembersMainRefusesWebSocketAttachAndSend(t *testing.T) {
	env, _, mainID := m4Setup(t)
	h := newWSHandler(bus.NewMessageBus(), env.api.agentLoop, "")
	t.Cleanup(h.Wait)

	wc := makeTestConn()
	h.handleAttachSession(context.Background(), "chat-m4", mainID, nil, wc)
	frames := drainConnFrames(wc)
	assert.Contains(t, frames, `"type":"error"`, "attach to a hidden main is refused: %s", frames)
	assert.NotContains(t, frames, "catch_up_complete")
	assert.NotContains(t, frames, "hello", "no transcript may be replayed")

	wc2 := makeTestConn()
	h.handleChatMessage(context.Background(), "chat-m4", mainID, "still here?", "mia", nil, "", "", false, wc2)
	sent := drainConnFrames(wc2)
	assert.Contains(t, sent, `"type":"error"`, "a send into a hidden main is refused: %s", sent)
	entries, err := env.store(t).ReadTranscript(mainID)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotEqual(t, "still here?", e.Content, "the refused message must not be saved")
	}
}

// Control: while the member is on the team, the same doors work.
func TestGW_M4_EligibleMainStillServed(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JGWGATEM4WS0000000000002"
	env.seedWorkspace(t, ws, false, "mia")
	id := u1MainID(ws, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
	require.Equal(t, http.StatusOK, code)
	code, body := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id+"/messages", "")
	assert.Equal(t, http.StatusOK, code, body)
}
