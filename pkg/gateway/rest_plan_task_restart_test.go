//go:build !cgo

// rest_plan_task_restart_test.go — ADR-052 Wave 2 "rest" agent tests:
//
//   - FR-007/A1: PUT /plans/{id} rejects ANY request whose body carries a
//     "state" key (DS-3) — every plan state transition goes through a
//     dedicated endpoint.
//   - FR-016/017/026: POST /plans/{id}/restart and POST /tasks/{id}/restart
//     (the ▶ Play routes) — the reason-gated failed[stopped_by_user]->approved
//     transition, non-done member reset (any-reason failed->next), and the
//     standalone-task restart's own stopped_by_user reason gate.
//   - FR-009/010/025: the Stop rewiring onto PlanEngine.StopPlan/StopTask,
//     including the partial-fan-out-failure surface (ADR-052 §6.4 Item 5) and
//     member-Stop leaving plan siblings untouched (US-7/A5).
//
// Traces to: docs/internal/architecture/ADR-052-autonomous-agent-plan-execution.md
//            docs/internal/specs/autonomous-agent-plan-execution-spec.md

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// postTaskAction issues POST /api/v1/tasks/{id}/{action} and returns the recorder.
func postTaskAction(t *testing.T, api *restAPI, id, action string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+id+"/"+action, nil)
	r.URL.Path = "/api/v1/tasks/" + id + "/" + action
	api.HandleTasks(w, r)
	return w
}

// mustCreateTask creates a task (with an optional plan_id, "" for standalone)
// via the real create path and returns its ID.
func mustCreateTask(t *testing.T, api *restAPI, wsID, title, planID string) string {
	t.Helper()
	body := `{"workspace_id":"` + wsID + `","title":"` + title + `"`
	if planID != "" {
		body += `,"plan_id":"` + planID + `"`
	}
	body += `}`
	w := postTask(t, api, body)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	return created.Id
}

// drivePlanToRunning walks a draft plan through the legal approved->running
// transitions directly via the store (isolates the handler under test from
// the approve gate / engine tick loop, mirroring TestPlanStop_RequiresRunning).
func drivePlanToRunning(t *testing.T, api *restAPI, id string) {
	t.Helper()
	approved := plan.StateApproved
	_, err := api.planStore.Update(id, plan.Patch{State: &approved})
	require.NoError(t, err)
	running := plan.StateRunning
	_, err = api.planStore.Update(id, plan.Patch{State: &running})
	require.NoError(t, err)
}

// drivePlanToFailed walks a draft plan through approved->running->failed(reason)
// directly via the store.
func drivePlanToFailed(t *testing.T, api *restAPI, id string, reason plan.FailedReason) {
	t.Helper()
	drivePlanToRunning(t, api, id)
	failed := plan.StateFailed
	_, err := api.planStore.Update(id, plan.Patch{State: &failed, FailedReason: &reason})
	require.NoError(t, err)
}

// --- FR-007/A1: PUT /plans/{id} rejects ANY state field (DS-3) --------------

// TestPlanPut_RejectsAnyStateField exercises the DS-3 table: every one of the
// five plan states passed via PUT is rejected 400, a non-state field still
// applies normally, and a mixed request (a legitimate field alongside state)
// rejects the WHOLE request rather than partially applying it.
func TestPlanPut_RejectsAnyStateField(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "PUT Lockdown WS")

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Locked down","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	for _, state := range []string{"draft", "approved", "running", "done", "failed"} {
		t.Run("state="+state, func(t *testing.T) {
			w := putPlan(t, api, p.Id, `{"state":"`+state+`"}`)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

			reloaded, gerr := api.planStore.Get(p.Id)
			require.NoError(t, gerr)
			assert.Equal(t, plan.StateDraft, reloaded.State, "state must be unchanged after a rejected PUT")
		})
	}

	// Non-state fields still succeed.
	wTitle := putPlan(t, api, p.Id, `{"title":"Renamed"}`)
	require.Equal(t, http.StatusOK, wTitle.Code, "body=%s", wTitle.Body.String())
	var afterTitle gen.Plan
	require.NoError(t, json.Unmarshal(wTitle.Body.Bytes(), &afterTitle))
	assert.Equal(t, "Renamed", afterTitle.Title)

	wGoal := putPlan(t, api, p.Id, `{"goal":"Ship it"}`)
	require.Equal(t, http.StatusOK, wGoal.Code, "body=%s", wGoal.Body.String())

	// A present state field rejects the WHOLE request, even alongside a
	// legitimate field and even when the state value equals the current one.
	wMixed := putPlan(t, api, p.Id, `{"title":"Should not apply","state":"draft"}`)
	assert.Equal(t, http.StatusBadRequest, wMixed.Code, "body=%s", wMixed.Body.String())
	reloaded, gerr := api.planStore.Get(p.Id)
	require.NoError(t, gerr)
	assert.Equal(t, "Renamed", reloaded.Title, "title must NOT have been updated by the rejected mixed request")
}

// --- FR-016/017/026: POST /plans/{id}/restart --------------------------------

// TestPlanRestart_HappyPath_ResetsNonDoneMembersAndReenters covers DS-5: a
// done member is preserved untouched, a cancelled member is reset to
// next/attempt_count=0/cancel_reason cleared, and the plan itself re-enters
// at approved (never running directly) with failed_reason cleared.
func TestPlanRestart_HappyPath_ResetsNonDoneMembersAndReenters(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Restart Happy WS")

	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Restartable","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	doneID := mustCreateTask(t, api, wsID, "already done", p.Id)
	doneStatus := task.StatusDone
	_, err := api.taskStore.Update(doneID, task.Patch{Status: &doneStatus})
	require.NoError(t, err)

	cancelledID := mustCreateTask(t, api, wsID, "was cancelled", p.Id)
	failedStatus := task.StatusFailed
	cancelReason := task.CancelReasonStoppedByUser
	attempts := 2
	_, err = api.taskStore.Update(cancelledID, task.Patch{
		Status:       &failedStatus,
		CancelReason: &cancelReason,
		AttemptCount: &attempts,
	})
	require.NoError(t, err)

	drivePlanToFailed(t, api, p.Id, plan.FailedReasonStoppedByUser)

	wRestart := postPlanAction(t, api, p.Id, "restart")
	require.Equal(t, http.StatusOK, wRestart.Code, "body=%s", wRestart.Body.String())
	var restarted gen.Plan
	require.NoError(t, json.Unmarshal(wRestart.Body.Bytes(), &restarted))
	assert.Equal(t, gen.PlanStateApproved, restarted.State, "restart re-enters at approved, never running directly")
	assert.Nil(t, restarted.FailedReason, "failed_reason must be cleared on restart")

	reloadedDone, derr := api.taskStore.Get(doneID)
	require.NoError(t, derr)
	assert.Equal(t, task.StatusDone, reloadedDone.Status, "a done member must be preserved untouched")

	reloadedCancelled, cerr := api.taskStore.Get(cancelledID)
	require.NoError(t, cerr)
	assert.Equal(t, task.StatusNext, reloadedCancelled.Status, "a cancelled member resets to next")
	assert.Equal(t, 0, reloadedCancelled.AttemptCount, "attempt_count resets to 0")
	assert.Empty(t, reloadedCancelled.CancelReason, "cancel_reason clears on restart")
}

// TestPlanRestart_RejectsGenuineFailure409 verifies the reason-guard: a plan
// failed for a reason OTHER than stopped_by_user (judge_rounds_exhausted /
// idle_expired) is frozen — no Play offered — and the restart call is 409.
func TestPlanRestart_RejectsGenuineFailure409(t *testing.T) {
	for _, reason := range []plan.FailedReason{plan.FailedReasonJudgeRoundsExhausted, plan.FailedReasonIdleExpired} {
		t.Run(string(reason), func(t *testing.T) {
			api := newTestRestAPIWithPlans(t)
			wsID := createTestWorkspace(t, api, "Restart Genuine Failure WS")
			wCreate := postPlan(t, api, wsID,
				`{"workspace_id":"`+wsID+`","title":"Not restartable","owner_agent_id":"`+testPlansAgentID+`"}`)
			require.Equal(t, http.StatusCreated, wCreate.Code)
			var p gen.Plan
			require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

			drivePlanToFailed(t, api, p.Id, reason)

			wRestart := postPlanAction(t, api, p.Id, "restart")
			assert.Equal(t, http.StatusConflict, wRestart.Code, "body=%s", wRestart.Body.String())

			reloaded, gerr := api.planStore.Get(p.Id)
			require.NoError(t, gerr)
			assert.Equal(t, plan.StateFailed, reloaded.State, "a genuinely-failed plan must stay frozen")
			assert.Equal(t, reason, reloaded.FailedReason)
		})
	}
}

// TestPlanRestart_RejectsNonFailedState409 verifies a plan that is not
// currently `failed` at all (e.g. still draft) cannot be restarted.
func TestPlanRestart_RejectsNonFailedState409(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Restart Non-Failed WS")
	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Still draft","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	wRestart := postPlanAction(t, api, p.Id, "restart")
	assert.Equal(t, http.StatusConflict, wRestart.Code, "body=%s", wRestart.Body.String())
}

// TestPlanRestart_NotFound404 verifies restarting an unknown plan ID 404s.
func TestPlanRestart_NotFound404(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	w := postPlanAction(t, api, "01JXNOSUCHPLAN00000000001", "restart")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- FR-026: POST /tasks/{id}/restart (standalone only) ---------------------

// TestTaskRestart_StandaloneHappyPath verifies a user-cancelled standalone
// task resets to next with attempt_count 0 and cancel_reason cleared.
func TestTaskRestart_StandaloneHappyPath(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Task Restart Happy WS")
	taskID := mustCreateTask(t, api, wsID, "standalone cancelled", "")

	failedStatus := task.StatusFailed
	cancelReason := task.CancelReasonStoppedByUser
	attempts := 1
	_, err := api.taskStore.Update(taskID, task.Patch{
		Status:       &failedStatus,
		CancelReason: &cancelReason,
		AttemptCount: &attempts,
	})
	require.NoError(t, err)

	w := postTaskAction(t, api, taskID, "restart")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var restarted gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &restarted))
	assert.Equal(t, gen.TaskStatusNext, restarted.Status)
	assert.Nil(t, restarted.CancelReason)

	reloaded, gerr := api.taskStore.Get(taskID)
	require.NoError(t, gerr)
	assert.Equal(t, task.StatusNext, reloaded.Status)
	assert.Equal(t, 0, reloaded.AttemptCount)
	assert.Empty(t, reloaded.CancelReason)
}

// TestTaskRestart_InPlanRejected409 verifies a plan-member task cannot be
// restarted via the standalone route — the plan's own restart re-runs it.
func TestTaskRestart_InPlanRejected409(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Task Restart In-Plan WS")
	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Owning plan","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	memberID := mustCreateTask(t, api, wsID, "plan member", p.Id)
	failedStatus := task.StatusFailed
	cancelReason := task.CancelReasonStoppedByUser
	_, err := api.taskStore.Update(memberID, task.Patch{Status: &failedStatus, CancelReason: &cancelReason})
	require.NoError(t, err)

	w := postTaskAction(t, api, memberID, "restart")
	assert.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
}

// TestTaskRestart_WrongReasonRejected409 verifies a GENUINELY failed
// standalone task (no cancel_reason — e.g. attempts exhausted) is not
// restartable via this endpoint (contracts/openapi.yaml's restartTask is
// reason-gated to stopped_by_user, unlike a plan's any-reason member reset).
func TestTaskRestart_WrongReasonRejected409(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Task Restart Wrong Reason WS")
	taskID := mustCreateTask(t, api, wsID, "genuinely failed", "")

	failedStatus := task.StatusFailed
	_, err := api.taskStore.Update(taskID, task.Patch{Status: &failedStatus})
	require.NoError(t, err)

	w := postTaskAction(t, api, taskID, "restart")
	assert.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
}

// TestTaskRestart_NotFound404 verifies restarting an unknown task ID 404s.
func TestTaskRestart_NotFound404(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	w := postTaskAction(t, api, "01JXNOSUCHTASK000000000001", "restart")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- FR-009/010: POST /plans/{id}/stop now delegates to PlanEngine.StopPlan -

// TestPlanStop_DelegatesToEngineFanOut verifies the happy path: every
// in_progress member is cancelled (failed + cancel_reason=stopped_by_user)
// and the plan itself transitions to failed(stopped_by_user).
func TestPlanStop_DelegatesToEngineFanOut(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Stop Fanout WS")
	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Fan-out me","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	m1 := mustCreateTask(t, api, wsID, "member 1", p.Id)
	m2 := mustCreateTask(t, api, wsID, "member 2", p.Id)
	inProgress := task.StatusInProgress
	_, err := api.taskStore.Update(m1, task.Patch{Status: &inProgress})
	require.NoError(t, err)
	_, err = api.taskStore.Update(m2, task.Patch{Status: &inProgress})
	require.NoError(t, err)

	drivePlanToRunning(t, api, p.Id)

	wStop := postPlanAction(t, api, p.Id, "stop")
	require.Equal(t, http.StatusOK, wStop.Code, "body=%s", wStop.Body.String())
	var stopped gen.Plan
	require.NoError(t, json.Unmarshal(wStop.Body.Bytes(), &stopped))
	assert.Equal(t, gen.PlanStateFailed, stopped.State)
	require.NotNil(t, stopped.FailedReason)
	assert.Equal(t, gen.PlanFailedReasonStoppedByUser, *stopped.FailedReason)

	for _, id := range []string{m1, m2} {
		reloaded, gerr := api.taskStore.Get(id)
		require.NoError(t, gerr)
		assert.Equal(t, task.StatusFailed, reloaded.Status, "member %s must be cancelled", id)
		assert.Equal(t, task.CancelReasonStoppedByUser, reloaded.CancelReason, "member %s must carry the cancel reason", id)
	}
}

// TestPlanStop_PartialFailureSurfacesAsServerError verifies ADR-052 §6.4 Item
// 5 / the task directive's "map the partial-stop aggregate error honestly":
// when a member's own cancel-write fails, the handler must NOT report an
// unqualified 200 — even though the plan's own state transition (a
// DIFFERENT store/directory) succeeds. Forces the member write to fail by
// stripping write permission on the tasks directory for the duration of the
// call (a real store-level I/O failure, not a mock).
func TestPlanStop_PartialFailureSurfacesAsServerError(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Stop Partial Failure WS")
	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Partial failure","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	m1 := mustCreateTask(t, api, wsID, "doomed member", p.Id)
	inProgress := task.StatusInProgress
	_, err := api.taskStore.Update(m1, task.Patch{Status: &inProgress})
	require.NoError(t, err)

	drivePlanToRunning(t, api, p.Id)

	tasksDir := api.taskStore.Dir()
	require.NoError(t, os.Chmod(tasksDir, 0o500)) // strip write: new temp files can no longer be created
	t.Cleanup(func() { _ = os.Chmod(tasksDir, 0o700) })

	wStop := postPlanAction(t, api, p.Id, "stop")
	assert.Equal(t, http.StatusInternalServerError, wStop.Code,
		"a partial fan-out failure must NOT report an unqualified 200; body=%s", wStop.Body.String())

	require.NoError(t, os.Chmod(tasksDir, 0o700)) // restore before further reads/cleanup

	// The plan's OWN state transition lives in a different directory and
	// must have succeeded regardless of the member write failure.
	reloadedPlan, perr := api.planStore.Get(p.Id)
	require.NoError(t, perr)
	assert.Equal(t, plan.StateFailed, reloadedPlan.State)
	assert.Equal(t, plan.FailedReasonStoppedByUser, reloadedPlan.FailedReason)

	// The member's write failed — it must still show its PRE-stop state, not
	// be silently reported as cancelled.
	reloadedMember, terr := api.taskStore.Get(m1)
	require.NoError(t, terr)
	assert.Equal(t, task.StatusInProgress, reloadedMember.Status,
		"a member whose cancel-write failed must not be silently reported as cancelled")
}

// TestPlanStop_EngineUnavailable503 verifies a restAPI with no PlanEngine
// wired (agentLoop.SetPlanEngine never called) fails closed with 503 rather
// than reaching a nil pointer, once the plan-exists/is-running preconditions
// (checked before the engine lookup) are already satisfied.
func TestPlanStop_EngineUnavailable503(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	agentWorkspace := tmpDir + "/agents/" + testPlansAgentID
	require.NoError(t, os.MkdirAll(agentWorkspace, 0o700))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: testPlansAgentID, Name: "Plans Test Agent", Default: true, Type: config.AgentTypeCustom, Home: agentWorkspace},
			},
		},
	}
	require.NoError(t, os.WriteFile(tmpDir+"/config.json",
		[]byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`), 0o600))
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{
		agentLoop: al,
		homePath:  tmpDir,
		taskStore: task.New(tmpDir + "/tasks"),
		taskLock:  task.TaskFileLock,
		planStore: plan.New(tmpDir + "/plans"),
	}
	require.Nil(t, agent.GetPlanEngine(al), "precondition: no engine wired")

	wsID := createTestWorkspace(t, api, "No Engine WS")
	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"No engine","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))
	drivePlanToRunning(t, api, p.Id)

	w := postPlanAction(t, api, p.Id, "stop")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body=%s", w.Body.String())
}

// --- FR-010/025: POST /tasks/{id}/stop now delegates to PlanEngine.StopTask -

// TestTaskStop_RequiresInProgress400 verifies a task that is not currently
// in_progress cannot be stopped (nothing running to cancel).
func TestTaskStop_RequiresInProgress400(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Task Stop Precondition WS")
	taskID := mustCreateTask(t, api, wsID, "not started", "")

	w := postTaskAction(t, api, taskID, "stop")
	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	reloaded, gerr := api.taskStore.Get(taskID)
	require.NoError(t, gerr)
	assert.Equal(t, task.StatusInbox, reloaded.Status, "an un-started task must be untouched by a rejected stop")
}

// TestTaskStop_HappyPathCancelsAndSetsReason verifies stopping an in_progress
// standalone task marks it failed with cancel_reason=stopped_by_user.
func TestTaskStop_HappyPathCancelsAndSetsReason(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Task Stop Happy WS")
	taskID := mustCreateTask(t, api, wsID, "running now", "")
	inProgress := task.StatusInProgress
	_, err := api.taskStore.Update(taskID, task.Patch{Status: &inProgress})
	require.NoError(t, err)

	w := postTaskAction(t, api, taskID, "stop")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var stopped gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &stopped))
	assert.Equal(t, gen.TaskStatusFailed, stopped.Status)
	require.NotNil(t, stopped.CancelReason)
	assert.Equal(t, gen.StoppedByUser, *stopped.CancelReason)

	reloaded, gerr := api.taskStore.Get(taskID)
	require.NoError(t, gerr)
	assert.Equal(t, task.StatusFailed, reloaded.Status)
	assert.Equal(t, task.CancelReasonStoppedByUser, reloaded.CancelReason)
}

// TestTaskStop_MemberStopDoesNotAffectPlanSiblings verifies member-level Stop
// (ADR-052 A5/US-7): stopping ONE in-plan member cancels only that member —
// its sibling and the plan itself are left untouched.
func TestTaskStop_MemberStopDoesNotAffectPlanSiblings(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Member Stop WS")
	wCreate := postPlan(t, api, wsID,
		`{"workspace_id":"`+wsID+`","title":"Multi-member","owner_agent_id":"`+testPlansAgentID+`"}`)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var p gen.Plan
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &p))

	m1 := mustCreateTask(t, api, wsID, "member 1", p.Id)
	m2 := mustCreateTask(t, api, wsID, "member 2", p.Id)
	inProgress := task.StatusInProgress
	_, err := api.taskStore.Update(m1, task.Patch{Status: &inProgress})
	require.NoError(t, err)
	_, err = api.taskStore.Update(m2, task.Patch{Status: &inProgress})
	require.NoError(t, err)
	drivePlanToRunning(t, api, p.Id)

	w := postTaskAction(t, api, m1, "stop")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	reloadedM1, e1 := api.taskStore.Get(m1)
	require.NoError(t, e1)
	assert.Equal(t, task.StatusFailed, reloadedM1.Status)
	assert.Equal(t, task.CancelReasonStoppedByUser, reloadedM1.CancelReason)

	reloadedM2, e2 := api.taskStore.Get(m2)
	require.NoError(t, e2)
	assert.Equal(t, task.StatusInProgress, reloadedM2.Status, "sibling member must be untouched")

	reloadedPlan, perr := api.planStore.Get(p.Id)
	require.NoError(t, perr)
	assert.Equal(t, plan.StateRunning, reloadedPlan.State, "the plan itself must still be running")
}

// TestTaskStop_NotFound404 verifies stopping an unknown task ID 404s.
func TestTaskStop_NotFound404(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	w := postTaskAction(t, api, "01JXNOSUCHTASK000000000002", "stop")
	assert.Equal(t, http.StatusNotFound, w.Code)
}
