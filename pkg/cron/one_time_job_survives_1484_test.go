package cron

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Issue #1484 (b). Requirement (oracle, from the issue): a one-time job is
// not dropped before its run's outcome is saved; a process that dies between
// the fire and that save must leave the job runnable.
//
// Boundary (docs/calendar.md, "a missed run is skipped, never replayed"): a
// one-time fire whose time passed while the process was NOT running never
// started, and stays skipped. The tests hold both sides of that line.

// bootNoLoop starts cs like a fresh process would (physicalBoot=true clears
// the dead process's Running flags) without the real-time runLoop, so the
// test drives RunDueJobs with its fake clock.
func bootNoLoop(t *testing.T, cs *CronService, physicalBoot bool) {
	t.Helper()
	cs.mu.Lock()
	_, err := cs.initRunStateUnsafe(physicalBoot)
	cs.mu.Unlock()
	if err != nil {
		t.Fatalf("initRunStateUnsafe: %v", err)
	}
}

func TestOneTimeJob1484_RunInterruptedByCrash_StaysRunnable(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 10, 22, 6, 59, 0, 0, time.UTC))
	cs, path := newAutonomyService(t, clk)
	due := clk.Now().Add(time.Minute).UnixMilli()
	job, err := cs.AddJobFull(JobSpec{Name: "routine", Message: "brief", AgentID: "owner",
		Schedule: CronSchedule{Kind: "at", AtMS: &due}})
	if err != nil {
		t.Fatalf("AddJobFull: %v", err)
	}

	// The run starts and is still going when the process "dies": capture the
	// store file exactly as the crash would leave it on disk.
	entered := make(chan struct{})
	release := make(chan struct{})
	var crashImage []byte
	cs.SetRunner(&recordingRunner{hook: func(*CronJob) (string, error) {
		close(entered)
		<-release
		return "", nil
	}})
	bootNoLoop(t, cs, false)
	clk.Advance(2 * time.Minute)
	cs.RunDueJobs(clk.Now())
	<-entered
	crashImage, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read crash image: %v", err)
	}
	close(release)
	cs.WaitForLane()
	cs.Stop()

	// A new process starts from the crash image.
	restartPath := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.WriteFile(restartPath, crashImage, 0o600); err != nil {
		t.Fatal(err)
	}
	clk.Advance(3 * time.Hour)
	restarted := NewCronService(restartPath)
	restarted.SetClock(clk)
	rec := &recordingRunner{}
	restarted.SetRunner(rec)
	bootNoLoop(t, restarted, true)
	defer restarted.Stop()

	restarted.RunDueJobs(clk.Now())
	restarted.WaitForLane()
	restarted.RunDueJobs(clk.Now()) // the one run is not repeated
	restarted.WaitForLane()

	if got := rec.calls(); len(got) != 1 || got[0] != job.ID {
		t.Fatalf("the interrupted one-time job must run exactly once after restart; runner calls = %v", got)
	}
	if _, ok := restarted.GetJob(job.ID); ok {
		t.Errorf("after its completed run the one-time job must be removed")
	}
}

// Boundary: a one-time fire missed while the process was down never started,
// so it stays skipped — at the first restart and at every later one.
func TestOneTimeJob1484_MissedWhileDown_StaysSkippedAcrossRestarts(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC))
	cs, path := newAutonomyService(t, clk)
	due := clk.Now().Add(time.Hour).UnixMilli() // 07:00
	if _, err := cs.AddJobFull(JobSpec{Name: "routine", Message: "brief", AgentID: "owner",
		Schedule: CronSchedule{Kind: "at", AtMS: &due}}); err != nil {
		t.Fatalf("AddJobFull: %v", err)
	}
	bootNoLoop(t, cs, false)
	cs.Stop() // the process stops before 07:00

	for _, restartAt := range []time.Duration{4*time.Hour + 19*time.Minute, time.Hour} {
		clk.Advance(restartAt)
		restarted := NewCronService(path)
		restarted.SetClock(clk)
		rec := &recordingRunner{}
		restarted.SetRunner(rec)
		bootNoLoop(t, restarted, true)
		restarted.RunDueJobs(clk.Now())
		restarted.WaitForLane()
		restarted.Stop()
		if calls := rec.calls(); len(calls) != 0 {
			t.Fatalf("a one-time fire missed while down must stay skipped (docs/calendar.md); runner calls = %v", calls)
		}
	}
}

// A one-time job whose run COMPLETED is never re-run by a restart.
func TestOneTimeJob1484_CompletedRunNotRepeatedAfterRestart(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 10, 22, 6, 59, 0, 0, time.UTC))
	cs, path := newAutonomyService(t, clk)
	due := clk.Now().Add(time.Minute).UnixMilli()
	enabled := true
	job, err := cs.AddJobFull(JobSpec{Name: "keep", Message: "m", AgentID: "owner", Enabled: &enabled,
		Schedule: CronSchedule{Kind: "at", AtMS: &due}})
	if err != nil {
		t.Fatalf("AddJobFull: %v", err)
	}
	// Keep the job after its run (DeleteAfterRun false) so a restart sees it.
	job.DeleteAfterRun = false
	if err := cs.UpdateJob(job); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	cs.SetRunner(&recordingRunner{})
	bootNoLoop(t, cs, false)
	clk.Advance(2 * time.Minute)
	cs.RunDueJobs(clk.Now())
	cs.WaitForLane()
	cs.Stop()

	clk.Advance(time.Hour)
	restarted := NewCronService(path)
	restarted.SetClock(clk)
	rec := &recordingRunner{}
	restarted.SetRunner(rec)
	bootNoLoop(t, restarted, true)
	defer restarted.Stop()
	restarted.RunDueJobs(clk.Now())
	restarted.WaitForLane()
	if calls := rec.calls(); len(calls) != 0 {
		t.Fatalf("a one-time job whose run completed must not run again after restart; calls = %v", calls)
	}
}
