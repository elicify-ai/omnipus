//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_task_runs_test.go — tests for GET /api/v1/tasks/{id}/runs (ADR-050
// docs/internal/architecture/ADR-050-task-run-history-model.md,
// docs/internal/specs/task-run-history-spec.md §3.6). Exercises the real
// dispatch path (api.HandleTasks) the same way rest_tasks_test.go's
// putTaskTodos/putTaskDependencies/deleteTask helpers do, so the "runs" case
// wired into HandleTasks' sub-resource switch is covered end to end, not
// just handleTaskRuns in isolation.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// getTaskRuns sends GET /api/v1/tasks/{id}/runs through HandleTasks (the
// real dispatcher a live request takes) and returns the recorder.
func getTaskRuns(t *testing.T, api *restAPI, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id+"/runs", nil)
	r.URL.Path = "/api/v1/tasks/" + id + "/runs"
	api.HandleTasks(w, r)
	return w
}

// TestTaskRunsEndpoint covers GET /api/v1/tasks/{id}/runs: the happy path
// (newest-first, full wire-field translation), a 404 for a well-formed but
// unknown task id, a 400 for a malformed id (validateEntityID), and a 405
// for the wrong method.
func TestTaskRunsEndpoint(t *testing.T) {
	t.Run("returns runs newest-first with full field translation", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		tsk := createTaskViaAPI(t, api, "RunsTask", "")

		// First attempt: a scheduled occurrence run that fails.
		occMs := int64(1_800_000_000_000)
		older, created, err := api.taskStore.OpenRun(tsk.Id, &occMs, task.RunKindScheduled, "sess-older")
		require.NoError(t, err)
		require.True(t, created)
		require.NoError(t, api.taskStore.CloseRun(tsk.Id, older.RunID, task.StatusFailed, "older result"))

		// Re-run of the SAME occurrence (RD7's per-occurrence Run-now):
		// OpenRun's idempotency only guards a concurrently-OPEN run for the
		// same key, so a fresh attempt after the prior one closed opens a
		// genuinely new run and the prior failed run is preserved.
		newer, created2, err := api.taskStore.OpenRun(tsk.Id, &occMs, task.RunKindManual, "sess-newer")
		require.NoError(t, err)
		require.True(t, created2)
		require.NoError(t, api.taskStore.CloseRun(tsk.Id, newer.RunID, task.StatusDone, "newer result"))

		w := getTaskRuns(t, api, tsk.Id)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var runs []gen.TaskRun
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &runs))
		require.Len(t, runs, 2, "both the original run and the re-run must be preserved")

		// newest (most recently STARTED) first.
		assert.Equal(t, newer.RunID, runs[0].RunId)
		assert.Equal(t, tsk.Id, runs[0].TaskId)
		assert.Equal(t, gen.TaskRunStatusDone, runs[0].Status)
		assert.Equal(t, gen.TaskRunKindManual, runs[0].Kind)
		assert.Equal(t, "sess-newer", runs[0].SessionId)
		require.NotNil(t, runs[0].OccurrenceMs)
		assert.Equal(t, occMs, *runs[0].OccurrenceMs)
		require.NotNil(t, runs[0].Result)
		assert.Equal(t, "newer result", *runs[0].Result)
		require.NotNil(t, runs[0].EndedAt, "a closed run must carry ended_at")

		assert.Equal(t, older.RunID, runs[1].RunId)
		assert.Equal(t, gen.TaskRunStatusFailed, runs[1].Status)
		assert.Equal(t, gen.TaskRunKindScheduled, runs[1].Kind)
		assert.Equal(t, "sess-older", runs[1].SessionId)
		require.NotNil(t, runs[1].Result)
		assert.Equal(t, "older result", *runs[1].Result)
	})

	t.Run("an in_progress (unclosed) run has a nil ended_at and no result", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		tsk := createTaskViaAPI(t, api, "RunsInProgressTask", "")

		open, created, err := api.taskStore.OpenRun(tsk.Id, nil, task.RunKindManual, "sess-open")
		require.NoError(t, err)
		require.True(t, created)

		w := getTaskRuns(t, api, tsk.Id)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var runs []gen.TaskRun
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &runs))
		require.Len(t, runs, 1)
		assert.Equal(t, open.RunID, runs[0].RunId)
		assert.Equal(t, gen.TaskRunStatusInProgress, runs[0].Status)
		assert.Nil(t, runs[0].OccurrenceMs, "OpenRun(nil) is an ad-hoc/manual run")
		assert.Nil(t, runs[0].EndedAt)
		assert.Nil(t, runs[0].Result)
	})

	t.Run("a task with zero runs returns an empty array, not null", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		tsk := createTaskViaAPI(t, api, "RunsNoneTask", "")

		w := getTaskRuns(t, api, tsk.Id)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		assert.JSONEq(t, "[]", w.Body.String())
	})

	t.Run("well-formed but unknown task id returns 404", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		w := getTaskRuns(t, api, "01ARZ3NDEKTSV4RRFFQ69G5FAV")
		assert.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
	})

	t.Run("malformed task id returns 400", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		// No path separators (those would change how HandleTasks' own
		// {id}/{sub} split parses the URL) — ".." alone is what
		// validateEntityID rejects.
		w := getTaskRuns(t, api, "task..id")
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	})

	t.Run("wrong method returns 405", func(t *testing.T) {
		// GET (list) and POST (Run now) are the supported methods; PUT is not.
		api := newTestRestAPIWithHome(t)
		tsk := createTaskViaAPI(t, api, "RunsMethodTask", "")
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/tasks/"+tsk.Id+"/runs", nil)
		r.URL.Path = "/api/v1/tasks/" + tsk.Id + "/runs"
		api.HandleTasks(w, r)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "body=%s", w.Body.String())
	})

	t.Run("POST with no executor returns 503", func(t *testing.T) {
		// Run now (POST) is supported; with a nil taskExecutor (degraded
		// gateway) it is rejected 503, not 405 — proving POST is routed.
		api := newTestRestAPIWithHome(t)
		tsk := createTaskViaAPI(t, api, "RunsRunNowTask", "")
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+tsk.Id+"/runs", nil)
		r.URL.Path = "/api/v1/tasks/" + tsk.Id + "/runs"
		api.HandleTasks(w, r)
		assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body=%s", w.Body.String())
	})
}
