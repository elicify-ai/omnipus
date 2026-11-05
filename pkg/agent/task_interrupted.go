// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// InterruptedTaskResult is the result text written on a task whose run the
// previous gateway process left in_progress when it stopped. Boot recovery
// recognises it: a one-time trigger fire that process had started is still
// owed its rerun (TaskTriggerScheduler.StartAfterPhysicalBoot), and this
// marker is how that fire is told apart from a run that really failed.
const InterruptedTaskResult = "interrupted: gateway restarted while task was running"

// abandonedRunResult closes a TaskRun the dead process had opened.
const abandonedRunResult = "abandoned: gateway restarted mid-execution"

// ReconcileInterruptedTasks resets every task left `in_progress` by a crashed
// or abandoned previous gateway process to `failed`, so a crash does not
// strand a task in a running state forever. Idempotent; safe when the tasks
// dir is absent.
//
// It must run at physical boot BEFORE anything in this process can claim a
// task (the gateway calls it before it starts the schedulers): every
// in_progress task it sees then belongs to the dead process. Run any later
// and it would fail a run this process had already started — for example
// the owed rerun of a crashed one-time trigger (6b review I2).
//
// ADR-050 RD10 / task-run-history-spec.md §3.5 removed the stuck-run reaper
// (operator decision 2026-07-20: "a run stays in_progress until closed" — no
// liveness-aware background sweep). A TaskRun opened by the crashed process
// is never told its task was reset here, so for each task reset this also
// best-effort closes that task's own open TaskRun(s) to `failed`, bounding
// the orphaned-run gap to "next restart" (MED-#M2 in the ADR).
func ReconcileInterruptedTasks(store *task.Store) {
	if store == nil {
		return
	}
	tasks, err := store.List(task.Filter{Status: task.StatusInProgress})
	if err != nil {
		slog.Error("task: reconcile interrupted tasks: list failed", "error", err)
		return
	}
	reset := 0
	for i := range tasks {
		if err := recoverInterruptedTask(store, tasks[i].ID); err != nil {
			slog.Error("task: reconcile interrupted tasks: reset failed", "id", tasks[i].ID, "error", err)
			continue
		}
		reset++
	}
	if reset > 0 {
		slog.Info("task: reconcile interrupted tasks: reset in_progress→failed on boot", "count", reset)
	}
}

// recoverInterruptedTask turns one dead in_progress claim into failed with
// InterruptedTaskResult, closes the task's open runs and ends its paired goal
// record. The caller has established that the claim belongs to a dead
// process.
func recoverInterruptedTask(store *task.Store, taskID string) error {
	failed := task.StatusFailed
	result := InterruptedTaskResult
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := store.Update(taskID, task.Patch{
		Status:      &failed,
		Result:      &result,
		CompletedAt: &now,
	}); err != nil {
		return err
	}
	closeAbandonedRuns(store, taskID)
	// GOAL-FR-015/FR-027/FR-028 (review finding C1): this reset is a real
	// terminal write, so its paired goal record must end with it, exactly as
	// the judged and user-driven endings do. Left unhooked, a crash-and-
	// restart left every in-flight task's goal record ACTIVE, and the next
	// run of that task would skip Goal.Reactivate and inherit the dead run's
	// counters and criterion statuses. CancelReason is empty (not a user
	// Stop), so the record ends `exhausted`.
	tools.TerminateTaskGoalRecord(
		tools.GoalStoreForTasks(store), taskID, task.StatusFailed, "", result)
	return nil
}

// closeAbandonedRuns closes every currently-open TaskRun of taskID to
// `failed`. Best-effort and non-fatal: a task with zero runs is not an error,
// and any store failure is logged at Warn rather than aborting the boot pass
// for the remaining tasks.
func closeAbandonedRuns(store *task.Store, taskID string) {
	runs, err := store.ListRuns(taskID)
	if err != nil {
		slog.Warn("task: reconcile interrupted tasks: list runs failed, leaving any open run untouched",
			"task_id", taskID, "error", err)
		return
	}
	closed := 0
	for _, run := range runs {
		if !run.IsOpen() {
			continue
		}
		if cErr := store.CloseRun(taskID, run.RunID, task.StatusFailed, abandonedRunResult); cErr != nil {
			slog.Warn("task: reconcile interrupted tasks: close orphaned run failed",
				"task_id", taskID, "run_id", run.RunID, "error", cErr)
			continue
		}
		closed++
	}
	if closed > 0 {
		slog.Info("task: reconcile interrupted tasks: closed orphaned in_progress run(s) on boot",
			"task_id", taskID, "count", closed)
	}
}
