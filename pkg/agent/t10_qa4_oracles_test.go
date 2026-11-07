//go:build goolm && stdjson

package agent

// Round-4 oracle strengthening for the T10 CHECK4 survivors M7 and M8
// (/workspace/logs/team-a-restart-20261107/T10-CHECK4-REPORT.md). Real stores,
// engine, loop scheduler and event bus; only the stopped-state write is failed
// through the existing session-writer seam, so these tests also run as root.

import (
	"errors"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CHECK4 M8 (and M7 on the idle path): when the stopped state of an idle
// loop cannot be saved, the sweep must leave the loop exactly as it was: still
// active, its job still present, and no status frame. It must not lose the
// jobs first, and once the store works again the next sweep expires the loop
// for real (one "stopped" frame, no jobs left).
func TestT10_IdleExpirySaveFailureKeepsLoopAndJobs(t *testing.T) {
	f := t10LoopSetup(t, false)
	f.command(t, "/loop original")
	before := f.meta(t)
	jobsBefore := t10SessionLoopJobs(f.ls, f.sid)
	require.Len(t, jobsBefore, 1, "precondition: the active loop has its step")

	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopStartedAt: &old, LoopLastActivityAt: &old}))

	sub := f.al.SubscribeEvents(64)
	defer f.al.UnsubscribeEvents(sub.ID)
	failures := 0
	restore := t10InterceptLoopWrites(t, func(sid string, p session.MetaPatch) error {
		if sid == f.sid && p.LoopMode != nil && *p.LoopMode == "" {
			failures++
			return errors.New("injected stopped-state save failure")
		}
		return nil
	})
	f.ls.IdleExpirySweep(config.PlanningConfig{}, now)
	restore()

	require.Equal(t, 1, failures, "instrument: the sweep attempted exactly one stopped-state save, and it failed")
	assert.Empty(t, t10LoopStates(sub.C), "an unsaved expiry publishes no loop status frame")
	after := f.meta(t)
	assert.Equal(t, before.LoopMode, after.LoopMode, "the loop is still active")
	assert.Equal(t, before.LoopJobID, after.LoopJobID)
	jobs := t10SessionLoopJobs(f.ls, f.sid)
	require.Len(t, jobs, 1, "the loop's job must not be removed before the stop is saved")
	assert.Equal(t, before.LoopJobID, jobs[0].ID)

	// With a working store the next sweep completes the expiry.
	f.ls.IdleExpirySweep(config.PlanningConfig{}, now)
	assert.Empty(t, f.meta(t).LoopMode, "the retry expires the loop")
	assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
	assert.Equal(t, []string{"stopped"}, t10LoopStates(sub.C), "exactly one stopped frame, after the save succeeded")
}
