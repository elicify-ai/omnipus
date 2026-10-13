// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task_test

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Oracle: #1026 caller follow-up, CHECK F1. A binding failure recorded as Failed
// must survive the REAL run_task caller, rather than being reverted to Next.
// The existing admission-refusal rollback tests must retain their own oracle.
// Only the filesystem writer and paid providers are replaced; the tool, inline
// executor, store and session creation are real. No sentinel/error shape is
// prescribed. The healthy control proves that the same caller can dispatch.
// Launcher-backed dispatch, compound Failed-write errors and unrelated argument
// validation are outside this caller regression (covered by other test packs).
// Serial: the reused fixture installs the package-global atomic-writer seam.
func TestRunTaskTool_SessionBindingFailureRemainsFailed(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode bindingFaultMode
	}{
		{name: "healthy_caller_control", mode: bindingHealthy},
		{name: "persistent_binding_failure", mode: bindingFailAlways},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBindingRunFixture(t, tc.mode, false)
			tool := tools.NewTaskRunTool(f.store)
			// Exactly the production wiring; this is not a dispatcher double.
			tool.SetStartTaskNow(f.executor.StartTaskNow)
			// The delegation policy always applies to an agent-initiated run and
			// fails closed when unwired (founder ruling 2026-10-10). This test's
			// concern is the session-binding failure, not the policy gate, so
			// install an allowing checker.
			tool.SetDelegationDenyChecker(func(context.Context, string, string) *tools.DelegationDenial { return nil })
			before, err := task.New(f.store.Dir()).Get(f.taskID)
			if err != nil {
				t.Fatalf("read caller precondition: %v", err)
			}
			if before.Status != task.StatusNext || before.SessionID != "" {
				t.Fatalf("caller precondition: status/session = %q/%q, want next/empty", before.Status, before.SessionID)
			}

			res := tool.Execute(context.Background(), map[string]any{"task_id": f.taskID})
			deadline, ok := t.Deadline()
			if !ok {
				t.Fatal("caller fixture needs a test deadline to drain all real executor work")
			}
			f.executor.Drain(time.Until(deadline) + time.Minute)
			// Reopen the store AFTER Execute, including the caller's rollback.
			stored, readErr := task.New(f.store.Dir()).Get(f.taskID)
			if readErr != nil {
				t.Fatalf("read task after real run_task caller returned and executor drained: %v", readErr)
			}
			if res == nil {
				t.Fatal("real run_task caller returned a nil tool result")
			}
			snapshot := f.fault.snapshot()
			if tc.mode == bindingHealthy {
				if res.IsError {
					t.Errorf("healthy caller returned error %q, want successful dispatch", res.ForLLM)
				}
				assertBindingWriteAttempts(t, snapshot, []error{nil})
				if stored.Status != task.StatusDone {
					t.Errorf("healthy caller final status = %q, want %q after real claim/adjudication; result=%q", stored.Status, task.StatusDone, stored.Result)
				}
				if stored.SessionID == "" {
					t.Error("healthy caller lost the durable session binding")
				}
				worker := f.worker.snapshot()
				if worker.turns != 1 || worker.calls != 2 {
					t.Errorf("healthy caller worker turns/calls = %d/%d, want 1/2 (goal_claim then final reply)", worker.turns, worker.calls)
				}
				return
			}

			assertBindingWriteAttempts(t, snapshot, []error{f.fault.bindingCause, f.fault.bindingCause})
			assertNoBindingWorkerExecution(t, f)
			if len(snapshot.failedWrites) == 0 {
				t.Fatal("fixture did not reach the executor's Failed disposition write; cannot test caller preservation")
			}
			for _, failed := range snapshot.failedWrites {
				assertBindingFailureReason(t, failed.Result, f.fault.bindingCause)
			}
			if !res.IsError {
				t.Errorf("binding-failure caller result = %q, want an error result", res.ForLLM)
			}
			assertBindingFailureReason(t, res.ForLLM, f.fault.bindingCause)
			if stored.Status != task.StatusFailed {
				t.Errorf("after real run_task caller returned: status = %q, want %q (must not revert to prior %q after executor persisted Failed); result=%q", stored.Status, task.StatusFailed, before.Status, stored.Result)
			}
			assertBindingFailureReason(t, stored.Result, f.fault.bindingCause)
			if stored.SessionID != "" {
				t.Errorf("binding-failure durable session = %q, want empty because both binding writes failed", stored.SessionID)
			}
		})
	}
}
