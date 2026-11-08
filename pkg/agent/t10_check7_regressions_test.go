//go:build goolm && stdjson

package agent

// Regression tests for the T10 CHECK7 findings F1 and F2
// (/workspace/logs/team-a-restart-20261107/T10-CHECK7-REPORT.md), written by
// the implementing lead and adapted from the CHECK's probes. Oracles: FR-05
// (one owner; a revoked callback never mutates a successor loop, S-B.8),
// FR-05(h) (a failed recovery write aborts the boot and the next boot retries
// from durable state) and spec section 8 (no turn past the run limit). Real
// stores, engine dispatch and command handler; only loop-state writes are
// failed or a callback is held at a test seam.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// t10C7ColdBoot reconstructs the session store and the engine from disk, as a
// gateway start does, up to (not including) loop recovery.
func t10C7ColdBoot(t *testing.T, f *t10LoopFixture) (*LoopScheduler, *session.UnifiedStore) {
	t.Helper()
	store, err := session.NewUnifiedStore(f.store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	f.al.sharedSessionStore = store
	cs := cron.NewCronService(f.path)
	ls := NewLoopScheduler(cs, f.al)
	ls.SetClock(f.clk)
	require.NoError(t, cs.LoadAfterPhysicalBoot())
	require.NoError(t, cs.ClearRunningAfterPhysicalBoot(nil))
	return ls, store
}

// t10C7DiskJobs reads the engine's job file.
func t10C7DiskJobs(t *testing.T, path string) []cron.CronJob {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var disk cron.CronStore
	require.NoError(t, json.Unmarshal(data, &disk))
	return disk.Jobs
}

// F1: a boot that fails to save a pending stop keeps the saved stop-only
// retry, so the next successful boot still stops the loop without a turn.
func TestT10_FailedBootKeepsTheSavedStopOnlyRetry(t *testing.T) {
	for _, interval := range []bool{false, true} {
		for _, failedBoot := range []bool{false, true} {
			name := "self-paced"
			if interval {
				name = "interval"
			}
			if failedBoot {
				name += "/failed-boot-then-boot"
			} else {
				name += "/boot"
			}
			t.Run(name, func(t *testing.T) {
				f := t10LoopSetup(t, false)
				previous := f.al.sharedSessionStore
				defer func() { f.al.sharedSessionStore = previous }()
				t10CappedLoop(t, f, interval)
				stops, counts, restore := t10RefuseLastRunWrites(t, f.sid, true)
				require.NoError(t, f.ls.Start())
				f.clk.Advance(15 * time.Minute)
				f.ls.RunDueJobs(f.clk.Now())
				f.ls.WaitForLane()
				require.Equal(t, int32(1), f.p.calls.Load(), "the last permitted turn ran")
				require.Equal(t, 1, *stops, "instrument: the stopped-state save failed")
				require.Equal(t, 1, *counts, "instrument: the completed-count save failed")
				jobs := t10C7DiskJobs(t, f.path)
				require.Len(t, jobs, 1)
				require.Equal(t, loopStopOnlyCommand, jobs[0].Payload.Command, "precondition: the stop-only retry is saved")
				f.ls.Stop()
				restore()

				boot, store := t10C7ColdBoot(t, f)
				saved, err := store.GetMeta(f.sid)
				require.NoError(t, err)
				require.Equal(t, 0, saved.LoopRunCount, "precondition: the count was lost on disk")
				require.NotEmpty(t, saved.LoopMode, "precondition: the loop is saved as running")
				if failedBoot {
					refusals := 0
					heal := t10InterceptLoopWrites(t, func(sid string, p session.MetaPatch) error {
						if sid == f.sid && p.LoopMode != nil && *p.LoopMode == "" {
							refusals++
							return errors.New("injected boot stopped-state save failure")
						}
						return nil
					})
					err = boot.Reconcile()
					heal()
					require.ErrorContains(t, err, "injected boot stopped-state save failure")
					require.ErrorContains(t, err, f.sid, "the recovery error names the chat")
					require.Equal(t, 1, refusals, "instrument: recovery's stopped-state save failed")
					left := t10C7DiskJobs(t, f.path)
					require.Len(t, left, 1, "a failed boot keeps the saved stop-only retry")
					assert.Equal(t, jobs[0].ID, left[0].ID)
					assert.Equal(t, loopStopOnlyCommand, left[0].Payload.Command)
					boot, store = t10C7ColdBoot(t, f)
				}
				require.NoError(t, boot.Reconcile())
				recovered, err := store.GetMeta(f.sid)
				require.NoError(t, err)
				assert.Empty(t, recovered.LoopMode, "the boot saves the stop")
				assert.Empty(t, t10SessionLoopJobs(boot, f.sid), "the boot arms no step")
				require.NoError(t, boot.Start())
				defer boot.Stop()
				f.clk.Advance(15 * time.Minute)
				boot.RunDueJobs(f.clk.Now())
				boot.WaitForLane()
				assert.Equal(t, int32(1), f.p.calls.Load(), "no turn runs past the limit after the boots")
			})
		}
	}
}

// F2: the retry of a failed run-limit stop is armed while the session lock is
// held. Paused right where the lock is released, the retry already exists,
// and a replacement started at that point keeps sole ownership.
func TestT10_FailedRunLimitStopArmsItsRetryBeforeTheLockIsReleased(t *testing.T) {
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
	f.ls.SetStopUnlockedTestHook(func() { close(entered); <-release })
	defer f.ls.SetStopUnlockedTestHook(nil)
	done := make(chan error, 1)
	go func() { _, err := f.ls.RunScheduled(context.Background(), &old); done <- err }()
	t10AwaitEdge(t, entered, "the failed run-limit stop released the session lock")
	require.Equal(t, 1, attempts, "instrument: the stop save really failed")

	// The fired one-shot (deleted by the engine only after the handler) and
	// the retry, armed under the lock.
	paused := t10SessionLoopJobs(f.ls, f.sid)
	require.Len(t, paused, 2, "the retry is armed before the session lock is released")
	for _, j := range paused {
		if j.ID != old.ID {
			assert.Equal(t, loopStopOnlyCommand, j.Payload.Command)
		}
	}

	f.command(t, "/loop replacement")
	before := f.meta(t)
	sub := f.al.SubscribeEvents(64)
	defer f.al.UnsubscribeEvents(sub.ID)
	close(release)
	require.ErrorContains(t, <-done, "injected failed run-limit stop save")
	assert.Equal(t, before, f.meta(t), "the old callback changes no saved loop state after the lock")
	assert.Empty(t, t10LoopStates(sub.C), "the old callback publishes nothing")
	jobs := t10SessionLoopJobs(f.ls, f.sid)
	require.Len(t, jobs, 1, "the old callback adds no step next to the replacement")
	assert.Equal(t, before.LoopJobID, jobs[0].ID)
	assert.Empty(t, jobs[0].Payload.Command, "the replacement's first step is a turn step")
	require.NoError(t, f.ls.Start())
	f.clk.Advance(time.Second)
	f.ls.RunDueJobs(f.clk.Now())
	f.ls.WaitForLane()
	assert.Equal(t, int32(1), f.p.calls.Load(), "the replacement runs its first turn at its own first instant")
}
