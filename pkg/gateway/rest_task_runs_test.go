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
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

	// D1 (operator decision 2026-07-20, TaskRunStatusField.tsx): Run-now must
	// never be allowed for a FUTURE occurrence — running it early would
	// double-execute the occurrence once at the manual instant and again at
	// its naturally-scheduled fire, since the scheduler is
	// RRULE/Task.status-driven and has no awareness of TaskRuns. The React
	// gate (`occurrence.ms > now`) only protects the calendar UI; these two
	// subtests prove handleTaskRunNow enforces the SAME threshold at the API
	// boundary, so a direct POST cannot bypass it. Both use
	// newTestRestAPIWithHome (nil taskExecutor) so the two outcomes are
	// unambiguous: 400 means the D1 gate itself rejected the request BEFORE
	// ever reaching the executor; 503 means the request cleared D1 and was
	// only then blocked by the (intentionally) missing executor — proving D1
	// let a past/current occurrence THROUGH rather than also rejecting it.
	t.Run("future occurrence_ms is rejected 400 by the D1 gate and opens no run", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		tsk := createTaskViaAPI(t, api, "RunsFutureOccTask", "")

		futureMs := time.Now().Add(24 * time.Hour).UnixMilli()
		reqBody, err := json.Marshal(map[string]any{"occurrence_ms": futureMs})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+tsk.Id+"/runs", bytes.NewReader(reqBody))
		r.URL.Path = "/api/v1/tasks/" + tsk.Id + "/runs"
		api.HandleTasks(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

		runs, err := api.taskStore.ListRuns(tsk.Id)
		require.NoError(t, err)
		assert.Empty(t, runs, "a D1-rejected future-occurrence Run-now must not open a TaskRun")
	})

	t.Run(
		"past occurrence_ms clears the D1 gate (falls through to the 503 executor-unavailable path)",
		func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			tsk := createTaskViaAPI(t, api, "RunsPastOccTask", "")

			pastMs := time.Now().Add(-24 * time.Hour).UnixMilli()
			reqBody, err := json.Marshal(map[string]any{"occurrence_ms": pastMs})
			require.NoError(t, err)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+tsk.Id+"/runs", bytes.NewReader(reqBody))
			r.URL.Path = "/api/v1/tasks/" + tsk.Id + "/runs"
			api.HandleTasks(w, r)
			assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body=%s", w.Body.String())
		},
	)

	t.Run(
		"omitted occurrence_ms (task-level Run-now) clears the D1 gate (falls through to the 503 executor-unavailable path)",
		func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			tsk := createTaskViaAPI(t, api, "RunsOmittedOccTask", "")

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+tsk.Id+"/runs", nil)
			r.URL.Path = "/api/v1/tasks/" + tsk.Id + "/runs"
			api.HandleTasks(w, r)
			assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body=%s", w.Body.String())
		},
	)
}

// TestTaskRunNow_LiveExecutor_ViaRealServer is the regression guard for the
// StartOccurrenceRun context-detach fix (pkg/agent/task_executor.go): the
// caller-supplied request context is intentionally NOT threaded through as
// the dispatch parent, because net/http.Server cancels the request context
// the instant the handler returns after WriteHeader(202) flushes — which
// would otherwise abort the just-launched background run almost immediately
// with a spurious "context canceled" failure.
//
// httptest.NewRequest deliberately does NOT reproduce this: it builds a
// *http.Request whose context is a bare context.Background() that is never
// canceled by anything, since no real connection/handler-return machinery is
// involved. A test built on httptest.NewRequest would pass whether or not
// the context-detach fix is present — it cannot exercise the bug at all.
// Only a real net/http.Server (httptest.NewServer) actually cancels the
// request's context on handler return, which is why this test drives the
// POST through one.
//
// Covers both call shapes from task-run-history-spec.md §3.4: a recurring
// occurrence Run-now ({"occurrence_ms":N}) and a normal/once re-run (empty
// body). Both must return 202, and — the actual regression assertion — the
// dispatched run must round-trip via GET /tasks/{id}/runs and reach a
// TERMINAL status (done/failed), proving the execution was not silently
// canceled by the request's context tearing down when the handler returned.
func TestTaskRunNow_LiveExecutor_ViaRealServer(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"main"})

	tsk := createTaskViaAPI(t, api, "RunNowLiveServerTask", wsID)
	wAssign := patchTask(t, api, tsk.Id, `{"agent_id":"main"}`)
	require.Equal(t, http.StatusOK, wAssign.Code,
		"assigning agent_id=main must succeed; body=%s", wAssign.Body.String())

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tasks/", api.HandleTasks)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	t.Run("occurrence run: POST {occurrence_ms:N} returns 202 and reaches a terminal status", func(t *testing.T) {
		// D1 (operator decision 2026-07-20, TaskRunStatusField.tsx /
		// handleTaskRunNow's own D1 gate): Run-now rejects a FUTURE
		// occurrence_ms with 400. This subtest exercises the ALLOWED path
		// (occurrence_ms in the past), so occMs is computed relative to
		// "now" rather than a fixed literal — the original hardcoded
		// constant (2026-07-19T16:00:00Z) silently drifted into the future
		// as real time passed it, which would now 400 instead of 202.
		occMs := time.Now().Add(-1 * time.Hour).UnixMilli()
		reqBody, err := json.Marshal(map[string]any{"occurrence_ms": occMs})
		require.NoError(t, err)

		resp, err := http.Post(srv.URL+"/api/v1/tasks/"+tsk.Id+"/runs", "application/json", bytes.NewReader(reqBody))
		require.NoError(t, err, "POST /tasks/{id}/runs over the real server must succeed")
		respBody, readErr := io.ReadAll(resp.Body)
		require.NoError(t, readErr)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusAccepted, resp.StatusCode, "body=%s", string(respBody))

		// The run row is opened asynchronously inside the dispatched goroutine
		// (handleTaskRunNow's own doc comment) — poll GET /tasks/{id}/runs
		// until this occurrence's run appears AND reaches done/failed. A
		// spurious context-cancel would strand it in_progress forever (no
		// reaper backstop, per pkg/agent/task_executor.go's runTask comment),
		// so this Eventually is the actual regression assertion.
		var matched gen.TaskRun
		require.Eventually(t, func() bool {
			w := getTaskRuns(t, api, tsk.Id)
			if w.Code != http.StatusOK {
				return false
			}
			var runs []gen.TaskRun
			if err := json.Unmarshal(w.Body.Bytes(), &runs); err != nil {
				return false
			}
			for _, run := range runs {
				if run.OccurrenceMs != nil && *run.OccurrenceMs == occMs &&
					(run.Status == gen.TaskRunStatusDone || run.Status == gen.TaskRunStatusFailed) {
					matched = run
					return true
				}
			}
			return false
		}, 10*time.Second, 20*time.Millisecond,
			"the occurrence run must round-trip via GET /tasks/{id}/runs and reach a terminal status — "+
				"a spurious request-context cancel would strand it in_progress forever")

		assert.Equal(t, tsk.Id, matched.TaskId)
		assert.Equal(t, gen.TaskRunKindManual, matched.Kind, "Run-now always opens a manual-kind run")
		require.NotNil(t, matched.EndedAt, "a terminal run must carry ended_at")
	})

	t.Run("normal re-run: POST with empty body returns 202 and reaches a terminal status", func(t *testing.T) {
		// The prior subtest's run mirrored the task to a terminal status, so
		// SpawnReset (called inside StartOccurrenceRun) is free to reset it
		// to `next` again — mirrors BDD scenario 3 ("re-run a failed
		// once-task") from task-run-history-spec.md §5.
		resp, err := http.Post(srv.URL+"/api/v1/tasks/"+tsk.Id+"/runs", "application/json", http.NoBody)
		require.NoError(t, err, "POST /tasks/{id}/runs with an empty body over the real server must succeed")
		respBody, readErr := io.ReadAll(resp.Body)
		require.NoError(t, readErr)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusAccepted, resp.StatusCode, "body=%s", string(respBody))

		require.Eventually(t, func() bool {
			w := getTaskRuns(t, api, tsk.Id)
			if w.Code != http.StatusOK {
				return false
			}
			var runs []gen.TaskRun
			if err := json.Unmarshal(w.Body.Bytes(), &runs); err != nil {
				return false
			}
			for _, run := range runs {
				if run.OccurrenceMs == nil &&
					(run.Status == gen.TaskRunStatusDone || run.Status == gen.TaskRunStatusFailed) {
					return true
				}
			}
			return false
		}, 10*time.Second, 20*time.Millisecond,
			"the ad-hoc re-run (empty body) must round-trip via GET /tasks/{id}/runs and reach a terminal status")
	})
}
