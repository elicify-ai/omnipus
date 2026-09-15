// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_superseded_session_test.go covers M6 (UAT 2026-07-31): a
// failed task run restarts in a BRAND NEW session (createTaskSessionSync mints
// one when the restart re-enters ExecuteTask), and the previous run's session
// must be closed out rather than left looking live forever.
// consumeTaskAttempt (task_run_loop.go) is the one restart edge.
package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestConsumeTaskAttempt_SupersededSessionTransitionsToInterrupted
// covers the no-signal path (FR-045): a worker response with no TASK_STATUS
// marker at all routes straight to consumeTaskAttempt without ever
// needing a judge. With MaxAttempts=2, the first attempt's outcome (newAttempt
// 1 < maxAttempts 2) takes the RESTART branch, the
// exact branch this fix's transitionTaskLifecycle call was added to.
//
// Positive lower bound (Binding Rule 4): this does not merely assert
// Status != StatusActive — it pins the EXACT terminal status
// (StatusInterrupted, via LifecycleCancelled's canonical mirror in
// lifecycle_bridge.go), and also confirms the redispatch actually happened
// (non-empty redispatch id, AttemptCount incremented, Status == next) so the
// test cannot pass vacuously against a branch that silently didn't run.
func TestConsumeTaskAttempt_SupersededSessionTransitionsToInterrupted(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	taskStore := GetTaskStore(al)
	maxAttempts := 2
	tk := &task.Task{
		ID: "t-superseded-consume-attempt", AgentID: "native-agent", WorkspaceID: "test-ws",
		Title:       "superseded attempt session (consumeTaskAttempt)",
		Status:      task.StatusInProgress,
		MaxAttempts: &maxAttempts,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	sessStore := al.GetAgentStore("native-agent")
	if sessStore == nil {
		t.Fatal("native-agent session store not available")
	}
	meta, err := sessStore.NewSession(session.SessionTypeTask, "system", "native-agent")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	taskSessionID := meta.ID

	// Precondition (positive control): a freshly minted session must start
	// Active — otherwise a "not Active anymore" assertion below would be
	// meaningless.
	before, err := sessStore.GetMeta(taskSessionID)
	if err != nil {
		t.Fatalf("get session meta before consumeTaskAttempt: %v", err)
	}
	if before.Status != session.StatusActive {
		t.Fatalf("precondition failed: freshly created session status = %q, want %q",
			before.Status, session.StatusActive)
	}

	redispatch := al.taskExecutor.consumeTaskAttempt(context.Background(), tk, taskSessionID,
		"the run ended without a met verdict", nil)

	if redispatch == "" {
		t.Fatal("expected a non-empty re-dispatch id — a no-signal outcome with attempts remaining must " +
			"re-dispatch, not this test proves nothing about the superseded branch otherwise")
	}
	if redispatch != tk.ID {
		t.Errorf("redispatch id = %q, want %q", redispatch, tk.ID)
	}

	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if final.AttemptCount != 1 {
		t.Fatalf("attempt_count = %d, want 1 — the no-signal outcome must consume an attempt via "+
			"consumeTaskAttempt", final.AttemptCount)
	}
	if final.Status != task.StatusNext {
		t.Fatalf("status = %q, want %q (re-dispatchable) — the redispatch branch, not attempt exhaustion, "+
			"must have been taken (maxAttempts=2, this is attempt 1)", final.Status, task.StatusNext)
	}

	after, err := sessStore.GetMeta(taskSessionID)
	if err != nil {
		t.Fatalf("get session meta after consumeTaskAttempt: %v", err)
	}
	if after.Status != session.StatusInterrupted {
		t.Errorf("superseded attempt's session status = %q, want %q — M6: consumeTaskAttempt's "+
			"re-dispatch branch supersedes this session (a brand new one is minted for the next attempt) "+
			"but never transitioned THIS session out of Active, so it would stay Active forever",
			after.Status, session.StatusInterrupted)
	}
}
