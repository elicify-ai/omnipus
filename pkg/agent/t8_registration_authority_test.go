package agent

// Oracle: tool-parity r17 §7.2 TK-30 and scenario 18.38. This isolates the
// real scheduler/cron registration boundary; dispatch is the process-edge
// recorder, NOT the full executor/plan/drain UAT row.
import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskTrigger_RevokedRecurringRearmKeepsQueuedSuccessor(t *testing.T) {
	for _, mode := range []string{"18.38 identical reattach", "18.38 different rule control", "18.38 ordinary continuation control"} {
		t.Run(mode, func(t *testing.T) {
			sched, store, clk, rec := newTriggerSchedulerForTest(t)
			sched.cs.SetMaxConcurrentRuns(1)
			dt := secondAligned(clk.Now().Add(time.Minute))
			tk := makeRruleTask(t, store, "t8-registration", "FREQ=DAILY", dt, "UTC")
			waiting := task.StatusInbox
			var err error
			tk, err = store.Update(tk.ID, task.Patch{Status: &waiting})
			require.NoError(t, err)
			sched.OnTaskUpserted(tk)
			original := jobsForTask(sched, tk.ID)
			require.Len(t, original, 1)
			entered, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			sched.SetRearmTestHook(func() {
				if calls.Add(1) == 1 {
					close(entered)
					<-release
				}
			})
			clk.Advance(2 * time.Minute)
			sched.RunDueJobs(clk.Now())
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("fixture did not reach the existing pre-lock re-arm seam")
			}
			require.Equal(t, []string{tk.ID}, rec.calls(), "positive control: J1 delivered before the seam")
			// Complete J1's launched task while its callback remains held. This is a
			// store boundary fixture, not a claim that a mock executed an agent turn.
			running := task.StatusInProgress
			_, err = store.Update(tk.ID, task.Patch{Status: &running})
			require.NoError(t, err)
			done := task.StatusDone
			_, err = store.Update(tk.ID, task.Patch{Status: &done})
			require.NoError(t, err)
			if mode == "18.38 ordinary continuation control" {
				unblock()
				sched.WaitForLane()
				next := jobsForTask(sched, tk.ID)
				require.Len(t, next, 1)
				require.NotNil(t, next[0].State.NextRunAtMS)
				assert.Equal(t, dt+oneDayMs, *next[0].State.NextRunAtMS)
				clk.Advance(24 * time.Hour)
				sched.RunDueJobs(clk.Now())
				sched.WaitForLane()
				assert.Equal(t, []string{tk.ID, tk.ID}, rec.calls(), "current callback continues once per boundary")
				return
			}
			manual := &task.Trigger{Type: task.TriggerManual}
			tk, err = store.Update(tk.ID, task.Patch{Trigger: &manual})
			require.NoError(t, err)
			sched.OnTaskUpserted(tk)
			require.Empty(t, jobsForTask(sched, tk.ID), "clear completed fully before reattach")
			rule := "FREQ=DAILY"
			if mode == "18.38 different rule control" {
				rule = "FREQ=DAILY;INTERVAL=2"
			}
			replacement := &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &dt, Tz: strP("UTC")}}
			tk, err = store.Update(tk.ID, task.Patch{Trigger: &replacement})
			require.NoError(t, err)
			sched.OnTaskUpserted(tk)
			successor := jobsForTask(sched, tk.ID)
			require.Len(t, successor, 1)
			require.NotEqual(t, original[0].ID, successor[0].ID, "reattach installs a genuinely new registration")
			require.NotNil(t, successor[0].State.NextRunAtMS)
			due := *successor[0].State.NextRunAtMS
			clk.Advance(time.UnixMilli(due).Add(time.Second).Sub(clk.Now()))
			sched.RunDueJobs(clk.Now())
			require.True(t, sched.cs.JobDispatchInFlight(successor[0].ID), "J2 is queued behind J1's occupied lane slot")
			assert.Equal(t, []string{tk.ID}, rec.calls(), "J2 cannot deliver before its lane slot frees")
			unblock()
			sched.WaitForLane()
			assert.Equal(t, []string{tk.ID, tk.ID}, rec.calls(), "revoked J1 must not replace queued J2; J2 delivers exactly once")
			for _, j := range []string{original[0].ID, successor[0].ID} {
				assert.False(t, sched.cs.JobDispatchInFlight(j), "original collected ID %s settles", j)
			}
			remaining := jobsForTask(sched, tk.ID)
			require.Len(t, remaining, 1, "one series, never a second job")
			// The delivered occurrence is the scheduled boundary, not callback wall time.
			occurrences := rec.occurrences()
			if assert.Len(t, occurrences, 2) {
				require.NotNil(t, occurrences[1])
				assert.Equal(t, due, *occurrences[1])
			}
		})
	}
}

// Keep the dispatch edge's signature checked without changing production
// visibility or introducing a synthetic scheduler implementation.
var _ func(context.Context, string, *int64) error = (&dispatchRecorder{}).dispatch
