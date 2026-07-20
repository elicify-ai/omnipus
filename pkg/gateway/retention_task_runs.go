//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Per-task run history retention (ADR-050, docs/internal/specs/task-run-history-spec.md
// §3.5). pkg/task/run_store.go already implements and unit-tests the store
// primitive — (*task.Store).PruneRuns(taskID, cutoff, staleAfter) — which
// deletes aged day files (keeping a floor-of-one) and reaps abandoned
// in_progress runs to failed (RD9/RD10). This file is purely the fan-out
// wiring that calls PruneRuns for every task on the SAME cadence as the
// session-retention sweep (executeSweepTick in retention_goroutine.go) — no
// new goroutine/scheduler.

// retentionTaskRunSweepFn is the function called from executeSweepTick on
// each enabled retention tick to prune every task's run history. gateway.go
// wires it to a closure over the live *task.Store once the store is
// constructed; nil (unwired — e.g. the task store failed to initialize)
// makes the task-run pass a silent no-op, mirroring retentionToolResultSweepFn's
// optionality in retention_goroutine.go. Tests replace this variable with a
// mock to observe call counts/args without touching the filesystem.
var retentionTaskRunSweepFn func(cutoff time.Time, staleAfter time.Duration) (int, error)

// taskRunStaleAfterMultiplier scales cfg.Schedules.RunTimeoutSeconds — the
// only per-run deadline this codebase actually enforces on task/schedule
// dispatch (see SchedulesConfig's doc comment in pkg/config/config.go:
// "deliberately separate from agents.defaults.timeout_seconds, which is
// intentionally 0/disabled"; schedules.go applies RunTimeoutSeconds via
// context.WithTimeout on every dispatch) — into the stuck-run reaper's
// staleAfter window (ADR-050 RD10).
//
// 12x the default 300s (5min) deadline yields a 1 hour staleAfter: generous
// headroom for a per-schedule TimeoutSeconds override, a delegation chain
// that legitimately runs longer than a bare LLM call, and CloseRun write
// latency — while staying far shorter than the 24h sweep cadence, so a
// genuinely abandoned run (gateway died mid-execution, RD10) is reaped on
// the very next tick instead of lingering for days. PruneRuns has no
// "matching live execution" check (RD10's stated ideal) — staleAfter alone
// decides, so it must stay comfortably above realistic run durations to
// avoid false-closing a healthy long-running task.
const taskRunStaleAfterMultiplier = 12

// taskRunStaleAfter resolves the stuck-run reaper's staleAfter duration from
// the live config on every tick (not a boot-time constant), so a
// hot-reloaded schedules.run_timeout_seconds is honored without a restart —
// same live-config-per-tick convention executeSweepTick already uses for
// the session retention window.
func taskRunStaleAfter(cfg *config.Config) time.Duration {
	timeoutSeconds := cfg.Schedules.RunTimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = config.DefaultSchedulesRunTimeoutSeconds
	}
	return time.Duration(timeoutSeconds) * time.Second * taskRunStaleAfterMultiplier
}

// pruneAllTaskRuns is the ADR-050 RD9/RD10 retention pass: it enumerates
// every task known to store — via ListWithUnreadable so a task whose
// task.json is corrupt/unreadable still has its run history pruned, since
// PruneRuns only needs the task ID string, never the parsed Task — and
// calls store.PruneRuns(taskID, cutoff, staleAfter) for each. PruneRuns
// itself is fully implemented and unit-tested (pkg/task/run_store_test.go);
// this function is purely the per-tick fan-out over every task ID. A
// missing runs/ directory (a task with zero runs) is already a no-op inside
// PruneRuns, so no existence check is needed here.
//
// Per-task PruneRuns failures are logged at Warn and do not abort the
// sweep — one task's runs directory being unreadable must not stop every
// other task's retention window from being enforced. The returned error is
// non-nil only when the task list itself could not be enumerated (e.g. the
// tasks directory is unreadable). The returned int is the count of tasks
// PruneRuns was successfully called for (whether or not it found anything
// to prune) — the caller logs it as a per-tick observability signal.
func pruneAllTaskRuns(store *task.Store, cutoff time.Time, staleAfter time.Duration) (int, error) {
	tasks, unreadableIDs, err := store.ListWithUnreadable(task.Filter{})
	if err != nil {
		return 0, fmt.Errorf("retention_sweep: list tasks for run pruning: %w", err)
	}

	visited := 0
	for _, t := range tasks {
		if pruneErr := store.PruneRuns(t.ID, cutoff, staleAfter); pruneErr != nil {
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
		if pruneErr := store.PruneRuns(id, cutoff, staleAfter); pruneErr != nil {
			slog.Warn("retention_sweep: prune task runs failed (unreadable task file)",
				"task_id", id, "error", pruneErr)
			continue
		}
		visited++
	}

	return visited, nil
}
