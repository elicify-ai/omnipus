//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the worker-is-not-a-chat-target / not-a-direct-runner guards at the
// gateway session-binding and execution-binding chokepoints:
//   - createSessionHTTP rejects a worker agent_id (RESIDUAL PATH 5)
//   - validateBoardTaskAgentID rejects a worker direct assignee (RESIDUAL PATH 7)
//   - firstEnabledAgentID skips a worker (RESIDUAL PATH 6)
//   - delegation-created worker task still succeeds (control proving Jim→worker works)

package gateway

import (
	"context"
	"encoding/json"
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
	"github.com/dapicom-ai/omnipus/pkg/taskstore"
)

// newWorkerTestRestAPI builds a restAPI whose config holds a base default agent
// ("mia") and a worker ("hans"). Both are registered in the loop's registry.
func newWorkerTestRestAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	enabledTrue := true
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
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true, Enabled: &enabledTrue},
				{ID: "hans", Name: "Hans", Type: config.AgentTypeWorker, Enabled: &enabledTrue},
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
		taskStore: taskstore.New(filepath.Join(tmpDir, "workflow-tasks")),
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

// TestValidateBoardTaskAgentID_RejectsWorker verifies RESIDUAL PATH 7: a board task
// directly assigned to a worker is rejected — direct (human/REST-initiated)
// assignment of a worker is forbidden; workers run only via delegation. A base
// agent and an empty agent_id are accepted.
func TestValidateBoardTaskAgentID_RejectsWorker(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)

	// Worker → error.
	err := api.validateBoardTaskAgentID("hans")
	require.Error(t, err, "a worker direct assignee must be rejected")
	assert.Contains(t, strings.ToLower(err.Error()), "worker",
		"the error must explain a worker cannot be directly assigned")

	// Base agent → nil (control).
	require.NoError(t, api.validateBoardTaskAgentID("mia"),
		"a base agent must be a valid direct assignee")

	// Empty agent_id → nil (start resolves default).
	require.NoError(t, api.validateBoardTaskAgentID(""),
		"empty agent_id must remain valid")
}

// TestBoardTaskPost_RejectsWorkerAssignee verifies the end-to-end REST path:
// POST /api/v1/board/tasks with a worker agent_id returns 400.
func TestBoardTaskPost_RejectsWorkerAssignee(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks",
		strings.NewReader(`{"name":"do a thing","agent_id":"hans"}`))
	api.handleBoardTaskPost(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"a board task directly assigned to a worker must be rejected with 400")
	assert.Contains(t, strings.ToLower(w.Body.String()), "worker")
}

// TestDelegationWorkerTaskStillSucceeds is the CONTROL that proves the
// validateBoardTaskAgentID worker guard does NOT break Jim→worker delegation.
// Delegation creates worker-targeted tasks through the task_create tool, which
// writes directly via taskstore.TaskStore.Create — a SEPARATE path that never
// calls the REST validator. Creating a worker-assigned task this way must succeed.
func TestDelegationWorkerTaskStillSucceeds(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)

	// Simulate the delegation path: Jim's task_create tool writes a worker-targeted
	// task straight to the task store. This must NOT be blocked.
	entity := &taskstore.TaskEntity{
		ID:          "01JXDELEGATEDWORKERTASK0001",
		Title:       "delegated work",
		Prompt:      "do the analysis",
		AgentID:     "hans", // worker assignee — legitimate for delegation
		CreatedBy:   "jim",
		Status:      "queued", // taskstore execution status (mirrors task_create tool)
		TriggerType: "delegation",
	}
	require.NoError(t, api.taskStore.Create(entity),
		"delegation (task_create → taskstore.Create) must still create a worker-targeted task")

	got, err := api.taskStore.Get(entity.ID)
	require.NoError(t, err)
	assert.Equal(t, "hans", got.AgentID,
		"the delegated worker task must persist with the worker as assignee")
}

// TestFirstEnabledAgentID_SkipsWorker verifies RESIDUAL PATH 6: the last-resort
// fallback never lands on a worker even when the worker is the first ACTIVE agent
// in the list. It returns the first enabled chat-target agent instead.
func TestFirstEnabledAgentID_SkipsWorker(t *testing.T) {
	enabled := true
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				// Worker appears first and is active, but must be skipped.
				{ID: "hans", Type: config.AgentTypeWorker, Enabled: &enabled},
				{ID: "mia", Enabled: &enabled},
			},
		},
	}
	got := firstEnabledAgentID(cfg)
	assert.NotEqual(t, "hans", got, "firstEnabledAgentID must never return a worker")
	assert.Equal(t, "mia", got, "firstEnabledAgentID must return the first enabled chat-target agent")
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

// TestFirstEnabledAgentID_AllWorkersReturnsEmpty verifies the degenerate case:
// when every enabled agent is a worker, the fallback returns "" rather than a
// worker (the caller then surfaces a "no agent configured" error).
func TestFirstEnabledAgentID_AllWorkersReturnsEmpty(t *testing.T) {
	enabled := true
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "w1", Type: config.AgentTypeWorker, Enabled: &enabled},
				{ID: "w2", Type: config.AgentTypeWorker, Enabled: &enabled},
			},
		},
	}
	assert.Equal(t, "", firstEnabledAgentID(cfg),
		"when all agents are workers, the fallback must return empty, never a worker")
}
