package gateway

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reconstruct only on a physical restart. Uses the real persisted store and
// scheduler; no test-side next-occurrence computation or marker implementation.
func t10PhysicalTaskRestart(t *testing.T, f *t10TaskEntryFixture) {
	t.Helper()
	f.s.Stop()
	f.s.WaitForLane()
	cs := cron.NewCronService(f.path)
	ctor := reflect.ValueOf(agent.NewTaskTriggerScheduler)
	require.Equal(t, 3, ctor.Type().NumIn(), "bind actual scheduler constructor; retain restart/marker assertions")
	require.Equal(t, 1, ctor.Type().NumOut())
	require.Equal(t, reflect.TypeOf((*agent.TaskTriggerScheduler)(nil)), ctor.Type().Out(0))
	first := reflect.ValueOf(f.path)
	if ctor.Type().In(0) == reflect.TypeOf(cs) {
		first = reflect.ValueOf(cs)
	}
	args := []reflect.Value{first, reflect.ValueOf(f.api.taskStore), reflect.ValueOf(f.api.taskExecutor)}
	for i, arg := range args {
		require.True(t, arg.Type().AssignableTo(ctor.Type().In(i)), "bind actual restart argument %d", i)
	}
	s := ctor.Call(args)[0].Interface().(*agent.TaskTriggerScheduler)
	s.SetClock(f.clk)
	require.NoError(t, s.Start())
	require.NoError(t, s.Reconcile())
	f.api.agentLoop.SetTaskTriggerScheduler(s)
	f.s = s
	t.Cleanup(func() { s.Stop(); s.WaitForLane() })
}

func t10MarkerProgression(t *testing.T, variant string) {
	// S-H.7's genuine-replacement clause, rewritten per the T8 ruling: a
	// finished one-time task retires its schedule, so this row is a waiting
	// (not done) one-time task. It does not share the recurring run-now setup,
	// which leaves the task done.
	if variant == "genuine-trigger-edit" {
		t10GenuineOnceTriggerEdit(t)
		return
	}
	f := t10TaskEntry(t, "manual")
	monday := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)
	tuesday := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC).UnixMilli()
	wednesday := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC).UnixMilli()
	f.clk.mu.Lock()
	f.clk.at = monday
	f.clk.mu.Unlock()
	count := 3
	if variant == "final-COUNT" {
		count = 1
	}
	trigger := fmt.Sprintf(`{"type":"recurring","config":{"rrule":"FREQ=DAILY;COUNT=%d","dtstart_ms":%d,"tz":"UTC"}}`, count, tuesday)
	w := patchTask(t, f.api, f.id, `{"trigger":`+trigger+`}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	original := t10ReadTaskJobs(t, f.path, f.id)
	require.Len(t, original, 1)
	require.Equal(t, &tuesday, original[0].State.NextRunAtMS)
	f.at = tuesday
	w = t10RunRequest(f, nil)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	runs := t10AwaitCompletedRuns(t, f, 1)
	assert.Equal(t, &tuesday, runs[0].OccurrenceMs)
	t10AssertConsumptionMarker(t, f, &tuesday)
	f.clk.advance(9 * time.Hour) // Monday 23:00, still before Tuesday's consumed instant.
	switch variant {
	case "physical-restart":
		t10PhysicalTaskRestart(t, f)
	case "reconcile":
		require.NoError(t, f.s.Reconcile())
	case "identical-save":
		w = patchTask(t, f.api, f.id, `{"trigger":`+trigger+`}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		t10AssertConsumptionMarker(t, f, &tuesday)
	case "final-COUNT":
		require.NoError(t, f.s.Reconcile())
	}
	jobs := t10ReadTaskJobs(t, f.path, f.id)
	if variant == "final-COUNT" {
		for _, j := range jobs {
			assert.Nil(t, j.State.NextRunAtMS, "last COUNT consumed: nothing left to arm")
		}
	} else {
		require.Len(t, jobs, 1)
		assert.Equal(t, &wednesday, jobs[0].State.NextRunAtMS, "max(now, scheduled marker), not Monday consumption wall clock")
	}
	// Tuesday cannot replay, including after physical restart before its instant.
	f.clk.advance(time.UnixMilli(tuesday).Sub(f.clk.Now()))
	f.s.RunDueJobs(f.clk.Now())
	f.s.WaitForLane()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.True(t, f.api.agentLoop.WaitForActiveRequestsContext(ctx))
	after, err := f.api.taskStore.ListRuns(f.id)
	require.NoError(t, err)
	assert.Len(t, after, 1, "consumed Tuesday must never resurrect")
	if variant != "final-COUNT" {
		// WaitForActiveRequestsContext closes intake for the life of this
		// loop (pkg/agent/active_requests.go). The later Wednesday run must
		// be admitted by a new loop, as a process restart would; reusing the
		// drained loop cancels it ("admission refused after intake closed").
		t10BootFreshTaskInstance(t, f)
		f.clk.advance(time.UnixMilli(wednesday).Sub(f.clk.Now()))
		f.s.RunDueJobs(f.clk.Now())
		f.s.WaitForLane()
		next := t10AwaitCompletedRuns(t, f, 2)
		var instants []int64
		for _, r := range next {
			require.NotNil(t, r.OccurrenceMs)
			instants = append(instants, *r.OccurrenceMs)
		}
		assert.ElementsMatch(t, []int64{tuesday, wednesday}, instants, "a no-op scheduler cannot pass suppression alone")
	}
}

// t10BootFreshTaskInstance replaces the drained loop with a new process
// instance on the same home. The marker, runs and armed job already on disk
// stay; the new loop's intake is open, so the later occurrence can be admitted.
func t10BootFreshTaskInstance(t *testing.T, f *t10TaskEntryFixture) {
	t.Helper()
	f.s.Stop()
	f.s.WaitForLane()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(f.api.homePath, "workspace"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia"}, judgeAgentForTaskRunTests(t)},
		},
	}
	cfg.Sandbox.ToolPolicies = map[string]string{"goal_claim": "allow"}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &taskRunNowSuccessProvider{})
	bindMetVerdictJudge(t, &restAPI{agentLoop: al, taskStore: f.api.taskStore}, func() []string {
		g, e := tools.GoalStoreForTasks(f.api.taskStore).GetByOwner(gen.GoalOwnerKindTask, f.id)
		if e != nil || g == nil {
			return nil
		}
		ids := make([]string, 0, len(g.Criteria)+len(g.DoD))
		for _, c := range g.Criteria {
			ids = append(ids, c.ID)
		}
		for _, c := range g.DoD {
			ids = append(ids, c.ID)
		}
		return ids
	})
	exec := agent.GetTaskExecutor(al)
	require.NotNil(t, exec)
	cs := cron.NewCronService(f.path)
	ctor := reflect.ValueOf(agent.NewTaskTriggerScheduler)
	require.Equal(t, 3, ctor.Type().NumIn())
	first := reflect.ValueOf(f.path)
	if ctor.Type().In(0) == reflect.TypeOf(cs) {
		first = reflect.ValueOf(cs)
	}
	args := []reflect.Value{first, reflect.ValueOf(f.api.taskStore), reflect.ValueOf(exec)}
	for i, arg := range args {
		require.True(t, arg.Type().AssignableTo(ctor.Type().In(i)), "bind restarted scheduler argument %d", i)
	}
	s := ctor.Call(args)[0].Interface().(*agent.TaskTriggerScheduler)
	s.SetClock(f.clk)
	require.NoError(t, s.Start())
	require.NoError(t, s.Reconcile())
	al.SetTaskTriggerScheduler(s)
	f.api.agentLoop = al
	f.api.taskExecutor = exec
	f.s = s
	t.Cleanup(func() { s.Stop(); s.WaitForLane() })
}

// t10GenuineOnceTriggerEdit is S-H.7's genuine trigger replacement on a task
// that is still waiting, plus the T8 rule that a finished one-time task stays
// retired. Spec: schedules-t10-spec-r6.md S-H.7 ("a genuine trigger replacement
// clears it with the registration"). A terminal once task is over
// (task_trigger_reconcile.go reconcileFresh); a new one-time trigger does not
// re-arm it.
func t10GenuineOnceTriggerEdit(t *testing.T) {
	t.Helper()
	t.Run("waiting", func(t *testing.T) {
		f := t10TaskEntry(t, "once")
		stored, err := f.api.taskStore.Get(f.id)
		require.NoError(t, err)
		require.False(t, task.IsTerminal(stored.Status), "setup must be a not-done task")
		unlock := task.LockTaskWriteOrder(f.id)
		_, err = f.api.taskStore.ConsumeOccurrenceForRun(f.id, f.at)
		unlock()
		require.NoError(t, err)
		// ConsumeOccurrenceForRun resets the task to next. Waiting, for a
		// future one-time trigger, is inbox (r17 TK-30): not done, and not
		// released to the ordinary drain.
		inbox := task.StatusInbox
		_, err = f.api.taskStore.Update(f.id, task.Patch{Status: &inbox})
		require.NoError(t, err)
		waiting, err := f.api.taskStore.Get(f.id)
		require.NoError(t, err)
		require.Equal(t, task.StatusInbox, waiting.Status)
		t10AssertConsumptionMarker(t, f, &f.at)
		// The replacement instant is one day after the armed one, still in
		// the future of the fixture clock. A once registration arms at at_ms.
		edited := f.at + int64(24*time.Hour/time.Millisecond)
		w := patchTask(t, f.api, f.id, fmt.Sprintf(`{"trigger":{"type":"once","config":{"at_ms":%d}}}`, edited))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		t10AssertConsumptionMarker(t, f, nil)
		jobs := t10ReadTaskJobs(t, f.path, f.id)
		require.Len(t, jobs, 1)
		assert.Equal(t, &edited, jobs[0].State.NextRunAtMS, "genuine one-time edit arms the replacement instant")
		after, err := f.api.taskStore.Get(f.id)
		require.NoError(t, err)
		assert.Equal(t, task.StatusInbox, after.Status, "the edit must not finish the waiting task")
	})
	t.Run("done-stays-retired", func(t *testing.T) {
		f := t10TaskEntry(t, "once")
		done := task.StatusDone
		_, err := f.api.taskStore.Update(f.id, task.Patch{Status: &done})
		require.NoError(t, err)
		stored, err := f.api.taskStore.Get(f.id)
		require.NoError(t, err)
		require.Equal(t, task.StatusDone, stored.Status)
		edited := f.at + int64(48*time.Hour/time.Millisecond)
		w := patchTask(t, f.api, f.id, fmt.Sprintf(`{"trigger":{"type":"once","config":{"at_ms":%d}}}`, edited))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		jobs := t10ReadTaskJobs(t, f.path, f.id)
		assert.Empty(t, jobs, "a finished one-time task retires its schedule; a new one-time trigger stays retired")
		after, err := f.api.taskStore.Get(f.id)
		require.NoError(t, err)
		assert.Equal(t, task.StatusDone, after.Status)
	})
}
