// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// R1's web half (CR C1 / ARCH A1 / PRT F1 / SFH SF-4): the web Start Task
// route — PATCH /api/v1/tasks/{id} status=in_progress — must admit the
// authorized restart of a Done task WITH a bound conversation and dispatch
// the bound-session continuation (same session, generation+1, worker
// started). OLD: the route required an EMPTY session binding to launch
// (rest_tasks.go::taskPatch.launchIfStarted) and the store froze Done, so a
// bound Done task could never be restarted from the board.

package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/require"
)

// gatewayHoldWorker parks every turn until released, then answers without
// claiming. Process-edge double only; the release keeps the test in control
// of the dispatched continuation's lifetime.
type gatewayHoldWorker struct {
	mu      sync.Mutex
	calls   int
	entered chan int
	release chan struct{}
	once    sync.Once
}

func newGatewayHoldWorker() *gatewayHoldWorker {
	return &gatewayHoldWorker{entered: make(chan int, 16), release: make(chan struct{})}
}

func (w *gatewayHoldWorker) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	w.mu.Lock()
	w.calls++
	n := w.calls
	w.mu.Unlock()
	select {
	case w.entered <- n:
	default:
	}
	<-w.release
	return &providers.LLMResponse{Content: "Turn finished."}, nil
}

func (w *gatewayHoldWorker) GetDefaultModel() string { return "gateway-hold-worker" }

func (w *gatewayHoldWorker) releaseAll() { w.once.Do(func() { close(w.release) }) }

func (w *gatewayHoldWorker) callsSnapshot() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

func (w *gatewayHoldWorker) waitEntered(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case <-w.entered:
	case <-time.After(within):
		t.Fatalf("no dispatched turn entered the provider within %s — the Start Task route never dispatched the bound continuation", within)
	}
}

// TestRestStartTask_DoneBoundTask_ContinuesTheBoundConversation drives the
// web Start Task path the architect named: PATCH status=in_progress on a Done
// task that already has a bound conversation. Required: 200, the task in
// progress, the SAME session id, the bound record at generation+1, and the
// dispatched continuation actually running.
func TestRestStartTask_DoneBoundTask_ContinuesTheBoundConversation(t *testing.T) {
	worker := newGatewayHoldWorker()
	t.Cleanup(worker.releaseAll)

	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	require.NoError(t, os.WriteFile(tmpDir+"/config.json",
		[]byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(tmpDir, "agents"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{
				{ID: "restart-worker", Name: "Restart Worker", Type: config.AgentTypeWorker, Home: filepath.Join(tmpDir, "agents", "restart-worker")},
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), worker)

	// The API's task store must be the loop's own store, so the seeded task is
	// visible to the executor the route dispatches into.
	home := config.OmnipusHomeDir()
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(home, "tasks")),
		taskLock:      task.TaskFileLock,
		taskExecutor:  agent.GetTaskExecutor(al),
	}
	wsID := ensureTestWorkspace(t, api)

	// The bound conversation: a real session for the worker whose lifecycle
	// record is terminal-completed at generation 1 — exactly what run 1 of a
	// Done task leaves behind.
	if _, err := al.GetSessionStore().GetOrCreateMainSession(wsID, "restart-worker"); err != nil {
		t.Fatalf("setup: create the bound session: %v", err)
	}
	sid, err := session.MainSessionID(wsID, "restart-worker")
	require.NoError(t, err)
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID:      sid,
		Generation:     1,
		State:          session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    wsID,
		AgentID:        "restart-worker",
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "t-rest-web"},
	}), "setup: seed the bound record")

	tk := &task.Task{
		ID: "t-rest-web", Title: "web restart", Prompt: "produce the report", Action: task.ActionLLM,
		AgentID: "restart-worker", WorkspaceID: wsID, Status: task.StatusDone,
		SessionID: sid, Result: "run 1 adjudicated result",
	}
	require.NoError(t, api.taskStore.Create(tk), "setup: seed the Done bound task")

	entriesBefore := worker.callsSnapshot()
	w := patchTask(t, api, tk.ID, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"the Start Task route must admit the authorized restart of a Done task with a bound conversation (CR C1 / ARCH A1); body=%s", w.Body.String())

	updated := getTaskFull(t, api, tk.ID)
	require.NotNil(t, updated.SessionId, "the restart must keep the bound conversation")
	require.Equal(t, sid, *updated.SessionId, "the restart must continue the SAME conversation, not mint a fresh one")
	require.Equal(t, gen.TaskStatusInProgress, updated.Status, "the task must leave Done into the executing state")

	// The bound continuation was dispatched: the record advanced one
	// generation and the worker turn is running.
	rec, loadErr := al.GetSessionLifecycleStore().Load(sid)
	require.NoError(t, loadErr)
	require.Equal(t, 2, rec.Generation, "the continuation revives the bound record to generation+1")
	require.Equal(t, sid, rec.ResumedFrom, "the terminal revival mints the next generation via resumed_from")
	worker.waitEntered(t, 30*time.Second)
	require.Greater(t, worker.callsSnapshot(), entriesBefore, "the dispatched continuation must start the worker")
}
