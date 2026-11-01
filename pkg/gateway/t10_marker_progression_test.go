package gateway

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/cron"
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
	case "genuine-trigger-edit":
		thursday := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC).UnixMilli()
		w = patchTask(t, f.api, f.id, fmt.Sprintf(`{"trigger":{"type":"once","config":{"at_ms":%d}}}`, thursday))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		t10AssertConsumptionMarker(t, f, nil)
		jobs := t10ReadTaskJobs(t, f.path, f.id)
		require.Len(t, jobs, 1)
		assert.Equal(t, &thursday, jobs[0].State.NextRunAtMS)
		return
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
