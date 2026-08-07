// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// task_occurrences_overlay_test.go — tests for the ADR-050
// docs/internal/architecture/ADR-050-task-run-history-model.md RD6 /
// task-run-history-spec §3.7 occurrence-run overlay: buildOneOccurrenceSet
// enriched with occurrence_runs (per-instant, matched by occurrence_ms) and
// DayBucket.run_counts (per-day tally). Exercises buildOccurrenceSets
// directly with a stub runsInRange callback — the same pure-function-testing
// pattern task_occurrences_test.go already uses for buildOccurrenceSets
// itself (noEveryAnchor, mustLoc are defined there) — so this never links
// the full gateway test binary (project rule: never run the full
// pkg/gateway suite locally, OOM risk in the devpod).

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestOccurrenceOverlay covers buildOneOccurrenceSet's additive run overlay:
// per-instant occurrence_runs join, per-bucket run_counts tally (incl. the
// scheduled = count - others derivation), the re-run-same-occurrence
// "latest wins" rule, and that the overlay never exceeds the existing
// 500-instant cap.
func TestOccurrenceOverlay(t *testing.T) {
	t.Run("per-instant overlay joins by occurrence_ms, unmatched instants get no entry", func(t *testing.T) {
		from := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 3) // 3-day span: DETAIL mode (<= 8*24h)
		fromMs, toMs := from.UnixMilli(), to.UnixMilli()

		dtstart := from.Add(9 * time.Hour) // 09:00 first occurrence
		tasks := []task.Task{{
			ID: "daily-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=DAILY;COUNT=3"),
					DtstartMs: ptr(dtstart.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}

		day1Ms := dtstart.UnixMilli()
		day2Ms := dtstart.AddDate(0, 0, 1).UnixMilli()
		// day3 (dtstart.AddDate(0,0,2)) is intentionally left with no run.
		runs := []task.TaskRun{
			{
				RunID: "run-1", TaskID: "daily-task", OccurrenceMs: ptr(day1Ms),
				Status: task.StatusDone, Result: "ok", SessionID: "sess-1",
			},
			{
				RunID: "run-2", TaskID: "daily-task", OccurrenceMs: ptr(day2Ms),
				Status: task.StatusFailed, SessionID: "sess-2",
			},
		}
		stub := func(taskID string, gotFromMs, gotToMs int64) ([]task.TaskRun, error) {
			assert.Equal(t, "daily-task", taskID)
			assert.Equal(t, fromMs, gotFromMs)
			assert.Equal(t, toMs, gotToMs)
			return runs, nil
		}

		sets, err := buildOccurrenceSets(tasks, fromMs, toMs, "UTC", noEveryAnchor, stub)
		require.NoError(t, err)
		require.Len(t, sets, 1)
		set := sets[0]
		require.Len(t, set.OccurrencesMs, 3, "COUNT=3 daily rule")
		require.NotNil(t, set.OccurrenceRuns, "expected overlay entries for the two matched occurrences")
		overlay := *set.OccurrenceRuns
		require.Len(t, overlay, 2, "only occurrences WITH a matching run get an entry — day3 must be absent")

		var sawDay1, sawDay2 bool
		for _, e := range overlay {
			switch e.OccurrenceMs {
			case day1Ms:
				sawDay1 = true
				assert.Equal(t, "run-1", e.RunId)
				assert.Equal(t, "sess-1", e.SessionId)
				assert.Equal(t, gen.TaskOccurrenceSetOccurrenceRunsStatusDone, e.Status)
				assert.True(t, e.HasResult, "run-1 has a non-empty result")
			case day2Ms:
				sawDay2 = true
				assert.Equal(t, "run-2", e.RunId)
				assert.Equal(t, "sess-2", e.SessionId)
				assert.Equal(t, gen.TaskOccurrenceSetOccurrenceRunsStatusFailed, e.Status)
				assert.False(t, e.HasResult, "run-2 has no result yet")
			default:
				t.Errorf("unexpected overlay entry for occurrence_ms=%d", e.OccurrenceMs)
			}
		}
		assert.True(t, sawDay1, "day1 entry missing")
		assert.True(t, sawDay2, "day2 entry missing")
	})

	t.Run("re-running an occurrence: the most recently started run wins", func(t *testing.T) {
		from := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 1)
		fromMs, toMs := from.UnixMilli(), to.UnixMilli()
		dtstart := from.Add(9 * time.Hour)
		occMs := dtstart.UnixMilli()

		tasks := []task.Task{{
			ID: "rerun-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=DAILY;COUNT=1"),
					DtstartMs: ptr(dtstart.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}
		runs := []task.TaskRun{
			{
				RunID: "run-old", TaskID: "rerun-task", OccurrenceMs: ptr(occMs),
				Status: task.StatusFailed, StartedAt: "2026-07-20T09:00:00Z",
			},
			{
				RunID: "run-new", TaskID: "rerun-task", OccurrenceMs: ptr(occMs),
				Status: task.StatusDone, Result: "fixed", StartedAt: "2026-07-20T10:00:00Z",
			},
		}
		stub := func(string, int64, int64) ([]task.TaskRun, error) { return runs, nil }

		sets, err := buildOccurrenceSets(tasks, fromMs, toMs, "UTC", noEveryAnchor, stub)
		require.NoError(t, err)
		require.Len(t, sets, 1)
		require.NotNil(t, sets[0].OccurrenceRuns)
		overlay := *sets[0].OccurrenceRuns
		require.Len(t, overlay, 1, "one occurrence, one overlay entry despite two runs")
		assert.Equal(t, "run-new", overlay[0].RunId, "the LATER-started run must win")
		assert.Equal(t, gen.TaskOccurrenceSetOccurrenceRunsStatusDone, overlay[0].Status)
		assert.True(t, overlay[0].HasResult)
	})

	t.Run("bucket run_counts tallies statuses; scheduled = count - others", func(t *testing.T) {
		from := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 10) // 10 days: OVERVIEW mode (> 8*24h)
		fromMs, toMs := from.UnixMilli(), to.UnixMilli()

		tasks := []task.Task{{
			ID: "hourly-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=HOURLY"),
					DtstartMs: ptr(from.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}

		// Day 0's bucket covers [from, from+24h): 24 hourly occurrences.
		// Stamp 5 done + 3 failed + 2 in_progress = 10 runs across day 0;
		// the other 9 buckets get no runs at all.
		day0Start := from.UnixMilli()
		var runs []task.TaskRun
		addRun := func(hourOffset int, status task.Status) {
			ms := day0Start + int64(hourOffset)*int64(time.Hour/time.Millisecond)
			runs = append(runs, task.TaskRun{
				RunID: runID(hourOffset, status), TaskID: "hourly-task",
				OccurrenceMs: ptr(ms), Status: status,
			})
		}
		for i := 0; i < 5; i++ {
			addRun(i, task.StatusDone)
		}
		for i := 5; i < 8; i++ {
			addRun(i, task.StatusFailed)
		}
		for i := 8; i < 10; i++ {
			addRun(i, task.StatusInProgress)
		}
		stub := func(string, int64, int64) ([]task.TaskRun, error) { return runs, nil }

		sets, err := buildOccurrenceSets(tasks, fromMs, toMs, "UTC", noEveryAnchor, stub)
		require.NoError(t, err)
		require.Len(t, sets, 1)
		set := sets[0]
		require.Len(t, set.DayBuckets, 10, "one bucket per day, all dense (24/day hourly)")

		b0 := set.DayBuckets[0]
		require.Equal(t, day0Start, b0.DayStartMs)
		require.Equal(t, int32(24), b0.Count)
		require.NotNil(t, b0.RunCounts, "day 0 has runs, expected a populated run_counts")
		assert.Equal(t, int32(5), b0.RunCounts.Done)
		assert.Equal(t, int32(3), b0.RunCounts.Failed)
		assert.Equal(t, int32(2), b0.RunCounts.InProgress)
		assert.Equal(t, int32(14), b0.RunCounts.Scheduled, "24 - (5+3+2) = 14")

		for i := 1; i < len(set.DayBuckets); i++ {
			assert.Nil(t, set.DayBuckets[i].RunCounts, "day %d has no runs, run_counts must stay unset", i)
		}
	})

	t.Run("run_counts tallies skipped and subtracts it from scheduled too", func(t *testing.T) {
		// Overlap-guard skip (task-run-history-spec.md, TaskRun.status=skipped,
		// pkg/task/run_store.go's RecordSkippedOccurrence): a skipped occurrence
		// already happened-and-was-skipped, so it must be tallied into its own
		// bucket and NOT counted as still-"scheduled" — regression coverage for
		// the populateBucketRunCounts fix accompanying the new status.
		from := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 10) // 10 days: OVERVIEW mode (> 8*24h)
		fromMs, toMs := from.UnixMilli(), to.UnixMilli()

		tasks := []task.Task{{
			ID: "hourly-skip-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=HOURLY"),
					DtstartMs: ptr(from.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}

		day0Start := from.UnixMilli()
		var runs []task.TaskRun
		addRun := func(hourOffset int, status task.Status) {
			ms := day0Start + int64(hourOffset)*int64(time.Hour/time.Millisecond)
			runs = append(runs, task.TaskRun{
				RunID: runID(hourOffset, status), TaskID: "hourly-skip-task",
				OccurrenceMs: ptr(ms), Status: status,
			})
		}
		for i := 0; i < 4; i++ {
			addRun(i, task.StatusDone)
		}
		for i := 4; i < 7; i++ {
			addRun(i, task.StatusSkipped)
		}
		stub := func(string, int64, int64) ([]task.TaskRun, error) { return runs, nil }

		sets, err := buildOccurrenceSets(tasks, fromMs, toMs, "UTC", noEveryAnchor, stub)
		require.NoError(t, err)
		require.Len(t, sets, 1)
		set := sets[0]
		require.Len(t, set.DayBuckets, 10, "one bucket per day, dense (24/day hourly)")

		b0 := set.DayBuckets[0]
		require.Equal(t, int32(24), b0.Count)
		require.NotNil(t, b0.RunCounts, "day 0 has runs, expected a populated run_counts")
		assert.Equal(t, int32(4), b0.RunCounts.Done)
		assert.Equal(t, int32(0), b0.RunCounts.Failed)
		assert.Equal(t, int32(0), b0.RunCounts.InProgress)
		assert.Equal(t, int32(3), b0.RunCounts.Skipped)
		assert.Equal(
			t,
			int32(17),
			b0.RunCounts.Scheduled,
			"24 - (4 done + 3 skipped) = 17, skipped must NOT be double-counted as scheduled",
		)

		for i := 1; i < len(set.DayBuckets); i++ {
			assert.Nil(t, set.DayBuckets[i].RunCounts, "day %d has no runs, run_counts must stay unset", i)
		}
	})

	t.Run("run_counts clamps scheduled at 0 when statuses exceed the bucket count", func(t *testing.T) {
		from := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 10)
		fromMs, toMs := from.UnixMilli(), to.UnixMilli()

		tasks := []task.Task{{
			ID: "hourly-task-2",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=HOURLY"),
					DtstartMs: ptr(from.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}

		// Stamp MORE done runs (30) than the bucket's projected Count (24)
		// for day 0 — simulates a schedule-edit/retention edge where more
		// historical runs land in the window than the CURRENT rule
		// projects for that day. scheduled must clamp at 0, never go
		// negative.
		day0Start := from.UnixMilli()
		var runs []task.TaskRun
		for i := 0; i < 30; i++ {
			ms := day0Start + int64(i)*int64(30*time.Minute/time.Millisecond)
			runs = append(runs, task.TaskRun{
				RunID: "run-" + time.UnixMilli(ms).Format("150405.000"), TaskID: "hourly-task-2",
				OccurrenceMs: ptr(ms), Status: task.StatusDone,
			})
		}
		stub := func(string, int64, int64) ([]task.TaskRun, error) { return runs, nil }

		sets, err := buildOccurrenceSets(tasks, fromMs, toMs, "UTC", noEveryAnchor, stub)
		require.NoError(t, err)
		require.Len(t, sets, 1)
		b0 := sets[0].DayBuckets[0]
		require.NotNil(t, b0.RunCounts)
		assert.Equal(t, int32(30), b0.RunCounts.Done)
		assert.Equal(t, int32(0), b0.RunCounts.Scheduled, "clamped at 0, never negative")
	})

	t.Run("overlay respects the existing 500-instant cap — no new cap added", func(t *testing.T) {
		from := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 2) // 2-day span, DETAIL mode
		fromMs, toMs := from.UnixMilli(), to.UnixMilli()

		tasks := []task.Task{{
			ID: "minutely-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=MINUTELY"),
					DtstartMs: ptr(from.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}

		// 2 days of MINUTELY = 2880 occurrences, far exceeding
		// perTaskInstantCap (500) — occurrences_ms is already truncated to
		// 500 by the existing expansion logic before the overlay ever runs.
		// The overlay's runsInRange stub returns MORE than 500 runs (600,
		// one per minute) so a bug that iterated `runs` directly instead of
		// `set.OccurrencesMs` would be caught.
		var runs []task.TaskRun
		for i := 0; i < 600; i++ {
			ms := from.UnixMilli() + int64(i)*int64(time.Minute/time.Millisecond)
			runs = append(runs, task.TaskRun{
				RunID: "run-" + time.UnixMilli(ms).Format("150405"), TaskID: "minutely-task",
				OccurrenceMs: ptr(ms), Status: task.StatusDone,
			})
		}
		stub := func(string, int64, int64) ([]task.TaskRun, error) { return runs, nil }

		sets, err := buildOccurrenceSets(tasks, fromMs, toMs, "UTC", noEveryAnchor, stub)
		require.NoError(t, err)
		require.Len(t, sets, 1)
		set := sets[0]
		require.True(t, set.Truncated, "2880 minutely occurrences must hit the 500 cap")
		require.Len(t, set.OccurrencesMs, 500)
		require.NotNil(t, set.OccurrenceRuns)
		overlay := *set.OccurrenceRuns
		assert.LessOrEqual(t, len(overlay), 500, "overlay must never exceed the existing per-task instant cap")
		assert.LessOrEqual(t, len(overlay), len(set.OccurrencesMs), "overlay <= returned instant count")
	})

	t.Run("nil runsInRange leaves both fields absent — existing callers unaffected", func(t *testing.T) {
		from := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 3)
		dtstart := from.Add(9 * time.Hour)
		tasks := []task.Task{{
			ID: "no-overlay-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=DAILY;COUNT=3"),
					DtstartMs: ptr(dtstart.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}
		// No trailing runsInRange argument at all — mirrors every existing
		// task_occurrences_test.go call site.
		sets, err := buildOccurrenceSets(tasks, from.UnixMilli(), to.UnixMilli(), "UTC", noEveryAnchor)
		require.NoError(t, err)
		require.Len(t, sets, 1)
		assert.Nil(t, sets[0].OccurrenceRuns)
		for _, b := range sets[0].DayBuckets {
			assert.Nil(t, b.RunCounts)
		}
	})

	t.Run("runsInRange error leaves both fields absent rather than failing the whole request", func(t *testing.T) {
		from := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 3)
		dtstart := from.Add(9 * time.Hour)
		tasks := []task.Task{{
			ID: "overlay-error-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=DAILY;COUNT=3"),
					DtstartMs: ptr(dtstart.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}
		stub := func(string, int64, int64) ([]task.TaskRun, error) {
			return nil, errOverlayStub
		}
		sets, err := buildOccurrenceSets(tasks, from.UnixMilli(), to.UnixMilli(), "UTC", noEveryAnchor, stub)
		require.NoError(t, err, "an overlay lookup failure must not fail the whole occurrences request")
		require.Len(t, sets, 1)
		assert.Nil(t, sets[0].OccurrenceRuns)
	})
}

// errOverlayStub is a fixed sentinel used by the "runsInRange error" subtest.
var errOverlayStub = errors.New("overlay stub: forced lookup failure")

// runID builds a small deterministic, collision-free run id for the
// run_counts tally test's addRun helper.
func runID(hourOffset int, status task.Status) string {
	return string(status) + "-" + time.Duration(hourOffset).String()
}
