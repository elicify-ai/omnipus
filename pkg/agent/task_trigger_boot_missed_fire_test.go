// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Boot-time recording of scheduled fires MISSED while the process was down
// (founder decision 2026-10-29, Q1 A: "A missed Calendar run stays skipped,
// never replayed. Add a visible 'missed' note in the Calendar"; train 6b,
// option B).
//
// The oracle, per case:
//
//  1. A recurring (rrule) trigger whose pending occurrence came due while
//     Omnipus was down gets one TaskRun close record with status "missed"
//     for that exact instant — the calendar join key — so GET
//     /api/v1/tasks/occurrences can surface it in occurrence_runs. The run
//     itself is never replayed.
//
//  2. A one-time trigger whose at-time passed while down gets the same
//     record, with a nil OccurrenceMs (TaskRun.OccurrenceMs's contract:
//     null for a once run); it shows in the task's run history.
//
//  3. A fire the dead process had STARTED (interrupted, Running persisted)
//     is NOT 'missed' — it is owed and fires at boot (the boot-only start
//     fix, task_trigger_boot_owed_fire_test.go); labelling it missed would
//     mislabel work that actually ran.
//
//  4. An occurrence that already has a run record (it ran; the process
//     died before the re-arm) is never labelled missed — one record per
//     missed instant, and a real run must not be out-ranked by a bookkeeping
//     record.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// armedInstantLocked returns the task's tracked job's pending next-run
// instant (the occurrence the engine believed was still to come).
func armedInstantLocked(t *testing.T, s *TaskTriggerScheduler, taskID string) int64 {
	t.Helper()
	s.mu.Lock()
	tracked, ok := s.taskToJob[taskID]
	s.mu.Unlock()
	if !ok {
		t.Fatalf("no tracked job for task %s", taskID)
	}
	job, jok := s.cs.GetJob(tracked.jobID)
	if !jok || job.State.NextRunAtMS == nil {
		t.Fatalf("tracked job for task %s is not armed (job=%v nextRun=%v)", taskID, jok, job.State.NextRunAtMS)
	}
	return *job.State.NextRunAtMS
}

// missedRuns returns the task's runs carrying status "missed".
func missedRuns(t *testing.T, store *task.Store, taskID string) []task.TaskRun {
	t.Helper()
	runs, err := store.ListRuns(taskID)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	var missed []task.TaskRun
	for _, r := range runs {
		if r.Status == "missed" {
			missed = append(missed, r)
		}
	}
	return missed
}

func TestTaskTriggerBoot_MissedRecurringFire_RecordedNotReplayed(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	storePath := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()

	s1 := NewTaskTriggerScheduler(storePath, store, nil)
	s1.dispatch = (&dispatchRecorder{}).dispatch
	s1.SetClock(clk)
	if err := s1.Start(); err != nil {
		t.Fatalf("process 1 Start: %v", err)
	}
	rule, tz := "FREQ=DAILY;COUNT=5", "UTC"
	dtstart := clk.Now().Add(time.Minute).UnixMilli()
	tsk := makeTask(t, store, "agent-rrule", &task.Trigger{
		Type:   task.TriggerRecurring,
		Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &dtstart, Tz: &tz},
	})
	s1.OnTaskUpserted(tsk)
	instant := armedInstantLocked(t, s1, tsk.ID)
	s1.Stop()

	clk.Advance(2 * time.Minute) // the occurrence passes while down

	rec2 := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(storePath, store, nil)
	s2.dispatch = rec2.dispatch
	s2.SetClock(clk)
	if err := s2.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("process 2 StartAfterPhysicalBoot: %v", err)
	}
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("process 2 Reconcile: %v", err)
	}
	defer s2.Stop()

	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	if got := rec2.calls(); len(got) != 0 {
		t.Fatalf("a missed fire is never replayed (founder Q1 A); dispatches = %v", got)
	}

	missed := missedRuns(t, store, tsk.ID)
	if len(missed) != 1 {
		t.Fatalf("the missed occurrence must be recorded exactly once with status missed; missed records = %d", len(missed))
	}
	if missed[0].OccurrenceMs == nil || *missed[0].OccurrenceMs != instant {
		t.Fatalf("the missed record must carry the missed instant as its occurrence key; got %v, want %d",
			missed[0].OccurrenceMs, instant)
	}
	if missed[0].Kind != "scheduled" {
		t.Fatalf("a missed scheduled fire is kind scheduled; got %q", missed[0].Kind)
	}
	if missed[0].SessionID != "" {
		t.Fatalf("a missed fire never started a session; got session_id %q", missed[0].SessionID)
	}

	// A second boot must not duplicate the record: one record per missed
	// instant.
	clk.Advance(time.Minute)
	s3 := NewTaskTriggerScheduler(storePath, store, nil)
	s3.dispatch = (&dispatchRecorder{}).dispatch
	s3.SetClock(clk)
	if err := s3.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("process 3 StartAfterPhysicalBoot: %v", err)
	}
	s3.Reconcile()
	s3.Stop()
	if got := len(missedRuns(t, store, tsk.ID)); got != 1 {
		t.Fatalf("a second boot must not duplicate the missed record; missed records = %d", got)
	}
}

func TestTaskTriggerBoot_MissedOnceFire_RecordedNotReplayed(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	storePath := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()

	s1 := NewTaskTriggerScheduler(storePath, store, nil)
	s1.dispatch = (&dispatchRecorder{}).dispatch
	s1.SetClock(clk)
	if err := s1.Start(); err != nil {
		t.Fatalf("process 1 Start: %v", err)
	}
	fireAt := clk.Now().Add(time.Minute).UnixMilli()
	tsk := makeTask(t, store, "agent-once", &task.Trigger{
		Type:   task.TriggerOnce,
		Config: task.TriggerConfig{AtMs: int64P(fireAt)},
	})
	s1.OnTaskUpserted(tsk)
	s1.Stop()

	clk.Advance(2 * time.Minute) // the at-time passes while down

	rec2 := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(storePath, store, nil)
	s2.dispatch = rec2.dispatch
	s2.SetClock(clk)
	if err := s2.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("process 2 StartAfterPhysicalBoot: %v", err)
	}
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("process 2 Reconcile: %v", err)
	}
	defer s2.Stop()

	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	if got := rec2.calls(); len(got) != 0 {
		t.Fatalf("a missed fire is never replayed (founder Q1 A); dispatches = %v", got)
	}

	missed := missedRuns(t, store, tsk.ID)
	if len(missed) != 1 {
		t.Fatalf("the missed once fire must be recorded exactly once with status missed; missed records = %d", len(missed))
	}
	if missed[0].OccurrenceMs != nil {
		t.Fatalf("a once run's occurrence_ms stays null per the TaskRun contract; got %d", *missed[0].OccurrenceMs)
	}
}

func TestTaskTriggerBoot_InterruptedRecurringFire_RunsIsNotMissed(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	storePath := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()

	rule, tz := "FREQ=DAILY;COUNT=5", "UTC"
	dtstart := clk.Now().Add(time.Minute).UnixMilli()

	// Process 1: arm a recurring trigger and die while its fire is running.
	blocker := &crashRunRecorder{entered: make(chan struct{}), release: make(chan struct{})}
	s1 := NewTaskTriggerScheduler(storePath, store, nil)
	s1.dispatch = blocker.dispatch
	s1.SetClock(clk)
	if err := s1.Start(); err != nil {
		t.Fatalf("process 1 Start: %v", err)
	}
	tsk := makeTask(t, store, "agent-rrule", &task.Trigger{
		Type:   task.TriggerRecurring,
		Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &dtstart, Tz: &tz},
	})
	s1.OnTaskUpserted(tsk)
	clk.Advance(2 * time.Minute)
	s1.RunDueJobs(clk.Now())
	select {
	case <-blocker.entered:
	case <-time.After(10 * time.Second):
		s1.Stop()
		t.Fatal("the recurring fire never started; cannot build a crash image")
	}
	crashImage, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("read crash image: %v", err)
	}
	close(blocker.release)
	s1.WaitForLane()
	s1.Stop()

	// Process 2: a fire the dead process had STARTED is a different failure
	// class from a missed one — the boot re-anchor to the next future
	// occurrence stands (task_trigger.go StartAfterPhysicalBoot's doc), the
	// crashed run is not replayed, and this feature must not label a
	// potentially partially-executed fire as missed.
	if err := os.WriteFile(storePath, crashImage, 0o600); err != nil {
		t.Fatalf("write crash image: %v", err)
	}
	rec2 := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(storePath, store, nil)
	s2.dispatch = rec2.dispatch
	s2.SetClock(clk)
	if err := s2.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("process 2 StartAfterPhysicalBoot: %v", err)
	}
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("process 2 Reconcile: %v", err)
	}
	defer s2.Stop()

	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	if got := rec2.calls(); len(got) != 0 {
		t.Fatalf("the boot re-anchor stands for recurring triggers; the crashed occurrence is not replayed; dispatches = %v", got)
	}
	if got := missedRuns(t, store, tsk.ID); len(got) != 0 {
		t.Fatalf("a fire that started must never be labelled missed; missed records = %d", len(got))
	}
}

func TestTaskTriggerBoot_OccurrenceWithExistingRun_NeverRelabelledMissed(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	storePath := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()

	s1 := NewTaskTriggerScheduler(storePath, store, nil)
	s1.dispatch = (&dispatchRecorder{}).dispatch
	s1.SetClock(clk)
	if err := s1.Start(); err != nil {
		t.Fatalf("process 1 Start: %v", err)
	}
	rule, tz := "FREQ=DAILY;COUNT=5", "UTC"
	dtstart := clk.Now().Add(time.Minute).UnixMilli()
	tsk := makeTask(t, store, "agent-rrule", &task.Trigger{
		Type:   task.TriggerRecurring,
		Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &dtstart, Tz: &tz},
	})
	s1.OnTaskUpserted(tsk)
	instant := armedInstantLocked(t, s1, tsk.ID)
	// The occurrence already has a run (it ran; the process died before the
	// re-arm landed). A bookkeeping record must not out-rank it.
	if _, _, err := store.RecordSkippedOccurrence(tsk.ID, &instant); err != nil {
		t.Fatalf("seed existing run: %v", err)
	}
	s1.Stop()

	clk.Advance(2 * time.Minute) // the armed instant is now in the past

	s2 := NewTaskTriggerScheduler(storePath, store, nil)
	s2.dispatch = (&dispatchRecorder{}).dispatch
	s2.SetClock(clk)
	if err := s2.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("process 2 StartAfterPhysicalBoot: %v", err)
	}
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("process 2 Reconcile: %v", err)
	}
	s2.Stop()

	if got := missedRuns(t, store, tsk.ID); len(got) != 0 {
		t.Fatalf("an occurrence that already has a run must never be relabelled missed; missed records = %d", len(got))
	}
	runs, err := store.RunsInRange(tsk.ID, instant, instant+1)
	if err != nil {
		t.Fatalf("RunsInRange: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "skipped" {
		t.Fatalf("the existing run must stand untouched; runs = %+v", runs)
	}
}
