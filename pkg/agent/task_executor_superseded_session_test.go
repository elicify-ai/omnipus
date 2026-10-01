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

// TestConsumeTaskAttempt_SupersededSessionStaysActive
// covers the no-signal path (FR-045): a worker response with no TASK_STATUS
// marker at all routes straight to consumeTaskAttempt without ever
// needing a judge. With MaxAttempts=2, the first attempt's outcome (newAttempt
// 1 < maxAttempts 2) takes the RESTART branch, the
// exact branch this fix's transitionTaskLifecycle call was added to.
//
// Sub-agent control plane ADR D4/MAJ-009 rename note: this test originally
// pinned StatusInterrupted as the superseded session's exact terminal status
// (task_executor_judge.go::supersedeTaskSession, via LifecycleStopped's
// canonical mirror). The ADR retires that wire value and makes stopped stay
// coarse-active instead, so the session-meta assertion below now checks
// StatusActive — which is also this session's starting value, so by itself
// it can no longer prove supersedeTaskSession actually ran (vs. silently not
// running). The task-level assertions above (non-empty redispatch id,
// AttemptCount incremented, Status == next) remain the load-bearing proof
// that the redispatch branch fired; flagged for qa-lead to add a positive
// signal for the session-meta half specifically in a follow-up.
func TestConsumeTaskAttempt_SupersededSessionStaysActive(t *testing.T) {
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
	if after.Status != session.StatusActive {
		t.Errorf("superseded attempt's session status = %q, want %q — M6: a superseded, stopped "+
			"session stays coarse-active (ADR D4/MAJ-009); supersedeTaskSession's explicit write "+
			"still runs so a session whose status drifted away from active for any other reason converges",
			after.Status, session.StatusActive)
	}
}
