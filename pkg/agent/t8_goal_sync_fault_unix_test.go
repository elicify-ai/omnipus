//go:build !windows

package agent

// r17 1.14: the tool-side combined writer, real goal write refusal and real
// cron registration. This is distinct from the REST half of the same row.
import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8GoalSync_WriteFailureDoesNotStrandSchedule(t *testing.T) {
	f := t8ReadLoop(t)
	clock := newTriggerFakeClock()
	sched := NewTaskTriggerScheduler(cron.NewCronService(filepath.Join(f.home, "tasks_triggers", "jobs.json")), f.ts, f.al.taskExecutor)
	sched.SetClock(clock)
	require.NoError(t, sched.Start())
	t.Cleanup(sched.Stop)
	f.al.SetTaskTriggerScheduler(sched)
	tk := f.task(t, "goal-failure-schedule", "reader-a", "reader-a", testHarnessWorkspaceMembershipID)
	g := f.taskGoal(t, tk)
	oldAt, newAt := clock.Now().Add(time.Hour).UnixMilli(), clock.Now().Add(2*time.Hour).UnixMilli()
	oldTrigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &oldAt}}
	tk, err := f.ts.Update(tk.ID, task.Patch{Trigger: &oldTrigger})
	require.NoError(t, err)
	sched.OnTaskUpserted(tk)
	require.Len(t, sched.cs.ListJobs(true), 1, "instrument: old occurrence is armed")
	beforeGoal, err := f.gs.Get(g.GoalID)
	require.NoError(t, err)
	dir := f.gs.Dir()
	require.NoError(t, os.Chmod(dir, 0o500))
	defer func() { require.NoError(t, os.Chmod(dir, 0o700)) }()
	probe, fault := os.CreateTemp(dir, ".t8-goal-write-probe-*")
	if fault == nil {
		require.NoError(t, probe.Close())
		require.NoError(t, os.Remove(probe.Name()))
		t.Fatal("BLOCKED: process bypasses write permissions; real goal staging fault unavailable — required by r17 1.14")
	}
	require.ErrorIs(t, fault, os.ErrPermission)
	_, err = f.gs.Get(g.GoalID)
	require.NoError(t, err, "instrument: goal reads still work; only writes are refused")
	r := f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "update_task", map[string]any{"task_id": tk.ID, "prompt": "Beta goal and schedule", "trigger": map[string]any{"type": "once", "config": map[string]any{"at_ms": newAt}}})
	require.False(t, r.IsError, "1.14 tool reports a persisted update, not total failure: %s", r.ForLLM)
	assert.Contains(t, r.ForLLM, "goal_sync_warning")
	after, err := f.ts.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, "Beta goal and schedule", after.Prompt)
	require.NotNil(t, after.Trigger)
	assert.Equal(t, &newAt, after.Trigger.Config.AtMs)
	afterGoal, err := f.gs.Get(g.GoalID)
	require.NoError(t, err)
	assert.Equal(t, beforeGoal, afterGoal, "failed goal write remains stale, not fabricated as synced")
	jobs := sched.cs.ListJobs(true)
	require.Len(t, jobs, 1)
	require.NotNil(t, jobs[0].State.NextRunAtMS)
	assert.Equal(t, newAt, *jobs[0].State.NextRunAtMS, "goal failure must not leave the old occurrence armed")
	clock.Advance(time.Hour)
	sched.RunDueJobs(clock.Now())
	sched.WaitForLane()
	runs, err := f.ts.ListRuns(tk.ID)
	require.NoError(t, err)
	assert.Empty(t, runs, "the superseded old occurrence never dispatches")
}
