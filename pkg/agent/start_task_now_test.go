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

	"github.com/elicify-ai/omnipus/pkg/task"
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
		agentLoop:    &AgentLoop{eventBus: NewEventBus()},
		store:        store,
		running:      make(map[string]*taskSlot),
		dispatchSema: newDispatchSemaphore(4),
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

// TestStartTaskNow_NilRegistry was DELETED (S1 M9, 978c232bc). It asserted
// StartTaskNow's own "agent registry is not available" refusal, which lived
// ONLY on the deleted launcher-less start path. The single remaining start path
// is startTaskNowViaLauncherAs -> steer.SessionLauncher, which resolves the
// target through the registry itself and refuses an unknown agent with
// steer.ErrAgentUnknown (pkg/agent/steer_launcher.go::Launch) — a different
// guard, on a different surface, already covered by the launcher's own tests.
// Keeping the test would only pin the "no session launcher is installed"
// message, which is not the behaviour it was written for.

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

// TestStartTaskNow_Idempotent_ExistingSessionID verifies the guarantee that
// matters for an already-bound task: ONE task, ONE session. Starting it again
// never mints a second session, and while its run is live the second start is
// refused rather than doubled.
//
// Ported to the production start path (S1 M9, 978c232bc). The deleted
// launcher-less path returned the bound id without dispatching; the launcher
// path RE-DISPATCHES the bound session, so a live run is refused by steer's
// admission ("an admission is already reserved") and the binding is untouched.
func TestStartTaskNow_Idempotent_ExistingSessionID(t *testing.T) {
	te, store, _, agentID := newStartTaskNowWithRegistry(t)

	// Hold the run goroutine open so the first run is still live for the second
	// call — the state a user produces by pressing Start twice.
	hold := make(chan struct{})
	te.goroutineCtxHook = func(context.Context, string) { <-hold }
	t.Cleanup(func() { close(hold) })

	tk := createInProgressTask(t, store, agentID, "ws-1")

	first, err := te.StartTaskNow(context.Background(), tk.ID)
	require.NoError(t, err, "the first start must bind a session")
	require.NotEmpty(t, first)

	second, err := te.StartTaskNow(context.Background(), tk.ID)
	require.Error(t, err, "a second start while the run is live must be refused, not doubled")
	require.Empty(t, second, "the refused call must report no session as started")

	got, err := store.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, first, got.SessionID,
		"no second session may be minted for an already-bound task: the binding must be unchanged")
}

// TestStartTaskNow_Idempotent_AlreadyRunning was DELETED (S1 M9, 978c232bc).
// It asserted the "goroutine already running" refusal produced by the
// launcher-less path's own running-map TOCTOU reservation. That string no
// longer exists anywhere in production: the single start path re-dispatches an
// already-bound session (refused by steer.Dispatch when the session is not
// dispatchable) and the run-slot collision is handled inside
// dispatchLaunchedTask. The concurrency guarantee is covered through the real
// path by TestStartTaskNow_Idempotent_ExistingSessionID and
// TestStartTaskNow_NoConcurrentDoubleLaunch.

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
