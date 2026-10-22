package gateway

import (
	"errors"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// preserveEndedTaskInterruption keeps the task/run/goal separate from an already
// ended native execution. Boot must not relabel its ceiling/hold as a fresh crash.
func (a *restAPI) preserveEndedTaskInterruption(t task.Task) bool {
	if a.agentLoop == nil || t.SessionID == "" {
		return false
	}
	lc := a.agentLoop.GetSessionLifecycleStore()
	if lc == nil {
		return false
	}
	rec, err := lc.Load(t.SessionID)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return false
	}
	if err != nil {
		// An unreadable identity is not proof of a crash or permission to end
		// its goal. Preserve it and put the unconfirmed recovery on the task.
		text := "Recovery could not read this task's saved session. Its outcome is unconfirmed; repair storage before continuing."
		_, updateErr := a.taskStore.Update(t.ID, task.Patch{Result: &text})
		slog.Error("rest: task recovery state unreadable; task and goal left unchanged", "task_id", t.ID, "error", err, "notice_error", updateErr)
		return true
	}
	return rec.State == session.LifecycleInterrupted &&
		(rec.InterruptionReason == session.InterruptionToolLimit || rec.InterruptionReason == session.InterruptionReconciliationRequired)
}
