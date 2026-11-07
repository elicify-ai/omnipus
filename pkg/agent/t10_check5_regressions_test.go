//go:build goolm && stdjson

package agent

// Regression tests for the T10 CHECK5 findings F1, F2 and the two surviving
// mutants O1/O2 of the CHECK4 replacement fix
// (/workspace/logs/team-a-restart-20261107/T10-CHECK5-REPORT.md), written by
// the implementing lead and adapted from the CHECK's probes. Oracles: FR-05
// (one owner; a revoked callback never mutates a successor loop, S-B.8) and
// spec section 8 (no turn past the saved run limit; no stopped loop reported
// unless saved). Real stores, engine dispatch, command handler and event bus;
// only a store write is failed or a callback is held at a test seam.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// F1: a callback that found its loop at the run limit, and is then revoked
// by a replacement before it stops the loop, must not touch the replacement.
func TestT10_RevokedRunLimitCallbackLeavesReplacementAlone(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		name := "still-current"
		if replaced {
			name = "replaced-before-stop"
		}
		t.Run(name, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			f.command(t, "/loop original")
			one := 1
			// The run limit was reached and its stop was never saved.
			require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopMaxRuns: &one, LoopRunCount: &one}))
			job, ok := f.ls.cs.GetJob(f.meta(t).LoopJobID)
			require.True(t, ok)
			sub := f.al.SubscribeEvents(64)
			defer f.al.UnsubscribeEvents(sub.ID)

			entered, release := make(chan struct{}), make(chan struct{})
			f.ls.SetRunLimitStopTestHook(func() { close(entered); <-release })
			defer f.ls.SetRunLimitStopTestHook(nil)
			done := make(chan error, 1)
			go func() { _, err := f.ls.RunScheduled(context.Background(), &job); done <- err }()
			t10AwaitEdge(t, entered, "the callback decided to stop the loop at its run limit")
			if replaced {
				f.command(t, "/loop replacement")
			}
			close(release)
			require.NoError(t, <-done)
			assert.Equal(t, int32(0), f.p.calls.Load(), "no turn runs at or past the run limit")
			states := t10LoopStates(sub.C)

			if !replaced {
				assert.Empty(t, f.meta(t).LoopMode)
				assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
				assert.Equal(t, []string{"stopped"}, states)
				return
			}
			m := f.meta(t)
			assert.Equal(t, loopModeSelfPaced, m.LoopMode, "the replacement is still active")
			assert.Equal(t, "replacement", m.LoopPrompt)
			assert.Equal(t, 0, m.LoopRunCount)
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, jobs, 1, "the replacement keeps its first step")
			assert.Equal(t, m.LoopJobID, jobs[0].ID)
			assert.Equal(t, []string{"stopped", "active"}, states,
				"only the old loop's stop and the replacement's start are announced")
		})
	}
}

// F2: a self-paced loop whose run-limit stop could not be saved keeps a step
// the real engine fires again, and that fire saves the stop without a turn.
func TestT10_SelfPacedRunLimitStopRetriesThroughTheEngine(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "stop-saved"
		if fail {
			name = "stop-save-fails-once"
		}
		t.Run(name, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			f.command(t, "/loop capped")
			one := 1
			require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopMaxRuns: &one}))
			sub := f.al.SubscribeEvents(64)
			defer f.al.UnsubscribeEvents(sub.ID)
			failures := 0
			restore := t10InterceptLoopWrites(t, func(sid string, p session.MetaPatch) error {
				if fail && failures == 0 && sid == f.sid && p.LoopMode != nil && *p.LoopMode == "" {
					failures++
					return errors.New("injected stopped-state save failure")
				}
				return nil
			})
			defer restore()
			require.NoError(t, f.ls.Start())
			fire := func() {
				f.clk.Advance(10 * time.Minute)
				f.ls.RunDueJobs(f.clk.Now())
				f.ls.WaitForLane()
			}
			fire()
			require.Equal(t, int32(1), f.p.calls.Load(), "the last permitted turn ran")
			if !fail {
				assert.Empty(t, f.meta(t).LoopMode)
				assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
				assert.Equal(t, []string{"stopped"}, t10LoopStates(sub.C))
				return
			}
			require.Equal(t, 1, failures, "instrument: exactly the stopped-state save failed")
			assert.Equal(t, loopModeSelfPaced, f.meta(t).LoopMode, "the unsaved stop changes nothing")
			assert.Empty(t, t10LoopStates(sub.C), "no status for an unsaved stop")
			require.Len(t, t10SessionLoopJobs(f.ls, f.sid), 1,
				"a step survives the engine deleting the fired one-shot, so the stop is retried")

			fire()
			assert.Equal(t, int32(1), f.p.calls.Load(), "the retry runs no turn past the limit")
			assert.Empty(t, f.meta(t).LoopMode, "the retry saves the stop")
			assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
			assert.Equal(t, []string{"stopped"}, t10LoopStates(sub.C), "the stop is announced once")
		})
	}
}

// CHECK5 O1/O2: a replacement refused because old jobs could not be removed
// says exactly what happened and announces the old loop's stop exactly once,
// also when the cleanup fails again on an already-stopped loop.
func TestT10_LoopReplacementRefusalIsExactAndAnnouncedOnce(t *testing.T) {
	f := t10LoopSetup(t, false)
	f.command(t, "/loop original")
	old, ok := f.ls.cs.GetJob(f.meta(t).LoopJobID)
	require.True(t, ok)
	_, err := f.ls.RunScheduled(context.Background(), &old)
	require.NoError(t, err)
	require.True(t, f.ls.RemoveJob(old.ID)) // the engine deletes a fired one-shot
	sub := f.al.SubscribeEvents(64)
	defer f.al.UnsubscribeEvents(sub.ID)
	replace := func(failRemoval bool) string {
		restore := f.ls.cs.SetSaveStoreTestHook(func() error {
			if failRemoval {
				return errors.New("injected engine save failure")
			}
			return nil
		})
		defer restore()
		_, _, reply := f.al.applyLoopCommandPrompt(context.Background(), bus.InboundMessage{Content: "/loop replacement"}, f.inst,
			&processOptions{UserInitiated: true, TranscriptStore: f.store, TranscriptSessionID: f.sid})
		return reply
	}

	assert.Equal(t, "Could not start the loop (internal error removing the previous schedule). The previous loop is stopped; try again.",
		replace(true))
	assert.Equal(t, []string{"stopped"}, t10LoopStates(sub.C), "the saved stop is announced exactly once")

	assert.Equal(t, "Could not start the loop (internal error removing the previous schedule). Try again.", replace(true),
		"a second failed cleanup of the already-stopped loop")
	assert.Empty(t, t10LoopStates(sub.C), "an already-stopped loop is not announced again")

	assert.NotContains(t, replace(false), "Could not")
	assert.Equal(t, []string{"active"}, t10LoopStates(sub.C))
	require.Len(t, t10SessionLoopJobs(f.ls, f.sid), 1)
}
