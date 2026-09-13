// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_tasks_running_freeze_test.go — the REST half of the running-task field
// freeze (operator decision, 2026-09-12).
//
// The rule is FIELD-LEVEL, never a blanket lock on a running task:
//
//   - the judged contract — the goal definition, the acceptance criteria, the
//     definition of done and the prompt — freezes for the duration of a run,
//     because the Judge measures the finished work against exactly that set.
//     Move one mid-run and neither a pass nor a fail means anything.
//   - the record of PROGRESS — status, todos, result, artifacts and every
//     other bookkeeping field — stays writable, because the working agent
//     updates those as it goes and MUST be able to.
//
// The mutable-side test is the one that carries the weight: an implementation
// that refused every PATCH on a running task would pass the frozen-side test
// on its own and would break the agent mid-run.
//
// The update_task twin lives in pkg/tools/task_running_freeze_test.go and
// asserts the identical rule with the identical wording.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// startFreezeTask creates a task and drives it to in_progress through the
// STORE rather than the REST launch path, so the fixture has no live dispatch
// goroutine behind it — this file is about the patch gate, not about dispatch.
func startFreezeTask(t *testing.T, api *restAPI, wsID, title string) gen.Task {
	t.Helper()
	tsk := createTaskViaAPI(t, api, title, wsID)
	inProgress := task.StatusInProgress
	_, err := api.taskStore.Update(tsk.Id, task.Patch{Status: &inProgress})
	require.NoError(t, err, "store-level advance to in_progress must succeed")
	require.Equal(t, gen.TaskStatusInProgress, getTaskStatus(t, api, tsk.Id),
		"precondition: the fixture task must be running")
	return tsk
}

// freezeErrMessage decodes the `error` string out of a jsonErr body, so the
// assertions below match the message a caller actually reads rather than its
// JSON-escaped on-the-wire form (a raw-body Contains check for `"prompt"`
// never matches, because the body carries `\"prompt\"`).
func freezeErrMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload),
		"error body must be JSON; body=%s", w.Body.String())
	require.NotEmpty(t, payload.Error, "error body must carry a message; body=%s", w.Body.String())
	return payload.Error
}

// getFreezeTask reads a task back over the wire.
func getFreezeTask(t *testing.T, api *restAPI, id string) gen.Task {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id, nil)
	r.URL.Path = "/api/v1/tasks/" + id
	api.HandleTasks(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET must return 200; body=%s", w.Body.String())
	var got gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got
}

// TestTaskPatch_RunningTask_RefusesFrozenDefinitionFields is side one of the
// rule: the target cannot move under work that is already being measured
// against it.
//
// 409 Conflict is the deliberate status, and the same one the update_task
// tool reports: the request is well formed and the field is legal, but the
// resource is in a state that forbids this particular change. 400 would say
// the caller sent something invalid (it did not — the identical body is
// accepted a moment before the task starts and a moment after it stops), and
// 403 would say the caller lacks permission (it does not).
func TestTaskPatch_RunningTask_RefusesFrozenDefinitionFields(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantNamed string
	}{
		{
			name:      "prompt (the instructions, and the goal definition)",
			body:      `{"prompt":"actually, do something else"}`,
			wantNamed: `"prompt"`,
		},
		{
			name:      "criteria (the acceptance criteria)",
			body:      `{"criteria":` + validCriteriaJSON + `}`,
			wantNamed: `"criteria"`,
		},
		{
			name:      "dod (the definition of done)",
			body:      `{"dod":` + validDoDJSON + `}`,
			wantNamed: `"dod"`,
		},
		{
			name:      "several at once names every one of them",
			body:      `{"prompt":"new","criteria":` + validCriteriaJSON + `,"dod":` + validDoDJSON + `}`,
			wantNamed: `"prompt", "criteria" and "dod"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			wsID := ensureTestWorkspace(t, api)
			tsk := startFreezeTask(t, api, wsID, "FrozenFieldTask")

			w := patchTask(t, api, tsk.Id, tc.body)

			require.Equal(t, http.StatusConflict, w.Code,
				"a frozen-field PATCH on a running task must be 409 Conflict; body=%s", w.Body.String())
			msg := freezeErrMessage(t, w)
			assert.Contains(t, msg, tc.wantNamed,
				"the refusal must NAME the refused field(s), not fail generically; msg=%s", msg)
			assert.Contains(t, strings.ToLower(msg), "while the task is running",
				"the refusal must say the task is running; msg=%s", msg)
		})
	}
}

// TestTaskPatch_RunningTask_MixedRequestAppliesNothing pins the whole-request
// rule. Silently applying the mutable half of a rejected request is worse than
// either outcome on its own: the caller is told the edit failed while part of
// it has in fact landed.
func TestTaskPatch_RunningTask_MixedRequestAppliesNothing(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	tsk := startFreezeTask(t, api, wsID, "MixedPatchTask")

	before := getFreezeTask(t, api, tsk.Id)

	w := patchTask(t, api, tsk.Id,
		`{"priority":4,"tags":["sneaky"],"prompt":"actually, do something else"}`)
	require.Equal(t, http.StatusConflict, w.Code,
		"a request mixing frozen and mutable fields must be refused as a whole; body=%s", w.Body.String())
	assert.Contains(t, freezeErrMessage(t, w), `"prompt"`,
		"the refusal must name the FROZEN field that caused it, not the mutable ones")

	after := getFreezeTask(t, api, tsk.Id)
	assert.Equal(t, before.Priority, after.Priority,
		"the mutable half of a refused request must NOT have been applied")
	assert.Equal(t, before.Tags, after.Tags,
		"the mutable half of a refused request must NOT have been applied")
}

// TestTaskPatch_RunningTask_MutableProgressFieldsStillSucceed is side two, and
// the assertion that actually separates this fix from a blanket lock. A fix
// that blocked everything on a running task would pass every test above and
// would break the agent mid-run — which the operator decision names as the
// worse failure of the two.
func TestTaskPatch_RunningTask_MutableProgressFieldsStillSucceed(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	tsk := startFreezeTask(t, api, wsID, "ProgressPatchTask")

	// The todos — the working checklist the LLM rewrites as it goes.
	wTodos := patchTask(t, api, tsk.Id,
		`{"todos":[{"text":"read the spec","status":"completed"},{"text":"write the code","status":"pending"}]}`)
	require.Equal(t, http.StatusOK, wTodos.Code,
		"a todo update on a running task must SUCCEED — todos are the record of progress, "+
			"not the target; body=%s", wTodos.Body.String())

	withTodos := getFreezeTask(t, api, tsk.Id)
	require.NotNil(t, withTodos.Todos, "todos must be present after the update")
	require.Len(t, *withTodos.Todos, 2, "both todos must have persisted")
	assert.Equal(t, "read the spec", (*withTodos.Todos)[0].Text,
		"the todo write must have actually persisted, not merely been accepted")

	// Result and artifacts — mid-run progress, written while the loop runs.
	wResult := patchTask(t, api, tsk.Id,
		`{"result":"two of three checks are green so far","artifacts":["out/report.txt"]}`)
	require.Equal(t, http.StatusOK, wResult.Code,
		"a result/artifacts update on a running task must SUCCEED; body=%s", wResult.Body.String())

	withResult := getFreezeTask(t, api, tsk.Id)
	require.NotNil(t, withResult.Result)
	assert.Equal(t, "two of three checks are green so far", *withResult.Result,
		"the progress write must have persisted")

	// The status itself — the agent advances it as the work ends. `failed` is
	// used because a running task can reach it without the executor this
	// harness deliberately does not wire.
	wStatus := patchTask(t, api, tsk.Id, `{"status":"failed"}`)
	require.Equal(t, http.StatusOK, wStatus.Code,
		"a status update on a running task must SUCCEED — stopping or ending a run is not an "+
			"edit of its definition; body=%s", wStatus.Body.String())
	assert.Equal(t, gen.TaskStatusFailed, getTaskStatus(t, api, tsk.Id),
		"the status write must have actually persisted")
}

// TestTaskPatch_NotRunning_FrozenFieldsAreEditable proves the gate is scoped
// to the running state rather than a permanent ban: the byte-identical body
// the running task refused is accepted on a task that is not running. Without
// this, an implementation that rejected `prompt` unconditionally would look
// correct from the frozen-side test alone.
func TestTaskPatch_NotRunning_FrozenFieldsAreEditable(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	tsk := createTaskViaAPI(t, api, "EditableBeforeRunTask", wsID)

	w := patchTask(t, api, tsk.Id, `{"prompt":"the real instructions"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"editing the prompt of a task that is NOT running must succeed; body=%s", w.Body.String())

	got := getFreezeTask(t, api, tsk.Id)
	require.NotNil(t, got.Prompt)
	assert.Equal(t, "the real instructions", *got.Prompt,
		"the prompt edit must have landed on a non-running task")
}
