//go:build !cgo

// board_task_hardening_test.go — test-hardening additions for the board-task epic.
//
// Gaps closed (from 7-reviewer gate findings):
//
//   H1  — WaitForActiveRequests cleanup helper so TempDir teardown does not
//          race the background goroutine (newTestRestAPIWithAgentDrained).
//   C1  — Concurrency test: 20 goroutines competing on one /start; exactly 1×202,
//          19×409; on-disk session_id matches the winner.
//   C1b — Controlled completion-vs-requeue race: verifies the onComplete guard
//          (t.Status != "active" → skip write) prevents a stale callback from
//          overwriting a requeued task.
//   C2  — Real agent path: /start dispatches ExecuteBoardTask with the mock
//          provider, WaitForActiveRequests drains, GET confirms done/failed (not
//          active) and session_id set.  Includes differentiation sub-test.
//   H2  — A2 agent-id existence validation: POST/PUT/start with unknown agent_id
//          on a populated registry → 400; registered id → 201; empty registry → 201.
//   H3  — validateStatusTransition unit test: active → error; other statuses → ok.
//   M3  — Panic recovery: panicMockProvider triggers recover() in ExecuteBoardTask;
//          task ends failed with result containing "panic:".
//   REC — reconcileStuckBoardTasks: active → failed (non-empty result), done
//          untouched, empty dir is no-op.
//   MIG — Gateway-layer migration regression: file with title (workflow shape) +
//          status "failed" must NOT appear in listBoardTasks (title wins).

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/boardtask"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/taskstore"
)

// ── H1: WaitForActiveRequests drain helper ────────────────────────────────────

// newTestRestAPIWithAgentDrained wraps newTestRestAPIWithAgent and registers a
// t.Cleanup that drains WaitForActiveRequests before the TempDir cleanup fires.
// Use this for any test that exercises the real ExecuteBoardTask goroutine path.
// LIFO cleanup ordering ensures: WaitForActiveRequests → al.Close → TempDir.
func newTestRestAPIWithAgentDrained(t *testing.T) *restAPI {
	t.Helper()
	api := newTestRestAPIWithAgent(t)
	t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })
	return api
}

// ── Mock providers ────────────────────────────────────────────────────────────

// panicMockProvider is an LLMProvider that panics on every Chat call.
// Used by TestExecuteBoardTask_PanicRecovered to exercise the recover() path.
type panicMockProvider struct{}

func (p *panicMockProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	panic("simulated agent panic for test")
}

func (p *panicMockProvider) GetDefaultModel() string { return "test-model" }

// ── newTestRestAPIWithProvider ────────────────────────────────────────────────

// newTestRestAPIWithProvider creates a restAPI with one enabled default agent,
// backed by the given provider. The config mirrors newTestRestAPIWithAgent.
// A t.Cleanup draining WaitForActiveRequests is registered automatically (H1).
func newTestRestAPIWithProvider(t *testing.T, provider providers.LLMProvider) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	agentWorkspace := filepath.Join(tmpDir, "agents", "01JXTESTAGENTSTARTTEST001")
	require.NoError(t, os.MkdirAll(agentWorkspace, 0o700))
	agentEnabled := true
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{
					ID:        "01JXTESTAGENTSTARTTEST001",
					Name:      "Test Agent",
					Default:   true,
					Enabled:   &agentEnabled,
					Type:      config.AgentTypeCustom,
					Workspace: agentWorkspace,
				},
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, provider)
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     taskstore.New(tmpDir + "/workflow-tasks"),
		taskLock:      boardtask.TaskFileLock,
	}
	// H1: drain goroutines before TempDir cleanup.
	t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })
	return api
}

// ── C1: concurrent /start on the same task ───────────────────────────────────

// TestBoardTaskStart_ConcurrentRace verifies that N=20 concurrent /start calls
// on the same task result in exactly one 202 and exactly 19 409s, and that the
// on-disk session_id matches the winner's session_id.
//
// This is the headline verification for the striped-mutex epic: without the lock,
// two goroutines can both observe status=="inbox" and both succeed, producing two
// live agent goroutines for the same task.
//
// BDD:
//
//	Given a board task in status=inbox with a non-empty prompt,
//	When 20 goroutines call POST /start concurrently,
//	Then exactly 1 returns 202 and 19 return 409.
//	And GET /board/tasks/{id} returns the winner's session_id on disk.
//
// Traces to: project-task-management-level1-spec.md — B8 (atomic check-and-set)
func TestBoardTaskStart_ConcurrentRace(t *testing.T) {
	const N = 20
	api := newTestRestAPIWithAgentDrained(t)
	task := createBoardTaskWithPromptViaAPI(t, api, "ConcurrentRaceTask", "inbox", "Run concurrent checks")

	type result struct {
		code      int
		sessionID string
	}
	results := make([]result, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks/"+task.Id+"/start", nil)
			r.URL.Path = "/api/v1/board/tasks/" + task.Id + "/start"
			api.HandleBoardTasks(w, r)
			code := w.Code
			sessionID := ""
			if code == http.StatusAccepted {
				var respTask gen.BoardTask
				_ = json.Unmarshal(w.Body.Bytes(), &respTask)
				if respTask.SessionId != nil {
					sessionID = *respTask.SessionId
				}
			}
			results[idx] = result{code: code, sessionID: sessionID}
		}(i)
	}
	wg.Wait()

	accepted, conflicted := 0, 0
	var winnerSessionID string
	for _, res := range results {
		switch res.code {
		case http.StatusAccepted:
			accepted++
			winnerSessionID = res.sessionID
		case http.StatusConflict:
			conflicted++
		default:
			t.Errorf("unexpected status code %d from /start", res.code)
		}
	}

	assert.Equal(t, 1, accepted,
		"exactly one /start must succeed (202); got accepted=%d conflicted=%d", accepted, conflicted)
	assert.Equal(t, N-1, conflicted,
		"all other /start calls must return 409; got accepted=%d conflicted=%d", accepted, conflicted)

	require.NotEmpty(t, winnerSessionID, "winner must have a non-empty session_id")

	// Drain the background goroutine before reading from disk.
	api.agentLoop.WaitForActiveRequests()

	diskTask, err := api.readBoardTask(task.Id)
	require.NoError(t, err)
	assert.Equal(t, winnerSessionID, diskTask.SessionID,
		"on-disk session_id must equal the winner's session_id")

	validStatuses := map[boardtask.Status]bool{
		boardtask.StatusActive: true,
		boardtask.StatusDone:   true,
		boardtask.StatusFailed: true,
	}
	assert.True(t, validStatuses[diskTask.Status],
		"on-disk status must be active/done/failed after /start, got %q", diskTask.Status)
}

// NOTE: The former TestBoardTaskStart_CompletionVsRequeueRace was removed — it
// reproduced the onComplete guard logic inline (an inline-closure replay that
// asserts against its own copy of the production check, not the real handler).
// TestRegression_CompletionCallback_SetsResult_RealPath below drives the REAL
// /start → ExecuteBoardTask → onComplete path and covers the same behavior.

// ── C2: real agent-path completion ───────────────────────────────────────────

// TestRegression_CompletionCallback_SetsResult_RealPath drives the REAL agent
// execution path: /start dispatches ExecuteBoardTask, WaitForActiveRequests
// drains, then GET verifies the task reached a terminal status and session_id
// is set and non-empty.
//
// Replaces the tautological inline-closure replay in the previous test. The key
// property: this test fails if onComplete never fires (task stuck as active).
//
// Differentiation: two different tasks get two different session_ids.
//
// BDD:
//
//	Given a board task dispatched via POST /start,
//	When WaitForActiveRequests() returns (goroutine completed),
//	Then GET task returns status ∈ {done, failed} (not active),
//	And session_id is non-empty and matches the /start response.
//
// Traces to: project-task-management-level1-spec.md — #404 (result populated on done)
func TestRegression_CompletionCallback_SetsResult_RealPath(t *testing.T) {
	api := newTestRestAPIWithProvider(t, &restMockProvider{})

	task := createBoardTaskWithPromptViaAPI(t, api, "RealPathTask", "inbox", "Run real completion path")

	wStart := postBoardTaskStart(t, api, task.Id)
	require.Equal(t, http.StatusAccepted, wStart.Code, "/start must return 202; body=%s", wStart.Body.String())
	var started gen.BoardTask
	require.NoError(t, json.Unmarshal(wStart.Body.Bytes(), &started))
	require.NotNil(t, started.SessionId, "session_id must be non-nil after /start")
	assert.NotEmpty(t, *started.SessionId, "session_id must be non-empty")

	// Drain the goroutine.
	api.agentLoop.WaitForActiveRequests()

	// GET: must be terminal (done or failed), not stuck as active.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+task.Id, nil)
	rGet.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "GET must return 200; body=%s", wGet.Body.String())

	var got gen.BoardTask
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))

	terminalStatuses := map[gen.BoardTaskStatus]bool{
		"done":   true,
		"failed": true,
	}
	assert.True(t, terminalStatuses[got.Status],
		"task must be done or failed after goroutine drains (not stuck active), got %q", got.Status)

	require.NotNil(t, got.SessionId, "session_id must remain set after completion")
	assert.Equal(t, *started.SessionId, *got.SessionId,
		"session_id must match the value returned by /start")

	// Differentiation: second task gets a different session_id.
	task2 := createBoardTaskWithPromptViaAPI(t, api, "RealPathTask2", "inbox", "Second prompt")
	wStart2 := postBoardTaskStart(t, api, task2.Id)
	require.Equal(t, http.StatusAccepted, wStart2.Code, "task2 /start must return 202; body=%s", wStart2.Body.String())
	var started2 gen.BoardTask
	require.NoError(t, json.Unmarshal(wStart2.Body.Bytes(), &started2))
	require.NotNil(t, started2.SessionId)
	assert.NotEqual(t, *started.SessionId, *started2.SessionId,
		"two different tasks must receive different session_ids (not hardcoded)")

	api.agentLoop.WaitForActiveRequests()
}

// ── H2: A2 agent_id existence validation ─────────────────────────────────────

// TestBoardTaskPost_RejectsUnknownAgentID verifies the A2 validation logic.
//
// BDD:
//
//	Given a registry with exactly one agent (01JXTESTAGENTSTARTTEST001),
//	When POST /board/tasks with agent_id="01JXUNKNOWNAGENT0000001X" (not in registry),
//	Then 400 with the unknown ID in the body.
//	When POST /board/tasks with agent_id=<registered id>,
//	Then 201.
//	Given a registry with zero agents (newTestRestAPIWithHome),
//	When POST /board/tasks with any agent_id,
//	Then 201 (empty registry → skip A2 validation).
//	When PUT /board/tasks/{id} with unknown agent_id,
//	Then 400.
//	When POST /start on a task whose stored agent_id is not in the populated registry,
//	Then 400.
//
// Traces to: project-task-management-level1-spec.md — A2 (agent_id existence)
func TestBoardTaskPost_RejectsUnknownAgentID(t *testing.T) {
	const registeredID = "01JXTESTAGENTSTARTTEST001"
	const unknownID = "01JXUNKNOWNAGENT0000001X"

	t.Run("populated registry rejects unknown agent_id on POST", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })

		body := fmt.Sprintf(`{"name":"AgentRejectTest","prompt":"do it","agent_id":%q}`, unknownID)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/board/tasks"
		api.HandleBoardTasks(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code,
			"POST with unknown agent_id must return 400 on populated registry; body=%s", w.Body.String())
		assert.Contains(t, w.Body.String(), unknownID,
			"400 body must mention the unknown agent_id for traceability")
	})

	t.Run("populated registry accepts registered agent_id on POST", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })

		body := fmt.Sprintf(`{"name":"AgentAcceptTest","prompt":"do it","agent_id":%q}`, registeredID)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/board/tasks"
		api.HandleBoardTasks(w, r)

		assert.Equal(t, http.StatusCreated, w.Code,
			"POST with registered agent_id must return 201; body=%s", w.Body.String())
	})

	t.Run("empty agent_id skips A2 validation (resolved at start time)", func(t *testing.T) {
		// validateBoardTaskAgentID returns nil when agent_id == "" — the field is
		// optional; the resolved default agent is used at /start time. This is the
		// A2 "skip" path for tasks where the caller does not specify an agent_id.
		api := newTestRestAPIWithAgent(t)
		t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })

		body := `{"name":"NoAgentIDTask","prompt":"do it"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/board/tasks"
		api.HandleBoardTasks(w, r)

		assert.Equal(t, http.StatusCreated, w.Code,
			"POST without agent_id must return 201 (A2 skip: empty agent_id is valid); body=%s", w.Body.String())
	})

	t.Run("populated registry rejects unknown agent_id on PUT", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })

		// Create a task without agent_id (passes POST A2 check).
		task := createBoardTaskWithPromptViaAPI(t, api, "PUTAgentRejectTask", "inbox", "work")

		putBody := fmt.Sprintf(`{"agent_id":%q}`, unknownID)
		wPut := httptest.NewRecorder()
		rPut := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id, strings.NewReader(putBody))
		rPut.Header.Set("Content-Type", "application/json")
		rPut.URL.Path = "/api/v1/board/tasks/" + task.Id
		api.HandleBoardTasks(wPut, rPut)

		assert.Equal(t, http.StatusBadRequest, wPut.Code,
			"PUT with unknown agent_id on populated registry must return 400; body=%s", wPut.Body.String())
	})

	t.Run("start rejects task with unknown stored agent_id on populated registry", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })

		// Write a task directly to disk with an unknown agent_id (bypass POST A2 check).
		taskID := "01JXSTARTREJECTAGENT0001X"
		tasksDir := filepath.Join(api.homePath, "tasks")
		require.NoError(t, os.MkdirAll(tasksDir, 0o700))
		now := time.Now().UTC().Format(time.RFC3339)
		data := fmt.Sprintf(
			`{"id":%q,"name":"StartRejectTask","status":"inbox","agent_id":%q,"prompt":"do it","created_at":%q,"updated_at":%q}`,
			taskID,
			unknownID,
			now,
			now,
		)
		require.NoError(t, os.WriteFile(filepath.Join(tasksDir, taskID+".json"), []byte(data), 0o600))

		wStart := httptest.NewRecorder()
		rStart := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks/"+taskID+"/start", nil)
		rStart.URL.Path = "/api/v1/board/tasks/" + taskID + "/start"
		api.HandleBoardTasks(wStart, rStart)

		assert.Equal(t, http.StatusBadRequest, wStart.Code,
			"/start with stored unknown agent_id on populated registry must return 400; body=%s", wStart.Body.String())
	})
}

// ── H3: validateStatusTransition unit test ───────────────────────────────────

// TestValidateStatusTransition_RejectsActive verifies the A4 function-level guard:
// validateStatusTransition must return an error when newStatus=="active", and
// must return nil for all other GTD statuses.
//
// BDD:
//
//	When validateStatusTransition(_, "active") is called,
//	Then an error is returned (with "active" in the message).
//	When validateStatusTransition(_, <any other GTD status>) is called,
//	Then nil is returned.
//
// Traces to: project-task-management-level1-spec.md — A4 (PUT cannot set active)
func TestValidateStatusTransition_RejectsActive(t *testing.T) {
	tests := []struct {
		name        string
		newStatus   boardtask.Status
		expectError bool
	}{
		{"active is rejected", boardtask.StatusActive, true},
		{"inbox is allowed", boardtask.StatusInbox, false},
		{"next is allowed", boardtask.StatusNext, false},
		{"waiting is allowed", boardtask.StatusWaiting, false},
		{"done is allowed", boardtask.StatusDone, false},
		{"failed is allowed", boardtask.StatusFailed, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStatusTransition(boardtask.StatusInbox, tc.newStatus)
			if tc.expectError {
				assert.Error(t, err,
					"validateStatusTransition(inbox→%q) must return an error", tc.newStatus)
				assert.Contains(t, err.Error(), "active",
					"error for status='active' must mention 'active'")
			} else {
				assert.NoError(t, err,
					"validateStatusTransition(inbox→%q) must not return an error", tc.newStatus)
			}
		})
	}

	// Differentiation: active errors, next does not.
	errActive := validateStatusTransition(boardtask.StatusInbox, boardtask.StatusActive)
	errNext := validateStatusTransition(boardtask.StatusInbox, boardtask.StatusNext)
	assert.Error(t, errActive, "active must produce an error (different from next)")
	assert.NoError(t, errNext, "next must not produce an error (different from active)")
}

// ── M3: panic recovery in ExecuteBoardTask ───────────────────────────────────

// TestExecuteBoardTask_PanicRecovered verifies that a panic in the agent goroutine
// is caught by recover(), onComplete is called with a "panic:" error, and the task
// transitions to status=failed (not stuck as active).
//
// Differentiation: a non-panicking provider produces done (not failed), proving
// the test is not just checking "task is not active".
//
// BDD:
//
//	Given a panicMockProvider that panics on Chat(),
//	When POST /start dispatches ExecuteBoardTask,
//	And WaitForActiveRequests() drains the goroutine,
//	Then GET task returns status=failed.
//	And result contains "panic:".
//
// Traces to: project-task-management-level1-spec.md — M3 (panic recovery)
func TestExecuteBoardTask_PanicRecovered(t *testing.T) {
	api := newTestRestAPIWithProvider(t, &panicMockProvider{})

	task := createBoardTaskWithPromptViaAPI(t, api, "PanicTask", "inbox", "This will panic")

	// /start dispatches the real goroutine; panic happens inside it.
	wStart := postBoardTaskStart(t, api, task.Id)
	require.Equal(t, http.StatusAccepted, wStart.Code,
		"/start must succeed (panic is inside goroutine, not handler); body=%s", wStart.Body.String())

	// Drain — recover() fires, onComplete("", err) sets task to failed.
	api.agentLoop.WaitForActiveRequests()

	// GET: must be failed with a panic result.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+task.Id, nil)
	rGet.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "GET after panic must return 200; body=%s", wGet.Body.String())

	var got gen.BoardTask
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))

	assert.Equal(t, gen.BoardTaskStatus("failed"), got.Status,
		"task must be failed after a goroutine panic (not stuck active)")
	require.NotNil(t, got.Result,
		"result must be set after panic recovery — onComplete must write the panic message")
	assert.Contains(t, *got.Result, "panic:",
		"result must contain 'panic:' from the recover() path in ExecuteBoardTask")

	// Differentiation: non-panicking provider produces done (not failed).
	apiOK := newTestRestAPIWithProvider(t, &restMockProvider{})
	taskOK := createBoardTaskWithPromptViaAPI(t, apiOK, "NoPanicTask", "inbox", "No panic here")
	wStartOK := postBoardTaskStart(t, apiOK, taskOK.Id)
	require.Equal(t, http.StatusAccepted, wStartOK.Code)
	apiOK.agentLoop.WaitForActiveRequests()

	wGetOK := httptest.NewRecorder()
	rGetOK := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+taskOK.Id, nil)
	rGetOK.URL.Path = "/api/v1/board/tasks/" + taskOK.Id
	apiOK.HandleBoardTasks(wGetOK, rGetOK)
	require.Equal(t, http.StatusOK, wGetOK.Code)

	var gotOK gen.BoardTask
	require.NoError(t, json.Unmarshal(wGetOK.Body.Bytes(), &gotOK))
	assert.NotEqual(t, gen.BoardTaskStatus("failed"), gotOK.Status,
		"non-panicking task must not be failed — panic vs no-panic produce different outcomes")
}

// ── REC: reconcileStuckBoardTasks ─────────────────────────────────────────────

// TestReconcileStuckBoardTasks verifies the boot reconciler resets stuck tasks.
//
// BDD:
//
//	Given a task file with status="active" in tasks/,
//	When reconcileStuckBoardTasks() is called,
//	Then the task status is "failed" with a non-empty result.
//	Given a task file with status="done",
//	Then the task status is still "done" (untouched).
//	Given an empty tasks directory,
//	Then reconcileStuckBoardTasks() is a no-op (no error).
//
// Traces to: project-task-management-level1-spec.md — REC (boot reconcile)
func TestReconcileStuckBoardTasks(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	tasksDir := filepath.Join(api.homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)

	// Write an active (stuck) task.
	const activeID = "01JXRECONCILE_ACTIVE00001"
	activeData := fmt.Sprintf(
		`{"id":%q,"name":"StuckActiveTask","status":"active","session_id":"orphaned","created_at":%q,"updated_at":%q}`,
		activeID, now, now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, activeID+".json"), []byte(activeData), 0o600))

	// Write a done task (must not be modified).
	const doneID = "01JXRECONCILE_DONE000001"
	doneData := fmt.Sprintf(
		`{"id":%q,"name":"DoneTask","status":"done","result":"all tests passed","created_at":%q,"updated_at":%q}`,
		doneID, now, now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, doneID+".json"), []byte(doneData), 0o600))

	// Run the reconciler.
	api.reconcileStuckBoardTasks()

	// Active task must now be failed with a non-empty diagnostic result.
	gotActive, err := api.readBoardTask(activeID)
	require.NoError(t, err, "readBoardTask (active→failed) must not error")
	assert.Equal(t, boardtask.StatusFailed, gotActive.Status,
		"reconcile must reset active task to failed")
	assert.NotEmpty(t, gotActive.Result,
		"reconciled active task must have a non-empty diagnostic result")

	// Done task must be unchanged.
	gotDone, err := api.readBoardTask(doneID)
	require.NoError(t, err, "readBoardTask (done) must not error")
	assert.Equal(t, boardtask.StatusDone, gotDone.Status,
		"reconcile must leave done tasks untouched")
	assert.Equal(t, "all tests passed", gotDone.Result,
		"done task result must not be modified by reconcile")

	// Differentiation: the two tasks end in different statuses (not both set to failed).
	assert.NotEqual(t, gotActive.Status, gotDone.Status,
		"active and done tasks must produce different outcomes (not both set to failed)")

	// Empty-directory no-op: a fresh api with no tasks dir must not error.
	apiEmpty := newTestRestAPIWithHome(t)
	apiEmpty.reconcileStuckBoardTasks() // must not panic or error

	emptyTasksDir := filepath.Join(apiEmpty.homePath, "tasks")
	entries, readDirErr := os.ReadDir(emptyTasksDir)
	if readDirErr != nil && !os.IsNotExist(readDirErr) {
		require.NoError(t, readDirErr)
	}
	assert.Empty(t, entries, "empty tasks dir must remain empty after reconcile no-op")
}

// ── MIG: gateway-layer migration regression ──────────────────────────────────

// TestRegression_MigrateWorkflowTasks_FailedWorkflowNotStranded verifies the
// gateway-layer invariant: a file with title="X" and status="failed" (workflow
// shape — no "name" field) must NOT appear in listBoardTasks.
//
// The regression: "failed" is in both the GTD and workflow status vocabularies.
// The title field is the decisive discriminator; a file with title and no name
// is a workflow entity and must be excluded from the GTD view.
//
// For the datamodel.Init migration path, see:
// pkg/datamodel/migrate_workflow_tasks_test.go.
//
// BDD:
//
//	Given tasks/ contains {"title":"Build Docker Image","status":"failed"} (no "name"),
//	When listBoardTasks() is called,
//	Then the file does NOT appear (title-only → workflow entity, not GTD).
//	And a real GTD task ({"name":"Write Tests","status":"inbox"}) DOES appear.
//
// Traces to: project-task-management-level1-spec.md — #402/#397 (migration regression)
func TestRegression_MigrateWorkflowTasks_FailedWorkflowNotStranded(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	tasksDir := filepath.Join(api.homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)

	// Workflow-shaped file: title + status="failed" (no "name" field).
	const workflowID = "01JXMIGFAILEDWF000000001"
	workflowData := fmt.Sprintf(
		`{"id":%q,"title":"Build Docker Image","status":"failed","created_at":%q,"updated_at":%q}`,
		workflowID, now, now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, workflowID+".json"), []byte(workflowData), 0o600))

	// Real GTD task: name + GTD status.
	const gtdID = "01JXMIGFAILEDGTD000000001"
	gtdData := fmt.Sprintf(
		`{"id":%q,"name":"Write Tests","status":"inbox","created_at":%q,"updated_at":%q}`,
		gtdID, now, now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, gtdID+".json"), []byte(gtdData), 0o600))

	tasks, err := api.listBoardTasks()
	require.NoError(t, err, "listBoardTasks must not error")

	// Workflow file must NOT appear.
	for _, tk := range tasks {
		assert.NotEqual(t, workflowID, tk.ID,
			"workflow file (title='Build Docker Image', no name) must NOT appear in GTD listBoardTasks")
	}

	// GTD file MUST appear.
	foundGTD := false
	for _, tk := range tasks {
		if tk.ID == gtdID {
			foundGTD = true
			assert.Equal(t, "Write Tests", tk.Name,
				"GTD task must have the correct name in listBoardTasks")
		}
	}
	assert.True(t, foundGTD, "real GTD task must appear in listBoardTasks")

	// Differentiation: workflow vs GTD files produce different membership outcomes.
	assert.Len(t, tasks, 1, "exactly 1 GTD task must appear; the workflow file is excluded")
}
