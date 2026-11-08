package agent

import (
	"errors"
	"time"

	"github.com/elicify-ai/omnipus/pkg/goal"
)

// primeGoalReminderObservation restores the transient window after a cold
// keeper reset and rechecks owned work at admission, not only at publication.
func (al *AgentLoop) primeGoalReminderObservation(sessionID string, rec *goal.Goal, messageID string) (bool, error) {
	work := goalReminderWorkFor(rec.GoalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	since := goalReminderWatermark(rec.GoalID)
	_, busy, err := al.collectGoalReminderProgress(sessionID, rec, work, since, time.Now().UTC())
	if err != nil {
		return true, err
	}
	if since.IsZero() {
		setGoalReminderWatermark(rec.GoalID, goalReminderInitialBoundary(rec))
	}
	if work.pending != nil {
		return true, nil
	}
	res := work.checkIn
	if !busy || res == nil || res.sessionID != sessionID || res.messageID == "" || res.messageID != messageID ||
		res.execution.RunID != "" || !res.stallAt.Equal(work.stallSince) {
		return busy, nil
	}
	// This private reservation permits only background busy, never a new
	// live helper, a judge, an unreadable owner or a changed predecessor.
	if al.goalAdjudicationInFlight(sessionID) {
		return true, nil
	}
	if live, _ := al.goalLiveTurnWorkFingerprint(sessionID); live {
		return true, nil
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return true, errors.New("goal check-in: lifecycle store is unavailable")
	}
	current, err := lifecycle.Load(sessionID)
	if err != nil {
		return true, err
	}
	return !res.parent.matches(current), nil
}

// noteGoalReminderExecution preserves the input's kind across the existing
// admission queue's turn reconstruction. It is RAM-only and keyed by the
// already-minted immutable execution ID, never by a guessed future turn ID.
func noteGoalReminderExecution(goalID, runID string) {
	if goalID == "" || runID == "" {
		return
	}
	work := goalReminderWorkFor(goalID)
	work.mu.Lock()
	if work.reminderRuns == nil {
		work.reminderRuns = make(map[string]bool)
	}
	work.reminderRuns[runID] = true
	work.mu.Unlock()
}

func goalReminderForTurn(opts processOptions, result *turnResult) string {
	if opts.goalReminderGoalID != "" {
		return opts.goalReminderGoalID
	}
	if result == nil || result.goalID == "" || opts.executionDisposition == nil {
		return ""
	}
	s := goalTriggers()
	s.mu.Lock()
	work := s.reminderWork[result.goalID]
	s.mu.Unlock()
	if work == nil {
		return ""
	}
	work.mu.Lock()
	defer work.mu.Unlock()
	runID := opts.executionDisposition.claim.RunID
	if !work.reminderRuns[runID] {
		return ""
	}
	delete(work.reminderRuns, runID)
	return result.goalID
}
