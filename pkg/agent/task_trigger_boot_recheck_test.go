// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Boot recovery of task triggers, 6b fix-round re-review findings:
//
//   - N1: classification of interrupted fires happens while this process's
//     task producers (the queued-task drain, the engines) are still stopped,
//     so a claim the drain takes afterwards is never mistaken for the dead
//     process's claim and reset underneath a live run.
//   - N3: when the dead claim of an owed once fire cannot be released, the
//     evidence that the fire was interrupted is kept, so a later boot still
//     owes and reruns it.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// crashOnceFireBeforeClaim runs process 1 up to a death inside a once fire
// before the task was claimed (cron Running=true persisted, task still next)
// and writes that crash image back to storePath.
func crashOnceFireBeforeClaim(t *testing.T, store *task.Store, storePath string, clk *triggerFakeClock) *task.Task {
	t.Helper()
	blocker := newCrashRunRecorder(nil, clk)
	s1 := NewTaskTriggerScheduler(storePath, store, nil)
	s1.dispatch = blocker.dispatch
	s1.SetClock(clk)
	if err := s1.Start(); err != nil {
		t.Fatalf("process 1 Start: %v", err)
	}
	fire := clk.Now().Add(time.Minute).UnixMilli()
	tsk := makeTask(t, store, "agent-recheck", &task.Trigger{
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
		t.Fatal("dispatch never entered; cannot build a crash image")
	}
	image, err := os.ReadFile(storePath)
	close(blocker.release)
	s1.WaitForLane()
	s1.Stop()
	if err != nil {
		t.Fatalf("read crash image: %v", err)
	}
	if got, gErr := store.Get(tsk.ID); gErr != nil || got.Status != task.StatusNext {
		t.Fatalf("precondition: the task was never claimed; task=%+v err=%v", got, gErr)
	}
	if err := os.WriteFile(storePath, image, 0o600); err != nil {
		t.Fatalf("write crash image: %v", err)
	}
	return tsk
}

// TestTaskTriggerBoot_LiveClaimAfterPrepare_NotReset (N1): the gateway
// prepares the trigger scheduler (classification + dead-claim recovery)
// before it starts the queued-task drain. A claim the drain takes after that
// belongs to a live run of this process: the trigger start must not fail it,
// close its run or fire the task a second time.
func TestTaskTriggerBoot_LiveClaimAfterPrepare_NotReset(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	path := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()
	tsk := crashOnceFireBeforeClaim(t, store, path, clk)

	rec := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(path, store, nil)
	s2.dispatch = rec.dispatch
	s2.SetClock(clk)
	if err := s2.PrepareAfterPhysicalBoot(); err != nil {
		t.Fatalf("PrepareAfterPhysicalBoot: %v", err)
	}
	// The drain, started after the prepare, claims the queued task: a live run.
	live, err := store.ClaimForRun(tsk.ID, clk.Now())
	if err != nil {
		t.Fatalf("live drain claim: %v", err)
	}
	if err = s2.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("StartAfterPhysicalBoot: %v", err)
	}
	defer s2.Stop()
	if err = s2.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()

	got, err := store.Get(tsk.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != task.StatusInProgress || got.StartedAt != live.StartedAt {
		t.Fatalf("a live claim of this process must not be reset by boot recovery; task status=%q result=%q",
			got.Status, got.Result)
	}
	if calls := rec.calls(); len(calls) != 0 {
		t.Fatalf("the task is already running live; the owed fire must not start a second run; dispatches=%v", calls)
	}
}

// TestTaskTriggerBoot_ClaimReleaseFailure_KeepsOwedFireForNextBoot (N3): the
// dead claim cannot be released (the task store refuses writes). The fire is
// not rerun on this boot, but its interrupted evidence survives, so the next
// boot — with a writable store — still reruns it exactly once.
func TestTaskTriggerBoot_ClaimReleaseFailure_KeepsOwedFireForNextBoot(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "tasks")
	store := task.New(tasksDir)
	path := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()
	tsk := crashOnceFireWithClaim(t, store, path, clk)

	if err := os.Chmod(tasksDir, 0o500); err != nil {
		t.Fatalf("make task store read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(tasksDir, 0o700) })
	if err := os.WriteFile(filepath.Join(tasksDir, "write-probe"), nil, 0o600); err == nil {
		t.Skip("task store is still writable (running as root?); cannot inject the write failure")
	}

	rec2 := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(path, store, nil)
	s2.dispatch = rec2.dispatch
	s2.SetClock(clk)
	if err := s2.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("StartAfterPhysicalBoot: %v", err)
	}
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	s2.Stop()
	if calls := rec2.calls(); len(calls) != 0 {
		t.Fatalf("with the dead claim unreleased the fire cannot run on this boot; dispatches=%v", calls)
	}
	kept := cron.NewCronService(path)
	interrupted := false
	for _, job := range kept.ListJobs(true) {
		if job.Payload.Message == tsk.ID && cron.InterruptedOneTimeFire(&job) {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatalf("a failed claim release must keep the evidence that the once fire was interrupted; jobs=%+v",
			kept.ListJobs(true))
	}

	if err := os.Chmod(tasksDir, 0o700); err != nil {
		t.Fatalf("restore task store: %v", err)
	}
	rec3 := &dispatchRecorder{}
	s3 := NewTaskTriggerScheduler(path, store, nil)
	s3.dispatch = rec3.dispatch
	s3.SetClock(clk)
	if err := s3.StartAfterPhysicalBoot(); err != nil {
		t.Fatalf("next boot StartAfterPhysicalBoot: %v", err)
	}
	defer s3.Stop()
	if err := s3.Reconcile(); err != nil {
		t.Fatalf("next boot Reconcile: %v", err)
	}
	s3.RunDueJobs(clk.Now())
	s3.WaitForLane()
	if calls := rec3.calls(); len(calls) != 1 || calls[0] != tsk.ID {
		t.Fatalf("the next boot must still rerun the owed fire once; dispatches=%v", calls)
	}
}
