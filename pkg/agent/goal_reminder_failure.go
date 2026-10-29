package agent

import (
	"errors"
	"fmt"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const goalReminderFailedTurnMax = 3

const goalReminderFailedTurnNotice = "3 reminder turns failed in a row — goal paused. Reply to continue."

// Failure accounting is process-local. Only the existing pause reason is saved;
// failed turns never increment the separate completed-no-progress streak.
func updateFailedGoalReminder(sessionID string, rec *goal.Goal, work *goalReminderWork, progress bool) (*goal.Goal, error) {
	next := work.failedReminders + 1
	updated, err := resolveGoalRecordStore().Update(rec.GoalID, func(cur *goal.Goal) error {
		if cur.State != generated.GoalStateActive || cur.ActiveSessionID != sessionID {
			return errGoalReminderObsolete
		}
		if goalReminderBrakePaused(cur) {
			return nil
		}
		if progress {
			cur.ZeroOutputPushes = 0
		}
		if next >= goalReminderFailedTurnMax {
			cur.LatestReason = goalReminderFailedTurnNotice
		}
		return nil
	})
	if err == nil {
		work.failedReminders = next
	}
	return updated, err
}

func goalReminderPauseContent(rec *goal.Goal) string {
	if rec != nil && rec.LatestReason == goalReminderFailedTurnNotice {
		return goalReminderFailedTurnNotice
	}
	return goalReminderPauseNotice
}

// Failed turns can have no assistant TurnID. Warm publication uses the actual
// assessed producer. Cold recovery uses one terminal, non-restart snapshot only;
// that necessary-state filter is not proof of arbitrary crash provenance.
func (al *AgentLoop) goalReminderFailurePauseID(sessionID, prefix, assessedRunID string) (string, error) {
	if assessedRunID == "" {
		store := al.GetSessionLifecycleStore()
		if store == nil {
			return "", errors.New("goal reminder pause: lifecycle store is unavailable")
		}
		rec, err := store.Load(sessionID)
		if err != nil {
			return "", fmt.Errorf("goal reminder pause: read failed execution identity: %w", err)
		}
		if rec.ExecutionID == nil || rec.ExecutionID.RunID == "" {
			return "", errors.New("goal reminder pause: failed execution identity is unavailable")
		}
		if !rec.Terminal() || session.LifecycleRecordIsRestartInterrupted(rec) {
			return "", errors.New("goal reminder pause: cold failure execution is not a settled terminal producer")
		}
		assessedRunID = rec.ExecutionID.RunID
	}
	return prefix + "-run-" + assessedRunID, nil
}
