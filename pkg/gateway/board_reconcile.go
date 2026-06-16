//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// board_reconcile.go — boot-time recovery of GTD board tasks that were left in
// status "active" by a previous gateway process that crashed, was OOM-killed, or
// received SIGKILL before its onComplete callback could persist the terminal status.
// Wired into setupAndStartServices (gateway.go), after ensureDefaultWorkspace and before
// any request handler is reachable, so no /start call can race with reconciliation.

import (
	"log/slog"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/boardtask"
)

// reconcileStuckBoardTasks is called once at gateway startup to recover board tasks
// that were left with status "active" by a crashed or abandoned previous process.
//
// A board task only acquires status "active" when ExecuteBoardTask is dispatched.
// On graceful shutdown, the goroutine's activeRequests entry is drained and onComplete
// transitions the task to "done" or "failed". On crash/OOM/SIGKILL the goroutine is
// abandoned and onComplete never runs — the task remains "active" forever, causing
// every subsequent POST /start to 409-conflict permanently (unretryable via the API).
//
// This function scans all GTD tasks and resets any "active" ones to "failed" with a
// descriptive result note. It is idempotent: if there are no stuck tasks, it returns
// immediately without writing anything. It is safe when the tasks directory is absent
// or empty.
func (a *restAPI) reconcileStuckBoardTasks() {
	tasks, err := a.listBoardTasks()
	if err != nil {
		slog.Error("board_reconcile: cannot list board tasks — skipping stuck-task recovery",
			"error", err)
		return
	}

	reset := 0
	now := time.Now().UTC().Format(time.RFC3339)
	for _, t := range tasks {
		if t.Status != boardtask.StatusActive {
			continue
		}
		t.Status = boardtask.StatusFailed
		t.Result = "interrupted: gateway restarted while task was running"
		t.UpdatedAt = now
		if writeErr := a.writeBoardTask(t); writeErr != nil {
			slog.Error("board_reconcile: failed to reset stuck task",
				"id", t.ID, "error", writeErr)
			continue
		}
		reset++
	}

	if reset > 0 {
		slog.Info("board_reconcile: reset stuck active tasks to failed on boot",
			"count", reset)
	}
}

// reconcileOrphanBlockedByEdges is called once at gateway startup to drop any
// blocked_by edges that point at task files that no longer exist on disk.
//
// Orphan edges arise when a task is deleted by a path that did not cascade (e.g.
// a manual file removal, or a crash between the file delete and the cascade), or
// when a blocked_by target is hand-edited away. A waiting task whose only blocker
// is an orphan would otherwise never advance, because AdvanceBlockedDependents is
// keyed on the completion of a real task that will never complete. DropOrphanEdges
// removes those stale references so the dependency graph self-heals on boot.
//
// It is idempotent and safe when the tasks directory is absent or empty. It runs
// before any request handler is reachable, so no concurrent mutation can race it.
func (a *restAPI) reconcileOrphanBlockedByEdges() {
	removed, err := boardtask.DropOrphanEdges(a.boardTasksDir())
	if err != nil {
		slog.Error("board_reconcile: orphan blocked_by edge cleanup failed", "error", err)
		return
	}
	if removed > 0 {
		slog.Info("board_reconcile: dropped orphan blocked_by edges on boot", "count", removed)
	}
}
