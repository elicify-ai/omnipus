//go:build goolm && stdjson

package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Traces to: schedules-t10-spec-r6.md lines 768–772, 1146; TDD 12, D-1.3.
// Real /loop command, real ProcessScheduled and engine history; the only fake
// is the provider's first failure and subsequent non-empty successful reply.
func TestT10_LoopFailureRecordsThenCarriesOnAtNextInterval(t *testing.T) {
	f := t10LoopSetup(t, false)
	f.command(t, "/loop every 15m report progress")
	id := f.meta(t).LoopJobID
	f.p.failure = errors.New("T10 loop provider unavailable")
	require.NoError(t, f.ls.Start())
	f.clk.Advance(15 * time.Minute)
	first := f.clk.Now().UnixMilli()
	f.ls.RunDueJobs(f.clk.Now())
	f.ls.WaitForLane()
	job, ok := f.ls.cs.GetJob(id)
	require.True(t, ok)
	require.Len(t, job.State.History, 1)
	assert.Equal(t, "error", job.State.LastStatus)
	assert.Contains(t, job.State.LastError, "T10 loop provider unavailable")
	assert.Zero(t, job.State.RetryAttempt, "loop errors carry on; never use heartbeat transient retry")
	require.NotNil(t, job.State.NextRunAtMS)
	assert.Equal(t, first+900000, *job.State.NextRunAtMS)
	assert.Equal(t, 1, f.meta(t).LoopRunCount)
	assert.Equal(t, "interval", f.meta(t).LoopMode)
	f.p.failure = nil
	f.clk.Advance(15 * time.Minute)
	f.ls.RunDueJobs(f.clk.Now())
	f.ls.WaitForLane()
	job, ok = f.ls.cs.GetJob(id)
	require.True(t, ok)
	require.Len(t, job.State.History, 2)
	assert.Equal(t, "ok", job.State.LastStatus)
	assert.Equal(t, []string{"error", "ok"}, []string{job.State.History[0].Status, job.State.History[1].Status})
	assert.Equal(t, 2, f.meta(t).LoopRunCount)
	assert.NotEmpty(t, job.State.History[1].SessionID)
}
