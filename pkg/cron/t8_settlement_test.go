package cron

// Oracle: r17 TK-30 / 18.34 / §13.3. This is the cron-lane accounting
// slice, NOT the plan/drain/collection-boundary scenario. CronService, its
// semaphore, reservation map, cancellation and panic handling remain real;
// the downstream scheduled runner and clock are controlled process edges.
// A retained second pending-publication call must fail the final assertions.
// Mutation and GREEN verification are deferred to the independent CHECK.
import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t8AssertOriginalIDsSettled(t *testing.T, cs *CronService, ids []string) {
	t.Helper()
	cs.WaitForLane()
	for _, id := range ids {
		assert.False(t, cs.JobDispatchInFlight(id), "original collected job %s remains in flight after settlement", id)
		cs.mu.RLock()
		count, present := cs.dispatchPending[id]
		cs.mu.RUnlock()
		assert.False(t, present, "original collected job %s retains a reservation entry (count=%d)", id, count)
	}
}

func TestT8CronDispatch_OriginalIDSettlement(t *testing.T) {
	for _, mode := range []string{"normal collected batch", "panic", "RunNow"} {
		t.Run(mode, func(t *testing.T) {
			clk := newFakeClock(time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC))
			cs, _ := newAutonomyService(t, clk)
			runner := &recordingRunner{result: "t8-real-runner-edge"}
			if mode == "panic" {
				runner.hook = func(*CronJob) (string, error) { panic("T8 scheduled runner panic fixture") }
			}
			cs.SetRunner(runner)
			require.NoError(t, cs.startNoLoop())
			t.Cleanup(cs.Stop)
			first := addDueJob(t, cs, "mia")
			ids := []string{first.ID}
			if mode == "normal collected batch" {
				second := addDueJob(t, cs, "mia")
				ids = append(ids, second.ID)
			}
			if mode == "RunNow" {
				status, sessionID, err := cs.RunNow(first.ID)
				require.NoError(t, err)
				assert.Equal(t, "ok", status)
				assert.Equal(t, "t8-real-runner-edge", sessionID)
			} else {
				clk.Advance(2 * time.Minute)
				cs.RunDueJobs(clk.Now())
			}
			t8AssertOriginalIDsSettled(t, cs, ids)
			assert.ElementsMatch(t, ids, runner.calls(), "every originally collected ID executes exactly once")
			if mode == "panic" {
				state := jobState(t, cs, first.ID)
				assert.Equal(t, "error", state.LastStatus)
				require.Len(t, state.History, 1)
				assert.Equal(t, "error", state.History[0].Status)
			}
		})
	}
	t.Run("cancellation while genuinely queued", func(t *testing.T) {
		clk := newFakeClock(time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC))
		cs, _ := newAutonomyService(t, clk)
		cs.SetAdmissionRule(func() (int, bool) { return 1, true })
		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		runner := &recordingRunner{ctxHook: func(ctx context.Context, _ *CronJob) (string, error) {
			close(entered)
			<-ctx.Done()
			<-release // keep capacity occupied until queued cancellation has settled
			return "", ctx.Err()
		}}
		cs.SetRunner(runner)
		require.NoError(t, cs.startNoLoop())
		t.Cleanup(cs.Stop)
		t.Cleanup(unblock)
		firstAt := clk.Now().Add(time.Minute).UnixMilli()
		secondAt := clk.Now().Add(2 * time.Minute).UnixMilli()
		holder, err := cs.AddJobFull(JobSpec{Kind: KindHeartbeat, Name: "lane holder", AgentID: "mia", Schedule: CronSchedule{Kind: "at", AtMS: &firstAt}})
		require.NoError(t, err)
		queued, err := cs.AddJobFull(JobSpec{Kind: KindHeartbeat, Name: "queued occurrence", AgentID: "mia", Schedule: CronSchedule{Kind: "at", AtMS: &secondAt}})
		require.NoError(t, err)
		clk.Advance(time.Minute)
		cs.RunDueJobs(clk.Now())
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("instrument: the real lane holder never entered its runner")
		}
		clk.Advance(time.Minute)
		cs.RunDueJobs(clk.Now())
		require.True(t, cs.JobDispatchInFlight(queued.ID), "instrument: the second occurrence really waits for the occupied lane")
		assert.Equal(t, []string{holder.ID}, runner.calls(), "the queued runner cannot execute before cancellation")
		cs.laneCancel()
		deadline := time.After(5 * time.Second)
		for cs.JobDispatchInFlight(queued.ID) {
			select {
			case <-deadline:
				t.Fatal("queued cancellation did not settle while the holder retained its slot")
			default:
				runtime.Gosched() // observe settlement; no time-based release or sleep
			}
		}
		unblock()
		cs.Stop()
		t8AssertOriginalIDsSettled(t, cs, []string{holder.ID, queued.ID})
		assert.Equal(t, []string{holder.ID}, runner.calls(), "cancellation settles the queued reservation without executing it")
	})
}
