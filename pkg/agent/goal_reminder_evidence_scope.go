package agent

import (
	"errors"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// reminderEvidenceOwned credits a machine-check record only when its recorded
// second falls strictly inside one retained run whose session is in the goal's
// canonical scope. Clocks are stored to the second, so a start or end boundary
// second is not proof of which execution wrote the record. A closed run does
// not keep credit after it ends, and a missing or ambiguous window is not
// progress. Tool calls, helper output and run completion stay separate signals.
func reminderEvidenceOwned(scope *goalReminderScope, taskID string, runs []task.TaskRun, at time.Time) (bool, error) {
	if scope == nil {
		return false, errors.New("goal reminder: check execution scope is unavailable")
	}
	second := at.UTC().Truncate(time.Second)
	compatible := 0
	owned := false
	incomplete := false
	for _, run := range runs {
		if run.TaskID != taskID {
			return false, errors.New("goal reminder: check execution belongs to a different task")
		}
		start, err := parseReminderTime(run.StartedAt)
		if err != nil {
			return false, err
		}
		start = start.UTC().Truncate(time.Second)
		var end time.Time
		if run.EndedAt != nil {
			end, err = parseReminderTime(*run.EndedAt)
			if err != nil {
				return false, err
			}
			if end.IsZero() {
				incomplete = true
				continue
			}
			end = end.UTC().Truncate(time.Second)
			if !start.IsZero() && end.Before(start) {
				return false, errors.New("goal reminder: evidence run closes before it starts")
			}
		}
		if start.IsZero() || run.RunID == "" || run.SessionID == "" {
			incomplete = true
			continue
		}
		// Boundary seconds count as possible producers. They can overlap another
		// run, but neither boundary can earn positive credit.
		if second.Before(start) || (!end.IsZero() && second.After(end)) {
			continue
		}
		compatible++
		if scope.sessions[run.SessionID] && second.After(start) && (end.IsZero() || second.Before(end)) {
			owned = true
		}
	}
	return !incomplete && compatible == 1 && owned, nil
}
