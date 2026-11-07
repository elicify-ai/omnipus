//go:build goolm && stdjson

package agent

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-05: a nil next instant ranks below every non-nil next instant, even
// when the nil job's original at timestamp and metadata pointer suggest otherwise.
func TestT10_LoopRecoveryRanksNilNextBelowFutureStep(t *testing.T) {
	f := t10LoopSetup(t, false)
	f.command(t, "/loop nil-next predecessor")
	old, ok := f.ls.cs.GetJob(f.meta(t).LoopJobID)
	require.True(t, ok)
	misleadingAt := f.clk.Now().Add(6 * time.Hour).UnixMilli()
	old.Schedule.AtMS = &misleadingAt
	old.State.NextRunAtMS = nil
	require.NoError(t, f.ls.cs.UpdateJob(&old))
	selected, err := f.ls.AddOneShot("native-agent", f.sid, int64(time.Hour/time.Millisecond))
	require.NoError(t, err)
	require.NoError(t, t10LoopRecovery(t, f.ls))
	jobs := t10SessionLoopJobs(f.ls, f.sid)
	require.Len(t, jobs, 1)
	assert.Equal(t, selected, jobs[0].ID)
	assert.Equal(t, selected, f.meta(t).LoopJobID)
	_, exists := f.ls.cs.GetJob(old.ID)
	assert.False(t, exists)
	require.NoError(t, f.ls.Start())
	f.clk.Advance(time.Hour)
	f.ls.RunDueJobs(f.clk.Now())
	f.ls.WaitForLane()
	assert.Equal(t, int32(1), f.p.calls.Load(), "selected future step must actually run")
}

// D-3b.16 explicitly requires the two modes IN THE SAME boot, not two
// independently plausible fixtures. Both use the same actual engine/store.
func TestT10_LoopRecoveryMissingModesShareOneBoot(t *testing.T) {
	f := t10LoopSetup(t, false)
	f.command(t, "/loop every 15m interval twin")
	_, otherSID := newGoalTestSession(t, f.al, "native-agent")
	twin := *f
	twin.sid = otherSID
	twin.command(t, "/loop self-paced twin")
	for _, sid := range []string{f.sid, twin.sid} {
		for _, j := range t10SessionLoopJobs(f.ls, sid) {
			require.True(t, f.ls.RemoveJob(j.ID))
		}
	}
	boot := f.clk.Now()
	require.NoError(t, t10LoopRecovery(t, f.ls))
	interval := t10SessionLoopJobs(f.ls, f.sid)
	paced := t10SessionLoopJobs(f.ls, twin.sid)
	require.Len(t, interval, 1)
	require.Len(t, paced, 1)
	assert.Equal(t, "every", interval[0].Schedule.Kind)
	require.NotNil(t, interval[0].Schedule.EveryMS)
	assert.Equal(t, int64(900000), *interval[0].Schedule.EveryMS)
	assert.Equal(t, "at", paced[0].Schedule.Kind)
	require.NotNil(t, paced[0].State.NextRunAtMS)
	assert.Equal(t, boot.Add(time.Second).UnixMilli(), *paced[0].State.NextRunAtMS)
	assert.Equal(t, interval[0].ID, f.meta(t).LoopJobID)
	assert.Equal(t, paced[0].ID, twin.meta(t).LoopJobID)
	// Stop the already-asserted self-paced twin so the exact interval count
	// below cannot be satisfied by its continuation chain.
	twin.command(t, "/loop stop")
	require.NoError(t, f.ls.Start())
	for i := 0; i < 2; i++ {
		f.clk.Advance(15 * time.Minute)
		f.ls.RunDueJobs(f.clk.Now())
		f.ls.WaitForLane()
	}
	assert.Equal(t, int32(2), f.p.calls.Load())
	assert.Equal(t, 2, f.meta(t).LoopRunCount)
}

// S-A.5/FR-25: symmetric counterpart to task and heartbeat reconciliation.
// Deceptive loop-like names/messages cannot confer loop ownership on other kinds.
func TestT10_LoopRecoveryPreservesForeignKindsByteForByte(t *testing.T) {
	f := t10LoopSetup(t, false)
	f.command(t, "/loop real owned step")
	var foreign []cron.CronJob
	for _, kind := range []string{"heartbeat", "task"} {
		at := f.clk.Now().Add(2 * time.Hour).UnixMilli()
		j, err := f.ls.cs.AddJobFull(cron.JobSpec{Kind: kind, Name: "loop:" + f.sid, AgentID: "native-agent", Message: f.sid, Schedule: cron.CronSchedule{Kind: "at", AtMS: &at}})
		require.NoError(t, err)
		j.Payload.Kind = kind
		j.State.LastError = "foreign-history-" + kind
		j.State.RetryAttempt = 2
		require.NoError(t, f.ls.cs.UpdateJob(j))
		foreign = append(foreign, *j)
	}
	require.NoError(t, t10LoopRecovery(t, f.ls))
	for _, want := range foreign {
		got, ok := f.ls.cs.GetJob(want.ID)
		require.True(t, ok)
		assert.Equal(t, want, got)
	}
	data, err := os.ReadFile(f.path)
	require.NoError(t, err)
	var disk cron.CronStore
	require.NoError(t, json.Unmarshal(data, &disk))
	for _, want := range foreign {
		var found []cron.CronJob
		for _, got := range disk.Jobs {
			if got.ID == want.ID {
				found = append(found, got)
			}
		}
		assert.Equal(t, []cron.CronJob{want}, found, "every foreign field survives on disk too")
	}
	assert.Len(t, t10SessionLoopJobs(f.ls, f.sid), 1, "real owned step remains; no-op pruning cannot remove everything")
}
