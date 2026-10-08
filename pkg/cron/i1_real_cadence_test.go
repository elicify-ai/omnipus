package cron

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Item 4: start the REAL timer loop. The first paid-work boundary is held so
// runLoop has no next fire while the asynchronous lane is executing. Only a
// completion wake can make the second natural tick happen; no manual due call.
func TestI1RealCronLoopRunsTwoConsecutiveTicks(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"))
	interval := int64(30) // tiny engine interval, not the product heartbeat minimum
	job, err := cs.AddJobFull(JobSpec{Name: "healthy-heartbeat", AgentID: "mia",
		SessionMode: SessionModeContinue, SessionID: "same-heartbeat-session",
		Schedule: CronSchedule{Kind: "every", EveryMS: &interval}, Message: "tick"})
	require.NoError(t, err)
	entered := make(chan int32, 3)
	release := make(chan struct{})
	var calls atomic.Int32
	runner := &recordingRunner{ctxHook: func(ctx context.Context, got *CronJob) (string, error) {
		if got.ID != job.ID || got.SessionID != job.SessionID {
			return "", context.Canceled
		}
		n := calls.Add(1)
		entered <- n
		if n == 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		} else {
			cs.EnableJob(job.ID, false) // keep the exact observation at two ticks
		}
		return got.SessionID, nil
	}}
	cs.SetRunner(runner)
	require.NoError(t, cs.Start())
	t.Cleanup(func() { cs.Stop(); cs.WaitForLane() })
	select {
	case n := <-entered:
		require.Equal(t, int32(1), n)
	case <-time.After(3 * time.Second):
		t.Fatal("real cron timer never delivered its first tick")
	}
	// Force the reported ordering outside production: leave paid work blocked
	// through several engine intervals. This is race forcing, not an elapsed
	// speed assertion; the oracle is two actual entry signals and run records.
	time.Sleep(100 * time.Millisecond)
	close(release)
	select {
	case n := <-entered:
		require.Equal(t, int32(2), n)
	case <-time.After(3 * time.Second):
		t.Fatal("healthy recurring job never delivered the second tick after first completion")
	}
	cs.Stop()
	cs.WaitForLane()
	require.Equal(t, int32(2), calls.Load())
	stored, ok := cs.GetJob(job.ID)
	require.True(t, ok)
	require.Len(t, stored.State.History, 2)
	require.Equal(t, "same-heartbeat-session", stored.State.History[0].SessionID)
	require.Equal(t, "same-heartbeat-session", stored.State.History[1].SessionID)
	require.Equal(t, "ok", stored.State.History[0].Status)
	require.Equal(t, "ok", stored.State.History[1].Status)
}
