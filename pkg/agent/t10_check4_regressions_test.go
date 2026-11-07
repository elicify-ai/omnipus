//go:build goolm && stdjson

package agent

// Regression tests for the T10 CHECK4 loop findings F1, F2 and F3
// (/workspace/logs/team-a-restart-20261107/T10-CHECK4-REPORT.md), written red
// before the fixes by the implementing lead and adapted from the CHECK's
// supplemental probes. Oracles: FR-05(g) (the stop/replace mutation, the
// post-turn writes and the status frame are one atomic set under the session
// lock; the replacement's first step is the session's selected step) and
// spec section 8 (no stopped loop is reported whose stopped state was never
// saved; no loop runs past its saved run limit). Real stores, engine, command
// handler, turns and event bus are used; only a store write is failed or an
// event publication is held.

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

// t10LoopStates drains every loop status event currently queued on ch, in
// publication order.
func t10LoopStates(ch <-chan Event) []string {
	states := []string{}
	for len(ch) > 0 {
		if p, ok := (<-ch).Payload.(LoopStatusChangedPayload); ok {
			states = append(states, p.State)
		}
	}
	return states
}

// F1: a replacement whose removal of the previous loop's jobs fails must not
// start: an old later step would otherwise outrank the new first step.
func TestT10_LoopReplacementRefusedWhenOldJobsCannotBeRemoved(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "removal-succeeds"
		if fail {
			name = "removal-fails-once"
		}
		t.Run(name, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			f.command(t, "/loop original")
			old, ok := f.ls.cs.GetJob(f.meta(t).LoopJobID)
			require.True(t, ok)
			_, err := f.ls.RunScheduled(context.Background(), &old)
			require.NoError(t, err)
			require.True(t, f.ls.RemoveJob(old.ID)) // the engine deletes a fired one-shot
			successor := t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, successor, 1, "precondition: the settled turn's later successor exists")

			sub := f.al.SubscribeEvents(64)
			defer f.al.UnsubscribeEvents(sub.ID)
			failures := 0
			restore := f.ls.cs.SetSaveStoreTestHook(func() error {
				if fail && failures == 0 {
					failures++
					return errors.New("injected engine save failure")
				}
				return nil
			})
			_, _, reply := f.al.applyLoopCommandPrompt(context.Background(), bus.InboundMessage{Content: "/loop replacement"}, f.inst,
				&processOptions{UserInitiated: true, TranscriptStore: f.store, TranscriptSessionID: f.sid})
			restore()
			states := t10LoopStates(sub.C)

			if !fail {
				assert.NotContains(t, reply, "Could not")
				jobs := t10SessionLoopJobs(f.ls, f.sid)
				require.Len(t, jobs, 1)
				assert.True(t, f.ls.isSelectedStep(f.sid, f.meta(t).LoopJobID))
				assert.Equal(t, []string{"stopped", "active"}, states)
				return
			}
			require.Equal(t, 1, failures, "instrument: exactly the bulk removal save failed")
			assert.True(t, strings.HasPrefix(reply, "Could not start the loop"), "reply %q", reply)
			assert.Empty(t, f.meta(t).LoopMode, "no replacement is active")
			assert.NotContains(t, states, "active", "no replacement is announced")
			for _, j := range t10SessionLoopJobs(f.ls, f.sid) {
				assert.Equal(t, successor[0].ID, j.ID, "no replacement step was added")
			}

			// Once the store works again the replacement starts and owns the
			// session's only step.
			f.command(t, "/loop replacement")
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, jobs, 1)
			m := f.meta(t)
			assert.Equal(t, jobs[0].ID, m.LoopJobID)
			assert.Equal(t, "replacement", m.LoopPrompt)
			assert.True(t, f.ls.isSelectedStep(f.sid, m.LoopJobID))
		})
	}
}

// F2: at the run limit, a failed stopped-state save changes nothing else and
// announces nothing; the loop still runs no turn past its limit, and the
// next fire stops it for real.
func TestT10_LoopRunLimitStopSaveFailureIsNotAnnounced(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "stop-saved"
		if fail {
			name = "stop-save-fails"
		}
		t.Run(name, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			f.command(t, "/loop capped")
			one := 1
			require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopMaxRuns: &one}))
			job, ok := f.ls.cs.GetJob(f.meta(t).LoopJobID)
			require.True(t, ok)
			sub := f.al.SubscribeEvents(64)
			defer f.al.UnsubscribeEvents(sub.ID)
			failures := 0
			restore := t10InterceptLoopWrites(t, func(sid string, p session.MetaPatch) error {
				if fail && sid == f.sid && p.LoopMode != nil && *p.LoopMode == "" {
					failures++
					return errors.New("injected stopped-state save failure")
				}
				return nil
			})
			_, err := f.ls.RunScheduled(context.Background(), &job)
			restore()
			require.NoError(t, err, "the turn itself completed")
			require.Equal(t, int32(1), f.p.calls.Load())
			if !fail {
				assert.Empty(t, f.meta(t).LoopMode)
				assert.Equal(t, []string{"stopped"}, t10LoopStates(sub.C))
				assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
				return
			}
			require.Equal(t, 1, failures, "instrument: exactly the stopped-state save failed")
			m := f.meta(t)
			assert.Equal(t, loopModeSelfPaced, m.LoopMode, "the saved loop is unchanged")
			assert.Equal(t, 1, m.LoopRunCount, "the completed run is counted")
			assert.Empty(t, t10LoopStates(sub.C), "no stopped (or other) frame for an unsaved stop")
			// CHECK5 F2: the fired one-shot is not deleted by the handler (the
			// engine deletes it after the handler returns), and a self-paced
			// loop gets exactly one retry step, which is the selected step
			// once the engine has deleted the fired one-shot.
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, jobs, 2, "the live job is not deleted on a failed stop save, and one retry step is armed")
			var retry cron.CronJob
			kept := false
			for _, j := range jobs {
				if j.ID == job.ID {
					kept = true
				} else {
					retry = j
				}
			}
			assert.True(t, kept, "the live job is not deleted on a failed stop save")
			require.True(t, f.ls.RemoveJob(job.ID)) // the engine deletes a fired one-shot
			require.True(t, f.ls.isSelectedStep(f.sid, retry.ID), "the retry step is the selected step")

			// The next fire runs no turn past the limit; with a working store
			// it saves the stop and announces it once.
			_, err = f.ls.RunScheduled(context.Background(), &retry)
			require.NoError(t, err)
			assert.Equal(t, int32(1), f.p.calls.Load(), "no turn runs past the saved run limit")
			assert.Empty(t, f.meta(t).LoopMode)
			assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
			assert.Equal(t, []string{"stopped"}, t10LoopStates(sub.C))
		})
	}
}

// F3: a user Stop issued while a settled turn is publishing its status must
// be the last loop status published.
func TestT10_LoopStopIsTheLastStatusAfterOverlappingSettlement(t *testing.T) {
	for _, overlap := range []bool{false, true} {
		name := "serial"
		if overlap {
			name = "stop-during-settlement-publication"
		}
		t.Run(name, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			f.command(t, "/loop original")
			job, ok := f.ls.cs.GetJob(f.meta(t).LoopJobID)
			require.True(t, ok)
			sub := f.al.SubscribeEvents(128)
			defer f.al.UnsubscribeEvents(sub.ID)
			entered, release := make(chan struct{}), make(chan struct{})
			var holdOnce, releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			f.al.SetEventSyncTap(func(e Event) {
				if p, ok := e.Payload.(LoopStatusChangedPayload); overlap && ok && p.State == "scheduled_next" {
					holdOnce.Do(func() { close(entered); <-release })
				}
			})
			defer f.al.SetEventSyncTap(nil)
			turnDone := make(chan error, 1)
			go func() { _, err := f.ls.RunScheduled(context.Background(), &job); turnDone <- err }()
			if !overlap {
				require.NoError(t, <-turnDone)
				f.command(t, "/loop stop")
			} else {
				t10AwaitEdge(t, entered, "the settled turn's scheduled_next publication")
				stopDone := make(chan string, 1)
				go func() {
					_, _, reply := f.al.applyLoopCommandPrompt(context.Background(), bus.InboundMessage{Content: "/loop stop"}, f.inst,
						&processOptions{UserInitiated: true, TranscriptStore: f.store, TranscriptSessionID: f.sid})
					stopDone <- reply
				}()
				reply, early := "", false
				select {
				case reply = <-stopDone:
					early = true
				case <-time.After(2 * time.Second):
				}
				assert.False(t, early, "the stop completed while the settled turn was still publishing under the session lock")
				unblock()
				require.NoError(t, <-turnDone)
				if !early {
					select {
					case reply = <-stopDone:
					case <-time.After(5 * time.Second):
						t.Fatal("the stop did not finish after the settlement released the session")
					}
				}
				assert.Equal(t, "Loop stopped.", reply)
			}
			states := t10LoopStates(sub.C)
			require.NotEmpty(t, states)
			assert.Equal(t, "stopped", states[len(states)-1], "the later user stop is the final loop status")
			assert.Equal(t, []string{"scheduled_next", "stopped"}, states)
			assert.Empty(t, f.meta(t).LoopMode)
			assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
		})
	}
}
