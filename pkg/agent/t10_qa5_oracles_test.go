//go:build goolm && stdjson

package agent

// Round-5 independent oracles for the CHECK6 behaviour findings B1 and B2
// (/workspace/logs/team-a-restart-20261107/T10-CHECK6-REPORT.md), written from
// the spec (FR-05 single owner, S-B.8, section 8) and the report, not from the
// implementing lead's tests. Real stores, engine, command handler, turns and
// event bus; a loop-state write is failed through the in-process session
// writer seam (never file permissions), so every test also runs as root.
//
// Oracles:
//   - B1: a callback whose run-limit stop failed to save never adds a retry or
//     step once a replacement loop started; the replacement keeps its own sole
//     job and runs its first turn at its own first instant. Stale callbacks
//     (also repeated) change nothing.
//   - B2: a retry created from a failed stop never runs a model turn, even
//     when the completed count was lost too; boot stops such a loop.
//   - A user /loop stop or replacement racing the pending retry, and an idle
//     sweep during the pending retry, each leave exactly one stop.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// qa5Fail is a loop-state write failure plan for one session: the first
// stopFails stopped-state saves fail, and, when count is set, every
// active-state save (the completed-count update) fails too, until healed.
type qa5Fail struct {
	mu        sync.Mutex
	stopFails int
	count     bool
	stops     int
	counts    int
}

func qa5Install(t *testing.T, sid string, plan *qa5Fail) {
	t.Helper()
	t10InterceptLoopWrites(t, func(s string, p session.MetaPatch) error {
		if s != sid || p.LoopMode == nil {
			return nil
		}
		plan.mu.Lock()
		defer plan.mu.Unlock()
		if *p.LoopMode == "" {
			if plan.stopFails > 0 {
				plan.stopFails--
				plan.stops++
				return errors.New("injected stopped-state save failure")
			}
			return nil
		}
		if plan.count {
			plan.counts++
			return errors.New("injected completed-count save failure")
		}
		return nil
	})
}

func (p *qa5Fail) heal() {
	p.mu.Lock()
	p.stopFails, p.count = 0, false
	p.mu.Unlock()
}

// qa5Capped starts a loop of the given mode with a run limit of one.
func qa5Capped(t *testing.T, f *t10LoopFixture, interval bool, prompt string) {
	t.Helper()
	cmd := "/loop " + prompt
	if interval {
		cmd = "/loop every 15m " + prompt
	}
	f.command(t, cmd)
	one := 1
	require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopMaxRuns: &one}))
}

// qa5Tick advances the fake clock and lets the real engine dispatch.
func qa5Tick(f *t10LoopFixture, d time.Duration) {
	f.clk.Advance(d)
	f.ls.RunDueJobs(f.clk.Now())
	f.ls.WaitForLane()
}

func qa5Modes() []struct {
	name     string
	interval bool
} {
	return []struct {
		name     string
		interval bool
	}{{"self-paced", false}, {"interval", true}}
}

// B1. The old callback's stop save fails; before its fire returns, the user
// replaces the loop. Nothing of the old callback may touch the replacement.
func TestT10QA5_FailedRunLimitStopNeverReachesAReplacement(t *testing.T) {
	for _, mode := range qa5Modes() {
		t.Run(mode.name, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			qa5Capped(t, f, mode.interval, "old")
			one, delay := 1, int64(120000)
			require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopRunCount: &one, LoopNextDelayMS: &delay}))
			old, ok := f.ls.cs.GetJob(f.meta(t).LoopJobID)
			require.True(t, ok)
			plan := &qa5Fail{stopFails: 1}
			qa5Install(t, f.sid, plan)

			entered, release := make(chan struct{}), make(chan struct{})
			f.ls.SetRunLimitStopFailedTestHook(func() { close(entered); <-release })
			defer f.ls.SetRunLimitStopFailedTestHook(nil)
			done := make(chan error, 1)
			go func() { _, err := f.ls.RunScheduled(context.Background(), &old); done <- err }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("the failed run-limit stop was never reached")
			}
			plan.mu.Lock()
			require.Equal(t, 1, plan.stops, "instrument: the old callback's stop save failed")
			plan.mu.Unlock()

			// The user replaces the loop while the failed callback is still running.
			cmd := "/loop replacement"
			if mode.interval {
				cmd = "/loop every 15m replacement"
			}
			f.command(t, cmd)
			replacement := f.meta(t)
			require.Equal(t, "replacement", replacement.LoopPrompt)
			require.NotEqual(t, old.ID, replacement.LoopJobID)
			sub := f.al.SubscribeEvents(64)
			defer f.al.UnsubscribeEvents(sub.ID)

			close(release)
			require.ErrorContains(t, <-done, "injected stopped-state save failure")

			assert.Equal(t, replacement, f.meta(t), "the old callback left the replacement's saved state untouched")
			assert.Empty(t, t10LoopStates(sub.C), "the old callback published nothing")
			assert.Equal(t, int32(0), f.p.calls.Load(), "the old callback ran no turn")
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, jobs, 1, "the replacement is the session's only job")
			assert.Equal(t, replacement.LoopJobID, jobs[0].ID)
			assert.False(t, isStopOnlyStep(&jobs[0]), "the replacement's step is not a stop-only step")
			assert.True(t, f.ls.isSelectedStep(f.sid, replacement.LoopJobID))

			// Stale callbacks, repeated: still nothing happens.
			for i := 0; i < 2; i++ {
				stale := old
				_, err := f.ls.RunScheduled(context.Background(), &stale)
				require.NoError(t, err, "stale callback %d", i+1)
			}
			assert.Equal(t, replacement, f.meta(t))
			assert.Empty(t, t10LoopStates(sub.C))
			assert.Equal(t, int32(0), f.p.calls.Load())
			jobs = t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, jobs, 1)
			assert.Equal(t, replacement.LoopJobID, jobs[0].ID)

			// The replacement's first turn runs at its own first instant.
			require.NoError(t, f.ls.Start())
			first := time.Second
			if mode.interval {
				first = 15 * time.Minute
			}
			qa5Tick(f, first)
			assert.Equal(t, int32(1), f.p.calls.Load(), "the replacement's first turn ran")
			assert.Equal(t, 1, f.meta(t).LoopRunCount)
			assert.Equal(t, "replacement", f.meta(t).LoopPrompt)
		})
	}
}

// B2. Whatever the failed saves of the last permitted run were, no retry of
// the stop runs a model turn, and the stop is finally saved and announced once.
func TestT10QA5_RunLimitStopRetryNeverRunsAModelTurn(t *testing.T) {
	for _, mode := range qa5Modes() {
		for _, tc := range []struct {
			name      string
			stopFails int
			count     bool
		}{
			{"stop-save-fails-once", 1, false},
			{"count-and-stop-fail-once", 1, true},
			{"count-and-stop-fail-twice", 2, true},
		} {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				f := t10LoopSetup(t, false)
				qa5Capped(t, f, mode.interval, "capped")
				sub := f.al.SubscribeEvents(64)
				defer f.al.UnsubscribeEvents(sub.ID)
				plan := &qa5Fail{stopFails: tc.stopFails, count: tc.count}
				qa5Install(t, f.sid, plan)
				require.NoError(t, f.ls.Start())

				qa5Tick(f, 20*time.Minute) // the last permitted turn
				require.Equal(t, int32(1), f.p.calls.Load(), "the last permitted turn ran")
				plan.mu.Lock()
				require.GreaterOrEqual(t, plan.stops, 1, "instrument: the stopped-state save failed")
				if tc.count {
					require.GreaterOrEqual(t, plan.counts, 1, "instrument: the completed-count save failed")
				}
				plan.mu.Unlock()
				assert.NotEmpty(t, f.meta(t).LoopMode, "an unsaved stop leaves the loop saved as running")
				assert.Empty(t, t10LoopStates(sub.C), "nothing is announced for an unsaved stop")
				require.NotEmpty(t, t10SessionLoopJobs(f.ls, f.sid), "a retry of the stop exists")

				for i := 0; i < 10 && f.meta(t).LoopMode != ""; i++ {
					qa5Tick(f, 20*time.Minute)
					assert.Equal(t, int32(1), f.p.calls.Load(), "retry %d ran no model turn", i+1)
				}
				assert.Empty(t, f.meta(t).LoopMode, "the stop is finally saved")
				assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
				assert.Equal(t, int32(1), f.p.calls.Load(), "exactly the one permitted model turn ran")
				assert.Equal(t, []string{"stopped"}, t10LoopStates(sub.C), "the stop is announced exactly once")
			})
		}
	}
}

// B2 at boot. After the last run's count and stop were both lost, a gateway
// restart (fresh engine from disk, same stores) stops the loop without a turn.
func TestT10QA5_BootStopsALoopWhoseCountAndStopWereLost(t *testing.T) {
	for _, mode := range qa5Modes() {
		t.Run(mode.name, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			qa5Capped(t, f, mode.interval, "capped")
			plan := &qa5Fail{stopFails: 1, count: true}
			qa5Install(t, f.sid, plan)
			require.NoError(t, f.ls.Start())
			qa5Tick(f, 20*time.Minute)
			require.Equal(t, int32(1), f.p.calls.Load())
			plan.mu.Lock()
			require.Equal(t, 1, plan.stops, "instrument: the stop save failed")
			require.GreaterOrEqual(t, plan.counts, 1, "instrument: the count save failed")
			plan.mu.Unlock()
			require.Equal(t, 0, f.meta(t).LoopRunCount, "precondition: the count was lost")
			require.NotEmpty(t, f.meta(t).LoopMode, "precondition: the loop is saved as running")
			f.ls.Stop()
			plan.heal()

			cs := cron.NewCronService(f.path)
			ls := NewLoopScheduler(cs, f.al)
			ls.SetClock(f.clk)
			require.NoError(t, cs.LoadAfterPhysicalBoot())
			require.NoError(t, cs.ClearRunningAfterPhysicalBoot(nil))
			sub := f.al.SubscribeEvents(64)
			defer f.al.UnsubscribeEvents(sub.ID)
			require.NoError(t, ls.Reconcile())
			assert.Empty(t, f.meta(t).LoopMode, "boot saves the stopped state")
			assert.Empty(t, t10SessionLoopJobs(ls, f.sid), "boot leaves no step for a stopped loop")

			require.NoError(t, ls.Start())
			defer ls.Stop()
			f.clk.Advance(time.Hour)
			ls.RunDueJobs(f.clk.Now())
			ls.WaitForLane()
			assert.Equal(t, int32(1), f.p.calls.Load(), "no model turn after boot")
			assert.Empty(t, f.meta(t).LoopMode)
			assert.Empty(t, t10SessionLoopJobs(ls, f.sid))
		})
	}
}

// qa5PendingRetry leaves a self-paced loop whose last run's count and stop
// saves failed, with its stop-only retry pending and storage healed.
func qa5PendingRetry(t *testing.T) (*t10LoopFixture, cron.CronJob) {
	t.Helper()
	f := t10LoopSetup(t, false)
	qa5Capped(t, f, false, "capped")
	plan := &qa5Fail{stopFails: 1, count: true}
	qa5Install(t, f.sid, plan)
	require.NoError(t, f.ls.Start())
	qa5Tick(f, time.Second)
	require.Equal(t, int32(1), f.p.calls.Load(), "the last permitted turn ran")
	plan.mu.Lock()
	require.Equal(t, 1, plan.stops, "instrument: the stop save failed")
	plan.mu.Unlock()
	plan.heal()
	jobs := t10SessionLoopJobs(f.ls, f.sid)
	require.Len(t, jobs, 1, "the stop's retry is pending")
	return f, jobs[0]
}

// A user /loop stop or a replacement arrives while the retry is already
// firing: the retry changes nothing; there is exactly the user's change.
func TestT10QA5_UserCommandRacingThePendingStopRetry(t *testing.T) {
	for _, command := range []string{"/loop stop", "/loop replacement"} {
		t.Run(command, func(t *testing.T) {
			f, retry := qa5PendingRetry(t)
			entered, release := make(chan struct{}), make(chan struct{})
			f.ls.SetRunLimitStopTestHook(func() { close(entered); <-release })
			defer f.ls.SetRunLimitStopTestHook(nil)
			done := make(chan error, 1)
			go func() { _, err := f.ls.RunScheduled(context.Background(), &retry); done <- err }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("the retry never reached its stop")
			}
			sub := f.al.SubscribeEvents(64)
			defer f.al.UnsubscribeEvents(sub.ID)
			f.command(t, command)
			afterCommand := f.meta(t)
			close(release)
			require.NoError(t, <-done)

			assert.Equal(t, afterCommand, f.meta(t), "the racing retry changed no saved state")
			assert.Equal(t, int32(1), f.p.calls.Load(), "no extra model turn")
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			if command == "/loop stop" {
				assert.Empty(t, f.meta(t).LoopMode)
				assert.Empty(t, jobs)
				assert.Equal(t, []string{"stopped"}, t10LoopStates(sub.C), "exactly one stopped frame: the user's")
				return
			}
			assert.Equal(t, "replacement", f.meta(t).LoopPrompt)
			assert.NotEmpty(t, f.meta(t).LoopMode, "the replacement keeps running")
			require.Len(t, jobs, 1)
			assert.Equal(t, afterCommand.LoopJobID, jobs[0].ID)
			assert.Equal(t, []string{"stopped", "active"}, t10LoopStates(sub.C), "only the replacement's own frames")
			qa5Tick(f, time.Second)
			assert.Equal(t, int32(2), f.p.calls.Load(), "the replacement runs its first turn at its own time")
		})
	}
}

// An idle sweep that runs while the retry is pending stops the loop once; the
// retry that fires afterwards (also a stale copy of it) adds nothing.
func TestT10QA5_IdleSweepDuringThePendingStopRetry(t *testing.T) {
	f, retry := qa5PendingRetry(t)
	sub := f.al.SubscribeEvents(64)
	defer f.al.UnsubscribeEvents(sub.ID)
	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopStartedAt: &old, LoopLastActivityAt: &old}))

	f.ls.IdleExpirySweep(config.PlanningConfig{}, now)
	assert.Empty(t, f.meta(t).LoopMode, "the sweep expired the loop")
	assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid), "the sweep removed the pending retry")
	assert.Equal(t, []string{"stopped"}, t10LoopStates(sub.C), "one stopped frame")

	stale := retry
	_, err := f.ls.RunScheduled(context.Background(), &stale)
	require.NoError(t, err)
	qa5Tick(f, time.Hour)
	assert.Equal(t, int32(1), f.p.calls.Load(), "no model turn after the sweep")
	assert.Empty(t, t10LoopStates(sub.C), "the late retry announces nothing")
	assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
}
