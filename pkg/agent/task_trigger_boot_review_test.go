// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Boot recovery of task triggers, 6b code review findings I2-I4 (train 6b,
// #1484; docs/calendar.md "Limits and things to watch"). Ported from the
// reviewer's probes (TestReview6b_CrashedClaimedOnceFire,
// TestReview6b_RecurringReplayBeforeReconcile,
// TestReview6b_MultipleMissedInstants).
//
//   - I2: a one-time fire whose task the dead process had already claimed
//     (task in_progress on disk) is owed its rerun at the next boot; the
//     overlap guard must not mistake the dead claim for a live run.
//   - I3: an interrupted RECURRING occurrence is never replayed, not even in
//     the window between the engine start and the boot Reconcile.
//   - I4: every recurring instant that came due while the process was down
//     gets its own missed record, at most the 20 most recent per boot.

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// crashOnceFireWithClaim runs process 1 up to a death in the middle of a once
// fire whose task has been durably claimed, and writes that crash image back
// to storePath. Returns the task.
func crashOnceFireWithClaim(t *testing.T, store *task.Store, storePath string, clk *triggerFakeClock) *task.Task {
	t.Helper()
	blocker := newCrashRunRecorder(store, clk)
	s1 := NewTaskTriggerScheduler(storePath, store, nil)
	s1.dispatch = blocker.dispatch
	s1.SetClock(clk)
	if err := s1.Start(); err != nil {
		t.Fatalf("process 1 Start: %v", err)
	}
	fire := clk.Now().Add(time.Minute).UnixMilli()
	tsk := makeTask(t, store, "agent-review", &task.Trigger{
		Type:   task.TriggerOnce,
		Config: task.TriggerConfig{AtMs: &fire},
	})
	s1.OnTaskUpserted(tsk)
	clk.Advance(2 * time.Minute)
	s1.RunDueJobs(clk.Now())
	select {
	case <-blocker.entered:
	case <-time.After(10 * time.Second):
		close(blocker.release)
		s1.Stop()
		t.Fatal("claim/dispatch never entered; cannot build a crash image")
	}
	image, err := os.ReadFile(storePath)
	close(blocker.release)
	s1.WaitForLane()
	s1.Stop()
	if err != nil {
		t.Fatalf("read crash image: %v", err)
	}
	claimed, err := store.Get(tsk.ID)
	if err != nil || claimed.Status != task.StatusInProgress {
		t.Fatalf("precondition: the dead claim must be durable; task=%+v err=%v", claimed, err)
	}
	if err := os.WriteFile(storePath, image, 0o600); err != nil {
		t.Fatalf("write crash image: %v", err)
	}
	return tsk
}

// TestTaskTriggerBoot_CrashedClaimedOnceFire_RerunsOnce: the crash image
// holds both the cron job's Running=true and the task's in_progress claim.
// The next boot reruns the fire exactly once.
func TestTaskTriggerBoot_CrashedClaimedOnceFire_RerunsOnce(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	path := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()
	tsk := crashOnceFireWithClaim(t, store, path, clk)

	rec := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(path, store, nil)
	s2.dispatch = rec.dispatch
	s2.SetClock(clk)
	if err := s2.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("StartAfterPhysicalBoot: %v", err)
	}
	defer s2.Stop()
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	if calls := rec.calls(); len(calls) != 1 || calls[0] != tsk.ID {
		runs, _ := store.ListRuns(tsk.ID)
		t.Fatalf("owed claimed once fire was not rerun: dispatches=%v runs=%+v", calls, runs)
	}
	runs, err := store.ListRuns(tsk.ID)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	for _, r := range runs {
		if r.Status == task.StatusSkipped {
			t.Fatalf("the owed rerun must not be recorded as an overlap skip; runs=%+v", runs)
		}
	}
	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	if calls := rec.calls(); len(calls) != 1 {
		t.Fatalf("the owed rerun must happen once only; dispatches=%v", calls)
	}
}

// TestTaskTriggerBoot_ClaimedOnceFire_RerunsAfterStuckTaskReconcile pins the
// production boot order: the gateway reconciles tasks left in_progress by
// the dead process (ReconcileInterruptedTasks) BEFORE the trigger scheduler
// starts, so a rerun this boot dispatches can never be failed by that
// reconcile afterwards. The owed fire must survive that order too.
func TestTaskTriggerBoot_ClaimedOnceFire_RerunsAfterStuckTaskReconcile(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	path := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()
	tsk := crashOnceFireWithClaim(t, store, path, clk)

	ReconcileInterruptedTasks(store)
	recovered, err := store.Get(tsk.ID)
	if err != nil || recovered.Status != task.StatusFailed || recovered.Result != InterruptedTaskResult {
		t.Fatalf("precondition: the dead claim is reconciled to failed/interrupted; task=%+v err=%v", recovered, err)
	}

	rec := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(path, store, nil)
	s2.dispatch = rec.dispatch
	s2.SetClock(clk)
	if err := s2.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("StartAfterPhysicalBoot: %v", err)
	}
	defer s2.Stop()
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	if calls := rec.calls(); len(calls) != 1 || calls[0] != tsk.ID {
		t.Fatalf("owed once fire must rerun after the stuck-task reconcile: dispatches=%v", calls)
	}
}

// TestTaskTriggerBoot_OnceFireLiveClaim_OverlapGuardStillSkips keeps the
// same-process overlap protection: a task claimed by a LIVE run of this
// process is not stomped by its trigger fire.
func TestTaskTriggerBoot_OnceFireLiveClaim_OverlapGuardStillSkips(t *testing.T) {
	s, store, clk, rec := newTriggerSchedulerForTest(t)
	fire := clk.Now().Add(time.Minute).UnixMilli()
	tsk := makeTask(t, store, "agent-review", &task.Trigger{
		Type:   task.TriggerOnce,
		Config: task.TriggerConfig{AtMs: &fire},
	})
	if _, err := store.ClaimForRun(tsk.ID, clk.Now()); err != nil {
		t.Fatalf("live claim: %v", err)
	}
	s.OnTaskUpserted(tsk)
	clk.Advance(2 * time.Minute)
	s.RunDueJobs(clk.Now())
	s.WaitForLane()
	if calls := rec.calls(); len(calls) != 0 {
		t.Fatalf("a live in_progress run must not be stomped by its trigger fire; dispatches=%v", calls)
	}
}

// TestTaskTriggerBoot_InterruptedRecurring_NotReplayedBeforeReconcile models
// the runLoop started by StartAfterPhysicalBoot ticking before the gateway's
// Reconcile: an interrupted recurring occurrence must not run.
func TestTaskTriggerBoot_InterruptedRecurring_NotReplayedBeforeReconcile(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	path := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()
	blocker := newCrashRunRecorder(store, clk)
	s1 := NewTaskTriggerScheduler(path, store, nil)
	s1.dispatch = blocker.dispatch
	s1.SetClock(clk)
	if err := s1.Start(); err != nil {
		t.Fatalf("process 1 Start: %v", err)
	}
	rule, tz := "FREQ=DAILY;COUNT=5", "UTC"
	first := clk.Now().Add(time.Minute).UnixMilli()
	tsk := makeTask(t, store, "agent-review", &task.Trigger{
		Type:   task.TriggerRecurring,
		Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &first, Tz: &tz},
	})
	s1.OnTaskUpserted(tsk)
	clk.Advance(2 * time.Minute)
	s1.RunDueJobs(clk.Now())
	select {
	case <-blocker.entered:
	case <-time.After(10 * time.Second):
		close(blocker.release)
		s1.Stop()
		t.Fatal("first dispatch never entered")
	}
	image, err := os.ReadFile(path)
	close(blocker.release)
	s1.WaitForLane()
	s1.Stop()
	if err != nil {
		t.Fatalf("read crash image: %v", err)
	}
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatalf("write crash image: %v", err)
	}
	// The dead claim is reconciled as the gateway does at boot, so a replay
	// would not be hidden by the overlap guard.
	ReconcileInterruptedTasks(store)

	rec := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(path, store, nil)
	s2.dispatch = rec.dispatch
	s2.SetClock(clk)
	if err := s2.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("StartAfterPhysicalBoot: %v", err)
	}
	defer s2.Stop()
	// What the runLoop started by StartAfterPhysicalBoot may do before the
	// gateway gets to Reconcile.
	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if calls := rec.calls(); len(calls) != 0 {
		t.Fatalf("recurring interrupted occurrence was replayed before reconcile: dispatches=%v instants=%v",
			calls, rec.occurrences())
	}
	// The series continues at its next future occurrence.
	if next := armedInstantLocked(t, s2, tsk.ID); next <= clk.Now().UnixMilli() {
		t.Fatalf("the series must be re-armed at a future occurrence; armed=%d now=%d", next, clk.Now().UnixMilli())
	}
}

// bootMinutelyAfterDowntime arms a minutely rrule (COUNT=count), stops
// before the first fire, advances the clock by downtime and boots again.
// Returns the task and the first armed instant.
func bootMinutelyAfterDowntime(t *testing.T, count int, downtime time.Duration) (*task.Store, *task.Task, int64) {
	t.Helper()
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	path := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()
	s1 := NewTaskTriggerScheduler(path, store, nil)
	s1.dispatch = (&dispatchRecorder{}).dispatch
	s1.SetClock(clk)
	if err := s1.Start(); err != nil {
		t.Fatalf("process 1 Start: %v", err)
	}
	rule := "FREQ=MINUTELY;COUNT=" + strconv.Itoa(count)
	tz := "UTC"
	dtstart := clk.Now().Add(time.Minute).UnixMilli()
	tsk := makeTask(t, store, "agent-review", &task.Trigger{
		Type:   task.TriggerRecurring,
		Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &dtstart, Tz: &tz},
	})
	s1.OnTaskUpserted(tsk)
	// RRULE instants have whole-second precision: read the armed instant.
	first := armedInstantLocked(t, s1, tsk.ID)
	s1.Stop()
	clk.Advance(downtime)

	rec := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(path, store, nil)
	s2.dispatch = rec.dispatch
	s2.SetClock(clk)
	if err := s2.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("StartAfterPhysicalBoot: %v", err)
	}
	defer s2.Stop()
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	if calls := rec.calls(); len(calls) != 0 {
		t.Fatalf("missed recurring instants are recorded, never replayed; dispatches=%v", calls)
	}
	return store, tsk, first
}

func missedInstantSet(t *testing.T, store *task.Store, taskID string) map[int64]int {
	t.Helper()
	out := make(map[int64]int)
	for _, r := range missedRuns(t, store, taskID) {
		if r.OccurrenceMs == nil {
			t.Fatalf("a recurring missed record must carry its instant; run=%+v", r)
		}
		out[*r.OccurrenceMs]++
	}
	return out
}

// TestTaskTriggerBoot_MultipleMissedInstants_EachRecorded: four minutely
// instants pass while down; each gets exactly one missed record.
func TestTaskTriggerBoot_MultipleMissedInstants_EachRecorded(t *testing.T) {
	store, tsk, first := bootMinutelyAfterDowntime(t, 10, 4*time.Minute+30*time.Second)
	got := missedInstantSet(t, store, tsk.ID)
	for n := 0; n < 4; n++ {
		want := first + int64(n)*time.Minute.Milliseconds()
		if got[want] != 1 {
			t.Errorf("missed instant %d (+%dm) must have exactly one missed record; recorded=%v", want, n, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("only the four instants that came due while down are missed; recorded=%v", got)
	}
}

// TestTaskTriggerBoot_MissedInstants_CappedAtTwentyMostRecent: 30 instants
// pass while down; only the 20 most recent are recorded (docs/calendar.md).
func TestTaskTriggerBoot_MissedInstants_CappedAtTwentyMostRecent(t *testing.T) {
	store, tsk, first := bootMinutelyAfterDowntime(t, 100, 30*time.Minute+30*time.Second)
	got := missedInstantSet(t, store, tsk.ID)
	if len(got) != 20 {
		t.Fatalf("at most the 20 most recent missed instants are recorded; got %d: %v", len(got), got)
	}
	for n := 10; n < 30; n++ {
		want := first + int64(n)*time.Minute.Milliseconds()
		if got[want] != 1 {
			t.Errorf("recent missed instant +%dm must be recorded once; recorded=%v", n, got)
		}
	}
}
