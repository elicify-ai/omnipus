package agent

// Oracle: r17 §7.2 TK-30 and scenarios 18.11–18.14. Real production
// create/update tools, stores, scheduler, ordinary drain and task executor.
// Only the provider and clock are process-edge fixtures. No dispatch function
// is replaced. This boundary does not prove the separate approval-policy rows.
import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t8NativeSchedulingFixture(t *testing.T) (*AgentLoop, *TaskTriggerScheduler, *triggerFakeClock) {
	t.Helper()
	al := newNativeTaskCompletionTestLoop(t, newClaimingWorker(turnClaimMet("the scheduled work is verified")))
	// The native execution helper has no Board record. Seed the real record
	// where the production create tool's configured resolver reads it.
	require.NoError(t, workspace.SaveRecord(filepath.Dir(al.GetConfig().AgentHomeBasePath()), workspace.Workspace{
		ID: "default", Name: "T8 local workspace", Status: "active", IsDefault: true, CoreTeam: []string{"native-agent"},
	}))
	clock := newTriggerFakeClock()
	// Plan timestamps persist at second precision; align the controlled clock
	// so min/exact/max expiry probes are not shifted by discarded fractions.
	clock.mu.Lock()
	clock.t = clock.t.Truncate(time.Second)
	clock.mu.Unlock()
	sched := NewTaskTriggerScheduler(filepath.Join(config.OmnipusHomeDir(), "tasks_triggers", "jobs.json"), al.taskStore, al.taskExecutor)
	sched.SetClock(clock)
	require.NoError(t, sched.Start())
	al.SetTaskTriggerScheduler(sched)
	t.Cleanup(sched.Stop)
	return al, sched, clock
}
func t8NativeTool(t *testing.T, al *AgentLoop, name string) tools.Tool {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	tool, ok := inst.Tools.Get(name)
	require.True(t, ok, "production %s tool is registered", name)
	return tool
}
func t8NativeTaskCallContext() context.Context {
	return tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "native-agent"), "default")
}
func t8CreatedTask(t *testing.T, al *AgentLoop, title string) *task.Task {
	t.Helper()
	list, err := al.taskStore.List(task.Filter{})
	require.NoError(t, err)
	for i := range list {
		if list[i].Title == title {
			return &list[i]
		}
	}
	t.Fatalf("create_task did not persist title %q", title)
	return nil
}
func t8CreateSchedulingArgs(title string) map[string]any {
	return map[string]any{"title": title, "prompt": "do and verify the scheduled work", "agent_id": "native-agent", "criteria": []any{map[string]any{"text": "the scheduled work is verified"}}, "dod": []any{map[string]any{"text": "the work is complete"}}}
}
func t8SessionCount(t *testing.T, al *AgentLoop) int {
	t.Helper()
	sessions, err := al.GetSessionStore().ListSessions()
	require.NoError(t, err)
	return len(sessions)
}

func TestTaskTrigger_ScheduledTaskNotRunEarly_ProductionWiring(t *testing.T) {
	for _, kind := range []string{"once", "recurring", "manual"} {
		t.Run(kind, func(t *testing.T) {
			al, sched, clock := t8NativeSchedulingFixture(t)
			at := clock.Now().Add(24 * time.Hour).UnixMilli()
			args := t8CreateSchedulingArgs("T8 " + kind)
			if kind == "once" {
				args["trigger"] = map[string]any{"type": "once", "config": map[string]any{"at_ms": at}}
			}
			if kind == "recurring" {
				args["trigger"] = map[string]any{"type": "recurring", "config": map[string]any{"rrule": "FREQ=DAILY;COUNT=3", "dtstart_ms": at, "tz": "UTC"}}
			}
			sessionsBefore := t8SessionCount(t, al)
			result := t8NativeTool(t, al, "create_task").Execute(t8NativeTaskCallContext(), args)
			require.False(t, result.IsError, "valid production create: %s", result.ForLLM)
			tk := t8CreatedTask(t, al, args["title"].(string))
			if kind == "manual" {
				require.Equal(t, task.StatusNext, tk.Status, "18.13 ordinary pickup control")
				al.taskExecutor.CheckQueuedTasks(context.Background())
				al.taskExecutor.wg.Wait()
				runs, err := al.taskStore.ListRuns(tk.ID)
				require.NoError(t, err)
				require.Len(t, runs, 1, "manual task picked up immediately")
				assert.Greater(t, t8SessionCount(t, al), sessionsBefore, "manual control actually creates a real task session")
				return
			}
			assert.Equal(t, task.StatusInbox, tk.Status, "18.11/18.12 scheduled create waits, never next")
			require.NotNil(t, tk.Trigger, "tool must persist its trigger before any scheduler claim")
			require.Len(t, sched.cs.ListJobs(true), 1, "production notification arms exactly one live job")
			for range 3 {
				al.taskExecutor.CheckQueuedTasks(context.Background())
				al.taskExecutor.wg.Wait()
				runs, err := al.taskStore.ListRuns(tk.ID)
				require.NoError(t, err)
				assert.Empty(t, runs, "no pre-occurrence dispatch")
				assert.Equal(t, sessionsBefore, t8SessionCount(t, al), "no pre-occurrence session")
			}
			clock.Advance(24 * time.Hour)
			sched.RunDueJobs(clock.Now())
			sched.WaitForLane()
			al.taskExecutor.wg.Wait()
			al.taskExecutor.CheckQueuedTasks(context.Background())
			al.taskExecutor.wg.Wait()
			runs, err := al.taskStore.ListRuns(tk.ID)
			require.NoError(t, err)
			require.Len(t, runs, 1, "one real execution at first occurrence")
			assert.Equal(t, &at, runs[0].OccurrenceMs, "execution belongs to scheduled instant")
			sched.RunDueJobs(clock.Now())
			sched.WaitForLane()
			al.taskExecutor.wg.Wait()
			runs, err = al.taskStore.ListRuns(tk.ID)
			require.NoError(t, err)
			assert.Len(t, runs, 1, "no repeat delivery at the same boundary")
			if kind == "recurring" {
				require.Len(t, sched.cs.ListJobs(true), 1, "series continues after first occurrence")
			}
		})
	}
}

func TestTaskTrigger_AttachmentMovesNextToWaiting_ProductionWiring(t *testing.T) {
	al, sched, clock := t8NativeSchedulingFixture(t)
	args := t8CreateSchedulingArgs("T8 attach before drain")
	result := t8NativeTool(t, al, "create_task").Execute(t8NativeTaskCallContext(), args)
	require.False(t, result.IsError, "manual creation: %s", result.ForLLM)
	tk := t8CreatedTask(t, al, args["title"].(string))
	require.Equal(t, task.StatusNext, tk.Status)
	before := t8SessionCount(t, al)
	at := clock.Now().Add(24 * time.Hour).UnixMilli()
	result = t8NativeTool(t, al, "update_task").Execute(t8NativeTaskCallContext(), map[string]any{"task_id": tk.ID, "trigger": map[string]any{"type": "recurring", "config": map[string]any{"rrule": "FREQ=DAILY;COUNT=3", "dtstart_ms": at, "tz": "UTC"}}})
	require.False(t, result.IsError, "18.14 trigger-only update through production wiring: %s", result.ForLLM)
	updated, err := al.taskStore.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusInbox, updated.Status)
	require.Len(t, sched.cs.ListJobs(true), 1)
	al.taskExecutor.CheckQueuedTasks(context.Background())
	al.taskExecutor.wg.Wait()
	runs, err := al.taskStore.ListRuns(tk.ID)
	require.NoError(t, err)
	assert.Empty(t, runs)
	assert.Equal(t, before, t8SessionCount(t, al))
	result = t8NativeTool(t, al, "update_task").Execute(t8NativeTaskCallContext(), map[string]any{"task_id": tk.ID, "trigger": map[string]any{"type": "manual", "config": map[string]any{}}})
	require.False(t, result.IsError, "18.14 manual clear: %s", result.ForLLM)
	updated, err = al.taskStore.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusInbox, updated.Status, "manualization never activates")
	assert.Empty(t, sched.cs.ListJobs(true), "real live job removed")
}
