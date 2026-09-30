// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task_test

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// Oracle: #1026's retry-once/visible-failure ruling applies identically to
// StartTaskNow's inline binding path. Reuse the RED pack's real loop and
// filesystem fault seam; do not replace task dispatch or the ownership guard.
func TestStartTaskNow_SessionBindingRetryRecoversAndCompletes(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode bindingFaultMode
	}{
		{name: "no_fault_instrument_control", mode: bindingHealthy},
		{name: "first_write_fails_retry_succeeds", mode: bindingFailFirst},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBindingRunFixture(t, tc.mode, false)
			sessionID, stored, err := startBindingTaskAndDrain(t, f)
			if err != nil {
				t.Errorf("recoverable manual dispatch error = %v, want nil", err)
			}
			wantWrites := []error{nil}
			if tc.mode == bindingFailFirst {
				wantWrites = []error{f.fault.bindingCause, nil}
			}
			snapshot := f.fault.snapshot()
			assertBindingWriteAttempts(t, snapshot, wantWrites)
			if len(snapshot.failedWrites) != 0 {
				t.Errorf("Failed disposition writes = %d, want 0 after successful binding", len(snapshot.failedWrites))
			}
			if stored.Status != task.StatusDone {
				t.Errorf("final status = %q, want %q after binding recovery; result=%q", stored.Status, task.StatusDone, stored.Result)
			}
			if sessionID == "" || sessionID != stored.SessionID {
				t.Errorf("returned session = %q, want the durable nonempty binding %q", sessionID, stored.SessionID)
			}
			worker := f.worker.snapshot()
			if worker.turns != 1 || worker.calls != 2 {
				t.Errorf("worker turns/calls = %d/%d, want 1/2 (real goal_claim then final reply)", worker.turns, worker.calls)
			}
			if len(worker.sessionIDs) != 2 {
				t.Errorf("provider binding observations = %d, want 2", len(worker.sessionIDs))
			}
			for i, observed := range worker.sessionIDs {
				if observed == "" || observed != sessionID {
					t.Errorf("provider request %d observed binding %q, want durable %q before execution", i+1, observed, sessionID)
				}
			}
			if f.judge.callCount() == 0 {
				t.Error("Judge provider calls = 0, want real adjudication of the accepted task claim")
			}
		})
	}
}

func TestStartTaskNow_SessionBindingRetryExhaustedFailsWithoutWorker(t *testing.T) {
	f := newBindingRunFixture(t, bindingFailAlways, false)
	sessionID, stored, err := startBindingTaskAndDrain(t, f)
	assertBindingWriteAttempts(t, f.fault.snapshot(), []error{f.fault.bindingCause, f.fault.bindingCause})
	assertNoBindingWorkerExecution(t, f)
	if sessionID != "" {
		t.Errorf("returned session = %q, want empty after binding failure", sessionID)
	}
	if stored.SessionID != "" {
		t.Errorf("durable session = %q, want empty after both binding writes failed", stored.SessionID)
	}
	if stored.Status != task.StatusFailed {
		t.Errorf("final status = %q, want %q after both binding writes fail; result=%q", stored.Status, task.StatusFailed, stored.Result)
	}
	assertBindingFailureReason(t, stored.Result, f.fault.bindingCause)
	assertBindingErrorCause(t, err, f.fault.bindingCause)
}

func TestStartTaskNow_SessionBindingAndFailedWriteErrorsBothReachCaller(t *testing.T) {
	f := newBindingRunFixture(t, bindingFailAlways, true)
	sessionID, _, err := startBindingTaskAndDrain(t, f)
	snapshot := f.fault.snapshot()
	assertBindingWriteAttempts(t, snapshot, []error{f.fault.bindingCause, f.fault.bindingCause})
	assertNoBindingWorkerExecution(t, f)
	if sessionID != "" {
		t.Errorf("returned session = %q, want empty after binding failure", sessionID)
	}
	if len(snapshot.failedWrites) == 0 {
		t.Error("Failed disposition write attempts = 0, want an attempt to persist the binding failure visibly")
	}
	for _, attempted := range snapshot.failedWrites {
		assertBindingFailureReason(t, attempted.Result, f.fault.bindingCause)
	}
	assertBindingErrorCause(t, err, f.fault.bindingCause)
	assertBindingErrorCause(t, err, f.fault.dispositionCause)
}

func startBindingTaskAndDrain(t *testing.T, f *bindingRunFixture) (string, *task.Task, error) {
	t.Helper()
	// Match the manual caller: its PATCH already moved the task in progress.
	inProgress := task.StatusInProgress
	if _, err := f.store.Update(f.taskID, task.Patch{Status: &inProgress}); err != nil {
		t.Fatalf("binding fixture: transition manual task in progress: %v", err)
	}
	deadline, ok := t.Deadline()
	if !ok {
		t.Fatal("binding fixture requires a go test timeout to verify complete draining")
	}
	sessionID, err := f.executor.StartTaskNow(context.Background(), f.taskID)
	// No worker can outlive this observation or the atomic-writer interceptor.
	f.executor.Drain(time.Until(deadline) + time.Minute)
	stored, readErr := f.store.Get(f.taskID)
	if readErr != nil {
		t.Fatalf("binding fixture: read manual task after executor drained: %v", readErr)
	}
	return sessionID, stored, err
}
