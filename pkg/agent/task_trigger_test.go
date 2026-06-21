// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/task"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// triggerFakeClock is a deterministic clock for TaskTriggerScheduler tests.
// It is set to time.Now().Add(24h) so that after job registration, the cron
// runLoop's real-time timer sleeps for ~24 hours and does not race our manual
// RunDueJobs calls during sub-second tests.
type triggerFakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTriggerFakeClock() *triggerFakeClock {
	// Start 24h in the future so cron runLoop timer sleeps a long time.
	return &triggerFakeClock{t: time.Now().Add(24 * time.Hour)}
}

func (c *triggerFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *triggerFakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// dispatchRecorder is a test-seam dispatch function that records task IDs fired.
type dispatchRecorder struct {
	mu      sync.Mutex
	taskIDs []string
}

func (r *dispatchRecorder) dispatch(_ context.Context, taskID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.taskIDs = append(r.taskIDs, taskID)
	return nil
}

func (r *dispatchRecorder) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.taskIDs))
	copy(out, r.taskIDs)
	return out
}

// newTriggerSchedulerForTest creates a TaskTriggerScheduler with an injected
// fake clock and dispatch recorder, then starts it. The fake clock is set to
// 24h in the future so the cron runLoop timer does not race test assertions.
// The caller drives dispatch via sched.RunDueJobs(clk.Now()).
func newTriggerSchedulerForTest(t *testing.T) (
	sched *TaskTriggerScheduler,
	store *task.Store,
	clk *triggerFakeClock,
	rec *dispatchRecorder,
) {
	t.Helper()
	dir := t.TempDir()
	store = task.New(filepath.Join(dir, "tasks"))
	storePath := filepath.Join(dir, "triggers", "jobs.json")

	sched = NewTaskTriggerScheduler(storePath, store, nil)
	rec = &dispatchRecorder{}
	sched.dispatch = rec.dispatch

	clk = newTriggerFakeClock()
	sched.SetClock(clk)

	if err := sched.Start(); err != nil {
		t.Fatalf("TaskTriggerScheduler.Start: %v", err)
	}
	t.Cleanup(func() { sched.Stop() })

	return sched, store, clk, rec
}

// makeTask creates a minimal valid task in the store and returns it.
func makeTask(t *testing.T, store *task.Store, agentID string, tr *task.Trigger) *task.Task {
	t.Helper()
	tsk := &task.Task{
		Title:       "trigger-test",
		Action:      task.ActionLLM,
		Status:      task.StatusNext,
		AgentID:     agentID,
		WorkspaceID: "ws-test",
		Trigger:     tr,
	}
	if err := store.Create(tsk); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	return tsk
}

// int64P returns a pointer to v.
func int64P(v int64) *int64 { return &v }

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

// TestTriggerOnce_FiresExactlyOnce verifies that a task with trigger type "once"
// fires exactly once when the fake clock is advanced past its at_ms, and that
// SpawnReset was called first (task is status "next" before dispatch).
func TestTriggerOnce_FiresExactlyOnce(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	// Set the trigger fire time to "now + 1 minute" (in fake-clock land).
	fireAtMs := clk.Now().Add(time.Minute).UnixMilli()
	tsk := makeTask(t, store, "agent-a", &task.Trigger{
		Type:   task.TriggerOnce,
		Config: task.TriggerConfig{AtMs: int64P(fireAtMs)},
	})

	// Register the trigger job.
	sched.OnTaskUpserted(tsk)

	// Pre-condition: the task is still in its original state (status next, no reset yet).
	// Advance past the fire time and drive RunDueJobs.
	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 dispatch call, got %d (%v)", len(calls), calls)
	}
	if calls[0] != tsk.ID {
		t.Errorf("dispatch called with task ID %q, want %q", calls[0], tsk.ID)
	}

	// Verify SpawnReset ran: task was reset to "next" before dispatch.
	// We verify by checking the task is still readable (reset succeeded).
	current, err := store.Get(tsk.ID)
	if err != nil {
		t.Fatalf("store.Get after trigger: %v", err)
	}
	// After SpawnReset, status is "next". The dispatch recorder does not
	// actually claim/run the task, so it stays "next".
	if current.Status != task.StatusNext {
		t.Errorf("after SpawnReset+dispatch, task status = %q, want %q",
			current.Status, task.StatusNext)
	}
	// Result and session must have been cleared by SpawnReset.
	if current.Result != "" || current.SessionID != "" {
		t.Errorf("SpawnReset did not clear result/sessionID: result=%q sessionID=%q",
			current.Result, current.SessionID)
	}
}

// TestTriggerEvery_FiresRepeatedly verifies that an "every" trigger fires on
// each clock advance past the interval, producing one dispatch per interval.
func TestTriggerEvery_FiresRepeatedly(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	const intervalMs = int64(60_000) // 1 minute
	tsk := makeTask(t, store, "agent-b", &task.Trigger{
		Type:   task.TriggerEvery,
		Config: task.TriggerConfig{EveryMs: int64P(intervalMs)},
	})
	sched.OnTaskUpserted(tsk)

	// First fire: advance past the first interval.
	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	if n := len(rec.calls()); n != 1 {
		t.Fatalf("after first interval: expected 1 dispatch, got %d", n)
	}

	// Second fire: advance past the second interval.
	// After the first fire, cron recomputes NextRunAtMS = prevFireTime + intervalMs.
	// Advance another interval so the second fire is due.
	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	if n := len(rec.calls()); n != 2 {
		t.Fatalf("after second interval: expected 2 dispatches, got %d", n)
	}
	// Both dispatches must be for the same task ID.
	for i, id := range rec.calls() {
		if id != tsk.ID {
			t.Errorf("dispatch[%d]: got task ID %q, want %q", i, id, tsk.ID)
		}
	}
}

// TestTriggerOverlapGuard verifies that when the task is already in_progress
// (SpawnReset returns ErrAlreadyRunning), RunScheduled performs a benign skip:
// no dispatch, no error, and the cron job continues (not removed).
func TestTriggerOverlapGuard(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	const intervalMs = int64(60_000)
	tsk := makeTask(t, store, "agent-c", &task.Trigger{
		Type:   task.TriggerEvery,
		Config: task.TriggerConfig{EveryMs: int64P(intervalMs)},
	})
	sched.OnTaskUpserted(tsk)

	// Manually set the task to in_progress to simulate an in-flight run.
	inProgress := task.StatusInProgress
	_, err := store.Update(tsk.ID, task.Patch{Status: &inProgress})
	if err != nil {
		t.Fatalf("store.Update to in_progress: %v", err)
	}

	// Fire the trigger.
	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	// The overlap guard must have fired: no dispatch, task still in_progress.
	if n := len(rec.calls()); n != 0 {
		t.Fatalf("overlap guard: expected 0 dispatches, got %d", n)
	}

	// Task status is still in_progress (SpawnReset was blocked).
	current, err := store.Get(tsk.ID)
	if err != nil {
		t.Fatalf("store.Get after overlap skip: %v", err)
	}
	if current.Status != task.StatusInProgress {
		t.Errorf("after overlap skip, task status = %q, want %q",
			current.Status, task.StatusInProgress)
	}
}

// TestTriggerHeartbeatNotRegistered verifies that a task with surface=heartbeat
// is NOT registered in the cron engine, regardless of its trigger type. The
// heartbeat service owns those periodic fires; double-registering here would
// cause two concurrent executions per heartbeat cycle.
func TestTriggerHeartbeatNotRegistered(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	const intervalMs = int64(60_000)
	surf := task.SurfaceHeartbeat
	tsk := makeTask(t, store, "agent-d", &task.Trigger{
		Type:   task.TriggerEvery,
		Config: task.TriggerConfig{EveryMs: int64P(intervalMs)},
	})
	// Patch the surface to heartbeat after creation.
	updated, err := store.Update(tsk.ID, task.Patch{Surface: &surf})
	if err != nil {
		t.Fatalf("store.Update surface: %v", err)
	}

	// Try to register — must be a no-op.
	sched.OnTaskUpserted(updated)

	// Verify no cron job was created.
	jobs := sched.cs.ListJobs(true)
	for _, j := range jobs {
		if j.Payload.Message == tsk.ID {
			t.Fatalf("heartbeat-surface task was registered as a cron job (job ID %q), want no registration", j.ID)
		}
	}

	// Advance and confirm no dispatch.
	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	if n := len(rec.calls()); n != 0 {
		t.Fatalf("heartbeat-surface task triggered %d dispatch(es), want 0", n)
	}
}

// TestTriggerOnTaskDeleted_RemovesJob verifies that OnTaskDeleted removes the
// cron job from the engine so it does not fire after the task is gone.
func TestTriggerOnTaskDeleted_RemovesJob(t *testing.T) {
	sched, store, clk, _ := newTriggerSchedulerForTest(t)

	fireAtMs := clk.Now().Add(time.Minute).UnixMilli()
	tsk := makeTask(t, store, "agent-e", &task.Trigger{
		Type:   task.TriggerOnce,
		Config: task.TriggerConfig{AtMs: int64P(fireAtMs)},
	})
	sched.OnTaskUpserted(tsk)

	// Confirm job registered.
	jobs := sched.cs.ListJobs(true)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job after OnTaskUpserted, got %d", len(jobs))
	}

	// Delete the task from the store and notify the scheduler.
	if _, err := store.Delete(tsk.ID); err != nil {
		t.Fatalf("store.Delete: %v", err)
	}
	sched.OnTaskDeleted(tsk.ID)

	// The cron engine must have no remaining jobs for this task.
	jobs = sched.cs.ListJobs(true)
	for _, j := range jobs {
		if j.Payload.Message == tsk.ID {
			t.Fatalf("job for deleted task still present (job ID %q)", j.ID)
		}
	}
}

// TestTriggerReconcile_RegistersExistingTasks verifies that Reconcile registers
// jobs for all triggered, non-terminal, non-heartbeat tasks already in the store.
func TestTriggerReconcile_RegistersExistingTasks(t *testing.T) {
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	storePath := filepath.Join(dir, "triggers", "jobs.json")

	// Pre-populate the store with two triggered tasks.
	clk := newTriggerFakeClock()
	fireAt := clk.Now().Add(time.Hour).UnixMilli()

	t1 := makeTask(t, store, "agent-x", &task.Trigger{
		Type:   task.TriggerOnce,
		Config: task.TriggerConfig{AtMs: int64P(fireAt)},
	})
	t2 := makeTask(t, store, "agent-x", &task.Trigger{
		Type:   task.TriggerEvery,
		Config: task.TriggerConfig{EveryMs: int64P(int64(60_000))},
	})
	// A manual-trigger task — must NOT be registered.
	t3 := makeTask(t, store, "agent-x", &task.Trigger{
		Type:   task.TriggerManual,
		Config: task.TriggerConfig{},
	})
	// A terminal task — must NOT be registered. Transition next→in_progress→done.
	t4 := makeTask(t, store, "agent-x", &task.Trigger{
		Type:   task.TriggerEvery,
		Config: task.TriggerConfig{EveryMs: int64P(int64(60_000))},
	})
	inProgStatus := task.StatusInProgress
	if _, err := store.Update(t4.ID, task.Patch{Status: &inProgStatus}); err != nil {
		t.Fatalf("store.Update in_progress: %v", err)
	}
	doneStatus := task.StatusDone
	if _, err := store.Update(t4.ID, task.Patch{Status: &doneStatus}); err != nil {
		t.Fatalf("store.Update done: %v", err)
	}

	sched := NewTaskTriggerScheduler(storePath, store, nil)
	var count atomic.Int64
	sched.dispatch = func(_ context.Context, _ string) error {
		count.Add(1)
		return nil
	}
	sched.SetClock(clk)
	if err := sched.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sched.Stop()

	if err := sched.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	jobs := sched.cs.ListJobs(true)
	registered := map[string]bool{}
	for _, j := range jobs {
		registered[j.Payload.Message] = true
	}

	if !registered[t1.ID] {
		t.Errorf("Reconcile did not register once-trigger task %q", t1.ID)
	}
	if !registered[t2.ID] {
		t.Errorf("Reconcile did not register every-trigger task %q", t2.ID)
	}
	if registered[t3.ID] {
		t.Errorf("Reconcile registered manual-trigger task %q (should be skipped)", t3.ID)
	}
	if registered[t4.ID] {
		t.Errorf("Reconcile registered terminal task %q (should be skipped)", t4.ID)
	}
}

// TestTriggerTerminalTask_JobRemoved verifies that when a task transitions to a
// terminal status and OnTaskUpserted is called, the existing trigger job is
// removed.
func TestTriggerTerminalTask_JobRemoved(t *testing.T) {
	sched, store, clk, _ := newTriggerSchedulerForTest(t)

	fireAtMs := clk.Now().Add(time.Hour).UnixMilli()
	tsk := makeTask(t, store, "agent-f", &task.Trigger{
		Type:   task.TriggerOnce,
		Config: task.TriggerConfig{AtMs: int64P(fireAtMs)},
	})
	sched.OnTaskUpserted(tsk)

	if len(sched.cs.ListJobs(true)) != 1 {
		t.Fatalf("expected 1 job after register, got %d", len(sched.cs.ListJobs(true)))
	}

	// Transition to done and notify. Lifecycle: next→in_progress→done.
	inProgStatus := task.StatusInProgress
	if _, err := store.Update(tsk.ID, task.Patch{Status: &inProgStatus}); err != nil {
		t.Fatalf("store.Update in_progress: %v", err)
	}
	doneStatus := task.StatusDone
	updated, err := store.Update(tsk.ID, task.Patch{Status: &doneStatus})
	if err != nil {
		t.Fatalf("store.Update done: %v", err)
	}
	sched.OnTaskUpserted(updated)

	if len(sched.cs.ListJobs(true)) != 0 {
		t.Errorf("expected 0 jobs after task reached terminal status, got %d", len(sched.cs.ListJobs(true)))
	}
}
