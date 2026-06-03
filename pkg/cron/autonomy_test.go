package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a deterministic, settable clock for tests (W-5 clock seam).
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

// recordingRunner captures every job it is asked to run.
type recordingRunner struct {
	mu      sync.Mutex
	jobs    []string
	result  string
	err     error
	hook    func(job *CronJob) (string, error)
	ctxHook func(ctx context.Context, job *CronJob) (string, error)
}

func (r *recordingRunner) RunScheduled(ctx context.Context, job *CronJob) (string, error) {
	r.mu.Lock()
	r.jobs = append(r.jobs, job.ID)
	r.mu.Unlock()
	if r.ctxHook != nil {
		return r.ctxHook(ctx, job)
	}
	if r.hook != nil {
		return r.hook(job)
	}
	return r.result, r.err
}

func (r *recordingRunner) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.jobs))
	copy(out, r.jobs)
	return out
}

func newAutonomyService(t *testing.T, clk Clock) (*CronService, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.json")
	cs := NewCronService(path)
	cs.SetClock(clk)
	return cs, path
}

// addDueJob inserts an enabled recurring job whose NextRun is in the past.
func addDueJob(t *testing.T, cs *CronService, owner string) *CronJob {
	t.Helper()
	job, err := cs.AddJob("due", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "do it", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	job.AgentID = owner
	if err := cs.UpdateJob(job); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	return job
}

// TestRunDueJobs_FiresDueJob verifies RunDueJobs fires a due job synchronously
// via the injected clock with zero wall-clock sleeps (W-5).
func TestRunDueJobs_FiresDueJob(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	runner := &recordingRunner{result: "sess-1"}
	cs.SetRunner(runner)

	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	job := addDueJob(t, cs, "mia")

	// Advance past the next-run boundary and fire.
	clk.Advance(2 * time.Minute)
	cs.RunDueJobs(clk.Now())
	cs.WaitForLane()

	calls := runner.calls()
	if len(calls) != 1 || calls[0] != job.ID {
		t.Fatalf("expected runner to fire job %s once, got %v", job.ID, calls)
	}

	st := jobState(t, cs, job.ID)
	if st.LastStatus != "ok" {
		t.Errorf("LastStatus = %q, want ok", st.LastStatus)
	}
	if len(st.History) != 1 || st.History[0].SessionID != "sess-1" {
		t.Errorf("history = %+v, want one record with sessionId sess-1", st.History)
	}
}

// TestRunDueJobs_OverlapSkip verifies the overlap guard (FR-008): a second fire
// while a run is in progress is skipped.
func TestRunDueJobs_OverlapSkip(t *testing.T) {
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

	// Deterministic skip signal (SC-010): rather than poll a wall-clock window
	// for a non-event, receive on this channel which fires exactly when the
	// overlap guard skips the second dispatch.
	skipped := make(chan string, 1)
	cs.SetOnSkip(func(jobID, reason string) {
		if reason == "overlap" {
			select {
			case skipped <- jobID:
			default:
			}
		}
	})

	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	job := addDueJob(t, cs, "mia")

	clk.Advance(2 * time.Minute)
	// First fire — blocks inside the runner.
	cs.RunDueJobs(clk.Now())
	<-entered // first run is now in-flight (Running=true)

	// Re-arm next-run and fire again while the first is still running. The
	// second dispatch acquires a lane slot, sees Running=true, and returns
	// without invoking the runner (overlap guard).
	reArm(t, cs, job.ID, clk.Now().UnixMilli()-1)
	cs.RunDueJobs(clk.Now())

	// Deterministically wait for the overlap skip to fire (no wall-clock poll).
	select {
	case got := <-skipped:
		if got != job.ID {
			t.Fatalf("overlap skip fired for job %s, want %s", got, job.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for overlap-guard skip signal")
	}

	// The runner must have been invoked exactly once: the first (still-blocked)
	// run, never the skipped second dispatch.
	if got := len(runner.calls()); got != 1 {
		t.Fatalf("overlap guard failed: runner called %d times, want 1", got)
	}

	close(release)
	cs.WaitForLane()
}

// TestRunDueJobs_ConcurrencyCapQueues verifies that with cap=2 and 4 due jobs,
// no more than 2 run concurrently and all eventually run (FR-007).
func TestRunDueJobs_ConcurrencyCapQueues(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	cs.SetMaxConcurrentRuns(2)

	release := make(chan struct{})
	var inFlight int32
	var peak int32
	runner := &recordingRunner{hook: func(job *CronJob) (string, error) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		<-release
		atomic.AddInt32(&inFlight, -1)
		return "", nil
	}}
	cs.SetRunner(runner)

	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	for range 4 {
		addDueJob(t, cs, "mia")
	}
	clk.Advance(2 * time.Minute)
	cs.RunDueJobs(clk.Now())

	// Give the two slots a moment to fill, then assert peak <= cap.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&inFlight) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt32(&peak); got > 2 {
		t.Fatalf("concurrency cap exceeded: peak=%d, cap=2", got)
	}

	close(release)
	cs.WaitForLane()

	if got := len(runner.calls()); got != 4 {
		t.Fatalf("expected all 4 jobs to run, got %d", got)
	}
}

// TestMigration_BackfillsOwner verifies owner-less jobs are backfilled with the
// supplied default agent id (W-8) and persisted.
func TestMigration_BackfillsOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	seedStore(t, path, []CronJob{{ID: "j1", Name: "old", Enabled: true}})

	cs := NewCronService(path)
	cs.SetDefaultAgentID("default-agent")

	jobs := cs.ListJobs(true)
	if len(jobs) != 1 || jobs[0].AgentID != "default-agent" {
		t.Fatalf("expected owner backfilled to default-agent, got %+v", jobs)
	}

	// Persisted on disk.
	cs2 := NewCronService(path)
	if got := cs2.ListJobs(true)[0].AgentID; got != "default-agent" {
		t.Fatalf("backfill not persisted: owner = %q", got)
	}
}

// TestMigration_NilDefaultSkipsFire verifies that with no default agent, an
// owner-less job is left empty AND is not fired by the lane (W-8).
func TestMigration_NilDefaultSkipsFire(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	runner := &recordingRunner{}
	cs.SetRunner(runner)

	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	// Add an owner-less due job (no SetDefaultAgentID).
	job, err := cs.AddJob("orphan", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "x", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	if job.AgentID != "" {
		t.Fatalf("expected empty owner, got %q", job.AgentID)
	}

	clk.Advance(2 * time.Minute)
	cs.RunDueJobs(clk.Now())
	cs.WaitForLane()

	if got := len(runner.calls()); got != 0 {
		t.Fatalf("owner-less job should be skipped, runner called %d times", got)
	}
}

// TestRunOutcome_ErrorAndConsecutiveFailures verifies error recording, the
// ConsecutiveFailures counter, and that a success resets it (FR-012).
func TestRunOutcome_ErrorAndConsecutiveFailures(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)

	var fail atomic.Bool
	fail.Store(true)
	runner := &recordingRunner{hook: func(job *CronJob) (string, error) {
		if fail.Load() {
			return "", fmt.Errorf("boom")
		}
		return "sess-ok", nil
	}}
	cs.SetRunner(runner)

	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	job := addDueJob(t, cs, "mia")

	fireOnce := func() {
		clk.Advance(2 * time.Minute)
		reArm(t, cs, job.ID, clk.Now().UnixMilli()-1)
		cs.RunDueJobs(clk.Now())
		cs.WaitForLane()
	}

	fireOnce()
	fireOnce()
	st := jobState(t, cs, job.ID)
	if st.LastStatus != "error" || st.LastError != "boom" {
		t.Fatalf("expected error status/text, got %q / %q", st.LastStatus, st.LastError)
	}
	if st.ConsecutiveFailures != 2 {
		t.Fatalf("ConsecutiveFailures = %d, want 2", st.ConsecutiveFailures)
	}

	// Now succeed → counter resets.
	fail.Store(false)
	fireOnce()
	st = jobState(t, cs, job.ID)
	if st.LastStatus != "ok" || st.ConsecutiveFailures != 0 {
		t.Fatalf("after success: status=%q failures=%d, want ok/0", st.LastStatus, st.ConsecutiveFailures)
	}
}

// TestRunOutcome_HistoryCappedAt20 verifies the inline run history is capped at
// the last 20 records (Ambiguity #5).
func TestRunOutcome_HistoryCappedAt20(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	runner := &recordingRunner{result: ""}
	cs.SetRunner(runner)

	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	job := addDueJob(t, cs, "mia")

	for i := range 25 {
		clk.Advance(2 * time.Minute)
		reArm(t, cs, job.ID, clk.Now().UnixMilli()-1)
		cs.RunDueJobs(clk.Now())
		cs.WaitForLane()
		_ = i
	}

	st := jobState(t, cs, job.ID)
	if len(st.History) != runHistoryCap {
		t.Fatalf("history len = %d, want %d", len(st.History), runHistoryCap)
	}
}

// TestStop_DrainsInFlightRuns verifies Stop() blocks on an in-flight run and
// cancels the lane context (W-3).
func TestStop_DrainsInFlightRuns(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)

	entered := make(chan struct{})
	finished := make(chan struct{})
	runner := &recordingRunner{ctxHook: func(ctx context.Context, job *CronJob) (string, error) {
		close(entered)
		<-ctx.Done() // wait for cancellation by Stop()
		close(finished)
		return "", context.Canceled
	}}
	cs.SetRunner(runner)

	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	addDueJob(t, cs, "mia")
	clk.Advance(2 * time.Minute)
	cs.RunDueJobs(clk.Now())
	<-entered

	done := make(chan struct{})
	go func() { cs.Stop(); close(done) }()

	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("lane context was not canceled by Stop()")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() did not return after draining")
	}
}

// --- helpers ---

func jobState(t *testing.T, cs *CronService, id string) CronJobState {
	t.Helper()
	for _, j := range cs.ListJobs(true) {
		if j.ID == id {
			return j.State
		}
	}
	t.Fatalf("job %s not found", id)
	return CronJobState{}
}

// reArm sets a job's NextRunAtMS so it becomes due again on the next RunDueJobs.
func reArm(t *testing.T, cs *CronService, id string, ms int64) {
	t.Helper()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == id {
			cs.store.Jobs[i].State.NextRunAtMS = &ms
			cs.store.Jobs[i].Enabled = true
			return
		}
	}
	t.Fatalf("reArm: job %s not found", id)
}

func seedStore(t *testing.T, path string, jobs []CronJob) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	st := CronStore{Version: 1, Jobs: jobs}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRunNow_DispatchesThroughLane verifies RunNow fires a single job
// synchronously through the lane and returns the run outcome (#264).
func TestRunNow_DispatchesThroughLane(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	runner := &recordingRunner{result: "sess-rn"}
	cs.SetRunner(runner)
	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	job := addDueJob(t, cs, "mia")

	status, sessionID, err := cs.RunNow(job.ID)
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if status != "ok" {
		t.Fatalf("status = %q, want ok", status)
	}
	if sessionID != "sess-rn" {
		t.Fatalf("sessionID = %q, want sess-rn", sessionID)
	}
	if calls := runner.calls(); len(calls) != 1 || calls[0] != job.ID {
		t.Fatalf("runner calls = %v, want [%s]", calls, job.ID)
	}
}

// TestRunNow_PropagatesError verifies a failed run is returned as status=error.
func TestRunNow_PropagatesError(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	runner := &recordingRunner{err: fmt.Errorf("kaboom")}
	cs.SetRunner(runner)
	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	job := addDueJob(t, cs, "mia")
	status, _, err := cs.RunNow(job.ID)
	if status != "error" {
		t.Fatalf("status = %q, want error", status)
	}
	if err == nil {
		t.Fatal("expected error from RunNow")
	}
}

// TestRunNow_SaturatedHistoryStillReportsError verifies the run-vs-skip
// classification is by record identity (RanAtMs), not history length. With a
// saturated history (runHistoryCap records already present), executeJobByID
// trims the slice back to runHistoryCap after appending the new record, so the
// length is unchanged across a genuine run. A length-only check would
// misclassify a real failure as "skipped" and HIDE it from the run-now
// endpoint; the identity check must still surface "error".
func TestRunNow_SaturatedHistoryStillReportsError(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	runner := &recordingRunner{err: fmt.Errorf("kaboom")}
	cs.SetRunner(runner)
	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	job := addDueJob(t, cs, "mia")

	// Pre-seed exactly runHistoryCap prior run records so the next run trims
	// back to the same length (len stays == runHistoryCap).
	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			h := make([]CronRunRecord, runHistoryCap)
			for k := range h {
				h[k] = CronRunRecord{RanAtMs: int64(1000 + k), Status: "ok", SessionID: "old"}
			}
			cs.store.Jobs[i].State.History = h
		}
	}
	cs.mu.Unlock()

	before := jobState(t, cs, job.ID)
	if len(before.History) != runHistoryCap {
		t.Fatalf("precondition: history len = %d, want %d", len(before.History), runHistoryCap)
	}

	status, _, err := cs.RunNow(job.ID)
	if status != "error" {
		t.Fatalf("status = %q, want error (saturated history must not mask a real run)", status)
	}
	if err == nil {
		t.Fatal("expected error from RunNow on a failed run with saturated history")
	}

	// History stayed capped, but the most-recent record is the NEW failed run.
	after := jobState(t, cs, job.ID)
	if len(after.History) != runHistoryCap {
		t.Fatalf("history len = %d, want %d (cap preserved)", len(after.History), runHistoryCap)
	}
	last := after.History[len(after.History)-1]
	if last.Status != "error" {
		t.Fatalf("last record status = %q, want error", last.Status)
	}
}

// TestRunNow_NotFound returns an error for an unknown id.
func TestRunNow_NotFound(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	cs.SetRunner(&recordingRunner{result: "x"})
	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	if _, _, err := cs.RunNow("missing"); err == nil {
		t.Fatal("expected error for missing job")
	}
}
