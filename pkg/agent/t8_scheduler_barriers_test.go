package agent

// r17 §13.1 test 12e (scenarios 1.13, 18.21, 18.22, 18.25, 18.26) and the
// pre-marker part of test 12d (18.31, 18.35): deterministic interleavings
// built on the test-only barrier seams named in each test. Every barrier
// holds the production code at the exact point the scenario names, never
// under a lock another writer in the scenario needs.

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// t8Gate is a one-shot barrier: the first caller of hold announces itself and
// blocks until open; later callers pass straight through.
type t8Gate struct {
	entered  chan struct{}
	release  chan struct{}
	holding  atomic.Bool
	openOnce sync.Once
}

func newT8Gate(t *testing.T) *t8Gate {
	t.Helper()
	g := &t8Gate{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(g.open) // a failing test never leaves a goroutine blocked
	return g
}

func (g *t8Gate) hold() {
	if g.holding.CompareAndSwap(false, true) {
		close(g.entered)
		<-g.release
	}
}

func (g *t8Gate) open() { g.openOnce.Do(func() { close(g.release) }) }

func (g *t8Gate) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("instrument: the barrier was never reached")
	}
}

// t8ToolSchedFixture is the production update_task wiring (real registry
// tools, task and goal stores) with a started task-trigger scheduler on a
// controlled clock. The scheduler's dispatch is a recorder so that a
// superseded occurrence that fires is observable.
func t8ToolSchedFixture(t *testing.T) (*t8ReadFixture, *TaskTriggerScheduler, *triggerFakeClock, *dispatchRecorder) {
	t.Helper()
	f := t8ReadLoop(t)
	clock := newTriggerFakeClock()
	sched := NewTaskTriggerScheduler(filepath.Join(f.home, "tasks_triggers", "jobs.json"), f.ts, f.al.taskExecutor)
	rec := &dispatchRecorder{}
	sched.dispatch = rec.dispatch
	sched.SetClock(clock)
	require.NoError(t, sched.Start())
	t.Cleanup(sched.Stop)
	f.al.SetTaskTriggerScheduler(sched)
	return f, sched, clock, rec
}

func (f *t8ReadFixture) updateTask(t *testing.T, args map[string]any) *tools.ToolResult {
	t.Helper()
	inst, ok := f.al.GetRegistry().GetAgent("reader-a")
	require.True(t, ok)
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "reader-a"), testHarnessWorkspaceMembershipID)
	return t8RegisteredCall(t, inst, ctx, "update_task", args)
}

func t8OnceArgs(atMs int64) map[string]any {
	return map[string]any{"type": "once", "config": map[string]any{"at_ms": atMs}}
}

// t8RunWriter runs update_task in a goroutine and reports its result.
func (f *t8ReadFixture) runWriter(t *testing.T, args map[string]any) <-chan *tools.ToolResult {
	t.Helper()
	out := make(chan *tools.ToolResult, 1)
	go func() { out <- f.updateTask(t, args) }()
	return out
}

func t8Await(t *testing.T, ch <-chan *tools.ToolResult, what string) *tools.ToolResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(15 * time.Second):
		t.Fatalf("%s did not finish", what)
		return nil
	}
}

func t8AssertStillQueued(t *testing.T, ch <-chan *tools.ToolResult, what string) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("%s completed while the first writer held the coordination lock: %s", what, r.ForLLM)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestT8TaskWrite_ConcurrentPromptWritersGoalFollowsPersistedPrompt covers
// 1.13: two concurrent prompt writers with a barrier between task persistence
// and goal synchronisation; the goal ends equal to the persisted prompt.
func TestT8TaskWrite_ConcurrentPromptWritersGoalFollowsPersistedPrompt(t *testing.T) {
	f, sched, _, _ := t8ToolSchedFixture(t)
	require.NotNil(t, sched, "instrument: scheduler is wired")
	tk := f.task(t, "prompt-race", "reader-a", "reader-a", testHarnessWorkspaceMembershipID)
	g := f.taskGoal(t, tk)

	gate := newT8Gate(t)
	restore := tools.SetAfterTaskPersistTestHook(func(string) { gate.hold() })
	defer restore()

	a := f.runWriter(t, map[string]any{"task_id": tk.ID, "prompt": "Alpha"})
	gate.waitEntered(t)
	b := f.runWriter(t, map[string]any{"task_id": tk.ID, "prompt": "Beta"})
	t8AssertStillQueued(t, b, "writer B")
	mid, err := f.ts.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alpha", mid.Prompt, "only A has persisted while A holds the lock")
	gate.open()

	ra, rb := t8Await(t, a, "writer A"), t8Await(t, b, "writer B")
	require.False(t, ra.IsError, "A: %s", ra.ForLLM)
	require.False(t, rb.IsError, "B: %s", rb.ForLLM)
	got, err := f.ts.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, "Beta", got.Prompt)
	synced, err := f.gs.Get(g.GoalID)
	require.NoError(t, err)
	assert.Equal(t, got.Prompt, synced.Prompt, "goal prompt equals the current persisted task prompt, never an older snapshot")
}

// TestT8Scheduler_QueuedTriggerWriterRegistersLast covers 18.21: writer B is
// queued on the per-task lock while A is held between its write and its
// reconciliation, so the registrations are ordered A then B.
func TestT8Scheduler_QueuedTriggerWriterRegistersLast(t *testing.T) {
	f, sched, clock, rec := t8ToolSchedFixture(t)
	tk := f.task(t, "order-21", "reader-a", "reader-a", testHarnessWorkspaceMembershipID)
	f.taskGoal(t, tk)
	at10 := clock.Now().Add(time.Hour).UnixMilli()
	at11 := clock.Now().Add(2 * time.Hour).UnixMilli()

	gate := newT8Gate(t)
	restore := tools.SetAfterTaskPersistTestHook(func(string) { gate.hold() })
	defer restore()

	a := f.runWriter(t, map[string]any{"task_id": tk.ID, "trigger": t8OnceArgs(at10)})
	gate.waitEntered(t)
	b := f.runWriter(t, map[string]any{"task_id": tk.ID, "trigger": t8OnceArgs(at11)})
	t8AssertStillQueued(t, b, "writer B")
	assert.Empty(t, jobsForTask(sched, tk.ID), "A's reconciliation has not run yet")
	gate.open()

	ra, rb := t8Await(t, a, "writer A"), t8Await(t, b, "writer B")
	require.False(t, ra.IsError, "A: %s", ra.ForLLM)
	require.False(t, rb.IsError, "B: %s", rb.ForLLM)

	got, err := f.ts.Get(tk.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Trigger)
	require.NotNil(t, got.Trigger.Config.AtMs)
	assert.Equal(t, at11, *got.Trigger.Config.AtMs, "storage holds B's 11:00")
	jobs := jobsForTask(sched, tk.ID)
	require.Len(t, jobs, 1)
	require.NotNil(t, jobs[0].State.NextRunAtMS)
	assert.Equal(t, at11, *jobs[0].State.NextRunAtMS, "the registered job equals the persisted trigger")

	clock.Advance(time.Hour + time.Minute) // past the superseded 10:00
	sched.RunDueJobs(clock.Now())
	sched.WaitForLane()
	assert.Empty(t, rec.calls(), "the superseded 10:00 occurrence dispatches nothing")
}

// TestT8Scheduler_QueuedManualClearRemovesJobAfterTriggerChange covers 18.22:
// A changes a weekly trigger and is held inside its ordered reconciliation;
// B's clear to manual is queued and not complete until A releases.
func TestT8Scheduler_QueuedManualClearRemovesJobAfterTriggerChange(t *testing.T) {
	f, sched, clock, rec := t8ToolSchedFixture(t)
	tk := f.task(t, "order-22", "reader-a", "reader-a", testHarnessWorkspaceMembershipID)
	f.taskGoal(t, tk)
	rule, tz := "FREQ=WEEKLY;BYDAY=MO", "UTC"
	start := clock.Now().Add(24 * time.Hour).UnixMilli()
	monday := &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &start, Tz: &tz}}
	armed, err := f.ts.Update(tk.ID, task.Patch{Trigger: &monday})
	require.NoError(t, err)
	sched.OnTaskUpserted(armed)
	before := jobsForTask(sched, tk.ID)
	require.Len(t, before, 1, "instrument: the Monday job is registered")

	gate := newT8Gate(t)
	restore := tools.SetAfterTaskPersistTestHook(func(string) { gate.hold() })
	defer restore()

	tuesday := map[string]any{"type": "recurring", "config": map[string]any{
		"rrule": "FREQ=WEEKLY;BYDAY=TU", "dtstart_ms": start, "tz": "UTC"}}
	a := f.runWriter(t, map[string]any{"task_id": tk.ID, "trigger": tuesday})
	gate.waitEntered(t)
	b := f.runWriter(t, map[string]any{"task_id": tk.ID, "trigger": map[string]any{"type": "manual", "config": map[string]any{}}})
	t8AssertStillQueued(t, b, "writer B")
	mid, err := f.ts.Get(tk.ID)
	require.NoError(t, err)
	require.NotNil(t, mid.Trigger)
	assert.Equal(t, task.TriggerRecurring, mid.Trigger.Type, "B has not persisted its clear while A holds the lock")
	gate.open()

	ra, rb := t8Await(t, a, "writer A"), t8Await(t, b, "writer B")
	require.False(t, ra.IsError, "A: %s", ra.ForLLM)
	require.False(t, rb.IsError, "B: %s", rb.ForLLM)
	final, err := f.ts.Get(tk.ID)
	require.NoError(t, err)
	require.NotNil(t, final.Trigger)
	assert.Equal(t, task.TriggerManual, final.Trigger.Type)
	assert.Empty(t, jobsForTask(sched, tk.ID), "registered state equals persisted state: no job in the live collection")

	clock.Advance(8 * 24 * time.Hour)
	sched.RunDueJobs(clock.Now())
	sched.WaitForLane()
	assert.Empty(t, rec.calls(), "nothing dispatches in the controlled window")
}

func t8DailyTrigger(atMs int64) *task.Trigger {
	rule, tz := "FREQ=DAILY", "UTC"
	return &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &atMs, Tz: &tz}}
}

// TestT8Scheduler_HeldCompletionNotificationRegistersNothingStale covers
// 18.25 at the OnTaskUpserted boundary: the stale producer is held before it
// takes the ordering lock while writer B completes fully.
func TestT8Scheduler_HeldCompletionNotificationRegistersNothingStale(t *testing.T) {
	sched, store, clock, rec := newTriggerSchedulerForTest(t)
	at10 := clock.Now().Add(time.Hour).Truncate(time.Second).UnixMilli() // RRULE instants are whole seconds
	at11 := clock.Now().Add(2 * time.Hour).Truncate(time.Second).UnixMilli()
	tk := &task.Task{Title: "recurring", Action: task.ActionLLM, AgentID: "agent-a", WorkspaceID: "ws-a", Status: task.StatusInbox, Trigger: t8DailyTrigger(at10)}
	require.NoError(t, store.Create(tk))
	sched.OnTaskUpserted(tk)
	require.Len(t, jobsForTask(sched, tk.ID), 1)
	stale, err := store.Get(tk.ID) // the retained 10:00 snapshot
	require.NoError(t, err)

	gate := newT8Gate(t)
	sched.SetUpsertNotifyTestHook(func(string) { gate.hold() })
	done := make(chan struct{})
	go func() { defer close(done); sched.OnTaskUpserted(stale) }()
	gate.waitEntered(t)

	// Writer B completes fully while the stale notification is held.
	replacement := t8DailyTrigger(at11)
	current, err := store.Update(tk.ID, task.Patch{Trigger: &replacement})
	require.NoError(t, err)
	sched.OnTaskUpserted(current)
	jobs := jobsForTask(sched, tk.ID)
	require.Len(t, jobs, 1)
	require.NotNil(t, jobs[0].State.NextRunAtMS)
	require.Equal(t, at11, *jobs[0].State.NextRunAtMS)

	gate.open()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("held notification never finished")
	}
	jobs = jobsForTask(sched, tk.ID)
	require.Len(t, jobs, 1, "the stale snapshot registers nothing new")
	require.NotNil(t, jobs[0].State.NextRunAtMS)
	assert.Equal(t, at11, *jobs[0].State.NextRunAtMS, "registered job still equals the persisted 11:00 trigger")

	clock.Advance(time.Hour + time.Minute)
	sched.RunDueJobs(clock.Now())
	sched.WaitForLane()
	assert.Empty(t, rec.calls(), "10:00 dispatches nothing")
	clock.Advance(time.Hour)
	sched.RunDueJobs(clock.Now())
	sched.WaitForLane()
	assert.Equal(t, []string{tk.ID}, rec.calls(), "11:00 dispatches exactly once")
}

// TestT8Scheduler_HeldRecoverySweepRegistersNothingAfterClear covers 18.26:
// the sweep has its list snapshot and is held before the per-task lock while
// the schedule is cleared; the sweep must re-validate and register nothing.
func TestT8Scheduler_HeldRecoverySweepRegistersNothingAfterClear(t *testing.T) {
	run := func(t *testing.T, doClear bool) (*TaskTriggerScheduler, *task.Task, *dispatchRecorder, *triggerFakeClock) {
		sched, store, clock, rec := newTriggerSchedulerForTest(t)
		at := clock.Now().Add(time.Hour).UnixMilli()
		// A crash orphan: persisted recurring trigger, nothing registered.
		tk := &task.Task{Title: "orphan", Action: task.ActionLLM, AgentID: "agent-a", WorkspaceID: "ws-a", Status: task.StatusInbox, Trigger: t8DailyTrigger(at)}
		require.NoError(t, store.Create(tk))
		require.Empty(t, jobsForTask(sched, tk.ID))

		gate := newT8Gate(t)
		sched.SetSweepRearmTestHook(func(string) { gate.hold() })
		done := make(chan struct{})
		go func() { defer close(done); sched.RunRecoverySweep() }()
		gate.waitEntered(t)
		if doClear {
			manual := &task.Trigger{Type: task.TriggerManual}
			cleared, err := store.Update(tk.ID, task.Patch{Trigger: &manual})
			require.NoError(t, err)
			sched.OnTaskUpserted(cleared)
			require.Empty(t, jobsForTask(sched, tk.ID))
		}
		gate.open()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("held sweep never finished")
		}
		return sched, tk, rec, clock
	}

	t.Run("cleared while the sweep is held", func(t *testing.T) {
		sched, tk, rec, clock := run(t, true)
		assert.Empty(t, jobsForTask(sched, tk.ID), "live job collection holds nothing after the sweep")
		clock.Advance(25 * time.Hour)
		sched.RunDueJobs(clock.Now())
		sched.WaitForLane()
		assert.Empty(t, rec.calls())
	})
	t.Run("control: unchanged trigger is re-armed by the same held sweep", func(t *testing.T) {
		sched, tk, _, _ := run(t, false)
		assert.Len(t, jobsForTask(sched, tk.ID), 1, "the barrier really sits on the registering path")
	})
}

// TestT8Scheduler_StaleFirePathRecordsNothing covers 18.35: a collected
// occurrence is held before the marker lock while the trigger is replaced
// (a) or cleared and reattached (b); the resumed fire records nothing.
func TestT8Scheduler_StaleFirePathRecordsNothing(t *testing.T) {
	for _, mode := range []string{"a replace with future once", "b clear and reattach past once"} {
		t.Run(mode, func(t *testing.T) {
			sched, store, clock, rec := newTriggerSchedulerForTest(t)
			at10 := clock.Now().Add(time.Minute).UnixMilli()
			tk := &task.Task{Title: "stale fire", Action: task.ActionLLM, AgentID: "agent-a", WorkspaceID: "ws-a", Status: task.StatusInbox, Trigger: &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at10}}}
			require.NoError(t, store.Create(tk))
			sched.OnTaskUpserted(tk)
			oldJobs := jobsForTask(sched, tk.ID)
			require.Len(t, oldJobs, 1)

			gate := newT8Gate(t)
			sched.SetPreMarkerLockTestHook(func(string, string) { gate.hold() })
			clock.Advance(time.Minute + 5*time.Second)
			sched.RunDueJobs(clock.Now())
			gate.waitEntered(t)
			require.Nil(t, t8StoredMarker(t, store, tk.ID), "instrument: nothing recorded before the marker lock")

			at11 := clock.Now().Add(time.Hour).UnixMilli()
			if mode == "a replace with future once" {
				next := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at11}}
				changed, err := store.Update(tk.ID, task.Patch{Trigger: &next})
				require.NoError(t, err)
				sched.OnTaskUpserted(changed)
				require.Len(t, jobsForTask(sched, tk.ID), 1)
			} else {
				manual := &task.Trigger{Type: task.TriggerManual}
				changed, err := store.Update(tk.ID, task.Patch{Trigger: &manual})
				require.NoError(t, err)
				sched.OnTaskUpserted(changed)
				require.Empty(t, jobsForTask(sched, tk.ID), "the clear removed the old registration")
				again := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at10}}
				changed, err = store.Update(tk.ID, task.Patch{Trigger: &again})
				require.NoError(t, err)
				sched.OnTaskUpserted(changed)
				require.Empty(t, jobsForTask(sched, tk.ID), "a past once is never armed")
			}
			gate.open()
			sched.WaitForLane()

			assert.Nil(t, t8StoredMarker(t, store, tk.ID), "the stale callback records no marker")
			assert.Empty(t, rec.calls(), "the stale callback dispatches nothing")
			if mode == "a replace with future once" {
				clock.Advance(time.Hour)
				sched.RunDueJobs(clock.Now())
				sched.WaitForLane()
				assert.Equal(t, []string{tk.ID}, rec.calls(), "the new occurrence dispatches exactly once")
				assert.Equal(t, float64(at11), t8StoredMarker(t, store, tk.ID), "marker is the new scheduled instant")
			}
		})
	}
}

// TestT8TaskTrigger_PendingDeliveryHeldBeforeMarkerProtectsPlan covers 18.31:
// the occurrence is collected (reservation published) and the fire path is
// held before the marker write; plan passes and the idle sweep run meanwhile.
func TestT8TaskTrigger_PendingDeliveryHeldBeforeMarkerProtectsPlan(t *testing.T) {
	al, sched, clock, pe, p := t8PlanWaitFixture(t)
	at := clock.Now().Add(14 * 24 * time.Hour).UnixMilli()
	member := t8PlanMember(t, al, p, "pre-marker member", &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}, nil)
	sched.OnTaskUpserted(member)
	jobs := jobsForTask(sched, member.ID)
	require.Len(t, jobs, 1)

	gate := newT8Gate(t)
	sched.SetPreMarkerLockTestHook(func(string, string) { gate.hold() })
	clock.Advance(14 * 24 * time.Hour)
	sched.RunDueJobs(clock.Now())
	gate.waitEntered(t)
	require.True(t, sched.cs.JobDispatchInFlight(jobs[0].ID), "instrument: delivery is pending")

	before := t8SessionCount(t, al)
	for range 2 {
		pe.processPlan(context.Background(), p.ID)
		pe.idleExpirySweep()
		got, err := al.taskStore.Get(member.ID)
		require.NoError(t, err)
		assert.Equal(t, task.StatusInbox, got.Status, "pending delivery is not settled, so not promoted")
		t8AssertNoFalseWaitEffects(t, al, pe, p, before)
	}
	assert.Nil(t, t8StoredMarker(t, al.taskStore, member.ID), "no marker before the barrier releases")

	gate.open()
	sched.WaitForLane()
	al.taskExecutor.wg.Wait()
	assert.Equal(t, float64(at), t8StoredMarker(t, al.taskStore, member.ID), "marker is the occurrence's scheduled instant")
	pe.processPlan(context.Background(), p.ID)
	al.taskExecutor.CheckQueuedTasks(context.Background())
	al.taskExecutor.wg.Wait()
	runs, err := al.taskStore.ListRuns(member.ID)
	require.NoError(t, err)
	assert.Len(t, runs, 1, "the released delivery dispatches exactly once")
	stored, err := pe.planStore.Get(p.ID)
	require.NoError(t, err)
	assert.NotEqual(t, plan.StateFailed, stored.State)
}
