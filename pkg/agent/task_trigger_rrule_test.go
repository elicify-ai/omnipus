// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		if _, err := store.Update(tsk.ID, task.Patch{Status: &inProgress}); err != nil {
			t.Fatalf("store.Update to in_progress: %v", err)
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
		sched.dispatch = func(ctx context.Context, taskID string) error {
			_ = rec.dispatch(ctx, taskID)
			return errors.New("boom: simulated dispatch failure")
		}

		clk.Advance(2 * time.Minute)
		sched.RunDueJobs(clk.Now())
		sched.WaitForLane()

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

		// Lock down the task store directory so SpawnReset's write fails while
		// store.Get (read-only, already succeeded before this) is unaffected —
		// forcing the SpawnReset-other-error exit path deterministically without
		// touching production code.
		taskDir := store.Dir()
		if err := os.Chmod(taskDir, 0o500); err != nil {
			t.Fatalf("chmod taskDir: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(taskDir, 0o700) })

		clk.Advance(2 * time.Minute)
		sched.RunDueJobs(clk.Now())
		sched.WaitForLane()

		if err := os.Chmod(taskDir, 0o700); err != nil {
			t.Fatalf("restore chmod: %v", err)
		}

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

	t.Run("SweepHealsBothOrphanShapesButSparesInFlight", func(t *testing.T) {
		sched, store, clk, rec := newTriggerSchedulerForTest(t)

		// (a) Untracked orphan: exists in the store, non-terminal, RRULE — but
		// OnTaskUpserted was never called, so the scheduler never tracked it.
		farDtstart := secondAligned(clk.Now().Add(10 * 24 * time.Hour))
		untrackedTsk := makeRruleTask(t, store, "agent-orphan-untracked", "FREQ=DAILY", farDtstart, "UTC")

		// (b) Dead-entry orphan: tracked and registered, then surgically
		// mutated to the exact "crash between RunDueJobs' clear and the
		// dispatch goroutine setting Running=true" shape the engine is known to
		// produce: NextRunAtMS cleared, Running false, job still present.
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
		sched.dispatch = func(ctx context.Context, taskID string) error {
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

		sched.RunRecoverySweep()

		// (a) and (b) must be healed.
		if jobs := jobsForTask(sched, untrackedTsk.ID); len(jobs) != 1 {
			t.Fatalf("sweep did not re-arm the untracked orphan, got %d jobs", len(jobs))
		} else if jobs[0].State.NextRunAtMS == nil {
			t.Fatal("untracked-orphan re-armed job has a nil NextRunAtMS")
		}
		if jobs := jobsForTask(sched, deadTsk.ID); len(jobs) != 1 {
			t.Fatalf("sweep did not re-arm the dead-entry orphan, got %d jobs", len(jobs))
		} else if jobs[0].ID == deadJob.ID {
			t.Fatal("dead-entry orphan job was not replaced (same job ID survived)")
		} else if jobs[0].State.NextRunAtMS == nil {
			t.Fatal("dead-entry orphan re-armed job has a nil NextRunAtMS")
		}

		// (c) must be left completely untouched by the sweep: same job ID,
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
