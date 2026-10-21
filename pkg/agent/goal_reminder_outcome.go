package agent

import (
	"errors"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/goal"
)

// finishGoalReminderOutcome records a natural completion for the existing
// keeper to assess. Keep registry/diff collection off the reply delivery path.
// A publication, failed/stopped/parked turn or unrelated input is not an outcome.
func (al *AgentLoop) finishGoalReminderOutcome(opts processOptions, result *turnResult) {
	goalID := goalReminderForTurn(opts, result)
	if goalID == "" || result == nil || result.turnID == "" || result.goalID != goalID {
		return
	}
	if result.stopped || result.turnFailed || result.status != TurnEndStatusCompleted {
		al.goalMarkIdleSettling(goalID, false)
		return
	}
	sessionID := opts.TranscriptSessionID
	rec, err := activeGoalForSession(sessionID)
	if err != nil {
		work := goalReminderWorkFor(goalID)
		work.mu.Lock()
		if work.lastAssessedTurn != result.turnID && work.pending == nil {
			work.pending = &goalReminderOutcome{turnID: result.turnID, execution: opts.executionDisposition}
		}
		work.mu.Unlock()
		al.reportGoalReadError(sessionID, "read completed goal reminder (assessment retained for retry)", err)
		return
	}
	if rec == nil || rec.GoalID != goalID || al.goalReminderHeld(sessionID, goalID) {
		return
	}
	work := goalReminderWorkFor(goalID)
	work.mu.Lock()
	if work.lastAssessedTurn == result.turnID {
		work.mu.Unlock()
		return
	}
	if work.pending == nil {
		work.pending = &goalReminderOutcome{turnID: result.turnID, execution: opts.executionDisposition}
	}
	work.mu.Unlock()
	al.goalMarkIdleSettling(goalID, false)
}

func (al *AgentLoop) goalReminderHeld(sessionID, goalID string) bool {
	return al.goalKeeperPausedByStop(sessionID) || al.goalHasParkedCard(sessionID) ||
		al.goalIsBlocked(goalID) || al.goalIsWaitingOnUser(goalID)
}

// retryGoalReminderOutcome runs before the idle-in-flight marker: a failed
// observation/write must be retried, not bypassed by dispatching another turn.
func (al *AgentLoop) retryGoalReminderOutcome(sessionID string, rec *goal.Goal) bool {
	if goalReminderBrakePaused(rec) {
		al.reportGoalReminderAssessment(sessionID, rec, nil)
		return false
	}
	if al.goalReminderHeld(sessionID, rec.GoalID) {
		return false
	}
	work := goalReminderWorkFor(rec.GoalID)
	work.mu.Lock()
	if work.pending == nil {
		work.mu.Unlock()
		return true
	}
	updated, busy, err := al.assessGoalReminderLocked(sessionID, rec, work)
	work.mu.Unlock()
	al.reportGoalReminderAssessment(sessionID, updated, err)
	return err == nil && !busy && !goalReminderBrakePaused(updated)
}

// assessGoalReminderLocked consumes only an actual completed reminder. The
// latch keeps work seen during a busy interval until the final assessment.
func (al *AgentLoop) assessGoalReminderLocked(sessionID string, rec *goal.Goal, work *goalReminderWork) (*goal.Goal, bool, error) {
	if work.pending != nil {
		settled, err := goalReminderExecutionSettled(work.pending.execution)
		if err != nil || !settled {
			return rec, !settled, err
		}
	}
	observedAt := time.Now().UTC()
	progress, busy, err := al.collectGoalReminderProgress(sessionID, rec, work, goalReminderWatermark(rec.GoalID))
	if err != nil || busy {
		return rec, busy, err
	}
	if work.pending == nil {
		return rec, false, nil
	}
	updated, err := updateGoalReminderStreak(sessionID, rec.GoalID, goalReminderMadeProgress(progress), true)
	if err != nil {
		return rec, false, err
	}
	work.lastAssessedTurn = work.pending.turnID
	work.pending = nil
	consumeGoalReminderProgress(rec.GoalID, work, observedAt)
	al.goalMarkIdleSettling(rec.GoalID, false)
	return updated, false, nil
}

// A compute handle disappearing is not completion of its output/continuation
// tail. Reuse the producing admission's existing full-settlement barrier.
func goalReminderExecutionSettled(execution *executionDisposition) (bool, error) {
	if execution == nil {
		return false, errors.New("goal reminder: completed turn has no admitted execution owner")
	}
	select {
	case <-execution.done:
		execution.mu.Lock()
		err := execution.result
		execution.mu.Unlock()
		if err != nil {
			return true, fmt.Errorf("goal reminder: producing execution did not settle successfully: %w", err)
		}
		return true, nil
	default:
		return false, nil
	}
}

func (al *AgentLoop) reportGoalReminderAssessment(sessionID string, rec *goal.Goal, err error) {
	if err != nil {
		if !errors.Is(err, errGoalReminderObsolete) {
			al.reportGoalReadError(sessionID, "assess completed goal reminder (no new reminder dispatched)", err)
		}
		return
	}
	if goalReminderBrakePaused(rec) {
		if err := al.pauseGoalReminders(sessionID, rec); err != nil {
			al.reportGoalReadError(sessionID, "publish goal reminder pause (will retry)", err)
		}
	}
}

// prepareGoalReminderDispatch observes work before reserving the existing idle
// marker. It never increments the streak. A write/read failure retains the
// current assessment window and prevents an unaccountable extra reminder.
func (al *AgentLoop) prepareGoalReminderDispatch(sessionID string, rec *goal.Goal) bool {
	work := goalReminderWorkFor(rec.GoalID)
	work.mu.Lock()
	current, err := activeGoalForSession(sessionID)
	if err == nil && (current == nil || current.GoalID != rec.GoalID) {
		err = errGoalReminderObsolete
	}
	if err != nil || goalReminderBrakePaused(current) || al.goalReminderHeld(sessionID, rec.GoalID) {
		work.mu.Unlock()
		al.reportGoalReminderAssessment(sessionID, current, err)
		return false
	}
	observedAt := time.Now().UTC()
	progress, busy, err := al.collectGoalReminderProgress(sessionID, current, work, goalReminderWatermark(rec.GoalID))
	if err != nil || busy || work.pending != nil {
		work.mu.Unlock()
		al.reportGoalReminderAssessment(sessionID, current, err)
		return false
	}
	current, err = updateGoalReminderStreak(sessionID, rec.GoalID, goalReminderMadeProgress(progress), false)
	if err != nil {
		work.mu.Unlock()
		al.reportGoalReminderAssessment(sessionID, current, fmt.Errorf("prepare reminder accounting: %w", err))
		return false
	}
	// Reserve exactly once; publication happens outside work.mu because the
	// real notifier may immediately run and complete the admitted turn.
	s := goalTriggers()
	s.mu.Lock()
	reserved := !s.idleSettling[rec.GoalID]
	if reserved {
		s.idleSettling[rec.GoalID] = true
	}
	s.mu.Unlock()
	if reserved {
		consumeGoalReminderProgress(rec.GoalID, work, observedAt)
		al.markGoalIdleFired(rec.GoalID, observedAt)
	}
	work.mu.Unlock()
	return reserved
}

// rearmAbnormalGoalReminder releases only reminder bookkeeping when the
// steered completion path exits before the shared natural-completion hook.
func (al *AgentLoop) rearmAbnormalGoalReminder(ts *turnState, result *turnResult, runErr error) {
	if ts == nil || result == nil || (runErr == nil && !result.stopped && !result.turnFailed && result.status == TurnEndStatusCompleted) {
		return
	}
	goalID := ts.opts.goalReminderGoalID
	if goalID == "" {
		goalID = goalReminderForTurn(ts.opts, &turnResult{goalID: ts.goalID})
	}
	if goalID != "" {
		al.goalMarkIdleSettling(goalID, false)
	}
}

func consumeGoalReminderProgress(goalID string, work *goalReminderWork, at time.Time) {
	work.observedProgress = false
	setGoalReminderWatermark(goalID, at)
	for key, eventAt := range work.events {
		if eventAt.Before(at.Truncate(time.Second)) {
			delete(work.events, key)
		}
	}
}
