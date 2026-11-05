package agent

// r17 §13.1 test 12d: scenarios 18.24 (promotion snapshot barrier), 18.34
// (published reservation before dispatch entry, single and batch) and the
// scheduler half of 18.40(a-c) (collection-save fault with a queued dispatch).

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestT8Plan_PromotionPassKeepsTriggerInstalledAfterSnapshot covers 18.24:
// a promotion pass has snapshotted its members and is held before its write
// while the creator installs a future once trigger; the pass must decide on
// fresh persisted state and leave the task waiting.
func TestT8Plan_PromotionPassKeepsTriggerInstalledAfterSnapshot(t *testing.T) {
	for _, install := range []bool{true, false} {
		name := "control: no trigger installed, member is promoted"
		if install {
			name = "trigger installed after the snapshot"
		}
		t.Run(name, func(t *testing.T) {
			al, sched, clock, pe, p := t8PlanWaitFixture(t)
			member := t8PlanMember(t, al, p, "promotion race member", nil, nil)
			snapshot, err := al.taskStore.List(task.Filter{PlanID: p.ID})
			require.NoError(t, err)
			require.Len(t, snapshot, 1)

			gate := newT8Gate(t)
			restore := task.SetPromoteInboxTestHook(func(string) { gate.hold() })
			defer restore()
			promoted := make(chan bool, 1)
			go func() { promoted <- pe.promoteInboxMembers(p, snapshot) }()
			gate.waitEntered(t)

			at := clock.Now().Add(time.Hour).UnixMilli()
			if install {
				trig := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}
				changed, uerr := al.taskStore.Update(member.ID, task.Patch{Trigger: &trig})
				require.NoError(t, uerr)
				sched.OnTaskUpserted(changed)
			}
			gate.open()
			var didPromote bool
			select {
			case didPromote = <-promoted:
			case <-time.After(10 * time.Second):
				t.Fatal("promotion pass never finished")
			}
			got, err := al.taskStore.Get(member.ID)
			require.NoError(t, err)
			if !install {
				assert.True(t, didPromote)
				assert.NotEqual(t, task.StatusInbox, got.Status, "the barrier sits on the promoting path")
				return
			}
			assert.False(t, didPromote, "nothing promoted")
			assert.Equal(t, task.StatusInbox, got.Status, "the trigger-due test ran on fresh state: T stays inbox")
			require.NotNil(t, got.Trigger, "the newly installed trigger is kept")
			runs, err := al.taskStore.ListRuns(member.ID)
			require.NoError(t, err)
			assert.Empty(t, runs, "zero dispatches before the occurrence")

			clock.Advance(time.Hour + time.Second)
			sched.RunDueJobs(clock.Now())
			sched.WaitForLane()
			al.taskExecutor.wg.Wait()
			pe.processPlan(context.Background(), p.ID)
			al.taskExecutor.CheckQueuedTasks(context.Background())
			al.taskExecutor.wg.Wait()
			runs, err = al.taskStore.ListRuns(member.ID)
			require.NoError(t, err)
			assert.Len(t, runs, 1, "at the occurrence the member dispatches exactly once")
		})
	}
}

// t8DueMembers creates the pending plan member T (and, for the batch case, a
// second member U due one second earlier) and returns ids and job ids.
func t8DueMembers(t *testing.T, al *AgentLoop, sched *TaskTriggerScheduler, p *plan.Plan, clock *triggerFakeClock, withU bool) (tID, uID, tJob, uJob string, at int64) {
	t.Helper()
	at = clock.Now().Add(14 * 24 * time.Hour).UnixMilli()
	if withU {
		uAt := at - 1000
		u := t8PlanMember(t, al, p, "batch member U", &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &uAt}}, nil)
		sched.OnTaskUpserted(u)
		uj := jobsForTask(sched, u.ID)
		require.Len(t, uj, 1)
		uID, uJob = u.ID, uj[0].ID
	}
	tt := t8PlanMember(t, al, p, "pending member T", &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}, nil)
	sched.OnTaskUpserted(tt)
	tj := jobsForTask(sched, tt.ID)
	require.Len(t, tj, 1)
	return tt.ID, uID, tj[0].ID, uJob, at
}

// TestT8TaskTrigger_ReservationPublishedBeforeDispatchEntry covers 18.34
// (single and batch): the pause sits after collection unlocked, in the
// published-reservation queued span.
func TestT8TaskTrigger_ReservationPublishedBeforeDispatchEntry(t *testing.T) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch"
		}
		t.Run(name, func(t *testing.T) {
			al, sched, clock, pe, p := t8PlanWaitFixture(t)
			sched.cs.SetMaxConcurrentRuns(1)
			tID, uID, tJob, uJob, at := t8DueMembers(t, al, sched, p, clock, batch)

			gate := newT8Gate(t)
			hold := func(id string) {
				if id == tJob {
					gate.hold()
				}
			}
			var restore func()
			if batch {
				// Lane of one: T, collected after U, is held between U's run and its own.
				restore = sched.cs.SetBeforeRunTestHook(hold)
			} else {
				restore = sched.cs.SetDispatchEntryTestHook(hold)
			}
			defer restore()

			clock.Advance(14*24*time.Hour + time.Second)
			sched.RunDueJobs(clock.Now())
			gate.waitEntered(t)
			assert.True(t, sched.cs.JobDispatchInFlight(tJob), "the reservation is already published during the pause")

			before := t8SessionCount(t, al)
			for range 2 {
				pe.processPlan(context.Background(), p.ID)
				pe.idleExpirySweep()
				got, err := al.taskStore.Get(tID)
				require.NoError(t, err)
				assert.Equal(t, task.StatusInbox, got.Status, "pending T is not promoted")
				if !batch {
					t8AssertNoFalseWaitEffects(t, al, pe, p, before)
				}
			}
			stored, err := pe.planStore.Get(p.ID)
			require.NoError(t, err)
			assert.Equal(t, plan.StateRunning, stored.State, "the sweep does not expire the plan")
			assert.Equal(t, p.LastActivityAt, stored.LastActivityAt, "no fabricated activity")

			gate.open()
			sched.WaitForLane()
			al.taskExecutor.wg.Wait()
			pe.processPlan(context.Background(), p.ID)
			al.taskExecutor.CheckQueuedTasks(context.Background())
			al.taskExecutor.wg.Wait()

			assert.Equal(t, float64(at), t8StoredMarker(t, al.taskStore, tID), "marker is the occurrence's scheduled instant")
			ids, jobIDs := []string{tID}, []string{tJob}
			if batch {
				ids, jobIDs = append(ids, uID), append(jobIDs, uJob)
			}
			for i, id := range ids {
				runs, rerr := al.taskStore.ListRuns(id)
				require.NoError(t, rerr)
				assert.Len(t, runs, 1, "each collected job dispatches exactly once: %s", id)
				assert.False(t, sched.cs.JobDispatchInFlight(jobIDs[i]), "settlement accounting for original job id %s", jobIDs[i])
			}
		})
	}
}

// TestT8TaskTrigger_CollectionSaveFailureStillDeliversQueuedOccurrence covers
// the scheduler half of 18.40(a-c): the collection-time store save fails
// while the dispatch is queued, storage then recovers, and the queued
// occurrence delivers exactly once. The reload leg is
// pkg/gateway TestTaskTrigger_ReloadKeepsLiveScheduler_ProductionPath, which
// shows the reload never touches the scheduler.
func TestT8TaskTrigger_CollectionSaveFailureStillDeliversQueuedOccurrence(t *testing.T) {
	sched, store, clock, rec := newTriggerSchedulerForTest(t)
	at := clock.Now().Add(time.Minute).UnixMilli()
	tk := &task.Task{Title: "save fault", Action: task.ActionLLM, AgentID: "agent-a", WorkspaceID: "ws-a", Status: task.StatusInbox, Trigger: &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}}
	require.NoError(t, store.Create(tk))
	sched.OnTaskUpserted(tk)
	jobs := jobsForTask(sched, tk.ID)
	require.Len(t, jobs, 1)

	gate := newT8Gate(t)
	defer sched.cs.SetDispatchEntryTestHook(func(string) { gate.hold() })()
	var failing atomic.Bool
	failing.Store(true)
	defer sched.cs.SetSaveStoreTestHook(func() error {
		if failing.Load() {
			return assert.AnError
		}
		return nil
	})()

	clock.Advance(time.Minute + time.Second)
	sched.RunDueJobs(clock.Now())
	gate.waitEntered(t)
	require.True(t, sched.cs.JobDispatchInFlight(jobs[0].ID), "instrument: occurrence queued with the save failing")

	failing.Store(false) // storage recovers before the slot frees
	gate.open()
	sched.WaitForLane()
	assert.Equal(t, []string{tk.ID}, rec.calls(), "the queued occurrence delivers exactly once")
	assert.Equal(t, float64(at), t8StoredMarker(t, store, tk.ID))
	assert.False(t, sched.cs.JobDispatchInFlight(jobs[0].ID), "reservation settled")
}
