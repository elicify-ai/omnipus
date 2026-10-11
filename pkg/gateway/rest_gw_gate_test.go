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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
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

// M5 (finding 5): a scheduled main-mode run re-checks the owner's entitlement
// when it FIRES. A derived-main user schedule whose owner has since left the team
// must refuse - not write into the hidden main nor mint one.
func TestGW_M5_ScheduledMainRunRefusesAnIneligibleOwner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	saveWS := func(team ...string) {
		require.NoError(t, writeWorkspaceFile(home, storedWorkspace{
			ID: "W5", Name: "W5", Status: "active", IsDefault: true, CoreTeam: team,
			CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
		}))
	}
	r, exec, _, _ := newRunnerHarness(t, baseConfig(), map[string]bool{"mia": true})
	job := &cron.CronJob{ID: "m5", Name: "user schedule", AgentID: "mia", CreatedBy: "alice",
		SessionMode: cron.SessionModeMain, Payload: cron.CronPayload{Message: "do it"}}
	const mainID = "main-session-W5+mia"

	saveWS() // the owner is NOT on the team
	sid, err := r.pickSession(job, "mia")
	require.Error(t, err, "an ineligible owner's main-mode run must refuse")
	assert.Empty(t, sid)
	_, getErr := exec.store.GetMeta(mainID)
	assert.Error(t, getErr, "no main may be minted for an ineligible owner")

	saveWS("mia") // control: eligible again
	sid, err = r.pickSession(job, "mia")
	require.NoError(t, err)
	assert.Equal(t, mainID, sid)
}

// M7 (finding 7): deleting a workspace removes the mains of members removed
// earlier too (their identity is retained on removal), not only today's team.
func TestGW_M7_WorkspaceDeleteReleasesMainsOfEarlierRemovedMembers(t *testing.T) {
	env := u1NewEnv(t, false, config.AgentConfig{ID: "ray"})
	const ws = "01JGWGATEM7WS0000000000001"
	env.seedWorkspace(t, ws, false, "mia", "ray")
	for _, id := range []string{u1MainID(ws, "mia"), u1MainID(ws, "ray")} {
		code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
		require.Equal(t, http.StatusOK, code, "precondition: %s opens", id)
	}
	env.seedWorkspace(t, ws, false, "mia") // ray is removed; its main is retained
	_, err := env.store(t).GetMeta(u1MainID(ws, "ray"))
	require.NoError(t, err, "instrument check: the removed member's main still exists")
	// A main of ANOTHER workspace must survive.
	const other = "01JGWGATEM7WS0000000000002"
	env.seedWorkspace(t, other, false, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+u1MainID(other, "mia"), "")
	require.Equal(t, http.StatusOK, code)

	stored, err := readWorkspaceFile(env.home, ws)
	require.NoError(t, err)
	releaseMainSessionsForWorkspace(env.api.agentLoop, stored)

	_, err = env.store(t).GetMeta(u1MainID(ws, "mia"))
	assert.Error(t, err, "the current member's main goes")
	_, err = env.store(t).GetMeta(u1MainID(ws, "ray"))
	assert.Error(t, err, "the earlier-removed member's main must go too")
	_, err = env.store(t).GetMeta(u1MainID(other, "mia"))
	assert.NoError(t, err, "another workspace's main must survive")
}

// M8 (finding 8): an unreadable workspace record is a visible, logged failure on
// the main's own lookup - not the plain 404 of a chat that does not exist.
func TestGW_M8_UnreadableWorkspaceIsAVisibleLoggedFailure(t *testing.T) {
	logBuf := captureSlogJSON(t)
	env := u1NewEnv(t, false)
	const ws = "01JGWGATEM8WS0000000000001"
	env.seedWorkspace(t, ws, false, "mia")
	id := u1MainID(ws, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
	require.Equal(t, http.StatusOK, code, "precondition")
	r5Block(t, filepath.Join(env.home, "workspaces", ws+".json"), 0o000)

	code, body := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")

	assert.Equal(t, http.StatusInternalServerError, code, "an unreadable workspace is not 'session not found': %s", body)
	assert.Contains(t, body, "main chat could not be prepared")
	rec := findAskUserGatewayLog(t, logBuf, "rest: main session could not be resolved: the workspace could not be read")
	assert.Equal(t, "ERROR", rec["level"], "an unreadable workspace is logged at error level")
	assert.NotEmpty(t, rec["error"], "the cause must be in the log")
	r5AssertClean(t, body, ws, env.home)
}

// M9 (finding 9): the accepted-answer-but-resume-failed log record carries the
// real cause for the operator; the client frame stays fixed text.
func TestGW_M9_AskUserResumeFailureLogCarriesTheCause(t *testing.T) {
	logBuf := captureSlogJSON(t)
	reg := askuser.NewRegistry(nil, failingGateDispatcher{err: errors.New("publish to the bus failed: queue wedged")}, askuser.Options{})
	require.NoError(t, reg.CreatePending(askSetFixture()))
	t.Cleanup(reg.Quiesce)
	var frame generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"ask_user_answer","card_id":"ask_1","session_id":"session_owner_1",
		"answers":[{"header":"Scope","selected":["Only unanswered"]},{"header":"Deploy","selected":["Staging"]}]
	}`), &frame))
	wc := &wsConn{userID: "daniel", sendCh: make(chan []byte, 4)}

	(&WSHandler{askUserReg: reg}).handleAskUserAnswer(wc, frame)

	record := findAskUserGatewayLog(t, logBuf, "ws: ask_user_answer accepted; resume failed")
	assert.Contains(t, record["error"], "queue wedged", "the operator needs the real cause in the log")
	r5AssertClean(t, drainConnFrames(wc), "queue wedged")
}

type failingGateDispatcher struct{ err error }

func (f failingGateDispatcher) DispatchResume(*askuser.PendingSet, string) error { return f.err }

// M10 (finding 10): a schedule whose default workspace cannot be READ is not
// silently saved as continue/isolated; the create fails visibly.
func TestGW_M10_ScheduleModeIsNotSilentlyDowngradedOnAReadFailure(t *testing.T) {
	// OMNIPUS_HOME first: newSchedulesTestAPI builds an agent loop, which reads
	// $OMNIPUS_HOME - without this it would open the developer's real data.
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	api, cs := newSchedulesTestAPI(t)
	api.homePath = home
	require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces"), 0o700))
	// Parses as a default workspace record, but not as a stored workspace.
	require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", "01JGWGATEM10WS000000000001.json"),
		[]byte(`{"id":"01JGWGATEM10WS000000000001","is_default":true,"core_team":"not-a-list"}`), 0o600))
	every := int64(60000)
	body := gen.ScheduleCreate{Name: "n", Message: "m", OwnerAgentId: "mia"}
	body.Trigger.Kind = "every"
	body.Trigger.EveryMs = &every
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	r := withUser(httptest.NewRequest(http.MethodPost, "/api/v1/schedules", bytes.NewBuffer(raw)), "alice")
	w := httptest.NewRecorder()

	api.HandleSchedules(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "a read failure must not be saved as a downgraded mode: %s", w.Body.String())
	assert.Empty(t, cs.ListJobs(true), "no schedule may be saved")
	r5AssertClean(t, w.Body.String(), "01JGWGATEM10WS000000000001", home)
}
