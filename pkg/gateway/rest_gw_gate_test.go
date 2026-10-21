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

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
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

	// The agent's session lives in the loop's ONE live store (the cascade no
	// longer opens a store of its own - DEL-10).
	sessStore := api.agentLoop.GetSessionStore()
	require.NotNil(t, sessStore)
	meta, err := sessStore.NewSession(session.SessionTypeChat, "webchat", created.Id)
	require.NoError(t, err)

	stale := strings.Repeat("0", 64)
	require.NotEqual(t, stale, created.Revision, "instrument check: the revision must really be stale")
	delW := httptest.NewRecorder()
	delR := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+created.Id+"?revision="+stale, nil)
	delR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(delW, delR)

	assert.Equal(t, http.StatusConflict, delW.Code, "a stale revision is still a conflict: %s", delW.Body.String())
	_, statErr := os.Stat(filepath.Join(sessStore.BaseDir(), meta.ID))
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
	_, statErr = os.Stat(filepath.Join(sessStore.BaseDir(), meta.ID))
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
	h.handleChatMessage(context.Background(), "chat-m4", mainID, "still here?", "mia", nil, "", "", wc2)
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
	r5Block(t, filepath.Join(env.home, "workspaces", ws+".json"))

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
	var frame gen.AskUserAnswerFrame
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

// Founder ruling (S2 #6): "a main chat can only be deleted when the main agent is
// deleted". DELETE /sessions/all never deletes a main (its history is kept), stops
// any running turn of the chats it does delete first, and leaves no references.
func clearAllDo(env *u1Env) (int, string) {
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/all", nil)
	w := httptest.NewRecorder()
	env.api.HandleClearSessions(w, r)
	return w.Code, w.Body.String()
}

func TestGW_ClearAll_MainsSurviveWithTheirHistory(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JGWGATECLRWS0000000000001"
	env.seedWorkspace(t, ws, true, "mia", "omnipus") // the default workspace: Omnipus, a member, owns a main here too
	mia := u1MainID(ws, "mia")
	omnipus := u1MainID(ws, "omnipus")
	for _, id := range []string{mia, omnipus} {
		code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
		require.Equal(t, http.StatusOK, code, "precondition: %s opens", id)
		require.NoError(t, env.store(t).AppendTranscript(id, session.TranscriptEntry{
			ID: "keep-" + id, Role: "user", Content: "keep me", AgentID: "mia", Timestamp: time.Now().UTC(),
		}))
	}
	chat := sf1NewChat(t, env)

	code, body := clearAllDo(env)

	require.Equal(t, http.StatusOK, code, body)
	for _, id := range []string{mia, omnipus} {
		entries, err := env.store(t).ReadTranscript(id)
		require.NoError(t, err, "a main must survive delete-all: %s", id)
		require.Len(t, entries, 1, "its history is kept")
		assert.Equal(t, "keep me", entries[0].Content)
	}
	_, err := env.store(t).GetMeta(chat)
	assert.Error(t, err, "an ordinary chat is deleted")
	var resp gen.ClearAllSessionsResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Equal(t, 1, resp.Count, "only the ordinary chat was removed")
}

// Gap 15 (R2): the mains-helper skip had no fixture, so removing it survived.
// A chat that BELONGS to a main — here through its own parent link, the second
// path sessionsBelongingToMains walks — is part of that main's history: it must
// survive delete-all and must not be counted as removed.
func TestGW_ClearAll_KeepsAMainsHelperChat(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JGWGATECLRWS0000000000002"
	env.seedWorkspace(t, ws, true, "mia") // the default workspace: Admin owns a main here too
	main := u1MainID(ws, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+main, "")
	require.Equal(t, http.StatusOK, code, "precondition: the main opens")

	helper := sf1NewChat(t, env)
	parent := main
	err := env.store(t).SetMeta(helper, session.MetaPatch{ParentSessionID: &parent})
	require.NoError(t, err, "fixture: link the chat to the main through its own parent")

	code, body := clearAllDo(env)
	require.Equal(t, http.StatusOK, code, body)
	var resp gen.ClearAllSessionsResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Equal(t, 0, resp.Count, "a main's helper is neither removed nor counted")

	if _, err := env.store(t).GetMeta(helper); err != nil {
		t.Fatalf("a chat belonging to a main must survive delete-all: %v", err)
	}
}

// Every chat delete-all removes is stopped first, through the same Stop the single
// delete uses; one whose Stop is incomplete is NOT deleted and is reported.
func TestGW_ClearAll_StopsEachChatFirstAndKeepsOnesThatWillNotStop(t *testing.T) {
	env := u1NewEnv(t, false)
	a, b := sf1NewChat(t, env), sf1NewChat(t, env)
	var stopped []string
	env.api.stopSession = func(_ context.Context, req agent.StopRequest) (agent.StopResult, error) {
		stopped = append(stopped, req.SessionID)
		if req.SessionID == b {
			return agent.StopResult{Report: steer.CancelReport{
				Unreachable: []steer.UnreachableSession{{ID: b, Reason: "wedged"}}}}, nil
		}
		return agent.StopResult{}, nil
	}

	code, body := clearAllDo(env)

	require.Equal(t, http.StatusOK, code, body)
	assert.ElementsMatch(t, []string{a, b}, stopped, "each chat is stopped before it is deleted")
	_, errA := env.store(t).GetMeta(a)
	assert.Error(t, errA, "the stopped chat is deleted")
	_, errB := env.store(t).GetMeta(b)
	assert.NoError(t, errB, "a chat that will not stop is not deleted")
	var resp gen.ClearAllSessionsResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Equal(t, 1, resp.Count)
	require.NotNil(t, resp.Warnings, "the kept chat is reported")
	r5AssertClean(t, body, a, b)
}

// No references are left to a deleted chat: its uploads go with it.
func TestGW_ClearAll_RemovesTheDeletedChatsUploads(t *testing.T) {
	env := u1NewEnv(t, false)
	chat := sf1NewChat(t, env)
	up := filepath.Join(filepath.Dir(env.store(t).BaseDir()), "uploads", chat)
	require.NoError(t, os.MkdirAll(up, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(up, "f.txt"), []byte("x"), 0o600))

	code, body := clearAllDo(env)

	require.Equal(t, http.StatusOK, code, body)
	_, err := os.Stat(up)
	assert.True(t, errors.Is(err, os.ErrNotExist), "the deleted chat's uploads must be gone")
}

// Architect final-gate item 1: DELETE /agents/{id} stops the agent's running work
// before it deletes the sessions, through the same Stop every delete uses, and a
// session that will not stop keeps the agent record so the delete can retry.
func TestGW_AgentDelete_StopsSessionsFirstAndKeepsTheRecordIfOneWillNotStop(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.agentLoop.SetReloadFunc(func() error {
		defer api.agentLoop.ClearReloadPending()
		return api.refreshConfigAndRewireServices(api.configPath())
	})
	createW := httptest.NewRecorder()
	createR := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(
		`{"name": "Busy Agent", "type": "Main", "model": "claude-sonnet-4-6", "soul": "I am busy."}`))
	createR.Header.Set("Content-Type", "application/json")
	createR.URL.Path = "/api/v1/agents"
	api.HandleAgents(createW, createR)
	require.Equal(t, http.StatusCreated, createW.Code, createW.Body.String())
	var created gen.Agent
	require.NoError(t, json.Unmarshal(createW.Body.Bytes(), &created))
	store := api.agentLoop.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", created.Id)
	require.NoError(t, err)
	var stopped []string
	wedged := true
	api.stopSession = func(_ context.Context, req agent.StopRequest) (agent.StopResult, error) {
		stopped = append(stopped, req.SessionID)
		if wedged {
			return agent.StopResult{Report: steer.CancelReport{
				Unreachable: []steer.UnreachableSession{{ID: req.SessionID, Reason: "wedged"}}}}, nil
		}
		return agent.StopResult{}, nil
	}
	del := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+created.Id+"?revision="+created.Revision, nil)
		r.URL.Path = "/api/v1/agents/" + created.Id
		api.HandleAgents(w, r)
		return w
	}

	w := del()

	assert.Equal(t, []string{meta.ID}, stopped, "the agent's session is stopped before it is deleted")
	assert.Equal(t, http.StatusInternalServerError, w.Code, "a session that will not stop is a partial, retryable delete: %s", w.Body.String())
	_, getErr := store.GetMeta(meta.ID)
	assert.NoError(t, getErr, "a session that did not stop must not be deleted")
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.Id, nil)
	getR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(getW, getR)
	assert.Equal(t, http.StatusOK, getW.Code, "the agent record stays so the same delete retries")

	wedged = false // retry once the work has stopped
	w = del()
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, getErr = store.GetMeta(meta.ID)
	assert.Error(t, getErr, "the retried delete removes the session")
}

// Control for the WebSocket door: an ELIGIBLE member's main still attaches (the
// removed-member case above is refused; this proves the gate is not a blanket refusal).
func TestGW_M4_EligibleMainStillAttachesOverWebSocket(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JGWGATEM4WS0000000000003"
	env.seedWorkspace(t, ws, false, "mia")
	id := u1MainID(ws, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+id, "")
	require.Equal(t, http.StatusOK, code)
	h := newWSHandler(bus.NewMessageBus(), env.api.agentLoop, "")
	t.Cleanup(h.Wait)
	wc := makeTestConn()

	h.handleAttachSession(context.Background(), "chat-m4c", id, nil, wc)

	frames := drainConnFrames(wc)
	assert.Contains(t, frames, "catch_up_complete", "an eligible main attaches: %s", frames)
}
