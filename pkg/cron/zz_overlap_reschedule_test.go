package cron

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestRecurringSurvivesOverlapSkip verifies a recurring job that is skipped by
// the overlap guard (FR-008) still recomputes its next fire, so it is not lost
// (BLOCKER 1). RunDueJobs clears NextRunAtMS before dispatch; the skip path must
// restore it via computeNextRun.
func TestRecurringSurvivesOverlapSkip(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	runner := &recordingRunner{hook: func(job *CronJob) (string, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return "", nil
	}}
	cs.SetRunner(runner)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	job := addDueJob(t, cs, "mia")
	clk.Advance(2 * time.Minute)

	// First fire blocks inside the runner (Running=true).
	cs.RunDueJobs(clk.Now())
	<-entered

	// Re-arm + fire again while the first run is in flight → overlap skip. The
	// skip path must re-compute NextRunAtMS for the recurring job.
	reArm(t, cs, job.ID, clk.Now().UnixMilli()-1)
	cs.RunDueJobs(clk.Now())

	// The second (skipped) dispatch returns immediately after recomputing the
	// next-run; poll the state until it is non-nil (returns as soon as it holds).
	// Use GetJob (copies under the RLock) so the poll never races the lane writer.
	waitFor(t, func() bool {
		j, ok := cs.GetJob(job.ID)
		return ok && j.State.NextRunAtMS != nil
	})

	if j, ok := cs.GetJob(job.ID); !ok || j.State.NextRunAtMS == nil {
		t.Fatalf("recurring job lost its schedule after overlap-skip (NextRunAtMS=nil)")
	}

	close(release)
	cs.WaitForLane()
}

// TestRecurringSurvivesOwnerMissingSkip verifies the owner-missing skip path
// (W-8) also recomputes NextRunAtMS for a recurring job (BLOCKER 1).
func TestRecurringSurvivesOwnerMissingSkip(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	runner := &recordingRunner{}
	cs.SetRunner(runner)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	// Owner-less recurring job (no SetDefaultAgentID) — the runner-wired lane
	// skips it because AgentID == "".
	job, err := cs.AddJob("orphan", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "x", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	clk.Advance(2 * time.Minute)
	reArm(t, cs, job.ID, clk.Now().UnixMilli()-1)
	cs.RunDueJobs(clk.Now())
	cs.WaitForLane()

	if got := len(runner.calls()); got != 0 {
		t.Fatalf("owner-less job should be skipped, runner called %d times", got)
	}
	if st := jobState(t, cs, job.ID); st.NextRunAtMS == nil {
		t.Fatalf("recurring owner-less job lost its schedule after skip (NextRunAtMS=nil)")
	}
}

// TestPanicInRunRecordedAsError verifies a panic in a scheduled run becomes a
// recorded error run, Running is cleared, and the recurring next-run survives
// (BLOCKER 2). No gateway crash.
func TestPanicInRunRecordedAsError(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	runner := &recordingRunner{hook: func(job *CronJob) (string, error) {
		panic("kaboom in scheduled run")
	}}
	cs.SetRunner(runner)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	job := addDueJob(t, cs, "mia")
	clk.Advance(2 * time.Minute)
	cs.RunDueJobs(clk.Now())
	cs.WaitForLane()

	st := jobState(t, cs, job.ID)
	if st.LastStatus != "error" {
		t.Fatalf("panic run: LastStatus = %q, want error", st.LastStatus)
	}
	if len(st.History) != 1 || st.History[0].Status != "error" {
		t.Fatalf("panic run: expected one error history record, got %+v", st.History)
	}
	if st.Running {
		t.Fatalf("panic run: Running must be cleared, still true")
	}
	if st.NextRunAtMS == nil {
		t.Fatalf("panic run: recurring next-run must survive, NextRunAtMS=nil")
	}
}

// TestBoundedStopReturnsOnTimeout verifies Stop() returns within its drain
// timeout even when a run is stuck and ignores cancellation (BLOCKER 6).
func TestBoundedStopReturnsOnTimeout(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	cs.SetStopDrainTimeout(150 * time.Millisecond)

	entered := make(chan struct{})
	stuck := make(chan struct{})
	runner := &recordingRunner{ctxHook: func(ctx context.Context, job *CronJob) (string, error) {
		close(entered)
		<-stuck // never released → ignores cancellation
		return "", nil
	}}
	cs.SetRunner(runner)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	addDueJob(t, cs, "mia")
	clk.Advance(2 * time.Minute)
	cs.RunDueJobs(clk.Now())
	<-entered

	done := make(chan struct{})
	go func() { cs.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within bounded drain timeout for a stuck run")
	}
	close(stuck) // let the stuck goroutine exit so the test is clean
	cs.WaitForLane()
}

// TestTimeoutClassifiedAsTimeoutStatus verifies a context.DeadlineExceeded run
// is recorded with Status "timeout" (not "error"), so the contract enum is live.
func TestTimeoutClassifiedAsTimeoutStatus(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	runner := &recordingRunner{ctxHook: func(ctx context.Context, job *CronJob) (string, error) {
		return "", context.DeadlineExceeded
	}}
	cs.SetRunner(runner)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	job := addDueJob(t, cs, "mia")
	clk.Advance(2 * time.Minute)

	status, _, err := cs.RunNow(job.ID)
	if status != "timeout" {
		t.Fatalf("RunNow status = %q, want timeout", status)
	}
	if err == nil {
		t.Fatal("expected a non-nil error for a timed-out run")
	}
	if st := jobState(t, cs, job.ID); st.LastStatus != "timeout" {
		t.Fatalf("LastStatus = %q, want timeout", st.LastStatus)
	}
}

// TestTransientRetryBackoffAndReset verifies FR-010: transient errors schedule
// the next fire at backoff offsets [60s,120s,300s] by RetryAttempt, and a
// success resets RetryAttempt to 0.
func TestTransientRetryBackoffAndReset(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)

	var transient atomic.Bool
	transient.Store(true)
	runner := &recordingRunner{hook: func(job *CronJob) (string, error) {
		if transient.Load() {
			return "", &TransientError{Err: errors.New("rate limited")}
		}
		return "sess-ok", nil
	}}
	cs.SetRunner(runner)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	job := addDueJob(t, cs, "mia")
	backoff := []int64{60000, 120000, 300000}

	fire := func() {
		clk.Advance(2 * time.Minute)
		reArm(t, cs, job.ID, clk.Now().UnixMilli()-1)
		cs.RunDueJobs(clk.Now())
		cs.WaitForLane()
	}

	for attempt := 0; attempt < len(backoff); attempt++ {
		fire()
		st := jobState(t, cs, job.ID)
		if st.LastStatus != "error" {
			t.Fatalf("attempt %d: LastStatus = %q, want error", attempt, st.LastStatus)
		}
		if st.RetryAttempt != attempt+1 {
			t.Fatalf("attempt %d: RetryAttempt = %d, want %d", attempt, st.RetryAttempt, attempt+1)
		}
		if st.NextRunAtMS == nil {
			t.Fatalf("attempt %d: NextRunAtMS nil", attempt)
		}
		// The next-run is computed inside the run using the (post-fire) injected
		// clock: now + backoff[attempt].
		wantNext := clk.Now().UnixMilli() + backoff[attempt]
		if *st.NextRunAtMS != wantNext {
			t.Fatalf("attempt %d: NextRunAtMS = %d, want now+%d (=%d)",
				attempt, *st.NextRunAtMS, backoff[attempt], wantNext)
		}
	}

	// One more transient failure after the schedule is exhausted → resumes normal
	// cadence and resets RetryAttempt.
	fire()
	st := jobState(t, cs, job.ID)
	if st.RetryAttempt != 0 {
		t.Fatalf("after exhaustion: RetryAttempt = %d, want 0 (normal cadence)", st.RetryAttempt)
	}

	// A subsequent success resets the counters.
	transient.Store(false)
	fire()
	st = jobState(t, cs, job.ID)
	if st.LastStatus != "ok" || st.RetryAttempt != 0 || st.ConsecutiveFailures != 0 {
		t.Fatalf("after success: status=%q retry=%d failures=%d, want ok/0/0",
			st.LastStatus, st.RetryAttempt, st.ConsecutiveFailures)
	}
}

// waitFor polls cond up to a short bounded deadline; it returns as soon as cond
// holds (no fixed sleep for the assertion).
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
