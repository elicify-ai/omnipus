// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// newEmitTestExecutor builds a TaskExecutor backed by a real task.Store and a
// minimal AgentLoop whose EventBus is live, so emitStatusChanged events can be
// observed via SubscribeEvents. agentLoop fields not needed for emit are left
// zero — emitStatusChanged only touches agentLoop.eventBus.
func newEmitTestExecutor(t *testing.T) (*TaskExecutor, *task.Store, *AgentLoop) {
	t.Helper()
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	al := &AgentLoop{eventBus: NewEventBus()}
	te := &TaskExecutor{
		agentLoop:     al,
		store:         store,
		running:       make(map[string]*taskSlot),
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
//
// Traces to: wave spec — emitStatusChanged: failTask emits failed event.
func TestTaskStatusEmit_FailTask(t *testing.T) {
	te, store, al := newEmitTestExecutor(t)
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	tk := createTask(t, store, "running", nil)

	te.failTask(tk.ID, "agent exploded")

	got := drainTaskStatusEvents(sub)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Status != string(task.StatusFailed) {
		t.Errorf("status = %q, want %q", got[0].Status, task.StatusFailed)
	}
	if got[0].TaskID != tk.ID {
		t.Errorf("task_id = %q, want %q", got[0].TaskID, tk.ID)
	}
	if got[0].AgentID != tk.AgentID {
		t.Errorf("agent_id = %q, want %q", got[0].AgentID, tk.AgentID)
	}
	if got[0].SessionID == "" {
		t.Error("session_id must be non-empty (contract requires minLength: 1)")
	}
}

// TestTaskStatusEmit_OnTaskComplete_TerminalStatuses verifies that onTaskComplete
// emits the task's terminal status (both done and failed).
//
// Traces to: wave spec — onTaskComplete: emits terminal status event.
func TestTaskStatusEmit_OnTaskComplete_TerminalStatuses(t *testing.T) {
	te, store, al := newEmitTestExecutor(t)
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	done := createTask(t, store, "completed", nil)
	failed := createTask(t, store, "failed", nil)

	te.onTaskComplete(done)
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
	if !statuses[string(task.StatusDone)] {
		t.Error("missing done event")
	}
	if !statuses[string(task.StatusFailed)] {
		t.Error("missing failed event")
	}
}

// TestTaskStatusEmit_SessionIDFallback verifies that when a task has no SessionID,
// the emit falls back to "task:<id>" so the contract-required session_id field
// is always populated.
//
// Traces to: wave spec — emitStatusChanged: session_id fallback to "task:<id>".
func TestTaskStatusEmit_SessionIDFallback(t *testing.T) {
	te, store, al := newEmitTestExecutor(t)
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	tk := createTask(t, store, "running", nil)
	// Ensure SessionID is empty (createTask doesn't set it).
	if tk.SessionID != "" {
		t.Fatalf("precondition: SessionID should be empty, got %q", tk.SessionID)
	}

	te.emitStatusChanged(tk, task.StatusInProgress)

	got := drainTaskStatusEvents(sub)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	want := "task:" + tk.ID
	if got[0].SessionID != want {
		t.Errorf("session_id = %q, want %q", got[0].SessionID, want)
	}
}

// TestTaskStatusEmit_SessionIDPreserved verifies that when a task has a real
// SessionID, it is carried through in the event payload.
//
// Traces to: wave spec — emitStatusChanged: real session_id preserved.
func TestTaskStatusEmit_SessionIDPreserved(t *testing.T) {
	te, store, al := newEmitTestExecutor(t)
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	tk := createTask(t, store, "running", nil)
	sid := "session-abc-123"
	updated, err := store.Update(tk.ID, task.Patch{SessionID: &sid})
	if err != nil {
		t.Fatalf("update session_id: %v", err)
	}

	te.emitStatusChanged(updated, task.StatusInProgress)

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
//
// Traces to: wave spec — emitStatusChanged: nil agentLoop is a safe no-op.
func TestTaskStatusEmit_NilAgentLoopSkipped(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	te := &TaskExecutor{
		agentLoop:     nil,
		store:         store,
		running:       make(map[string]*taskSlot),
		maxConcurrent: defaultMaxConcurrentTasksPerAgent,
		dispatchSema:  newDispatchSemaphore(4),
	}

	// Must not panic.
	tk := &task.Task{
		ID:      "t-nil",
		AgentID: "jim",
		Status:  task.StatusNext,
	}
	te.emitStatusChanged(tk, task.StatusNext)
}

// TestTaskStatusEmit_StateCriticalDrop verifies that a dropped
// task_status_changed event is counted and classified as state-critical
// (logged at WARN, not DEBUG, since a drop leaves stale task state in the SPA).
//
// Traces to: wave spec — emitStatusChanged: dropped events are state-critical.
func TestTaskStatusEmit_StateCriticalDrop(t *testing.T) {
	te, _, al := newEmitTestExecutor(t)
	// Buffer of 1 — fill it, then emit again to force a drop.
	sub := al.SubscribeEvents(1)
	defer al.UnsubscribeEvents(sub.ID)

	tk := &task.Task{
		ID:      "t-drop",
		AgentID: "jim",
		Status:  task.StatusInProgress,
	}

	te.emitStatusChanged(tk, task.StatusInProgress) // fills the buffer
	te.emitStatusChanged(tk, task.StatusInProgress) // dropped

	if got := al.EventDrops(EventKindTaskStatusChanged); got != 1 {
		t.Errorf("dropped count = %d, want 1", got)
	}
	if !isStateCriticalEventKind(EventKindTaskStatusChanged) {
		t.Error("EventKindTaskStatusChanged must be state-critical so drops are WARN-logged")
	}
}

// TestTaskStatusEmit_AllCanonicalStatuses verifies that every canonical 7-state
// task status can be carried by the emitStatusChanged payload without surprise.
//
// Traces to: wave spec — emitStatusChanged: all 7 statuses are transport-safe.
func TestTaskStatusEmit_AllCanonicalStatuses(t *testing.T) {
	te, store, al := newEmitTestExecutor(t)
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	tk := createTask(t, store, "running", nil)

	// All 7 canonical statuses from the unified vocabulary (Detail #1).
	statuses := []task.Status{
		task.StatusInbox,
		task.StatusNext,
		task.StatusPlanning,
		task.StatusInProgress,
		task.StatusBlocked,
		task.StatusDone,
		task.StatusFailed,
	}
	for _, s := range statuses {
		te.emitStatusChanged(tk, s)
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
		if !seen[string(s)] {
			t.Errorf("missing status %q in emitted events", s)
		}
	}
}
