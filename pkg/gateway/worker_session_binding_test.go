//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the worker-is-not-a-chat-target / not-a-direct-runner guards at the
// gateway session-binding and execution-binding chokepoints:
//   - createSessionHTTP rejects a worker agent_id (RESIDUAL PATH 5)
//   - POST /api/v1/tasks rejects a direct worker agent_id assignment (RESIDUAL PATH 7 - Sprint 2)
//   - firstChatTargetAgentID skips a worker (RESIDUAL PATH 6)
//   - delegation-created worker task still succeeds (control: Jim→worker works)
//
// Sprint 2 changes: validateBoardTaskAgentID and handleBoardTaskPost are DELETED
// (part of the removed /board/tasks surface). The worker-guard for direct task
// assignment is now exercised via POST /api/v1/tasks with agent_id=worker.
// TestDelegationWorkerTaskStillSucceeds is rewritten to use task.Task directly.

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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/task"
)

// newWorkerTestRestAPI builds a restAPI whose config holds a base default agent
// ("mia") and a worker ("hans"). Both are registered in the loop's registry.
func newWorkerTestRestAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true},
				{ID: "hans", Name: "Hans", Type: config.AgentTypeWorker},
			},
		},
	}
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), cfgJSON, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop: al,
		homePath:  tmpDir,
		taskStore: task.New(filepath.Join(tmpDir, "tasks")),
		taskLock:  task.TaskFileLock,
	}
	t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })
	return api, tmpDir
}

// TestCreateSessionHTTP_RejectsWorker verifies RESIDUAL PATH 5: an explicit worker
// agent_id on POST /api/v1/sessions returns 400 — a worker cannot back a chat
// session. A base agent (control) returns 201.
func TestCreateSessionHTTP_RejectsWorker(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)

	// Worker agent_id → 400.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"agent_id":"hans"}`))
	api.createSessionHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"a worker agent_id must be rejected with 400 when creating a session")
	assert.Contains(t, strings.ToLower(w.Body.String()), "worker",
		"the error must explain a worker cannot back a session")

	// Control: base agent → 201.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"agent_id":"mia"}`))
	api.createSessionHTTP(w2, r2)
	require.Equal(t, http.StatusCreated, w2.Code,
		"a base agent must be able to back a session (control)")
}

// TestTaskPost_RejectsWorkerAssignee verifies RESIDUAL PATH 7 via the unified
// task surface: POST /api/v1/tasks with agent_id="hans" (a worker) returns 400.
// Worker direct assignment is forbidden; workers run only via delegation.
// Sprint 2: replaces the deleted TestBoardTaskPost_RejectsWorkerAssignee
// (which called the removed handleBoardTaskPost/validateBoardTaskAgentID).
//
// Previously BLOCKED (RESIDUAL PATH 7): validateTaskAgentID is now implemented
// in pkg/gateway/rest_tasks.go and wired into handleTaskCreate. When the registry
// is non-empty it rejects any agent_id whose IsWorker flag is true with 400,
// citing "is a worker and cannot be directly assigned a task".
//
// Traces to: RESIDUAL PATH 7 (worker direct-assignment guard)
func TestTaskPost_RejectsWorkerAssignee(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)
	wsID := ensureTestWorkspace(t, api)

	// Attempt to directly assign a task to the worker agent "hans" → 400.
	workerBody := fmt.Sprintf(
		`{"title":"WorkerAssignTest","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":"hans"}`,
		wsID,
	)
	wWorker := httptest.NewRecorder()
	rWorker := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(workerBody))
	rWorker.Header.Set("Content-Type", "application/json")
	rWorker.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wWorker, rWorker)

	require.Equal(t, http.StatusBadRequest, wWorker.Code,
		"POST with a worker agent_id must return 400; body=%s", wWorker.Body.String())
	assert.Contains(t, strings.ToLower(wWorker.Body.String()), "worker",
		"400 body must explain that a worker cannot be directly assigned a task")

	// Differentiation: a base agent (mia) must still be accepted → 201.
	// Two different agent types → two different response codes proves the guard is real.
	baseBody := fmt.Sprintf(
		`{"title":"BaseAgentAssignTest","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":"mia"}`,
		wsID,
	)
	wBase := httptest.NewRecorder()
	rBase := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(baseBody))
	rBase.Header.Set("Content-Type", "application/json")
	rBase.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wBase, rBase)

	require.Equal(t, http.StatusCreated, wBase.Code,
		"POST with a non-worker agent_id must return 201 (differentiation); body=%s", wBase.Body.String())
	require.NotEqual(t, wWorker.Code, wBase.Code,
		"worker POST and base-agent POST must return different status codes (guard is not hardcoded)")
}

// TestDelegationWorkerTaskStillSucceeds is the CONTROL that proves the worker
// guard on the REST API does NOT block Jim→worker delegation. Delegation
// creates worker-targeted tasks through task_create → taskStore.Create
// directly — bypassing the REST validation layer. Creating a worker-assigned
// task this way must succeed.
// Sprint 2: rewritten from taskstore.TaskEntity to task.Task.
func TestDelegationWorkerTaskStillSucceeds(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)

	// Use a synthetic workspace ID: the store.Create validates workspace_id is
	// non-empty (via normalize) but does NOT FK-check it (that's REST-layer only).
	const delegationWorkspaceID = "01JXDELEGATIONWS000000001"

	// Simulate the delegation path: Jim's task_create tool writes a worker-targeted
	// task straight to the task store. This must NOT be blocked.
	entity := &task.Task{
		ID:          "test-delegated-worker-task-001",
		Title:       "delegated work",
		Prompt:      "do the analysis",
		AgentID:     "hans", // worker assignee — legitimate for delegation
		CreatedBy:   "jim",
		Status:      task.StatusInbox, // unified status: inbox on creation
		Action:      task.ActionLLM,
		WorkspaceID: delegationWorkspaceID,
	}
	require.NoError(t, api.taskStore.Create(entity),
		"delegation (task_create → taskStore.Create) must still create a worker-targeted task")

	got, err := api.taskStore.Get(entity.ID)
	require.NoError(t, err, "taskStore.Get must find the delegated task")
	assert.Equal(t, "hans", got.AgentID,
		"the delegated worker task must persist with the worker as assignee")
	assert.Equal(t, task.StatusInbox, got.Status,
		"the delegated task status must be inbox (unified vocabulary)")
	assert.Equal(t, "delegated work", got.Title,
		"the delegated task title must match the original")
}

// TestFirstChatTargetAgentID_SkipsWorker verifies RESIDUAL PATH 6: the last-resort
// fallback never lands on a worker even when the worker appears first in the list.
// It returns the first chat-target agent instead.
func TestFirstChatTargetAgentID_SkipsWorker(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				// Worker appears first but must be skipped — workers are never chat targets.
				{ID: "hans", Type: config.AgentTypeWorker},
				{ID: "mia"},
			},
		},
	}
	got := firstChatTargetAgentID(cfg)
	assert.NotEqual(t, "hans", got, "firstChatTargetAgentID must never return a worker")
	assert.Equal(t, "mia", got, "firstChatTargetAgentID must return the first chat-target agent")
}

// TestHandleChatMessage_RejectsWorkerAgentID verifies RESIDUAL PATH 4: a chat
// frame that explicitly addresses a worker agentID must be rejected with an error
// frame and must NOT mint a live session for the worker. A worker is not a chat
// target.
func TestHandleChatMessage_RejectsWorkerAgentID(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)
	handler := newWSHandler(bus.NewMessageBus(), api.agentLoop, "")

	wc := makeTestConn()
	handler.handleChatMessage(
		context.Background(),
		"chat-worker-1", // chatID
		"",              // frameSessionID (empty → would mint a new session)
		"do the work",   // content
		"hans",          // agentID = worker
		nil,             // mediaRefs
		"",              // modelName (no per-turn override)
		"",              // workspaceID (no active workspace)
		wc,
	)

	// Drain frames: expect exactly one error frame, and no session_started frame.
	var sawError, sawSessionStarted bool
	for {
		select {
		case raw := <-wc.sendCh:
			var f replayFrameDecoder
			require.NoError(t, json.Unmarshal(raw, &f))
			switch f.Type {
			case string(gen.WsFrameTypeError):
				sawError = true
				assert.Contains(t, strings.ToLower(f.Message), "worker",
					"the error frame must explain a worker cannot be a chat target")
			case string(gen.WsFrameTypeSessionStarted):
				sawSessionStarted = true
			}
		default:
			require.True(t, sawError, "a worker chat frame must produce an error frame")
			require.False(t, sawSessionStarted, "a worker chat frame must NOT mint a session")
			return
		}
	}
}

// TestFirstChatTargetAgentID_AllWorkersReturnsEmpty verifies the degenerate case:
// when every agent is a worker, the fallback returns "" rather than a
// worker (the caller then surfaces a "no agent configured" error).
func TestFirstChatTargetAgentID_AllWorkersReturnsEmpty(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "w1", Type: config.AgentTypeWorker},
				{ID: "w2", Type: config.AgentTypeWorker},
			},
		},
	}
	assert.Equal(t, "", firstChatTargetAgentID(cfg),
		"when all agents are workers, the fallback must return empty, never a worker")
}
