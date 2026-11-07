//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// All T8 rows here remain 'needs T8 backend'. Real stores, tool wiring,
// executor, scheduler and (where used) plan engine; only paid model I/O and
// clock are controlled. Binding a missing process-edge hook cannot satisfy
// these assertions by itself. No substitute delivery/recovery implementation.
type t10T8Fixture struct {
	al   *AgentLoop
	s    *TaskTriggerScheduler
	clk  *triggerFakeClock
	p    *claimingWorker
	path string
}

// Aligned production-shaped homes: tool workspace lookup, task/goal stores,
// and session stores must share one home. The older completion helper uses
// deliberately independent homes and is not a valid create_task fixture.
func t10NativeTaskLoop(t *testing.T, p providers.LLMProvider) *AgentLoop {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"}},
		List:     []config.AgentConfig{{ID: "native-agent", Type: config.AgentTypeWorker, Home: filepath.Join(home, "agents", "native-agent")}, judgeAgentConfigForTaskTests(t)},
	}}
	cfg.Sandbox.ToolPolicies = map[string]string{"goal_claim": "allow"}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), p)
	bindMetSoftTierJudge(t, al)
	mintGenuineBootEpochForLoop(t, al)
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), lifecycle)
	al.taskExecutor.SetLifecycleStore(lifecycle)
	al.taskExecutor.SetSessionLauncher(NewSteerLauncher(al))
	t.Cleanup(al.Close)
	return al
}

func t10T8(t *testing.T) *t10T8Fixture {
	t.Helper()
	p := newClaimingWorker(turnClaimMet("T10 scheduled contract completed"))
	al := t10NativeTaskLoop(t, p)
	provisionTestWorkspace(t, "default", "native-agent")
	require.NoError(t, workspace.SaveDelegation(omnipusHome(), "default", []workspace.DelegationEdge{{FromAgent: "native-agent", ToAgent: "native-agent"}}))
	path := filepath.Join(t.TempDir(), "cron", "jobs.json")
	s := t10TaskScheduler(t, path, al.taskStore, al.taskExecutor)
	clk := newTriggerFakeClock()
	s.SetClock(clk)
	require.NoError(t, s.Start())
	al.SetTaskTriggerScheduler(s)
	t.Cleanup(func() { s.Stop(); s.WaitForLane() })
	return &t10T8Fixture{al, s, clk, p, path}
}
func t10TaskScheduler(t *testing.T, path string, store *task.Store, te *TaskExecutor) *TaskTriggerScheduler {
	t.Helper()
	ctor := reflect.ValueOf(NewTaskTriggerScheduler)
	require.Equal(t, 3, ctor.Type().NumIn())
	require.Equal(t, 1, ctor.Type().NumOut())
	first := reflect.ValueOf(path)
	if ctor.Type().In(0) == reflect.TypeOf((*cron.CronService)(nil)) {
		first = reflect.ValueOf(cron.NewCronService(path))
	}
	require.True(t, first.Type().AssignableTo(ctor.Type().In(0)), "bind actual injected scheduler constructor")
	return ctor.Call([]reflect.Value{first, reflect.ValueOf(store), reflect.ValueOf(te)})[0].Interface().(*TaskTriggerScheduler)
}
func (f *t10T8Fixture) create(t *testing.T, kind string) *task.Task {
	t.Helper()
	tk := newCompletionContractTask(t, f.al, "native-agent", "T10 "+kind)
	status := task.StatusInbox
	var trigger *task.Trigger
	at := f.clk.Now().Truncate(time.Second).Add(time.Hour).UnixMilli()
	switch kind {
	case "once":
		trigger = &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}
	case "recurring":
		rule, tz := "FREQ=DAILY;COUNT=3", "UTC"
		trigger = &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &at, Tz: &tz}}
	}
	fresh, err := f.al.taskStore.Update(tk.ID, task.Patch{Status: &status, Trigger: &trigger})
	require.NoError(t, err)
	f.s.OnTaskUpserted(fresh)
	return fresh
}
func (f *t10T8Fixture) jobs(t *testing.T, id string) []cron.CronJob {
	t.Helper()
	var out []cron.CronJob
	for _, j := range f.s.cs.ListJobs(true) {
		if j.Payload.Message == id {
			out = append(out, j)
		}
	}
	return out
}
func (f *t10T8Fixture) fire(t *testing.T, at int64) {
	t.Helper()
	f.clk.Advance(time.UnixMilli(at).Sub(f.clk.Now()))
	f.s.RunDueJobs(f.clk.Now())
	f.s.WaitForLane()
}
func (f *t10T8Fixture) terminal(t *testing.T, id string) {
	t.Helper()
	require.Eventually(t, func() bool {
		tk, e := f.al.taskStore.Get(id)
		if e != nil || tk.Status != task.StatusDone {
			return false
		}
		runs, e := f.al.taskStore.ListRuns(id)
		return e == nil && len(runs) == 1 && runs[0].Status == task.StatusDone && runs[0].EndedAt != nil
	}, 5*time.Second, time.Millisecond, "real execution must finish AND close its stored run")
	rows, err := f.al.taskStore.ListRuns(id)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, task.StatusDone, rows[0].Status)
	assert.NotEmpty(t, rows[0].SessionID)
}
func t10InternalMarker(t *testing.T, store *task.Store, id string) any {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(store.Dir(), id+".json"))
	require.NoError(t, e)
	var v map[string]any
	require.NoError(t, json.Unmarshal(b, &v))
	return v["last_delivered_at_ms"]
}

func t10T8ToolWaiting(t *testing.T) {
	for _, kind := range []string{"once", "recurring", "manual"} {
		t.Run(kind, func(t *testing.T) {
			f := t10T8(t)
			inst, ok := f.al.registry.GetAgent("native-agent")
			require.True(t, ok)
			tool, ok := inst.Tools.Get("create_task")
			require.True(t, ok, "real registered tool, not a detached mock")
			at := f.clk.Now().Truncate(time.Second).Add(time.Hour).UnixMilli()
			trigger := map[string]any{"type": kind, "config": map[string]any{}}
			if kind == "once" {
				trigger["config"] = map[string]any{"at_ms": at}
			}
			if kind == "recurring" {
				trigger["config"] = map[string]any{"rrule": "FREQ=DAILY;COUNT=3", "dtstart_ms": at, "tz": "UTC"}
			}
			ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "native-agent"), "default")
			res := tool.Execute(ctx, map[string]any{"title": "T10 tool waiting", "prompt": "Produce a complete result", "agent_id": "native-agent", "trigger": trigger,
				"criteria": []any{map[string]any{"kind": "prose", "text": "Result exists", "judgment": "boolean"}}, "dod": []any{map[string]any{"kind": "prose", "text": "Result checked", "judgment": "boolean"}}})
			require.False(t, res.IsError, res.ForLLM)
			rows, e := f.al.taskStore.List(task.Filter{})
			require.NoError(t, e)
			require.Len(t, rows, 1)
			tk := rows[0]
			if kind != "manual" {
				assert.Equal(t, task.StatusInbox, tk.Status, "needs T8: time-trigger tool creation is scheduler-owned waiting")
				require.NotNil(t, tk.Trigger, "needs T8: tool must persist trigger, not ignore unknown argument")
				assert.Equal(t, task.TriggerType(kind), tk.Trigger.Type)
				f.al.taskExecutor.CheckQueuedTasks(ctx)
				assert.Zero(t, f.p.turnsStarted())
				sessions, e := f.al.GetSessionStore().ListSessions()
				require.NoError(t, e)
				assert.Empty(t, sessions)
				jobs := f.jobs(t, tk.ID)
				require.Len(t, jobs, 1)
				assert.Equal(t, &at, jobs[0].State.NextRunAtMS)
				f.fire(t, at)
				f.terminal(t, tk.ID)
			} else {
				assert.Equal(t, task.StatusNext, tk.Status)
				f.al.taskExecutor.CheckQueuedTasks(ctx)
				f.terminal(t, tk.ID)
				assert.Empty(t, f.jobs(t, tk.ID))
			}
		})
	}
}

func t10T8ForeignActivation(t *testing.T) {
	f := t10T8(t)
	tk := f.create(t, "once")
	before, e := os.ReadFile(filepath.Join(f.al.taskStore.Dir(), tk.ID+".json"))
	require.NoError(t, e)
	jobs := f.jobs(t, tk.ID)
	// Creator is authorised to edit, but its workspace may not activate the receiver.
	inst, ok := f.al.registry.GetAgent("native-agent")
	require.True(t, ok)
	caller, ok := inst.Tools.Get("update_task")
	require.True(t, ok, "real registered edit path includes scheduler notification")
	foreign := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "native-agent"), "foreign")
	for _, args := range []map[string]any{{"task_id": tk.ID, "status": "next"}, {"task_id": tk.ID, "trigger": map[string]any{"type": "once", "config": map[string]any{"at_ms": *tk.Trigger.Config.AtMs + 3600000}}}} {
		r := caller.Execute(foreign, args)
		assert.True(t, r.IsError, "needs T8: foreign activation refused")
		after, e := os.ReadFile(filepath.Join(f.al.taskStore.Dir(), tk.ID+".json"))
		require.NoError(t, e)
		assert.Equal(t, before, after)
		assert.Equal(t, jobs, f.jobs(t, tk.ID))
	}
	assert.Zero(t, f.p.turnsStarted())
	// The manual clear exception is non-activating and must actually remove the job.
	r := caller.Execute(foreign, map[string]any{"task_id": tk.ID, "trigger": map[string]any{"type": "manual", "config": map[string]any{}}})
	require.False(t, r.IsError, r.ForLLM)
	assert.Empty(t, f.jobs(t, tk.ID))
	fresh, e := f.al.taskStore.Get(tk.ID)
	require.NoError(t, e)
	assert.Equal(t, tk.Status, fresh.Status)
}

func t10T8AutomaticPromotions(t *testing.T) {
	for _, path := range []string{"completion", "delete", "recompute", "plan-pass"} {
		t.Run(path, func(t *testing.T) {
			f := t10T8(t)
			tk := f.create(t, "once")
			at := *tk.Trigger.Config.AtMs
			blocker := newCompletionContractTask(t, f.al, "native-agent", "blocker")
			blocked := []string{blocker.ID}
			next := task.StatusNext
			_, e := f.al.taskStore.Update(tk.ID, task.Patch{Status: &next, BlockedBy: &blocked})
			require.NoError(t, e)
			if path == "plan-pass" {
				ps := plan.New(filepath.Join(t.TempDir(), "plans"))
				p := newPlanGateTestPlan(t, ps, plan.StateRunning)
				_, e = f.al.taskStore.Update(tk.ID, task.Patch{PlanID: &p.ID})
				require.NoError(t, e)
				f.al.taskExecutor.SetPlanStore(ps)
				pe := NewPlanEngine(f.al, ps, f.al.taskStore, f.al.taskExecutor)
				pe.clock = f.clk
				pe.Tick(context.Background())
			} else if path == "delete" {
				ids, err := f.al.taskStore.Delete(blocker.ID)
				require.NoError(t, err)
				for _, id := range ids {
					_, err = f.al.taskStore.AdvanceUnblocked(id)
					require.NoError(t, err)
				}
			} else {
				done := task.StatusDone
				_, e = f.al.taskStore.Update(blocker.ID, task.Patch{Status: &done})
				require.NoError(t, e)
				if path == "completion" {
					_, e = f.al.taskStore.AdvanceBlockedDependents(blocker.ID)
				} else {
					_, e = f.al.taskStore.AdvanceUnblocked(tk.ID)
				}
				require.NoError(t, e)
			}
			fresh, e := f.al.taskStore.Get(tk.ID)
			require.NoError(t, e)
			assert.Contains(t, []task.Status{task.StatusInbox, task.StatusBlocked}, fresh.Status, "needs T8: automatic promotion must test fresh trigger under write lock")
			// Remove the setup blocker before the actual occurrence. Deletion still may not release early.
			if path == "plan-pass" {
				_, e = f.al.taskStore.Delete(blocker.ID)
				require.NoError(t, e)
			}
			f.al.taskExecutor.CheckQueuedTasks(context.Background())
			assert.Zero(t, f.p.turnsStarted())
			f.fire(t, at)
			f.terminal(t, tk.ID)
		})
	}
}

func t10T8ExpectedWaiting(t *testing.T, pending bool) {
	f := t10T8(t)
	ps := plan.New(filepath.Join(t.TempDir(), "plans"))
	p := newPlanGateTestPlan(t, ps, plan.StateRunning)
	f.al.taskExecutor.SetPlanStore(ps)
	pe := NewPlanEngine(f.al, ps, f.al.taskStore, f.al.taskExecutor)
	pe.clock = f.clk
	tk := f.create(t, "once")
	// r17 18.27/29: a real scheduled root beyond the seven-day idle bound,
	// plus a manual member transitively waiting on it. No fabricated activity.
	at := f.clk.Now().Truncate(time.Second).Add(14 * 24 * time.Hour).UnixMilli()
	trigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}
	tk, e := f.al.taskStore.Update(tk.ID, task.Patch{PlanID: &p.ID, Trigger: &trigger})
	require.NoError(t, e)
	f.s.OnTaskUpserted(tk)
	dependent := newCompletionContractTask(t, f.al, "native-agent", "transitive waiting member")
	deps, next := []string{tk.ID}, task.StatusNext
	child, e := f.al.taskStore.Update(dependent.ID, task.Patch{PlanID: &p.ID, BlockedBy: &deps, Status: &next})
	require.NoError(t, e)
	require.Equal(t, task.StatusBlocked, child.Status, "real dependency derivation establishes the transitive wait")
	before, e := ps.Get(p.ID)
	require.NoError(t, e)
	release := func() {}
	if pending {
		release = t10HoldCollected(t, f, tk.ID)
	}
	for _, d := range []time.Duration{time.Hour, 24 * time.Hour, 8 * 24 * time.Hour} {
		f.clk.Advance(d)
		pe.Tick(context.Background())
		pe.idleExpirySweep()
		after, e := ps.Get(p.ID)
		require.NoError(t, e)
		assert.Equal(t, plan.StateRunning, after.State, "needs T8: expected wait must not idle-expire")
		assert.NotEqual(t, plan.PhaseStalled, after.PlanPhase, "transitive scheduled wait is not a genuine stall")
		assert.Equal(t, before.LastActivityAt, after.LastActivityAt, "no synthetic activity to hide expiry")
		assert.NotContains(t, after.HandoverText, "[stalled]")
		assert.Equal(t, before.JudgeRounds, after.JudgeRounds)
		assert.Equal(t, before.Supervision, after.Supervision, "wait spends no supervision attempt")
		fresh, e := f.al.taskStore.Get(tk.ID)
		require.NoError(t, e)
		assert.Equal(t, task.StatusInbox, fresh.Status)
		assert.Empty(t, fresh.SessionID)
		child, err := f.al.taskStore.Get(dependent.ID)
		require.NoError(t, err)
		assert.Equal(t, task.StatusBlocked, child.Status)
		assert.Empty(t, child.SessionID)
	}
	assert.Zero(t, f.p.turnsStarted())
	if pending {
		release()
		f.s.WaitForLane()
	} else {
		f.fire(t, at)
	}
	// Positive counterpart: suppressing every plan/task action cannot pass.
	// Only real completion of the scheduled root may release its dependent.
	f.terminal(t, tk.ID)
	f.terminal(t, dependent.ID)
	assert.Equal(t, 2, f.p.turnsStarted(), "exactly one real turn for root and dependent")
	rootRuns, err := f.al.taskStore.ListRuns(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, &at, rootRuns[0].OccurrenceMs)
	childRuns, err := f.al.taskStore.ListRuns(dependent.ID)
	require.NoError(t, err)
	assert.Nil(t, childRuns[0].OccurrenceMs)
}

// A hook observes the real pre-handler boundary; it is not a fake scheduler.
// The production method name is a temporary binding, not a product requirement.
func t10HoldCollected(t *testing.T, f *t10T8Fixture, id string) func() {
	t.Helper()
	edge, ok := any(f.s.cs).(interface {
		SetBeforeDispatchTestHook(func(context.Context, string))
	})
	if !ok {
		t.Fatal("BLOCKED: needs T8 backend: real pre-handler dispatch hook absent — r17 18.31/32/34; assertions require actual collected ownership")
	}
	jobs := f.jobs(t, id)
	require.Len(t, jobs, 1)
	j := jobs[0]
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	edge.SetBeforeDispatchTestHook(func(_ context.Context, jobID string) {
		if jobID == j.ID {
			once.Do(func() { close(entered); <-release })
		}
	})
	var free sync.Once
	unblock := func() { free.Do(func() { close(release) }) }
	t.Cleanup(func() { unblock(); f.s.WaitForLane(); edge.SetBeforeDispatchTestHook(nil) })
	require.NotNil(t, j.State.NextRunAtMS)
	f.clk.Advance(time.UnixMilli(*j.State.NextRunAtMS).Sub(f.clk.Now()))
	f.s.RunDueJobs(f.clk.Now())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real dispatch never reached held edge")
	}
	assert.True(t, f.s.cs.JobDispatchInFlight(j.ID), "ownership must be published before dispatch callback")
	assert.Nil(t, t10InternalMarker(t, f.al.taskStore, id))
	assert.Zero(t, f.p.turnsStarted())
	return unblock
}

func t10T8QueuedRegistration(t *testing.T, edit bool) {
	f := t10T8(t)
	tk := f.create(t, "recurring")
	jobs := f.jobs(t, tk.ID)
	require.Len(t, jobs, 1)
	original := jobs[0].ID
	release := t10HoldCollected(t, f, tk.ID)
	if edit {
		title := "queued title edit"
		fresh, e := f.al.taskStore.Update(tk.ID, task.Patch{Title: &title})
		require.NoError(t, e)
		f.s.OnTaskUpserted(fresh)
	}
	f.s.RunRecoverySweep()
	f.s.RunRecoverySweep()
	after := f.jobs(t, tk.ID)
	require.Len(t, after, 1)
	assert.Equal(t, original, after[0].ID, "queued registration must not be replaced")
	assert.True(t, f.s.cs.JobDispatchInFlight(original))
	release()
	f.s.WaitForLane()
	f.terminal(t, tk.ID)
	assert.False(t, f.s.cs.JobDispatchInFlight(original), "original pending counter must settle to zero")
}

func t10T8PhysicalBoot(t *testing.T) {
	for _, when := range []string{"before", "missed", "started-unfinished-6b"} {
		t.Run(when, func(t *testing.T) {
			f := t10T8(t)
			tk := f.create(t, "once")
			at := *tk.Trigger.Config.AtMs
			if when == "started-unfinished-6b" {
				meta, e := f.al.GetSessionStore().NewSession(session.SessionTypeTask, "task", tk.AgentID)
				require.NoError(t, e)
				require.NoError(t, f.al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
					SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
					WorkspaceID: tk.WorkspaceID, AgentID: tk.AgentID, OwnerScopeKind: session.OwnerScopeHuman,
					Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: tk.ID}, Unattended: true,
					CreatedAt: f.clk.Now(),
				}))
				inProgress := task.StatusInProgress
				_, e = f.al.taskStore.Update(tk.ID, task.Patch{Status: &inProgress, SessionID: &meta.ID})
				require.NoError(t, e)
				r := f.al.taskExecutor.openRun(tk.ID, &at, task.RunKindScheduled, meta.ID)
				require.NotNil(t, r)
				// Existing filesystem seam seeds a fresh T8 durable fixture, not a migration.
				path := filepath.Join(f.al.taskStore.Dir(), tk.ID+".json")
				b, e := os.ReadFile(path)
				require.NoError(t, e)
				var doc map[string]any
				require.NoError(t, json.Unmarshal(b, &doc))
				doc["last_delivered_at_ms"] = at
				b, e = json.Marshal(doc)
				require.NoError(t, e)
				require.NoError(t, os.WriteFile(path, b, 0600))
			}
			f.s.Stop()
			f.s.WaitForLane()
			if when != "before" {
				f.clk.Advance(2 * time.Hour)
			}
			s := t10TaskScheduler(t, f.path, f.al.taskStore, f.al.taskExecutor)
			s.SetClock(f.clk)
			f.s = s
			f.al.SetTaskTriggerScheduler(s)
			require.NoError(t, s.Start())
			require.NoError(t, s.Reconcile())
			t.Cleanup(func() { s.Stop(); s.WaitForLane() })
			if when == "missed" {
				s.RunDueJobs(f.clk.Now())
				s.WaitForLane()
				f.al.taskExecutor.CheckQueuedTasks(context.Background())
				fresh, e := f.al.taskStore.Get(tk.ID)
				require.NoError(t, e)
				assert.Equal(t, task.StatusInbox, fresh.Status)
				assert.Empty(t, fresh.SessionID)
				assert.Zero(t, f.p.turnsStarted())
				assert.Nil(t, t10InternalMarker(t, f.al.taskStore, tk.ID))
			} else if when == "before" {
				f.fire(t, at)
				f.terminal(t, tk.ID)
				assert.Equal(t, 1, f.p.turnsStarted())
			} else {
				// 6b F1 exception is explicit: a started-but-unfinished delivery is replayed,
				// never counted as consumed-and-skipped. A no-op recovery fails this oracle.
				require.Eventually(t, func() bool { return f.p.turnsStarted() == 1 }, 5*time.Second, time.Millisecond, "needs T8 backend: 6b F1 must replay interrupted started once")
				s.RunDueJobs(f.clk.Now())
				s.WaitForLane()
				assert.Equal(t, 1, f.p.turnsStarted())
			}
		})
	}
}

func t10T8MarkerLifecycle(t *testing.T) {
	f := t10T8(t)
	tk := f.create(t, "once")
	at := *tk.Trigger.Config.AtMs
	blocker := newCompletionContractTask(t, f.al, "native-agent", "dependency")
	deps := []string{blocker.ID}
	next := task.StatusNext
	_, e := f.al.taskStore.Update(tk.ID, task.Patch{Status: &next, BlockedBy: &deps})
	require.NoError(t, e)
	f.fire(t, at)
	assert.Equal(t, float64(at), t10InternalMarker(t, f.al.taskStore, tk.ID), "scheduled instant, not wall clock")
	fresh, e := f.al.taskStore.Get(tk.ID)
	require.NoError(t, e)
	assert.Empty(t, fresh.SessionID)
	assert.Zero(t, f.p.turnsStarted())
	identical := fresh.Trigger
	_, e = f.al.taskStore.Update(tk.ID, task.Patch{Trigger: &identical})
	require.NoError(t, e)
	assert.Equal(t, float64(at), t10InternalMarker(t, f.al.taskStore, tk.ID))
	manual := &task.Trigger{Type: task.TriggerManual}
	fresh, e = f.al.taskStore.Update(tk.ID, task.Patch{Trigger: &manual})
	require.NoError(t, e)
	f.s.OnTaskUpserted(fresh)
	assert.Nil(t, t10InternalMarker(t, f.al.taskStore, tk.ID))
	assert.Empty(t, f.jobs(t, tk.ID))
}

func t10T8StaleDelivery(t *testing.T) {
	f := t10T8(t)
	tk := f.create(t, "once")
	jobs := f.jobs(t, tk.ID)
	require.Len(t, jobs, 1)
	old := jobs[0]
	// Released T8 seam before the fire's coordination lock (r17 18.35): the
	// replacement fully completes before the old callback takes the lock that
	// guards the delivery/reset write. Content returns identical. (Rebound from
	// the SpawnReset hook, which T8 moved inside that lock; calling
	// OnTaskUpserted there self-deadlocks. Assertions unchanged.)
	var once sync.Once
	f.s.SetPreMarkerLockTestHook(func(_, _ string) {
		once.Do(func() {
			manual := &task.Trigger{Type: task.TriggerManual}
			fresh, e := f.al.taskStore.Update(tk.ID, task.Patch{Trigger: &manual})
			require.NoError(t, e)
			f.s.OnTaskUpserted(fresh)
			fresh, e = f.al.taskStore.Update(tk.ID, task.Patch{Trigger: &tk.Trigger})
			require.NoError(t, e)
			f.s.OnTaskUpserted(fresh)
		})
	})
	f.clk.Advance(time.UnixMilli(*old.State.NextRunAtMS).Sub(f.clk.Now()))
	_, e := f.s.RunScheduled(context.Background(), &old)
	require.NoError(t, e)
	assert.Nil(t, t10InternalMarker(t, f.al.taskStore, tk.ID), "identical content cannot revive removed registration authority")
	assert.Zero(t, f.p.turnsStarted())
	rows, e := f.al.taskStore.ListRuns(tk.ID)
	require.NoError(t, e)
	assert.Empty(t, rows)
}

func t10T8DeferredRearm(t *testing.T) {
	f := t10T8(t)
	tk := f.create(t, "recurring")
	jobs := f.jobs(t, tk.ID)
	require.Len(t, jobs, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var free sync.Once
	f.s.SetRearmTestHook(func() { once.Do(func() { close(entered); <-release }) })
	t.Cleanup(func() { free.Do(func() { close(release) }); f.s.WaitForLane(); f.s.SetRearmTestHook(nil) })
	at := *jobs[0].State.NextRunAtMS
	f.clk.Advance(time.UnixMilli(at).Sub(f.clk.Now()))
	f.s.RunDueJobs(f.clk.Now())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real deferred rearm hook not reached")
	}
	manual := &task.Trigger{Type: task.TriggerManual}
	fresh, e := f.al.taskStore.Update(tk.ID, task.Patch{Trigger: &manual})
	require.NoError(t, e)
	f.s.OnTaskUpserted(fresh)
	free.Do(func() { close(release) })
	f.s.WaitForLane()
	assert.Empty(t, f.jobs(t, tk.ID), "stale terminal producer must reread current manual trigger")
}

func t10T8CheckedLoad(t *testing.T) {
	f := t10T8(t)
	f.create(t, "recurring")
	f.s.Stop()
	f.s.WaitForLane()
	damaged := []byte(`{"version":1,"jobs":[{"id":"t8-damage","enabled":"invalid"}]}`)
	require.NoError(t, os.WriteFile(f.path, damaged, 0600))
	s := t10TaskScheduler(t, f.path, f.al.taskStore, f.al.taskExecutor)
	s.SetClock(f.clk)
	err := s.Start()
	var typed *json.UnmarshalTypeError
	require.ErrorAs(t, err, &typed)
	assert.Contains(t, typed.Field, "enabled")
	after, e := os.ReadFile(f.path)
	require.NoError(t, e)
	assert.Equal(t, damaged, after)
	assert.Zero(t, f.p.turnsStarted())
}

func t10T8WriteOrdering(t *testing.T) {
	f := t10T8(t)
	tk := f.create(t, "once")
	edge, ok := any(f.s).(interface{ SetTaskReconcileWriteHookForTest(func(string)) })
	if !ok {
		t.Fatal("BLOCKED: needs T8 backend: ordered task write→goal sync→scheduler notification boundary absent — r17 12e/18.21–22")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var free sync.Once
	edge.SetTaskReconcileWriteHookForTest(func(id string) {
		if id == tk.ID {
			once.Do(func() { close(entered); <-release })
		}
	})
	t.Cleanup(func() { free.Do(func() { close(release) }); edge.SetTaskReconcileWriteHookForTest(nil) })
	inst, ok := f.al.registry.GetAgent("native-agent")
	require.True(t, ok)
	tool, ok := inst.Tools.Get("update_task")
	require.True(t, ok)
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "native-agent"), tk.WorkspaceID)
	updated := *tk.Trigger.Config.AtMs + 3600000
	a, b := make(chan *tools.ToolResult, 1), make(chan *tools.ToolResult, 1)
	go func() {
		a <- tool.Execute(ctx, map[string]any{"task_id": tk.ID, "trigger": map[string]any{"type": "once", "config": map[string]any{"at_ms": updated}}})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual writer never reached ordering boundary")
	}
	started := make(chan struct{})
	go func() {
		close(started)
		b <- tool.Execute(ctx, map[string]any{"task_id": tk.ID, "trigger": map[string]any{"type": "manual", "config": map[string]any{}}})
	}()
	<-started
	var early *tools.ToolResult
	select {
	case early = <-b:
		t.Error("second writer completed while first still owns ordered boundary")
	case <-time.After(25 * time.Millisecond):
	}
	free.Do(func() { close(release) })
	ar := <-a
	br := early
	if br == nil {
		br = <-b
	}
	require.False(t, ar.IsError, ar.ForLLM)
	require.False(t, br.IsError, br.ForLLM)
	fresh, e := f.al.taskStore.Get(tk.ID)
	require.NoError(t, e)
	require.NotNil(t, fresh.Trigger)
	assert.Equal(t, task.TriggerManual, fresh.Trigger.Type)
	assert.Empty(t, f.jobs(t, tk.ID))
	assert.Zero(t, f.p.turnsStarted())
}

func t10T8ReloadIdentity(t *testing.T) {
	f := t10T8(t)
	tk := f.create(t, "once")
	jobs := f.jobs(t, tk.ID)
	require.Len(t, jobs, 1)
	f.clk.Advance(2 * time.Hour)
	before := f.s
	// Kernel reload is real; gateway full-cycle coverage is separately in
	// TestReload_DoesNotRestartEngineOrStrandRunning (not claimed equivalent).
	cfg, err := f.al.cfg.Clone()
	require.NoError(t, err)
	f.al.SwapConfig(cfg)
	assert.Same(t, before, f.al.taskTriggerScheduler())
	assert.Equal(t, jobs, f.jobs(t, tk.ID))
	f.s.RunDueJobs(f.clk.Now())
	f.s.WaitForLane()
	f.terminal(t, tk.ID)
	assert.Equal(t, float64(*tk.Trigger.Config.AtMs), t10InternalMarker(t, f.al.taskStore, tk.ID))
}

func t10InheritedT8Row(t *testing.T, id string) {
	switch id {
	case "12b-18.11-18.14":
		t10T8ToolWaiting(t)
	case "12c-18.15-18.18-18.23":
		t10T8ForeignActivation(t)
	case "12d-18.19-18.20-18.24":
		t10T8AutomaticPromotions(t)
	case "12d-18.27-18.29":
		t10T8ExpectedWaiting(t, false)
	case "12d-18.31-18.32":
		t10T8ExpectedWaiting(t, true)
	case "12d-18.34":
		t10T8QueuedRegistration(t, false)
	case "12d-18.36":
		t10T8QueuedRegistration(t, true)
	case "12d-18.37":
		t10T8PhysicalBoot(t)
	case "12e-18.21-18.22":
		t10T8WriteOrdering(t)
	case "12e-18.25-18.26":
		t10T8DeferredRearm(t)
	case "12f-18.30-18.33":
		t10T8MarkerLifecycle(t)
	case "12f-18.35-18.38":
		t10T8StaleDelivery(t)
	case "12f-18.39":
		t10T8CheckedLoad(t)
	case "12f-18.40":
		t10T8ReloadIdentity(t)
	default:
		t.Fatalf("unbound T8 contract row %q", id)
	}
}
