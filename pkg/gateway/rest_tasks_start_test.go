// rest_tasks_start_test.go — tests for the "Run/Start" task delegation fix.
//
// When PATCH /api/v1/tasks/{id} transitions status to in_progress AND the task
// has an assigned agent, the handler must immediately launch the agent via
// StartTaskNow, create a session (with WorkspaceID set), persist session_id on
// the task, and return session_id in the PATCH response.
//
// Traces to: task delegation bug — Run/Start never launched agent (session_id
// stayed null, "Open in Chat" button hidden, no session appeared).

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

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newTestRestAPIWithTaskExecutor creates a restAPI that has a live TaskExecutor
// wired via the agentLoop. The agentLoop has no registered agent with a real
// session store, so StartTaskNow will fail gracefully with "agent not found" —
// this is intentional: it tests the path where the executor IS wired but the
// agent doesn't exist, and confirms the PATCH still returns 200.
func newTestRestAPIWithTaskExecutor(t *testing.T) *restAPI {
	t.Helper()
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
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		taskLock:      task.TaskFileLock,
		taskExecutor:  agent.GetTaskExecutor(al),
	}
}

// getTaskStartedAt reads back a task via GET /api/v1/tasks/{id} and returns
// its started_at pointer (nil when unset). Used by the launch-revert tests
// below to confirm a reverted in_progress transition doesn't leave a phantom
// "started" timestamp behind (see the store's StartedAt auto-stamp in
// updateLocked).
func getTaskStartedAt(t *testing.T, api *restAPI, id string) *time.Time {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id, nil)
	r.URL.Path = "/api/v1/tasks/" + id
	api.HandleTasks(w, r)
	require.Equal(t, http.StatusOK, w.Code, "getTaskStartedAt GET must return 200; body=%s", w.Body.String())
	var tsk gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))
	return tsk.StartedAt
}

// getTaskFull reads back a task via GET /api/v1/tasks/{id} and returns the
// full wire struct. Used by the fresh-run-reset revert tests below, which
// need to assert on several fields (result, session_id, artifacts,
// completed_at, status) from a single consistent read.
func getTaskFull(t *testing.T, api *restAPI, id string) gen.Task {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id, nil)
	r.URL.Path = "/api/v1/tasks/" + id
	api.HandleTasks(w, r)
	require.Equal(t, http.StatusOK, w.Code, "getTaskFull GET must return 200; body=%s", w.Body.String())
	var tsk gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))
	return tsk
}

// advanceTaskToNext advances a task from inbox to next (adding a description for
// the partial-task guard).
func advanceTaskToNext(t *testing.T, api *restAPI, id string) {
	t.Helper()
	w := patchTask(t, api, id, `{"status":"next","description":"ready to run"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"inbox→next must succeed; body=%s", w.Body.String())
}

// TestHandleTaskPatch_InProgress_NoAgentID verifies that PATCH status=in_progress
// on a task with NO agent_id still returns 200 with status=in_progress and no
// session_id in the response (no executor launch attempted).
//
// BDD:
//
//	Given a task with no agent_id,
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"},
//	Then 200, status=in_progress, session_id is null/absent.
func TestHandleTaskPatch_InProgress_NoAgentID(t *testing.T) {
	api := newTestRestAPIWithTaskExecutor(t)
	wsID := ensureTestWorkspace(t, api)

	// Create a task without an agent assigned.
	tsk := createTaskViaAPI(t, api, "HumanTask", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"PATCH status=in_progress with no agent must return 200; body=%s", w.Body.String())

	var updated gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, gen.TaskStatusInProgress, updated.Status)
	assert.Nil(t, updated.SessionId,
		"session_id must be nil when task has no agent assigned")
}

// TestHandleTaskPatch_InProgress_StampsStartedAt verifies the fix for the
// Board card bug where "Started" never populated: PATCH status=in_progress
// via the "Start" button path (no agent assigned, so no StartTaskNow launch —
// isolates the store-level stamping from the executor) must persist a
// non-empty started_at, both in the PATCH response and on a subsequent GET.
//
// Traces to: UAT bug — Board task card's "Started" field always showed "—"
// even on completed tasks. Root cause: the REST PATCH "Start" action set
// status=in_progress via task.Store.Update, which never stamped StartedAt on
// that path (only TaskExecutor.ExecuteTask's ClaimForRun stamped it, and that
// path is only reached via scheduler/heartbeat dispatch — not the human
// "Start" click, which PATCHes status directly).
//
// BDD:
//
//	Given a task with no agent_id (isolates Update()'s auto-stamp from
//	     StartTaskNow's own launch machinery),
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"} (no started_at
//	     supplied by the client, matching the real frontend "Start" action),
//	Then 200, the response carries a non-empty started_at, and a subsequent
//	     GET returns the same started_at (it persisted to disk).
func TestHandleTaskPatch_InProgress_StampsStartedAt(t *testing.T) {
	api := newTestRestAPIWithTaskExecutor(t)
	wsID := ensureTestWorkspace(t, api)

	tsk := createTaskViaAPI(t, api, "StartedAtTask", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	before := time.Now().UTC()
	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"PATCH status=in_progress must return 200; body=%s", w.Body.String())

	var updated gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, gen.TaskStatusInProgress, updated.Status)
	require.NotNil(t, updated.StartedAt,
		"started_at must be populated in the PATCH response, not left nil")
	assert.False(t, updated.StartedAt.Before(before.Add(-time.Second)),
		"started_at must reflect the real transition time, not a stale/zero value")

	// Confirm it actually persisted (not just present in the response echo).
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+tsk.Id, nil)
	getR.URL.Path = "/api/v1/tasks/" + tsk.Id
	api.HandleTasks(getW, getR)
	require.Equal(t, http.StatusOK, getW.Code)

	var fetched gen.Task
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &fetched))
	require.NotNil(t, fetched.StartedAt, "started_at must survive a round-trip GET after the PATCH")
	assert.Equal(t, updated.StartedAt.Format(time.RFC3339), fetched.StartedAt.Format(time.RFC3339),
		"GET must return the same started_at the PATCH persisted")
}

// TestHandleTaskPatch_InProgress_NoOpDoesNotRestampStartedAt verifies that a
// second PATCH status=in_progress on an already-in_progress task (the
// idempotent no-op case) does NOT re-stamp started_at — the field must
// reflect the task's FIRST real execution start, not the most recent no-op
// PATCH.
//
// BDD:
//
//	Given a task already in_progress with a started_at timestamp,
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"} again,
//	Then 200 and started_at is unchanged from the first transition.
func TestHandleTaskPatch_InProgress_NoOpDoesNotRestampStartedAt(t *testing.T) {
	api := newTestRestAPIWithTaskExecutor(t)
	wsID := ensureTestWorkspace(t, api)

	tsk := createTaskViaAPI(t, api, "NoOpRestampTask", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	w1 := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w1.Code)
	var first gen.Task
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &first))
	require.NotNil(t, first.StartedAt, "first transition must stamp started_at")

	// Sleep past RFC 3339 second-precision so a bug that re-stamps would be
	// observable as a strictly later timestamp.
	time.Sleep(1100 * time.Millisecond)

	w2 := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w2.Code)
	var second gen.Task
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &second))
	require.NotNil(t, second.StartedAt, "started_at must remain set on the no-op PATCH")
	assert.Equal(t, first.StartedAt.Format(time.RFC3339), second.StartedAt.Format(time.RFC3339),
		"a same-status no-op PATCH must not re-stamp started_at")
}

// TestHandleTaskPatch_InProgress_NilTaskExecutor verifies that PATCH
// status=in_progress with an agent_id but a nil taskExecutor returns 503
// Service Unavailable (gateway degraded — logs a warn, rejects the transition
// so the task is not left stranded as in_progress).
//
// BDD:
//
//	Given a task with an agent_id (bypassed via store) and taskExecutor=nil,
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"},
//	Then 503 Service Unavailable and the task is NOT left as in_progress.
func TestHandleTaskPatch_InProgress_NilTaskExecutor(t *testing.T) {
	api := newTestRestAPIWithHome(t) // does NOT wire taskExecutor
	wsID := ensureTestWorkspace(t, api)

	tsk := createTaskViaAPI(t, api, "AgentTaskNoExec", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	// Inject agent_id directly via the store, bypassing the registry FK guard
	// (registry is empty in this test setup so the REST PATCH would reject "mia"
	// with 400 before reaching the executor-nil branch).
	agentID := "synthetic-agent"
	_, err := api.taskStore.Update(tsk.Id, task.Patch{AgentID: &agentID})
	require.NoError(t, err, "direct agent_id set must succeed")

	// PATCH status=in_progress with nil executor must return 503.
	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"nil taskExecutor must return 503; body=%s", w.Body.String())

	// Task must be reverted, not left as in_progress.
	statusAfter := getTaskStatus(t, api, tsk.Id)
	assert.Equal(t, gen.TaskStatusNext, statusAfter,
		"task must revert to 'next' after nil-executor 503 (not left as in_progress)")

	// StartedAt must be cleared, not left carrying a "started" timestamp from
	// an attempt that never actually ran (the in_progress patch above stamped
	// it before the nil-executor check reverted the status).
	startedAtAfter := getTaskStartedAt(t, api, tsk.Id)
	assert.Nil(t, startedAtAfter,
		"task must have StartedAt cleared after nil-executor revert, not a phantom 'started' timestamp")
}

// TestHandleTaskPatch_InProgress_WithKnownAgent verifies that PATCH
// status=in_progress on a task with a known agent_id returns 200 with
// status=in_progress. StartTaskNow succeeds (agent is in the registry, task is
// found by the executor's store); the goroutine runs in the background after
// the PATCH returns.
//
// BDD:
//
//	Given a task with agent_id="mia" (the test registry's one seeded agent),
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"},
//	Then 200 and status=in_progress.
func TestHandleTaskPatch_InProgress_WithKnownAgent(t *testing.T) {
	// Use aligned stores so the executor can find the task by ID.
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	// "mia" is newTestRestAPIAlignedStoresWithProvider's one explicitly seeded
	// chat-target agent (the retired "main" sentinel used to be registered
	// implicitly regardless of cfg — see that helper's comment) but is NOT
	// part of defaultWorkspaceTeam's fixed base-roster candidate list, so it
	// must be put on this workspace's team explicitly for
	// validateTaskAgentID's team-membership check to accept it below.
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})

	tsk := createTaskViaAPI(t, api, "AgentTask", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	// First assign the agent_id so the follow-up PATCH can transition to in_progress.
	wAssign := patchTask(t, api, tsk.Id, `{"agent_id":"mia"}`)
	require.Equal(t, http.StatusOK, wAssign.Code,
		"assigning agent_id=mia must return 200; body=%s", wAssign.Body.String())

	// Now transition to in_progress — StartTaskNow is called synchronously; the
	// goroutine may fail later (no real LLM in test), but the PATCH response must
	// be 200 with status=in_progress.
	body := `{"status":"in_progress"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+tsk.Id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks/" + tsk.Id
	api.HandleTasks(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"PATCH status=in_progress must return 200; body=%s", w.Body.String())

	var updated gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, gen.TaskStatusInProgress, updated.Status,
		"task status must be in_progress after PATCH")

	// Teardown race guard: StartTaskNow launched a background goroutine that
	// writes session + task files into the test's TempDir. Register a cleanup
	// (LIFO → runs before al.Close and before TempDir removal) that polls until
	// the task reaches a terminal state, guaranteeing all goroutine writes are
	// complete before the temp directory is removed.
	//
	// The mock LLM returns immediately so the goroutine finishes in well under
	// a second on any machine; the 10 s bound is a generous safety margin.
	taskID := tsk.Id
	t.Cleanup(func() {
		require.Eventually(t, func() bool {
			s := getTaskStatus(t, api, taskID)
			return s == gen.TaskStatusDone || s == gen.TaskStatusFailed
		}, 10*time.Second, 20*time.Millisecond,
			"task goroutine must reach a terminal state before test teardown")
	})
}

// TestTransition_DoneRepeatingTaskRunNow is FIX 2's regression test (the
// second of the two 7-reviewer-gate findings on commit 1b3d4b3d): a
// done-but-still-armed recurring/every task's manual "Run now" action (PATCH
// status=in_progress) must succeed — mirroring the fact that the scheduler
// itself keeps a repeating trigger's series armed past a done status
// (pkg/agent/task_trigger.go's OnTaskUpserted). The narrow validateTransition
// carve-out (pkg/task/store.go) must NOT unfreeze `done` for a task whose
// trigger does not repeat (manual/once) — that must stay a 400 exactly as
// before.
func TestTransition_DoneRepeatingTaskRunNow(t *testing.T) {
	t.Run("done every task: PATCH status=in_progress is rejected (400, use POST /runs)", func(t *testing.T) {
		api := newTestRestAPIAlignedStores(t)
		wsID := ensureTestWorkspace(t, api)
		setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})

		body := fmt.Sprintf(
			`{"title":"RerunMe","action":"llm","workspace_id":%q,"agent_id":"mia","trigger":{"type":"every","config":{"every_ms":60000}}}`,
			wsID,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
		var tsk gen.Task
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))

		advanceTaskToDone(t, api, tsk.Id)
		doneTask := getTaskStatus(t, api, tsk.Id)
		require.Equal(t, gen.TaskStatusDone, doneTask, "precondition: task must be done")
		sessionBefore := getTaskSessionID(t, api, tsk.Id)

		// ADR-050 / task-run-history-spec.md §3.4: the PATCH-based "fresh-run
		// reset" re-run path is retired. A done+repeating task's "Run now" must
		// now be rejected via PATCH — re-running goes exclusively through
		// POST /api/v1/tasks/{id}/runs.
		wRerun := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
		require.Equal(t, http.StatusBadRequest, wRerun.Code,
			"a done EVERY (repeating) task's PATCH status=in_progress must be rejected; body=%s", wRerun.Body.String())
		var errResp gen.ErrorResponse
		require.NoError(t, json.Unmarshal(wRerun.Body.Bytes(), &errResp))
		assert.Contains(t, errResp.Error, "runs",
			"rejection message must point callers to POST /api/v1/tasks/{id}/runs")

		// The reject happens before any store write — the task must be left
		// completely untouched.
		statusAfter := getTaskStatus(t, api, tsk.Id)
		assert.Equal(t, gen.TaskStatusDone, statusAfter,
			"task must remain done after the rejected PATCH (no write occurred)")
		sessionAfter := getTaskSessionID(t, api, tsk.Id)
		assert.Equal(t, sessionBefore, sessionAfter,
			"session_id must be unchanged after the rejected PATCH (no fresh-run reset, no write)")
	})

	t.Run("done manual (no trigger) task: PATCH status=in_progress still 400s", func(t *testing.T) {
		api := newTestRestAPIAlignedStores(t)
		wsID := ensureTestWorkspace(t, api)

		tsk := createTaskViaAPI(t, api, "OneShotDone", wsID)
		advanceTaskToDone(t, api, tsk.Id)
		require.Equal(t, gen.TaskStatusDone, getTaskStatus(t, api, tsk.Id), "precondition: task must be done")

		wRerun := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
		assert.Equal(t, http.StatusBadRequest, wRerun.Code,
			"a done task with a NON-repeating trigger must stay frozen; body=%s", wRerun.Body.String())
		var errResp gen.ErrorResponse
		require.NoError(t, json.Unmarshal(wRerun.Body.Bytes(), &errResp))
		assert.NotEmpty(t, errResp.Error)
	})
}

// TestHandleTaskPatch_RepeatingRunNow_RejectedLeavesDataUnchanged verifies
// ADR-050 / task-run-history-spec.md §3.4: PATCH-based re-run of a done
// REPEATING task is rejected with 400 before any store write, so a task
// carrying a prior run's result/session_id/artifacts/completed_at is left
// completely untouched. This retires the old "fresh-run reset + revert on
// failure" behavior (handleTaskPatch used to wipe session_id/result/
// artifacts/completed_at before calling StartTaskNow, then restore them on
// failure) — that whole path is gone, so the "data preserved" guarantee now
// holds trivially: the request never reaches a write in the first place.
//
// Traces to: ADR-050 / docs/internal/plan/task-run-history-spec.md §3.4 —
// the PATCH-based "fresh-run reset" re-run path is retired; re-running a
// repeating task goes exclusively through POST /api/v1/tasks/{id}/runs.
//
// BDD:
//
//	Given a done REPEATING task carrying a prior run's result, session_id,
//	     artifacts, and completed_at,
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"} ("Run now"),
//	Then 400 Bad Request (error message points to POST /runs), and a
//	     subsequent GET shows status=done with result/session_id/artifacts/
//	     completed_at ALL UNCHANGED from their pre-PATCH values.
func TestHandleTaskPatch_RepeatingRunNow_RejectedLeavesDataUnchanged(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})

	body := fmt.Sprintf(
		`{"title":"RerunFailPreserve","action":"llm","workspace_id":%q,"agent_id":"mia","trigger":{"type":"every","config":{"every_ms":60000}}}`,
		wsID,
	)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var tsk gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))

	advanceTaskToDone(t, api, tsk.Id)
	require.Equal(t, gen.TaskStatusDone, getTaskStatus(t, api, tsk.Id), "precondition: task must be done")

	// Seed distinguishable "prior completed run" data directly on the store so
	// an unchanged-vs-wiped comparison is unambiguous (advanceTaskToDone's own
	// mock-LLM result text would work too, but a synthetic, obviously-non-empty
	// fixture makes the assertions below self-evidently about THIS data, not
	// incidental mock-provider output).
	seededResult := "the completed run's important result text"
	seededSessionID := "prior-run-session-xyz"
	seededArtifacts := []string{"report.md", "chart.png"}
	seededCompletedAt := "2026-01-01T00:00:00Z"
	_, seedErr := api.taskStore.Update(tsk.Id, task.Patch{
		Result:      &seededResult,
		SessionID:   &seededSessionID,
		Artifacts:   &seededArtifacts,
		CompletedAt: &seededCompletedAt,
	})
	require.NoError(t, seedErr, "seeding prior-run data must succeed")

	before := getTaskFull(t, api, tsk.Id)
	require.Equal(t, seededResult, deref(before.Result), "precondition: seeded result must be readable")
	require.Equal(t, seededSessionID, deref(before.SessionId), "precondition: seeded session_id must be readable")
	require.NotNil(t, before.Artifacts)
	require.Len(t, *before.Artifacts, 2, "precondition: seeded artifacts must be readable")
	require.NotNil(t, before.CompletedAt, "precondition: seeded completed_at must be readable")

	wRerun := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusBadRequest, wRerun.Code,
		"PATCH status=in_progress on a done repeating task must be rejected; body=%s", wRerun.Body.String())
	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(wRerun.Body.Bytes(), &errResp))
	assert.Contains(t, errResp.Error, "runs",
		"rejection message must point callers to POST /api/v1/tasks/{id}/runs")

	after := getTaskFull(t, api, tsk.Id)
	assert.Equal(t, gen.TaskStatusDone, after.Status,
		"task must remain done after the rejected PATCH (no write occurred)")
	assert.Equal(t, seededResult, deref(after.Result),
		"result must be UNCHANGED after the rejected PATCH — no write ever reached the store")
	assert.Equal(t, seededSessionID, deref(after.SessionId),
		"session_id must be UNCHANGED after the rejected PATCH — the prior run's chat link must not be lost")
	require.NotNil(t, after.Artifacts, "artifacts must be unchanged, not wiped, after the rejected PATCH")
	assert.ElementsMatch(t, seededArtifacts, *after.Artifacts,
		"artifacts must be unchanged after the rejected PATCH")
	require.NotNil(t, after.CompletedAt, "completed_at must be unchanged, not wiped, after the rejected PATCH")
	assert.Equal(t, before.CompletedAt, after.CompletedAt,
		"completed_at must be UNCHANGED after the rejected PATCH")
}

// TestHandleTaskPatch_InProgress_Idempotent verifies that a second PATCH with
// status=in_progress on a task that is already in_progress with a session_id
// does NOT create a second session (idempotent path).
//
// BDD:
//
//	Given a task already in_progress with session_id="sess-abc",
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"} again,
//	Then 200 and the response session_id is still "sess-abc" (not a new one).
func TestHandleTaskPatch_InProgress_Idempotent(t *testing.T) {
	api := newTestRestAPIWithTaskExecutor(t)
	wsID := ensureTestWorkspace(t, api)

	// Create a task and advance it to in_progress via the normal API path first.
	tsk := createTaskViaAPI(t, api, "IdempotentTask", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	// Move to in_progress first time (StartTaskNow will fail "agent not found"
	// because we have no real agent; session_id will be empty after the first patch).
	w1 := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w1.Code)
	var first gen.Task
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &first))
	require.Equal(t, gen.TaskStatusInProgress, first.Status)

	// Directly seed a session_id on the task file to simulate a session being set.
	// (In production, StartTaskNow does this; here we simulate a running state.)
	seededSessionID := "test-session-idempotency-xyz"
	_, updateErr := api.taskStore.Update(tsk.Id, task.Patch{SessionID: &seededSessionID})
	require.NoError(t, updateErr)

	// Second PATCH: task is already in_progress with a session_id. The handler
	// must detect preUpdateStatus==in_progress and skip StartTaskNow entirely.
	w2 := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w2.Code,
		"second PATCH status=in_progress must return 200; body=%s", w2.Body.String())

	var second gen.Task
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &second))
	assert.Equal(t, gen.TaskStatusInProgress, second.Status)
	// The session_id from the first run must be preserved (not overwritten).
	require.NotNil(t, second.SessionId,
		"session_id must be returned on second PATCH")
	assert.Equal(t, seededSessionID, *second.SessionId,
		"session_id must be the seeded value, not a new one (idempotent)")
}

// TestHandleTaskPatch_StatusPreservedWhenExecutorNil verifies that when taskExecutor
// is nil AND no agent is assigned, the status is still persisted correctly.
//
// BDD:
//
//	Given a task with no agent and taskExecutor=nil,
//	When PATCH /tasks/{id} with {"status":"in_progress"},
//	Then 200 and status=in_progress persisted on disk.
func TestHandleTaskPatch_StatusPreservedWhenExecutorNil(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	tsk := createTaskViaAPI(t, api, "NoExecTask", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"PATCH status=in_progress (no exec, no agent) must return 200; body=%s", w.Body.String())

	// Verify persistence: read back via GET.
	statusAfter := getTaskStatus(t, api, tsk.Id)
	assert.Equal(t, gen.TaskStatusInProgress, statusAfter,
		"in_progress status must persist to disk even when taskExecutor is nil")
}

// newTestRestAPIAlignedStores creates a restAPI where api.taskStore and the
// internal taskExecutor store both resolve to the same on-disk directory. This
// is required for tests that call StartTaskNow (which reads from the executor's
// store) and then verify the task via the API (which reads from api.taskStore).
//
// The agent loop puts the task store at filepath.Dir(workspace)/tasks. We pass
// that exact path to task.New so the two stores share the same files.
//
// Uses restMockProvider (bare &providers.LLMResponse{}, no parseable
// TASK_STATUS signal) — fine for tests that only need a task to reach SOME
// terminal state. A test that must prove genuine successful completion
// content (e.g. distinguishing a real Done from a context-cancellation
// failure that also lands on a terminal status) needs
// newTestRestAPIAlignedStoresWithProvider with a scripted provider instead.
func newTestRestAPIAlignedStores(t *testing.T) *restAPI {
	t.Helper()
	return newTestRestAPIAlignedStoresWithProvider(t, &restMockProvider{})
}

// newTestRestAPIAlignedStoresWithProvider is newTestRestAPIAlignedStores
// parameterized on the LLM provider, so a caller that needs the agent's
// response content to be genuine and meaningful (not the bare no-op
// restMockProvider) can supply its own scripted providers.LLMProvider.
func newTestRestAPIAlignedStoresWithProvider(t *testing.T, provider providers.LLMProvider) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	// The agent loop will place its task store at filepath.Dir(workspace)/tasks.
	// Set workspace to tmpDir/workspace so the task store lands at tmpDir/tasks,
	// matching what we pass to task.New below.
	workspaceDir := tmpDir + "/workspace"
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			// A single real, chat-target agent ("mia") so tests that need "a
			// known agent in the registry" have one to assign tasks to. The
			// retired "main" sentinel used to be registered implicitly
			// regardless of this list (pkg/agent/registry.go's old always-on
			// fallback); that sentinel is gone with no back-compat, so this
			// harness must seed a real agent explicitly instead — see
			// TestHandleTaskPatch_InProgress_WithKnownAgent and friends below.
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[{"id":"mia"}]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, provider)

	// Both the executor and the API use the same tasks directory.
	sharedTaskStore := task.New(tmpDir + "/tasks")
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     sharedTaskStore,
		taskLock:      task.TaskFileLock,
		taskExecutor:  agent.GetTaskExecutor(al),
	}
}

// TestHandleTaskPatch_RunTask_StartErrRevertsStatus verifies that when
// StartTaskNow fails for any reason, PATCH status=in_progress returns a
// non-2xx status code AND reverts the task to its prior status so it is not
// left stranded as in_progress.
//
// BDD:
//
//	Given a task with an agent_id not present in the registry,
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"},
//	Then non-2xx (500), and the task is NOT in_progress.
func TestHandleTaskPatch_RunTask_StartErrRevertsStatus(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)

	tsk := createTaskViaAPI(t, api, "StartErrTask", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	// Directly inject a non-existent agent_id via the store, bypassing the REST
	// FK guard. This makes StartTaskNow fail at the registry check with 500.
	unknownAgent := "nonexistent-agent-xyz"
	_, storeErr := api.taskStore.Update(tsk.Id, task.Patch{AgentID: &unknownAgent})
	require.NoError(t, storeErr, "direct agent_id set must succeed")

	// PATCH status=in_progress — StartTaskNow will fail (agent not in registry).
	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	assert.NotEqual(t, http.StatusOK, w.Code,
		"StartTaskNow failure must return non-2xx; body=%s", w.Body.String())
	assert.GreaterOrEqual(t, w.Code, 400,
		"response code must be >=400; body=%s", w.Body.String())

	// Task must be reverted to its prior status, not left as in_progress.
	statusAfter := getTaskStatus(t, api, tsk.Id)
	assert.Equal(t, gen.TaskStatusNext, statusAfter,
		"task must revert to 'next' after StartTaskNow error (not left as in_progress)")

	// StartedAt must be cleared, not left carrying a "started" timestamp from
	// an attempt that never actually ran (the in_progress patch above stamped
	// it before StartTaskNow's failure reverted the status).
	startedAtAfter := getTaskStartedAt(t, api, tsk.Id)
	assert.Nil(t, startedAtAfter,
		"task must have StartedAt cleared after StartTaskNow-error revert, not a phantom 'started' timestamp")
}

// TestHandleTaskPatch_RunTask_DispatchCapReturns409 verifies that when the
// dispatch semaphore is exhausted, PATCH status=in_progress returns 409 Conflict
// (ErrDispatchCapReached) AND leaves the task in its prior status.
//
// BDD:
//
//	Given a task with a known agent_id and all dispatch slots occupied,
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"},
//	Then 409 Conflict and the task is NOT in_progress.
func TestHandleTaskPatch_RunTask_DispatchCapReturns409(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	// See TestHandleTaskPatch_InProgress_WithKnownAgent: "main" must be put on
	// the workspace team explicitly for the team-membership check to accept it.
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})

	tsk := createTaskViaAPI(t, api, "CapTest", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	wAssign := patchTask(t, api, tsk.Id, `{"agent_id":"mia"}`)
	require.Equal(t, http.StatusOK, wAssign.Code,
		"assign agent_id must succeed; body=%s", wAssign.Body.String())

	te := api.taskExecutor
	require.NotNil(t, te, "taskExecutor must not be nil in this test")

	// Exhaust all semaphore slots by acquiring each one. capN ≥ 1 is guaranteed.
	// All slots are released at end of test via Cleanup.
	capN := te.DispatchSemaCap()
	releases := make([]func(), 0, capN)
	for i := range capN {
		ok, rel := te.TryAcquireDispatchSema()
		require.True(t, ok, "sema slot %d/%d must be available before exhaustion", i+1, capN)
		releases = append(releases, rel)
	}
	t.Cleanup(func() {
		for _, rel := range releases {
			if rel != nil {
				rel()
			}
		}
	})

	// PATCH status=in_progress — dispatch cap is exhausted → 409.
	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	assert.Equal(t, http.StatusConflict, w.Code,
		"exhausted dispatch cap must return 409; body=%s", w.Body.String())

	// Task must be reverted to its prior status, not left as in_progress.
	statusAfter := getTaskStatus(t, api, tsk.Id)
	assert.Equal(t, gen.TaskStatusNext, statusAfter,
		"task must revert to 'next' after 409 (not left as in_progress)")

	// StartedAt must be cleared, not left carrying a "started" timestamp from
	// an attempt that never actually ran (the in_progress patch above stamped
	// it before the dispatch-cap failure reverted the status).
	startedAtAfter := getTaskStartedAt(t, api, tsk.Id)
	assert.Nil(t, startedAtAfter,
		"task must have StartedAt cleared after dispatch-cap revert, not a phantom 'started' timestamp")
}

// TestHandleTaskPatch_RunTask_NilExecutorReturns503 verifies that PATCH
// status=in_progress returns 503 Service Unavailable when taskExecutor is nil
// (gateway degraded), AND leaves the task in its prior status.
//
// BDD:
//
//	Given a task with an agent_id and api.taskExecutor == nil,
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"},
//	Then 503 Service Unavailable, and the task status is NOT in_progress.
func TestHandleTaskPatch_RunTask_NilExecutorReturns503(t *testing.T) {
	// Build a restAPI with taskExecutor explicitly nil, but with a real agent in
	// the registry so the agent-FK check does not reject the PATCH before we reach
	// the executor branch.
	api := newTestRestAPIWithHome(t) // taskExecutor is nil by construction
	wsID := ensureTestWorkspace(t, api)

	tsk := createTaskViaAPI(t, api, "NilExecTask503", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	// Do NOT assign agent_id — the executor-nil branch is only reached when
	// agent_id is set.  We use the internal store.Update to set agent_id so we
	// bypass the registry FK guard that rejects unknown agents.
	agentID := "synthetic-agent"
	_, err := api.taskStore.Update(tsk.Id, task.Patch{AgentID: &agentID})
	require.NoError(t, err, "direct agent_id set must succeed")

	// PATCH status=in_progress with nil executor — should be 503.
	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"nil taskExecutor must return 503; body=%s", w.Body.String())

	// Task must be reverted to 'next', not left as in_progress.
	statusAfter := getTaskStatus(t, api, tsk.Id)
	assert.Equal(t, gen.TaskStatusNext, statusAfter,
		"task must revert to 'next' after 503 (not left as in_progress)")
}

// TestHandleTaskPatch_NilStatus_AlreadyInProgress_NoLaunch verifies that a PATCH
// carrying NO status field (req.Status == nil) on a task that is ALREADY
// in_progress never enters the StartTaskNow launch path, even when the task has
// an agent_id and taskExecutor is nil. The patch must succeed with 200 and the
// task must remain in_progress (no silent revert, no 503).
//
// This covers the revert-silent-fail bug: when req.Status is nil, preUpdateStatus
// is "" because the pre-read is skipped. If the launch guard fired in this case
// and StartTaskNow failed, the revert would call Update with status="" which the
// store rejects (IsValidStatus("")==false), causing a silent no-op revert and a
// misleading error response. The fix is to gate the entire launch block on
// req.Status != nil.
//
// BDD:
//
//	Given a task already in_progress with an agent_id, and taskExecutor == nil,
//	When PATCH /api/v1/tasks/{id} with a body that contains NO "status" field
//	     (e.g. {"priority":1}),
//	Then 200 OK, the task remains in_progress, and no revert is attempted.
func TestHandleTaskPatch_NilStatus_AlreadyInProgress_NoLaunch(t *testing.T) {
	api := newTestRestAPIWithHome(t) // taskExecutor is nil by construction
	wsID := ensureTestWorkspace(t, api)

	tsk := createTaskViaAPI(t, api, "NilStatusAlreadyInProgress", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	// Force the task to in_progress via the store directly (bypassing the REST
	// path that would require a real executor). Also inject an agent_id so that,
	// if the launch guard were to fire incorrectly, it would reach the nil-executor
	// branch and produce a 503.
	inProgress := task.StatusInProgress
	agentID := "synthetic-agent"
	_, err := api.taskStore.Update(tsk.Id, task.Patch{Status: &inProgress, AgentID: &agentID})
	require.NoError(t, err, "direct status+agent_id set must succeed")

	// Confirm the task is in_progress before the PATCH.
	require.Equal(t, gen.TaskStatusInProgress, getTaskStatus(t, api, tsk.Id))

	// PATCH with NO "status" field — only a metadata field to prove the path works.
	// Must NOT enter the launch guard (req.Status == nil).
	w := patchTask(t, api, tsk.Id, `{"priority":1}`)
	assert.Equal(t, http.StatusOK, w.Code,
		"PATCH with no status field on already-in_progress task must return 200; body=%s",
		w.Body.String())

	// Task must still be in_progress — no silent revert.
	statusAfter := getTaskStatus(t, api, tsk.Id)
	assert.Equal(t, gen.TaskStatusInProgress, statusAfter,
		"task must remain in_progress after a nil-status PATCH (no revert attempted)")
}
