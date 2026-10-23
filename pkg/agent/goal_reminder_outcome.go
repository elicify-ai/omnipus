package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/goal"
)

// finishGoalReminderOutcome retains real automatic reminder completions and
// failures for the existing keeper. Keep collection off the reply delivery path.
// Publications, stopped/parked turns and unrelated inputs are not outcomes.
func (al *AgentLoop) finishGoalReminderOutcome(opts processOptions, result *turnResult) {
	goalID := goalReminderForTurn(opts, result)
	if goalID == "" || result == nil || result.turnID == "" || result.goalID != goalID {
		return
	}
	failed := result.turnFailed || result.status == TurnEndStatusError
	if result.stopped || result.status == TurnEndStatusAborted || result.status == TurnEndStatusParked ||
		(!failed && result.status != TurnEndStatusCompleted) {
		al.goalMarkIdleSettling(goalID, false)
		return
	}
	sessionID := opts.TranscriptSessionID
	rec, err := activeGoalForSession(sessionID)
	if err != nil {
		work := goalReminderWorkFor(goalID)
		work.mu.Lock()
		if work.lastAssessedTurn != result.turnID && work.pending == nil {
			work.pending = &goalReminderOutcome{turnID: result.turnID, execution: opts.executionDisposition, failed: failed}
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
		work.pending = &goalReminderOutcome{turnID: result.turnID, execution: opts.executionDisposition, failed: failed}
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

// assessGoalReminderLocked consumes only a settled actual reminder outcome.
// Successful and failed turns have separate streaks; busy progress stays latched.
func (al *AgentLoop) assessGoalReminderLocked(sessionID string, rec *goal.Goal, work *goalReminderWork) (*goal.Goal, bool, error) {
	var assessedRunID string
	if work.pending != nil {
		settled, err := goalReminderExecutionSettled(work.pending.execution)
		if errors.Is(err, context.Canceled) {
			work.lastAssessedTurn = work.pending.turnID
			work.lastAssessedRunID = ""
			work.pending = nil
			al.goalMarkIdleSettling(rec.GoalID, false)
			return rec, false, nil
		}
		if err != nil || !settled {
			return rec, !settled, err
		}
		claim := work.pending.execution.claim
		if work.pending.failed && (claim.SessionID != sessionID || claim.RunID == "") {
			return rec, false, errors.New("goal reminder: failed outcome has no matching producing execution identity")
		}
		assessedRunID = claim.RunID
	}
	observedAt := time.Now().UTC()
	progress, busy, err := al.collectGoalReminderProgress(sessionID, rec, work, goalReminderWatermark(rec.GoalID))
	if err != nil {
		return rec, busy, err
	}
	if busy {
		updated, resetErr := resetBusyGoalReminderProgress(sessionID, rec, work, progress, observedAt)
		return updated, true, resetErr
	}
	if work.pending == nil {
		return rec, false, nil
	}
	progress.background = progress.background || work.observedProgress
	var updated *goal.Goal
	if work.pending.failed {
		updated, err = updateFailedGoalReminder(sessionID, rec, work, goalReminderMadeProgress(progress))
	} else {
		updated, err = updateGoalReminderStreak(sessionID, rec.GoalID, goalReminderMadeProgress(progress), true)
		if err == nil {
			work.failedReminders = 0
		}
	}
	if err != nil {
		return rec, false, err
	}
	work.lastAssessedTurn = work.pending.turnID
	work.lastAssessedRunID = assessedRunID
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
	if err != nil || work.pending != nil {
		work.mu.Unlock()
		al.reportGoalReminderAssessment(sessionID, current, err)
		return false
	}
	if busy {
		current, err = resetBusyGoalReminderProgress(sessionID, current, work, progress, observedAt)
		work.mu.Unlock()
		al.reportGoalReminderAssessment(sessionID, current, err)
		return false
	}
	progress.background = progress.background || work.observedProgress
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

// rearmAbnormalGoalReminder retains actual failed reminder turns from ordinary
// or steered exits that bypass the shared natural-completion hook.
func (al *AgentLoop) rearmAbnormalGoalReminder(ts *turnState, result *turnResult, runErr error) {
	if ts == nil || result == nil || (runErr == nil && !result.stopped && !result.turnFailed && result.status == TurnEndStatusCompleted) {
		return
	}
	goalID := ts.opts.goalReminderGoalID
	if goalID == "" {
		goalID = goalReminderForTurn(ts.opts, &turnResult{goalID: ts.goalID})
	}
	if goalID == "" {
		return
	}
	al.goalMarkIdleSettling(goalID, false)
	if result.stopped || ts.stopRequested.Load() || result.status == TurnEndStatusAborted ||
		result.status == TurnEndStatusParked || errors.Is(runErr, context.Canceled) ||
		(runErr == nil && !result.turnFailed && result.status != TurnEndStatusError) {
		return
	}
	failed := *result
	if failed.turnID == "" {
		failed.turnID = ts.turnID
	}
	if failed.goalID == "" {
		failed.goalID = ts.goalID
	}
	failed.turnFailed = true
	opts := ts.opts
	opts.goalReminderGoalID = goalID
	al.finishGoalReminderOutcome(opts, &failed)
}

// A busy observation can reset on NEW productive activity, but can neither
// assess an unproductive completion nor re-use that activity on the next poll.
// Keep the outcome latch until the completed reminder itself is assessed.
func resetBusyGoalReminderProgress(sessionID string, rec *goal.Goal, work *goalReminderWork, progress goalReminderProgress, at time.Time) (*goal.Goal, error) {
	if !goalReminderMadeProgress(progress) {
		return rec, nil
	}
	updated := rec
	if rec.ZeroOutputPushes != 0 {
		var err error
		updated, err = updateGoalReminderStreak(sessionID, rec.GoalID, true, false)
		if err != nil {
			return rec, err
		}
	}
	advanceGoalReminderWindow(rec.GoalID, work, at)
	return updated, nil
}

func consumeGoalReminderProgress(goalID string, work *goalReminderWork, at time.Time) {
	work.observedProgress = false
	advanceGoalReminderWindow(goalID, work, at)
}

func advanceGoalReminderWindow(goalID string, work *goalReminderWork, at time.Time) {
	setGoalReminderWatermark(goalID, at)
	for key, eventAt := range work.events {
		if eventAt.Before(at.Truncate(time.Second)) {
			delete(work.events, key)
		}
	}
}
