// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func activityRunsAPI(t *testing.T) *restAPI {
	t.Helper()
	api, _ := buildHeartbeatTestAPI(t)
	api.agentLoop.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), session.NewLifecycleStore(t.TempDir()))
	api.taskStore = agent.GetTaskStore(api.agentLoop)
	return api
}

func getActivityRuns(t *testing.T, api *restAPI, agentID, query string) ([]gen.AgentActivityRun, []byte) {
	t.Helper()
	target := "/api/v1/agents/" + agentID + "/activity-runs"
	if query != "" {
		target += "?" + query
	}
	w := httptest.NewRecorder()
	api.HandleAgents(w, httptest.NewRequest(http.MethodGet, target, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var rows []gen.AgentActivityRun
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	return rows, w.Body.Bytes()
}

func seedRun(t *testing.T, api *restAPI, taskID, title, assignee, ws string, kind task.RunKind, sessionID string, recipients []string) *task.TaskRun {
	t.Helper()
	require.NoError(t, api.taskStore.Create(&task.Task{
		ID: taskID, Title: title, AgentID: assignee, WorkspaceID: ws, Status: task.StatusInProgress, Action: task.ActionLLM,
	}))
	run, _, err := api.taskStore.OpenRun(taskID, nil, kind, sessionID, recipients)
	require.NoError(t, err)
	return run
}

func persistRunSession(t *testing.T, api *restAPI, id string, state session.LifecycleState, steeredUnder string) {
	t.Helper()
	rec := &session.LifecycleRecord{
		SessionID: id, Generation: 1, State: state, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: "ws-a", AgentID: "mia", Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: "x"},
	}
	if state == session.LifecycleNeedsInput {
		rec.NeedsInput = &session.NeedsInput{CorrelationID: "corr-wait", TTLDeadline: time.Now().Add(time.Hour)}
	}
	if steeredUnder != "" {
		rec.OwnerScopeKind = session.OwnerScopeParentSession
		rec.OwnerScopeID = steeredUnder
		rec.SteeredBy = &session.SteeredBy{SteeringSessionID: steeredUnder, RootSessionID: steeredUnder}
	}
	require.NoError(t, api.agentLoop.GetSessionLifecycleStore().Persist(rec))
}

// Oracle: session-core FR-033 / BDD-10.1 — Activity shows an agent's open
// task/scheduler runs with their own session as the Open target, one row per
// run (a MAIN child run is ONE row, mode=main), states mapped from the run
// session's lifecycle; a closed run, another agent's run and a run in another
// workspace are not shown.
func TestAgentActivityRuns_RowsForAssigneeScopeStateAndOpenTarget(t *testing.T) {
	api := activityRunsAPI(t)
	mainID, err := session.MainSessionID("ws-a", "mia")
	require.NoError(t, err)

	seedRun(t, api, "t-main", "Nightly report", "mia", "ws-a", task.RunKindScheduled, "sess-main-child", []string{mainID})
	persistRunSession(t, api, "sess-main-child", session.LifecycleRunning, mainID)
	seedRun(t, api, "t-iso", "Ad hoc job", "mia", "ws-a", task.RunKindManual, "sess-iso", nil)
	persistRunSession(t, api, "sess-iso", session.LifecycleQueued, "")
	seedRun(t, api, "t-wait", "Needs an answer", "mia", "ws-a", task.RunKindManual, "sess-wait", nil)
	persistRunSession(t, api, "sess-wait", session.LifecycleNeedsInput, "")
	seedRun(t, api, "t-other-ws", "Elsewhere", "mia", "ws-b", task.RunKindManual, "sess-other-ws", nil)
	seedRun(t, api, "t-other-agent", "Not mine", "jim", "ws-a", task.RunKindManual, "sess-jim", nil)
	closed := seedRun(t, api, "t-closed", "Done already", "mia", "ws-a", task.RunKindManual, "sess-closed", nil)
	require.NoError(t, api.taskStore.CloseRun("t-closed", closed.RunID, task.StatusDone, "ok"))

	rows, raw := getActivityRuns(t, api, "mia", "workspace_id=ws-a")
	byTask := map[string]gen.AgentActivityRun{}
	for _, r := range rows {
		byTask[r.TaskId] = r
	}
	require.Len(t, rows, 3, "exactly the three open runs of mia in ws-a: %s", raw)

	m := byTask["t-main"]
	require.Equal(t, gen.AgentActivityRunModeMain, m.Mode, "a child of the assignee's main is the MAIN row")
	require.Equal(t, gen.AgentActivityRunKindScheduled, m.Kind)
	require.Equal(t, gen.AgentActivityRunStateRunning, m.State)
	require.Equal(t, gen.AgentActivityRunRoleAssignee, m.Role)
	require.NotNil(t, m.SessionId)
	require.Equal(t, "sess-main-child", *m.SessionId, "Open targets the run's own session")
	require.Equal(t, "Nightly report", m.TaskTitle)

	require.Equal(t, gen.AgentActivityRunModeIsolated, byTask["t-iso"].Mode)
	require.Equal(t, gen.AgentActivityRunKindTask, byTask["t-iso"].Kind)
	require.Equal(t, gen.AgentActivityRunStateQueued, byTask["t-iso"].State)
	require.Equal(t, gen.AgentActivityRunStateWaiting, byTask["t-wait"].State)

	var generic []map[string]any
	require.NoError(t, json.Unmarshal(raw, &generic))
	for _, row := range generic {
		_, hasTokens := row["available_tokens"]
		require.False(t, hasTokens, "no token figure is carried: unknown is never zero")
	}
}

// Oracle: FR-019/FR-033 — an agent whose main was captured as a run recipient
// (the starter) sees that run even though it is not the assignee.
func TestAgentActivityRuns_RecipientSeesTheRunItStarted(t *testing.T) {
	api := activityRunsAPI(t)
	jimMain, err := session.MainSessionID("ws-a", "jim")
	require.NoError(t, err)
	miaMain, err := session.MainSessionID("ws-a", "mia")
	require.NoError(t, err)
	seedRun(t, api, "t-started", "Started by jim", "mia", "ws-a", task.RunKindManual, "sess-started", []string{jimMain, miaMain})
	persistRunSession(t, api, "sess-started", session.LifecycleRunning, miaMain)

	rows, _ := getActivityRuns(t, api, "jim", "")
	require.Len(t, rows, 1)
	require.Equal(t, gen.AgentActivityRunRoleRecipient, rows[0].Role)
	require.Equal(t, "mia", rows[0].AgentId, "agent_id is the assignee")

	none, _ := getActivityRuns(t, api, "agent-with-nothing", "")
	require.Empty(t, none, "an agent with no run gets an empty array, never null")
}
