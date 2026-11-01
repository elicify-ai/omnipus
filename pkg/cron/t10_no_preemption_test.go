package cron

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// S-D.3 / D-2: a healthy->pressure transition gates new dispatches without
// cancelling already admitted work. All admission decisions are production's.
func TestT10_MemoryPressureDoesNotPreemptLiveDispatches(t *testing.T) {
	cs, clk, _ := t10Seed(t, "heartbeat", "loop", "task", "heartbeat", "task")
	t10Start(t, cs)
	for _, id := range []string{"heartbeat-3", "task-4"} {
		job, ok := cs.GetJob(id)
		require.True(t, ok)
		later, next := int64(120000), t10T0.Add(2*time.Minute).UnixMilli()
		job.Schedule.EveryMS = &later
		job.State.NextRunAtMS = &next
		require.NoError(t, cs.UpdateJob(&job))
	}
	var pressure atomic.Bool
	restore := config.SetMemoryProviderForTest(func() (bool, bool) { return pressure.Load(), true }, func() (uint64, bool) { return 16 << 30, true })
	defer restore()
	var logs t10LogBuffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(prior)
	type dispatch struct {
		id  string
		ctx context.Context
	}
	entered := make(chan dispatch, 5)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	cs.SetRunner(t10RunnerFunc(func(ctx context.Context, j *CronJob) (string, error) {
		entered <- dispatch{j.ID, ctx}
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return "settled-" + j.ID, nil
	}))
	clk.Set(t10T0.Add(time.Minute))
	cs.RunDueJobs(clk.Now())
	first := []dispatch{t10Await(t, entered), t10Await(t, entered), t10Await(t, entered)}
	pressure.Store(true)
	clk.Set(t10T0.Add(2 * time.Minute))
	cs.RunDueJobs(clk.Now())
	assert.Eventually(t, func() bool { return len(entered) > 0 || len(t10MemoryWaits(t, &logs)) == 2 }, time.Second, time.Millisecond)
	assert.Empty(t, entered, "new work waits while three already-admitted handlers are alive")
	for _, d := range first {
		assert.NoError(t, d.ctx.Err(), "memory pressure must not cancel %s", d.id)
		j, ok := cs.GetJob(d.id)
		require.True(t, ok)
		assert.True(t, j.State.Running)
	}
	once.Do(func() { close(release) })
	cs.WaitForLane()
	seen := map[string]bool{}
	for _, d := range first {
		seen[d.id] = true
	}
	for len(seen) < 5 {
		seen[t10Await(t, entered).id] = true
	}
	waits := t10MemoryWaits(t, &logs)
	assert.Equal(t, map[string]int{"heartbeat-3": 1, "task-4": 1}, waits, "one memory line per actual wait, including after it settles")
	for _, j := range cs.ListJobs(true) {
		assert.Equal(t, "settled-"+j.ID, j.SessionID)
		assert.Equal(t, "ok", j.State.LastStatus)
	}
}
