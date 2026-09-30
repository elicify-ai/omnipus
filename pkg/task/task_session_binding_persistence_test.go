// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// Oracle: #1026 dispatch ruling. Retry the same session-binding write ONCE.
// Recovery preserves Done. Exhaustion means visible Failed, no worker/provider
// execution; if recording Failed also fails, return BOTH underlying causes.
// All tests are serial because they use the existing task atomic-writer seam.
func TestExecuteTask_SessionBindingRetryRecoversAndCompletes(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode bindingFaultMode
	}{
		{name: "no_fault_instrument_control", mode: bindingHealthy},
		{name: "first_write_fails_retry_succeeds", mode: bindingFailFirst},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBindingRunFixture(t, tc.mode, false)
			err, stored := f.executeAndDrain(t)
			if err != nil {
				t.Errorf("recoverable dispatch error = %v, want nil", err)
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
			if stored.SessionID == "" {
				t.Error("durable SessionID is empty, want the successful binding")
			}
			worker := f.worker.snapshot()
			if worker.turns != 1 || worker.calls != 2 {
				t.Errorf("worker turns/calls = %d/%d, want 1/2 (real goal_claim then final reply)", worker.turns, worker.calls)
			}
			if len(worker.sessionIDs) != 2 {
				t.Errorf("provider binding observations = %d, want 2", len(worker.sessionIDs))
			}
			for i, sessionID := range worker.sessionIDs {
				if sessionID == "" || sessionID != stored.SessionID {
					t.Errorf("provider request %d observed binding %q, want durable nonempty %q before execution", i+1, sessionID, stored.SessionID)
				}
			}
			if f.judge.callCount() == 0 {
				t.Error("Judge provider calls = 0, want real adjudication of the accepted task claim")
			}
		})
	}
}

func TestExecuteTask_SessionBindingRetryExhaustedFailsWithoutWorker(t *testing.T) {
	f := newBindingRunFixture(t, bindingFailAlways, false)
	err, stored := f.executeAndDrain(t)
	assertBindingWriteAttempts(t, f.fault.snapshot(), []error{f.fault.bindingCause, f.fault.bindingCause})
	assertNoBindingWorkerExecution(t, f)
	if stored.Status != task.StatusFailed {
		t.Errorf("final status = %q, want %q after both binding writes fail; result=%q", stored.Status, task.StatusFailed, stored.Result)
	}
	assertBindingFailureReason(t, stored.Result, f.fault.bindingCause)
	// The ruling requires an inspectable Failed task, and any returned error
	// must identify the cause. It does not require a particular outer sentence.
	if err != nil {
		assertBindingErrorCause(t, err, f.fault.bindingCause)
	}
}

func TestExecuteTask_SessionBindingAndFailedWriteErrorsBothReachCaller(t *testing.T) {
	f := newBindingRunFixture(t, bindingFailAlways, true)
	err, _ := f.executeAndDrain(t)
	snapshot := f.fault.snapshot()
	assertBindingWriteAttempts(t, snapshot, []error{f.fault.bindingCause, f.fault.bindingCause})
	assertNoBindingWorkerExecution(t, f)
	if len(snapshot.failedWrites) == 0 {
		t.Error("Failed disposition write attempts = 0, want an attempt to persist the binding failure visibly")
	}
	for _, attempted := range snapshot.failedWrites {
		assertBindingFailureReason(t, attempted.Result, f.fault.bindingCause)
	}
	// No retry count for disposition persistence is specified. Every attempted
	// Failed write is rejected by a different cause, so nil or just one cause
	// cannot honestly report this compounded failure to the existing caller.
	assertBindingErrorCause(t, err, f.fault.bindingCause)
	assertBindingErrorCause(t, err, f.fault.dispositionCause)
}

func assertBindingWriteAttempts(t *testing.T, snapshot bindingFaultSnapshot, wantErrors []error) {
	t.Helper()
	if len(snapshot.bindings) != len(wantErrors) {
		t.Errorf("session-binding write attempts = %d, want %d (initial write plus exactly one retry on failure)", len(snapshot.bindings), len(wantErrors))
		return
	}
	for i, attempt := range snapshot.bindings {
		if attempt.sessionID == "" || attempt.sessionID != snapshot.bindings[0].sessionID {
			t.Errorf("binding attempt %d used session %q, want the same nonempty session %q", i+1, attempt.sessionID, snapshot.bindings[0].sessionID)
		}
		if !errors.Is(attempt.err, wantErrors[i]) {
			t.Errorf("binding write %d error = %v, want cause %v", i+1, attempt.err, wantErrors[i])
		}
	}
}

func assertNoBindingWorkerExecution(t *testing.T, f *bindingRunFixture) {
	t.Helper()
	worker := f.worker.snapshot()
	if worker.calls != 0 || worker.turns != 0 {
		t.Errorf("worker/provider calls/turns = %d/%d, want 0/0 after binding retry exhaustion", worker.calls, worker.turns)
	}
	if calls := f.judge.callCount(); calls != 0 {
		t.Errorf("Judge provider calls = %d, want 0 after binding retry exhaustion", calls)
	}
}

func assertBindingFailureReason(t *testing.T, reason string, cause error) {
	t.Helper()
	// The distinctive cause includes the specified persistence context; assert
	// the whole injected cause without inventing unspecified wrapper wording.
	if !strings.Contains(reason, cause.Error()) {
		t.Errorf("visible Failed reason = %q, want session-binding persistence cause %q", reason, cause.Error())
	}
}

func assertBindingErrorCause(t *testing.T, got, cause error) {
	t.Helper()
	if !errors.Is(got, cause) {
		t.Errorf("caller error = %v, want retained underlying cause %q", got, cause.Error())
	}
	if got == nil || !strings.Contains(got.Error(), cause.Error()) {
		t.Errorf("caller-visible error = %v, want complete cause text %q", got, cause.Error())
	}
}
