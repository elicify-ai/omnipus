//go:build !cgo

// rest_plans_test.go — tests for the Plans REST surface (ADR-049 D1/D4, Wave
// 2-C1 deferred REST paths): CRUD, approve (400 body shape + tiered/member
// gates), stop, progress, and the plan_id cross-workspace FK guard on tasks.
// Also covers the agent-delete-while-owning-an-active-plan 400 guard
// (rest.go's deleteAgent).

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const testPlansAgentID = "01JXTESTPLANSAGENT0000001"

// newTestRestAPIWithPlans builds a restAPI with one enabled agent and a real
// pkg/plan.Store, mirroring newTestRestAPIWithAgent (board_task_hardening_test.go)
// plus planStore wiring.
func newTestRestAPIWithPlans(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	agentWorkspace := filepath.Join(tmpDir, "agents", testPlansAgentID)
	require.NoError(t, os.MkdirAll(agentWorkspace, 0o700))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:      tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{
					ID:      testPlansAgentID,
					Name:    "Plans Test Agent",
					Default: true,
					Type:    config.AgentTypeCustom,
					Home:    agentWorkspace,
				},
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		taskLock:      task.TaskFileLock,
		planStore:     plan.New(tmpDir + "/plans"),
	}
	t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })
	return api
}

// createTestWorkspace creates a workspace via the real HandleWorkspaces path
// and returns its ID.
func createTestWorkspace(t *testing.T, api *restAPI, name string) string {
	t.Helper()
	return createWorkspaceViaAPI(t, api, name, "")
}

// postPlan issues POST /api/v1/workspaces/{wsID}/plans and returns the recorder.
func postPlan(t *testing.T, api *restAPI, wsID, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+wsID+"/plans", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces/" + wsID + "/plans"
	api.HandleWorkspaces(w, r)
	return w
}

// getPlan issues GET /api/v1/plans/{id} and returns the recorder.
func getPlan(t *testing.T, api *restAPI, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/plans/"+id, nil)
	r.URL.Path = "/api/v1/plans/" + id
	api.HandlePlans(w, r)
	return w
}

// putPlan issues PUT /api/v1/plans/{id} and returns the recorder.
func putPlan(t *testing.T, api *restAPI, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/plans/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/plans/" + id
	api.HandlePlans(w, r)
	return w
}

// postPlanAction issues POST /api/v1/plans/{id}/{action} and returns the recorder.
func postPlanAction(t *testing.T, api *restAPI, id, action string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/plans/"+id+"/"+action, nil)
	r.URL.Path = "/api/v1/plans/" + id + "/" + action
	api.HandlePlans(w, r)
	return w
}

// deletePlan issues DELETE /api/v1/plans/{id} and returns the recorder.
func deletePlan(t *testing.T, api *restAPI, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/plans/"+id, nil)
	r.URL.Path = "/api/v1/plans/" + id
	api.HandlePlans(w, r)
	return w
}

// postTask issues POST /api/v1/tasks and returns the recorder.
func postTask(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)
	return w
}

// TestPlanCRUD_RoundtripAndProgress covers create, list, get, update, and
// delete (with plan_id cleared on the former member task), plus server-computed
// progress (R4/C19).
func TestPlanCRUD_RoundtripAndProgress(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")

	// Create.
	createBody := `{"workspace_id":"` + wsID + `","title":"Launch v1","owner_agent_id":"` + testPlansAgentID + `"}`
	wCreate := postPlan(t, api, wsID, createBody)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var created gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))
	assert.NotEmpty(t, created.Id)
	assert.Equal(t, gen.PlanStateDraft, created.State)
	assert.Equal(t, wsID, created.WorkspaceId)
	require.NotNil(t, created.Progress)
	assert.InDelta(t, 0, *created.Progress, 0.0001, "no members yet -> 0 progress")

	// List (workspace-scoped).
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+wsID+"/plans", nil)
	rList.URL.Path = "/api/v1/workspaces/" + wsID + "/plans"
	api.HandleWorkspaces(wList, rList)
	require.Equal(t, http.StatusOK, wList.Code)
	var listResp gen.PlanListResponse
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &listResp))
	require.Len(t, listResp.Plans, 1)
	assert.Equal(t, created.Id, listResp.Plans[0].Id)

	// Get.
	wGet := getPlan(t, api, created.Id)
	require.Equal(t, http.StatusOK, wGet.Code)

	// Update.
	wPut := putPlan(t, api, created.Id, `{"title":"Launch v1 (delayed)"}`)
	require.Equal(t, http.StatusOK, wPut.Code, "body=%s", wPut.Body.String())
	var updated gen.Plan
	require.NoError(t, json.Unmarshal(wPut.Body.Bytes(), &updated))
	assert.Equal(t, "Launch v1 (delayed)", updated.Title)

	// Add a member task and mark it done -> progress should be 1.0.
	taskBody := `{"workspace_id":"` + wsID + `","title":"member","plan_id":"` + created.Id + `"}`
	wTask := postTask(t, api, taskBody)
	require.Equal(t, http.StatusCreated, wTask.Code, "body=%s", wTask.Body.String())
	var createdTask gen.Task
	require.NoError(t, json.Unmarshal(wTask.Body.Bytes(), &createdTask))
	_, uerr := api.taskStore.Update(createdTask.Id, task.Patch{Status: taskStatusPtr(task.StatusDone)})
	require.NoError(t, uerr)

	wGetAfter := getPlan(t, api, created.Id)
	require.Equal(t, http.StatusOK, wGetAfter.Code)
	var afterProgress gen.Plan
	require.NoError(t, json.Unmarshal(wGetAfter.Body.Bytes(), &afterProgress))
	require.NotNil(t, afterProgress.Progress)
	assert.InDelta(t, 1.0, *afterProgress.Progress, 0.0001)

	// Delete (draft, non-running) — clears plan_id on the member task.
	wDel := deletePlan(t, api, created.Id)
	require.Equal(t, http.StatusNoContent, wDel.Code)
	reloadedTask, gErr := api.taskStore.Get(createdTask.Id)
	require.NoError(t, gErr)
	assert.Empty(t, reloadedTask.PlanID, "plan_id should be cleared on the former member task")

	wGetGone := getPlan(t, api, created.Id)
	assert.Equal(t, http.StatusNotFound, wGetGone.Code)
}

// taskStatusPtr is a tiny local helper (avoids importing testify's pointer
// helpers just for this one call site).
func taskStatusPtr(s task.Status) *task.Status { return &s }

// TestPlanApprove_MemberCriteriaGateReturns400WithTaskErrors verifies FR-084:
// the unconditional member-task-criteria gate rejects approval with the
// {task_errors:[{task_id,title,reason}]} body shape the SPA parses.
func TestPlanApprove_MemberCriteriaGateReturns400WithTaskErrors(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Approve WS")

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Needs criteria","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	// Member task with ZERO criteria.
	wTask := postTask(t, api, `{"workspace_id":"`+wsID+`","title":"no criteria task","plan_id":"`+p.Id+`"}`)
	require.Equal(t, http.StatusCreated, wTask.Code)
	var offendingTask gen.Task
	require.NoError(t, json.Unmarshal(wTask.Body.Bytes(), &offendingTask))

	wApprove := postPlanAction(t, api, p.Id, "approve")
	require.Equal(t, http.StatusBadRequest, wApprove.Code, "body=%s", wApprove.Body.String())
	var approveErr gen.PlanApproveError
	require.NoError(t, json.Unmarshal(wApprove.Body.Bytes(), &approveErr))
	require.NotNil(t, approveErr.TaskErrors, "expected task_errors, got body=%s", wApprove.Body.String())
	require.Len(t, *approveErr.TaskErrors, 1)
	assert.Equal(t, offendingTask.Id, (*approveErr.TaskErrors)[0].TaskId)
	assert.NotEmpty(t, (*approveErr.TaskErrors)[0].Reason)

	// The plan must still be draft — approval never partially applied.
	wGet := getPlan(t, api, p.Id)
	var reloaded gen.Plan
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &reloaded))
	assert.Equal(t, gen.PlanStateDraft, reloaded.State)
}

// TestPlanApprove_SoftTierEmptyDoDSucceeds_ThenNotDraftRejected verifies a
// human/UI-authored plan (CreatedBy = username, not a known agent ID — the
// only creation path this wave implements) may approve with an empty DoD
// (SD-A7 soft tier), and that re-approving a non-draft plan is rejected with
// the `error` field of PlanApproveError.
func TestPlanApprove_SoftTierEmptyDoDSucceeds_ThenNotDraftRejected(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Soft Tier WS")

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"No DoD needed","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))
	require.Empty(t, p.Dod)

	wApprove := postPlanAction(t, api, p.Id, "approve")
	require.Equal(t, http.StatusOK, wApprove.Code, "body=%s", wApprove.Body.String())
	var approved gen.Plan
	require.NoError(t, json.Unmarshal(wApprove.Body.Bytes(), &approved))
	assert.Equal(t, gen.PlanStateApproved, approved.State)

	wApproveAgain := postPlanAction(t, api, p.Id, "approve")
	require.Equal(t, http.StatusBadRequest, wApproveAgain.Code)
	var approveErr gen.PlanApproveError
	require.NoError(t, json.Unmarshal(wApproveAgain.Body.Bytes(), &approveErr))
	require.NotNil(t, approveErr.Error)
	assert.Contains(t, *approveErr.Error, "draft")
}

// TestPlanStop_RequiresRunning verifies POST /plans/{id}/stop is rejected on
// a non-running plan and succeeds (running->failed(stopped_by_user)) once the
// plan is running.
func TestPlanStop_RequiresRunning(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Stop WS")

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Stoppable","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	// Stop on a draft plan -> 400.
	wStopDraft := postPlanAction(t, api, p.Id, "stop")
	assert.Equal(t, http.StatusBadRequest, wStopDraft.Code)

	// draft -> approved (soft tier, empty DoD, no members).
	wApprove := postPlanAction(t, api, p.Id, "approve")
	require.Equal(t, http.StatusOK, wApprove.Code)

	// approved -> running (legal transition; the engine would normally do
	// this on its next tick, but this test drives the transition directly
	// via PUT to isolate the stop endpoint).
	wRun := putPlan(t, api, p.Id, `{"state":"running"}`)
	require.Equal(t, http.StatusOK, wRun.Code, "body=%s", wRun.Body.String())

	wStop := postPlanAction(t, api, p.Id, "stop")
	require.Equal(t, http.StatusOK, wStop.Code, "body=%s", wStop.Body.String())
	var stopped gen.Plan
	require.NoError(t, json.Unmarshal(wStop.Body.Bytes(), &stopped))
	assert.Equal(t, gen.PlanStateFailed, stopped.State)
	require.NotNil(t, stopped.FailedReason)
	assert.Equal(t, gen.PlanFailedReasonStoppedByUser, *stopped.FailedReason)
}

// TestTaskPlanID_CrossWorkspaceRejected verifies the plan_id FK gap fix
// (rest_tasks.go): a task cannot reference a plan in a different workspace.
func TestTaskPlanID_CrossWorkspaceRejected(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsA := createTestWorkspace(t, api, "WS A")
	wsB := createTestWorkspace(t, api, "WS B")

	wCreate := postPlan(t, api, wsA, `{"workspace_id":"`+wsA+`","title":"Plan in A","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	// Cross-workspace: task in B referencing plan in A -> 400.
	wCross := postTask(t, api, `{"workspace_id":"`+wsB+`","title":"cross-ws task","plan_id":"`+p.Id+`"}`)
	assert.Equal(t, http.StatusBadRequest, wCross.Code, "body=%s", wCross.Body.String())

	// Same-workspace: task in A referencing plan in A -> 201.
	wSame := postTask(t, api, `{"workspace_id":"`+wsA+`","title":"same-ws task","plan_id":"`+p.Id+`"}`)
	assert.Equal(t, http.StatusCreated, wSame.Code, "body=%s", wSame.Body.String())

	// PATCH path: create a plain task in B, then try to PATCH its plan_id to
	// the plan in A -> 400.
	wPlainTask := postTask(t, api, `{"workspace_id":"`+wsB+`","title":"plain B task"}`)
	require.Equal(t, http.StatusCreated, wPlainTask.Code)
	var plainTask gen.Task
	require.NoError(t, json.Unmarshal(wPlainTask.Body.Bytes(), &plainTask))

	wPatch := httptest.NewRecorder()
	rPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+plainTask.Id,
		strings.NewReader(`{"plan_id":"`+p.Id+`"}`))
	rPatch.Header.Set("Content-Type", "application/json")
	rPatch.URL.Path = "/api/v1/tasks/" + plainTask.Id
	api.HandleTasks(wPatch, rPatch)
	assert.Equal(t, http.StatusBadRequest, wPatch.Code, "body=%s", wPatch.Body.String())
}

// TestDeleteAgent_OwningActivePlan_Rejected verifies the agent-delete guard
// (rest.go's deleteAgent): an agent that owns >=1 running plan cannot be
// deleted.
func TestDeleteAgent_OwningActivePlan_Rejected(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Owner WS")

	// Wire a real PlanEngine so HasActivePlansOwnedBy is reachable — mirrors
	// gateway.go's boot wiring (agent.NewPlanEngine + SetPlanEngine), but
	// without Start()ing background goroutines (HasActivePlansOwnedBy is a
	// synchronous store scan, not engine-loop-dependent).
	pe := agent.NewPlanEngine(api.agentLoop, api.planStore, api.taskStore, api.taskExecutor)
	api.agentLoop.SetPlanEngine(pe)

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Owned plan","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	require.Equal(t, http.StatusOK, postPlanAction(t, api, p.Id, "approve").Code)
	require.Equal(t, http.StatusOK, putPlan(t, api, p.Id, `{"state":"running"}`).Code)

	assert.True(t, pe.HasActivePlansOwnedBy(testPlansAgentID))

	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+testPlansAgentID, nil)
	rDel.URL.Path = "/api/v1/agents/" + testPlansAgentID
	api.HandleAgents(wDel, rDel)
	assert.Equal(t, http.StatusBadRequest, wDel.Code, "body=%s", wDel.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(wDel.Body.Bytes(), &body))
	assert.Equal(t, "agent_owns_active_plans", body["code"])
}

// TestPlanEngineBoot_ConstructStartStop is a smoke test of gateway.go's exact
// plan-engine boot wiring (setupAndStartServices): construct a *plan.Store +
// agent.NewPlanEngine bound to a REAL AgentLoop's own task store/executor
// (agent.GetTaskStore/GetTaskExecutor — the same accessors gateway.go calls),
// SetPlanEngine, RegisterActiveCounter stubs, Start, then Stop — asserting no
// error/panic at any step. This is deliberately narrow: Wave 2-B's own
// pkg/agent/plan_engine_test.go already exhaustively covers boot
// reconciliation behavior in-package with fake judge/dispatcher seams; this
// test instead proves MY gateway-side integration (real dependencies, the
// exact construction call shape) actually boots.
func TestPlanEngineBoot_ConstructStartStop(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:      tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	planStore := plan.New(filepath.Join(tmpDir, "plans"))
	var emitted []agent.PlanStatusChangedPayload
	planStore.OnChange = func(p *plan.Plan) {
		emitted = append(emitted, agent.PlanStatusChangedPayload{
			PlanID: p.ID,
			State:  string(p.State),
		})
	}

	pe := agent.NewPlanEngine(al, planStore, agent.GetTaskStore(al), agent.GetTaskExecutor(al))
	pe.RegisterActiveCounter("goal", func() (int, error) { return 0, nil })
	pe.RegisterActiveCounter("loop", func() (int, error) { return 0, nil })

	require.NoError(t, pe.Start(context.Background()))
	al.SetPlanEngine(pe)
	assert.Same(t, pe, agent.GetPlanEngine(al))

	// Admission works against an empty plan store (0 active, cap from config
	// default since Planning is unset on this minimal cfg).
	ok, active, cap := pe.Admit("plan")
	assert.True(t, ok)
	assert.Equal(t, 0, active)
	assert.Positive(t, cap)

	pe.Stop()

	// A plan created via the store (OnChange fires) proves the boot-wired
	// hook is live end-to-end, independent of engine Start/Stop state.
	require.NoError(t, planStore.Create(&plan.Plan{
		WorkspaceID:  "smoke-ws",
		Title:        "smoke",
		OwnerAgentID: "smoke-agent",
	}))
	require.Len(t, emitted, 1)
	assert.Equal(t, "draft", emitted[0].State)
}

// TestPlanOwnerAgent_ValidationRejectsUnregisteredSystemAndWorker is review
// r1 m1: the prior owner_agent_id check on both POST (create) and PUT was
// format-only (validateEntityID, a path-traversal guard) — it let a
// typo'd/nonexistent agent id, a System Agent, or a worker through
// unrejected, even though Plan.OwnerAgentID's own doc comment requires "the
// agent woken at plan decision points" (a real, addressable agent — none of
// those three kinds can ever actually be woken: nonexistent has no
// destination, System Agents are never chat targets (ADR-049 D3), workers
// are delegation-only labor with no standalone turn to wake).
func TestPlanOwnerAgent_ValidationRejectsUnregisteredSystemAndWorker(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List,
		config.AgentConfig{ID: "judge-agent", Name: "Judge", Type: config.AgentTypeSystem},
		config.AgentConfig{ID: "worker-agent", Name: "Worker", Type: config.AgentTypeWorker},
	)
	wsID := createTestWorkspace(t, api, "Owner Validation WS")

	cases := []struct {
		name    string
		ownerID string
	}{
		{"unregistered", "nonexistent-agent-id"},
		{"system_agent", "judge-agent"},
		{"worker_agent", "worker-agent"},
	}
	for _, tc := range cases {
		t.Run("create_"+tc.name, func(t *testing.T) {
			w := postPlan(t, api, wsID,
				`{"workspace_id":"`+wsID+`","title":"Bad Owner `+tc.name+`","owner_agent_id":"`+tc.ownerID+`"}`)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		})
	}

	// A valid plan to exercise the PUT path against.
	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Valid Owner","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	for _, tc := range cases {
		t.Run("put_"+tc.name, func(t *testing.T) {
			w := putPlan(t, api, p.Id, `{"owner_agent_id":"`+tc.ownerID+`"}`)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		})
	}
}

// TestPlanPut_DoDAndOwnerFrozenOnceNotDraft is review r1 m6: DoD and
// owner_agent_id must become immutable once a plan leaves draft state — DoD
// is the contract the plan judge adjudicates against (changing it mid-flight
// could invalidate in-progress/already-recorded judge rounds), and owner is
// who gets woken at plan decision points (reassigning it mid-flight would
// silently redirect wake notifications). Bounds must stay mutable in any
// state (an operator may legitimately extend a running plan's budget), and a
// state-neutral field like title must also stay editable.
func TestPlanPut_DoDAndOwnerFrozenOnceNotDraft(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List,
		config.AgentConfig{ID: "other-owner-agent", Name: "Other Owner", Type: config.AgentTypeCustom, Default: false})
	wsID := createTestWorkspace(t, api, "Freeze WS")

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Freeze Me","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	// DoD and owner_agent_id are still mutable in draft.
	wDraftDoD := putPlan(t, api, p.Id,
		`{"dod":[{"kind":"prose","text":"ship it","author":{"kind":"user","id":"tester"}}]}`)
	require.Equal(t, http.StatusOK, wDraftDoD.Code, "body=%s", wDraftDoD.Body.String())
	wDraftOwner := putPlan(t, api, p.Id, `{"owner_agent_id":"other-owner-agent"}`)
	require.Equal(t, http.StatusOK, wDraftOwner.Code, "body=%s", wDraftOwner.Body.String())
	// Put the owner back for the rest of this test.
	wDraftOwnerBack := putPlan(t, api, p.Id, `{"owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusOK, wDraftOwnerBack.Code, "body=%s", wDraftOwnerBack.Body.String())

	// draft -> approved.
	wApprove := postPlanAction(t, api, p.Id, "approve")
	require.Equal(t, http.StatusOK, wApprove.Code, "body=%s", wApprove.Body.String())

	// DoD and owner_agent_id must now be REJECTED (409, state conflict).
	wApprovedDoD := putPlan(t, api, p.Id,
		`{"dod":[{"kind":"prose","text":"changed mid-flight","author":{"kind":"user","id":"tester"}}]}`)
	assert.Equal(t, http.StatusConflict, wApprovedDoD.Code, "body=%s", wApprovedDoD.Body.String())
	wApprovedOwner := putPlan(t, api, p.Id, `{"owner_agent_id":"other-owner-agent"}`)
	assert.Equal(t, http.StatusConflict, wApprovedOwner.Code, "body=%s", wApprovedOwner.Body.String())

	// Bounds and title must still be editable once approved.
	wApprovedBounds := putPlan(t, api, p.Id, `{"bounds":{"idle_expiry_days":14}}`)
	assert.Equal(t, http.StatusOK, wApprovedBounds.Code, "body=%s", wApprovedBounds.Body.String())
	wApprovedTitle := putPlan(t, api, p.Id, `{"title":"Still Editable"}`)
	assert.Equal(t, http.StatusOK, wApprovedTitle.Code, "body=%s", wApprovedTitle.Body.String())

	// Confirm the rejected DoD/owner writes did NOT silently apply.
	wGet := getPlan(t, api, p.Id)
	require.Equal(t, http.StatusOK, wGet.Code)
	var final gen.Plan
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &final))
	assert.Equal(t, testPlansAgentID, final.OwnerAgentId, "owner must still be the original owner")
	require.NotNil(t, final.Dod)
	require.Len(t, *final.Dod, 1)
	assert.Equal(t, "ship it", (*final.Dod)[0].Text, "DoD must still be the draft-state value")
}
