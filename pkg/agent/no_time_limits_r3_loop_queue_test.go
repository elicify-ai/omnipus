package agent

// PR #1311 G7 / architect R2-A1: a due /loop fire waiting on the REAL cron
// lane is work, even though RunDueJobs cleared next-fire and no turn or
// lifecycle record exists yet. The first real loop run holds the only lane
// slot at its provider edge. Sweep time is injected; no sleeps manufacture
// the queue. CHECK: ignore pending cron dispatches; count only live turns;
// keep already-finished loop work protected forever.

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func r3AddQueuedLoopSession(t *testing.T, al *AgentLoop, scheduler *LoopScheduler, at time.Time) (string, string) {
	t.Helper()
	store, sid := newGoalTestSession(t, al, "native-agent")
	const interval = time.Hour
	jobID, err := scheduler.AddInterval("native-agent", sid, interval.Milliseconds())
	if err != nil {
		t.Fatalf("SETUP AddInterval: %v", err)
	}
	mode, prompt, maxRuns := loopModeInterval, "Produce the recurring report", config.DefaultLoopMaxRuns
	intervalMS, activity := interval.Milliseconds(), at.UTC().Format(time.RFC3339)
	if err := store.SetMeta(sid, session.MetaPatch{LoopMode: &mode, LoopPrompt: &prompt,
		LoopMaxRuns: &maxRuns, LoopIntervalMS: &intervalMS, LoopJobID: &jobID,
		LoopStartedAt: &activity, LoopLastActivityAt: &activity}); err != nil {
		t.Fatalf("SETUP persist real loop metadata: %v", err)
	}
	return sid, jobID
}

func r3FindLoopJob(s *LoopScheduler, id string) (cron.CronJob, bool) {
	for _, job := range s.ListEnabledJobs() {
		if job.ID == id {
			return job, true
		}
	}
	return cron.CronJob{}, false
}

func TestR3LoopIdleExpiry_PendingCronLaneDispatchIsWorkUntilItFinishes(t *testing.T) {
	provider := newGatedFinalProvider()
	al, _ := newGoalLoopTestLoop(t, provider, nil)
	installTestSessionLauncher(t, al)
	clock := newTriggerFakeClock() // future due times keep the real cron timer from racing manual dispatch
	scheduler := NewLoopScheduler(filepath.Join(t.TempDir(), "loops.json"), al)
	scheduler.SetClock(clock)
	scheduler.cs.SetMaxConcurrentRuns(1) // REAL lane, exactly one slot
	if err := scheduler.Start(); err != nil {
		t.Fatalf("SETUP scheduler.Start: %v", err)
	}
	al.SetLoopScheduler(scheduler)
	release := sync.OnceFunc(func() { close(provider.release) })
	t.Cleanup(func() { release(); scheduler.WaitForLane(); scheduler.Stop() })
	base := clock.Now()

	// Run an actual /loop turn, not a fake reservation in the cron semaphore.
	_, holderJob := r3AddQueuedLoopSession(t, al, scheduler, base)
	scheduler.RunDueJobs(base.Add(time.Hour))
	waitClosed(t, provider.entered, "the cron slot-holder's real provider call")
	if job, ok := r3FindLoopJob(scheduler, holderJob); !ok || !job.State.Running {
		t.Fatalf("SETUP slot holder is not executing a real cron job: job=%+v present=%v", job, ok)
	}

	pendingSID, pendingJob := r3AddQueuedLoopSession(t, al, scheduler, base)
	scheduler.RunDueJobs(base.Add(time.Hour))
	pending, ok := r3FindLoopJob(scheduler, pendingJob)
	if !ok || pending.State.NextRunAtMS != nil || pending.State.Running {
		t.Fatalf("SETUP due dispatch must have cleared next-fire and be waiting for the lane: job=%+v present=%v", pending, ok)
	}
	if _, live := al.activeTurnStates.Load(pendingSID); live {
		t.Fatal("SETUP pending cron dispatch must have no registered turn")
	}
	if _, err := al.GetSessionLifecycleStore().Load(pendingSID); !errors.Is(err, session.ErrLifecycleNotFound) {
		t.Fatalf("SETUP pending cron dispatch must have no lifecycle record: %v", err)
	}

	// Exactly beyond the injected one-day idle bound, with the dispatch still
	// waiting on the occupied lane. A nil next-fire is NOT absence of work.
	scheduler.IdleExpirySweep(config.PlanningConfig{IdleExpiryDays: 1}, base.Add(24*time.Hour+time.Nanosecond))
	if _, ok := r3FindLoopJob(scheduler, pendingJob); !ok {
		t.Error("G7 pending /loop dispatch was removed by idle expiry while waiting for the real cron lane slot")
	}
	meta, err := al.GetSessionStore().GetMeta(pendingSID)
	if err != nil || meta.LoopMode != loopModeInterval || meta.LoopJobID != pendingJob {
		t.Errorf("G7 queued loop was stopped before its turn started: meta=%+v err=%v", meta, err)
	}

	release()
	scheduler.WaitForLane() // includes the pending dispatch AND all completion writes
	after, err := al.GetSessionStore().GetMeta(pendingSID)
	if err != nil || after.LoopRunCount != 1 {
		t.Errorf("G7 due queued run was lost instead of executed after the lane freed: meta=%+v err=%v, want run_count=1", after, err)
	}
	if _, live := al.activeTurnStates.Load(pendingSID); live {
		t.Fatal("CONTROL pending loop turn did not actually finish")
	}
	// No work remains; the retained idle brake must still operate. Two injected
	// idle bounds past the future fixture clock is also beyond the completed
	// run's real activity timestamp; never sleep to reach the idle window.
	scheduler.IdleExpirySweep(config.PlanningConfig{IdleExpiryDays: 1}, base.Add(2*24*time.Hour+time.Nanosecond))
	if _, ok := r3FindLoopJob(scheduler, pendingJob); ok {
		t.Error("G7 idle control: finished /loop job did not expire once its run and queue were gone")
	}
	final, err := al.GetSessionStore().GetMeta(pendingSID)
	if err != nil || final.LoopMode != "" {
		t.Errorf("G7 idle control: loop mode=%+v err=%v, want disabled", final, err)
	}
}
