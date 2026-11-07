package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1621: a caller must be able to consume every returned field after
// ListJobs releases its lock, while the real execution path writes run state.
// Run with -race; no service lock or run-state write is replaced by a test fake.
func TestCronService_ListJobsConcurrentWithExecution(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	cs, _ := newAutonomyService(t, clk)
	runner := &recordingRunner{result: "listjobs-race-session"}
	cs.SetRunner(runner)

	const intervalMS = int64(1000) // A one-second recurring fixture; no real timer runs.
	job, err := cs.AddJobFull(JobSpec{
		Kind: KindHeartbeat,
		Name: "ListJobs concurrent reader",
		Schedule: CronSchedule{
			Kind: "every", EveryMS: int64Ptr(intervalMS),
		},
		Message: "read every returned job field",
		Command: "fixture-command",
		AgentID: "mia",
	})
	require.NoError(t, err)
	// Both state pointees must be present even in the reader's first snapshot.
	job.State.LastRunAtMS = int64Ptr(clk.Now().Add(-time.Second).UnixMilli())
	require.NoError(t, cs.UpdateJob(job))
	jobID := job.ID
	wantPayload := CronPayload{
		Kind: KindHeartbeat, Message: "read every returned job field", Command: "fixture-command",
	}

	type readResult struct {
		snapshots, nextRunReads, lastRunReads int
		runningReads                          int
		timestampSum                          int64
		err                                   error
	}
	readerReady := make(chan struct{})
	executionDone := make(chan struct{})
	readerDone := make(chan readResult, 1)

	go func() {
		var result readResult
		defer func() { readerDone <- result }()
		jobs := cs.ListJobs(true)
		close(readerReady) // Writer cannot fire before a caller has its snapshot.
		for {
			if len(jobs) != 1 {
				result.err = fmt.Errorf("ListJobs(true) returned %d jobs, want 1", len(jobs))
				return
			}
			// Re-read caller-held elements between lock acquisitions. A shallow
			// backing-slice return must not be hidden by the next ListJobs lock.
			for range 128 {
				for i := range jobs {
					returned := jobs[i]
					if returned.ID != jobID || returned.Payload != wantPayload {
						result.err = fmt.Errorf("snapshot identity/payload changed: id=%q payload=%+v", returned.ID, returned.Payload)
						return
					}
					if returned.State.Running {
						result.runningReads++
					}
					if next := returned.State.NextRunAtMS; next != nil {
						result.timestampSum += *next
						result.nextRunReads++
					}
					if last := returned.State.LastRunAtMS; last != nil {
						result.timestampSum += *last
						result.lastRunReads++
					}
					// Marshal recursively consumes all other fields too: schedule
					// pointees, payload, history entries and ownership/session data.
					if _, err := json.Marshal(returned); err != nil {
						result.err = fmt.Errorf("consume complete ListJobs snapshot: %w", err)
						return
					}
					result.snapshots++
				}
			}
			select {
			case <-executionDone:
				return
			default:
				runtime.Gosched() // Yield without a time-based overlap assumption.
				jobs = cs.ListJobs(true)
			}
		}
	}()

	// Repetition widens overlap; the channel, not a sleep, establishes that a
	// reader already holds returned jobs before the first real run-state write.
	const fires = 64
	go func() {
		<-readerReady
		defer close(executionDone)
		for range fires {
			clk.Advance(time.Second)
			cs.executeJobByID(context.Background(), jobID)
		}
	}()

	<-executionDone
	reads := <-readerDone // Join the reader before inspecting results/cleaning up.
	require.NoError(t, reads.err)
	assert.Positive(t, reads.snapshots, "instrument: reader consumed returned jobs")
	assert.Positive(t, reads.nextRunReads, "instrument: reader dereferenced NextRunAtMS")
	assert.Positive(t, reads.lastRunReads, "instrument: reader dereferenced LastRunAtMS")
	t.Logf("consumed %d complete snapshots; next/last pointee reads=%d/%d; running reads=%d; timestamp sum=%d",
		reads.snapshots, reads.nextRunReads, reads.lastRunReads, reads.runningReads, reads.timestampSum)

	wantCalls := make([]string, fires)
	for i := range wantCalls {
		wantCalls[i] = jobID
	}
	assert.Equal(t, wantCalls, runner.calls(), "instrument: all real executions reached the runner")
	stored, found := cs.GetJob(jobID)
	require.True(t, found, "recurring fixture must remain after its executions")
	assert.False(t, stored.State.Running, "instrument: the execution path settled its running flag")
	assert.Equal(t, "ok", stored.State.LastStatus, "instrument: the execution path recorded its result")
	require.NotNil(t, stored.State.LastRunAtMS)
	assert.Equal(t, clk.Now().UnixMilli(), *stored.State.LastRunAtMS, "instrument: final fire wrote run state")
}
