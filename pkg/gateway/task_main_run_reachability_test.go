//go:build goolm && stdjson

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

type recordingTaskLauncher struct {
	mu   sync.Mutex
	reqs []steer.LaunchRequest
}

func (l *recordingTaskLauncher) Launch(_ context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reqs = append(l.reqs, req)
	return steer.LaunchResult{SessionID: fmt.Sprintf("reach-child-%d", len(l.reqs)), Generation: 1}, nil
}

func (l *recordingTaskLauncher) Dispatch(_ context.Context, _ string, gen int) (steer.DispatchResult, error) {
	return steer.DispatchResult{State: steer.DispatchRunning, Generation: gen}, nil
}

// Reachability (Definition of Done): with the gateway's REAL main resolver
// installed on the real TaskExecutor — the exact pair gateway_boot wires — a
// started task whose assignee owns an eligible main runs as a child of THAT
// main, and the run's recipients (stored on the TaskRun) are the starter's
// main and the assignee's main, deduplicated. Without the resolver the same
// task would run isolated. Real: taskMainResolver, TaskExecutor.StartTaskNow,
// the task store and run store; only the launcher is a recorder.
func TestTaskMainRun_ReachableThroughTheGatewayResolver(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	const wsID = "01JXREACHWS0000000000001"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "WS", Status: "active", CoreTeam: []string{"mia"},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})
	te := agent.GetTaskExecutor(api.agentLoop)
	require.NotNil(t, te, "the agent loop must own a task executor")
	launcher := &recordingTaskLauncher{}
	te.SetSessionLauncher(launcher)
	te.SetMainSessionResolver(taskMainResolver{api: api})

	store := agent.GetTaskStore(api.agentLoop)
	tk := &task.Task{ID: "reach-task-1", Title: "main run", AgentID: "mia", WorkspaceID: wsID,
		Status: task.StatusInProgress, Action: task.ActionLLM}
	require.NoError(t, store.Create(tk))

	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "mia"), wsID)
	sessionID, err := te.StartTaskNow(ctx, tk.ID)
	require.NoError(t, err)
	require.NotEmpty(t, sessionID)

	wantMain, err := session.MainSessionID(wsID, "mia")
	require.NoError(t, err)
	require.Len(t, launcher.reqs, 1)
	require.Equal(t, wantMain, launcher.reqs[0].SteeringSessionID,
		"the run is a child of the assignee's main, not a parentless chat")

	run, err := store.OpenRunForSession(tk.ID, sessionID)
	require.NoError(t, err)
	require.Equal(t, []string{wantMain}, run.RecipientSessionIDs,
		"starter and assignee share a main: one recipient, captured at the start")
}
