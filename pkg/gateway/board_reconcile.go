//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// board_reconcile.go — boot-time recovery of GTD board tasks that were left in
// status "active" by a previous gateway process that crashed, was OOM-killed, or
// received SIGKILL before its onComplete callback could persist the terminal status.
// Wired into setupAndStartServices (gateway.go), after ensureInboxProject and before
// any request handler is reachable, so no /start call can race with reconciliation.

import (
	"log/slog"
	"time"
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
		if t.Status != "active" {
			continue
		}
		t.Status = "failed"
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
