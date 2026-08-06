// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// run_store_test.go exercises the append-only, day-partitioned TaskRun store
// (ADR-050 / docs/internal/specs/task-run-history-spec.md): OpenRun
// idempotency, CloseRun fold semantics (including H9's truncate-not-reject
// oversized-result behavior), ListRuns/RunsInRange fold-last-wins across
// multiple day files, day-partition assignment, PruneRuns age +
// floor-of-one retention plus its global-fold-aware open-run file
// protection (delta-review Fix 1, 2026-07-20), and OpenRun's concurrency
// guarantee. There is no stuck-run reaper (operator decision 2026-07-20,
// superseding ADR-050 RD10's original design) — a run stays in_progress
// until its own execution closes it, so no reaper tests exist here.
//
// Reuses newStore/ptr/mustCreate helpers already defined in store_test.go /
// coverage_test.go (same package).

package task

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRunRecordAt is a test-only bypass of OpenRun/CloseRun's "now"-based
// day-file targeting. It lets a test fabricate a run record landing in an
// arbitrary UTC calendar day, which is required to exercise multi-day-file
// fold and retention behavior deterministically (the public API always
// stamps StartedAt/day-file as time.Now()).
func writeRunRecordAt(t *testing.T, s *Store, taskID string, rec TaskRun, at time.Time) {
	t.Helper()
	require.NoError(t, s.appendRunRecord(taskID, rec, at))
}

// chtimes sets a file's mtime (and atime) to at. Used to simulate an aged day
// file for PruneRuns' cutoff logic without waiting real time.
func chtimes(t *testing.T, path string, at time.Time) {
	t.Helper()
	require.NoError(t, os.Chtimes(path, at, at))
}

// ---- OpenRun idempotency ----------------------------------------------------

func TestOpenRun_IdempotentSameKey(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"

	run1, created1, err := s.OpenRun(taskID, nil, RunKindManual, "session-a")
	require.NoError(t, err)
	assert.True(t, created1)
	require.NotNil(t, run1)
	assert.NotEmpty(t, run1.RunID)
	assert.Equal(t, StatusInProgress, run1.Status)
	assert.Nil(t, run1.OccurrenceMs)
	assert.Nil(t, run1.EndedAt)

	run2, created2, err := s.OpenRun(taskID, nil, RunKindManual, "session-b")
	require.NoError(t, err)
	assert.False(t, created2, "second OpenRun for the same key must not create a new run")
	require.NotNil(t, run2)
	assert.Equal(t, run1.RunID, run2.RunID, "idempotent OpenRun must return the SAME run")
	// The existing open run's original session_id is what's returned, not the
	// second caller's — OpenRun does not mutate an existing open run.
	assert.Equal(t, "session-a", run2.SessionID)
}

func TestOpenRun_DifferentOccurrenceMsAreDistinctRuns(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"

	occA := ptr(int64(1000))
	occB := ptr(int64(2000))

	runA, createdA, err := s.OpenRun(taskID, occA, RunKindScheduled, "session-a")
	require.NoError(t, err)
	assert.True(t, createdA)

	runB, createdB, err := s.OpenRun(taskID, occB, RunKindScheduled, "session-b")
	require.NoError(t, err)
	assert.True(t, createdB, "a distinct occurrence_ms must open a distinct run")
	assert.NotEqual(t, runA.RunID, runB.RunID)

	// Re-opening occA is still idempotent against the first run.
	runAAgain, createdAAgain, err := s.OpenRun(taskID, occA, RunKindScheduled, "session-a2")
	require.NoError(t, err)
	assert.False(t, createdAAgain)
	assert.Equal(t, runA.RunID, runAAgain.RunID)
}

func TestOpenRun_NilVsNonNilOccurrenceAreDistinct(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"

	adhoc, created1, err := s.OpenRun(taskID, nil, RunKindManual, "session-adhoc")
	require.NoError(t, err)
	assert.True(t, created1)

	occ := ptr(int64(500))
	scheduled, created2, err := s.OpenRun(taskID, occ, RunKindScheduled, "session-sched")
	require.NoError(t, err)
	assert.True(t, created2, "occurrence_ms=nil and occurrence_ms=500 must be distinct keys")
	assert.NotEqual(t, adhoc.RunID, scheduled.RunID)
}

func TestOpenRun_ReRunAfterFailureOpensNewRun(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"

	run1, created1, err := s.OpenRun(taskID, nil, RunKindManual, "session-1")
	require.NoError(t, err)
	assert.True(t, created1)
	require.NoError(t, s.CloseRun(taskID, run1.RunID, StatusFailed, "boom"))

	// The prior run is now closed (failed) — OpenRun(nil) again must open a
	// GENUINELY NEW run, not idempotently return the closed one (RD7).
	run2, created2, err := s.OpenRun(taskID, nil, RunKindManual, "session-2")
	require.NoError(t, err)
	assert.True(t, created2, "re-run after failure must open a new run, not reuse the closed one")
	assert.NotEqual(t, run1.RunID, run2.RunID)

	runs, err := s.ListRuns(taskID)
	require.NoError(t, err)
	require.Len(t, runs, 2, "the prior failed run must be preserved alongside the new one")
}

func TestOpenRun_InvalidInputs(t *testing.T) {
	s := newStore(t)

	_, _, err := s.OpenRun("", nil, RunKindManual, "s")
	assert.Error(t, err, "empty task id must be rejected")

	_, _, err = s.OpenRun("../escape", nil, RunKindManual, "s")
	assert.Error(t, err, "path-traversal task id must be rejected")

	_, _, err = s.OpenRun("task-1", nil, RunKind("bogus"), "s")
	assert.Error(t, err, "invalid run kind must be rejected")
}

// ---- CloseRun fold -----------------------------------------------------------

func TestCloseRun_FoldsToTerminalState(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"

	run, _, err := s.OpenRun(taskID, ptr(int64(42)), RunKindScheduled, "session-x")
	require.NoError(t, err)

	require.NoError(t, s.CloseRun(taskID, run.RunID, StatusDone, "all good"))

	runs, err := s.ListRuns(taskID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	closed := runs[0]
	assert.Equal(t, run.RunID, closed.RunID)
	assert.Equal(t, StatusDone, closed.Status)
	assert.Equal(t, "all good", closed.Result)
	require.NotNil(t, closed.EndedAt)
	assert.False(t, closed.IsOpen())
	// Identity fields carried forward from the open record.
	assert.Equal(t, taskID, closed.TaskID)
	require.NotNil(t, closed.OccurrenceMs)
	assert.Equal(t, int64(42), *closed.OccurrenceMs)
	assert.Equal(t, "session-x", closed.SessionID)
	assert.Equal(t, RunKindScheduled, closed.Kind)
	assert.Equal(t, run.StartedAt, closed.StartedAt)
}

func TestCloseRun_NotFound(t *testing.T) {
	s := newStore(t)
	err := s.CloseRun("task-1", "nonexistent-run-id", StatusDone, "x")
	assert.ErrorIs(t, err, ErrRunNotFound)
}

func TestCloseRun_AlreadyClosed(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"
	run, _, err := s.OpenRun(taskID, nil, RunKindManual, "s")
	require.NoError(t, err)
	require.NoError(t, s.CloseRun(taskID, run.RunID, StatusDone, "first close"))

	err = s.CloseRun(taskID, run.RunID, StatusFailed, "second close")
	assert.ErrorIs(t, err, ErrRunAlreadyClosed)
}

func TestCloseRun_RejectsNonTerminalStatus(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"
	run, _, err := s.OpenRun(taskID, nil, RunKindManual, "s")
	require.NoError(t, err)

	err = s.CloseRun(taskID, run.RunID, StatusInProgress, "x")
	assert.Error(t, err, "CloseRun must reject a non-terminal status")
}

// TestCloseRun_TruncatesOversizedResult verifies H9: a result over the
// 50000-character contract cap is TRUNCATED (with a clear marker appended),
// never rejected. With no stuck-run reaper, a rejected close would strand
// the run in_progress forever with no path to a terminal state — the close
// must always succeed.
func TestCloseRun_TruncatesOversizedResult(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"
	run, _, err := s.OpenRun(taskID, nil, RunKindManual, "s")
	require.NoError(t, err)

	oversized := strings.Repeat("x", maxRunResultChars+12345)
	require.NoError(t, s.CloseRun(taskID, run.RunID, StatusDone, oversized),
		"CloseRun must ALWAYS succeed on a terminal status, even with an oversized result")

	runs, err := s.ListRuns(taskID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	closed := runs[0]
	assert.Equal(t, StatusDone, closed.Status)
	assert.LessOrEqual(t, len(closed.Result), maxRunResultChars,
		"stored result must be truncated to the contract cap")
	assert.Contains(t, closed.Result, "truncated", "a truncated result must carry a clear marker")
	assert.True(t, strings.HasPrefix(oversized, closed.Result[:100]),
		"the retained prefix must be the ORIGINAL content, not something else")
}

// TestCloseRun_ResultAtExactCapIsNotTruncated verifies the truncation only
// engages strictly ABOVE maxRunResultChars — a result exactly at the cap is
// stored verbatim, with no marker appended.
func TestCloseRun_ResultAtExactCapIsNotTruncated(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"
	run, _, err := s.OpenRun(taskID, nil, RunKindManual, "s")
	require.NoError(t, err)

	exact := strings.Repeat("y", maxRunResultChars)
	require.NoError(t, s.CloseRun(taskID, run.RunID, StatusDone, exact))

	runs, err := s.ListRuns(taskID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, exact, runs[0].Result, "a result exactly at the cap must be stored unmodified")
}

// ---- Day-partition assignment -----------------------------------------------

func TestOpenRun_DayPartitionAssignment(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"

	run, _, err := s.OpenRun(taskID, nil, RunKindManual, "s")
	require.NoError(t, err)

	wantDay := time.Now().UTC().Format("2006-01-02")
	wantPath := filepath.Join(s.Dir(), taskID, "runs", wantDay+".jsonl")
	_, statErr := os.Stat(wantPath)
	require.NoError(t, statErr, "open record must land in today's UTC day file")

	// The single line in the file must be the open record.
	data, err := os.ReadFile(wantPath)
	require.NoError(t, err)
	var rec TaskRun
	require.NoError(t, json.Unmarshal([]byte(firstLine(data)), &rec))
	assert.Equal(t, run.RunID, rec.RunID)
	assert.True(t, rec.IsOpen())
}

func firstLine(data []byte) string {
	for i, b := range data {
		if b == '\n' {
			return string(data[:i])
		}
	}
	return string(data)
}

// ---- ListRuns / RunsInRange fold-last-wins across MULTIPLE day files -------

func TestListRuns_FoldsAcrossMultipleDayFiles(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"

	day1 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)

	// Run A: opened on day1, closed on day2 (spans a day boundary — the open
	// record and its later close land in different day files, per spec §2.2).
	runA := TaskRun{
		RunID:     "run-a",
		TaskID:    taskID,
		Status:    StatusInProgress,
		SessionID: "session-a",
		Kind:      RunKindManual,
		StartedAt: day1.Format(time.RFC3339),
	}
	writeRunRecordAt(t, s, taskID, runA, day1)

	closedAt := day2.Format(time.RFC3339)
	runAClosed := runA
	runAClosed.Status = StatusDone
	runAClosed.Result = "finished on day 2"
	runAClosed.EndedAt = &closedAt
	writeRunRecordAt(t, s, taskID, runAClosed, day2)

	// Run B: opened AND closed on day1 only (single-file run).
	runB := TaskRun{
		RunID:     "run-b",
		TaskID:    taskID,
		Status:    StatusInProgress,
		SessionID: "session-b",
		Kind:      RunKindScheduled,
		StartedAt: day1.Add(time.Hour).Format(time.RFC3339),
	}
	writeRunRecordAt(t, s, taskID, runB, day1)
	runBClosedAt := day1.Add(2 * time.Hour).Format(time.RFC3339)
	runBClosed := runB
	runBClosed.Status = StatusFailed
	runBClosed.Result = "boom"
	runBClosed.EndedAt = &runBClosedAt
	writeRunRecordAt(t, s, taskID, runBClosed, day1)

	// Sanity: two distinct day files exist on disk.
	entries, err := os.ReadDir(filepath.Join(s.Dir(), taskID, "runs"))
	require.NoError(t, err)
	require.Len(t, entries, 2)

	runs, err := s.ListRuns(taskID)
	require.NoError(t, err)
	require.Len(t, runs, 2, "fold must produce exactly one materialized record per run_id")

	byID := map[string]TaskRun{}
	for _, r := range runs {
		byID[r.RunID] = r
	}

	gotA := byID["run-a"]
	assert.Equal(t, StatusDone, gotA.Status, "run-a must fold to its LATER (day2) close record, not the day1 open")
	assert.Equal(t, "finished on day 2", gotA.Result)
	require.NotNil(t, gotA.EndedAt)

	gotB := byID["run-b"]
	assert.Equal(t, StatusFailed, gotB.Status)
	assert.Equal(t, "boom", gotB.Result)

	// Newest-first ordering: run-b started later (day1+1h) than run-a (day1).
	require.Len(t, runs, 2)
	assert.Equal(t, "run-b", runs[0].RunID, "ListRuns must be newest-first by started_at")
	assert.Equal(t, "run-a", runs[1].RunID)
}

func TestRunsInRange_FoldsAcrossMultipleDayFilesAndFiltersByOccurrence(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"

	day1 := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 2, 2, 9, 0, 0, 0, time.UTC)

	inRange := TaskRun{
		RunID:        "run-in-range",
		TaskID:       taskID,
		OccurrenceMs: ptr(int64(5000)),
		Status:       StatusInProgress,
		SessionID:    "s1",
		Kind:         RunKindScheduled,
		StartedAt:    day1.Format(time.RFC3339),
	}
	writeRunRecordAt(t, s, taskID, inRange, day1)
	// Close it on day2 — the fold must still find it via RunsInRange even
	// though the close record lives in a different day file than the open.
	closedAt := day2.Format(time.RFC3339)
	inRangeClosed := inRange
	inRangeClosed.Status = StatusDone
	inRangeClosed.Result = "ok"
	inRangeClosed.EndedAt = &closedAt
	writeRunRecordAt(t, s, taskID, inRangeClosed, day2)

	outOfRange := TaskRun{
		RunID:        "run-out-of-range",
		TaskID:       taskID,
		OccurrenceMs: ptr(int64(999999)),
		Status:       StatusDone,
		SessionID:    "s2",
		Kind:         RunKindScheduled,
		StartedAt:    day1.Format(time.RFC3339),
	}
	writeRunRecordAt(t, s, taskID, outOfRange, day1)

	adhoc := TaskRun{
		RunID:     "run-adhoc",
		TaskID:    taskID,
		Status:    StatusDone,
		SessionID: "s3",
		Kind:      RunKindManual,
		StartedAt: day1.Format(time.RFC3339),
	}
	writeRunRecordAt(t, s, taskID, adhoc, day1)

	got, err := s.RunsInRange(taskID, 0, 10000)
	require.NoError(t, err)
	require.Len(t, got, 1, "only the in-range, non-nil-occurrence run must be returned")
	assert.Equal(t, "run-in-range", got[0].RunID)
	assert.Equal(t, StatusDone, got[0].Status, "must reflect the folded (closed) state")
}

// ---- PruneRuns: age + floor-of-one -------------------------------------------

func TestPruneRuns_AgeCutoffDeletesOldFiles(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"
	dir := filepath.Join(s.Dir(), taskID, "runs")

	old1 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	old2 := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, d := range []time.Time{old1, old2, recent} {
		rec := TaskRun{
			RunID:     "run-" + d.Format("20060102"),
			TaskID:    taskID,
			Status:    StatusDone,
			Result:    "x",
			SessionID: "s",
			Kind:      RunKindManual,
			StartedAt: d.Format(time.RFC3339),
		}
		endedAt := d.Format(time.RFC3339)
		rec.EndedAt = &endedAt
		writeRunRecordAt(t, s, taskID, rec, d)
	}
	// Backdate the files' mtimes to match their nominal day (WriteFileAtomic
	// stamps the real "now" mtime on write, which would otherwise make every
	// file look equally fresh to the cutoff check).
	chtimes(t, filepath.Join(dir, old1.Format("2006-01-02")+".jsonl"), old1)
	chtimes(t, filepath.Join(dir, old2.Format("2006-01-02")+".jsonl"), old2)
	chtimes(t, filepath.Join(dir, recent.Format("2006-01-02")+".jsonl"), recent)

	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.PruneRuns(taskID, cutoff))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{recent.Format("2006-01-02") + ".jsonl"}, names,
		"both old1 and old2 (before cutoff) must be deleted; recent (after cutoff) retained")
}

func TestPruneRuns_FloorOfOneOverridesCutoff(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"
	dir := filepath.Join(s.Dir(), taskID, "runs")

	veryOld1 := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)
	veryOld2 := time.Date(2018, 1, 2, 0, 0, 0, 0, time.UTC)

	for _, d := range []time.Time{veryOld1, veryOld2} {
		rec := TaskRun{
			RunID:     "run-" + d.Format("20060102"),
			TaskID:    taskID,
			Status:    StatusDone,
			SessionID: "s",
			Kind:      RunKindManual,
			StartedAt: d.Format(time.RFC3339),
		}
		endedAt := d.Format(time.RFC3339)
		rec.EndedAt = &endedAt
		writeRunRecordAt(t, s, taskID, rec, d)
		chtimes(t, filepath.Join(dir, d.Format("2006-01-02")+".jsonl"), d)
	}

	// cutoff is "now" — both files are ancient, both would normally be
	// deleted, but the floor-of-one must always retain the newest.
	require.NoError(t, s.PruneRuns(taskID, time.Now()))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "floor-of-one must retain exactly the newest day file")
	assert.Equal(t, veryOld2.Format("2006-01-02")+".jsonl", entries[0].Name())
}

func TestPruneRuns_NoRunsDirIsNoop(t *testing.T) {
	s := newStore(t)
	err := s.PruneRuns("task-never-ran", time.Now())
	assert.NoError(t, err)
}

// TestPruneRuns_OpenRunSurvivesAgeCutoff verifies the operator's 2026-07-20
// no-reaper decision at the store level: an in_progress run's day file is
// NOT specially protected by PruneRuns beyond the ordinary floor-of-one —
// but critically, PruneRuns never closes, mutates, or otherwise touches an
// open run's record. A run may legitimately stay in_progress for days ("a
// run could even go over days"), and only its own execution's CloseRun call
// ever terminates it.
func TestPruneRuns_OpenRunSurvivesAgeCutoff(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"

	oldStart := time.Now().Add(-48 * time.Hour)
	openRun := TaskRun{
		RunID:     "still-open-run",
		TaskID:    taskID,
		Status:    StatusInProgress,
		SessionID: "s-open",
		Kind:      RunKindScheduled,
		StartedAt: oldStart.Format(time.RFC3339),
	}
	writeRunRecordAt(t, s, taskID, openRun, oldStart)

	// A cutoff far in the future would normally delete every file were it
	// not for the floor-of-one AND the fact that PruneRuns performs no
	// reaping — there is only ever one day file here, so the floor-of-one
	// alone already protects it, but the point under test is Status: no
	// reaper logic runs to flip it to failed.
	require.NoError(t, s.PruneRuns(taskID, time.Now().Add(24*time.Hour)))

	runs, err := s.ListRuns(taskID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, StatusInProgress, runs[0].Status, "PruneRuns must never close an open run")
	assert.Nil(t, runs[0].EndedAt)
}

// TestPruneRuns_StaleDayFileWithOpenRunIsNotDeleted is the regression for
// delta-review Fix 1 (2026-07-20): with no stuck-run reaper, a day file that
// is the ONLY surviving record of a still-open run must never be deleted
// purely on age — doing so would make an orphaned in_progress run vanish
// from history entirely (worse than the intended "stays in_progress
// forever"). A stale file whose only run is already closed has no such
// record to protect and is deleted normally by the ordinary age+floor-of-one
// rule.
func TestPruneRuns_StaleDayFileWithOpenRunIsNotDeleted(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"
	dir := filepath.Join(s.Dir(), taskID, "runs")

	oldClosedDay := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	oldOpenDay := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	recentDay := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	closedRun := TaskRun{
		RunID:     "closed-run",
		TaskID:    taskID,
		Status:    StatusDone,
		Result:    "ok",
		SessionID: "s-closed",
		Kind:      RunKindManual,
		StartedAt: oldClosedDay.Format(time.RFC3339),
	}
	closedEndedAt := oldClosedDay.Format(time.RFC3339)
	closedRun.EndedAt = &closedEndedAt
	writeRunRecordAt(t, s, taskID, closedRun, oldClosedDay)

	// This run's open record is its ONLY record anywhere — no close record
	// exists in any file, simulating a crashed/orphaned execution with no
	// reaper to close it.
	openRun := TaskRun{
		RunID:     "orphaned-open-run",
		TaskID:    taskID,
		Status:    StatusInProgress,
		SessionID: "s-open",
		Kind:      RunKindScheduled,
		StartedAt: oldOpenDay.Format(time.RFC3339),
	}
	writeRunRecordAt(t, s, taskID, openRun, oldOpenDay)

	recentRun := TaskRun{
		RunID:     "recent-run",
		TaskID:    taskID,
		Status:    StatusDone,
		Result:    "ok",
		SessionID: "s-recent",
		Kind:      RunKindManual,
		StartedAt: recentDay.Format(time.RFC3339),
	}
	recentEndedAt := recentDay.Format(time.RFC3339)
	recentRun.EndedAt = &recentEndedAt
	writeRunRecordAt(t, s, taskID, recentRun, recentDay)

	chtimes(t, filepath.Join(dir, oldClosedDay.Format("2006-01-02")+".jsonl"), oldClosedDay)
	chtimes(t, filepath.Join(dir, oldOpenDay.Format("2006-01-02")+".jsonl"), oldOpenDay)
	chtimes(t, filepath.Join(dir, recentDay.Format("2006-01-02")+".jsonl"), recentDay)

	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.PruneRuns(taskID, cutoff))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(
		t,
		[]string{
			oldOpenDay.Format("2006-01-02") + ".jsonl",
			recentDay.Format("2006-01-02") + ".jsonl",
		},
		names,
		"the stale closed-only file must be deleted; the stale file holding the orphaned open run must survive; the newest file is always retained (floor-of-one)",
	)

	runs, err := s.ListRuns(taskID)
	require.NoError(t, err)
	var stillHasOpenRun bool
	for _, r := range runs {
		if r.RunID == "orphaned-open-run" {
			stillHasOpenRun = true
			assert.True(t, r.IsOpen(), "the orphaned run must still read as in_progress — its record was not deleted")
		}
	}
	assert.True(t, stillHasOpenRun, "the orphaned open run must remain visible in run history after PruneRuns")
}

// TestPruneRuns_OldFileWithClosedRunClosedInNewerFileIsDeleted proves the
// Fix 1 guard is GLOBAL-fold-aware, not per-file: a run whose open record
// landed in an old day file (e.g. it started right before a UTC day
// boundary) but whose close record was appended to a NEWER file
// (appendRunRecord always targets the CLOSE time's day, per CloseRun's own
// doc comment) must NOT be treated as still open. The old file holding only
// its (superseded) open record is safe to delete once its day is past
// cutoff — the run's identity/result survive via the newer file's full-copy
// close record (RD9). This guards against the guard itself becoming
// overly conservative and defeating retention for the common case of a run
// that opens and closes on different calendar days.
func TestPruneRuns_OldFileWithClosedRunClosedInNewerFileIsDeleted(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"
	dir := filepath.Join(s.Dir(), taskID, "runs")

	openDay := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	closeDay := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC) // still stale, but newer than openDay
	recentDay := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	openRec := TaskRun{
		RunID:     "spans-midnight-run",
		TaskID:    taskID,
		Status:    StatusInProgress,
		SessionID: "s",
		Kind:      RunKindManual,
		StartedAt: openDay.Format(time.RFC3339),
	}
	writeRunRecordAt(t, s, taskID, openRec, openDay)

	closeRec := openRec
	closeRec.Status = StatusDone
	closeRec.Result = "done"
	closedAt := closeDay.Format(time.RFC3339)
	closeRec.EndedAt = &closedAt
	writeRunRecordAt(t, s, taskID, closeRec, closeDay)

	recentRun := TaskRun{
		RunID:     "recent-run",
		TaskID:    taskID,
		Status:    StatusDone,
		SessionID: "s2",
		Kind:      RunKindManual,
		StartedAt: recentDay.Format(time.RFC3339),
	}
	recentEndedAt := recentDay.Format(time.RFC3339)
	recentRun.EndedAt = &recentEndedAt
	writeRunRecordAt(t, s, taskID, recentRun, recentDay)

	chtimes(t, filepath.Join(dir, openDay.Format("2006-01-02")+".jsonl"), openDay)
	chtimes(t, filepath.Join(dir, closeDay.Format("2006-01-02")+".jsonl"), closeDay)
	chtimes(t, filepath.Join(dir, recentDay.Format("2006-01-02")+".jsonl"), recentDay)

	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.PruneRuns(taskID, cutoff))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(
		t,
		[]string{recentDay.Format("2006-01-02") + ".jsonl"},
		names,
		"both old files hold only a superseded/closed record for the same globally-closed run and must be deleted; only the newest file survives (floor-of-one)",
	)

	runs, err := s.ListRuns(taskID)
	require.NoError(t, err)
	for _, r := range runs {
		if r.RunID == "spans-midnight-run" {
			t.Fatalf("closed run's record must have been pruned along with both stale day files, got %+v", r)
		}
	}
}

// ---- Concurrency -------------------------------------------------------------

func TestOpenRun_ConcurrentSameKeyCreatesExactlyOne(t *testing.T) {
	s := newStore(t)
	taskID := "task-1"
	occ := ptr(int64(123))

	const n = 20
	var wg sync.WaitGroup
	results := make([]*TaskRun, n)
	createdFlags := make([]bool, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			run, created, err := s.OpenRun(taskID, occ, RunKindScheduled, "session")
			results[idx] = run
			createdFlags[idx] = created
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	createdCount := 0
	var runID string
	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		require.NotNil(t, results[i])
		if createdFlags[i] {
			createdCount++
		}
		if runID == "" {
			runID = results[i].RunID
		} else {
			assert.Equal(t, runID, results[i].RunID, "every concurrent caller must observe the SAME run")
		}
	}
	assert.Equal(t, 1, createdCount, "exactly one goroutine must have created the run")

	// And exactly one run record exists on disk for this task.
	runs, err := s.ListRuns(taskID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
}

// ---- Validators --------------------------------------------------------------

func TestIsValidRunKindAndRunStatus(t *testing.T) {
	assert.True(t, IsValidRunKind(RunKindScheduled))
	assert.True(t, IsValidRunKind(RunKindManual))
	assert.False(t, IsValidRunKind(RunKind("bogus")))

	assert.True(t, IsValidRunStatus(StatusInProgress))
	assert.True(t, IsValidRunStatus(StatusDone))
	assert.True(t, IsValidRunStatus(StatusFailed))
	assert.False(t, IsValidRunStatus(StatusInbox))
	assert.False(t, IsValidRunStatus(StatusBlocked))
}

func TestErrRunNotFoundIsDistinctSentinel(t *testing.T) {
	// Guard against ErrRunNotFound/ErrRunAlreadyClosed accidentally aliasing
	// each other or the generic ErrNotFound sentinel.
	assert.False(t, errors.Is(ErrRunNotFound, ErrNotFound))
	assert.False(t, errors.Is(ErrRunNotFound, ErrRunAlreadyClosed))
}
