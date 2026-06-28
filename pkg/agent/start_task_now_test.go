// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/task"
)

// newStartTaskNowExecutor builds a minimal TaskExecutor with a nil agentLoop
// suitable for testing the idempotency guards and error paths in StartTaskNow
// that do not require a live agent registry.
func newStartTaskNowExecutor(t *testing.T) (*TaskExecutor, *task.Store) {
	t.Helper()
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	// agentLoop is nil — StartTaskNow guards against nil registry before
	// attempting to call GetAgentStore, so the nil registry path exercises the
	// "registry not available" error branch cleanly.
	te := &TaskExecutor{
		agentLoop:     &AgentLoop{eventBus: NewEventBus()},
		store:         store,
		running:       make(map[string]*taskSlot),
		maxConcurrent: defaultMaxConcurrentTasksPerAgent,
		dispatchSema:  newDispatchSemaphore(4),
	}
	return te, store
}

// createInProgressTask creates a task.Task with status=in_progress in the store
// directly (bypassing the transition guard) for testing purposes.
func createInProgressTask(t *testing.T, store *task.Store, agentID, workspaceID string) *task.Task {
	t.Helper()
	tk := &task.Task{
		Title:       "TestTask",
		Prompt:      "do something",
		AgentID:     agentID,
		Priority:    3,
		Action:      task.ActionLLM,
		Status:      task.StatusNext, // create at next; store won't reject next
		WorkspaceID: workspaceID,
	}
	require.NoError(t, store.Create(tk))
	// Advance to in_progress via Update (bypasses ClaimForRun which requires next).
	updated, err := store.Update(tk.ID, task.Patch{Status: ptrStatus(task.StatusInProgress)})
	require.NoError(t, err)
	return updated
}

// TestStartTaskNow_NilRegistry verifies that StartTaskNow returns an error when
// the agent registry is unavailable (nil agentLoop.registry).
//
// BDD:
//
//	Given a TaskExecutor whose AgentLoop has a nil registry,
//	When StartTaskNow is called for a task with an assigned agent,
//	Then an error is returned mentioning "registry" and no session is created.
func TestStartTaskNow_NilRegistry(t *testing.T) {
	te, store := newStartTaskNowExecutor(t)
	// te.agentLoop.registry is nil (AgentLoop constructed without one).

	tk := createInProgressTask(t, store, "some-agent", "ws-1")

	sessID, err := te.StartTaskNow(context.Background(), tk.ID)
	assert.Error(t, err, "StartTaskNow must error when registry is nil")
	assert.Empty(t, sessID, "no session ID must be returned on error")
	assert.Contains(t, err.Error(), "registry",
		"error must mention 'registry' so caller can diagnose")
}

// TestStartTaskNow_NoAgentID verifies that StartTaskNow returns an error when
// the task has no assigned agent.
//
// BDD:
//
//	Given a task with no agent_id,
//	When StartTaskNow is called,
//	Then an error is returned.
func TestStartTaskNow_NoAgentID(t *testing.T) {
	te, store := newStartTaskNowExecutor(t)

	tk := &task.Task{
		Title:       "Unassigned",
		Prompt:      "do something",
		Action:      task.ActionLLM,
		Status:      task.StatusNext,
		AgentID:     "", // no agent
		WorkspaceID: "ws-test",
	}
	require.NoError(t, store.Create(tk))

	sessID, err := te.StartTaskNow(context.Background(), tk.ID)
	assert.Error(t, err, "StartTaskNow must error when task has no agent_id")
	assert.Empty(t, sessID)
}

// TestStartTaskNow_Idempotent_ExistingSessionID verifies that StartTaskNow is a
// no-op and returns the existing session_id when the task already has one set.
//
// BDD:
//
//	Given a task in_progress whose session_id is already "existing-session-123",
//	When StartTaskNow is called,
//	Then the existing session_id is returned without error and no goroutine is launched.
func TestStartTaskNow_Idempotent_ExistingSessionID(t *testing.T) {
	te, store := newStartTaskNowExecutor(t)

	tk := &task.Task{
		Title:       "AlreadyRunning",
		Prompt:      "do something",
		Action:      task.ActionLLM,
		Status:      task.StatusNext,
		AgentID:     "some-agent",
		SessionID:   "existing-session-123",
		WorkspaceID: "ws-test",
	}
	require.NoError(t, store.Create(tk))

	sessID, err := te.StartTaskNow(context.Background(), tk.ID)
	require.NoError(t, err, "StartTaskNow on a task with existing session_id must succeed")
	assert.Equal(t, "existing-session-123", sessID,
		"StartTaskNow must return the existing session_id")

	// Confirm the running map was not touched (no goroutine launched).
	te.mu.Lock()
	_, registered := te.running[tk.ID]
	te.mu.Unlock()
	assert.False(t, registered, "no goroutine must be registered for an idempotent call")
}

// TestStartTaskNow_Idempotent_AlreadyRunning verifies that StartTaskNow returns
// an error (not a panic or second session) when the task's cancel is already
// registered in the running map.
//
// BDD:
//
//	Given a task in_progress and a cancel already in te.running[taskID],
//	When StartTaskNow is called,
//	Then an error is returned mentioning "goroutine already running".
func TestStartTaskNow_Idempotent_AlreadyRunning(t *testing.T) {
	te, store := newStartTaskNowExecutor(t)

	tk := createInProgressTask(t, store, "some-agent", "ws-1")

	// Manually register a live slot to simulate a running goroutine.
	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = cancelCtx
	te.mu.Lock()
	te.running[tk.ID] = &taskSlot{cancel: cancel, reserved: false}
	te.mu.Unlock()
	t.Cleanup(func() {
		te.mu.Lock()
		delete(te.running, tk.ID)
		te.mu.Unlock()
	})

	sessID, err := te.StartTaskNow(context.Background(), tk.ID)
	assert.Error(t, err, "StartTaskNow must error when goroutine is already running")
	assert.Empty(t, sessID)
	assert.Contains(t, err.Error(), "goroutine already running")
}

// TestStartTaskNow_TaskNotFound verifies that StartTaskNow returns an error for
// a non-existent task ID.
//
// BDD:
//
//	Given a task ID that does not exist in the store,
//	When StartTaskNow is called,
//	Then an error is returned.
func TestStartTaskNow_TaskNotFound(t *testing.T) {
	te, store := newStartTaskNowExecutor(t)
	_ = store // used for its dir

	sessID, err := te.StartTaskNow(context.Background(), "does-not-exist")
	assert.Error(t, err, "StartTaskNow must error for a non-existent task ID")
	assert.Empty(t, sessID)
}
