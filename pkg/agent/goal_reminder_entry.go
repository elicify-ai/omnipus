package agent

import (
	"time"

	"github.com/elicify-ai/omnipus/pkg/goal"
)

// primeGoalReminderObservation restores the transient window after a cold
// keeper reset and rechecks owned work at admission, not only at publication.
func (al *AgentLoop) primeGoalReminderObservation(sessionID string, rec *goal.Goal) (bool, error) {
	work := goalReminderWorkFor(rec.GoalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	observedAt := time.Now().UTC()
	since := goalReminderWatermark(rec.GoalID)
	_, busy, err := al.collectGoalReminderProgress(sessionID, rec, work, since)
	if err != nil {
		return true, err
	}
	if since.IsZero() {
		consumeGoalReminderProgress(rec.GoalID, work, observedAt)
	}
	return busy || work.pending != nil, nil
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
