// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// task_trigger_reconcile.go — the ordered, fresh-state registration rule of
// the task-trigger scheduler (tool-parity spec r17 §7.2 TK-30).
//
// Producer rule: every path that registers, replaces or removes a task's
// trigger job does so inside the task's coordination lock
// (task.LockTaskWriteOrder) and decides from the task state persisted at that
// moment, read under the lock — never from the snapshot a caller carried. A
// notification that arrives after a newer write is therefore a no-op against
// the persisted trigger. Lock order is always task lock, then the scheduler's
// own mutex (s.mu), then the cron engine's; the task lock is never taken
// while s.mu is held, and never for two tasks at once.
//
// Occurrence ownership (r14): the task's current registration owns the
// occurrence it carries from arming until delivery settles — the job's
// NextRunAtMS is still set (even overdue), or its dispatch is in flight. An
// unchanged trigger whose registration owns an occurrence keeps that
// registration. The one carve-out is a physical process start: restored
// state is recomputed once at boot, so an occurrence already overdue at boot
// is not owned — a past once is not re-armed (skipped, never replayed) and a
// recurring series moves to its next future boundary.
//
// Delivery marker (r11/r13): the fire path validates, under the task lock,
// that its job is still the task's current registration and that the
// delivered occurrence is the one the fresh persisted trigger describes, and
// records it (task.Task.LastDeliveredAtMs) in the same write that resets the
// task for the run. A revoked or superseded job records and dispatches
// nothing.

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// errNoFutureOccurrence is desiredSchedule's "nothing to arm" outcome: a once
// trigger whose instant is not in the future, or an exhausted series.
var errNoFutureOccurrence = errors.New("task_trigger: no future occurrence")

// ReconcileTaskLocked brings the live registration of taskID in line with
// the task's persisted trigger. The caller holds task.LockTaskWriteOrder for
// taskID (WriteTaskReconciled step 3). A failure to register leaves no job
// for the task (best-effort de-arm: no job is safer than a stale one) and is
// returned; the persisted trigger stays authoritative.
func (s *TaskTriggerScheduler) ReconcileTaskLocked(taskID string) error {
	return s.reconcileTaskLocked(taskID, false)
}

func (s *TaskTriggerScheduler) reconcileTaskLocked(taskID string, boot bool) error {
	t, err := s.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			s.OnTaskDeleted(taskID)
			return nil
		}
		return fmt.Errorf("task_trigger: read task %q for reconciliation: %w", taskID, err)
	}
	return s.reconcileFresh(t, boot)
}

// reconcileFresh applies the registration rule to t, which the caller read
// under t's coordination lock.
func (s *TaskTriggerScheduler) reconcileFresh(t *task.Task, boot bool) error {
	// Heartbeat-surface tasks belong to the per-agent heartbeat service;
	// registering here would double-fire them.
	if t.EffectiveSurface() == task.SurfaceHeartbeat {
		return nil
	}
	noTrigger := t.Trigger == nil || t.Trigger.Type == task.TriggerManual
	// The owed fire of a crashed one-time trigger (6b, bootOwedFire) is kept
	// whatever the task's status: the dead run's claim is reconciled to
	// failed/interrupted at boot, and that status must not take away the
	// rerun it is owed (6b review I2).
	s.mu.Lock()
	owed := !noTrigger && s.isBootOwedFireLocked(t)
	s.mu.Unlock()
	// A terminal recurring task keeps its series (a done/failed run is not the
	// end of the series); a terminal once task is over. There is no
	// consecutive-failure circuit breaker (operator decision, 2026-07-19).
	if noTrigger || (task.IsTerminal(t.Status) && !t.Trigger.IsRepeating() && !owed) {
		s.OnTaskDeleted(t.ID)
		return nil
	}
	nowMs := s.now().UnixMilli()
	generation := triggerGeneration(t.Trigger)

	// Keep rule, decided before the replacement schedule is computed: an
	// unchanged trigger whose registration still owns its occurrence keeps
	// it and re-arms nothing. The owed fire of a crashed one-time trigger is
	// owned by this boot even though its instant is past (6b: it runs once,
	// at this boot, and only then).
	s.mu.Lock()
	current, tracked := s.taskToJob[t.ID]
	keep := tracked && current.generation == generation &&
		(owed || s.keepsOccurrenceLocked(current.jobID, nowMs, boot))
	s.mu.Unlock()
	if keep {
		return nil
	}

	sched, err := desiredSchedule(t.Trigger, nowMs)
	if err != nil {
		s.OnTaskDeleted(t.ID)
		if errors.Is(err, errNoFutureOccurrence) {
			slog.Info("task_trigger: no future occurrence to arm", "task_id", t.ID, "trigger_type", t.Trigger.Type)
			return nil
		}
		return fmt.Errorf("task_trigger: task %q trigger cannot be scheduled: %w", t.ID, err)
	}

	s.mu.Lock()
	job, err := s.replaceJobLocked(t.ID, s.jobSpecFor(t, sched), generation)
	if err != nil {
		s.dearmAllLocked(t.ID)
	}
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("task_trigger: register job for task %q: %w", t.ID, err)
	}
	slog.Info("task_trigger: registered trigger job", "task_id", t.ID, "job_id", job.ID, "kind", sched.Kind)
	return nil
}

// desiredSchedule maps a trigger to the job it needs now: a once trigger only
// for a strictly future instant (a past once is never armed — skipped, never
// replayed), a recurring trigger at its next future occurrence.
func desiredSchedule(tr *task.Trigger, nowMs int64) (cron.CronSchedule, error) {
	if tr.Type == task.TriggerOnce && tr.Config.AtMs != nil && *tr.Config.AtMs <= nowMs {
		return cron.CronSchedule{}, errNoFutureOccurrence
	}
	sched, err := triggerToCronSchedule(tr, nowMs)
	if errors.Is(err, errRruleExhausted) {
		return cron.CronSchedule{}, errNoFutureOccurrence
	}
	return sched, err
}

// keepsOccurrenceLocked reports whether jobID owns an occurrence that an
// unchanged trigger must keep. In a running process that is ownership: an
// unconsumed armed next run (future or overdue) or a dispatch in flight. At
// boot only a strictly future armed occurrence is kept; an overdue restored
// one is not owned by this process. Caller holds s.mu.
func (s *TaskTriggerScheduler) keepsOccurrenceLocked(jobID string, nowMs int64, boot bool) bool {
	job, ok := s.cs.GetJob(jobID)
	if !ok {
		return false
	}
	if boot {
		return job.State.NextRunAtMS != nil && *job.State.NextRunAtMS > nowMs
	}
	return job.State.NextRunAtMS != nil || s.cs.JobDispatchInFlight(jobID)
}

// dearmAllLocked removes every job for taskID, tracked or not — including one
// the engine appended before its save failed. Caller holds s.mu.
func (s *TaskTriggerScheduler) dearmAllLocked(taskID string) {
	delete(s.taskToJob, taskID)
	for _, job := range s.cs.ListJobs(true) {
		if job.Payload.Message == taskID {
			s.cs.RemoveJob(job.ID)
		}
	}
}

// isCurrentRegistration reports whether jobID is taskID's current
// registration (registration identity, r13).
func (s *TaskTriggerScheduler) isCurrentRegistration(taskID, jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	tracked, ok := s.taskToJob[taskID]
	return ok && tracked.jobID == jobID
}

// OccurrencePending reports whether taskID's current registration still
// carries an undelivered occurrence: its next run is armed (future or due but
// not yet collected) or its dispatch is queued or running. It is the
// due-but-pending half of the plan engine's expected-wait predicate (r17
// TK-30 state 2).
func (s *TaskTriggerScheduler) OccurrencePending(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	tracked, ok := s.taskToJob[taskID]
	if !ok {
		return false
	}
	return s.keepsOccurrenceLocked(tracked.jobID, s.clockNowMsLocked(), false)
}

// clockNowMsLocked is now() for a caller already holding s.mu.
func (s *TaskTriggerScheduler) clockNowMsLocked() int64 {
	if s.clock == nil {
		return systemClock{}.Now().UnixMilli()
	}
	return s.clock.Now().UnixMilli()
}

// fireDecision is what prepareFire decided under the task lock.
type fireDecision struct {
	t            *task.Task // fresh task at the fire
	reset        *task.Task // the reset task to dispatch, when dispatch is true
	isRrule      bool
	fireGen      string
	occurrenceMs *int64 // the RRULE instant for the run record; nil for once
	dispatch     bool
	rearm        bool
	// gone: the task was deleted before or during the fire; its job was
	// removed (runScheduledFire reports errScheduledFireTaskGone).
	gone bool
	err  error
}

// prepareFire is the locked half of RunScheduled: it reads the task fresh
// under its coordination lock, refuses a job that is no longer the task's
// current registration or an occurrence the persisted trigger no longer
// describes, and otherwise resets the task and records the delivery marker in
// one write. Dispatch and the RRULE re-arm run after the lock is released.
func (s *TaskTriggerScheduler) prepareFire(job *cron.CronJob) fireDecision {
	taskID := job.Payload.Message
	if hook := s.currentTestHooks().preMarkerLock; hook != nil {
		hook(taskID, job.ID)
	}
	unlock := task.LockTaskWriteOrder(taskID)
	defer unlock()

	t, err := s.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			slog.Info("task_trigger: task not found, removing orphan job", "task_id", taskID, "job_id", job.ID)
			s.OnTaskDeleted(taskID)
			s.cs.RemoveJob(job.ID)
			return fireDecision{gone: true}
		}
		// Task-unreadable exit (Scheduler rule 2e): re-arming is impossible
		// here; the recovery sweep re-arms once the task is readable.
		slog.Error("task_trigger: task unreadable, cannot re-arm (relying on recovery sweep)",
			"task_id", taskID, "job_id", job.ID, "error", err)
		return fireDecision{err: fmt.Errorf("task_trigger: get task %q: %w", taskID, err)}
	}
	if t.Trigger == nil || t.Trigger.Type == task.TriggerManual {
		slog.Info("task_trigger: task trigger removed, cleaning up job", "task_id", taskID, "job_id", job.ID)
		s.OnTaskDeleted(taskID)
		s.cs.RemoveJob(job.ID)
		return fireDecision{}
	}
	if !s.isCurrentRegistration(taskID, job.ID) {
		slog.Info("task_trigger: job is no longer the task's registration, not delivering",
			"task_id", taskID, "job_id", job.ID)
		return fireDecision{}
	}
	d := fireDecision{t: t}
	d.isRrule = t.Trigger.Type == task.TriggerRecurring && t.Trigger.Config.Rrule != nil && *t.Trigger.Config.Rrule != ""
	var delivered *int64
	if job.Schedule.AtMS != nil {
		v := *job.Schedule.AtMS
		delivered = &v
	}
	if d.isRrule {
		d.fireGen = triggerGeneration(t.Trigger)
		// The run record's calendar join key (ADR-050 §3.2) is the RRULE
		// instant; a once fire's run stays an ad-hoc run (nil).
		d.occurrenceMs = delivered
	} else if t.Trigger.Config.AtMs == nil || delivered == nil || *t.Trigger.Config.AtMs != *delivered {
		slog.Info("task_trigger: occurrence no longer matches the persisted trigger, not delivering",
			"task_id", taskID, "job_id", job.ID)
		return fireDecision{}
	}
	if hook := s.getSpawnResetTestHook(); hook != nil {
		hook()
	}
	return s.resetForFire(job, d, delivered)
}

// resetForFire resets the task and records the delivery under the lock
// prepareFire holds.
func (s *TaskTriggerScheduler) resetForFire(job *cron.CronJob, d fireDecision, delivered *int64) fireDecision {
	taskID := d.t.ID
	reset, err := s.store.SpawnResetForOccurrence(taskID, delivered)
	if err == nil {
		d.reset, d.dispatch, d.rearm = reset, true, d.isRrule
		return d
	}
	switch {
	case errors.Is(err, task.ErrNotFound):
		// F5: deleted between the read and the reset — remove the orphan, never
		// re-arm a task that no longer exists.
		slog.Info("task_trigger: task not found on spawn reset (deleted mid-fire), removing orphan job",
			"task_id", taskID, "job_id", job.ID)
		s.OnTaskDeleted(taskID)
		s.cs.RemoveJob(job.ID)
		return fireDecision{gone: true}
	case errors.Is(err, task.ErrAlreadyRunning):
		// Overlap guard: never stomp an in-flight run. The skip is recorded for
		// run history; a failure to record it never blocks the re-arm.
		slog.Info("task_trigger: task already in_progress, skipping fire (overlap guard)",
			"task_id", taskID, "job_id", job.ID)
		skippedRun, recorded, recErr := s.store.RecordSkippedOccurrence(taskID, d.occurrenceMs)
		if recErr != nil {
			slog.Warn("task_trigger: failed to record skipped-occurrence run, continuing anyway",
				"task_id", taskID, "job_id", job.ID, "error", recErr)
		} else if recorded && s.emitRunStatus != nil {
			s.emitRunStatus(taskID, skippedRun.RunID, skippedRun.OccurrenceMs, skippedRun.Status)
		}
		d.rearm = d.isRrule
		return d
	case d.isRrule:
		// No retry backoff (Scheduler rule 3): the next occurrence is the retry.
		slog.Warn("task_trigger: spawn reset failed for rrule task, re-arming next occurrence (no retry backoff)",
			"task_id", taskID, "job_id", job.ID, "error", err)
		d.rearm = true
		return d
	default:
		return fireDecision{err: fmt.Errorf("task_trigger: spawn reset task %q: %w", taskID, err)}
	}
}
