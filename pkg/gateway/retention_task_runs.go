// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// Per-task run history retention (ADR-050, docs/internal/specs/task-run-history-spec.md
// §3.5). pkg/task/run_store.go already implements and unit-tests the store
// primitive — (*task.Store).PruneRuns(taskID, cutoff) — which deletes aged
// day files, keeping a floor-of-one (RD9). This file is purely the fan-out
// wiring that calls PruneRuns for every task on the SAME cadence as the
// session-retention sweep (executeSweepTick in retention_goroutine.go) — no
// new goroutine/scheduler.
//
// No stuck-run reaper: a run stays in_progress until its own execution
// closes it (operator decision 2026-07-20) — "a run could even go over
// days." The staleAfter/taskRunStaleAfterMultiplier plumbing that used to
// derive a reaper deadline from cfg.Schedules.RunTimeoutSeconds has been
// removed along with the reaper itself. A liveness-aware reaper is a
// planned follow-up requiring careful design (ADR-050 follow-up).

// retentionTaskRunSweepFn is the function called from executeSweepTick on
// each enabled retention tick to prune every task's run history. gateway.go
// wires it to a closure over the live *task.Store once the store is
// constructed; nil (unwired — e.g. the task store failed to initialize)
// makes the task-run pass a silent no-op, mirroring retentionToolResultSweepFn's
// optionality in retention_goroutine.go. Tests replace this variable with a
// mock to observe call counts/args without touching the filesystem.
var retentionTaskRunSweepFn func(cutoff time.Time) (int, error)

// pruneAllTaskRuns is the ADR-050 RD9 retention pass: it enumerates every
// task known to store — via ListWithUnreadable so a task whose task.json is
// corrupt/unreadable still has its run history pruned, since PruneRuns only
// needs the task ID string, never the parsed Task — and calls
// store.PruneRuns(taskID, cutoff) for each. PruneRuns itself is fully
// implemented and unit-tested (pkg/task/run_store_test.go); this function is
// purely the per-tick fan-out over every task ID. A missing runs/ directory
// (a task with zero runs) is already a no-op inside PruneRuns, so no
// existence check is needed here.
//
// Per-task PruneRuns failures are logged at Warn and do not abort the
// sweep — one task's runs directory being unreadable must not stop every
// other task's retention window from being enforced. The returned error is
// non-nil only when the task list itself could not be enumerated (e.g. the
// tasks directory is unreadable). The returned int is the count of tasks
// PruneRuns was successfully called for (whether or not it found anything
// to prune) — the caller logs it as a per-tick observability signal.
func pruneAllTaskRuns(store *task.Store, cutoff time.Time) (int, error) {
	tasks, unreadableIDs, err := store.ListWithUnreadable(task.Filter{})
	if err != nil {
		return 0, fmt.Errorf("retention_sweep: list tasks for run pruning: %w", err)
	}

	visited := 0
	for _, t := range tasks {
		if pruneErr := store.PruneRuns(t.ID, cutoff); pruneErr != nil {
			slog.Warn("retention_sweep: prune task runs failed",
				"task_id", t.ID, "error", pruneErr)
			continue
		}
		visited++
	}
	// Unreadable task.json files still get their runs/ directory pruned —
	// see the doc comment above. Unreadability is already logged by
	// ListWithUnreadable itself (task: skip unreadable task file); no
	// duplicate log line here on the happy path.
	for _, id := range unreadableIDs {
		if pruneErr := store.PruneRuns(id, cutoff); pruneErr != nil {
			slog.Warn("retention_sweep: prune task runs failed (unreadable task file)",
				"task_id", id, "error", pruneErr)
			continue
		}
		visited++
	}

	return visited, nil
}
