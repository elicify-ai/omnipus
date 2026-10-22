package agent

import (
	"context"
	"errors"
	"fmt"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
)

const goalReminderFailedTurnMax = 3

const goalReminderFailedTurnNotice = "3 reminder turns failed in a row — goal paused. Reply to continue."

// A failed reminder is assessed only after its real producing owner settles.
// Its original error remains on the turn/receiver and execution disposition;
// that expected failure is not itself an observation/storage error.
func goalReminderPendingSettled(outcome *goalReminderOutcome) (bool, error) {
	if !outcome.failed {
		return goalReminderExecutionSettled(outcome.execution)
	}
	if outcome.execution == nil {
		return false, errors.New("goal reminder: failed turn has no admitted execution owner")
	}
	select {
	case <-outcome.execution.done:
		outcome.execution.mu.Lock()
		result := outcome.execution.result
		outcome.execution.mu.Unlock()
		if errors.Is(result, context.Canceled) {
			return true, context.Canceled
		}
		return true, nil
	default:
		return false, nil
	}
}

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

// Failed turns can have an error row but no assistant TurnID. If a restart
// lands before the pause notice is saved, the actual last execution supplies a
// stable identity without inventing an assistant turn or another durable field.
func (al *AgentLoop) goalReminderFailurePauseID(sessionID, prefix string) (string, error) {
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
	return prefix + "-run-" + rec.ExecutionID.RunID, nil
}
