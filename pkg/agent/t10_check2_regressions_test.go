//go:build goolm && stdjson

package agent

// Regression tests for the T10 CHECK2 findings F1, F2 and F3
// (/workspace/logs/team-a-restart-20261107/T10-CHECK2-REPORT.md), written red
// before the fixes by the implementing lead, adapted from the CHECK's
// supplemental probes. Oracles come from the spec of record
// (schedules-t10-spec-r6.md): FR-05(g) and section 9 "Session loop state"
// (replace/stop mutations share the per-session write lock with the
// selected-step check and post-turn writes), section 8 "Explicit
// non-behaviors" (no stopped loop is reported whose stopped state was never
// persisted), and FR-06 with the inherited 6b U2b contract (a one-time fire
// the gateway interrupts keeps its job for one replay at the next boot).
// Real stores, engine, command handler, executor and turns are used; only a
// filesystem write is delayed or failed, and the provider is a test double.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// F1: the shared engine can be stopped before the task-trigger scheduler
// (the gateway stops both, in either order). Either order is the gateway
// stopping, not a person, so the handed-off one-time run must end
// interrupted and keep its replay job.
func TestT10_SharedEngineStopFirstKeepsOnceReplay(t *testing.T) {
	for _, engineFirst := range []bool{false, true} {
		name := "scheduler-stop-first"
		if engineFirst {
			name = "shared-engine-stop-first"
		}
		t.Run(name, func(t *testing.T) {
			clk := newTriggerFakeClock()
			worker := newU2bAsyncWorker()
			p := newU2bAsyncProcess(t, t.TempDir(), clk, worker)
			fire := p.fireWhileProviderHeld(t, worker)
			if engineFirst {
				// The gateway's own call: CronService.Stop on the one shared
				// engine, before TaskTriggerScheduler.Stop.
				p.scheduler.cs.Stop()
				u2bAsyncJoin(t, "task settlement after the shared engine stopped", p.loop.taskExecutor.wg.Wait)
			}
			p.stop()
			stopped, err := p.loop.taskStore.Get(fire.taskID)
			require.NoError(t, err)
			assert.Equal(t, InterruptedTaskResult, stopped.Result,
				"the gateway stopping is not a person's Stop; the one-time fire is owed at the next boot")
			assert.Len(t, p.storedJobs(t, fire.taskID), 1,
				"a one-time run the gateway interrupted keeps its durable replay job")
		})
	}
}

// F2: a /loop stop or replacement issued while post-turn settlement holds the
// session lock must wait for it, then remove the settled turn's successor too.
func TestT10_LoopStopReplaceSerializeWithPostTurnSettle(t *testing.T) {
	for _, action := range []string{"stop", "replacement"} {
		for _, raced := range []bool{false, true} {
			name := action + "/serial"
			if raced {
				name = action + "/during-settle"
			}
			t.Run(name, func(t *testing.T) {
				f := t10LoopSetup(t, false)
				f.command(t, "/loop original")
				old, ok := f.ls.cs.GetJob(f.meta(t).LoopJobID)
				require.True(t, ok)
				if !raced {
					_, err := f.ls.RunScheduled(context.Background(), &old)
					require.NoError(t, err)
					f.command(t, "/loop "+action)
				} else {
					removedDuringSettle := t10RaceCommandIntoSettle(t, f, &old, "/loop "+action)
					assert.False(t, removedDuringSettle,
						"the command changed the engine while settlement held the session lock")
				}
				jobs := t10SessionLoopJobs(f.ls, f.sid)
				if action == "stop" {
					assert.Empty(t, jobs, "a stopped loop leaves no loop job, not the settled turn's successor")
					assert.Empty(t, f.meta(t).LoopMode)
					return
				}
				require.Len(t, jobs, 1, "the replacement's first step is the session's only loop job")
				m := f.meta(t)
				assert.Equal(t, jobs[0].ID, m.LoopJobID)
				assert.Equal(t, "replacement", m.LoopPrompt)
				assert.Equal(t, 0, m.LoopRunCount, "the old turn does not count against the replacement")
				assert.True(t, f.ls.isSelectedStep(f.sid, m.LoopJobID),
					"the replacement's first step owns the loop")
			})
		}
	}
}

// t10RaceCommandIntoSettle runs old's turn, holds its settlement inside the
// real run-count write (session lock held), issues command, and reports
// whether the command reached the engine before settlement was released.
func t10RaceCommandIntoSettle(t *testing.T, f *t10LoopFixture, old *cron.CronJob, command string) bool {
	t.Helper()
	entered, release, removed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var holdOnce, releaseOnce, removedOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	restoreWrite := t10InterceptLoopWrites(t, func(sid string, p session.MetaPatch) error {
		if sid == f.sid && p.LoopMode != nil && *p.LoopMode != "" {
			holdOnce.Do(func() { close(entered); <-release })
		}
		return nil
	})
	defer restoreWrite()
	turnDone := make(chan error, 1)
	go func() { _, err := f.ls.RunScheduled(context.Background(), old); turnDone <- err }()
	t10AwaitEdge(t, entered, "the settled turn's run-count write under the session lock")
	restoreSave := f.ls.cs.SetSaveStoreTestHook(func() error {
		removedOnce.Do(func() { close(removed) })
		return nil
	})
	commandDone := make(chan string, 1)
	go func() {
		_, _, reply := f.al.applyLoopCommandPrompt(context.Background(), bus.InboundMessage{Content: command}, f.inst,
			&processOptions{UserInitiated: true, TranscriptStore: f.store, TranscriptSessionID: f.sid})
		commandDone <- reply
	}()
	removedDuringSettle := false
	select {
	case <-removed:
		removedDuringSettle = true
	case <-time.After(2 * time.Second):
	}
	restoreSave()
	unblock()
	require.NoError(t, <-turnDone)
	reply := <-commandDone
	require.NotContains(t, reply, "Could not")
	return removedDuringSettle
}

func t10AwaitEdge(t *testing.T, edge <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-edge:
	case <-time.After(5 * time.Second):
		t.Fatalf("could not reach the instrumented edge: %s", what)
	}
}

// F3: when the stopped state cannot be saved, the command must not report a
// stop, and the loop stays exactly as saved (one live step, still selected).
func TestT10_LoopStopStateWriteFailureIsNotReportedAsStopped(t *testing.T) {
	for _, tc := range []struct {
		command   string
		wantReply string
	}{
		{"/loop stop", "Could not stop the loop"},
		{"/loop replacement", "Could not start the loop"},
	} {
		t.Run(strings.TrimPrefix(tc.command, "/loop "), func(t *testing.T) {
			f := t10LoopSetup(t, false)
			f.command(t, "/loop original")
			before := f.meta(t)
			injected := errors.New("injected stopped-state write failure")
			failures := 0
			restore := t10InterceptLoopWrites(t, func(sid string, p session.MetaPatch) error {
				if sid == f.sid && p.LoopMode != nil && *p.LoopMode == "" {
					failures++
					return injected
				}
				return nil
			})
			matched, handled, reply := f.al.applyLoopCommandPrompt(context.Background(),
				bus.InboundMessage{Content: tc.command}, f.inst,
				&processOptions{UserInitiated: true, TranscriptStore: f.store, TranscriptSessionID: f.sid})
			require.True(t, matched && handled)
			require.Equal(t, 1, failures, "instrument: exactly the stopped-state write failed")
			restore()
			assert.True(t, strings.HasPrefix(reply, tc.wantReply), "reply %q", reply)
			assert.NotEqual(t, "Loop stopped.", reply)

			after := f.meta(t)
			assert.Equal(t, before.LoopMode, after.LoopMode)
			assert.Equal(t, "original", after.LoopPrompt)
			assert.Equal(t, before.LoopJobID, after.LoopJobID)
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, jobs, 1, "a loop not reported stopped keeps its live step")
			assert.Equal(t, before.LoopJobID, jobs[0].ID)

			require.NoError(t, f.ls.Reconcile())
			jobs = t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, jobs, 1, "boot recovery keeps exactly one step of the still-active loop")
			assert.Equal(t, f.meta(t).LoopJobID, jobs[0].ID)
		})
	}
}
