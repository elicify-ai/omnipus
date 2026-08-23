// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newStartTaskNowWithRegistry builds a TaskExecutor whose agentLoop has a
// registry with one agent pre-registered under agentID. It returns the executor,
// the task store, the agentLoop, and the registered agentID.
func newStartTaskNowWithRegistry(t *testing.T) (*TaskExecutor, *task.Store, *AgentLoop, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	const agentID = "test-agent"
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: agentID, Name: agentID, Type: config.AgentTypeCore},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	// Register the agent instance so StartTaskNow's registry.GetAgent check passes.
	registerInstance(t, al, cfg, home, agentID, config.AgentTypeCore)

	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))

	te := &TaskExecutor{
		agentLoop:    al,
		store:        store,
		running:      make(map[string]*taskSlot),
		dispatchSema: newDispatchSemaphore(4),
	}
	return te, store, al, agentID
}

// TestStartTaskNow_GoroutineContextDetachedFromRequestContext is the regression
// test for the "Task execution failed: turn not started: context canceled" bug.
//
// Root cause: StartTaskNow previously derived the goroutine's context from the
// caller's ctx via context.WithCancel(ctx). When the caller was an HTTP handler,
// r.Context() was canceled as soon as the response was sent — before the
// goroutine had a chance to call processTaskDirect — causing every Run to fail
// immediately.
//
// Fix: StartTaskNow now uses context.WithCancel(context.Background()) so the
// goroutine's context is a detached root, not tied to any HTTP request.
//
// BDD:
//
//	Given a task in_progress with an agent assigned,
//	When StartTaskNow is called with a context that is immediately canceled
//	  (mimicking the HTTP response returning),
//	Then the goroutine still runs (goroutineCtxHook is invoked),
//	And the goroutine's context is NOT done at the time the hook executes
//	  (i.e. it is not derived from the canceled request context).
func TestStartTaskNow_GoroutineContextDetachedFromRequestContext(t *testing.T) {
	te, store, _, agentID := newStartTaskNowWithRegistry(t)

	// Place the task directly into in_progress (the state handleTaskPatch sets it
	// to before calling StartTaskNow).
	tk := createInProgressTask(t, store, agentID, "ws-1")

	// ctxDoneResult is written by the hook: true if the goroutine context was
	// already done when the hook ran (the bug), false if it was still alive (the fix).
	// We check ctx.Done() INSIDE the hook because runTaskFromInProgress's own
	// defer cancel() runs after the hook returns — so the assertion must happen
	// before the goroutine exits.
	ctxDoneResult := make(chan bool, 1)
	goroutineRan := make(chan struct{})

	te.goroutineCtxHook = func(ctx context.Context, taskID string) {
		// Check whether the goroutine's context is done at the moment the hook runs.
		// With the bug: ctx derived from canceled reqCtx → already done here.
		// With the fix: ctx derived from context.Background() → still alive here.
		select {
		case <-ctx.Done():
			ctxDoneResult <- true
		default:
			ctxDoneResult <- false
		}
		close(goroutineRan)
	}

	// Simulate the HTTP request context: create it and cancel it immediately after
	// StartTaskNow returns (mimicking the response flushing and the conn closing).
	reqCtx, cancelReq := context.WithCancel(context.Background())

	sessID, err := te.StartTaskNow(reqCtx, tk.ID)
	// Cancel the "request context" right after the synchronous part of
	// StartTaskNow has completed — this is the timing that caused the bug.
	cancelReq()

	if err != nil {
		t.Fatalf("StartTaskNow returned unexpected error: %v", err)
	}
	if sessID == "" {
		t.Fatal("StartTaskNow returned empty session ID")
	}

	// Wait for the goroutine to actually start (give it generous headroom).
	select {
	case <-goroutineRan:
		// Good: goroutine started.
	case <-time.After(3 * time.Second):
		t.Fatal("goroutine did not start within 3 s — StartTaskNow did not launch it")
	}

	// Read the result from inside the hook.
	wasDone := <-ctxDoneResult

	if wasDone {
		t.Errorf("goroutine context was already done when the hook ran — " +
			"it must be detached from the HTTP request context (context.Background() root)")
	}

	// Also verify that the reqCtx is indeed canceled (prove our test simulated
	// the HTTP response returning).
	select {
	case <-reqCtx.Done():
		// Expected.
	default:
		t.Error("reqCtx should be canceled by now — test setup error")
	}
}

// TestStartTaskNow_CancelViaRunningMap verifies that the goroutine's context CAN
// be canceled via the te.running[taskID] cancel function — the intended path for
// an explicit "cancel task" action — even though it is detached from the request.
//
// BDD:
//
//	Given a task started by StartTaskNow,
//	When the cancel stored in te.running[taskID] is called (TaskExecutor.Stop path),
//	Then the goroutine's context is canceled.
func TestStartTaskNow_CancelViaRunningMap(t *testing.T) {
	te, store, _, agentID := newStartTaskNowWithRegistry(t)

	tk := createInProgressTask(t, store, agentID, "ws-2")

	goroutineRan := make(chan struct{})
	goroutineCtxCh := make(chan context.Context, 1)
	// Block the goroutine until the test is ready to observe cancellation.
	holdGoroutine := make(chan struct{})

	te.goroutineCtxHook = func(ctx context.Context, taskID string) {
		goroutineCtxCh <- ctx
		close(goroutineRan)
		// Block until the test cancels via Stop.
		<-holdGoroutine
	}

	sessID, err := te.StartTaskNow(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTaskNow error: %v", err)
	}
	if sessID == "" {
		t.Fatal("empty session ID")
	}

	select {
	case <-goroutineRan:
	case <-time.After(3 * time.Second):
		t.Fatal("goroutine did not start within 3 s")
	}

	goroutineCtx := <-goroutineCtxCh

	// Context must be alive before we cancel.
	select {
	case <-goroutineCtx.Done():
		t.Fatal("goroutine context already canceled before cancel — unexpected")
	default:
	}

	// Cancel via the running map entry — the intended path for an explicit
	// "cancel task" action. We retrieve the cancel function directly from
	// te.running rather than through a Stop() method (which was removed because
	// it had no production callers and would wrongly hard-cancel tasks that
	// should drain gracefully).
	te.mu.Lock()
	slot, ok := te.running[tk.ID]
	te.mu.Unlock()
	if !ok || slot == nil || slot.cancel == nil {
		t.Fatalf("te.running[%q] must hold a live slot with a non-nil cancel", tk.ID)
	}
	slot.cancel()
	// Unblock the goroutine so it can exit cleanly.
	close(holdGoroutine)

	// The goroutine's context must now be canceled.
	select {
	case <-goroutineCtx.Done():
		// Expected.
	case <-time.After(time.Second):
		t.Error("goroutine context was not canceled within 1 s after Stop()")
	}
}
