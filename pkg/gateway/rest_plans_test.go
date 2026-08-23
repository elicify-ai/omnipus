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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
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
	// Wire a real PlanEngine (mirrors gateway.go's boot wiring: planStore and
	// PlanEngine are always present together in production) so
	// handlePlanStop/handleTaskStop's delegation to PlanEngine.StopPlan/
	// StopTask (ADR-052) is reachable from every test using this helper,
	// without each one having to wire it individually (see
	// TestDeleteAgent_OwningActivePlan_Rejected for a case that also wires
	// its own local instance to hold onto the *PlanEngine handle — a second,
	// harmless SetPlanEngine call, not a conflict; both share the same
	// planStore/taskStore pointers).
	//
	// Not Start()ed: nothing here needs the tick/event loops. ⚠ That does NOT
	// make StopPlan synchronous — an earlier version of this comment claimed
	// "StopPlan/StopTask are synchronous store operations" and that was the
	// bug. StopPlan ends with wakeOwner, which for a plan with no chat origin
	// (every plan created through this REST harness has none) dispatches a
	// REAL agent turn on its own goroutine. Left undrained, that turn writes
	// its session transcript and state into tmpDir AFTER t.TempDir has removed
	// it — seen in CI as ".../state.json: no such file or directory" from a
	// test that had already passed.
	pe := agent.NewPlanEngine(al, api.planStore, api.taskStore, api.taskExecutor)
	api.agentLoop.SetPlanEngine(pe)
	// ORDER IS LOAD-BEARING, in two directions:
	//
	//  - Within this cleanup: pe.Stop() drains the WHOLE wake turn (wakeWG),
	//    of which WaitForActiveRequests only ever covered the LLM call itself
	//    (activeRequests is Add-ed inside runTurn around the provider call,
	//    loop.go, not around the turn). So Stop first, then wait. Neither is
	//    redundant: WaitForActiveRequests still covers turns this engine did
	//    not dispatch — the Run pump and ExecuteBoardTask — which pe.Stop()
	//    knows nothing about.
	//  - Against the other cleanups: t.Cleanup runs LIFO, so registering the
	//    drain HERE (after mustAgentLoop's al.Close above, and after
	//    t.TempDir's removal at the top of this function) is what makes it
	//    run BEFORE both of them — the turn must finish while the
	//    registry/session stores it writes through are still open and while
	//    tmpDir still exists. Registering it any later in this function is
	//    fine; hoisting it earlier, or moving it into a caller, silently
	//    inverts the order and restores the bug.
	t.Cleanup(func() {
		pe.Stop()
		api.agentLoop.WaitForActiveRequests()
	})
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

// TestPlanCreate_WhitespaceOnlyTitleRejected is a regression test for S2 UAT
// finding B: `title: "   \t  "` (whitespace-only) returned 201 and produced an
// unnamed, unfindable/unfilterable plan chip, because plan.Store's own
// required-field check is a plain `== ""` — never true for a non-empty string
// of only whitespace. The fix trims in the REST handler (handleWorkspacePlan
// Create/handlePlanPut) before the title ever reaches the store, so a
// whitespace-only title now hits the SAME "title is required" 400 an empty
// string already returned.
func TestPlanCreate_WhitespaceOnlyTitleRejected(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")

	body := `{"workspace_id":"` + wsID + `","title":"   \t  ","owner_agent_id":"` + testPlansAgentID + `"}`
	w := postPlan(t, api, wsID, body)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Contains(t, errResp.Error, "title is required")
}

// TestPlanCreate_TitleTrimmed proves the flip side of the same fix: a
// legitimate title with incidental leading/trailing whitespace is accepted
// (201, not 400) and persisted trimmed — not verbatim with the whitespace
// still attached, which would itself be "unfindable/unfilterable" by an exact
// title search.
func TestPlanCreate_TitleTrimmed(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")

	body := `{"workspace_id":"` + wsID + `","title":"  Launch v1  ","owner_agent_id":"` + testPlansAgentID + `"}`
	w := postPlan(t, api, wsID, body)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var created gen.Plan
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.Equal(t, "Launch v1", created.Title, "response title must be trimmed")

	wGet := getPlan(t, api, created.Id)
	require.Equal(t, http.StatusOK, wGet.Code)
	var got gen.Plan
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	assert.Equal(t, "Launch v1", got.Title, "persisted title must be trimmed")
}

// TestPlanPut_WhitespaceOnlyTitleRejectedAndTrimmed mirrors the create-path
// tests above for the PUT /plans/{id} update path (handlePlanPut has the same
// untrimmed-check gap on patch.Title).
func TestPlanPut_WhitespaceOnlyTitleRejectedAndTrimmed(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans WS")

	createBody := `{"workspace_id":"` + wsID + `","title":"Launch v1","owner_agent_id":"` + testPlansAgentID + `"}`
	wCreate := postPlan(t, api, wsID, createBody)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var created gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))

	// Whitespace-only PUT title -> 400.
	wBad := putPlan(t, api, created.Id, `{"title":"   \t  "}`)
	require.Equal(t, http.StatusBadRequest, wBad.Code, "body=%s", wBad.Body.String())
	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(wBad.Body.Bytes(), &errResp))
	assert.Contains(t, errResp.Error, "title must not be empty")

	// Legitimate PUT title with surrounding whitespace -> 200, stored trimmed.
	wGood := putPlan(t, api, created.Id, `{"title":"  Renamed Plan  "}`)
	require.Equal(t, http.StatusOK, wGood.Code, "body=%s", wGood.Body.String())
	var updated gen.Plan
	require.NoError(t, json.Unmarshal(wGood.Body.Bytes(), &updated))
	assert.Equal(t, "Renamed Plan", updated.Title)

	wGet := getPlan(t, api, created.Id)
	require.Equal(t, http.StatusOK, wGet.Code)
	var got gen.Plan
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	assert.Equal(t, "Renamed Plan", got.Title, "persisted title must be trimmed")
}

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
// a draft plan and succeeds (running->failed(stopped_by_user)) once the plan
// is running. Stop ALSO succeeds on a cap-queued `approved` plan (ADR-052
// spec Edge Case "Stop wins", gap-sweep fix-wave-2 finding #1) — see
// TestPlanStop_OnApprovedCapQueued_MembersUntouchedAndRestartable
// (rest_plan_task_restart_test.go) for that dedicated coverage; this test's
// own draft-rejection case is unaffected since draft is neither running nor
// approved.
func TestPlanStop_RequiresRunning(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Stop WS")

	// Every status assertion below carries the response body. This test failed
	// once on ci-omnipus-2 (@670a8c0c) and was undiagnosable after the fact:
	// the CI flake filter keeps only the "--- FAIL" header line, and the bare
	// assertions here named no body, so which of the four calls broke — and
	// why — could not be recovered from the run log. Keep the bodies.
	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Stoppable","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code, "create body=%s", wCreate.Body.String())
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	// Stop on a draft plan -> 400.
	wStopDraft := postPlanAction(t, api, p.Id, "stop")
	assert.Equal(t, http.StatusBadRequest, wStopDraft.Code,
		"draft stop body=%s", wStopDraft.Body.String())

	// draft -> approved (soft tier, empty DoD, no members).
	wApprove := postPlanAction(t, api, p.Id, "approve")
	require.Equal(t, http.StatusOK, wApprove.Code, "approve body=%s", wApprove.Body.String())

	// approved -> running (legal transition; the engine would normally do
	// this on its next tick, but this test drives the transition directly
	// via the store to isolate the stop endpoint — PUT can no longer set
	// state at all, ADR-052 FR-007/A1, see TestPlanPut_RejectsAnyStateField).
	running := plan.StateRunning
	_, rerr := api.planStore.Update(p.Id, plan.Patch{State: &running})
	require.NoError(t, rerr)

	wStop := postPlanAction(t, api, p.Id, "stop")
	require.Equal(t, http.StatusOK, wStop.Code, "body=%s", wStop.Body.String())
	var stopped gen.Plan
	require.NoError(t, json.Unmarshal(wStop.Body.Bytes(), &stopped))
	assert.Equal(t, gen.PlanStateFailed, stopped.State)
	require.NotNil(t, stopped.FailedReason)
	assert.Equal(t, gen.PlanFailedReasonStoppedByUser, *stopped.FailedReason)
}

// TestPlanGet_WirePlanPhaseStalled is the wire-visibility half of the
// swimlane-board UAT fix (backend detection lives in
// pkg/agent/plan_engine.go's surfaceStallIfAny/PhaseStalled): GET
// /api/v1/plans/{id} must emit plan_phase: "stalled" once the plan engine
// has persisted it, so the SPA's planPhaseChip can render a distinct chip
// instead of an indistinguishable "Running 0/N". toWirePlan itself needs no
// code change for this — it already forwards EffectivePlanPhase() verbatim
// (rest_plans.go:163-164) — this test proves that generic forwarding
// actually carries the new enum value end to end through the generated
// wire type, not just in principle.
func TestPlanGet_WirePlanPhaseStalled(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Stalled WS")

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Stalled plan","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	wApprove := postPlanAction(t, api, p.Id, "approve")
	require.Equal(t, http.StatusOK, wApprove.Code, "body=%s", wApprove.Body.String())

	running := plan.StateRunning
	stalled := plan.PhaseStalled
	handover := "[stalled] This plan has no dispatchable or in-flight members, so it cannot make progress right now."
	_, uerr := api.planStore.Update(p.Id, plan.Patch{State: &running, PlanPhase: &stalled, HandoverText: &handover})
	require.NoError(t, uerr)

	wGet := getPlan(t, api, p.Id)
	require.Equal(t, http.StatusOK, wGet.Code)
	var got gen.Plan
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	require.NotNil(t, got.PlanPhase)
	assert.Equal(t, gen.PlanPlanPhaseStalled, *got.PlanPhase)

	// HandoverText is server-side only (never wire-exposed) — the raw JSON
	// must not leak it. It names internal task IDs meant for the owner
	// agent's chat turn, not for a REST client.
	assert.NotContains(t, wGet.Body.String(), "handover_text",
		"HandoverText must stay off the wire — it's for the owner agent's chat turn, not REST clients")
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
	// This SECOND engine displaces the harness's own in the loop, so the
	// harness cleanup's pe.Stop() drains that one, not this one. Drain this
	// one too — it is a live engine bound to the same stores and the same
	// tmpDir, and any wake it dispatches outlives the test otherwise.
	// Registered here (later than the harness's) so it runs first; Stop on an
	// engine that dispatched nothing is a zero-counter wait.
	t.Cleanup(pe.Stop)

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Owned plan","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	require.Equal(t, http.StatusOK, postPlanAction(t, api, p.Id, "approve").Code)
	// PUT can no longer set state at all (ADR-052 FR-007/A1) — drive the
	// approved->running transition directly via the store instead.
	running := plan.StateRunning
	_, rerr := api.planStore.Update(p.Id, plan.Patch{State: &running})
	require.NoError(t, rerr)

	hasActive, haErr := pe.HasActivePlansOwnedBy(testPlansAgentID)
	require.NoError(t, haErr)
	assert.True(t, hasActive)

	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+testPlansAgentID, nil)
	rDel.URL.Path = "/api/v1/agents/" + testPlansAgentID
	api.HandleAgents(wDel, rDel)
	assert.Equal(t, http.StatusBadRequest, wDel.Code, "body=%s", wDel.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(wDel.Body.Bytes(), &body))
	assert.Equal(t, "agent_owns_active_plans", body["code"])
}

// TestDeleteAgent_PlanStoreListError_FailsClosed is the fix-wave regression
// for finding 1 (14-reviewer sign-off): when HasActivePlansOwnedBy cannot
// determine plan ownership (a plan-store List() error), the delete-guard
// must refuse the delete (503) rather than silently treating "unknown" as
// "no active plans" and letting a possibly-owning agent be deleted out from
// under a live plan.
func TestDeleteAgent_PlanStoreListError_FailsClosed(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Owner WS 2")

	pe := agent.NewPlanEngine(api.agentLoop, api.planStore, api.taskStore, api.taskExecutor)
	api.agentLoop.SetPlanEngine(pe)
	t.Cleanup(pe.Stop)

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Owned plan 2","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))
	require.Equal(t, http.StatusOK, postPlanAction(t, api, p.Id, "approve").Code)
	running := plan.StateRunning
	_, rerr := api.planStore.Update(p.Id, plan.Patch{State: &running})
	require.NoError(t, rerr)

	// Force plan.Store.List to fail with a genuine (non-ENOENT) read error,
	// UID-independently: root (this repo's CI worker runs as uid=0) has
	// CAP_DAC_OVERRIDE and ignores permission bits entirely, so an
	// os.Chmod(dir, 0o000) injection succeeds in listing anyway on CI while
	// failing correctly on a non-root dev machine — a root-only false RED,
	// not a real product bug (mirrors the os.Getuid()==0 skip-guard pattern
	// used elsewhere, e.g. pkg/tools/write_file_reason_test.go,
	// pkg/agent/list_all_sessions_test.go). Swap the plans directory for a
	// regular file instead: os.ReadDir on a path that is not a directory
	// returns ENOTDIR unconditionally, root included, because it is a
	// path-type error rather than a DAC permission check.
	dir := api.planStore.Dir()
	backupDir := dir + ".bak"
	require.NoError(t, os.Rename(dir, backupDir))
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))
	t.Cleanup(func() {
		_ = os.Remove(dir)
		_ = os.Rename(backupDir, dir)
	})

	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+testPlansAgentID, nil)
	rDel.URL.Path = "/api/v1/agents/" + testPlansAgentID
	api.HandleAgents(wDel, rDel)
	assert.Equal(t, http.StatusServiceUnavailable, wDel.Code, "body=%s", wDel.Body.String())
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
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
	ok, active, capOut := pe.Admit("plan")
	assert.True(t, ok)
	assert.Equal(t, 0, active)
	assert.Positive(t, capOut)

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

	// wantErr pins the EXACT distinguishing substring validatePlanOwnerAgent
	// (rest_plans.go) produces for each cause, surfaced verbatim on the wire
	// via jsonErr(w, http.StatusBadRequest, err.Error()) at both the POST and
	// PUT call sites. Without this, all three subtests below assert only 400,
	// but validatePlanOwnerAgent has exactly TWO distinct rejection messages
	// ("is not a registered agent" for both the genuinely-unregistered ID AND
	// a nil-cfg edge case; "is a System Agent or worker and cannot own a
	// plan" for a real-but-non-chat-target agent) — a regression that, say,
	// dropped the IsChatTarget() check and let the registry lookup treat
	// System/Worker agents as "not found" (e.g. a registry that filters them
	// out) would still 400 every subtest here, without ever exercising the
	// system/worker-specific branch two of the three subtests exist to check.
	cases := []struct {
		name    string
		ownerID string
		wantErr string
	}{
		{"unregistered", "nonexistent-agent-id", "is not a registered agent"},
		{"system_agent", "judge-agent", "is a System Agent or worker and cannot own a plan"},
		{"worker_agent", "worker-agent", "is a System Agent or worker and cannot own a plan"},
	}
	for _, tc := range cases {
		t.Run("create_"+tc.name, func(t *testing.T) {
			w := postPlan(t, api, wsID,
				`{"workspace_id":"`+wsID+`","title":"Bad Owner `+tc.name+`","owner_agent_id":"`+tc.ownerID+`"}`)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.wantErr,
				"the 400 must name the specific owner_agent_id rejection reason, not a masked generic one")
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
			assert.Contains(t, w.Body.String(), tc.wantErr,
				"the 400 must name the specific owner_agent_id rejection reason, not a masked generic one")
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

// --- The harness's own teardown contract ------------------------------------

// blockingWakeProvider holds a turn open inside the LLM call until the test
// releases it. A call to it is positive evidence that a REAL turn ran for the
// agent it is installed on — not that a dispatch was recorded somewhere.
type blockingWakeProvider struct {
	mu      sync.Mutex
	entered chan struct{} // closed on the first call
	closed  bool
	gate    chan struct{} // the call blocks until this closes
}

func newBlockingWakeProvider() *blockingWakeProvider {
	return &blockingWakeProvider{entered: make(chan struct{}), gate: make(chan struct{})}
}

func (p *blockingWakeProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.entered)
	}
	p.mu.Unlock()

	select {
	case <-p.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &providers.LLMResponse{Content: "acknowledged"}, nil
}

func (p *blockingWakeProvider) GetDefaultModel() string { return "test-model" }

// TestPlanStopREST_DispatchesARealOwnerWakeTurn pins the fact that makes
// newTestRestAPIWithPlans's teardown contract necessary: POST /plans/{id}/stop
// is NOT a synchronous store operation. It reaches PlanEngine.StopPlan, which
// ends in wakeOwner, and because every plan created through this REST harness
// has NO chat origin, wakeOwner takes the direct-dispatch leg — a REAL agent
// turn on its own goroutine, writing a real session transcript into tmpDir.
// The harness comment used to assert the opposite; that assertion is what let
// the undrained turn ship.
//
// ⚠ WHAT THIS TEST DOES NOT PROVE. It does NOT discriminate the harness's
// pe.Stop() drain, and it must not be read as if it did. Verified by mutation:
// with pe.Stop() removed from the cleanup this test is 5/5 GREEN. The reason is
// structural — activeRequests is Add-ed inside runTurn around the PROVIDER CALL
// (loop.go), which is exactly where the gate below holds the turn, so
// WaitForActiveRequests alone covers the held window. The windows pe.Stop()
// actually adds are the ones this test cannot hold open from outside:
//
//   - goroutine start -> the activeRequests.Add (session resolution, context
//     build, memory recall — all reading and writing tmpDir), and
//   - the provider call returning -> goroutine exit (persisting the assistant
//     message, session state, cost — the ".../state.json: no such file or
//     directory" write in the CI log).
//
// Holding either open needs a seam inside the turn that does not exist and is
// not worth adding. The deterministic drain proof therefore lives one layer
// down, in pkg/agent's TestPlanEngineStop_DrainsWakeTurnDispatchedByNeverStarted
// Engine, which calls Stop() directly instead of through a cleanup that
// WaitForActiveRequests also happens to cover.
//
// So the honest claims here are: (1) the stop endpoint really does start an
// agent turn, (2) teardown does not abandon it mid-LLM-call, and (3) nothing
// survives teardown.
func TestPlanStopREST_DispatchesARealOwnerWakeTurn(t *testing.T) {
	noLeaks := []goleak.Option{
		goleak.IgnoreCurrent(),
		// The memory subsystem opens its bleve index lazily, INSIDE the turn,
		// so these appear after the snapshot. They belong to a
		// process-lifetime index the memory store owns and closes — outside
		// any plan-engine teardown contract. (replay_test.go excludes the
		// same family for the same reason.)
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve/v2/index/scorch.(*Scorch).introducerLoop"),
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve/v2/index/scorch.(*Scorch).persisterLoop"),
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve/v2/index/scorch.(*Scorch).mergerLoop"),
	}

	const hold = 300 * time.Millisecond
	prov := newBlockingWakeProvider()
	var released atomic.Bool
	// Belt and braces: if any require/Fatal below aborts the subtest before
	// the releaser is armed, the gate still opens so teardown cannot wedge.
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(prov.gate) }) }
	t.Cleanup(release)

	start := time.Now()
	t.Run("stop a running plan through the real REST path", func(t *testing.T) {
		api := newTestRestAPIWithPlans(t)

		// Install the blocking provider on the plan's OWNER, so the only turn
		// that can block here is the owner wake this test is about.
		inst, ok := api.agentLoop.GetRegistry().GetAgent(testPlansAgentID)
		require.True(t, ok, "the harness agent must resolve; the wake dispatch pre-resolves it")
		inst.Provider = prov

		// FIXED upstream: testHarnessAgentIDs (test_agent_loop_helper_test.go)
		// now normalizes every id via routing.NormalizeAgentID before seeding,
		// exactly matching what the registry keys agents under — so
		// newTestRestAPIWithPlans's own mustAgentLoop call already seeds
		// testPlansAgentID correctly (lower-cased) and this explicit call is a
		// no-op (seedTestWorkspaceMembershipForIDs's toSeed loop finds the id
		// already resolves via workspace.FindForAgent). Kept as a harmless,
		// explicit belt-and-braces re-assertion — and because
		// seedTestWorkspaceMembershipForIDs now guards its own postcondition,
		// this line would fail LOUD (not silently refuse the turn) if the
		// upstream normalization ever regressed.
		seedTestWorkspaceMembershipForIDs(t, []string{strings.ToLower(testPlansAgentID)})

		wsID := createTestWorkspace(t, api, "Wake Drain WS")
		wCreate := postPlan(t, api, wsID,
			`{"workspace_id":"`+wsID+`","title":"Wake drain","owner_agent_id":"`+testPlansAgentID+`"}`)
		require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
		var p gen.Plan
		require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

		require.Equal(t, http.StatusOK, postPlanAction(t, api, p.Id, "approve").Code)
		// PUT can no longer set state (ADR-052 FR-007/A1) — drive
		// approved->running through the store, as TestDeleteAgent does.
		running := plan.StateRunning
		_, rerr := api.planStore.Update(p.Id, plan.Patch{State: &running})
		require.NoError(t, rerr)

		wStop := postPlanAction(t, api, p.Id, "stop")
		require.Equal(t, http.StatusOK, wStop.Code, "body=%s", wStop.Body.String())

		// CLAIM (1): a real agent turn ran for the plan's owner. Positive
		// evidence — the owner's OWN provider was entered — not a recorded
		// dispatch. This is the limb that fails if StopPlan ever stops waking
		// the owner, or if the harness's plan is given a chat origin (which
		// would reroute the wake to the notifier/bus leg instead).
		select {
		case <-prov.entered:
		case <-time.After(10 * time.Second):
			t.Fatal("POST /plans/{id}/stop started no owner wake turn — the harness's teardown " +
				"drain would be guarding nothing, and the endpoint no longer does what " +
				"ADR-052's wakeOwner says it does")
		}

		go func() {
			time.Sleep(hold)
			released.Store(true)
			release()
		}()
	})
	// Every cleanup newTestRestAPIWithPlans registered — pe.Stop(),
	// WaitForActiveRequests, al.Close, and t.TempDir's RemoveAll — has run by
	// the time t.Run returns.
	elapsed := time.Since(start)

	// CLAIM (2): teardown did not abandon the turn mid-LLM-call. Note honestly
	// that WaitForActiveRequests alone satisfies this — activeRequests spans
	// exactly the provider call — so this limb is a guard against teardown
	// losing BOTH waits, not evidence for the pe.Stop() drain.
	if !released.Load() {
		t.Fatalf("the harness teardown finished in %v while the wake turn dispatched by "+
			"POST /plans/{id}/stop was still inside its LLM call — teardown is abandoning "+
			"in-flight turns over a t.TempDir() it is about to remove", elapsed)
	}
	if elapsed < hold {
		t.Fatalf("teardown returned after %v but the turn was held for %v", elapsed, hold)
	}
	// CLAIM (3): nothing survived teardown.
	goleak.VerifyNone(t, noLeaks...)
}
