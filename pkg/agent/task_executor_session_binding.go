package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
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

// boundSessionRetryable is the bound-session gate of startTaskNowViaLauncherAs (S1):
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
		if errors.Is(err, session.ErrLifecycleNotFound) {
			return fmt.Errorf(
				"task_executor: task %q is bound to session %q whose runtime classification was never persisted "+
					"(a prior dispatch failed before it could start); Rerun the task", t.ID, t.SessionID)
		}
		// ADR-093 round 2 (gate SFH SF-1): any OTHER read failure is a storage
		// fault, not a missing classification — surface it as the typed
		// bound-session read error so it stays diagnosable, and let the task
		// keep its status (the restart is admitted once the record reads).
		return fmt.Errorf("task_executor: read bound session %q: %w", t.SessionID, err)
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

// isTransientLaunchRefusal reports a refusal that says nothing about the task
// itself and happened BEFORE the task was moved to in-progress
// (dispatchLaunchedTask is what does that, as its last step): the caller gave up
// (cancelled or timed out), the executor is draining for shutdown, or the
// dispatch cap is full. Such a start is worth repeating, so the task keeps its
// prior status instead of being failed (delta review R1 #2; architect ruling
// 2026-10-11). Matching is by errors.Is through the wraps; anything else is
// permanent - binding failures, mint/resolve errors, delegation denial, and the
// lifecycle refusals (stopped, terminal, stale generation, revival failed), all
// of which a retry would only repeat.
//
// ErrDispatchCapReached is deliberately in the list but cannot reach
// failTaskBeforeDispatch on the launcher path (the launcher's own admission
// replaces the dispatch semaphore there); it matters for the non-launcher
// callers, and no launcher-path test may claim to exercise it.
func isTransientLaunchRefusal(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrExecutorDraining) ||
		errors.Is(err, ErrDispatchCapReached)
}

// failTaskBeforeDispatch makes a session-setup failure visible without running
// a worker. Unlike the log-only failTask helper, it retains a Failed-write
// error alongside the setup cause so the dispatch caller can see both.
//
// A transient refusal (isTransientLaunchRefusal) is returned without failing the task.
func (te *TaskExecutor) failTaskBeforeDispatch(taskID string, cause error) error {
	if isTransientLaunchRefusal(cause) {
		// Report it to the caller, but write no Failed disposition: the task
		// keeps its status (and, for a dispatch-stage refusal, its bound
		// session, which a later start re-dispatches).
		return cause
	}
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
