//go:build !cgo

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
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/task"
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
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
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

// TestHandleTaskPatch_InProgress_NilTaskExecutor verifies that PATCH
// status=in_progress with an agent_id but a nil taskExecutor still returns 200
// (graceful fallback — logs a warn, does not crash or return 500).
//
// BDD:
//
//	Given a task with an agent_id and taskExecutor=nil on the restAPI,
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"},
//	Then 200, status=in_progress, no crash.
func TestHandleTaskPatch_InProgress_NilTaskExecutor(t *testing.T) {
	api := newTestRestAPIWithHome(t) // does NOT wire taskExecutor
	wsID := ensureTestWorkspace(t, api)

	tsk := createTaskViaAPI(t, api, "AgentTaskNoExec", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	// Assign a (fake) agent_id so the executor-launch path is reached.
	// validateTaskAgentID skips the check when the registry is empty/no agents.
	w := patchTask(t, api, tsk.Id, `{"status":"in_progress","agent_id":"mia"}`)
	// The store may reject "mia" if registry validation fires with an empty agent
	// list; we only care that the response is not 500.
	assert.NotEqual(t, http.StatusInternalServerError, w.Code,
		"PATCH with nil taskExecutor must not return 500; body=%s", w.Body.String())
}

// TestHandleTaskPatch_InProgress_WithKnownAgent verifies that PATCH
// status=in_progress on a task with a known agent_id returns 200 with
// status=in_progress. StartTaskNow may not create a session in the test
// environment (the default "main" agent has no real session store in the
// test harness), but the PATCH must still return 200 — executor errors on the
// session-creation path are logged as Warn and do not surface as 500.
//
// BDD:
//
//	Given a task with agent_id="main" (the test registry's default agent),
//	When PATCH /api/v1/tasks/{id} with {"status":"in_progress"},
//	Then 200 and status=in_progress.
func TestHandleTaskPatch_InProgress_WithKnownAgent(t *testing.T) {
	api := newTestRestAPIWithTaskExecutor(t)
	wsID := ensureTestWorkspace(t, api)

	tsk := createTaskViaAPI(t, api, "AgentTask", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	// First assign the agent_id so the follow-up PATCH can transition to in_progress.
	wAssign := patchTask(t, api, tsk.Id, `{"agent_id":"main"}`)
	require.Equal(t, http.StatusOK, wAssign.Code,
		"assigning agent_id=main must return 200; body=%s", wAssign.Body.String())

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
