// task_trigger_boot_hold.go: hold the owed fire of a crashed one-time trigger
// until the task executor's session plane is wired (6b U2b boot replay).
//
// The gateway starts the task-trigger scheduler (startSchedulers) BEFORE it
// wires the executor's session plane: the lifecycle store (startPlanEngine),
// the SteerLauncher (setupPlans) and the main resolver (buildRESTAPI). The
// engine fires a re-armed owed fire at once, so without this hold the replay
// was dispatched in that window: the run mode fell back to ISOLATED (no
// launcher, no resolver) and the run's runtime classification was never
// minted (no lifecycle store). Once the plane was wired, the turn's runtime
// invariant refused the replay ("session has no persisted runtime
// classification"), so the owed run never executed.
//
// The queued-task drain has the same window (it starts in startSchedulers and
// ticks one second later): once the gateway has marked the plane expected
// (ExpectSessionPlane), CheckQueuedTasks dispatches nothing until the plane is
// fully wired, and the first tick after that dispatches the queued tasks.
//
// The hold applies only to a boot-owed fire (bootOwedFire) and only while the
// plane is missing its launcher or lifecycle store, or is expected and not
// yet fully wired; every other fire, and a
// boot-owed fire on an already wired executor, runs at once as before. A held
// fire is released when all three parts are wired, so its mode is derived and
// its classification minted exactly as in the steady state. While held, the
// engine keeps the job with its Running flag (cron.ErrRunHandedOff), and the
// owed-fire marker keeps the boot Reconcile from replacing it: a process that
// stops before the plane is wired leaves the debt for the next physical boot.

package agent

import (
	"context"
	"errors"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/cron"
)

// holdBootOwedFire reports whether job is the owed fire of a crashed one-time
// trigger that must wait for the session plane. When it does, the fire is
// queued on the executor and RunScheduled hands the job off to it.
func (s *TaskTriggerScheduler) holdBootOwedFire(ctx context.Context, job *cron.CronJob) bool {
	if s.executor == nil {
		return false
	}
	taskID := job.Payload.Message
	s.mu.Lock()
	owed := taskID != "" && s.bootOwedFire[taskID]
	s.mu.Unlock()
	if !owed {
		return false
	}
	held := *job
	if !s.executor.whenSessionPlaneWired(func() { s.runHeldBootOwedFire(ctx, &held) }) {
		return false
	}
	slog.Warn("task_trigger: owed one-time fire held until the session plane is wired",
		"task_id", taskID, "job_id", job.ID)
	return true
}

// runHeldBootOwedFire runs a held owed fire once the plane is wired and
// settles its job the way the engine settles a one-time job: a fire handed
// off to a run is settled by that run; any other outcome removes the job.
func (s *TaskTriggerScheduler) runHeldBootOwedFire(ctx context.Context, job *cron.CronJob) {
	taskID := job.Payload.Message
	s.mu.Lock()
	stopping := s.stopping
	owed := s.bootOwedFire[taskID]
	s.mu.Unlock()
	if stopping || ctx.Err() != nil {
		// The job keeps its Running flag: the next physical boot replays it.
		slog.Warn("task_trigger: held owed one-time fire not run: the scheduler stopped first; kept for the next boot",
			"task_id", taskID, "job_id", job.ID)
		return
	}
	if !owed {
		// The trigger was edited or the task deleted while the fire was held:
		// the edit wins, as at markBootOwedFires.
		slog.Info("task_trigger: held owed one-time fire superseded before it ran",
			"task_id", taskID, "job_id", job.ID)
		s.cs.RemoveJob(job.ID)
		return
	}
	_, err := s.runScheduledFire(ctx, job)
	if errors.Is(err, cron.ErrRunHandedOff) {
		return
	}
	if err != nil {
		slog.Error("task_trigger: held owed one-time fire failed",
			"task_id", taskID, "job_id", job.ID, "error", err)
	}
	s.cs.RemoveJob(job.ID)
}

// whenSessionPlaneWired queues fn until the session plane is wired and
// reports true, or reports false (fn not queued) when the launcher and the
// lifecycle store are already wired and no full plane is still expected
// (ExpectSessionPlane).
func (te *TaskExecutor) whenSessionPlaneWired(fn func()) bool {
	te.mu.Lock()
	defer te.mu.Unlock()
	if te.launcher != nil && te.lifecycleStore != nil && !te.awaitingSessionPlaneLocked() {
		return false
	}
	te.planeWaiters = append(te.planeWaiters, fn)
	return true
}

// ExpectSessionPlane marks that this executor's session plane (launcher,
// lifecycle store, main resolver) is about to be wired: the gateway calls it
// at boot, before startSchedulers starts the queued-task drain and the
// trigger scheduler, because it wires the plane only afterwards (setupPlans,
// startPlanEngine, buildRESTAPI). Until all three parts are wired, the drain
// dispatches nothing (CheckQueuedTasks) and owed fires are held, so no run
// starts in the wrong mode or without its runtime classification. An
// executor never marked keeps its old behaviour: it dispatches with whatever
// is wired.
func (te *TaskExecutor) ExpectSessionPlane() {
	if te == nil {
		return
	}
	te.mu.Lock()
	te.planeExpected = true
	te.mu.Unlock()
}

// awaitingSessionPlane reports whether the session plane is expected but not
// yet fully wired.
func (te *TaskExecutor) awaitingSessionPlane() bool {
	te.mu.Lock()
	defer te.mu.Unlock()
	return te.awaitingSessionPlaneLocked()
}

// awaitingSessionPlaneLocked is awaitingSessionPlane for a caller holding mu.
func (te *TaskExecutor) awaitingSessionPlaneLocked() bool {
	return te.planeExpected && !te.sessionPlaneWiredLocked()
}

// sessionPlaneWiredLocked reports whether the launcher, the lifecycle store
// and the main resolver are all wired. Caller holds mu.
func (te *TaskExecutor) sessionPlaneWiredLocked() bool {
	return te.launcher != nil && te.lifecycleStore != nil && te.mainResolver != nil
}

// releaseSessionPlaneWaiters runs every queued waiter, in order and in the
// caller's goroutine, once the launcher, the lifecycle store and the main
// resolver are all wired. The gateway wires the resolver last
// (buildRESTAPI), so a held fire derives its run mode from the real resolver.
func (te *TaskExecutor) releaseSessionPlaneWaiters() {
	te.mu.Lock()
	if !te.sessionPlaneWiredLocked() || len(te.planeWaiters) == 0 {
		te.mu.Unlock()
		return
	}
	waiters := te.planeWaiters
	te.planeWaiters = nil
	te.mu.Unlock()
	for _, fn := range waiters {
		fn()
	}
}
