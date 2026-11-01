package agent

// r17 TK-30: real settled delivery, then real stored-trigger mutations. No
// test-owned marker, fake delivery flag, or callback is used. The 6b exception
// is separate: every task here starts with no task run, so none is an
// unfinished-started occurrence that must be replayed once at physical boot.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t8StoredMarker(t *testing.T, store *task.Store, id string) any {
	t.Helper()
	got, err := store.Get(id)
	require.NoError(t, err)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	return fields["last_delivered_at_ms"]
}
func TestT8DeliveryMarker_SettledInvalidationAndIdenticalSave(t *testing.T) {
	for _, kind := range []string{"18.33a clear and reattach", "18.33b1 replace future", "18.33c identical save", "18.36 other edit and sweep"} {
		t.Run(kind, func(t *testing.T) {
			al, sched, clock, pe, p := t8PlanWaitFixture(t)
			predecessor := &task.Task{Title: "held predecessor", Action: task.ActionLLM, AgentID: "native-agent", WorkspaceID: "default", Status: task.StatusInbox}
			require.NoError(t, al.taskStore.Create(predecessor))
			at := clock.Now().Add(time.Minute).UnixMilli()
			trigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}
			member := t8PlanMember(t, al, p, "delivered but dependency-held", trigger, []string{predecessor.ID})
			sched.OnTaskUpserted(member)
			jobs := jobsForTask(sched, member.ID)
			require.Len(t, jobs, 1)
			clock.Advance(time.Minute + 5*time.Second)
			sched.RunDueJobs(clock.Now())
			sched.WaitForLane()
			al.taskExecutor.wg.Wait()
			require.False(t, sched.cs.JobDispatchInFlight(jobs[0].ID), "no pending callback can hide a later stale-marker restoration")
			runs, err := al.taskStore.ListRuns(member.ID)
			require.NoError(t, err)
			require.Empty(t, runs, "remaining dependency actually held dispatch")
			require.Equal(t, float64(at), t8StoredMarker(t, al.taskStore, member.ID), "18.33 the actual fire persists its scheduled instant, not the five-second-late wall time")
			change := trigger
			switch kind {
			case "18.33a clear and reattach":
				manual := &task.Trigger{Type: task.TriggerManual}
				changed, err := al.taskStore.Update(member.ID, task.Patch{Trigger: &manual})
				require.NoError(t, err)
				assert.Nil(t, t8StoredMarker(t, al.taskStore, member.ID), "clear atomically invalidates delivery before any scheduler callback")
				sched.OnTaskUpserted(changed)
			case "18.33b1 replace future":
				future := clock.Now().Add(time.Hour).UnixMilli()
				change = &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &future}}
			case "18.36 other edit and sweep":
				title := "edited while held"
				changed, err := al.taskStore.Update(member.ID, task.Patch{Title: &title})
				require.NoError(t, err)
				sched.OnTaskUpserted(changed)
				sched.RunRecoverySweep()
			}
			changed, err := al.taskStore.Update(member.ID, task.Patch{Trigger: &change})
			require.NoError(t, err)
			want := any(nil)
			if kind == "18.33c identical save" || kind == "18.36 other edit and sweep" {
				want = float64(at)
			}
			assert.Equal(t, want, t8StoredMarker(t, al.taskStore, member.ID), "invalidation/preservation is in the same task write, before reconciliation")
			sched.OnTaskUpserted(changed)
			pe.processPlan(context.Background(), p.ID)
			afterRuns, err := al.taskStore.ListRuns(member.ID)
			require.NoError(t, err)
			assert.Empty(t, afterRuns, "an unmet dependency remains held")
		})
	}
}
func TestT8Boot_PastUndeliveredOnceVersusFutureControl(t *testing.T) {
	for _, missed := range []bool{true, false} {
		name := "18.30 before occurrence fires once"
		if missed {
			name = "18.30 downtime across occurrence stays missed"
		}
		t.Run(name, func(t *testing.T) {
			al, old, clock, pe, p := t8PlanWaitFixture(t)
			at := clock.Now().Add(time.Hour).UnixMilli()
			member := t8PlanMember(t, al, p, "new scheduled task", &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}, nil)
			old.OnTaskUpserted(member)
			require.Len(t, jobsForTask(old, member.ID), 1)
			runs, err := al.taskStore.ListRuns(member.ID)
			require.NoError(t, err)
			require.Empty(t, runs, "fresh T8 data; the 6b started-but-unfinished replay class is excluded by construction")
			old.Stop()
			if missed {
				clock.Advance(2 * time.Hour)
			} else {
				clock.Advance(30 * time.Minute)
			}
			restarted := NewTaskTriggerScheduler(filepath.Join(config.OmnipusHomeDir(), "tasks_triggers", "jobs.json"), al.taskStore, al.taskExecutor)
			restarted.SetClock(clock)
			require.NoError(t, restarted.Start())
			t.Cleanup(restarted.Stop)
			al.SetTaskTriggerScheduler(restarted)
			require.NoError(t, restarted.Reconcile())
			if !missed {
				clock.Advance(30 * time.Minute)
				restarted.RunDueJobs(clock.Now())
				restarted.WaitForLane()
				al.taskExecutor.wg.Wait()
			}
			pe.processPlan(context.Background(), p.ID)
			al.taskExecutor.CheckQueuedTasks(context.Background())
			al.taskExecutor.wg.Wait()
			after, err := al.taskStore.ListRuns(member.ID)
			require.NoError(t, err)
			if missed {
				assert.Empty(t, after, "an undelivered past once is never replayed/promoted")
				stored, err := al.taskStore.Get(member.ID)
				require.NoError(t, err)
				assert.Equal(t, task.StatusInbox, stored.Status)
				members, err := al.taskStore.List(task.Filter{PlanID: p.ID})
				require.NoError(t, err)
				assert.NotEmpty(t, pe.planStallReason(members, clock.Now()), "missed, unowned work is a genuine correction-needed stall")
			} else {
				assert.Len(t, after, 1, "a still-future occurrence is re-armed and executes exactly once")
			}
		})
	}
}
