// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// task_occurrences_missed_overlay_test.go — the GET /api/v1/tasks/occurrences
// half of the boot-time missed-fire recording (founder decision 2026-10-29,
// Q1 A, train 6b option B): a TaskRun with status "missed" recorded for a
// missed instant must surface in TaskOccurrenceSet.occurrence_runs with that
// status — which also proves the regenerated wire enum
// (gen.TaskOccurrenceSetOccurrenceRunsStatus) accepts it, i.e. the H4
// encode-time validation does not drop the entry. Same pure-function pattern
// as task_occurrences_overlay_test.go (stub runsInRange, no gateway binary).

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestOccurrenceOverlay_MissedRunSurfaces(t *testing.T) {
	from := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 3) // DETAIL mode (<= 8*24h)
	fromMs, toMs := from.UnixMilli(), to.UnixMilli()

	dtstart := from.Add(9 * time.Hour)
	tasks := []task.Task{{
		ID: "missed-task",
		Trigger: &task.Trigger{
			Type: task.TriggerRecurring,
			Config: task.TriggerConfig{
				Rrule:     ptr("FREQ=DAILY;COUNT=3"),
				DtstartMs: ptr(dtstart.UnixMilli()),
				Tz:        ptr("UTC"),
			},
		},
	}}

	missedInstant := dtstart.AddDate(0, 0, 1).UnixMilli() // day-2 occurrence missed while down
	runs := []task.TaskRun{
		{
			RunID: "run-day1", TaskID: "missed-task", OccurrenceMs: ptr(dtstart.UnixMilli()),
			Status: task.StatusDone, Result: "ok", SessionID: "sess-1",
		},
		{
			// The boot-time missed record: no session, no result, the missed
			// instant as the calendar join key.
			RunID: "run-missed", TaskID: "missed-task", OccurrenceMs: ptr(missedInstant),
			Status: task.StatusMissed, SessionID: "",
		},
	}
	stub := func(taskID string, gotFromMs, gotToMs int64) ([]task.TaskRun, error) {
		assert.Equal(t, "missed-task", taskID)
		return runs, nil
	}

	sets, err := buildOccurrenceSets(tasks, fromMs, toMs, "UTC", noEveryAnchor, stub)
	require.NoError(t, err)
	require.Len(t, sets, 1)
	set := sets[0]
	require.NotNil(t, set.OccurrenceRuns, "the missed occurrence must surface in occurrence_runs")

	var missedEntry *wireOccurrenceRun
	for i := range *set.OccurrenceRuns {
		if (*set.OccurrenceRuns)[i].OccurrenceMs == missedInstant {
			missedEntry = &(*set.OccurrenceRuns)[i]
		}
	}
	require.NotNil(t, missedEntry, "no occurrence_runs entry for the missed instant %d", missedInstant)
	assert.Equal(t, gen.TaskOccurrenceSetOccurrenceRunsStatusMissed, missedEntry.Status,
		"the missed run must carry status missed on the wire")
	assert.Equal(t, "run-missed", missedEntry.RunId)
	assert.Equal(t, "", missedEntry.SessionId, "a missed fire never started a session")
	assert.False(t, missedEntry.HasResult, "a missed fire has no result")
}
