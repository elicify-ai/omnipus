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
