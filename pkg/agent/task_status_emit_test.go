// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/taskstore"
)

// newEmitTestExecutor builds a TaskExecutor backed by a real TaskStore and a
// minimal AgentLoop whose EventBus is live, so emitStatusChanged events can be
// observed via SubscribeEvents. agentLoop fields not needed for emit are left
// zero — emitStatusChanged only touches agentLoop.eventBus.
func newEmitTestExecutor(t *testing.T) (*TaskExecutor, *taskstore.TaskStore, *AgentLoop) {
	t.Helper()
	dir := t.TempDir()
	store := taskstore.New(filepath.Join(dir, "tasks"))
	al := &AgentLoop{eventBus: NewEventBus()}
	te := &TaskExecutor{
		agentLoop:     al,
		store:         store,
		running:       make(map[string]context.CancelFunc),
		maxConcurrent: defaultMaxConcurrentTasksPerAgent,
		dispatchSema:  newDispatchSemaphore(4),
	}
	return te, store, al
}

// drainTaskStatusEvents reads events from sub.C (non-blocking) and returns the
// TaskStatusChangedPayload values found.
func drainTaskStatusEvents(sub EventSubscription) []TaskStatusChangedPayload {
	var out []TaskStatusChangedPayload
	for {
		select {
		case evt, ok := <-sub.C:
			if !ok {
				return out
			}
			if evt.Kind != EventKindTaskStatusChanged {
				continue
			}
			if p, ok := evt.Payload.(TaskStatusChangedPayload); ok {
				out = append(out, p)
			}
		default:
			return out
		}
	}
}

// TestTaskStatusEmit_FailTask verifies that failTask emits a task_status_changed
// event with status "failed".
func TestTaskStatusEmit_FailTask(t *testing.T) {
	te, store, al := newEmitTestExecutor(t)
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	task := createTask(t, store, "running", nil)

	te.failTask(task.ID, "agent exploded")

	got := drainTaskStatusEvents(sub)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Status != "failed" {
		t.Errorf("status = %q, want %q", got[0].Status, "failed")
	}
	if got[0].TaskID != task.ID {
		t.Errorf("task_id = %q, want %q", got[0].TaskID, task.ID)
	}
	if got[0].AgentID != task.AgentID {
		t.Errorf("agent_id = %q, want %q", got[0].AgentID, task.AgentID)
	}
	if got[0].SessionID == "" {
		t.Error("session_id must be non-empty (contract requires minLength: 1)")
	}
}

// TestTaskStatusEmit_OnTaskComplete_TerminalStatuses verifies that onTaskComplete
// emits the task's terminal status (both completed and failed).
func TestTaskStatusEmit_OnTaskComplete_TerminalStatuses(t *testing.T) {
	te, store, al := newEmitTestExecutor(t)
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	completed := createTask(t, store, "completed", nil)
	failed := createTask(t, store, "failed", nil)

	te.onTaskComplete(completed)
	te.onTaskComplete(failed)

	got := drainTaskStatusEvents(sub)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}

	statuses := map[string]bool{}
	for _, p := range got {
		statuses[p.Status] = true
		if p.SessionID == "" {
			t.Error("session_id must be non-empty")
		}
	}
	if !statuses["completed"] {
		t.Error("missing completed event")
	}
	if !statuses["failed"] {
		t.Error("missing failed event")
	}
}

// TestTaskStatusEmit_SessionIDFallback verifies that when a task has no SessionID,
// the emit falls back to "task:<id>" so the contract-required session_id field
// is always populated.
func TestTaskStatusEmit_SessionIDFallback(t *testing.T) {
	te, store, al := newEmitTestExecutor(t)
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	task := createTask(t, store, "running", nil)
	// Ensure SessionID is empty (createTask doesn't set it).
	if task.SessionID != "" {
		t.Fatalf("precondition: SessionID should be empty, got %q", task.SessionID)
	}

	te.emitStatusChanged(task, "running")

	got := drainTaskStatusEvents(sub)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	want := "task:" + task.ID
	if got[0].SessionID != want {
		t.Errorf("session_id = %q, want %q", got[0].SessionID, want)
	}
}

// TestTaskStatusEmit_SessionIDPreserved verifies that when a task has a real
// SessionID, it is carried through in the event payload.
func TestTaskStatusEmit_SessionIDPreserved(t *testing.T) {
	te, store, al := newEmitTestExecutor(t)
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	task := createTask(t, store, "running", nil)
	sid := "session-abc-123"
	updated, err := store.Update(task.ID, taskstore.TaskPatch{SessionID: &sid})
	if err != nil {
		t.Fatalf("update session_id: %v", err)
	}

	te.emitStatusChanged(updated, "running")

	got := drainTaskStatusEvents(sub)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].SessionID != sid {
		t.Errorf("session_id = %q, want %q", got[0].SessionID, sid)
	}
}

// TestTaskStatusEmit_NilAgentLoopSkipped verifies that emitStatusChanged is a
// no-op when agentLoop is nil (unit tests that construct TaskExecutor directly).
func TestTaskStatusEmit_NilAgentLoopSkipped(t *testing.T) {
	dir := t.TempDir()
	store := taskstore.New(filepath.Join(dir, "tasks"))
	te := &TaskExecutor{
		agentLoop:     nil,
		store:         store,
		running:       make(map[string]context.CancelFunc),
		maxConcurrent: defaultMaxConcurrentTasksPerAgent,
		dispatchSema:  newDispatchSemaphore(4),
	}

	// Must not panic.
	task := &taskstore.TaskEntity{
		ID:      "t-nil",
		AgentID: "jim",
		Status:  "queued",
	}
	te.emitStatusChanged(task, "queued")
}

// TestTaskStatusEmit_StateCriticalDrop verifies that a dropped
// task_status_changed event is counted and classified as state-critical
// (logged at WARN, not DEBUG, since a drop leaves stale task state in the SPA).
func TestTaskStatusEmit_StateCriticalDrop(t *testing.T) {
	te, _, al := newEmitTestExecutor(t)
	// Buffer of 1 — fill it, then emit again to force a drop.
	sub := al.SubscribeEvents(1)
	defer al.UnsubscribeEvents(sub.ID)

	task := &taskstore.TaskEntity{
		ID:      "t-drop",
		AgentID: "jim",
		Status:  "running",
	}

	te.emitStatusChanged(task, "running") // fills the buffer
	te.emitStatusChanged(task, "running") // dropped

	if got := al.EventDrops(EventKindTaskStatusChanged); got != 1 {
		t.Errorf("dropped count = %d, want 1", got)
	}
	if !isStateCriticalEventKind(EventKindTaskStatusChanged) {
		t.Error("EventKindTaskStatusChanged must be state-critical so drops are WARN-logged")
	}
}

// TestTaskStatusEmit_AllCanonicalStatuses verifies that every canonical task
// status value from the AsyncAPI enum can be carried by the payload without
// surprise (queued, assigned, running, completed, failed).
func TestTaskStatusEmit_AllCanonicalStatuses(t *testing.T) {
	te, store, al := newEmitTestExecutor(t)
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	task := createTask(t, store, "running", nil)

	statuses := []string{"queued", "assigned", "running", "completed", "failed"}
	for _, s := range statuses {
		te.emitStatusChanged(task, s)
	}

	got := drainTaskStatusEvents(sub)
	if len(got) != len(statuses) {
		t.Fatalf("expected %d events, got %d", len(statuses), len(got))
	}
	seen := make(map[string]bool)
	for _, p := range got {
		seen[p.Status] = true
	}
	for _, s := range statuses {
		if !seen[s] {
			t.Errorf("missing status %q in emitted events", s)
		}
	}
}
