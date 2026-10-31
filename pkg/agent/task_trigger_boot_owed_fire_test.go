// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Boot behaviour for crashed one-time task triggers (founder decision
// 2026-10-29, "Missed Calendar runs", Q1 A: "boot-only start fix for crashed
// one-time triggers", train 6b).
//
// The oracle, split into the three cases the decision names:
//
//  1. A one-time trigger whose run the dead process had STARTED is owed that
//     run at the next boot — it fires exactly once, and only via the boot
//     path. dbee8a193 (#1484) fixed this for the user-schedules CronService
//     (StartAfterPhysicalBoot); the task-trigger scheduler's dedicated
//     CronService instance is started with plain Start() and its boot
//     Reconcile re-derives every job from the task store, which drops the
//     engine-armed owed fire again (a past "at" job gets no next run). These
//     tests pin the owed run across that full boot sequence.
//
//  2. A one-time fire that merely came due while Omnipus was down never
//     started, and stays skipped, never replayed (docs/calendar.md).
//
//  3. A same-process scheduler restart (gateway reload constructs a fresh
//     instance and calls plain Start) is NOT a boot: persisted Running flags
//     there belong to live lanes, so nothing is replayed.

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// crashRunRecorder is a dispatch seam for building a crash image: the first
// dispatch claims the task durably exactly as production does
// (SpawnTriggeredRun -> ExecuteTask calls store.ClaimForRun before it
// returns, leaving the task in_progress on disk), closes entered (the run has
// started), then blocks until release, so the test can capture the
// trigger-store file AND the task file exactly as a process death mid-run
// would leave them (6b review I2: a recorder that skipped the claim hid the
// dead in_progress claim the next boot must deal with).
type crashRunRecorder struct {
	store   *task.Store
	clk     *triggerFakeClock
	mu      sync.Mutex
	entered chan struct{}
	closed  bool
	release chan struct{}
}

// newCrashRunRecorder returns a recorder that claims through store at clk's
// time, as the production dispatch does. A nil store models a death inside
// the fire before the task was claimed.
func newCrashRunRecorder(store *task.Store, clk *triggerFakeClock) *crashRunRecorder {
	return &crashRunRecorder{
		store:   store,
		clk:     clk,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *crashRunRecorder) dispatch(_ context.Context, taskID string, _ *int64) error {
	if r.store != nil { // nil store: the process dies before the claim
		if _, err := r.store.ClaimForRun(taskID, r.clk.Now()); err != nil {
			return err
		}
	}
	r.mu.Lock()
	first := !r.closed
	r.closed = true
	r.mu.Unlock()
	if first && r.entered != nil {
		close(r.entered)
	}
	if r.release != nil {
		<-r.release
	}
	return nil
}

// TestTaskTriggerBoot_CrashedOnceRun_RerunsOnceAtNextBoot holds case 1: the
// process dies while a once trigger's run is in progress; the next boot runs
// it exactly once, and the boot Reconcile must not take the armed fire away.
func TestTaskTriggerBoot_CrashedOnceRun_RerunsOnceAtNextBoot(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	storePath := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()

	// Process 1: arm a once trigger a minute out, fire it, and die with the
	// run still going. The crash image is the store file at that moment:
	// NextRunAtMS cleared (RunDueJobs persists before dispatch), Running
	// persisted true (executeJobByID persists when the run starts), and no
	// outcome saved yet.
	blocker := newCrashRunRecorder(store, clk)
	s1 := NewTaskTriggerScheduler(storePath, store, nil)
	s1.dispatch = blocker.dispatch
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

	clk.Advance(2 * time.Minute)
	s1.RunDueJobs(clk.Now())
	select {
	case <-blocker.entered:
	case <-time.After(10 * time.Second):
		s1.Stop()
		t.Fatal("the once fire never started; cannot build a crash image")
	}
	crashImage, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("read crash image: %v", err)
	}
	close(blocker.release)
	s1.WaitForLane()
	s1.Stop()

	// Process 2 boots from the crash image, then reconciles — the production
	// gateway boot sequence for this scheduler. The run the dead process had
	// started is owed and must survive the reconcile.
	if err := os.WriteFile(storePath, crashImage, 0o600); err != nil {
		t.Fatalf("write crash image: %v", err)
	}
	rec2 := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(storePath, store, nil)
	s2.dispatch = rec2.dispatch
	s2.SetClock(clk)
	if err := s2.StartAfterPhysicalBoot(); err != nil { // the fixed boot path
		t.Fatalf("process 2 StartAfterPhysicalBoot: %v", err)
	}
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("process 2 Reconcile: %v", err)
	}
	defer s2.Stop()

	// The owed fire must still be armed after the reconcile — a re-derived
	// past "at" job has no next run, which is exactly the loss this forbids.
	s2.mu.Lock()
	tracked, isTracked := s2.taskToJob[tsk.ID]
	var nextRun *int64
	if isTracked {
		if job, ok := s2.cs.GetJob(tracked.jobID); ok {
			nextRun = job.State.NextRunAtMS
		}
	}
	s2.mu.Unlock()
	if !isTracked || nextRun == nil {
		t.Fatalf("boot reconcile must leave the owed once fire armed; tracked=%v nextRun=%v",
			isTracked, nextRun)
	}

	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	calls := rec2.calls()
	if len(calls) != 1 || calls[0] != tsk.ID {
		t.Fatalf("the run the dead process had started must run exactly once at the next boot; dispatches = %v", calls)
	}

	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	if got := rec2.calls(); len(got) != 1 {
		t.Fatalf("the owed run must not repeat; dispatches = %v", got)
	}
}

// TestTaskTriggerBoot_OnceDueWhileDown_NotReplayed holds case 2: a once
// trigger whose time passed while the process was down never started, and
// stays skipped — the boot path must not turn it into a replay.
func TestTaskTriggerBoot_OnceDueWhileDown_NotReplayed(t *testing.T) {
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
	s1.Stop() // the process goes down BEFORE the fire time

	clk.Advance(2 * time.Minute) // the fire time passes while down

	rec2 := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(storePath, store, nil)
	s2.dispatch = rec2.dispatch
	s2.SetClock(clk)
	if err := s2.StartAfterPhysicalBoot(); err != nil { // the fixed boot path
		t.Fatalf("process 2 StartAfterPhysicalBoot: %v", err)
	}
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("process 2 Reconcile: %v", err)
	}
	defer s2.Stop()

	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	if got := rec2.calls(); len(got) != 0 {
		t.Fatalf("a once fire that came due while down stays skipped, never replayed; dispatches = %v", got)
	}
}

// TestTaskTriggerBoot_PlainStartIsNotABoot_NoReplay holds case 3: a fresh
// scheduler instance started with plain Start — what the gateway reload path
// does within one live process — must not replay a persisted interrupted
// fire. Only the physical-boot path may.
func TestTaskTriggerBoot_PlainStartIsNotABoot_NoReplay(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	storePath := filepath.Join(dir, "triggers", "jobs.json")
	clk := newTriggerFakeClock()

	blocker := newCrashRunRecorder(store, clk)
	s1 := NewTaskTriggerScheduler(storePath, store, nil)
	s1.dispatch = blocker.dispatch
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
	clk.Advance(2 * time.Minute)
	s1.RunDueJobs(clk.Now())
	select {
	case <-blocker.entered:
	case <-time.After(10 * time.Second):
		s1.Stop()
		t.Fatal("the once fire never started; cannot build a crash image")
	}
	crashImage, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("read crash image: %v", err)
	}
	close(blocker.release)
	s1.WaitForLane()
	s1.Stop()

	if err := os.WriteFile(storePath, crashImage, 0o600); err != nil {
		t.Fatalf("write crash image: %v", err)
	}
	// Same-process restart semantics: plain Start, like gateway reload.
	rec2 := &dispatchRecorder{}
	s2 := NewTaskTriggerScheduler(storePath, store, nil)
	s2.dispatch = rec2.dispatch
	s2.SetClock(clk)
	if err := s2.Start(); err != nil {
		t.Fatalf("reload Start: %v", err)
	}
	if err := s2.Reconcile(); err != nil {
		t.Fatalf("reload Reconcile: %v", err)
	}
	defer s2.Stop()

	s2.RunDueJobs(clk.Now())
	s2.WaitForLane()
	if got := rec2.calls(); len(got) != 0 {
		t.Fatalf("plain Start is not a boot; a persisted interrupted fire must not replay; dispatches = %v", got)
	}
}
