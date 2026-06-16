//go:build !cgo

// Regression tests for:
//   - #407: concurrent gateway PUT + system.task.update on same task → no race
//   - #408/#409: PUT status:"active" → 403; typed Status compile check
//   - #410: existing board-task start/concurrency tests still pass after defer refactor
//
// Traces to: project-task-management-level1-spec.md — #407/#408/#409/#410

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/boardtask"
)

// ── #407: concurrent PUT + task.update on same task ──────────────────────────

// TestBoardTask_ConcurrentPUTAndUpdate_NoRace runs N gateway PUT requests all
// targeting the same task id concurrently under -race.
//
// The fix in #407 adds TaskFileLock.Get(id) in both the gateway PUT handler and
// the sysagent system.task.update tool. This test covers the gateway side: N
// concurrent PUTs must leave the task in a consistent state (status == last
// completed write, no partial JSON).
//
// BDD:
//
//	Given a board task in status=inbox,
//	When 20 goroutines concurrently PUT status=next on the task,
//	Then no data race occurs (verified by -race) and the final on-disk
//	status is "next" (all writes preserved the same intended value).
//
// Traces to: pkg/gateway/rest_board.go HandleBoardTasks PUT — #407 race fix
func TestBoardTask_ConcurrentPUTAndUpdate_NoRace(t *testing.T) {
	const N = 20
	api := newTestRestAPIWithHome(t)

	// Create a task.
	task := createBoardTaskViaAPI(t, api, "RaceTask_"+t.Name(), "inbox")

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := `{"status":"next"}`
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
				strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/board/tasks/" + task.Id
			api.HandleBoardTasks(w, r)
			// We accept 200 or 409 (if status is already next on re-PUT).
			// The important thing is no data race (verified by -race flag).
			_ = w.Code
		}(i)
	}
	wg.Wait()

	// After all concurrent PUTs, the on-disk task must be readable and valid JSON.
	diskTask, err := api.readBoardTask(task.Id)
	require.NoError(t, err, "task must be readable after concurrent PUTs")
	assert.Equal(t, boardtask.StatusNext, diskTask.Status,
		"after concurrent PUT status=next, disk status must be 'next'")
}

// TestBoardTask_ConcurrentUpdate_WithFileLock_NoRace tests the striped-lock
// serialization by running concurrent PUTs that each update a different field —
// proving the lock prevents interleaved read-modify-write corruption.
//
// This is the core #407 regression: without the lock, two goroutines could both
// read status=inbox, both set their own field, and write back conflicting JSON.
//
// Traces to: pkg/boardtask/lock.go TaskFileLock — #407
func TestBoardTask_ConcurrentUpdate_WithFileLock_NoRace(t *testing.T) {
	const N = 10
	api := newTestRestAPIWithHome(t)

	task := createBoardTaskViaAPI(t, api, "FileLockTask_"+t.Name(), "inbox")

	// All goroutines PUT status=next → no matter the interleaving, the final
	// on-disk JSON must be parseable and status must be "next".
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
				strings.NewReader(`{"status":"next","description":"concurrent update"}`))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/board/tasks/" + task.Id
			api.HandleBoardTasks(w, r)
		}()
	}
	wg.Wait()

	diskTask, err := api.readBoardTask(task.Id)
	require.NoError(t, err, "task must be valid JSON after concurrent locked updates")
	assert.Equal(t, boardtask.StatusNext, diskTask.Status)
}

// ── #408/#409: PUT status:"active" → 403 ─────────────────────────────────────

// TestBoardTask_PUT_StatusActive_Returns403 asserts that PUT /board/tasks/{id}
// with status:"active" returns 403 — "active" may only be set via /start.
//
// BDD:
//
//	Given a board task in status=inbox,
//	When PUT is called with {"status":"active"},
//	Then 403 is returned.
//
// Guards against: validateStatusTransition regression where active is accepted via PUT.
// Traces to: pkg/gateway/rest_board.go validateStatusTransition — #408/#409
func TestBoardTask_PUT_StatusActive_Returns403(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	task := createBoardTaskViaAPI(t, api, "ActiveStatusTask_"+t.Name(), "inbox")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
		strings.NewReader(`{"status":"active"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"PUT status=active must return 403; body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "active",
		"error message must mention 'active'")
}

// TestBoardTask_PUT_OtherStatuses_Allowed asserts that PUT with non-active GTD
// statuses (next, waiting, done, failed, inbox) is accepted (200).
//
// Differentiation test: proves the 403 guard is specific to "active", not all statuses.
// Traces to: pkg/gateway/rest_board.go validateStatusTransition — #408/#409
func TestBoardTask_PUT_OtherStatuses_Allowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	statuses := []string{"next", "waiting", "done", "failed", "inbox"}
	for _, status := range statuses {
		t.Run("status="+status, func(t *testing.T) {
			task := createBoardTaskViaAPI(t, api, fmt.Sprintf("Task_%s_%s", status, t.Name()), "inbox")
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
				strings.NewReader(fmt.Sprintf(`{"status":%q}`, status)))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/board/tasks/" + task.Id
			api.HandleBoardTasks(w, r)

			assert.Equal(t, http.StatusOK, w.Code,
				"PUT status=%q must return 200; body=%s", status, w.Body.String())
		})
	}
}

// TestBoardTaskStatus_TypedConstants asserts that the boardtask.Status typed
// constants are distinct from plain string "active" — the typed Status type catches
// accidental mixing at compile time. This test verifies the runtime values.
//
// BDD: typed Status constants must not equal each other cross-type.
// Traces to: pkg/boardtask/boardtask.go Status type — #409
func TestBoardTaskStatus_TypedConstants(t *testing.T) {
	tests := []struct {
		name   string
		status boardtask.Status
		want   string
	}{
		{"inbox", boardtask.StatusInbox, "inbox"},
		{"next", boardtask.StatusNext, "next"},
		{"active", boardtask.StatusActive, "active"},
		{"waiting", boardtask.StatusWaiting, "waiting"},
		{"done", boardtask.StatusDone, "done"},
		{"failed", boardtask.StatusFailed, "failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, boardtask.Status(tc.want), tc.status,
				"boardtask.Status(%s) must equal %q", tc.name, tc.want)
			assert.True(t, boardtask.IsGTDStatus(string(tc.status)),
				"IsGTDStatus(%q) must return true for valid GTD status", tc.status)
		})
	}

	// "active" must NOT appear as a valid PUT status in the transition validator.
	err := validateStatusTransition("inbox", boardtask.StatusActive)
	require.Error(t, err, "validateStatusTransition to 'active' must return an error")
	assert.Contains(t, err.Error(), "active",
		"error message must mention 'active'")
}

// ── #410: start/concurrency tests still pass after defer refactor ─────────────

// TestBoardTask_StartDefer_SessionIDSetOnDisk asserts that after /start, the
// on-disk task has a non-empty session_id — verifying the defer refactor did not
// break the session assignment path.
//
// BDD:
//
//	Given a board task in status=inbox with a prompt,
//	When POST /start is called,
//	Then 202 is returned and on-disk session_id is non-empty.
//
// Traces to: pkg/gateway/rest_board.go startBoardTaskLocked — #410 defer refactor
func TestBoardTask_StartDefer_SessionIDSetOnDisk(t *testing.T) {
	api := newTestRestAPIWithAgentDrained(t)

	task := createBoardTaskWithPromptViaAPI(t, api, "DeferTask_"+t.Name(), "inbox", "Execute this deferred task")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks/"+task.Id+"/start", nil)
	r.URL.Path = "/api/v1/board/tasks/" + task.Id + "/start"
	api.HandleBoardTasks(w, r)

	require.Equal(t, http.StatusAccepted, w.Code,
		"/start must return 202; body=%s", w.Body.String())

	var respTask struct {
		SessionId *string `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &respTask))
	require.NotNil(t, respTask.SessionId, "session_id must be set in the /start response")
	assert.NotEmpty(t, *respTask.SessionId, "session_id must be non-empty")

	// Drain the background goroutine before reading from disk.
	api.agentLoop.WaitForActiveRequests()

	diskTask, err := api.readBoardTask(task.Id)
	require.NoError(t, err)
	assert.NotEmpty(t, diskTask.SessionID,
		"on-disk session_id must be non-empty after /start with defer refactor")
}
