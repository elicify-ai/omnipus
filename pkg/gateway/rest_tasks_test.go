//go:build !cgo

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
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
	w := putTaskTodos(t, api, tsk.Id, `[{"text":"a","done":false}]`)
	require.Equal(t, http.StatusOK, w.Code,
		"PUT /tasks/{id}/todos with bare array must return 200; body=%s", w.Body.String())

	var updated gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	require.NotNil(t, updated.Todos, "todos must be set after PUT")
	require.Len(t, *updated.Todos, 1, "exactly one todo expected")
	assert.Equal(t, "a", (*updated.Todos)[0].Text, "todo text must match")
	assert.False(t, (*updated.Todos)[0].Done, "todo done must be false")

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
		tcID, wsID, tB.Id, tDone.Id, now, now,
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
