// rest_tasks_plan_gate_test.go — REST-layer coverage for the S1 plan-state
// gate's REST bypass (bc66345f placed the gate in CheckQueuedTasks only, one
// level above the ExecuteTask/StartTaskNow primitives every dispatch path
// funnels through — see pkg/agent/task_executor.go's requirePlanExecuting).
//
// Before the primitive-level fix, PATCH /api/v1/tasks/{id} with
// {"status":"in_progress"} tested only the TASK's own status transition
// (rest_tasks.go's handleTaskPatch launch block) and called
// TaskExecutor.StartTaskNow unconditionally — never inspecting the task's
// PlanID or its parent plan's lifecycle state at all. Dragging a plan
// member's Kanban card from Inbox straight to "In Progress" (one column
// further than the "Inbox straight to Next" scenario the original S1 fix's
// own doc comment cites as its motivating case) bypassed the Execute
// approval gate entirely — the human never clicked Execute, but the REST
// PATCH launched the agent anyway. requirePlanExecuting being enforced
// INSIDE StartTaskNow itself (task_executor.go) is what closes this: it has
// no bypass (unlike ExecuteTask's executeTaskPlanVerified for the plan
// engine's own dispatch), so every caller — this REST handler included —
// pays the real check.
package gateway

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/plan"
)

// wirePlanStore installs a real *plan.Store on both api.planStore (the REST
// seam validateTaskPlanID reads) and api.taskExecutor's own plan store (the
// primitive-level gate requirePlanExecuting reads) — mirroring gateway.go's
// production boot wiring, where SetPlanStore is called on both AgentLoop and
// TaskExecutor with the SAME store value (see TaskExecutor.SetPlanStore's own
// doc comment).
func wirePlanStore(t *testing.T, api *restAPI) *plan.Store {
	t.Helper()
	planStore := plan.New(filepath.Join(api.homePath, "plans"))
	api.planStore = planStore
	require.NotNil(t, api.taskExecutor, "wirePlanStore: taskExecutor must be wired first")
	api.taskExecutor.SetPlanStore(planStore)
	return planStore
}

// makeTestPlan creates and persists a plan in the given state, owned by
// "main" (the always-registered default agent sentinel this test file's
// harness assigns tasks to) on workspaceID.
func makeTestPlan(t *testing.T, planStore *plan.Store, workspaceID string, state plan.State) *plan.Plan {
	t.Helper()
	p := &plan.Plan{
		Title:        "plan gate REST test plan",
		WorkspaceID:  workspaceID,
		OwnerAgentID: "main",
		State:        state,
	}
	require.NoError(t, planStore.Create(p), "create plan (state=%s)", state)
	return p
}

// assignPlanMemberTask creates a `next` task, assigns agent_id="main", and
// attaches it to planID via PATCH plan_id — the same sequence a real Kanban
// board / task-detail panel drives through the REST API.
func assignPlanMemberTask(t *testing.T, api *restAPI, title, workspaceID, planID string) gen.Task {
	t.Helper()
	tsk := createTaskViaAPI(t, api, title, workspaceID)
	advanceTaskToNext(t, api, tsk.Id)

	wAssign := patchTask(t, api, tsk.Id, `{"agent_id":"main"}`)
	require.Equal(t, 200, wAssign.Code, "assign agent_id=main must return 200; body=%s", wAssign.Body.String())

	wPlan := patchTask(t, api, tsk.Id, fmt.Sprintf(`{"plan_id":%q}`, planID))
	require.Equal(t, 200, wPlan.Code, "assign plan_id must return 200; body=%s", wPlan.Body.String())

	final := getTaskStatus(t, api, tsk.Id)
	require.Equal(t, gen.TaskStatusNext, final, "task must still be next before the in_progress PATCH under test")
	return tsk
}

// TestHandleTaskPatch_InProgress_DraftPlanMember_DoesNotLaunch is DoD item 1:
// a REST PATCH to in_progress on a DRAFT plan's member must NOT launch the
// agent. Root-cause repro: a Kanban drag straight to "In Progress" on a plan
// whose Execute button was never clicked.
func TestHandleTaskPatch_InProgress_DraftPlanMember_DoesNotLaunch(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"main"})
	planStore := wirePlanStore(t, api)

	p := makeTestPlan(t, planStore, wsID, plan.StateDraft)
	tsk := assignPlanMemberTask(t, api, "DraftPlanMemberTask", wsID, p.ID)

	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	assert.Equal(t, 409, w.Code,
		"PATCH to in_progress on a DRAFT plan's member must be rejected 409, not launch the agent; body=%s",
		w.Body.String())

	statusAfter := getTaskStatus(t, api, tsk.Id)
	assert.Equal(t, gen.TaskStatusNext, statusAfter,
		"task must revert to next — a draft plan's member must never reach in_progress via REST")

	// Confirm no session was ever created (no agent launch happened at all).
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest("GET", "/api/v1/tasks/"+tsk.Id, nil)
	getR.URL.Path = "/api/v1/tasks/" + tsk.Id
	api.HandleTasks(getW, getR)
	require.Equal(t, 200, getW.Code)
	var fetched gen.Task
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &fetched))
	assert.Nil(t, fetched.SessionId, "no session_id must have been created for a rejected launch")

	// The plan itself must be untouched.
	gotPlan, err := planStore.Get(p.ID)
	require.NoError(t, err)
	assert.Equal(t, plan.StateDraft, gotPlan.State, "plan state must be unchanged")
}

// TestHandleTaskPatch_InProgress_StoppedPlanMember_DoesNotLaunch mirrors the
// Draft case for a plan the user already Stopped (terminal
// failed(stopped_by_user)) — the PRIYA-D8-race scenario's REST-layer sibling.
func TestHandleTaskPatch_InProgress_StoppedPlanMember_DoesNotLaunch(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"main"})
	planStore := wirePlanStore(t, api)

	p := makeTestPlan(t, planStore, wsID, plan.StateFailed)
	tsk := assignPlanMemberTask(t, api, "StoppedPlanMemberTask", wsID, p.ID)

	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	assert.Equal(t, 409, w.Code,
		"PATCH to in_progress on a STOPPED (failed) plan's member must be rejected 409; body=%s",
		w.Body.String())

	statusAfter := getTaskStatus(t, api, tsk.Id)
	assert.Equal(t, gen.TaskStatusNext, statusAfter, "task must revert to next")
}

// TestHandleTaskPatch_InProgress_PausedRunningPlanMember_DoesNotLaunch is the
// REST-layer sibling of the paused-plan gate follow-up (FR-065): a plan that
// is State==running but PausedReason != "" must not let its member launch
// via REST either — State alone is not sufficient (see
// plan.Plan.PermitsMemberDispatch's doc).
func TestHandleTaskPatch_InProgress_PausedRunningPlanMember_DoesNotLaunch(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"main"})
	planStore := wirePlanStore(t, api)

	p := makeTestPlan(t, planStore, wsID, plan.StateRunning)
	pausedReason := "owner_disabled"
	_, err := planStore.Update(p.ID, plan.Patch{PausedReason: &pausedReason})
	require.NoError(t, err, "pause plan")

	tsk := assignPlanMemberTask(t, api, "PausedPlanMemberTask", wsID, p.ID)

	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	assert.Equal(t, 409, w.Code,
		"PATCH to in_progress on a PAUSED running plan's member must be rejected 409; body=%s",
		w.Body.String())

	statusAfter := getTaskStatus(t, api, tsk.Id)
	assert.Equal(t, gen.TaskStatusNext, statusAfter, "task must revert to next")
}

// TestHandleTaskPatch_InProgress_ApprovedPlanMember_StillLaunches proves the
// gate is not over-broad: an Approved (or Running, unpaused) plan's member
// must still be allowed to launch via REST exactly as before this fix.
func TestHandleTaskPatch_InProgress_ApprovedPlanMember_StillLaunches(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"main"})
	planStore := wirePlanStore(t, api)

	p := makeTestPlan(t, planStore, wsID, plan.StateApproved)
	tsk := assignPlanMemberTask(t, api, "ApprovedPlanMemberTask", wsID, p.ID)

	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, 200, w.Code,
		"PATCH to in_progress on an APPROVED plan's member must succeed; body=%s", w.Body.String())

	var updated gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, gen.TaskStatusInProgress, updated.Status)

	taskID := tsk.Id
	t.Cleanup(func() {
		require.Eventually(t, func() bool {
			s := getTaskStatus(t, api, taskID)
			return s == gen.TaskStatusDone || s == gen.TaskStatusFailed
		}, 10*time.Second, 20*time.Millisecond, "task goroutine must reach a terminal state before test teardown")
	})
}
