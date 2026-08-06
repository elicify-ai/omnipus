// rest_tasks_test.go — regression tests for the fixes in rest_tasks.go (Sprint 2 hotfix).
//
// Fix #1: PUT /tasks/{id}/todos and PUT /tasks/{id}/dependencies now accept a
//
//	bare JSON array body (the correct contract shape) instead of a
//	TaskUpdateRequest object.
//
// Fix #2: POST /tasks must NOT copy source_channel / source_chat_id from
//
//	client-supplied body (exfiltration sink).
//
// Fix #3 (verify): DELETE a blocker of a multi-dep task whose remaining deps
//
//	are all done → the dependent advances blocked→next.
//
// Fix #6: PATCH /tasks/{id} with status=blocked → 400 (gateway seam guard).
//
//	PATCH /tasks/{id} with an illegal transition → 400 (store ErrValidation).
//
// Traces to: project-task-management-level1-spec.md

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// putTaskTodos sends PUT /api/v1/tasks/{id}/todos with the given bare-array body
// and returns the response recorder.
func putTaskTodos(t *testing.T, api *restAPI, id, jsonBody string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/tasks/"+id+"/todos", strings.NewReader(jsonBody))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks/" + id + "/todos"
	api.HandleTasks(w, r)
	return w
}

// putTaskDependencies sends PUT /api/v1/tasks/{id}/dependencies with the given
// bare-array body and returns the response recorder.
func putTaskDependencies(t *testing.T, api *restAPI, id, jsonBody string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/tasks/"+id+"/dependencies", strings.NewReader(jsonBody))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks/" + id + "/dependencies"
	api.HandleTasks(w, r)
	return w
}

// deleteTask sends DELETE /api/v1/tasks/{id} and returns the response recorder.
func deleteTask(t *testing.T, api *restAPI, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+id, nil)
	r.URL.Path = "/api/v1/tasks/" + id
	api.HandleTasks(w, r)
	return w
}

// ── Fix #1a: PUT todos bare-array body ───────────────────────────────────────

// TestTaskTodos_BareArrayBody_Accepted verifies PUT /tasks/{id}/todos with a
// bare JSON array body returns 200 and persists the checklist.
//
// BDD:
//
//	Given an existing task,
//	When PUT /tasks/{id}/todos with body [{"text":"a","done":false}],
//	Then 200 and the task has one todo item with text="a".
//	Given the same task,
//	When PUT /tasks/{id}/todos with body [] (clear the list),
//	Then 200 and the task has no todos.
//
// Regression for bug #1 (bare-array vs TaskUpdateRequest object decode mismatch).
func TestTaskTodos_BareArrayBody_Accepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	tsk := createTaskViaAPI(t, api, "TodoTask", wsID)

	// PUT with a single-item array → 200.
	w := putTaskTodos(t, api, tsk.Id, `[{"text":"a","status":"pending"}]`)
	require.Equal(t, http.StatusOK, w.Code,
		"PUT /tasks/{id}/todos with bare array must return 200; body=%s", w.Body.String())

	var updated gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	require.NotNil(t, updated.Todos, "todos must be set after PUT")
	require.Len(t, *updated.Todos, 1, "exactly one todo expected")
	assert.Equal(t, "a", (*updated.Todos)[0].Text, "todo text must match")
	assert.Equal(t, gen.TaskTodosStatusPending, (*updated.Todos)[0].Status, "todo status must be pending")

	// Differentiation: clear the checklist with empty array → 200, no todos.
	wClear := putTaskTodos(t, api, tsk.Id, `[]`)
	require.Equal(t, http.StatusOK, wClear.Code,
		"PUT /tasks/{id}/todos with empty array must return 200; body=%s", wClear.Body.String())
	var cleared gen.Task
	require.NoError(t, json.Unmarshal(wClear.Body.Bytes(), &cleared))
	if cleared.Todos != nil {
		assert.Empty(t, *cleared.Todos,
			"empty array body must clear the checklist")
	}
}

// TestTaskTodos_InvalidJSON_Returns400 verifies PUT /tasks/{id}/todos returns 400
// on malformed JSON input.
//
// BDD:
//
//	Given an existing task,
//	When PUT /tasks/{id}/todos with body "not json",
//	Then 400.
func TestTaskTodos_InvalidJSON_Returns400(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	tsk := createTaskViaAPI(t, api, "TodoBadJSONTask", "")

	w := putTaskTodos(t, api, tsk.Id, `not json`)
	assert.Equal(t, http.StatusBadRequest, w.Code,
		"PUT /tasks/{id}/todos with malformed JSON must return 400")
}

// ── Fix #1b: PUT dependencies bare-array body ─────────────────────────────────

// TestTaskDependencies_BareArrayBody_Accepted verifies PUT /tasks/{id}/dependencies
// with a bare JSON array body (["<id>"]) returns 200.
//
// BDD:
//
//	Given tasks TA and TB,
//	When PUT /tasks/TB/dependencies with body [TA.id],
//	Then 200 and TB.blocked_by=[TA.id].
//
// Regression for bug #1 (bare-array vs TaskUpdateRequest object decode mismatch).
func TestTaskDependencies_BareArrayBody_Accepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	ta := createTaskViaAPI(t, api, "DepA", wsID)
	tb := createTaskViaAPI(t, api, "DepB", wsID)

	body := fmt.Sprintf(`[%q]`, ta.Id)
	w := putTaskDependencies(t, api, tb.Id, body)
	require.Equal(t, http.StatusOK, w.Code,
		"PUT /tasks/{id}/dependencies with bare string array must return 200; body=%s", w.Body.String())

	var updated gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	require.NotNil(t, updated.BlockedBy, "blocked_by must be set after PUT")
	require.Len(t, *updated.BlockedBy, 1, "exactly one blocker expected")
	assert.Equal(t, ta.Id, (*updated.BlockedBy)[0], "blocker must be TA")

	// Differentiation: PUT with empty array clears blocked_by → different response.
	wClear := putTaskDependencies(t, api, tb.Id, `[]`)
	require.Equal(t, http.StatusOK, wClear.Code,
		"PUT /tasks/{id}/dependencies with empty array must return 200; body=%s", wClear.Body.String())
	var cleared gen.Task
	require.NoError(t, json.Unmarshal(wClear.Body.Bytes(), &cleared))
	if cleared.BlockedBy != nil {
		assert.Empty(t, *cleared.BlockedBy,
			"empty array must clear blocked_by")
	}
}

// TestTaskDependencies_SelfEdge_Returns400 verifies PUT /tasks/{id}/dependencies
// with a self-referencing array returns 400 (ErrBlockedBySelfEdge wraps ErrValidation).
//
// BDD:
//
//	Given task TA,
//	When PUT /tasks/TA/dependencies with body [TA.id],
//	Then 400.
func TestTaskDependencies_SelfEdge_Returns400(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	ta := createTaskViaAPI(t, api, "SelfEdgeTask", wsID)

	body := fmt.Sprintf(`[%q]`, ta.Id)
	w := putTaskDependencies(t, api, ta.Id, body)
	assert.Equal(t, http.StatusBadRequest, w.Code,
		"PUT /tasks/{id}/dependencies with self-edge must return 400; body=%s", w.Body.String())
}

// TestTaskDependencies_Cycle_Returns400 verifies PUT /tasks/{id}/dependencies
// with a cycle returns 400 (ErrBlockedByCycle wraps ErrValidation).
//
// BDD:
//
//	Given tasks TA and TB where TA.blocked_by=[TB],
//	When PUT /tasks/TB/dependencies with body [TA.id],
//	Then 400 (would form TB→TA→TB cycle).
func TestTaskDependencies_Cycle_Returns400(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	ta := createTaskViaAPI(t, api, "CycleA", wsID)
	tb := createTaskViaAPI(t, api, "CycleB", wsID)

	// TA blocked by TB.
	bodyAB := fmt.Sprintf(`[%q]`, tb.Id)
	wAB := putTaskDependencies(t, api, ta.Id, bodyAB)
	require.Equal(t, http.StatusOK, wAB.Code,
		"PUT TA blocked_by TB must succeed; body=%s", wAB.Body.String())

	// Attempt: TB blocked by TA (creates cycle TA→TB→TA) → 400.
	bodyBA := fmt.Sprintf(`[%q]`, ta.Id)
	wBA := putTaskDependencies(t, api, tb.Id, bodyBA)
	assert.Equal(t, http.StatusBadRequest, wBA.Code,
		"PUT TB blocked_by TA must return 400 (cycle); body=%s", wBA.Body.String())
}

// TestTaskDependencies_InvalidJSON_Returns400 verifies PUT /tasks/{id}/dependencies
// returns 400 on malformed JSON.
func TestTaskDependencies_InvalidJSON_Returns400(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	tsk := createTaskViaAPI(t, api, "DepBadJSON", "")

	w := putTaskDependencies(t, api, tsk.Id, `not json`)
	assert.Equal(t, http.StatusBadRequest, w.Code,
		"PUT /tasks/{id}/dependencies with malformed JSON must return 400")
}

// ── Fix #2: source_channel / source_chat_id not accepted from client ──────────

// TestTaskCreate_SourceChannelIgnored verifies POST /tasks with source_channel
// and source_chat_id in the body does NOT store those fields on the created task.
//
// BDD:
//
//	Given a POST /tasks body with source_channel="telegram" and source_chat_id="chat-1",
//	When POST /api/v1/tasks is called,
//	Then 201 and the returned task has no source_channel or source_chat_id.
//
// Regression for bug #2 (exfiltration sink — internal routing fields must not be
// client-settable).
func TestTaskCreate_SourceChannelIgnored(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	body := fmt.Sprintf(
		`{"title":"ExfilTest","action":"llm","workspace_id":%q,"source_channel":"telegram","source_chat_id":"chat-12345"}`,
		wsID,
	)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)

	require.Equal(t, http.StatusCreated, w.Code,
		"POST /tasks must return 201; body=%s", w.Body.String())

	var tsk gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))

	// source_channel and source_chat_id must NOT be echoed back.
	sc := ""
	if tsk.SourceChannel != nil {
		sc = *tsk.SourceChannel
	}
	schi := ""
	if tsk.SourceChatId != nil {
		schi = *tsk.SourceChatId
	}
	assert.Empty(t, sc,
		"source_channel must not be stored from client-supplied body (exfiltration sink)")
	assert.Empty(t, schi,
		"source_chat_id must not be stored from client-supplied body (exfiltration sink)")
}

// getTaskDue reads a task back via GET /api/v1/tasks/{id} and returns its Due.
func getTaskDue(t *testing.T, api *restAPI, id string) *time.Time {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id, nil)
	r.URL.Path = "/api/v1/tasks/" + id
	api.HandleTasks(w, r)
	require.Equal(t, http.StatusOK, w.Code, "getTaskDue GET must return 200; body=%s", w.Body.String())
	var tsk gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))
	return tsk.Due
}

// TestTaskPatch_ClearDue_ClearsPreviouslySetDue proves the clear_due UAT fix: a
// PATCH with {"clear_due":true} clears a previously-set due date (the empty
// `due` string is not a valid date-time and cannot express a clear). It also
// proves a `due` value still sets the date, and that `due` wins when both are
// present.
func TestTaskPatch_ClearDue_ClearsPreviouslySetDue(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	tsk := createTaskViaAPI(t, api, "ClearDueTask", "")

	// 1) Set a due date.
	wSet := patchTask(t, api, tsk.Id, `{"due":"2026-08-15T17:00:00Z"}`)
	require.Equal(t, http.StatusOK, wSet.Code, "PATCH due=value must return 200; body=%s", wSet.Body.String())
	due := getTaskDue(t, api, tsk.Id)
	require.NotNil(t, due, "due must be set after PATCH due=value")
	assert.Equal(t, "2026-08-15T17:00:00Z", due.UTC().Format(time.RFC3339))

	// 2) Clear it with clear_due=true.
	wClear := patchTask(t, api, tsk.Id, `{"clear_due":true}`)
	require.Equal(t, http.StatusOK, wClear.Code, "PATCH clear_due=true must return 200; body=%s", wClear.Body.String())
	assert.Nil(t, getTaskDue(t, api, tsk.Id), "clear_due=true must clear the previously-set due date")

	// 3) A due value still sets the date (round-trip).
	wSet2 := patchTask(t, api, tsk.Id, `{"due":"2026-09-01T09:00:00Z"}`)
	require.Equal(t, http.StatusOK, wSet2.Code, "PATCH due=value must return 200; body=%s", wSet2.Body.String())
	due2 := getTaskDue(t, api, tsk.Id)
	require.NotNil(t, due2, "due must be set after a second PATCH due=value")
	assert.Equal(t, "2026-09-01T09:00:00Z", due2.UTC().Format(time.RFC3339))

	// 4) due wins over clear_due when both are present (clear_due ignored).
	wBoth := patchTask(t, api, tsk.Id, `{"due":"2026-10-10T10:00:00Z","clear_due":true}`)
	require.Equal(t, http.StatusOK, wBoth.Code, "PATCH due+clear_due must return 200; body=%s", wBoth.Body.String())
	dueBoth := getTaskDue(t, api, tsk.Id)
	require.NotNil(t, dueBoth, "due value must win when batched with clear_due=true")
	assert.Equal(t, "2026-10-10T10:00:00Z", dueBoth.UTC().Format(time.RFC3339))
}

// ── Fix #6a: PATCH status=blocked → 400 (gateway seam guard) ─────────────────

// TestTaskPatch_StatusBlocked_Returns400 verifies PATCH /tasks/{id} with
// status=blocked returns 400 at the gateway seam.
//
// BDD:
//
//	Given an existing task,
//	When PATCH /tasks/{id} with {"status":"blocked"},
//	Then 400 (blocked is a derived side-state).
func TestTaskPatch_StatusBlocked_Returns400(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	tsk := createTaskViaAPI(t, api, "BlockedGuardTask", "")

	w := patchTask(t, api, tsk.Id, `{"status":"blocked"}`)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"PATCH status=blocked must return 400; body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "blocked",
		"400 body must mention 'blocked' so the caller can diagnose")
}

// ── Fix #6b: PATCH illegal transition (done → inbox) → 400 ──────────────────

// TestTaskPatch_IllegalTransition_Returns400 verifies PATCH /tasks/{id} with an
// illegal status transition returns 400 (ErrIllegalTransition wraps ErrValidation).
//
// BDD:
//
//	Given a task in status "done",
//	When PATCH /tasks/{id} with {"status":"inbox"},
//	Then 400 (done → inbox is not a permitted transition).
//	Differentiation: PATCH {"status":"failed"} on the same done task also returns 400
//	(not 200), proving the guard fires on the transition, not the field presence.
func TestTaskPatch_IllegalTransition_Returns400(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	// Create a task and advance it through inbox→next→in_progress→done.
	tsk := createTaskViaAPI(t, api, "DoneTask", wsID)

	// Advance to next (needs description for the partial-task guard).
	wNext := patchTask(t, api, tsk.Id, `{"status":"next","description":"ready"}`)
	require.Equal(t, http.StatusOK, wNext.Code,
		"PATCH status=next must succeed; body=%s", wNext.Body.String())

	wInProg := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, wInProg.Code,
		"PATCH status=in_progress must succeed; body=%s", wInProg.Body.String())

	wDone := patchTask(t, api, tsk.Id, `{"status":"done"}`)
	require.Equal(t, http.StatusOK, wDone.Code,
		"PATCH status=done must succeed; body=%s", wDone.Body.String())

	// Illegal transition: done → inbox → 400.
	wIllegal := patchTask(t, api, tsk.Id, `{"status":"inbox"}`)
	require.Equal(t, http.StatusBadRequest, wIllegal.Code,
		"PATCH status=inbox from done must return 400 (illegal transition); body=%s", wIllegal.Body.String())

	// Differentiation: done → failed is also an illegal transition from done → 400.
	wIllegal2 := patchTask(t, api, tsk.Id, `{"status":"failed"}`)
	require.Equal(t, http.StatusBadRequest, wIllegal2.Code,
		"PATCH status=failed from done must return 400 (illegal transition); body=%s", wIllegal2.Body.String())
}

// ── Fix #3 (verify): delete blocker → multi-dep task advances blocked→next ────

// TestTaskDelete_Blocker_AdvancesMultiDepDependent verifies that deleting a task
// that is one of two blockers of a dependent, where the other blocker is already
// done, causes the dependent to advance from blocked → next.
//
// Scenario:
//
//	TC is blocked with blocked_by=[TB, TDone].
//	TDone is done; TB is next (not done) → TC is blocked.
//	DELETE /tasks/TB → TB's edge is removed → remaining blocker TDone is done
//	→ TC must advance to next.
//
// TC is seeded directly to disk in `blocked` status (bypassing the gateway) because
// updateLocked does not call recomputeBlockedStateLocked — only AddDependency does.
//
// Regression for bug #3: cascadeDeleteEdges now includes tasks whose remaining
// blockers are all `done` after the delete (not only tasks with an empty list).
func TestTaskDelete_Blocker_AdvancesMultiDepDependent(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	tasksDir := api.homePath + "/tasks"

	// TDone: create via API and advance to done.
	tDone := createTaskViaAPI(t, api, "TDone", wsID)
	wNext := patchTask(t, api, tDone.Id, `{"status":"next","description":"ready"}`)
	require.Equal(t, http.StatusOK, wNext.Code)
	wInP := patchTask(t, api, tDone.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, wInP.Code)
	wDn := patchTask(t, api, tDone.Id, `{"status":"done"}`)
	require.Equal(t, http.StatusOK, wDn.Code)

	// TB: create via API (stays in inbox/next — not done, the blocker to delete).
	tB := createTaskViaAPI(t, api, "TB", wsID)

	// TC: seed directly to disk in `blocked` status with blocked_by=[TB.Id, TDone.Id].
	// We do this because updateLocked does not recompute the blocked side-state;
	// the recomputeBlockedStateLocked path is only triggered by AddDependency.
	const tcID = "test-multidep-tc-00000000001"
	now := "2026-06-01T00:00:00Z"
	tcData := fmt.Sprintf(
		`{"id":%q,"title":"TC","action":"llm","status":"blocked","workspace_id":%q,"blocked_by":[%q,%q],"created_at":%q,"updated_at":%q}`,
		tcID,
		wsID,
		tB.Id,
		tDone.Id,
		now,
		now,
	)
	require.NoError(t, os.WriteFile(tasksDir+"/"+tcID+".json", []byte(tcData), 0o600))

	// Verify TC can be read back as blocked.
	statusTC := getTaskStatus(t, api, tcID)
	require.Equal(t, gen.TaskStatus("blocked"), statusTC,
		"TC must start in blocked status (seeded directly to disk)")

	// DELETE TB → remaining blocker TDone is done → TC advances blocked→next.
	wDel := deleteTask(t, api, tB.Id)
	require.Equal(t, http.StatusNoContent, wDel.Code,
		"DELETE TB must return 204; body=%s", wDel.Body.String())

	// Assert TC is now next.
	statusAfter := getTaskStatus(t, api, tcID)
	assert.Equal(t, gen.TaskStatus("next"), statusAfter,
		"TC must advance from blocked→next after deleting TB (remaining blocker TDone is done)")
}

// ── validateTaskAgentID guard-clause hardening ───────────────────────────────
//
// validateTaskAgentID's two early guards (a.agentLoop == nil; an empty/nil
// registry) used to `return nil` — silently ALLOWING the agent_id assignment,
// which would skip BOTH the subagent_3p rejection and the team-membership
// check for any caller that reached them. They now return
// errTaskAgentLoopUnavailable (fail CLOSED) instead, matching the codebase's
// established "dependency not initialized -> deny" convention (see
// rest_god_mode.go / rest_sandbox_config.go / rest_security_wave5.go).
//
// These tests call validateTaskAgentID directly rather than through
// handleTaskCreate/handleTaskPatch: both of those handlers already dereference
// a.agentLoop.GetConfig() before ever calling validateTaskAgentID, so a nil
// agentLoop would panic on that earlier line first — the guard is reachable
// only by a caller that does not share that precondition, which these tests
// construct directly.

// TestValidateTaskAgentID_NilAgentLoop_FailsClosed proves a nil agentLoop now
// denies rather than silently allowing the assignment.
func TestValidateTaskAgentID_NilAgentLoop_FailsClosed(t *testing.T) {
	api := &restAPI{}
	err := api.validateTaskAgentID("some-agent", "some-workspace")
	require.Error(t, err, "a nil agentLoop must fail CLOSED, not silently allow the assignment")
	assert.ErrorIs(t, err, errTaskAgentLoopUnavailable)
}

// TestValidateTaskAgentID_EmptyAgentID_StillSkipsCheck proves the one
// legitimate no-op path survives the guard-clause hardening: an empty
// agent_id means there is no assignment to validate at all — this is not a
// fail-open case, there is simply nothing to check, and must remain nil even
// with agentLoop nil.
func TestValidateTaskAgentID_EmptyAgentID_StillSkipsCheck(t *testing.T) {
	api := &restAPI{}
	err := api.validateTaskAgentID("", "some-workspace")
	require.NoError(t, err, "an empty agent_id has nothing to validate and must remain a no-op")
}

// ── reconcileStuckTasks: orphaned TaskRun closure (ADR-050 RD10 / MED-#M2) ──
//
// ADR-050 removed the stuck-run reaper (operator decision 2026-07-20: no
// liveness-aware background sweep — "a run stays in_progress until closed").
// That leaves reconcileStuckTasks's own boot-time reset as the ONLY place an
// abandoned run for a crashed task can ever be closed; without also closing
// the TaskRun, it would stay in_progress forever, even across further
// restarts, diverging from the just-recovered (failed) Task.

// TestReconcileStuckTasks_ClosesOrphanedTaskRun proves that when
// reconcileStuckTasks resets a stuck in_progress task to failed, it also
// closes that task's own open TaskRun to failed — bounding the orphaned-run
// gap to "next restart" instead of leaving it in_progress permanently.
//
// BDD:
//
//	Given a task file with status="in_progress" that has an OPEN TaskRun,
//	When reconcileStuckTasks() is called,
//	Then the task is reset to failed AND its open run is closed to failed
//	  with an "abandoned: gateway restarted mid-execution" result.
//	Given a second in_progress task whose run is ALREADY closed (done),
//	Then reconcile leaves that already-terminal run untouched (no double-close).
func TestReconcileStuckTasks_ClosesOrphanedTaskRun(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Task A: genuinely stuck — an in_progress task with an OPEN run, as a
	// crashed dispatch (OpenRun called, never CloseRun'd) would leave it.
	// createTaskWithStatusViaAPI (rest_board_test.go) drives the legal
	// inbox→next→in_progress transition path — a direct inbox→in_progress
	// PATCH is not itself a legal transition.
	taskA := createTaskWithStatusViaAPI(t, api, "ReconcileOrphanRunTask", "", "in_progress")

	openRun, created, err := api.taskStore.OpenRun(taskA.Id, nil, task.RunKindManual, "sess-orphan")
	require.NoError(t, err)
	require.True(t, created)

	// Task B: also in_progress, but its run was already closed BEFORE the
	// crash (e.g. the completion handler raced the crash and won) — reconcile
	// must not attempt to close it a second time (CloseRun would error on an
	// already-terminal run; this proves reconcile doesn't blindly force one).
	taskB := createTaskWithStatusViaAPI(t, api, "ReconcileClosedRunTask", "", "in_progress")

	closedRun, createdB, err := api.taskStore.OpenRun(taskB.Id, nil, task.RunKindManual, "sess-already-done")
	require.NoError(t, err)
	require.True(t, createdB)
	require.NoError(t, api.taskStore.CloseRun(taskB.Id, closedRun.RunID, task.StatusDone, "finished before the crash"))

	api.reconcileStuckTasks()

	// Task A's Task row: reset to failed (existing reconcileStuckTasks behavior).
	gotA, err := api.taskStore.Get(taskA.Id)
	require.NoError(t, err)
	assert.Equal(t, task.StatusFailed, gotA.Status)

	// Task A's run: the NEW behavior under test — the previously-open run is
	// now closed to failed with a diagnostic result, not stranded in_progress.
	runsA, err := api.taskStore.ListRuns(taskA.Id)
	require.NoError(t, err)
	require.Len(t, runsA, 1)
	assert.Equal(t, openRun.RunID, runsA[0].RunID)
	assert.Equal(t, task.StatusFailed, runsA[0].Status,
		"reconcile must close the orphaned open run to failed, not leave it in_progress forever")
	assert.False(t, runsA[0].IsOpen(), "the orphaned run must no longer be open after reconcile")
	assert.Equal(t, "abandoned: gateway restarted mid-execution", runsA[0].Result)
	require.NotNil(t, runsA[0].EndedAt, "a closed run must carry ended_at")

	// Task B's run: already terminal before reconcile ran — untouched, not
	// double-closed or overwritten.
	runsB, err := api.taskStore.ListRuns(taskB.Id)
	require.NoError(t, err)
	require.Len(t, runsB, 1)
	assert.Equal(t, task.StatusDone, runsB[0].Status,
		"an already-closed run must not be touched by reconcile")
	assert.Equal(t, "finished before the crash", runsB[0].Result)
}

// TestReconcileStuckTasks_TaskWithNoRuns_IsNoOp proves reconcile does not
// error or panic on a stuck task that has zero recorded runs (a pre-ADR-050
// task that was never dispatched through OpenRun, or one whose runs
// directory was pruned) — reconcileStuckTaskRuns's ListRuns call folds to an
// empty slice and there is nothing to close.
func TestReconcileStuckTasks_TaskWithNoRuns_IsNoOp(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	tsk := createTaskWithStatusViaAPI(t, api, "ReconcileNoRunsTask", "", "in_progress")

	require.NotPanics(t, func() { api.reconcileStuckTasks() })

	got, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err)
	assert.Equal(t, task.StatusFailed, got.Status, "the task itself must still be reset to failed")

	runs, err := api.taskStore.ListRuns(tsk.Id)
	require.NoError(t, err)
	assert.Empty(t, runs, "a task with zero runs stays with zero runs after reconcile")
}
