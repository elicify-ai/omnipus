// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the worker-is-not-a-chat-target guards at the gateway
// session-binding chokepoints, and the workspace-team-membership guard on
// direct task assignment:
//   - createSessionHTTP rejects a worker agent_id (RESIDUAL PATH 5)
//   - POST/PATCH /api/v1/tasks: agent_id must be a member of the task's
//     workspace TEAM (core_team ∪ delegation-edge endpoints) — worker or not,
//     subagent_3p included (ADR-042 wired external-CLI task execution through
//     TaskExecutor, retiring the old unconditional 3p rejection). (RESIDUAL
//     PATH 7 - Sprint 2; P1 Agent System fix - workers were previously banned
//     outright)
//   - firstChatTargetAgentID skips a worker (RESIDUAL PATH 6)
//   - delegation-created worker task still succeeds (control: Jim→worker works)
//
// Sprint 2 changes: validateBoardTaskAgentID and handleBoardTaskPost are DELETED
// (part of the removed /board/tasks surface). The task-assignment guard is now
// exercised via POST/PATCH /api/v1/tasks.
// TestDelegationWorkerTaskStillSucceeds is rewritten to use task.Task directly.
//
// P1 fix (Agent System): validateTaskAgentID used to reject EVERY worker
// outright (reg.IsWorker(agentID) => 400), regardless of workspace. That is
// replaced by a workspace-team-membership check (worker or not). ADR-042 then
// retired the interim unconditional subagent_3p rejection — external-CLI task
// execution is wired through TaskExecutor, so team membership is the ONLY
// gate for every agent type. See rest_tasks.go's validateTaskAgentID
// docstring and pkg/gateway/rest_tasks_external_cli_assignment_test.go for
// the full 3p accept/reject matrix.
// TestTaskPost_WorkerAssignment / TestTaskPatch_WorkerAssignment below cover
// the new behavior; the differentiation subtests prove the guard is keyed on
// team membership, not agent type.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newWorkerTestRestAPI builds a restAPI whose config holds a base default agent
// ("mia"), a core Ava agent ("ava" — the sole agent a fresh POST-created
// workspace's implicit core_team now seeds by default), a native
// worker ("hans"), a non-worker agent not in any workspace's default team
// ("otto"), and a subagent_3p (external-CLI) worker ("gustav"). All five are
// registered in the loop's registry.
func newWorkerTestRestAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true},
				{ID: "ava", Name: "Ava", Type: config.AgentTypeCore},
				{ID: "hans", Name: "Hans", Type: config.AgentTypeWorker},
				{ID: "otto", Name: "Otto", Type: config.AgentTypeCustom},
				{
					ID:   "gustav",
					Name: "Gustav",
					Type: config.AgentTypeWorker,
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{
							Kind: config.ExecutorKindExternalCLI,
							CLI:  "claude-code",
						},
					},
				},
			},
		},
	}
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), cfgJSON, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop: al,
		homePath:  tmpDir,
		taskStore: task.New(filepath.Join(tmpDir, "tasks")),
		taskLock:  task.TaskFileLock,
	}
	t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })
	return api, tmpDir
}

// TestCreateSessionHTTP_RejectsWorker verifies RESIDUAL PATH 5: an explicit worker
// agent_id on POST /api/v1/sessions returns 400 — a worker cannot back a chat
// session. A base agent (control) returns 201.
func TestCreateSessionHTTP_RejectsWorker(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)

	// Worker agent_id → 400.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"agent_id":"hans"}`))
	api.createSessionHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"a worker agent_id must be rejected with 400 when creating a session")
	assert.Contains(t, strings.ToLower(w.Body.String()), "worker",
		"the error must explain a worker cannot back a session")

	// Control: base agent → 201.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"agent_id":"mia"}`))
	api.createSessionHTTP(w2, r2)
	require.Equal(t, http.StatusCreated, w2.Code,
		"a base agent must be able to back a session (control)")
}

// TestTaskPost_WorkerAssignment verifies the P1 fix on the unified task
// surface: POST /api/v1/tasks/{id} now gates agent_id assignment on
// workspace-TEAM membership (core_team ∪ delegation-edge endpoints), NOT on
// the worker/non-worker type distinction. The differentiation subtests below
// prove this precisely: the SAME worker agent_id ("hans") returns 201 when it
// is a workspace team member and 400 when it is not, and a NON-worker agent
// ("otto") that is off-team is rejected too — symmetric behavior regardless
// of type. A subagent_3p (external-CLI) worker ("gustav") is rejected even
// when it IS a team member, because task execution for external-CLI workers
// is not wired through TaskExecutor today (see validateTaskAgentID's
// docstring in rest_tasks.go for the full rationale).
//
// Sprint 2: replaces the deleted TestBoardTaskPost_RejectsWorkerAssignee
// (which called the removed handleBoardTaskPost/validateBoardTaskAgentID).
// P1 Agent System fix: previously ANY worker was unconditionally 400'd here
// regardless of workspace ("is a worker and cannot be directly assigned a
// task"); that blanket type-based ban is gone.
//
// Traces to: RESIDUAL PATH 7 (task direct-assignment guard); P1 Agent System
// bug (tasks could not be assigned to Subagent/subagent_3p worker-type agents).
func TestTaskPost_WorkerAssignment(t *testing.T) {
	t.Run("worker on workspace team is accepted (the P1 fix)", func(t *testing.T) {
		api, _ := newWorkerTestRestAPI(t)
		wsID := ensureTestWorkspace(t, api)
		setWorkspaceCoreTeam(t, api, wsID, []string{"mia", "hans"})

		body := fmt.Sprintf(
			`{"title":"WorkerOnTeam","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":"hans"}`,
			wsID,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)

		require.Equal(t, http.StatusCreated, w.Code,
			"a worker that IS a workspace team member must be directly assignable; body=%s", w.Body.String())
		var created gen.Task
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
		require.NotNil(t, created.AgentId)
		assert.Equal(t, "hans", *created.AgentId)
	})

	t.Run("worker NOT on workspace team is rejected", func(t *testing.T) {
		api, _ := newWorkerTestRestAPI(t)
		wsID := ensureTestWorkspace(t, api) // default team = ["ava"] only; "hans" absent

		body := fmt.Sprintf(
			`{"title":"WorkerOffTeam","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":"hans"}`,
			wsID,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)

		require.Equal(t, http.StatusBadRequest, w.Code,
			"a worker that is NOT a workspace team member must be rejected; body=%s", w.Body.String())
		assert.Contains(t, strings.ToLower(w.Body.String()), "team",
			"the 400 body must explain the rejection is about workspace-team membership")
	})

	t.Run("non-worker NOT on workspace team is also rejected (symmetry)", func(t *testing.T) {
		api, _ := newWorkerTestRestAPI(t)
		wsID := ensureTestWorkspace(t, api) // default team = ["ava"] only; "otto" (non-worker) absent

		body := fmt.Sprintf(
			`{"title":"NonWorkerOffTeam","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":"otto"}`,
			wsID,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)

		require.Equal(t, http.StatusBadRequest, w.Code,
			"a non-worker agent that is NOT a workspace team member must ALSO be rejected — the guard is "+
				"no longer keyed on worker-vs-main type, only on team membership; body=%s", w.Body.String())
	})

	t.Run("subagent_3p (external-CLI) worker on the team is accepted (ADR-042)", func(t *testing.T) {
		api, _ := newWorkerTestRestAPI(t)
		wsID := ensureTestWorkspace(t, api)
		// Put gustav ON the team: since ADR-042 wired external-CLI task
		// execution through TaskExecutor, team membership is the only gate —
		// the old unconditional subagent_3p rejection is retired. The off-team
		// reject + full accept matrix lives in
		// rest_tasks_external_cli_assignment_test.go.
		setWorkspaceCoreTeam(t, api, wsID, []string{"mia", "gustav"})

		body := fmt.Sprintf(
			`{"title":"Subagent3pTest","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":"gustav"}`,
			wsID,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)

		require.Equal(t, http.StatusCreated, w.Code,
			"a subagent_3p (external-CLI) worker that IS a workspace team member must be accepted — "+
				"ADR-042 wired external-CLI task execution; body=%s",
			w.Body.String())
		var created gen.Task
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
		require.NotNil(t, created.AgentId)
		assert.Equal(t, "gustav", *created.AgentId,
			"the accepted task must persist the subagent_3p assignee")
	})

	t.Run("differentiation: base agent in default team is accepted (control)", func(t *testing.T) {
		// Control proving the guard is real (not hardcoded): "ava" is the ONLY
		// agent newWorkspaceSetupTeam seeds by default, so a freshly
		// created workspace (no explicit core_team) already has her on-team.
		// (This control previously used "mia" via defaultWorkspaceTeam's
		// full base roster; that seed now applies only to the auto-created
		// boot default workspace, ensureDefaultWorkspace — not to ordinary
		// POST-created workspaces like this one.)
		api, _ := newWorkerTestRestAPI(t)
		wsID := ensureTestWorkspace(t, api)

		body := fmt.Sprintf(
			`{"title":"BaseAgentDefaultTeamTest","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":"ava"}`,
			wsID,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)

		require.Equal(t, http.StatusCreated, w.Code,
			"ava is newWorkspaceSetupTeam's sole default seed member, so a fresh workspace accepts her "+
				"without an explicit core_team; body=%s", w.Body.String())
	})
}

// TestTaskPatch_WorkerAssignment mirrors TestTaskPost_WorkerAssignment on the
// PATCH /api/v1/tasks/{id} path — the second call site of validateTaskAgentID.
func TestTaskPatch_WorkerAssignment(t *testing.T) {
	t.Run("PATCH assigns a worker that is a workspace team member", func(t *testing.T) {
		api, _ := newWorkerTestRestAPI(t)
		wsID := ensureTestWorkspace(t, api)
		setWorkspaceCoreTeam(t, api, wsID, []string{"mia", "hans"})

		created := createTaskViaAPI(t, api, "PatchWorkerOnTeam", wsID)

		w := patchTask(t, api, created.Id, `{"agent_id":"hans"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"PATCH assigning a workspace-team-member worker must succeed; body=%s", w.Body.String())
		var updated gen.Task
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
		require.NotNil(t, updated.AgentId)
		assert.Equal(t, "hans", *updated.AgentId)
	})

	t.Run("PATCH rejects a worker that is not a workspace team member", func(t *testing.T) {
		api, _ := newWorkerTestRestAPI(t)
		wsID := ensureTestWorkspace(t, api) // "hans" absent from the default team

		created := createTaskViaAPI(t, api, "PatchWorkerOffTeam", wsID)

		w := patchTask(t, api, created.Id, `{"agent_id":"hans"}`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"PATCH assigning an off-team worker must be rejected; body=%s", w.Body.String())

		// Content check: the task must not have gained the rejected agent_id.
		wGet := httptest.NewRecorder()
		rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+created.Id, nil)
		rGet.URL.Path = "/api/v1/tasks/" + created.Id
		api.HandleTasks(wGet, rGet)
		require.Equal(t, http.StatusOK, wGet.Code)
		var got gen.Task
		require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
		assert.Nil(t, got.AgentId, "task must not have an agent_id after a rejected PATCH")
	})

	t.Run("PATCH accepts subagent_3p on the team (ADR-042)", func(t *testing.T) {
		api, _ := newWorkerTestRestAPI(t)
		wsID := ensureTestWorkspace(t, api)
		setWorkspaceCoreTeam(t, api, wsID, []string{"mia", "gustav"})

		created := createTaskViaAPI(t, api, "PatchSubagent3p", wsID)

		w := patchTask(t, api, created.Id, `{"agent_id":"gustav"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"PATCH assigning an on-team subagent_3p worker must succeed (ADR-042); body=%s", w.Body.String())
		var updated gen.Task
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
		require.NotNil(t, updated.AgentId)
		assert.Equal(t, "gustav", *updated.AgentId)
	})
}

// TestTaskPost_AgentOnDifferentWorkspaceTeam_Rejected proves validateTaskAgentID's
// workspace-team check is workspace-SCOPED — membership on ANY workspace's team
// is not sufficient; the agent must be on THIS task's OWN workspace's team. Every
// other test in this file exercises exactly one workspace at a time, so a bug
// that checked "is this agent on some workspace's team anywhere" (instead of the
// task's specific workspace) would still pass all of them. Two independent
// reviewers flagged this as the one missing case.
//
// BDD:
//
//	Given workspace A's team does NOT include agent "hans" (default roster),
//	  and workspace B's team DOES include agent "hans" (explicit core_team),
//	When POST /api/v1/tasks in workspace A assigns agent_id="hans",
//	Then 400 — hans's membership on workspace B does not authorize workspace A.
//	Control: the identical assignment in workspace B (where hans IS a member)
//	succeeds, proving the rejection above is about workspace scoping and not
//	some unrelated failure (e.g. "hans" not existing in the registry).
func TestTaskPost_AgentOnDifferentWorkspaceTeam_Rejected(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)

	// wsA keeps the default (Ava-only) seed roster — "hans" is not in it.
	wsA := ensureTestWorkspace(t, api)
	wsB := createWorkspaceViaAPI(t, api, "TeamWorkspaceB_"+t.Name(), "")
	setWorkspaceCoreTeam(t, api, wsB, []string{"mia", "hans"})

	body := fmt.Sprintf(
		`{"title":"CrossWorkspaceLeak","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":"hans"}`,
		wsA,
	)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"hans is a member of workspace B's team, not workspace A's — the check must be "+
			"scoped to the task's OWN workspace, not any/every workspace; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "not a member",
		"the 400 body must explain the team-membership rejection")

	// Control: the SAME agent_id in workspace B (where it IS a team member) succeeds.
	bodyB := fmt.Sprintf(
		`{"title":"SameWorkspaceControl","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":"hans"}`,
		wsB,
	)
	wB := httptest.NewRecorder()
	rB := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(bodyB))
	rB.Header.Set("Content-Type", "application/json")
	rB.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wB, rB)
	require.Equal(t, http.StatusCreated, wB.Code,
		"control: hans IS on workspace B's team, so the identical assignment there must succeed; body=%s",
		wB.Body.String())
}

// TestTaskPatch_AgentOnDifferentWorkspaceTeam_Rejected mirrors
// TestTaskPost_AgentOnDifferentWorkspaceTeam_Rejected on the PATCH
// /api/v1/tasks/{id} path — the second call site of validateTaskAgentID.
func TestTaskPatch_AgentOnDifferentWorkspaceTeam_Rejected(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)

	wsA := ensureTestWorkspace(t, api) // "hans" absent from the default team
	wsB := createWorkspaceViaAPI(t, api, "TeamWorkspaceB_"+t.Name(), "")
	setWorkspaceCoreTeam(t, api, wsB, []string{"mia", "hans"})

	created := createTaskViaAPI(t, api, "PatchCrossWorkspaceLeak", wsA)

	w := patchTask(t, api, created.Id, `{"agent_id":"hans"}`)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"hans is a member of workspace B's team, not the task's OWN workspace A — "+
			"PATCH must reject it; body=%s", w.Body.String())

	// Content check: the task must not have gained the rejected agent_id.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+created.Id, nil)
	rGet.URL.Path = "/api/v1/tasks/" + created.Id
	api.HandleTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code)
	var got gen.Task
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	assert.Nil(t, got.AgentId, "task must not have an agent_id after a rejected PATCH")

	// Control: PATCH assigning hans to a task IN workspace B (where it IS a
	// team member) succeeds — proves the rejection above is workspace-scoping,
	// not some unrelated failure.
	createdB := createTaskViaAPI(t, api, "PatchSameWorkspaceControl", wsB)
	wB := patchTask(t, api, createdB.Id, `{"agent_id":"hans"}`)
	require.Equal(t, http.StatusOK, wB.Code,
		"control: hans IS on workspace B's team; body=%s", wB.Body.String())
}

// TestDelegationWorkerTaskStillSucceeds is the CONTROL that proves the worker
// guard on the REST API does NOT block Jim→worker delegation. Delegation
// creates worker-targeted tasks through task_create → taskStore.Create
// directly — bypassing the REST validation layer. Creating a worker-assigned
// task this way must succeed.
// Sprint 2: rewritten from taskstore.TaskEntity to task.Task.
func TestDelegationWorkerTaskStillSucceeds(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)

	// Use a synthetic workspace ID: the store.Create validates workspace_id is
	// non-empty (via normalize) but does NOT FK-check it (that's REST-layer only).
	const delegationWorkspaceID = "01JXDELEGATIONWS000000001"

	// Simulate the delegation path: Jim's task_create tool writes a worker-targeted
	// task straight to the task store. This must NOT be blocked.
	entity := &task.Task{
		ID:          "test-delegated-worker-task-001",
		Title:       "delegated work",
		Prompt:      "do the analysis",
		AgentID:     "hans", // worker assignee — legitimate for delegation
		CreatedBy:   "jim",
		Status:      task.StatusInbox, // unified status: inbox on creation
		Action:      task.ActionLLM,
		WorkspaceID: delegationWorkspaceID,
	}
	require.NoError(t, api.taskStore.Create(entity),
		"delegation (task_create → taskStore.Create) must still create a worker-targeted task")

	got, err := api.taskStore.Get(entity.ID)
	require.NoError(t, err, "taskStore.Get must find the delegated task")
	assert.Equal(t, "hans", got.AgentID,
		"the delegated worker task must persist with the worker as assignee")
	assert.Equal(t, task.StatusInbox, got.Status,
		"the delegated task status must be inbox (unified vocabulary)")
	assert.Equal(t, "delegated work", got.Title,
		"the delegated task title must match the original")
}

// TestFirstChatTargetAgentID_SkipsWorker verifies RESIDUAL PATH 6: the last-resort
// fallback never lands on a worker even when the worker appears first in the list.
// It returns the first chat-target agent instead.
func TestFirstChatTargetAgentID_SkipsWorker(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				// Worker appears first but must be skipped — workers are never chat targets.
				{ID: "hans", Type: config.AgentTypeWorker},
				{ID: "mia"},
			},
		},
	}
	got := firstChatTargetAgentID(cfg)
	assert.NotEqual(t, "hans", got, "firstChatTargetAgentID must never return a worker")
	assert.Equal(t, "mia", got, "firstChatTargetAgentID must return the first chat-target agent")
}

// TestHandleChatMessage_RejectsWorkerAgentID verifies RESIDUAL PATH 4: a chat
// frame that explicitly addresses a worker agentID must be rejected with an error
// frame and must NOT mint a live session for the worker. A worker is not a chat
// target.
func TestHandleChatMessage_RejectsWorkerAgentID(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)
	handler := newWSHandler(bus.NewMessageBus(), api.agentLoop, "")

	wc := makeTestConn()
	handler.handleChatMessage(
		context.Background(),
		"chat-worker-1", // chatID
		"",              // frameSessionID (empty → would mint a new session)
		"do the work",   // content
		"hans",          // agentID = worker
		nil,             // mediaRefs
		"",              // modelName (no per-turn override)
		"",              // workspaceID (no active workspace)
		false,           // setupKickoff
		wc,
	)

	// Drain frames: expect exactly one error frame, and no session_started frame.
	var sawError, sawSessionStarted bool
	for {
		select {
		case raw := <-wc.sendCh:
			var f replayFrameDecoder
			require.NoError(t, json.Unmarshal(raw, &f))
			switch f.Type {
			case string(gen.WsFrameTypeError):
				sawError = true
				assert.Contains(t, strings.ToLower(f.Message), "worker",
					"the error frame must explain a worker cannot be a chat target")
			case string(gen.WsFrameTypeSessionStarted):
				sawSessionStarted = true
			}
		default:
			require.True(t, sawError, "a worker chat frame must produce an error frame")
			require.False(t, sawSessionStarted, "a worker chat frame must NOT mint a session")
			return
		}
	}
}

// TestFirstChatTargetAgentID_AllWorkersReturnsEmpty verifies the degenerate case:
// when every agent is a worker, the fallback returns "" rather than a
// worker (the caller then surfaces a "no agent configured" error).
func TestFirstChatTargetAgentID_AllWorkersReturnsEmpty(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "w1", Type: config.AgentTypeWorker},
				{ID: "w2", Type: config.AgentTypeWorker},
			},
		},
	}
	assert.Equal(t, "", firstChatTargetAgentID(cfg),
		"when all agents are workers, the fallback must return empty, never a worker")
}
