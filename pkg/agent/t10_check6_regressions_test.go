//go:build goolm && stdjson

package agent

// Regression tests for the T10 CHECK6 findings B1 and B2
// (/workspace/logs/team-a-restart-20261107/T10-CHECK6-REPORT.md), written by
// the implementing lead and adapted from the CHECK's probes. Oracles: FR-05
// (one owner; a revoked callback never mutates a successor loop, S-B.8),
// spec section 8 (no turn past the run limit; no stopped loop reported unless
// saved; boot stops a loop at its limit) and the commissioned CHECK item 2
// (a retry of a failed run-limit stop never runs a model turn). Real stores,
// engine dispatch, command handler and event bus; only loop-state writes are
// failed or a callback is held at a test seam.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B1: a callback whose run-limit stop failed to save, and whose loop is then
// replaced, must not add a retry step that outranks the replacement's first
// step.
func TestT10_FailedRunLimitStopArmsNoRetryForAReplacement(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		name := "still-current"
		if replaced {
			name = "replaced-after-failed-save"
		}
		t.Run(name, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			f.command(t, "/loop capped-old")
			one := 1
			delay := int64(120000)
			require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopMaxRuns: &one, LoopRunCount: &one, LoopNextDelayMS: &delay}))
			old, ok := f.ls.cs.GetJob(f.meta(t).LoopJobID)
			require.True(t, ok)
			attempts := 0
			restore := t10InterceptLoopWrites(t, func(sid string, p session.MetaPatch) error {
				if sid == f.sid && p.LoopMode != nil && *p.LoopMode == "" && attempts == 0 {
					attempts++
					return errors.New("injected failed run-limit stop save")
				}
				return nil
			})
			defer restore()
			entered, release := make(chan struct{}), make(chan struct{})
			f.ls.SetRunLimitStopFailedTestHook(func() { close(entered); <-release })
			defer f.ls.SetRunLimitStopFailedTestHook(nil)
			done := make(chan error, 1)
			go func() { _, err := f.ls.RunScheduled(context.Background(), &old); done <- err }()
			t10AwaitEdge(t, entered, "the failed run-limit stop released the session lock")
			require.Equal(t, 1, attempts, "instrument: the stop save really failed")
			if replaced {
				f.command(t, "/loop replacement")
			}
			before := f.meta(t)
			sub := f.al.SubscribeEvents(64)
			defer f.al.UnsubscribeEvents(sub.ID)
			close(release)
			require.ErrorContains(t, <-done, "injected failed run-limit stop save")
			assert.Equal(t, before, f.meta(t), "the old callback changes no saved loop state after the lock")
			assert.Empty(t, t10LoopStates(sub.C), "the old callback publishes nothing")
			assert.Equal(t, int32(0), f.p.calls.Load())
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			if !replaced {
				// Direct entry: the fired one-shot is still there (the engine
				// deletes it after the handler) next to the one retry step.
				require.Len(t, jobs, 2, "the current owner still gets its retry step")
				return
			}
			require.Len(t, jobs, 1, "a revoked callback adds no step next to the replacement")
			assert.Equal(t, before.LoopJobID, jobs[0].ID)
			assert.True(t, f.ls.isSelectedStep(f.sid, before.LoopJobID), "the replacement keeps its first step")
			require.NoError(t, f.ls.Start())
			f.clk.Advance(time.Second)
			f.ls.RunDueJobs(f.clk.Now())
			f.ls.WaitForLane()
			assert.Equal(t, int32(1), f.p.calls.Load(), "the replacement runs its first turn at its own first instant")
			assert.Equal(t, 1, f.meta(t).LoopRunCount)
		})
	}
}

// t10RefuseLastRunWrites fails the loop-state writes of the session's last
// permitted run: always the stopped-state save, and with count also the
// completed-count save (the one active-state write of a run at its limit).
// It returns the refusal counters and the restore function.
func t10RefuseLastRunWrites(t *testing.T, sid string, count bool) (stops, counts *int, restore func()) {
	t.Helper()
	stops, counts = new(int), new(int)
	restore = t10InterceptLoopWrites(t, func(s string, p session.MetaPatch) error {
		if s != sid || p.LoopMode == nil {
			return nil
		}
		if *p.LoopMode == "" {
			*stops++
			return errors.New("injected stopped-state save failure")
		}
		if count {
			*counts++
			return errors.New("injected completed-count save failure")
		}
		return nil
	})
	return stops, counts, restore
}

// t10CappedLoop starts a loop of the given mode with a run limit of one.
func t10CappedLoop(t *testing.T, f *t10LoopFixture, interval bool) {
	t.Helper()
	cmd := "/loop capped"
	if interval {
		cmd = "/loop every 15m capped"
	}
	f.command(t, cmd)
	one := 1
	require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopMaxRuns: &one}))
}

// B2: when the last permitted run's count and stop both fail to save, the
// retry of the stop runs no model turn; it only stops the loop.
func TestT10_RunLimitStopRetryRunsNoTurnWhenTheCountWasLost(t *testing.T) {
	for _, interval := range []bool{false, true} {
		for _, lostCount := range []bool{false, true} {
			name := "self-paced"
			if interval {
				name = "interval"
			}
			if lostCount {
				name += "/count-and-stop-lost"
			} else {
				name += "/stop-lost"
			}
			t.Run(name, func(t *testing.T) {
				f := t10LoopSetup(t, false)
				t10CappedLoop(t, f, interval)
				sub := f.al.SubscribeEvents(64)
				defer f.al.UnsubscribeEvents(sub.ID)
				stops, counts, restore := t10RefuseLastRunWrites(t, f.sid, lostCount)
				defer restore()
				require.NoError(t, f.ls.Start())
				fire := func() { f.ls.RunDueJobs(f.clk.Now()); f.ls.WaitForLane() }
				f.clk.Advance(15 * time.Minute)
				fire()
				require.Equal(t, int32(1), f.p.calls.Load(), "the last permitted turn ran")
				require.Equal(t, 1, *stops, "instrument: the stopped-state save failed")
				if lostCount {
					require.Equal(t, 1, *counts, "instrument: the completed-count save failed too")
					assert.Equal(t, 0, f.meta(t).LoopRunCount, "the lost count is not saved")
				} else {
					assert.Equal(t, 1, f.meta(t).LoopRunCount)
				}
				assert.NotEmpty(t, f.meta(t).LoopMode, "an unsaved stop leaves the loop saved as running")
				assert.Empty(t, t10LoopStates(sub.C), "nothing is announced for an unsaved stop")
				jobs := t10SessionLoopJobs(f.ls, f.sid)
				require.Len(t, jobs, 1, "one step remains to retry the stop on")
				require.NotNil(t, jobs[0].State.NextRunAtMS)

				restore() // storage heals before the retry fires
				f.clk.Advance(time.UnixMilli(*jobs[0].State.NextRunAtMS).Sub(f.clk.Now()))
				fire()
				assert.Equal(t, int32(1), f.p.calls.Load(), "the retry of a failed run-limit stop runs no model turn")
				assert.Empty(t, f.meta(t).LoopMode, "the retry saves the stop")
				assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
				assert.Equal(t, []string{"stopped"}, t10LoopStates(sub.C), "the stop is announced once")
			})
		}
	}
}

// B2 at boot: a gateway restart after the count and the stop of the last
// permitted run were both lost stops the loop without a turn.
func TestT10_BootStopsALoopWhoseRunLimitCountAndStopWereLost(t *testing.T) {
	for _, interval := range []bool{false, true} {
		name := "self-paced"
		if interval {
			name = "interval"
		}
		t.Run(name, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			t10CappedLoop(t, f, interval)
			stops, counts, restore := t10RefuseLastRunWrites(t, f.sid, true)
			defer restore()
			require.NoError(t, f.ls.Start())
			f.clk.Advance(15 * time.Minute)
			f.ls.RunDueJobs(f.clk.Now())
			f.ls.WaitForLane()
			require.Equal(t, int32(1), f.p.calls.Load(), "the last permitted turn ran")
			require.Equal(t, 1, *stops, "instrument: the stopped-state save failed")
			require.Equal(t, 1, *counts, "instrument: the completed-count save failed")
			require.Len(t, t10SessionLoopJobs(f.ls, f.sid), 1)
			f.ls.Stop()
			restore()

			// Gateway restart: fresh session store and engine from disk.
			freshStore, err := session.NewUnifiedStore(f.store.BaseDir())
			require.NoError(t, err)
			defer freshStore.Close()
			previous := f.al.sharedSessionStore
			f.al.sharedSessionStore = freshStore
			defer func() { f.al.sharedSessionStore = previous }()
			saved, err := freshStore.GetMeta(f.sid)
			require.NoError(t, err)
			require.Equal(t, 0, saved.LoopRunCount, "precondition: the count was lost on disk")
			require.NotEmpty(t, saved.LoopMode, "precondition: the loop is saved as running")
			cs := cron.NewCronService(f.path)
			ls := NewLoopScheduler(cs, f.al)
			ls.SetClock(f.clk)
			require.NoError(t, cs.LoadAfterPhysicalBoot())
			require.NoError(t, cs.ClearRunningAfterPhysicalBoot(nil))
			require.NoError(t, ls.Reconcile())
			assert.Empty(t, t10SessionLoopJobs(ls, f.sid), "boot arms no step for a loop at its limit")
			recovered, err := freshStore.GetMeta(f.sid)
			require.NoError(t, err)
			assert.Empty(t, recovered.LoopMode, "boot saves the stopped state")
			assert.Equal(t, int32(1), f.p.calls.Load(), "no turn ran past the limit")
		})
	}
}
