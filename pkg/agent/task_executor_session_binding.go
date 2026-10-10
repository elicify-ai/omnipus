package agent

import (
	"errors"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// persistTaskSessionBinding retries the same binding once. No worker may be
// launched until it succeeds: goal_claim needs the durable binding to prove
// that a delegated task turn owns the session whose goal it is claiming.
func (te *TaskExecutor) persistTaskSessionBinding(taskID, sessionID string) (*task.Task, error) {
	binding := task.Patch{SessionID: &sessionID}
	updated, firstErr := te.store.Update(taskID, binding)
	if firstErr == nil {
		return updated, nil
	}
	updated, retryErr := te.store.Update(taskID, binding)
	if retryErr == nil {
		return updated, nil
	}
	return nil, fmt.Errorf("task_executor: could not persist session binding for task %q after one retry: %w",
		taskID, errors.Join(firstErr, retryErr))
}

// boundSessionRetryable is StartTaskNow's non-launcher idempotency gate (S1):
// it decides whether an already-bound session may be returned as-is. Returns nil
// only when the durable-lifecycle subsystem is unwired (the documented
// test-harness/degraded-boot seam — dispatch proceeds as before the wave) OR the
// bound session's classification record exists AND the task is not Failed.
// Otherwise it returns an error naming the recorded failure, so a second
// StartTaskNow after a failed pre-dispatch setup never reports a false retry
// success — it starts nothing and tells the caller to Rerun.
func (te *TaskExecutor) boundSessionRetryable(t *task.Task) error {
	if t == nil || t.SessionID == "" {
		return nil
	}
	ls := te.getLifecycleStore()
	if ls == nil {
		// No durable record to verify against — the harness seam.
		return nil
	}
	if _, err := ls.Load(t.SessionID); err != nil {
		return fmt.Errorf(
			"task_executor: task %q is bound to session %q whose runtime classification was never persisted "+
				"(a prior dispatch failed before it could start); Rerun the task", t.ID, t.SessionID)
	}
	if t.Status == task.StatusFailed {
		reason := t.Result
		if reason == "" {
			reason = "the previous run failed before it could start"
		}
		return fmt.Errorf(
			"task_executor: task %q is Failed (%s); Rerun the task to start a new run", t.ID, reason)
	}
	return nil
}

// failTaskBeforeDispatch makes a session-setup failure visible without running
// a worker. Unlike the log-only failTask helper, it retains a Failed-write
// error alongside the setup cause so the dispatch caller can see both.
func (te *TaskExecutor) failTaskBeforeDispatch(taskID string, cause error) error {
	failed := task.StatusFailed
	reason := cause.Error()
	now := time.Now().UTC().Format(time.RFC3339)
	updated, err := te.store.Update(taskID, task.Patch{
		Status:      &failed,
		Result:      &reason,
		CompletedAt: &now,
	})
	if err != nil {
		return errors.Join(cause, fmt.Errorf("task_executor: could not persist Failed disposition for task %q: %w", taskID, err))
	}
	terminateTaskGoalRecord(taskID, updated.Status, updated.CancelReason, reason)
	te.emitStatusChanged(updated, task.StatusFailed)
	return cause
}
