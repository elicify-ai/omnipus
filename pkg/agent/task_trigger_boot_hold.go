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
// Every other scheduled fire that comes due in that window is held too, once
// the gateway has marked the plane expected; on an executor nobody marked, an
// ordinary fire runs at once as before, and a boot-owed fire waits only for
// the launcher and the lifecycle store. A held fire is released when all
// three parts are wired, so its mode is derived and its classification
// minted exactly as in the steady state. While held, the engine keeps the job
// with its Running flag (cron.ErrRunHandedOff), and the owed-fire marker keeps
// the boot Reconcile from replacing it: a process that stops before the plane
// is wired leaves the debt for the next physical boot. A held fire that
// cannot start is never dropped silently (runHeldFire).

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// holdFire reports whether job's fire must wait for the session plane. When
// it does, the fire is queued on the executor and RunScheduled hands the job
// off to it (cron.ErrRunHandedOff: the engine keeps the job with its Running
// flag). Two kinds of fire wait:
//
//   - the owed fire of a crashed one-time trigger (bootOwedFire), while the
//     executor lacks its launcher or lifecycle store, or while the plane is
//     expected and not yet fully wired;
//   - any other scheduled fire, while the plane is expected (the gateway
//     marked it, ExpectSessionPlane) and not yet fully wired.
//
// A held fire carries the bootOwedFire marker, so the boot reconcile and an
// edit that leaves the schedule unchanged keep its job, while a schedule edit
// or a deletion clears it and the held fire then never runs. Every task
// trigger is an "at" job, the only kind the engine lets a runner hand off.
func (s *TaskTriggerScheduler) holdFire(ctx context.Context, job *cron.CronJob) bool {
	taskID := job.Payload.Message
	if s.executor == nil || taskID == "" || job.Schedule.Kind != "at" {
		return false
	}
	s.mu.Lock()
	current := s.tracksJobLocked(taskID, job.ID)
	owed := current && s.bootOwedFire[taskID]
	marked := false
	if current && !owed {
		// Marked before it is queued, so a release that runs at once still
		// finds it. Unheld, the mark is taken back below.
		s.bootOwedFire[taskID] = true
		marked = true
	}
	s.mu.Unlock()
	held := *job
	if !s.executor.whenSessionPlaneWired(owed, func() { s.runHeldFire(ctx, &held) }) {
		if marked {
			s.mu.Lock()
			delete(s.bootOwedFire, taskID)
			s.mu.Unlock()
		}
		return false
	}
	slog.Warn("task_trigger: scheduled fire held until the session plane is wired",
		"task_id", taskID, "job_id", job.ID, "owed_replay", owed)
	return true
}

// runHeldFire runs a held fire once the plane is wired and settles its job.
// A failure never removes the job silently:
//
//   - the scheduler stopped first: the job is kept, the next physical boot
//     replays it;
//   - the task is gone (before the release or during its dispatch): one audit
//     warning names it and the job is removed;
//   - the task cannot be read right now: an audit warning names it and the
//     job is kept, with its Running flag and marker, for the next boot;
//   - the schedule was edited meanwhile (this job is no longer the task's
//     tracked job): the edit wins, the job is removed;
//   - the task's agent is gone: a failed run is recorded in the task's run
//     history and the task is saved as Failed, then the job is removed; if
//     either write fails, the job is kept;
//   - the fire cannot start for another reason: an audit warning names it and
//     the job is kept for the next boot.
//
// A fire handed off to a run is settled by that run; a fire that ended
// without a run (trigger cleared, overlap skip) is removed as the engine
// removes a one-time job.
func (s *TaskTriggerScheduler) runHeldFire(ctx context.Context, job *cron.CronJob) {
	taskID := job.Payload.Message
	s.mu.Lock()
	stopping := s.stopping
	s.mu.Unlock()
	if stopping || ctx.Err() != nil {
		slog.Warn("task_trigger: held scheduled fire not run: the scheduler stopped first; kept for the next boot",
			"task_id", taskID, "job_id", job.ID)
		return
	}
	t, err := s.store.Get(taskID)
	if errors.Is(err, task.ErrNotFound) {
		s.dropDeletedHeldFire(ctx, taskID, job.ID, err)
		return
	}
	if err != nil {
		s.keepHeldFire(ctx, taskID, job.ID, heldFireTaskUnreadable, err)
		return
	}
	// The held fire runs only while it is still this task's tracked job and
	// still marked: a schedule edit replaces the job (and clears the mark), so
	// a replacement that came due and was marked meanwhile is never run by
	// the replaced fire's release.
	s.mu.Lock()
	current := s.tracksJobLocked(taskID, job.ID) && s.bootOwedFire[taskID]
	s.mu.Unlock()
	if !current {
		if _, gerr := s.store.Get(taskID); errors.Is(gerr, task.ErrNotFound) {
			s.dropDeletedHeldFire(ctx, taskID, job.ID, gerr)
			return
		}
		slog.Info("task_trigger: held scheduled fire superseded by a schedule edit before it ran",
			"task_id", taskID, "job_id", job.ID)
		s.cs.RemoveJob(job.ID)
		return
	}
	if t.AgentID != "" && !s.executor.agentRegistered(t.AgentID) {
		s.settleMissingAgentFire(ctx, t, job.ID)
		return
	}
	if hook := heldFireBeforeRunHookForTest.Load(); hook != nil {
		(*hook)(taskID)
	}
	_, err = s.runScheduledFire(ctx, job)
	if errors.Is(err, cron.ErrRunHandedOff) {
		return
	}
	if errors.Is(err, errScheduledFireTaskGone) {
		// Deleted after the read above; runScheduledFire already removed the
		// job and dropped the tracking.
		slog.Warn("task_trigger: held scheduled fire not started: the task was deleted",
			"task_id", taskID, "job_id", job.ID)
		s.executor.auditHeldFireNotStarted(ctx, taskID, job.ID, heldFireTaskDeleted)
		return
	}
	if err != nil {
		s.keepHeldFire(ctx, taskID, job.ID, heldFireDispatchFailed, err)
		return
	}
	s.cs.RemoveJob(job.ID)
}

// heldFireBeforeRunHookForTest, when set, runs in runHeldFire after the
// release has read the task and checked its job, marker and agent, right
// before runScheduledFire reads the task again. Nil in production; set only
// by tests through SetHeldFireBeforeRunHookForTest.
var heldFireBeforeRunHookForTest atomic.Pointer[func(taskID string)]

// SetHeldFireBeforeRunHookForTest installs fn as the hook run right before a
// released held fire calls runScheduledFire (nil removes it). For tests only.
func SetHeldFireBeforeRunHookForTest(fn func(taskID string)) {
	if fn == nil {
		heldFireBeforeRunHookForTest.Store(nil)
		return
	}
	heldFireBeforeRunHookForTest.Store(&fn)
}

// heldFireCloseRunHookForTest, when set, runs in recordUnstartedFireFailure
// after its OpenRun succeeded and before store.CloseRun; a non-nil result is
// used as the CloseRun error in place of calling CloseRun. Nil in
// production; set only by tests through SetHeldFireCloseRunHookForTest.
var heldFireCloseRunHookForTest atomic.Pointer[func() error]

// SetHeldFireCloseRunHookForTest installs fn as the hook run right before the
// failed-run close of an unstarted fire (nil removes it). For tests only.
func SetHeldFireCloseRunHookForTest(fn func() error) {
	if fn == nil {
		heldFireCloseRunHookForTest.Store(nil)
		return
	}
	heldFireCloseRunHookForTest.Store(&fn)
}

// closeUnstartedFireRun closes the failed run of an unstarted fire, unless
// the test hook (heldFireCloseRunHookForTest) supplies the close error.
func (te *TaskExecutor) closeUnstartedFireRun(taskID, runID, reason string) error {
	if hook := heldFireCloseRunHookForTest.Load(); hook != nil {
		if err := (*hook)(); err != nil {
			return err
		}
	}
	return te.store.CloseRun(taskID, runID, task.StatusFailed, reason)
}

// keepHeldFire keeps a held fire that could not start: the job stays with
// its Running flag (the next physical boot's replay evidence), its marker is
// restored so a reconcile cannot take it away, and an audit warning names the
// task.
func (s *TaskTriggerScheduler) keepHeldFire(ctx context.Context, taskID, jobID, reason string, err error) {
	s.mu.Lock()
	if tracked, ok := s.taskToJob[taskID]; ok && tracked.jobID == jobID {
		s.bootOwedFire[taskID] = true
	}
	s.mu.Unlock()
	slog.Error("task_trigger: held scheduled fire could not start; kept for the next boot",
		"task_id", taskID, "job_id", jobID, "reason", reason, "error", err)
	s.executor.auditHeldFireNotStarted(ctx, taskID, jobID, reason)
}

// settleMissingAgentFire settles a fire whose task's agent is gone: a failed
// run is recorded and the task saved as Failed, then the job is removed. If
// either write fails, the job is kept (keepHeldFire) and the next physical
// boot settles it.
func (s *TaskTriggerScheduler) settleMissingAgentFire(ctx context.Context, t *task.Task, jobID string) {
	s.mu.Lock()
	delete(s.bootOwedFire, t.ID)
	s.mu.Unlock()
	reason := fmt.Sprintf("agent %q not found", t.AgentID)
	if err := s.executor.recordUnstartedFireFailure(t, reason); err != nil {
		s.keepHeldFire(ctx, t.ID, jobID, heldFireAgentMissing, err)
		return
	}
	slog.Warn("task_trigger: scheduled fire not started: its agent is gone; failed run recorded",
		"task_id", t.ID, "job_id", jobID, "agent_id", t.AgentID)
	s.executor.auditHeldFireNotStarted(ctx, t.ID, jobID, heldFireAgentMissing)
	s.cs.RemoveJob(jobID)
}

// settleOwedFireOfMissingAgent settles the owed fire of a crashed one-time
// trigger that runs unheld (the plane is already wired) when its task's agent
// is gone, exactly as a released held fire would (settleMissingAgentFire):
// the debt a failed save kept at the previous boot is settled with a failed
// run, not left to the dispatch path, which fails the task without one. It
// reports true when it settled the fire; every other fire, and a task it
// cannot read, goes on to runScheduledFire unchanged.
func (s *TaskTriggerScheduler) settleOwedFireOfMissingAgent(ctx context.Context, job *cron.CronJob) bool {
	taskID := job.Payload.Message
	if s.executor == nil || taskID == "" {
		return false
	}
	s.mu.Lock()
	owed := s.tracksJobLocked(taskID, job.ID) && s.bootOwedFire[taskID]
	s.mu.Unlock()
	if !owed {
		return false
	}
	t, err := s.store.Get(taskID)
	if err != nil || t.AgentID == "" || s.executor.agentRegistered(t.AgentID) {
		return false
	}
	s.settleMissingAgentFire(ctx, t, job.ID)
	return true
}

// dropDeletedHeldFire settles a held fire whose task is gone: one audit
// warning, then the job and its tracking are removed.
func (s *TaskTriggerScheduler) dropDeletedHeldFire(ctx context.Context, taskID, jobID string, err error) {
	slog.Warn("task_trigger: held scheduled fire not started: the task was deleted",
		"task_id", taskID, "job_id", jobID, "error", err)
	s.executor.auditHeldFireNotStarted(ctx, taskID, jobID, heldFireTaskDeleted)
	s.OnTaskDeleted(taskID)
	s.cs.RemoveJob(jobID)
}

// tracksJobLocked reports whether jobID is the job currently tracked for
// taskID. Caller holds s.mu.
func (s *TaskTriggerScheduler) tracksJobLocked(taskID, jobID string) bool {
	tracked, ok := s.taskToJob[taskID]
	return ok && tracked.jobID == jobID
}

// The reasons a held fire did not start, as written to the audit log.
const (
	heldFireTaskDeleted    = "task_deleted"
	heldFireTaskUnreadable = "task_unreadable"
	heldFireAgentMissing   = "agent_missing"
	heldFireDispatchFailed = "dispatch_failed"
)

// auditHeldFireNotStarted writes the WARN audit entry for a held fire that
// did not start. The entry carries the ids and one fixed reason code only;
// the underlying error, which can name server paths, goes to the ordinary
// log at the call site, never to the audit log.
func (te *TaskExecutor) auditHeldFireNotStarted(ctx context.Context, taskID, jobID, reason string) {
	if te.agentLoop == nil {
		slog.Error("task_trigger: no agent loop, audit entry for a held fire not written",
			"task_id", taskID, "job_id", jobID, "reason", reason)
		return
	}
	fields := map[string]any{"task_id": taskID, "job_id": jobID, "reason": reason}
	audit.Emit(ctx, te.agentLoop.AuditLogger(), audit.EventTaskTriggerFireNotStarted, audit.SeverityWarn, fields)
}

// agentRegistered reports whether agentID is a registered agent. Without an
// agent loop there is no registry to ask, so the dispatch path decides.
func (te *TaskExecutor) agentRegistered(agentID string) bool {
	if te.agentLoop == nil {
		return true
	}
	_, ok := te.agentLoop.GetRegistry().GetAgent(agentID)
	return ok
}

// recordUnstartedFireFailure records a scheduled fire that could not start as
// a failed run in t's run history and fails the task with reason.
func (te *TaskExecutor) recordUnstartedFireFailure(t *task.Task, reason string) error {
	run, created, err := te.store.OpenRun(t.ID, nil, task.RunKindScheduled, "", nil)
	if err != nil {
		return fmt.Errorf("task_trigger: record failed run for task %q: %w", t.ID, err)
	}
	if !created {
		return fmt.Errorf("task_trigger: record failed run for task %q: run %q is still open", t.ID, run.RunID)
	}
	if err := te.closeUnstartedFireRun(t.ID, run.RunID, reason); err != nil {
		return fmt.Errorf("task_trigger: close failed run %q for task %q: %w", run.RunID, t.ID, err)
	}
	te.emitRunStatus(t.ID, run.RunID, nil, task.StatusFailed)
	return te.saveUnstartedFireFailed(t.ID, reason)
}

// saveUnstartedFireFailed marks the task Failed with reason, as failTask
// does, but returns the save error so the caller keeps the job until the
// Failed status is saved.
func (te *TaskExecutor) saveUnstartedFireFailed(taskID, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	failed := task.StatusFailed
	updated, err := te.store.Update(taskID, task.Patch{
		Status:      &failed,
		Result:      &reason,
		CompletedAt: &now,
	})
	if err != nil {
		return fmt.Errorf("task_trigger: mark task %q failed: %w", taskID, err)
	}
	terminateTaskGoalRecord(taskID, updated.Status, updated.CancelReason, reason)
	te.emitStatusChanged(updated, task.StatusFailed)
	return nil
}

// whenSessionPlaneWired queues fn until the session plane is wired and
// reports true, or reports false (fn not queued) when the fire need not wait:
// an owed fire waits while the launcher or the lifecycle store is missing or
// the expected plane is not fully wired; any other fire waits only while the
// expected plane (ExpectSessionPlane) is not fully wired.
func (te *TaskExecutor) whenSessionPlaneWired(owed bool, fn func()) bool {
	te.mu.Lock()
	defer te.mu.Unlock()
	wait := te.awaitingSessionPlaneLocked()
	if owed && (te.launcher == nil || te.lifecycleStore == nil) {
		wait = true
	}
	if !wait {
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
