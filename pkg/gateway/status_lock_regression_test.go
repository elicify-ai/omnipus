//go:build !cgo

// Regression tests for concurrent PATCH + task.update on same task.
//
// Sprint 2 changes:
//   - #407: concurrent PUT → concurrent PATCH (method replaced, same invariant).
//   - #408/#409: PUT status="active" → 403 DELETED (design-removed: "active" removed,
//     "in_progress" is directly settable via PATCH).
//   - #410: /start defer refactor test DELETED (/start endpoint removed in Sprint 2).
//   - TestBoardTaskStatus_TypedConstants REWRITTEN to use task.Status (unified vocab).
//   - TestTask_PATCH_OtherStatuses_Allowed REWRITTEN to use 7-state vocabulary.
//
// Traces to: project-task-management-level1-spec.md — #407/#408/#409/#410

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// ── #407: concurrent PATCH + task.update on same task ──────────────────────

// TestTask_ConcurrentPATCHAndUpdate_NoRace runs N gateway PATCH requests all
// targeting the same task id concurrently under -race.
//
// The fix in #407 adds TaskFileLock.Get(id) in both the gateway PATCH handler
// and the sysagent system.task.update tool. This test covers the gateway side:
// N concurrent PATCHes must leave the task in a consistent state.
//
// Sprint 2: PUT replaced by PATCH; "active" removed; "next" is the status.
//
// BDD:
//
//	Given a task in status=inbox,
//	When 20 goroutines concurrently PATCH status=next on the task,
//	Then no data race occurs (verified by -race) and the final on-disk
//	status is "next" (all writes preserved the same intended value).
//
// Traces to: pkg/gateway/rest_tasks.go HandleTasks PATCH — #407 race fix
func TestTask_ConcurrentPATCHAndUpdate_NoRace(t *testing.T) {
	const N = 20
	api := newTestRestAPIWithHome(t)
	tsk := createTaskViaAPI(t, api, "RaceTask_"+t.Name(), "")

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Include description so the "partial task" guard allows next.
			body := `{"status":"next","description":"ready to run"}`
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+tsk.Id,
				strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/tasks/" + tsk.Id
			api.HandleTasks(w, r)
			// Accept 200 or any valid response; the important thing is no data race.
			_ = w.Code
		}()
	}
	wg.Wait()

	// After all concurrent PATCHes, the on-disk task must be readable and valid JSON.
	diskTask, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err, "task must be readable after concurrent PATCHes")
	assert.Equal(t, task.StatusNext, diskTask.Status,
		"after concurrent PATCH status=next, disk status must be 'next'")
}

// TestTask_ConcurrentUpdate_WithFileLock_NoRace tests the striped-lock
// serialization by running N concurrent PATCHes — proving the lock prevents
// interleaved read-modify-write corruption.
//
// Traces to: pkg/task/lock.go TaskFileLock — #407
func TestTask_ConcurrentUpdate_WithFileLock_NoRace(t *testing.T) {
	const N = 10
	api := newTestRestAPIWithHome(t)
	tsk := createTaskViaAPI(t, api, "FileLockTask_"+t.Name(), "")

	// All goroutines PATCH status=next — the final on-disk JSON must be parseable.
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+tsk.Id,
				strings.NewReader(`{"status":"next","description":"concurrent update"}`))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/tasks/" + tsk.Id
			api.HandleTasks(w, r)
		}()
	}
	wg.Wait()

	diskTask, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err, "task must be valid JSON after concurrent locked updates")
	assert.Equal(t, task.StatusNext, diskTask.Status,
		"after concurrent PATCH, status must be next (last write wins, no corruption)")

	// Differentiation: the final status must match the intended value from the PATCH.
	// Correct lock enforcement means the status is "next", not a mix of statuses.
	assert.NotEqual(t, task.StatusInbox, diskTask.Status,
		"concurrent PATCH to 'next' must not leave task stuck in 'inbox' (lock enforcement)")
}

// ── Task Status typed constants ───────────────────────────────────────────────

// TestTaskStatus_TypedConstants asserts that the unified task.Status typed
// constants have the correct string values and are distinct.
//
// Sprint 2: replaces TestBoardTaskStatus_TypedConstants which used boardtask.Status.
// The unified 7-state vocabulary replaces the old GTD 6-state + workflow vocab.
//
// BDD: task.Status constants must have the expected string values.
// Traces to: pkg/task/task.go Status type — unified vocabulary
func TestTaskStatus_TypedConstants(t *testing.T) {
	tests := []struct {
		name   string
		status task.Status
		want   string
	}{
		{"inbox", task.StatusInbox, "inbox"},
		{"next", task.StatusNext, "next"},
		{"planning", task.StatusPlanning, "planning"},
		{"in_progress", task.StatusInProgress, "in_progress"},
		{"blocked", task.StatusBlocked, "blocked"},
		{"done", task.StatusDone, "done"},
		{"failed", task.StatusFailed, "failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, task.Status(tc.want), tc.status,
				"task.Status(%s) must equal %q", tc.name, tc.want)
			assert.True(t, task.IsValidStatus(tc.status),
				"IsValidStatus(%q) must return true for canonical status", tc.status)
		})
	}

	// Differentiation: statuses are distinct from each other.
	assert.NotEqual(t, task.StatusInbox, task.StatusNext, "inbox != next")
	assert.NotEqual(t, task.StatusBlocked, task.StatusInProgress,
		"blocked != in_progress (unified vocab replaces waiting/active)")
	assert.NotEqual(t, task.StatusDone, task.StatusFailed, "done != failed")

	// The old "active" and "waiting" strings must NOT be valid statuses.
	assert.False(t, task.IsValidStatus("active"),
		"'active' must not be a valid status in the unified vocabulary (removed in Sprint 2)")
	assert.False(t, task.IsValidStatus("waiting"),
		"'waiting' must not be a valid status in the unified vocabulary (removed in Sprint 2)")
}

// ── PATCH status vocabulary ───────────────────────────────────────────────────

// TestTask_PATCH_AllStatuses_Allowed asserts that each of the 7 unified statuses
// is reachable via legal lifecycle transitions, and that `blocked` is NOT directly
// settable via PATCH (it is a derived side-state — Fix #6 gateway seam guard).
//
// Sprint 2 lifecycle: inbox → next → planning | in_progress → done | failed.
// inbox → failed is also allowed directly. `blocked` is derived from blocked_by
// deps and is rejected at the gateway seam with 400.
//
// Differentiation: testing two different paths proves neither is hardcoded.
//
// Traces to: pkg/gateway/rest_tasks.go HandleTasks PATCH — Sprint 2 status rules
func TestTask_PATCH_AllStatuses_Allowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	// inbox → next (needs description for the partial-task guard).
	tskNext := createTaskViaAPI(t, api, "Task_next", wsID)
	wNext := patchTask(t, api, tskNext.Id, `{"status":"next","description":"ready to go"}`)
	assert.Equal(t, http.StatusOK, wNext.Code,
		"PATCH inbox→next must return 200; body=%s", wNext.Body.String())

	// inbox → next → planning.
	tskPlanning := createTaskViaAPI(t, api, "Task_planning", wsID)
	wPN := patchTask(t, api, tskPlanning.Id, `{"status":"next","description":"ready"}`)
	require.Equal(t, http.StatusOK, wPN.Code, "inbox→next; body=%s", wPN.Body.String())
	wPlanning := patchTask(t, api, tskPlanning.Id, `{"status":"planning"}`)
	assert.Equal(t, http.StatusOK, wPlanning.Code,
		"PATCH next→planning must return 200; body=%s", wPlanning.Body.String())

	// inbox → next → in_progress (directly settable, no /start required in Sprint 2).
	tskInProg := createTaskViaAPI(t, api, "Task_in_progress", wsID)
	wIPn := patchTask(t, api, tskInProg.Id, `{"status":"next","description":"doing it"}`)
	require.Equal(t, http.StatusOK, wIPn.Code, "inbox→next; body=%s", wIPn.Body.String())
	wInProg := patchTask(t, api, tskInProg.Id, `{"status":"in_progress"}`)
	assert.Equal(t, http.StatusOK, wInProg.Code,
		"PATCH next→in_progress must return 200 (directly settable, no /start required); body=%s",
		wInProg.Body.String())

	// inbox → next → in_progress → done.
	tskDone := createTaskViaAPI(t, api, "Task_done", wsID)
	patchTask(t, api, tskDone.Id, `{"status":"next","description":"done path"}`)
	patchTask(t, api, tskDone.Id, `{"status":"in_progress"}`)
	wDone := patchTask(t, api, tskDone.Id, `{"status":"done"}`)
	assert.Equal(t, http.StatusOK, wDone.Code,
		"PATCH in_progress→done must return 200; body=%s", wDone.Body.String())

	// inbox → failed (legal direct transition).
	tskFailed := createTaskViaAPI(t, api, "Task_failed", wsID)
	wFailed := patchTask(t, api, tskFailed.Id, `{"status":"failed"}`)
	assert.Equal(t, http.StatusOK, wFailed.Code,
		"PATCH inbox→failed must return 200; body=%s", wFailed.Body.String())

	// `blocked` is a derived side-state → PATCH status=blocked must return 400 (Fix #6).
	tskBlocked := createTaskViaAPI(t, api, "Task_blocked_guard", wsID)
	wBlocked := patchTask(t, api, tskBlocked.Id, `{"status":"blocked"}`)
	assert.Equal(t, http.StatusBadRequest, wBlocked.Code,
		"PATCH status=blocked must return 400 (derived side-state; Fix #6 gateway seam guard); body=%s",
		wBlocked.Body.String())

	// PATCH with invalid/old status "active" → 400 (not in the 7-state vocabulary).
	tskActive := createTaskViaAPI(t, api, "OldActiveTask", wsID)
	wActive := patchTask(t, api, tskActive.Id, `{"status":"active"}`)
	assert.Equal(t, http.StatusBadRequest, wActive.Code,
		"PATCH status=active must return 400 (removed in Sprint 2, not a valid status); body=%s",
		wActive.Body.String())

	// PATCH with old "waiting" → 400.
	tskWaiting := createTaskViaAPI(t, api, "OldWaitingTask", wsID)
	wWaiting := patchTask(t, api, tskWaiting.Id, `{"status":"waiting"}`)
	assert.Equal(t, http.StatusBadRequest, wWaiting.Code,
		"PATCH status=waiting must return 400 (removed in Sprint 2, use 'blocked' instead); body=%s",
		wWaiting.Body.String())

	// Differentiation: in_progress (valid) and active (invalid) get different codes.
	assert.NotEqual(t, wInProg.Code, wActive.Code,
		"valid status 'in_progress' and invalid status 'active' must return different codes")
}
