package agent

// r17 TK-30: exercise available real filesystem and cron-lane boundaries.
// The remaining pre-lock/snapshot barriers are distinct, recorded gaps.
import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8ScheduleSync_RealCronSaveFailureAfterAppend(t *testing.T) {
	f := t8ReadLoop(t)
	clock := newTriggerFakeClock()
	path := filepath.Join(f.home, "tasks_triggers", "jobs.json")
	sched := NewTaskTriggerScheduler(path, f.ts, f.al.taskExecutor)
	sched.SetClock(clock)
	require.NoError(t, sched.Start())
	t.Cleanup(sched.Stop)
	f.al.SetTaskTriggerScheduler(sched)
	tk := f.task(t, "schedule-save-fault", "reader-a", "reader-a", testHarnessWorkspaceMembershipID)
	g := f.taskGoal(t, tk)
	oldAt := clock.Now().Add(time.Hour).UnixMilli()
	newAt := clock.Now().Add(2 * time.Hour).UnixMilli()
	oldTrigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &oldAt}}
	tk, err := f.ts.Update(tk.ID, task.Patch{Trigger: &oldTrigger})
	require.NoError(t, err)
	sched.OnTaskUpserted(tk)
	require.Len(t, sched.cs.ListJobs(true), 1, "instrument: initial registration really exists in the live engine")
	// A directory at the exact save destination makes the actual atomic
	// replacement fail even as root. The live engine appends before this save.
	backup := path + ".saved"
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700))
	restored := false
	restore := func() {
		if !restored {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Rename(backup, path))
			restored = true
		}
	}
	defer restore()
	inst, ok := f.al.GetRegistry().GetAgent("reader-a")
	require.True(t, ok)
	r := t8RegisteredCall(t, inst, tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "reader-a"), testHarnessWorkspaceMembershipID), "update_task", map[string]any{"task_id": tk.ID, "prompt": "Beta task and goal", "trigger": map[string]any{"type": "once", "config": map[string]any{"at_ms": newAt}}})
	require.False(t, r.IsError, "1.15 task saved; scheduler failure is reported as warning: %s", r.ForLLM)
	assert.Contains(t, r.ForLLM, "schedule_sync_warning", "scheduler-save failure must never silently disappear")
	after, err := f.ts.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, "Beta task and goal", after.Prompt)
	require.NotNil(t, after.Trigger)
	assert.Equal(t, &newAt, after.Trigger.Config.AtMs)
	synced, err := f.gs.Get(g.GoalID)
	require.NoError(t, err)
	assert.Equal(t, "Beta task and goal", synced.Prompt, "scheduler failure does not skip goal sync")
	assert.Empty(t, sched.cs.ListJobs(true), "best-effort de-arm must remove even an untracked job appended before the failed save")
	restore()
	require.NoError(t, sched.Reconcile())
	jobs := sched.cs.ListJobs(true)
	require.Len(t, jobs, 1)
	require.NotNil(t, jobs[0].State.NextRunAtMS)
	assert.Equal(t, newAt, *jobs[0].State.NextRunAtMS, "saved trigger remains authoritative for recovery")
}

func TestT8Scheduler_StaleProducerCannotRestoreClearedTrigger(t *testing.T) {
	// Producer-boundary assertion, not a claim that a recovery-snapshot or
	// terminal-callback pre-lock hook has been executed.
	for _, mode := range []string{"manual clear", "new occurrence"} {
		t.Run(mode, func(t *testing.T) {
			sched, store, clock, rec := newTriggerSchedulerForTest(t)
			at := clock.Now().Add(time.Hour).UnixMilli()
			newAt := clock.Now().Add(2 * time.Hour).UnixMilli()
			old := &task.Task{Title: "deferred producer", Action: task.ActionLLM, AgentID: "agent-a", WorkspaceID: "ws-a", Status: task.StatusInbox, Trigger: &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}}
			require.NoError(t, store.Create(old))
			sched.OnTaskUpserted(old)
			require.Len(t, jobsForTask(sched, old.ID), 1)
			replacement := &task.Trigger{Type: task.TriggerManual}
			if mode == "new occurrence" {
				replacement = &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &newAt}}
			}
			current, err := store.Update(old.ID, task.Patch{Trigger: &replacement})
			require.NoError(t, err)
			sched.OnTaskUpserted(current)
			sched.OnTaskUpserted(old) // retained stale snapshot arrives after the new write fully completed
			jobs := jobsForTask(sched, old.ID)
			if mode == "manual clear" {
				assert.Empty(t, jobs, "18.25/18.26 stale producer cannot resurrect the removed registration")
			} else {
				require.Len(t, jobs, 1)
				require.NotNil(t, jobs[0].State.NextRunAtMS)
				assert.Equal(t, newAt, *jobs[0].State.NextRunAtMS)
			}
			clock.Advance(time.Hour + time.Second)
			sched.RunDueJobs(clock.Now())
			sched.WaitForLane()
			assert.Empty(t, rec.calls(), "the superseded occurrence never dispatches")
		})
	}
}

func TestT8TaskTrigger_PendingLaneWaitProtectsPlan(t *testing.T) {
	al, sched, clock, pe, p := t8PlanWaitFixture(t)
	sched.cs.SetAdmissionRule(func() (int, bool) { return 1, true })
	at := clock.Now().Add(14 * 24 * time.Hour).UnixMilli()
	// Hold an unrelated recurring callback at the existing pre-rearm seam.
	// This occupies the real cron lane without replacing the task dispatcher.
	rule, tz := "FREQ=DAILY", "UTC"
	blocker := &task.Task{Title: "lane blocker", Prompt: "do the lane control", Action: task.ActionLLM, AgentID: "native-agent", WorkspaceID: "default", Status: task.StatusInbox, Criteria: []task.AcceptanceCriterion{planProseCriterion("the scheduled work is verified")}, Trigger: &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &at, Tz: &tz}}}
	require.NoError(t, al.taskStore.Create(blocker))
	pairTaskGoalRecordUsingTaskCriteria(t, al, blocker.ID)
	sched.OnTaskUpserted(blocker)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var enterOnce sync.Once
	sched.SetRearmTestHook(func() { enterOnce.Do(func() { close(entered); <-release }) })
	clock.Advance(14 * 24 * time.Hour)
	sched.RunDueJobs(clock.Now())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: real cron lane never reached the held recurring callback")
	}
	// T's due registration is collected only after the lane is proven occupied.
	member := t8PlanMember(t, al, p, "pending scheduled member", &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}, nil)
	// Arm strictly before its instant under the controlled clock, not at/past it.
	clock.Advance(-time.Second)
	sched.OnTaskUpserted(member)
	jobs := jobsForTask(sched, member.ID)
	require.Len(t, jobs, 1)
	clock.Advance(time.Second)
	sched.RunDueJobs(clock.Now())
	require.True(t, sched.cs.JobDispatchInFlight(jobs[0].ID), "18.32 real pending reservation, not a simulated boolean")
	before := t8SessionCount(t, al)
	for range 3 {
		pe.processPlan(context.Background(), p.ID)
		pe.idleExpirySweep()
		got, err := al.taskStore.Get(member.ID)
		require.NoError(t, err)
		assert.Equal(t, task.StatusInbox, got.Status, "queued delivery is still awaiting, not promoted")
		t8AssertNoFalseWaitEffects(t, al, pe, p, before)
	}
	// Release before any fatal assertion about outcome; cleanup also releases
	// on RED so the foreground runner cannot leave a blocked lane behind.
	unblock()
	sched.WaitForLane()
	al.taskExecutor.wg.Wait()
	pe.processPlan(context.Background(), p.ID)
	al.taskExecutor.CheckQueuedTasks(context.Background())
	al.taskExecutor.wg.Wait()
	runs, err := al.taskStore.ListRuns(member.ID)
	require.NoError(t, err)
	assert.Len(t, runs, 1, "settled delivery releases exactly one execution")
	assert.False(t, sched.cs.JobDispatchInFlight(jobs[0].ID), "original collected job reservation settled")
	stored, err := pe.planStore.Get(p.ID)
	require.NoError(t, err)
	assert.NotEqual(t, plan.StateFailed, stored.State, "legitimate pending wait cannot idle-expire")
}
