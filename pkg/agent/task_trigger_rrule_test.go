// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// -----------------------------------------------------------------------------
// Test helpers (RRULE-specific)
// -----------------------------------------------------------------------------

// strP returns a pointer to v.
func strP(v string) *string { return &v }

// oneDayMs is 24h expressed in milliseconds, used to assert a daily RRULE's
// next occurrence lands exactly one day after the previous one.
const oneDayMs = int64(24 * 60 * 60 * 1000)

// secondAligned truncates t to whole-second precision and returns Unix
// milliseconds. RRULE (RFC 5545) has no sub-second precision — the
// expansion library normalizes to whole seconds — so tests anchor DTSTART on
// a second boundary to keep exact-equality assertions meaningful instead of
// tripping over that normalization.
func secondAligned(t time.Time) int64 {
	return t.Truncate(time.Second).UnixMilli()
}

// derefI64 dereferences a possibly-nil *int64 for diagnostic printing
// (avoids printing a raw pointer address in test failure messages).
func derefI64(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

// jobsForTask returns every cron job currently registered whose payload
// carries taskID (normally 0 or 1 — the scheduler tracks a single job per
// task — but tests use it to assert that invariant directly).
func jobsForTask(sched *TaskTriggerScheduler, taskID string) []cron.CronJob {
	var out []cron.CronJob
	for _, j := range sched.cs.ListJobs(true) {
		if j.Payload.Message == taskID {
			out = append(out, j)
		}
	}
	return out
}

// makeRruleTask creates a `recurring` RRULE task via the shared makeTask
// helper (task_trigger_test.go).
func makeRruleTask(t *testing.T, store *task.Store, agentID, rruleBody string, dtstartMs int64, tz string) *task.Task {
	t.Helper()
	return makeTask(t, store, agentID, &task.Trigger{
		Type: task.TriggerRecurring,
		Config: task.TriggerConfig{
			Rrule:     strP(rruleBody),
			DtstartMs: int64P(dtstartMs),
			Tz:        strP(tz),
		},
	})
}

// -----------------------------------------------------------------------------
// Test 9: TestTriggerScheduler_RruleRearmAllPaths
// -----------------------------------------------------------------------------

// TestTriggerScheduler_RruleRearmAllPaths covers Scheduler rules 2, 3, 5, 6:
// every readable-task RunScheduled exit re-arms the next RRULE occurrence
// (success, overlap-skip, dispatch-error, SpawnReset-other-error); the
// task-unreadable exit cannot re-arm and instead relies on the recovery
// sweep; a series retires silently at exhaustion; and the sweep re-arms both
// true-orphan job shapes while leaving a genuinely in-flight (false-orphan)
// fire untouched.
func TestTriggerScheduler_RruleRearmAllPaths(t *testing.T) {
	t.Run("ReArmOnSuccess", func(t *testing.T) {
		sched, store, clk, rec := newTriggerSchedulerForTest(t)

		dtstart := secondAligned(clk.Now().Add(time.Minute))
		tsk := makeRruleTask(t, store, "agent-rearm-success", "FREQ=DAILY", dtstart, "UTC")
		sched.OnTaskUpserted(tsk)

		clk.Advance(2 * time.Minute)
		sched.RunDueJobs(clk.Now())
		sched.WaitForLane()

		if n := len(rec.calls()); n != 1 {
			t.Fatalf("expected 1 dispatch, got %d", n)
		}

		jobs := jobsForTask(sched, tsk.ID)
		if len(jobs) != 1 {
			t.Fatalf("expected exactly 1 armed job after success re-arm, got %d", len(jobs))
		}
		wantNext := dtstart + oneDayMs
		if jobs[0].Schedule.Kind != "at" || jobs[0].Schedule.AtMS == nil || *jobs[0].Schedule.AtMS != wantNext {
			t.Fatalf("re-armed job schedule = %+v, want Kind=at AtMS=%d", jobs[0].Schedule, wantNext)
		}
	})

	t.Run("ReArmOnOverlapSkip", func(t *testing.T) {
		sched, store, clk, rec := newTriggerSchedulerForTest(t)

		dtstart := secondAligned(clk.Now().Add(time.Minute))
		tsk := makeRruleTask(t, store, "agent-rearm-overlap", "FREQ=DAILY", dtstart, "UTC")
		sched.OnTaskUpserted(tsk)

		// Force the overlap guard: SpawnReset returns ErrAlreadyRunning while the
		// task is in_progress (mirrors TestTriggerOverlapGuard's technique).
		inProgress := task.StatusInProgress
		if _, updateErr := store.Update(tsk.ID, task.Patch{Status: &inProgress}); updateErr != nil {
			t.Fatalf("store.Update to in_progress: %v", updateErr)
		}

		clk.Advance(2 * time.Minute)
		sched.RunDueJobs(clk.Now())
		sched.WaitForLane()

		if n := len(rec.calls()); n != 0 {
			t.Fatalf("overlap guard: expected 0 dispatches, got %d", n)
		}

		jobs := jobsForTask(sched, tsk.ID)
		if len(jobs) != 1 {
			t.Fatalf("expected exactly 1 armed job after overlap-skip re-arm, got %d", len(jobs))
		}
		wantNext := dtstart + oneDayMs
		if jobs[0].Schedule.AtMS == nil || *jobs[0].Schedule.AtMS != wantNext {
			t.Fatalf("re-armed job AtMS = %v, want %d", derefI64(jobs[0].Schedule.AtMS), wantNext)
		}
	})

	t.Run("ReArmOnDispatchErrorNoBackoff", func(t *testing.T) {
		sched, store, clk, rec := newTriggerSchedulerForTest(t)

		dtstart := secondAligned(clk.Now().Add(time.Minute))
		tsk := makeRruleTask(t, store, "agent-rearm-dispatcherr", "FREQ=DAILY", dtstart, "UTC")
		sched.OnTaskUpserted(tsk)

		// Dispatch fails every time — the fire must still be treated as a
		// success by the cron engine (RunScheduled returns nil, no retry
		// backoff): the re-armed job's AtMS is the natural next occurrence, not
		// shifted by any backoff offset.
		sched.dispatch = func(ctx context.Context, taskID string, occurrenceMs *int64) error {
			_ = rec.dispatch(ctx, taskID, occurrenceMs)
			return errors.New("boom: simulated dispatch failure")
		}

		jobsBefore := jobsForTask(sched, tsk.ID)
		if len(jobsBefore) != 1 {
			t.Fatalf("setup: expected 1 initial job, got %d", len(jobsBefore))
		}

		clk.Advance(2 * time.Minute)

		// Sev 7 (Test-9 falsifiability): call RunScheduled directly and assert
		// its return value. Driving this only through RunDueJobs/WaitForLane
		// (as a prior version of this test did) never observes RunScheduled's
		// return at all — the fired job is removed and replaced by
		// replaceJobLocked's own remove-then-add regardless of what
		// RunScheduled returns, so an assertion phrased only in terms of
		// dispatch count and re-armed schedule would pass even if RunScheduled
		// wrongly surfaced an error here.
		msg, err := sched.RunScheduled(context.Background(), &jobsBefore[0])
		if err != nil {
			t.Fatalf("RunScheduled must return a nil error on an rrule dispatch failure "+
				"(Scheduler rule 3: the re-arm IS the retry, no backoff), got %v", err)
		}
		if msg != "" {
			t.Fatalf("RunScheduled returned message %q, want empty", msg)
		}

		if n := len(rec.calls()); n != 1 {
			t.Fatalf("expected dispatch to have been attempted once, got %d calls", n)
		}

		jobs := jobsForTask(sched, tsk.ID)
		if len(jobs) != 1 {
			t.Fatalf("expected exactly 1 armed job after dispatch-error re-arm, got %d", len(jobs))
		}
		wantNext := dtstart + oneDayMs
		if jobs[0].Schedule.AtMS == nil || *jobs[0].Schedule.AtMS != wantNext {
			t.Fatalf("re-armed job AtMS = %v, want %d (natural next occurrence, no backoff shift)",
				derefI64(jobs[0].Schedule.AtMS), wantNext)
		}
	})

	t.Run("ReArmOnSpawnResetOtherError", func(t *testing.T) {
		sched, store, clk, rec := newTriggerSchedulerForTest(t)

		dtstart := secondAligned(clk.Now().Add(time.Minute))
		tsk := makeRruleTask(t, store, "agent-rearm-spawnreset-err", "FREQ=DAILY", dtstart, "UTC")
		sched.OnTaskUpserted(tsk)

		// Force a generic (non-ErrNotFound, non-ErrAlreadyRunning) SpawnReset
		// failure deterministically and portably, via the SAME
		// spawnResetTestHook seam TestTriggerScheduler_SpawnResetTaskNotFound_NoReArm
		// (F5) uses: it fires immediately after RunScheduled's own store.Get has
		// already succeeded (t below is that valid in-memory snapshot) but
		// immediately before SpawnReset's own read.
		//
		// A prior version of this subtest chmod'd the task store directory to
		// 0o500 to make SpawnReset's write fail with EACCES. That is NOT
		// portable: a process running as root (as this repo's ci-omnipus CI
		// worker does — deploy/ci-worker/Dockerfile is `FROM golang:1.26-bookworm`
		// with no USER directive, so the container's default user is root)
		// bypasses directory-write permission checks entirely (CAP_DAC_OVERRIDE),
		// so the chmod silently failed to block the write — SpawnReset then
		// actually SUCCEEDED, dispatch was reached, and this assertion failed
		// deterministically on CI (reproduced 5/5 locally via `sudo`, vs. 0
		// failures in 250+ non-root runs, including heavy synthetic CPU
		// contention) — a root-vs-non-root environment difference, not a race.
		//
		// Swapping the task's on-disk file for a directory of the same name
		// sidesteps privilege entirely: os.ReadFile on a directory returns
		// EISDIR unconditionally, for root and non-root alike, so
		// store.SpawnReset's internal re-read of the task deterministically
		// fails with a generic wrapped error (not os.IsNotExist, so not
		// task.ErrNotFound) regardless of which user runs the test.
		taskPath := filepath.Join(store.Dir(), tsk.ID+".json")
		sched.SetSpawnResetTestHook(func() {
			if err := os.Remove(taskPath); err != nil {
				t.Errorf("remove task file before directory swap: %v", err)
				return
			}
			if err := os.Mkdir(taskPath, 0o700); err != nil {
				t.Errorf("swap task file for a directory: %v", err)
			}
		})

		clk.Advance(2 * time.Minute)
		sched.RunDueJobs(clk.Now())
		sched.WaitForLane()

		if n := len(rec.calls()); n != 0 {
			t.Fatalf("dispatch should never be reached when SpawnReset fails, got %d calls", n)
		}

		jobs := jobsForTask(sched, tsk.ID)
		if len(jobs) != 1 {
			t.Fatalf("expected exactly 1 armed job after SpawnReset-error re-arm, got %d", len(jobs))
		}
		wantNext := dtstart + oneDayMs
		if jobs[0].Schedule.AtMS == nil || *jobs[0].Schedule.AtMS != wantNext {
			t.Fatalf("re-armed job AtMS = %v, want %d", derefI64(jobs[0].Schedule.AtMS), wantNext)
		}
	})

	t.Run("TaskUnreadableThenSweepRecovers", func(t *testing.T) {
		sched, store, clk, rec := newTriggerSchedulerForTest(t)

		dtstart := secondAligned(clk.Now().Add(time.Minute))
		tsk := makeRruleTask(t, store, "agent-unreadable", "FREQ=DAILY", dtstart, "UTC")
		sched.OnTaskUpserted(tsk)

		taskPath := filepath.Join(store.Dir(), tsk.ID+".json")
		original, err := os.ReadFile(taskPath)
		if err != nil {
			t.Fatalf("read original task file: %v", err)
		}

		if err := os.WriteFile(taskPath, []byte("{not valid json"), 0o600); err != nil {
			t.Fatalf("corrupt task file: %v", err)
		}

		clk.Advance(2 * time.Minute)
		sched.RunDueJobs(clk.Now())
		sched.WaitForLane()

		if n := len(rec.calls()); n != 0 {
			t.Fatalf("dispatch should not run for an unreadable task, got %d calls", n)
		}
		// The fired "at" job is deleted by the engine (DeleteAfterRun) regardless
		// of what RunScheduled returned — re-arm was impossible (store.Get
		// failed), so the task is now a true orphan awaiting the sweep.
		if jobs := jobsForTask(sched, tsk.ID); len(jobs) != 0 {
			t.Fatalf("expected the fired job to be gone after an unreadable-task exit, got %d", len(jobs))
		}

		// Restore the file so the recovery sweep (rule 6) can read it again —
		// this is the designated recovery for the unreadable-task exit.
		if err := os.WriteFile(taskPath, original, 0o600); err != nil {
			t.Fatalf("restore task file: %v", err)
		}

		sched.RunRecoverySweep()

		jobs := jobsForTask(sched, tsk.ID)
		if len(jobs) != 1 {
			t.Fatalf("expected the recovery sweep to re-arm the orphaned rrule task, got %d jobs", len(jobs))
		}
		if jobs[0].Schedule.AtMS == nil {
			t.Fatal("re-armed job has a nil AtMS")
		}
	})

	t.Run("RetireSilentlyAtExhaustion", func(t *testing.T) {
		sched, store, clk, rec := newTriggerSchedulerForTest(t)

		dtstart := secondAligned(clk.Now().Add(time.Minute))
		tsk := makeRruleTask(t, store, "agent-exhaust", "FREQ=DAILY;COUNT=1", dtstart, "UTC")
		sched.OnTaskUpserted(tsk)

		clk.Advance(2 * time.Minute)
		sched.RunDueJobs(clk.Now())
		sched.WaitForLane()

		if n := len(rec.calls()); n != 1 {
			t.Fatalf("expected the single COUNT=1 occurrence to fire once, got %d dispatches", n)
		}
		if jobs := jobsForTask(sched, tsk.ID); len(jobs) != 0 {
			t.Fatalf("expected no job after an exhausted COUNT=1 series retires, got %d", len(jobs))
		}

		// The sweep must not resurrect a legitimately exhausted series.
		sched.RunRecoverySweep()
		if jobs := jobsForTask(sched, tsk.ID); len(jobs) != 0 {
			t.Fatalf("recovery sweep must not re-arm an exhausted series, got %d jobs", len(jobs))
		}
	})

	t.Run("SweepHealsOrphanShapesButSparesInFlightAndAmbiguousFirstStrike", func(t *testing.T) {
		sched, store, clk, rec := newTriggerSchedulerForTest(t)

		// (a) Untracked orphan: exists in the store, non-terminal, RRULE — but
		// OnTaskUpserted was never called, so the scheduler never tracked it.
		// This is an immediate, unambiguous orphan (H1: no tracked job at all
		// to be ambiguous about) — healed on the very first sweep.
		farDtstart := secondAligned(clk.Now().Add(10 * 24 * time.Hour))
		untrackedTsk := makeRruleTask(t, store, "agent-orphan-untracked", "FREQ=DAILY", farDtstart, "UTC")

		// (b) Ambiguous ("dead-entry") shape: tracked and registered, then
		// surgically mutated to the exact "crash between RunDueJobs' clear and
		// the dispatch goroutine setting Running=true" shape the engine is
		// known to produce: NextRunAtMS cleared, Running false, job still
		// present. H1: this is indistinguishable from a legitimately queued
		// dispatch on a single snapshot, so it must NOT be re-armed on the
		// first sweep — only after a SECOND consecutive sweep observes the
		// same shape.
		deadTsk := makeRruleTask(t, store, "agent-orphan-dead", "FREQ=DAILY", farDtstart, "UTC")
		sched.OnTaskUpserted(deadTsk)
		deadJobsBefore := jobsForTask(sched, deadTsk.ID)
		if len(deadJobsBefore) != 1 {
			t.Fatalf("setup: expected 1 initial job for the dead-orphan task, got %d", len(deadJobsBefore))
		}
		deadJob := deadJobsBefore[0]
		deadJob.State.NextRunAtMS = nil
		deadJob.State.Running = false
		if err := sched.cs.UpdateJob(&deadJob); err != nil {
			t.Fatalf("setup: mutate dead-orphan job state: %v", err)
		}

		// (c) False orphan: genuinely in flight. A blocking dispatch holds
		// Running=true (persisted by the engine before the dispatch function is
		// even invoked) until the test releases it.
		inFlightDtstart := secondAligned(clk.Now().Add(time.Minute))
		inFlightTsk := makeRruleTask(t, store, "agent-orphan-inflight", "FREQ=DAILY", inFlightDtstart, "UTC")
		sched.OnTaskUpserted(inFlightTsk)
		inFlightJobsBefore := jobsForTask(sched, inFlightTsk.ID)
		if len(inFlightJobsBefore) != 1 {
			t.Fatalf("setup: expected 1 initial job for the in-flight task, got %d", len(inFlightJobsBefore))
		}
		origInFlightJobID := inFlightJobsBefore[0].ID

		dispatchStarted := make(chan struct{})
		dispatchProceed := make(chan struct{})
		var startOnce sync.Once
		sched.dispatch = func(ctx context.Context, taskID string, occurrenceMs *int64) error {
			startOnce.Do(func() { close(dispatchStarted) })
			<-dispatchProceed
			return nil
		}

		clk.Advance(2 * time.Minute)
		sched.RunDueJobs(clk.Now())
		<-dispatchStarted // the in-flight job's Running=true is now persisted

		// Sanity: cs itself reports Running=true for the in-flight job right now.
		if job, ok := sched.cs.GetJob(origInFlightJobID); !ok || !job.State.Running {
			t.Fatalf("setup: expected in-flight job Running=true, got ok=%v job=%+v", ok, job)
		}

		// First sweep: (a) is healed immediately (true orphan, no grace
		// period). (b) is the ambiguous "queued dispatch" shape (H1) — it
		// must NOT be re-armed yet, only its consecutive-sweep streak bumped.
		// (c) must be untouched.
		sched.RunRecoverySweep()

		if jobs := jobsForTask(sched, untrackedTsk.ID); len(jobs) != 1 {
			t.Fatalf("sweep did not re-arm the untracked orphan, got %d jobs", len(jobs))
		} else if jobs[0].State.NextRunAtMS == nil {
			t.Fatal("untracked-orphan re-armed job has a nil NextRunAtMS")
		}
		if jobs := jobsForTask(sched, deadTsk.ID); len(jobs) != 1 {
			t.Fatalf("expected the ambiguous dead-entry job to still be present after one sweep, got %d jobs",
				len(jobs))
		} else if jobs[0].ID != deadJob.ID {
			t.Fatal("H1: ambiguous dead-entry shape was re-armed after only ONE sweep — " +
				"must wait for a second consecutive ambiguous sweep")
		}

		inFlightJobsAfterFirstSweep := jobsForTask(sched, inFlightTsk.ID)
		if len(inFlightJobsAfterFirstSweep) != 1 || inFlightJobsAfterFirstSweep[0].ID != origInFlightJobID {
			t.Fatalf("first sweep must not touch an in-flight fire: jobs=%+v, want unchanged job %q",
				inFlightJobsAfterFirstSweep, origInFlightJobID)
		}
		if !inFlightJobsAfterFirstSweep[0].State.Running {
			t.Fatal("in-flight job State.Running was cleared by the first sweep (must stay true)")
		}

		// Second consecutive sweep: the SAME ambiguous shape persists for (b)
		// (nothing about deadTsk's job changed between sweeps), so H1's
		// two-strike rule now escalates it to a true-orphan re-arm.
		sched.RunRecoverySweep()

		if jobs := jobsForTask(sched, deadTsk.ID); len(jobs) != 1 {
			t.Fatalf("second sweep did not re-arm the dead-entry orphan, got %d jobs", len(jobs))
		} else if jobs[0].ID == deadJob.ID {
			t.Fatal("dead-entry orphan job was not replaced after two consecutive ambiguous sweeps " +
				"(same job ID survived)")
		} else if jobs[0].State.NextRunAtMS == nil {
			t.Fatal("dead-entry orphan re-armed job has a nil NextRunAtMS")
		}

		// (c) must be left completely untouched by either sweep: same job ID,
		// still Running.
		inFlightJobsDuring := jobsForTask(sched, inFlightTsk.ID)
		if len(inFlightJobsDuring) != 1 || inFlightJobsDuring[0].ID != origInFlightJobID {
			t.Fatalf("sweep must not touch an in-flight fire: jobs=%+v, want unchanged job %q",
				inFlightJobsDuring, origInFlightJobID)
		}
		if !inFlightJobsDuring[0].State.Running {
			t.Fatal("in-flight job State.Running was cleared by the sweep (must stay true)")
		}

		// Release the blocked dispatch and let the in-flight fire complete
		// normally — its own exit-path re-arm must still work afterwards.
		close(dispatchProceed)
		sched.WaitForLane()

		if n := len(rec.calls()); n != 0 {
			// The custom dispatch above bypasses rec entirely; this just guards
			// against an accidental double-registration elsewhere in the test.
			t.Fatalf("unexpected recorder calls in this subtest: %d", n)
		}

		finalInFlightJobs := jobsForTask(sched, inFlightTsk.ID)
		if len(finalInFlightJobs) != 1 {
			t.Fatalf("expected the in-flight task to end with exactly 1 armed job, got %d", len(finalInFlightJobs))
		}
		if finalInFlightJobs[0].ID == origInFlightJobID {
			t.Fatal("expected the in-flight task's own exit-path re-arm to replace the fired job")
		}
		wantNext := inFlightDtstart + oneDayMs
		if finalInFlightJobs[0].Schedule.AtMS == nil || *finalInFlightJobs[0].Schedule.AtMS != wantNext {
			t.Fatalf("in-flight task's post-fire job AtMS = %v, want %d",
				derefI64(finalInFlightJobs[0].Schedule.AtMS), wantNext)
		}
	})
}

// -----------------------------------------------------------------------------
// H1: TestTriggerScheduler_AmbiguousSweepRequiresTwoStrikes
// -----------------------------------------------------------------------------

// TestTriggerScheduler_AmbiguousSweepRequiresTwoStrikes is H1's focused
// regression, isolated from the multi-shape interaction covered by
// SweepHealsOrphanShapesButSparesInFlightAndAmbiguousFirstStrike above: a
// due-but-queued dispatch (RunDueJobs already cleared NextRunAtMS;
// executeJobByID has not yet persisted Running=true, e.g. because it is
// still queued on the cron engine's lane semaphore) is, on a single sweep
// snapshot, byte-for-byte the same shape as a true crash orphan — job
// present, NextRunAtMS nil, not Running. The sweep must not re-arm on the
// first observation of that shape; only after the SAME task stays in it
// across two consecutive sweeps.
func TestTriggerScheduler_AmbiguousSweepRequiresTwoStrikes(t *testing.T) {
	sched, store, clk, _ := newTriggerSchedulerForTest(t)

	farDtstart := secondAligned(clk.Now().Add(10 * 24 * time.Hour))
	tsk := makeRruleTask(t, store, "agent-ambiguous", "FREQ=DAILY", farDtstart, "UTC")
	sched.OnTaskUpserted(tsk)

	jobsBefore := jobsForTask(sched, tsk.ID)
	if len(jobsBefore) != 1 {
		t.Fatalf("setup: expected 1 initial job, got %d", len(jobsBefore))
	}
	origJob := jobsBefore[0]

	// Fabricate the queued-dispatch shape directly, mirroring exactly what
	// RunDueJobs + a not-yet-scheduled executeJobByID would leave behind.
	mutated := origJob
	mutated.State.NextRunAtMS = nil
	mutated.State.Running = false
	if err := sched.cs.UpdateJob(&mutated); err != nil {
		t.Fatalf("mutate job state: %v", err)
	}

	sched.RunRecoverySweep()
	if jobs := jobsForTask(sched, tsk.ID); len(jobs) != 1 || jobs[0].ID != origJob.ID {
		t.Fatalf("first ambiguous sweep must NOT re-arm; jobs=%+v, want unchanged job %q", jobs, origJob.ID)
	}

	sched.RunRecoverySweep()
	jobsAfterSecond := jobsForTask(sched, tsk.ID)
	if len(jobsAfterSecond) != 1 || jobsAfterSecond[0].ID == origJob.ID {
		t.Fatalf("second consecutive ambiguous sweep must re-arm; jobs=%+v, want a NEW job replacing %q",
			jobsAfterSecond, origJob.ID)
	}
	if jobsAfterSecond[0].State.NextRunAtMS == nil {
		t.Fatal("re-armed job has a nil NextRunAtMS")
	}

	t.Run("StreakResetsWhenArmedInBetween", func(t *testing.T) {
		// A fresh task: one ambiguous sweep (streak=1), then the job
		// resolves favorably (Running flips true, as a genuinely queued
		// dispatch would), then goes back to the ambiguous shape. The streak
		// must have been cleared by the intervening armed observation, so
		// this requires two MORE consecutive ambiguous sweeps, not one.
		sched2, store2, clk2, _ := newTriggerSchedulerForTest(t)
		dt := secondAligned(clk2.Now().Add(10 * 24 * time.Hour))
		tsk2 := makeRruleTask(t, store2, "agent-ambiguous-reset", "FREQ=DAILY", dt, "UTC")
		sched2.OnTaskUpserted(tsk2)

		jobs2 := jobsForTask(sched2, tsk2.ID)
		if len(jobs2) != 1 {
			t.Fatalf("setup: expected 1 initial job, got %d", len(jobs2))
		}
		orig2 := jobs2[0]

		ambiguous := orig2
		ambiguous.State.NextRunAtMS = nil
		ambiguous.State.Running = false
		if err := sched2.cs.UpdateJob(&ambiguous); err != nil {
			t.Fatalf("mutate job state: %v", err)
		}

		sched2.RunRecoverySweep() // strike 1
		if jobs := jobsForTask(sched2, tsk2.ID); len(jobs) != 1 || jobs[0].ID != orig2.ID {
			t.Fatalf("first ambiguous sweep must NOT re-arm; jobs=%+v", jobs)
		}

		// Resolve favorably: Running flips true (as if the dispatch finally
		// got its lane slot), clearing the streak per shouldReArmLocked.
		running := orig2
		running.State.Running = true
		if err := sched2.cs.UpdateJob(&running); err != nil {
			t.Fatalf("mutate job state to Running: %v", err)
		}
		sched2.RunRecoverySweep() // observes armed, clears streak
		if jobs := jobsForTask(sched2, tsk2.ID); len(jobs) != 1 || jobs[0].ID != orig2.ID {
			t.Fatalf("armed observation must leave the job untouched; jobs=%+v", jobs)
		}

		// Back to ambiguous.
		backToAmbiguous := orig2
		backToAmbiguous.State.NextRunAtMS = nil
		backToAmbiguous.State.Running = false
		if err := sched2.cs.UpdateJob(&backToAmbiguous); err != nil {
			t.Fatalf("mutate job state back to ambiguous: %v", err)
		}

		sched2.RunRecoverySweep() // strike 1 again (streak was reset)
		if jobs := jobsForTask(sched2, tsk2.ID); len(jobs) != 1 || jobs[0].ID != orig2.ID {
			t.Fatalf("streak must have reset: this sweep must NOT re-arm; jobs=%+v", jobs)
		}

		sched2.RunRecoverySweep() // strike 2: now re-arms
		if jobs := jobsForTask(sched2, tsk2.ID); len(jobs) != 1 || jobs[0].ID == orig2.ID {
			t.Fatalf("second post-reset ambiguous sweep must re-arm; jobs=%+v", jobs)
		}
	})
}

// -----------------------------------------------------------------------------
// H2: TestTriggerScheduler_UnreadableTaskEscalatesToError
// -----------------------------------------------------------------------------

// TestTriggerScheduler_UnreadableTaskEscalatesToError is H2's regression:
// unlike the pre-existing TaskUnreadableThenSweepRecovers subtest (which
// restores the file BEFORE the sweep runs), this corrupts the task file and
// runs the sweep WITHOUT restoring it — the exact hole H2 closes, where
// store.List's silent Warn+skip made the file invisible to the recovery
// mechanism forever. Asserts the escalated ERROR fires with the task ID
// surfaced, via slog's default handler swapped for a capturing one.
func TestTriggerScheduler_UnreadableTaskEscalatesToError(t *testing.T) {
	sched, store, clk, _ := newTriggerSchedulerForTest(t)

	dtstart := secondAligned(clk.Now().Add(time.Minute))
	tsk := makeRruleTask(t, store, "agent-unreadable-sweep", "FREQ=DAILY", dtstart, "UTC")
	sched.OnTaskUpserted(tsk)

	// Corrupt the file, then let the already-tracked job fire: RunScheduled's
	// store.Get fails on the corrupt file (task-unreadable exit, no re-arm),
	// and the fired "at" job is deleted by the engine's own DeleteAfterRun
	// regardless (same mechanics as the pre-existing
	// TaskUnreadableThenSweepRecovers subtest). This leaves the task
	// "isn't currently armed" (isArmed reads live cron state: the tracked
	// job ID no longer resolves) — the precondition logUnreadableTasks
	// requires before it logs. Without firing the job first, the tracked
	// job would still have a live future NextRunAtMS and read as armed,
	// suppressing the ERROR log entirely.
	taskPath := filepath.Join(store.Dir(), tsk.ID+".json")
	if err := os.WriteFile(taskPath, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("corrupt task file: %v", err)
	}

	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	// Must be a raceFreeLogBuffer, never a bare bytes.Buffer: slog.SetDefault
	// swaps the PROCESS-GLOBAL logger, so every still-running goroutine in the
	// test binary writes into this sink concurrently with the Reset()/String()
	// calls below. See raceFreeLogBuffer's doc comment (test_helpers_test.go)
	// for the CI failure this exact line produced on 2026-08-23.
	logBuf := captureDefaultSlog(t, slog.LevelInfo)

	sched.RunRecoverySweep()

	logged := logBuf.String()
	if !strings.Contains(logged, "level=ERROR") {
		t.Fatalf("expected an ERROR-level log entry for the unreadable task, got log:\n%s", logged)
	}
	if !strings.Contains(logged, "task file unreadable") {
		t.Fatalf("expected the unreadable-task ERROR message, got log:\n%s", logged)
	}
	if !strings.Contains(logged, tsk.ID) {
		t.Fatalf("expected the unreadable task's ID %q in the log, got log:\n%s", tsk.ID, logged)
	}
	if !strings.Contains(logged, "consecutive_sweeps=1") {
		t.Fatalf("expected consecutive_sweeps=1 on the first observation, got log:\n%s", logged)
	}

	// A second consecutive sweep (still uncorrected) must escalate the
	// streak counter.
	logBuf.Reset()
	sched.RunRecoverySweep()
	if !strings.Contains(logBuf.String(), "consecutive_sweeps=2") {
		t.Fatalf("expected consecutive_sweeps=2 on the second observation, got log:\n%s", logBuf.String())
	}
}

// -----------------------------------------------------------------------------
// F5: TestTriggerScheduler_SpawnResetTaskNotFound_NoReArm
// -----------------------------------------------------------------------------

// TestTriggerScheduler_SpawnResetTaskNotFound_NoReArm is F5's regression:
// RunScheduled already special-cases task.ErrNotFound from its EARLIER
// store.Get (removes the job, no re-arm) but, before this fix, did not
// special-case the same error from the LATER store.SpawnReset call — which
// can return it if the task is deleted in the exact window between the two
// calls. Uses the spawnResetTestHook seam (mirrors the existing
// rearmTestHook pattern) to force that window deterministically: the hook
// fires after store.Get has already succeeded but immediately before
// SpawnReset's own read, where it deletes the task out from under the fire.
func TestTriggerScheduler_SpawnResetTaskNotFound_NoReArm(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	dtstart := secondAligned(clk.Now().Add(time.Minute))
	tsk := makeRruleTask(t, store, "agent-spawnreset-notfound", "FREQ=DAILY", dtstart, "UTC")
	sched.OnTaskUpserted(tsk)

	sched.SetSpawnResetTestHook(func() {
		if _, err := store.Delete(tsk.ID); err != nil {
			t.Errorf("simulated concurrent delete: store.Delete: %v", err)
		}
	})

	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	if n := len(rec.calls()); n != 0 {
		t.Fatalf("dispatch should not run when SpawnReset finds the task deleted, got %d calls", n)
	}
	if jobs := jobsForTask(sched, tsk.ID); len(jobs) != 0 {
		t.Fatalf("F5: expected no job re-armed after SpawnReset ErrNotFound (task deleted mid-fire), got %d", len(jobs))
	}
}

// -----------------------------------------------------------------------------
// Test 10: TestTriggerScheduler_EditDuringFire
// -----------------------------------------------------------------------------

// TestTriggerScheduler_EditDuringFire deterministically forces the
// edit-during-fire interleaving (Scheduler rule 4) via the rearm test hook,
// rather than hoping goroutine scheduling reproduces it: the hook runs a
// simulated concurrent PUT (a new repeat rule) to completion immediately
// before the firing job's own re-arm attempts its generation check, so the
// stale re-arm deterministically observes the mismatch and no-ops, leaving
// exactly one armed job — for the new rule.
func TestTriggerScheduler_EditDuringFire(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	dtstart := secondAligned(clk.Now().Add(time.Minute))
	ruleA := "FREQ=DAILY"
	ruleB := "FREQ=WEEKLY;BYDAY=MO"
	tsk := makeRruleTask(t, store, "agent-edit-during-fire", ruleA, dtstart, "UTC")
	sched.OnTaskUpserted(tsk)

	var hookCalls int
	sched.SetRearmTestHook(func() {
		hookCalls++
		if hookCalls > 1 {
			return // apply the simulated concurrent edit exactly once
		}
		newTrigger := &task.Trigger{
			Type: task.TriggerRecurring,
			Config: task.TriggerConfig{
				Rrule:     strP(ruleB),
				DtstartMs: int64P(dtstart),
				Tz:        strP("UTC"),
			},
		}
		updated, err := store.Update(tsk.ID, task.Patch{Trigger: &newTrigger})
		if err != nil {
			t.Errorf("simulated concurrent PUT: store.Update: %v", err)
			return
		}
		// This is the "PUT completes during the fire" step: OnTaskUpserted runs
		// to completion (its own full remove→AddJobFull→map-write critical
		// section) before the paused re-arm resumes and checks the generation.
		sched.OnTaskUpserted(updated)
	})

	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	if hookCalls == 0 {
		t.Fatal("rearm test hook was never invoked — the fire did not reach the RRULE re-arm path")
	}
	if n := len(rec.calls()); n != 1 {
		t.Fatalf("expected exactly 1 dispatch (the original fire), got %d", n)
	}

	jobs := jobsForTask(sched, tsk.ID)
	if len(jobs) != 1 {
		t.Fatalf("expected exactly one armed job after edit-during-fire, got %d", len(jobs))
	}
	if jobs[0].Schedule.Kind != "at" || jobs[0].Schedule.AtMS == nil {
		t.Fatalf("armed job schedule = %+v, want a well-formed 'at' job", jobs[0].Schedule)
	}

	finalTask, err := store.Get(tsk.ID)
	if err != nil {
		t.Fatalf("store.Get final task: %v", err)
	}
	if finalTask.Trigger.Config.Rrule == nil || *finalTask.Trigger.Config.Rrule != ruleB {
		t.Fatalf("stored trigger rrule = %v, want %q (the new rule, not the stale one)",
			finalTask.Trigger.Config.Rrule, ruleB)
	}

	// The one armed job must fire at rule B's next occurrence, not rule A's —
	// proof the stale re-arm (rule A) actually no-opped rather than merely
	// losing a race to be listed last.
	wantNext, ok, err := task.NextOccurrenceAfter(ruleB, dtstart, "UTC", clk.Now().UnixMilli())
	if err != nil || !ok {
		t.Fatalf("task.NextOccurrenceAfter(ruleB): ok=%v err=%v", ok, err)
	}
	if *jobs[0].Schedule.AtMS != wantNext {
		t.Fatalf("armed job AtMS = %d, want %d (rule B's next occurrence)", *jobs[0].Schedule.AtMS, wantNext)
	}
}

// -----------------------------------------------------------------------------
// Test 11: TestTriggerScheduler_LegacyPathUnchanged
// -----------------------------------------------------------------------------

// TestTriggerScheduler_LegacyPathUnchanged is the byte-for-byte regression
// guard (Scheduler rule 7) on the at/every/cron job translation for
// non-RRULE triggers — the seam that also carries workspace heartbeats. It
// pins triggerToCronSchedule's exact output for once/every/legacy cron_expr
// (unit level) plus one end-to-end legacy-cron registration (integration
// level, not previously covered by task_trigger_test.go).
func TestTriggerScheduler_LegacyPathUnchanged(t *testing.T) {
	nowMs := time.Now().UnixMilli()

	t.Run("once", func(t *testing.T) {
		at := nowMs + 60000
		tr := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}
		sched, err := triggerToCronSchedule(tr, nowMs)
		if err != nil {
			t.Fatalf("triggerToCronSchedule(once): %v", err)
		}
		if sched.Kind != "at" || sched.AtMS == nil || *sched.AtMS != at {
			t.Fatalf("once mapping = %+v, want Kind=at AtMS=%d", sched, at)
		}
		if sched.EveryMS != nil || sched.Expr != "" {
			t.Fatalf("once mapping leaked unrelated fields: %+v", sched)
		}
	})

	t.Run("once_missing_at_ms", func(t *testing.T) {
		tr := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{}}
		if _, err := triggerToCronSchedule(tr, nowMs); err == nil {
			t.Fatal("expected an error for a 'once' trigger missing config.at_ms")
		}
	})

	t.Run("every", func(t *testing.T) {
		every := int64(60000)
		tr := &task.Trigger{Type: task.TriggerEvery, Config: task.TriggerConfig{EveryMs: &every}}
		sched, err := triggerToCronSchedule(tr, nowMs)
		if err != nil {
			t.Fatalf("triggerToCronSchedule(every): %v", err)
		}
		if sched.Kind != "every" || sched.EveryMS == nil || *sched.EveryMS != every {
			t.Fatalf("every mapping = %+v, want Kind=every EveryMS=%d", sched, every)
		}
	})

	t.Run("every_missing_every_ms", func(t *testing.T) {
		tr := &task.Trigger{Type: task.TriggerEvery, Config: task.TriggerConfig{}}
		if _, err := triggerToCronSchedule(tr, nowMs); err == nil {
			t.Fatal("expected an error for an 'every' trigger missing config.every_ms")
		}
	})

	t.Run("legacy_cron_expr", func(t *testing.T) {
		expr := "0 9 * * MON"
		tr := &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{CronExpr: &expr}}
		sched, err := triggerToCronSchedule(tr, nowMs)
		if err != nil {
			t.Fatalf("triggerToCronSchedule(legacy cron): %v", err)
		}
		if sched.Kind != "cron" || sched.Expr != expr {
			t.Fatalf("legacy cron mapping = %+v, want Kind=cron Expr=%q", sched, expr)
		}
		if sched.AtMS != nil || sched.EveryMS != nil {
			t.Fatalf("legacy cron mapping leaked unrelated fields: %+v", sched)
		}
	})

	t.Run("recurring_missing_cron_expr_and_rrule", func(t *testing.T) {
		tr := &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{}}
		if _, err := triggerToCronSchedule(tr, nowMs); err == nil {
			t.Fatal("expected an error for a 'recurring' trigger with neither cron_expr nor rrule")
		}
	})

	t.Run("unsupported_type", func(t *testing.T) {
		tr := &task.Trigger{Type: task.TriggerManual}
		if _, err := triggerToCronSchedule(tr, nowMs); err == nil {
			t.Fatal("expected an error for a manual trigger (never routed through triggerToCronSchedule in practice)")
		}
	})

	t.Run("end_to_end_legacy_cron_registration", func(t *testing.T) {
		sched, store, _, _ := newTriggerSchedulerForTest(t)

		expr := "0 9 * * MON"
		tsk := makeTask(t, store, "agent-legacy-cron", &task.Trigger{
			Type:   task.TriggerRecurring,
			Config: task.TriggerConfig{CronExpr: &expr},
		})
		sched.OnTaskUpserted(tsk)

		jobs := jobsForTask(sched, tsk.ID)
		if len(jobs) != 1 {
			t.Fatalf("expected exactly 1 job for the legacy cron task, got %d", len(jobs))
		}
		if jobs[0].Schedule.Kind != "cron" || jobs[0].Schedule.Expr != expr {
			t.Fatalf("legacy cron job schedule = %+v, want Kind=cron Expr=%q", jobs[0].Schedule, expr)
		}
		if jobs[0].DeleteAfterRun {
			t.Error("legacy cron job DeleteAfterRun = true, want false (only 'at' jobs auto-delete)")
		}
	})

	t.Run("end_to_end_heartbeat_still_unregistered", func(t *testing.T) {
		// Regression guard: RRULE additions must not affect the heartbeat-surface
		// skip that TestTriggerHeartbeatNotRegistered already covers for `every`
		// — pin it here too for a `recurring`+cron_expr heartbeat task, since
		// that combination was never exercised before this feature.
		sched, store, _, rec := newTriggerSchedulerForTest(t)

		expr := "*/5 * * * *"
		surf := task.SurfaceHeartbeat
		tsk := makeTask(t, store, "agent-heartbeat-cron", &task.Trigger{
			Type:   task.TriggerRecurring,
			Config: task.TriggerConfig{CronExpr: &expr},
		})
		updated, err := store.Update(tsk.ID, task.Patch{Surface: &surf})
		if err != nil {
			t.Fatalf("store.Update surface: %v", err)
		}

		sched.OnTaskUpserted(updated)

		if jobs := jobsForTask(sched, tsk.ID); len(jobs) != 0 {
			t.Fatalf("heartbeat-surface recurring task was registered as a cron job, want none (got %d)", len(jobs))
		}
		if n := len(rec.calls()); n != 0 {
			t.Fatalf("heartbeat-surface task dispatched %d time(s), want 0", n)
		}
	})
}

// -----------------------------------------------------------------------------
// Regression: recurring/every task fires once then dies
// -----------------------------------------------------------------------------

// TestTriggerScheduler_RecurringSurvivesRunCompletion is the regression test
// for "a recurring/every task fires exactly once, then dies". Dispatch is
// asynchronous in production (SpawnTriggeredRun/ExecuteTask launch
// `go te.runTask(...)` and return immediately), so RunScheduled's own
// exit-path re-arm (rearmRrule) always runs — and arms the next occurrence —
// BEFORE that async run finishes. The bug was that the async run's LATER
// terminal status landing (via a task_update tool call or a REST PATCH) also
// calls NotifyTaskUpserted → OnTaskUpserted, which unconditionally removed
// ANY terminal task's job — deleting the job rearmRrule had just armed.
//
// This test reproduces that exact production sequence without needing the
// full async executor: fire the trigger (which re-arms via rearmRrule
// exactly as production does, since the dispatch recorder returns
// immediately like the real fire-and-forget dispatch), THEN simulate the
// async run's later completion by setting the store task to `done` and
// calling OnTaskUpserted with it — the SAME call NotifyTaskUpserted makes in
// production (pkg/agent/loop.go NotifyTaskUpserted, invoked from
// taskUpdate.SetOnComplete / the REST PATCH handler) — and asserts the job
// the fire just armed is STILL present afterwards.
//
// Before the fix (OnTaskUpserted's unconditional `terminal ⇒ remove`): this
// test FAILS — the completion call removes the job, leaving 0 armed jobs.
// After the fix (isRepeating survives terminal): this test PASSES — the
// series stays armed at its correct next occurrence.
func TestTriggerScheduler_RecurringSurvivesRunCompletion(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	dtstart := secondAligned(clk.Now().Add(time.Minute))
	tsk := makeRruleTask(t, store, "agent-survives-completion", "FREQ=DAILY", dtstart, "UTC")
	sched.OnTaskUpserted(tsk)

	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	if n := len(rec.calls()); n != 1 {
		t.Fatalf("expected 1 dispatch, got %d", n)
	}

	// The fire's own exit-path re-arm (rearmRrule) must already have armed
	// the next occurrence — production's dispatch (SpawnTriggeredRun) only
	// launches the async run and returns immediately, exactly like this
	// test's dispatch recorder, so RunScheduled always reaches rearmRrule
	// before that run's own completion could possibly land.
	armedAfterFire := jobsForTask(sched, tsk.ID)
	if len(armedAfterFire) != 1 {
		t.Fatalf("expected exactly 1 armed job right after the fire, got %d", len(armedAfterFire))
	}
	wantNext := dtstart + oneDayMs
	if armedAfterFire[0].Schedule.AtMS == nil || *armedAfterFire[0].Schedule.AtMS != wantNext {
		t.Fatalf("armed job AtMS = %v, want %d", derefI64(armedAfterFire[0].Schedule.AtMS), wantNext)
	}

	// Simulate the async run completing to `done` LATER, and the production
	// notification path that follows it: NotifyTaskUpserted -> OnTaskUpserted
	// with the now-terminal task snapshot.
	doneStatus := task.StatusDone
	completed, err := store.Update(tsk.ID, task.Patch{Status: &doneStatus})
	if err != nil {
		t.Fatalf("store.Update to done: %v", err)
	}
	sched.OnTaskUpserted(completed)

	armedAfterCompletion := jobsForTask(sched, tsk.ID)
	if len(armedAfterCompletion) != 1 {
		t.Fatalf("REGRESSION: the async completion's OnTaskUpserted call removed the job the fire had "+
			"just armed (recurring series died after exactly one fire) — got %d jobs, want 1",
			len(armedAfterCompletion))
	}
	if armedAfterCompletion[0].Schedule.AtMS == nil || *armedAfterCompletion[0].Schedule.AtMS != wantNext {
		t.Fatalf("job surviving completion has AtMS = %v, want %d (unchanged next occurrence)",
			derefI64(armedAfterCompletion[0].Schedule.AtMS), wantNext)
	}
}

// TestTriggerScheduler_RecurringMultiFireAcrossCompletions is the multi-fire
// proof: a FREQ=DAILY;COUNT=3 series must fire exactly 3 times across 3
// simulated async completions (not once), then retire cleanly with no
// re-arm once the series is exhausted — including immediately after the
// LAST completion's own OnTaskUpserted call, proving that call does not
// resurrect an already-exhausted series.
func TestTriggerScheduler_RecurringMultiFireAcrossCompletions(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	dtstart := secondAligned(clk.Now().Add(time.Minute))
	tsk := makeRruleTask(t, store, "agent-multi-fire", "FREQ=DAILY;COUNT=3", dtstart, "UTC")
	sched.OnTaskUpserted(tsk)

	// The first advance crosses only the first occurrence (dtstart); each
	// subsequent advance crosses exactly one more daily occurrence. Crossing
	// more than one occurrence per advance would let rearmRrule skip straight
	// to a LATER occurrence's boundary, undercounting the fires this test
	// means to force one at a time.
	advances := []time.Duration{2 * time.Minute, 24 * time.Hour, 24 * time.Hour}
	doneStatus := task.StatusDone

	for i, adv := range advances {
		fireNum := i + 1

		clk.Advance(adv)
		sched.RunDueJobs(clk.Now())
		sched.WaitForLane()

		if n := len(rec.calls()); n != fireNum {
			t.Fatalf("after fire %d: expected %d cumulative dispatches, got %d", fireNum, fireNum, n)
		}

		// Simulate that fire's async run completing to `done`, and the
		// production notification path (NotifyTaskUpserted) that follows.
		completed, err := store.Update(tsk.ID, task.Patch{Status: &doneStatus})
		if err != nil {
			t.Fatalf("fire %d: store.Update to done: %v", fireNum, err)
		}
		sched.OnTaskUpserted(completed)

		if fireNum < 3 {
			jobs := jobsForTask(sched, tsk.ID)
			if len(jobs) != 1 {
				t.Fatalf("after fire %d completion: expected 1 armed job for the next occurrence, got %d",
					fireNum, len(jobs))
			}
			wantNext := dtstart + int64(fireNum)*oneDayMs
			if jobs[0].Schedule.AtMS == nil || *jobs[0].Schedule.AtMS != wantNext {
				t.Fatalf("after fire %d: armed job AtMS = %v, want %d",
					fireNum, derefI64(jobs[0].Schedule.AtMS), wantNext)
			}
			// SpawnReset (called by RunScheduled on the NEXT advance) resets
			// any non-in_progress status back to `next` on its own — mirrors
			// production, where nothing manually resets status between fires.
		}
	}

	if n := len(rec.calls()); n != 3 {
		t.Fatalf("expected the COUNT=3 series to fire exactly 3 times total, got %d", n)
	}
	if jobs := jobsForTask(sched, tsk.ID); len(jobs) != 0 {
		t.Fatalf("expected the exhausted COUNT=3 series to retire with no job after the 3rd fire's "+
			"completion, got %d", len(jobs))
	}
}

// -----------------------------------------------------------------------------
// FIX 1 proof (7-reviewer gate on 1b3d4b3d, C1b): RRULE occurrence preserved
// across a slow completion, not silently replaced.
// -----------------------------------------------------------------------------

// TestTriggerScheduler_RruleNoClobberOnSlowCompletion is C1b's regression
// test: OnTaskUpserted's terminal-repeating path used to call
// replaceJobLocked UNCONDITIONALLY — with no generation/staleness guard
// (unlike rearmRrule's own check) — so a completion notification landing
// well after the fire (this test advances the clock in between, simulating a
// slow async run) would blindly recompute "next occurrence after now" and
// replace the job rearmRrule had already armed, even though that armed
// occurrence was still perfectly valid and still in the future. FIX 1's
// idempotency guard (same generation, still armed) makes this a no-op
// instead, preserving both the occurrence's fire time AND the job identity
// rearmRrule installed.
//
// Before FIX 1: this test FAILS on the job-ID assertion (the completion
// replaces the job with a new one, even though the AtMS happens to compute
// out the same because the clock advance here does not cross the next
// occurrence's own boundary — proving the replacement was gratuitous, not
// merely harmless). After FIX 1: PASSES — same job, same AtMS.
func TestTriggerScheduler_RruleNoClobberOnSlowCompletion(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	dtstart := secondAligned(clk.Now().Add(time.Minute))
	tsk := makeRruleTask(t, store, "agent-no-clobber", "FREQ=DAILY", dtstart, "UTC")
	sched.OnTaskUpserted(tsk)

	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	if n := len(rec.calls()); n != 1 {
		t.Fatalf("expected 1 dispatch, got %d", n)
	}

	armedAfterFire := jobsForTask(sched, tsk.ID)
	if len(armedAfterFire) != 1 {
		t.Fatalf("expected exactly 1 armed job right after the fire, got %d", len(armedAfterFire))
	}
	wantNext := dtstart + oneDayMs
	if armedAfterFire[0].Schedule.AtMS == nil || *armedAfterFire[0].Schedule.AtMS != wantNext {
		t.Fatalf("armed job AtMS = %v, want %d", derefI64(armedAfterFire[0].Schedule.AtMS), wantNext)
	}

	// Simulate a slow async run: the clock advances several hours between the
	// fire (which already re-armed via rearmRrule) and the completion
	// notification landing — well short of the next occurrence's own
	// boundary (24h away), so the armed occurrence is still valid and still
	// in the future when the completion notification arrives.
	clk.Advance(6 * time.Hour)

	doneStatus := task.StatusDone
	completed, err := store.Update(tsk.ID, task.Patch{Status: &doneStatus})
	if err != nil {
		t.Fatalf("store.Update to done: %v", err)
	}
	sched.OnTaskUpserted(completed)

	armedAfterCompletion := jobsForTask(sched, tsk.ID)
	if len(armedAfterCompletion) != 1 {
		t.Fatalf("expected exactly 1 armed job after completion, got %d", len(armedAfterCompletion))
	}
	if armedAfterCompletion[0].ID != armedAfterFire[0].ID {
		t.Fatalf("REGRESSION (C1b): the slow completion's OnTaskUpserted call replaced the job (%q -> %q) "+
			"instead of leaving the already-armed occurrence alone (a genuine no-op)",
			armedAfterFire[0].ID, armedAfterCompletion[0].ID)
	}
	if armedAfterCompletion[0].Schedule.AtMS == nil || *armedAfterCompletion[0].Schedule.AtMS != wantNext {
		t.Fatalf("armed occurrence after completion = %v, want %d (unchanged, not clobbered)",
			derefI64(armedAfterCompletion[0].Schedule.AtMS), wantNext)
	}
}

// -----------------------------------------------------------------------------
// FIX 1 proof: `failed` survives exactly like `done` (RecurringSurvivesRunCompletion
// shape, StatusFailed).
// -----------------------------------------------------------------------------

// TestTriggerScheduler_RecurringSurvivesFailedCompletion is
// TestTriggerScheduler_RecurringSurvivesRunCompletion's sibling for
// StatusFailed: a repeating trigger's series must survive a per-run FAILED
// status exactly as it survives DONE (task.IsTerminal treats both as
// terminal; Trigger.IsRepeating does not distinguish which terminal status
// caused the completion notification).
func TestTriggerScheduler_RecurringSurvivesFailedCompletion(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	dtstart := secondAligned(clk.Now().Add(time.Minute))
	tsk := makeRruleTask(t, store, "agent-survives-failure", "FREQ=DAILY", dtstart, "UTC")
	sched.OnTaskUpserted(tsk)

	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	if n := len(rec.calls()); n != 1 {
		t.Fatalf("expected 1 dispatch, got %d", n)
	}

	armedAfterFire := jobsForTask(sched, tsk.ID)
	if len(armedAfterFire) != 1 {
		t.Fatalf("expected exactly 1 armed job right after the fire, got %d", len(armedAfterFire))
	}
	wantNext := dtstart + oneDayMs

	failedStatus := task.StatusFailed
	completed, err := store.Update(tsk.ID, task.Patch{Status: &failedStatus})
	if err != nil {
		t.Fatalf("store.Update to failed: %v", err)
	}
	sched.OnTaskUpserted(completed)

	armedAfterCompletion := jobsForTask(sched, tsk.ID)
	if len(armedAfterCompletion) != 1 {
		t.Fatalf("a FAILED repeating task's job must survive exactly like a DONE one — got %d jobs, want 1",
			len(armedAfterCompletion))
	}
	if armedAfterCompletion[0].Schedule.AtMS == nil || *armedAfterCompletion[0].Schedule.AtMS != wantNext {
		t.Fatalf("job surviving a failed completion has AtMS = %v, want %d (unchanged next occurrence)",
			derefI64(armedAfterCompletion[0].Schedule.AtMS), wantNext)
	}
	if armedAfterCompletion[0].ID != armedAfterFire[0].ID {
		t.Errorf("job ID changed after a failed completion (%q -> %q); the idempotency guard "+
			"must treat failed exactly like done", armedAfterFire[0].ID, armedAfterCompletion[0].ID)
	}
}

// -----------------------------------------------------------------------------
// RunRecoverySweep: terminal-but-repeating + lost job, and exhausted-not-resurrected.
// -----------------------------------------------------------------------------

// TestTriggerScheduler_SweepReArmsTerminalRepeatingLostJob drives a recurring
// task to `done`, then deletes its tracked cron job out from under the
// scheduler (simulating a crash/restart-adjacent loss of the in-memory
// taskToJob entry's underlying engine job — e.g. the jobs.json store file
// getting corrupted or truncated between the job's registration and the next
// read), and asserts RunRecoverySweep re-arms it: terminal-but-repeating is a
// CANDIDATE for the sweep, not a skip (see RunRecoverySweep's doc comment).
// A separate exhausted (COUNT reached) terminal series must NOT be
// resurrected by the same sweep — exhaustion is retirement, not orphaning.
func TestTriggerScheduler_SweepReArmsTerminalRepeatingLostJob(t *testing.T) {
	sched, store, clk, _ := newTriggerSchedulerForTest(t)

	dtstart := secondAligned(clk.Now().Add(time.Minute))

	// Task 1: a non-exhausted recurring series, driven to `done`, then its
	// job is removed out from under the scheduler (simulating a lost job).
	liveTsk := makeRruleTask(t, store, "agent-sweep-live", "FREQ=DAILY", dtstart, "UTC")
	sched.OnTaskUpserted(liveTsk)
	if jobs := jobsForTask(sched, liveTsk.ID); len(jobs) != 1 {
		t.Fatalf("expected 1 job registered for the live task, got %d", len(jobs))
	}
	doneStatus := task.StatusDone
	liveDone, err := store.Update(liveTsk.ID, task.Patch{Status: &doneStatus})
	if err != nil {
		t.Fatalf("store.Update live task to done: %v", err)
	}
	sched.OnTaskUpserted(liveDone) // no-op post-FIX-1: job stays armed, same ID

	liveJobsBefore := jobsForTask(sched, liveTsk.ID)
	if len(liveJobsBefore) != 1 {
		t.Fatalf("expected the done-but-repeating task to still have 1 armed job before the loss, got %d",
			len(liveJobsBefore))
	}
	// Remove the job directly from the underlying cron engine WITHOUT going
	// through OnTaskDeleted, so the scheduler's own taskToJob map still
	// "thinks" it is tracking a job that no longer resolves — the exact
	// shape RunRecoverySweep's orphan detection (shouldReArmLocked) targets.
	sched.cs.RemoveJob(liveJobsBefore[0].ID)

	// Task 2: an exhausted (COUNT=1) series, also driven to `done`. Its job
	// was already retired (no job) by OnTaskUpserted's own errRruleExhausted
	// handling — the sweep must NOT resurrect it.
	exhaustedTsk := makeRruleTask(t, store, "agent-sweep-exhausted", "FREQ=DAILY;COUNT=1", dtstart, "UTC")
	sched.OnTaskUpserted(exhaustedTsk)
	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()
	if jobs := jobsForTask(sched, exhaustedTsk.ID); len(jobs) != 0 {
		t.Fatalf("expected the COUNT=1 series to have retired (no job) after its one fire, got %d", len(jobs))
	}
	exhaustedDone, err := store.Update(exhaustedTsk.ID, task.Patch{Status: &doneStatus})
	if err != nil {
		t.Fatalf("store.Update exhausted task to done: %v", err)
	}
	sched.OnTaskUpserted(exhaustedDone)
	if jobs := jobsForTask(sched, exhaustedTsk.ID); len(jobs) != 0 {
		t.Fatalf("exhausted task must still have no job after its done completion, got %d", len(jobs))
	}

	// Run the recovery sweep.
	sched.RunRecoverySweep()

	liveJobsAfterSweep := jobsForTask(sched, liveTsk.ID)
	if len(liveJobsAfterSweep) != 1 {
		t.Fatalf("RunRecoverySweep must re-arm the done-but-repeating task whose job was lost, got %d jobs",
			len(liveJobsAfterSweep))
	}
	wantNext := dtstart + oneDayMs
	if liveJobsAfterSweep[0].Schedule.AtMS == nil || *liveJobsAfterSweep[0].Schedule.AtMS != wantNext {
		t.Errorf("re-armed job AtMS = %v, want %d", derefI64(liveJobsAfterSweep[0].Schedule.AtMS), wantNext)
	}

	if jobs := jobsForTask(sched, exhaustedTsk.ID); len(jobs) != 0 {
		t.Errorf("RunRecoverySweep must NOT resurrect an exhausted (COUNT reached) series, got %d jobs", len(jobs))
	}
}

// TestTriggerOverlapGuard_RecordsSkippedOccurrence covers the run-history
// transparency fix for the overlap guard (task-run-history-spec.md,
// TaskRun.status=skipped): when RunScheduled's SpawnReset hits
// task.ErrAlreadyRunning, a run record must actually be written for the
// occurrence that was skipped — not just logged and silently dropped
// (mirrors TestTriggerOverlapGuard / the "ReArmOnOverlapSkip" subtest's
// setup technique, extended to assert on run-history rather than just the
// re-arm).
func TestTriggerOverlapGuard_RecordsSkippedOccurrence(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	dtstart := secondAligned(clk.Now().Add(time.Minute))
	tsk := makeRruleTask(t, store, "agent-skip-record", "FREQ=DAILY", dtstart, "UTC")
	sched.OnTaskUpserted(tsk)

	jobsBefore := jobsForTask(sched, tsk.ID)
	if len(jobsBefore) != 1 || jobsBefore[0].Schedule.AtMS == nil {
		t.Fatalf("setup: expected 1 armed job with AtMS set, got %+v", jobsBefore)
	}
	wantOccurrenceMs := *jobsBefore[0].Schedule.AtMS

	// Force the overlap guard: SpawnReset returns ErrAlreadyRunning while the
	// task is in_progress (mirrors TestTriggerOverlapGuard's technique).
	inProgress := task.StatusInProgress
	if _, updateErr := store.Update(tsk.ID, task.Patch{Status: &inProgress}); updateErr != nil {
		t.Fatalf("store.Update to in_progress: %v", updateErr)
	}

	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	// No dispatch — the overlap guard fired, exactly as before.
	if n := len(rec.calls()); n != 0 {
		t.Fatalf("overlap guard: expected 0 dispatches, got %d", n)
	}

	// A run record for the skipped occurrence MUST now exist, carrying the
	// exact occurrence_ms the fired job was armed for.
	runs, err := store.RunsInRange(tsk.ID, wantOccurrenceMs, wantOccurrenceMs+1)
	if err != nil {
		t.Fatalf("store.RunsInRange: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected exactly 1 run recorded for the skipped occurrence, got %d (%+v)", len(runs), runs)
	}
	got := runs[0]
	if got.Status != task.StatusSkipped {
		t.Errorf("recorded run status = %q, want %q", got.Status, task.StatusSkipped)
	}
	if got.OccurrenceMs == nil || *got.OccurrenceMs != wantOccurrenceMs {
		t.Errorf("recorded run occurrence_ms = %v, want %d", derefI64(got.OccurrenceMs), wantOccurrenceMs)
	}
	if got.SessionID != "" {
		t.Errorf("skipped run must carry no session_id (no session ever ran), got %q", got.SessionID)
	}
	if got.Kind != task.RunKindScheduled {
		t.Errorf(
			"skipped run kind = %q, want %q (this occurrence WAS the scheduled fire)",
			got.Kind,
			task.RunKindScheduled,
		)
	}
	if got.EndedAt == nil {
		t.Errorf("skipped run must be recorded already-closed (EndedAt set), got nil")
	} else if got.StartedAt != *got.EndedAt {
		t.Errorf("skipped run StartedAt (%q) must equal EndedAt (%q) — recorded in one shot, never opened separately",
			got.StartedAt, *got.EndedAt)
	}

	// The store-level validator and the REST-layer wire mapping's own guard
	// (toWireTaskRun / gen.TaskRunStatus(...).Valid(), pkg/gateway/
	// rest_task_runs.go) both gate on IsValidRunStatus — confirm the new
	// status is accepted there too, so the wire round trip does not silently
	// drop it (task-run-history-spec.md's "IsValidRunStatus/the wire round
	// trip ... don't drop it" requirement).
	if !task.IsValidRunStatus(task.StatusSkipped) {
		t.Errorf("task.IsValidRunStatus(StatusSkipped) = false, want true")
	}

	// The re-arm must still have happened regardless — the skip-recording is
	// a "nice to have," never allowed to block the scheduler's forward
	// progress (Scheduler rule 2/3).
	jobsAfter := jobsForTask(sched, tsk.ID)
	if len(jobsAfter) != 1 {
		t.Fatalf("expected exactly 1 armed job after overlap-skip re-arm, got %d", len(jobsAfter))
	}
	wantNext := dtstart + oneDayMs
	if jobsAfter[0].Schedule.AtMS == nil || *jobsAfter[0].Schedule.AtMS != wantNext {
		t.Errorf("re-armed job AtMS = %v, want %d", derefI64(jobsAfter[0].Schedule.AtMS), wantNext)
	}
}

// TestTriggerOverlapGuard_SuppressesSkipWhenOccurrenceAlreadyRunNow is the
// regression test for the confirmed concurrency-review finding: RD8 lets a
// user Run-now a FUTURE occurrence ahead of its natural schedule. If that
// manual run is STILL in_progress when the SAME occurrence's natural
// scheduled fire arrives, the overlap guard used to unconditionally write a
// SECOND "skipped" TaskRun record for the exact same occurrence_ms — and
// because both the server (task_occurrences.go's runIsNewer) and the client
// (eventMapping.ts's isRunNewer) resolve multiple runs per occurrence by
// "latest StartedAt wins", that skip record (stamped at the LATER natural
// fire time) would permanently out-rank the manual run's own record once it
// closes — mislabeling a task the user successfully ran by hand as
// "skipped".
//
// This test reproduces the exact sequence: OpenRun a manual run for a
// FUTURE occurrence (simulating Run-now-early), force the task in_progress
// past that occurrence's own natural scheduled fire, then let the scheduler
// fire and hit the overlap guard. The fix (Store.RecordSkippedOccurrence's
// occurrenceAlreadyHasRunLocked guard) must suppress the duplicate skip: only
// the ORIGINAL manual run's record may exist for this occurrence_ms
// afterward.
func TestTriggerOverlapGuard_SuppressesSkipWhenOccurrenceAlreadyRunNow(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	dtstart := secondAligned(clk.Now().Add(time.Minute))
	tsk := makeRruleTask(t, store, "agent-run-now-early", "FREQ=DAILY", dtstart, "UTC")
	sched.OnTaskUpserted(tsk)

	jobsBefore := jobsForTask(sched, tsk.ID)
	if len(jobsBefore) != 1 || jobsBefore[0].Schedule.AtMS == nil {
		t.Fatalf("setup: expected 1 armed job with AtMS set, got %+v", jobsBefore)
	}
	occurrenceMs := *jobsBefore[0].Schedule.AtMS

	// RD8: the user Run-now's this exact FUTURE occurrence ahead of its
	// natural schedule — OpenRun records a manual, still-open run keyed to
	// occurrenceMs.
	manualRun, created, err := store.OpenRun(tsk.ID, &occurrenceMs, task.RunKindManual, "session-manual-run-now")
	if err != nil {
		t.Fatalf("store.OpenRun (manual run-now): %v", err)
	}
	if !created {
		t.Fatalf("expected OpenRun to create a new run, got created=false")
	}

	// That manual run is still executing (in_progress) when the occurrence's
	// own natural scheduled fire arrives (mirrors TestTriggerOverlapGuard's
	// technique for forcing the overlap guard).
	inProgress := task.StatusInProgress
	if _, updateErr := store.Update(tsk.ID, task.Patch{Status: &inProgress}); updateErr != nil {
		t.Fatalf("store.Update to in_progress: %v", updateErr)
	}

	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	// No dispatch — the overlap guard fired, exactly as before.
	if n := len(rec.calls()); n != 0 {
		t.Fatalf("overlap guard: expected 0 dispatches, got %d", n)
	}

	// The fix under test: exactly ONE run record must exist for this
	// occurrence — the original manual run — with NO second "skipped"
	// record written alongside it.
	runs, err := store.RunsInRange(tsk.ID, occurrenceMs, occurrenceMs+1)
	if err != nil {
		t.Fatalf("store.RunsInRange: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected exactly 1 run for occurrence_ms=%d (the manual run, no duplicate skip), got %d: %+v",
			occurrenceMs, len(runs), runs)
	}
	got := runs[0]
	if got.RunID != manualRun.RunID {
		t.Errorf("surviving run_id = %q, want the manual run's %q (a skip record must not have been written)",
			got.RunID, manualRun.RunID)
	}
	if got.Status != task.StatusInProgress {
		t.Errorf("surviving run status = %q, want %q — the manual run must be untouched by the overlap guard",
			got.Status, task.StatusInProgress)
	}
	if got.Kind != task.RunKindManual {
		t.Errorf("surviving run kind = %q, want %q", got.Kind, task.RunKindManual)
	}

	// Re-arm must still happen regardless — suppressing the duplicate skip
	// must never block the scheduler's forward progress (Scheduler rule 2/3,
	// same invariant TestTriggerOverlapGuard_RecordsSkippedOccurrence checks).
	jobsAfter := jobsForTask(sched, tsk.ID)
	if len(jobsAfter) != 1 {
		t.Fatalf("expected exactly 1 armed job after overlap-skip re-arm, got %d", len(jobsAfter))
	}
	wantNext := dtstart + oneDayMs
	if jobsAfter[0].Schedule.AtMS == nil || *jobsAfter[0].Schedule.AtMS != wantNext {
		t.Errorf("re-armed job AtMS = %v, want %d", derefI64(jobsAfter[0].Schedule.AtMS), wantNext)
	}
}
