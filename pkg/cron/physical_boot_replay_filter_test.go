package cron

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 6b review I3: a derived-job scheduler (the task-trigger scheduler) must be
// able to decline the replay of an interrupted one-time job — its recurring
// occurrences are stored as at-jobs and must never be replayed — before the
// run loop can fire it. Genuine once jobs the filter accepts still replay.

// crashTwoAtJobs builds a crash image holding two interrupted at-jobs (both
// Running, outcome unsaved) and returns the restart path and the job IDs.
func crashTwoAtJobs(t *testing.T, clk *fakeClock) (string, string, string) {
	t.Helper()
	cs, path := newAutonomyService(t, clk)
	due := clk.Now().Add(time.Minute).UnixMilli()
	keep, err := cs.AddJobFull(JobSpec{Name: "once", Message: "m", AgentID: "owner",
		Schedule: CronSchedule{Kind: "at", AtMS: &due}})
	if err != nil {
		t.Fatalf("AddJobFull once: %v", err)
	}
	drop, err := cs.AddJobFull(JobSpec{Name: "occurrence", Message: "m", AgentID: "owner",
		Schedule: CronSchedule{Kind: "at", AtMS: &due}})
	if err != nil {
		t.Fatalf("AddJobFull occurrence: %v", err)
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	cs.SetRunner(&recordingRunner{hook: func(*CronJob) (string, error) {
		entered <- struct{}{}
		<-release
		return "", nil
	}})
	bootNoLoop(t, cs, false)
	clk.Advance(2 * time.Minute)
	cs.RunDueJobs(clk.Now())
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("both runs must start to build the crash image")
		}
	}
	image, err := os.ReadFile(path)
	close(release)
	cs.WaitForLane()
	cs.Stop()
	if err != nil {
		t.Fatalf("read crash image: %v", err)
	}
	restartPath := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.WriteFile(restartPath, image, 0o600); err != nil {
		t.Fatal(err)
	}
	return restartPath, keep.ID, drop.ID
}

func TestPhysicalBootReplayFilter_DeclinedJobNeverFires(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 10, 22, 6, 59, 0, 0, time.UTC))
	restartPath, keepID, dropID := crashTwoAtJobs(t, clk)

	restarted := NewCronService(restartPath)
	restarted.SetClock(clk)
	rec := &recordingRunner{}
	restarted.SetRunner(rec)
	restarted.mu.Lock()
	_, err := restarted.initRunStateReplayingUnsafe(true, runOnly(keepID))
	restarted.mu.Unlock()
	if err != nil {
		t.Fatalf("initRunStateReplayingUnsafe: %v", err)
	}
	defer restarted.Stop()

	restarted.RunDueJobs(clk.Now())
	restarted.WaitForLane()
	if got := rec.calls(); len(got) != 1 || got[0] != keepID {
		t.Fatalf("only the accepted interrupted job replays; runner calls = %v", got)
	}
	dropped, ok := restarted.GetJob(dropID)
	if !ok {
		t.Fatalf("a declined job is left in the store for its owner to reconcile")
	}
	if dropped.State.Running || dropped.State.NextRunAtMS != nil {
		t.Fatalf("a declined job must lose its dead Running flag and stay unarmed; state = %+v", dropped.State)
	}
}

// The public entry point applies the filter before its run loop starts.
func TestPhysicalBootReplayFilter_PublicStartDeclinesBeforeLoop(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 10, 22, 6, 59, 0, 0, time.UTC))
	restartPath, keepID, dropID := crashTwoAtJobs(t, clk)

	restarted := NewCronService(restartPath)
	restarted.SetClock(clk)
	rec := &recordingRunner{}
	restarted.SetRunner(rec)
	if err := restarted.StartAfterPhysicalBootReplaying(runOnly(keepID)); err != nil {
		t.Fatalf("StartAfterPhysicalBootReplaying: %v", err)
	}
	restarted.RunDueJobs(clk.Now())
	restarted.WaitForLane()
	restarted.Stop()
	for _, id := range rec.calls() {
		if id == dropID {
			t.Fatalf("a declined interrupted job must never fire; runner calls = %v", rec.calls())
		}
	}
	if err := restarted.StartAfterPhysicalBootReplaying(nil); err == nil {
		t.Fatalf("a second physical-boot window on one instance must be refused")
	}
}

// runOnly replays the job with the given ID and declines every other one.
func runOnly(id string) func(*CronJob) BootReplay {
	return func(job *CronJob) BootReplay {
		if job.ID == id {
			return BootReplayRun
		}
		return BootReplayDecline
	}
}

// 6b re-review N3: a kept job stays interrupted evidence — Running persisted,
// never fired in this process — so the next physical boot can still owe it.
func TestPhysicalBootReplayFilter_KeptJobStaysInterruptedForNextBoot(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 10, 22, 6, 59, 0, 0, time.UTC))
	restartPath, keepID, dropID := crashTwoAtJobs(t, clk)

	restarted := NewCronService(restartPath)
	restarted.SetClock(clk)
	rec := &recordingRunner{}
	restarted.SetRunner(rec)
	restarted.mu.Lock()
	_, err := restarted.initRunStateReplayingUnsafe(true, func(job *CronJob) BootReplay {
		if job.ID == keepID {
			return BootReplayKeep
		}
		return BootReplayDecline
	})
	restarted.mu.Unlock()
	if err != nil {
		t.Fatalf("initRunStateReplayingUnsafe: %v", err)
	}
	restarted.RunDueJobs(clk.Now())
	restarted.WaitForLane()
	restarted.Stop()
	if got := rec.calls(); len(got) != 0 {
		t.Fatalf("neither a kept nor a declined job fires; runner calls = %v", got)
	}

	next := NewCronService(restartPath)
	kept, ok := next.GetJob(keepID)
	if !ok || !InterruptedOneTimeFire(&kept) {
		t.Fatalf("a kept job must still be an interrupted fire on disk for the next boot; job=%+v ok=%v", kept, ok)
	}
	dropped, ok := next.GetJob(dropID)
	if !ok || InterruptedOneTimeFire(&dropped) {
		t.Fatalf("a declined job loses its interrupted evidence; job=%+v ok=%v", dropped, ok)
	}
}
