package agent

// Round 9, CK-10. Oracle: r17 TK-30. A failed plan member whose time trigger
// was attached while it was failed has no live registration (a terminal once
// task is unregistered). The automatic correction reset puts it back in
// `inbox` (waiting), and autoResetFailedMember must then bring the trigger
// scheduler in line, so a live job owns the occurrence and fires it. These
// tests use the real loop / plan engine / task store / trigger scheduler /
// executor fixture (t8PlanWaitFixture) with a controlled clock and the fake
// provider; the round-8 tests use a loop-less engine and cannot see this.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// t8CK10Seed builds the real fixture, parks the plan awaiting supervision
// and seeds one assigned failed member (no trigger yet).
func t8CK10Seed(t *testing.T) (*AgentLoop, *TaskTriggerScheduler, *triggerFakeClock, *PlanEngine, *plan.Plan, *task.Task) {
	t.Helper()
	al, sched, clock, pe, p := t8PlanWaitFixture(t)
	il, err := plan.NewIntentLog(filepath.Join(t.TempDir(), "plan_intents"))
	require.NoError(t, err)
	pe.SetIntentLog(il)
	awaiting := plan.PhaseAwaitingSupervision
	sig := "sig-ck10"
	p, err = pe.planStore.Update(p.ID, plan.Patch{PlanPhase: &awaiting, LastUnmetTerminalSignature: &sig})
	require.NoError(t, err)
	pe.recordUnmetTerminalSignature(p.ID, sig)
	m := &task.Task{
		Title: "failed member", Prompt: "verify failed member", Action: task.ActionLLM,
		AgentID: "native-agent", CreatedByAgentID: "native-agent", CreatedBy: "native-agent",
		WorkspaceID: "default", PlanID: p.ID, Status: task.StatusFailed,
		Criteria: []task.AcceptanceCriterion{planProseCriterion("the scheduled work is verified")},
	}
	require.NoError(t, al.taskStore.Create(m))
	pairTaskGoalRecordUsingTaskCriteria(t, al, m.ID)
	got, err := al.taskStore.Get(m.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, got.Status, "instrument: the member is failed")
	return al, sched, clock, pe, p, got
}

// t8CK10AttachTrigger adds the trigger through the ordered producer
// (tools.WriteTaskReconciled with the loop's reconciler), as REST and
// update_task do. A terminal task keeps its status.
func t8CK10AttachTrigger(t *testing.T, al *AgentLoop, id string, trig *task.Trigger) *task.Task {
	t.Helper()
	out, err := tools.WriteTaskReconciled(al.taskStore, id, tools.TaskWriteRequest{
		Patch:             task.Patch{Trigger: &trig},
		ReconcileSchedule: al.ReconcileTaskScheduleLocked,
	})
	require.NoError(t, err)
	require.NoError(t, out.ScheduleErr, "instrument: producer reconciliation")
	require.Equal(t, task.StatusFailed, out.Updated.Status, "instrument: a terminal task keeps its status when a trigger is added")
	return out.Updated
}

func t8CK10Jobs(sched *TaskTriggerScheduler) []cron.CronJob { return sched.cs.ListJobs(true) }

func t8CK10Correct(t *testing.T, pe *PlanEngine, planID, memberID string, verb CorrectionVerb) error {
	t.Helper()
	req := CorrectionRequest{Verb: verb, FalsifiedAssumption: "assumed the failure was permanent", Reason: "retry the failed member"}
	if verb == CorrectionTargetedRetry {
		req.RetriedMemberID = memberID
	} else {
		req.TailMembers = []task.Task{{
			ID: "m-ck10-tail", Title: "m-ck10-tail", WorkspaceID: "default", Status: task.StatusNext,
			Criteria: []task.AcceptanceCriterion{planProseCriterion("tail work is done")},
		}}
	}
	_, err := pe.AppendCorrection(context.Background(), planID, supervisorCaller(), req)
	return err
}

func t8CK10Trigger(kind string, clock *triggerFakeClock) (*task.Trigger, int64) {
	at := clock.Now().Add(2 * time.Hour).UnixMilli()
	if kind == "recurring" {
		rule, tz := "FREQ=DAILY", "UTC"
		return &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &at, Tz: &tz}}, at
	}
	return &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}, at
}

// CK-10: reset -> registration -> fire, end to end. Fails if the scheduler
// reconciliation in autoResetFailedMember is removed (a once member would
// wait in inbox with no job and never fire).
func TestT8PlanCorrection_AutoReset_FutureTriggerMember_RegistersAndFires_TK30_CK10(t *testing.T) {
	cases := []struct {
		name, kind string
		verb       CorrectionVerb
	}{
		{"future once, append correction", "once", CorrectionAppend},
		{"future once, targeted retry", "once", CorrectionTargetedRetry},
		{"future recurring, append correction", "recurring", CorrectionAppend},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, sched, clock, pe, p, m := t8CK10Seed(t)
			trig, at := t8CK10Trigger(tc.kind, clock)
			m = t8CK10AttachTrigger(t, al, m.ID, trig)

			// While failed: a terminal once task has no job; a terminal
			// recurring task keeps its series' job (the series is not over).
			if tc.kind == "once" {
				require.Empty(t, t8CK10Jobs(sched), "a failed member with a once trigger has ZERO scheduler jobs")
			} else {
				require.Len(t, t8CK10Jobs(sched), 1, "instrument: a terminal recurring task keeps its series job")
			}

			require.NoError(t, t8CK10Correct(t, pe, p.ID, m.ID, tc.verb), "automatic correction")

			got, err := al.taskStore.Get(m.ID)
			require.NoError(t, err)
			assert.Equal(t, task.StatusInbox, got.Status, "the reset leaves the member waiting in inbox (exact)")
			require.NotNil(t, got.Trigger, "the trigger is kept")
			assert.Equal(t, trig.Type, got.Trigger.Type)
			assert.Equal(t, at, t8CK10Occurrence(t, got.Trigger), "the exact occurrence is kept")

			jobs := t8CK10Jobs(sched)
			require.Len(t, jobs, 1, "exactly ONE armed job after the reset")
			require.NotNil(t, jobs[0].State.NextRunAtMS)
			assert.Equal(t, at, *jobs[0].State.NextRunAtMS, "the job is armed at exactly the trigger instant")
			jobID := jobs[0].ID

			// An identical reconciliation neither adds nor replaces a job.
			func() {
				unlock := task.LockTaskWriteOrder(m.ID)
				defer unlock()
				require.NoError(t, al.ReconcileTaskScheduleLocked(got))
			}()
			again := t8CK10Jobs(sched)
			require.Len(t, again, 1, "no second job on an identical reconciliation")
			assert.Equal(t, jobID, again[0].ID, "no replacement job either")

			// No early run just before the instant.
			clock.Advance(2*time.Hour - time.Millisecond)
			sched.RunDueJobs(clock.Now())
			sched.WaitForLane()
			al.taskExecutor.wg.Wait()
			runs, err := al.taskStore.ListRuns(m.ID)
			require.NoError(t, err)
			require.Empty(t, runs, "no run before the occurrence")
			early, err := al.taskStore.Get(m.ID)
			require.NoError(t, err)
			require.Equal(t, task.StatusInbox, early.Status, "still waiting one millisecond before the occurrence")

			// At the instant the real lane fires and the executor joins.
			clock.Advance(time.Millisecond)
			sched.RunDueJobs(clock.Now())
			sched.WaitForLane()
			al.taskExecutor.wg.Wait()
			pe.processPlan(context.Background(), p.ID)
			al.taskExecutor.CheckQueuedTasks(context.Background())
			al.taskExecutor.wg.Wait()

			runs, err = al.taskStore.ListRuns(m.ID)
			require.NoError(t, err)
			assert.Len(t, runs, 1, "exactly one execution at the occurrence")
			fired, err := al.taskStore.Get(m.ID)
			require.NoError(t, err)
			require.NotNil(t, fired.LastDeliveredAtMs, "the delivery marker is recorded")
			assert.Equal(t, at, *fired.LastDeliveredAtMs, "the marker records exactly the delivered occurrence")
		})
	}
}

// CK-10 companion: the reset persisted but the scheduler cannot save. The
// correction still succeeds, the member waits in inbox with its trigger, the
// failure is a WARN naming the member, and no stale job is left armed.
func TestT8PlanCorrection_AutoReset_FutureOnceMember_SaveFault_WarnsNoStaleJob_TK30_CK10(t *testing.T) {
	readLog := captureLogFile(t, logger.WARN)
	al, sched, clock, pe, p, m := t8CK10Seed(t)
	trig, at := t8CK10Trigger("once", clock)
	m = t8CK10AttachTrigger(t, al, m.ID, trig)
	require.Empty(t, t8CK10Jobs(sched), "instrument: zero jobs while failed")

	// A directory at the exact save destination makes the real atomic
	// replacement of the jobs file fail, after the engine appended the job.
	path := filepath.Join(config.OmnipusHomeDir(), "tasks_triggers", "jobs.json")
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

	require.NoError(t, t8CK10Correct(t, pe, p.ID, m.ID, CorrectionAppend),
		"a reconciliation failure never fails the correction")

	got, err := al.taskStore.Get(m.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusInbox, got.Status, "the persisted reset stands")
	require.NotNil(t, got.Trigger)
	assert.Equal(t, at, t8CK10Occurrence(t, got.Trigger), "the trigger is kept")
	assert.Empty(t, t8CK10Jobs(sched), "best-effort de-arm: no stale job is left armed")

	log := readLog()
	assert.Contains(t, log, "auto-reset: could not reset failed member", "the failure is a warning")
	assert.Contains(t, log, "trigger schedule could not be reconciled", "the warning names the reconciliation failure")
	assert.Contains(t, log, m.ID, "the warning names the member")

	// The saved trigger stays authoritative for a later reconciliation.
	restore()
	require.NoError(t, sched.Reconcile())
	jobs := t8CK10Jobs(sched)
	require.Len(t, jobs, 1)
	require.NotNil(t, jobs[0].State.NextRunAtMS)
	assert.Equal(t, at, *jobs[0].State.NextRunAtMS)
}

func t8CK10Occurrence(t *testing.T, tr *task.Trigger) int64 {
	t.Helper()
	if tr.Type == task.TriggerRecurring {
		require.NotNil(t, tr.Config.DtstartMs)
		return *tr.Config.DtstartMs
	}
	require.NotNil(t, tr.Config.AtMs)
	return *tr.Config.AtMs
}
