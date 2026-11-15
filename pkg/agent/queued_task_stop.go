// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// closeStoppedQueuedTaskLaunch gives a task launch that a person stopped while
// it was still queued the same outcome task cancel gives it (#1557): the task
// becomes failed with CancelReason stopped_by_user, written by the one
// person-stop writer (writeTaskStoppedByPerson).
//
// The Stop removed the queued selection and landed the session stopped, but
// no task run ever started, so no executor tail exists to end the task (a
// started run ends it in finishRunTurn). Left in_progress, the next boot's
// ReconcileInterruptedTasks would relabel the person's Stop as a gateway
// interruption.
//
// It acts only when every one of these holds, and otherwise writes nothing:
//   - the session is a task launch (task origin) and has landed stopped;
//   - the landed Stop is a person's own Stop of this session (cause "stop",
//     a human actor) — a cascade from a plan or task Stop is written by that
//     Stop itself, and a restart is boot recovery's;
//   - the executor holds no run of the task;
//   - the task is still in_progress on this very session.
//
// A store failure is returned so the Stop's caller reports it.
func (al *AgentLoop) closeStoppedQueuedTaskLaunch(sessionID string) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil || al.taskStore == nil {
		return nil
	}
	rec, err := lifecycle.Load(sessionID)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Stop ended session %s, but its saved state could not be read to close its task: %w", sessionID, err)
	}
	if rec.Origin == nil || rec.Origin.Kind != session.OriginKindTask || rec.Origin.TaskID == "" {
		return nil
	}
	if rec.State != session.LifecycleStopped || rec.StopNote == nil || rec.StopNote.Cause != session.StopCauseStop {
		return nil
	}
	personID, ok := personStopActorID(rec.StopNote.By)
	if !ok {
		return nil
	}
	taskID := rec.Origin.TaskID
	if al.taskExecutor != nil && al.taskExecutor.holdsRun(taskID) {
		return nil
	}
	current, err := al.taskStore.Get(taskID)
	if errors.Is(err, task.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Stop ended session %s, but task %s could not be read to save it as stopped: %w", sessionID, taskID, err)
	}
	if current.Status != task.StatusInProgress || current.SessionID != sessionID {
		return nil
	}
	inProgress := task.StatusInProgress
	if _, err := writeTaskStoppedByPerson(al, al.taskStore, taskID, personID, &inProgress); err != nil {
		if errors.Is(err, task.ErrStatusConflict) {
			return nil // another writer ended the task first; its outcome stands
		}
		return fmt.Errorf("Stop ended session %s, but task %s could not be saved as stopped: %w. Repair the gateway storage, then stop the task again", sessionID, taskID, err)
	}
	return nil
}

// personStopActorID returns the person's id from a stop note actor written by
// session.StopActorFromPrincipal for a human principal ("human:<id>").
func personStopActorID(by string) (string, bool) {
	id, ok := strings.CutPrefix(by, string(session.PrincipalKindHuman)+":")
	if !ok || strings.TrimSpace(id) == "" {
		return "", false
	}
	return id, true
}
