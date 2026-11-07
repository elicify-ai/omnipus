// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// task_trigger_run_now.go — the scheduler half of the run-now entry-point
// contract (T10 spec r6 FR-16/FR-17, eligibility table E0–E4 in US-9).
//
// Two phases, both under the task's coordination lock
// (task.LockTaskWriteOrder) and against the fresh persisted task:
//
//   - reserveRunNowLocked decides the route. It writes nothing: the
//     reservation is the in-memory runNowReservation it returns.
//   - commitRunNowLocked is dispatch admission. It re-checks that the
//     reserved registration is still the task's current one and still carries
//     the reserved instant, then commits the consumption marker in ONE task
//     write (Store.ConsumeOccurrenceForRun) and only then replaces the
//     registration with the next boundary strictly after max(now, marker).
//
// The executor (task_executor_run_now.go) runs the admission checks between
// the two phases with the lock released, so a slow check never holds the
// ordering boundary.

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// Run-now refusals (E0, E3, E4 de-armed, admission-time registration change).
// Each message is a plain sentence the REST layer returns as-is.
var (
	// ErrRunNowDeliveryQueued is E0: the task's automatic delivery was
	// collected and is waiting for admission; that delivery will run it.
	ErrRunNowDeliveryQueued = errors.New(
		"an automatic run of this task is already queued and will start shortly, so Run now did not start another run")
	// ErrRunNowNotArmed is E3: the requested instant is in the future and is
	// not the task's next scheduled run.
	ErrRunNowNotArmed = errors.New(
		"this time is not the task's next scheduled run, so it cannot be run now; refresh the calendar and try again")
	// ErrRunNowScheduleOutOfSync is E4 on a de-armed registration: the task
	// has a future schedule but the scheduler holds no registration for it.
	ErrRunNowScheduleOutOfSync = errors.New(
		"this task's schedule is out of sync, so Run now cannot use it; save the schedule again, then retry")
	// ErrRunNowScheduleChanged is the admission-time identity check: the
	// registration the request reserved was replaced or consumed meanwhile.
	ErrRunNowScheduleChanged = errors.New(
		"the task's schedule changed while Run now was starting, so nothing was run; refresh and try again")
	// ErrRunNowAlreadyRunning: the task already has a run in progress.
	ErrRunNowAlreadyRunning = errors.New("this task is already running")
)

// runNowRoute is the route a run-now reservation decided.
type runNowRoute int

const (
	// runNowOrdinary is today's ordinary dispatch (E4 with nothing armed, or
	// a task without a schedule).
	runNowOrdinary runNowRoute = iota
	// runNowHistorical is E2: today's materialize-on-demand re-run of a past
	// occurrence, carrying that occurrence as the run's calendar key.
	runNowHistorical
	// runNowConsume is E1 (and E4 when armed): the run IS the armed
	// occurrence, consumed at admission.
	runNowConsume
)

// runNowReservation is the in-memory reservation (FR-17(a)). Nothing in it is
// durable; dropping it releases nothing because nothing was taken.
type runNowReservation struct {
	route      runNowRoute
	taskID     string
	jobID      string
	generation string
	instant    int64 // the armed instant (consume) or the explicit past one (historical)
}

// scheduleAnchorMs is the instant every next-occurrence computation of the
// scheduler counts from: max(now, run-now consumption marker), so an
// occurrence consumed early is never armed again — not by a re-arm, a
// reconciliation or a boot recomputation (T10 FR-16).
func scheduleAnchorMs(t *task.Task, nowMs int64) int64 {
	if t != nil && t.ConsumedOccurrenceMs != nil && *t.ConsumedOccurrenceMs > nowMs {
		return *t.ConsumedOccurrenceMs
	}
	return nowMs
}

// reserveRunNowLocked applies the eligibility table to t, read fresh under
// t's coordination lock (held by the caller). occurrenceMs is the requested
// instant, nil for the task-level action. It writes nothing.
func (s *TaskTriggerScheduler) reserveRunNowLocked(t *task.Task, occurrenceMs *int64) (runNowReservation, error) {
	r := runNowReservation{taskID: t.ID}
	nowMs := s.now().UnixMilli()
	scheduled := t.Trigger.HasSchedule() && t.EffectiveSurface() != task.SurfaceHeartbeat

	s.mu.Lock()
	tracked, isTracked := s.taskToJob[t.ID]
	armedAt, armed, inFlight := int64(0), false, false
	if isTracked {
		inFlight = s.cs.JobDispatchInFlight(tracked.jobID)
		if job, ok := s.cs.GetJob(tracked.jobID); ok && job.State.NextRunAtMS != nil {
			armedAt, armed = *job.State.NextRunAtMS, true
		}
	}
	s.mu.Unlock()

	if scheduled && inFlight {
		if t.Status == task.StatusInProgress {
			return r, ErrRunNowAlreadyRunning
		}
		return r, ErrRunNowDeliveryQueued // E0, checked first
	}
	armed = armed && scheduled
	consume := func() (runNowReservation, error) {
		r.route, r.jobID, r.generation, r.instant = runNowConsume, tracked.jobID, tracked.generation, armedAt
		return r, nil
	}
	if occurrenceMs != nil {
		switch {
		case armed && *occurrenceMs == armedAt:
			return consume() // E1
		case *occurrenceMs <= nowMs:
			r.route, r.instant = runNowHistorical, *occurrenceMs // E2
			return r, nil
		default:
			return r, ErrRunNowNotArmed // E3
		}
	}
	if armed {
		return consume() // E4, armed
	}
	if scheduled {
		if _, err := desiredSchedule(t.Trigger, scheduleAnchorMs(t, nowMs)); err == nil {
			return r, ErrRunNowScheduleOutOfSync // E4, live future trigger, de-armed
		}
	}
	return r, nil // E4, nothing armed: ordinary dispatch
}

// commitRunNowLocked is dispatch admission for a consume reservation. fresh is
// the task read under its coordination lock (held by the caller). It verifies
// registration identity, commits the marker in one task write, and only then
// moves the registration past the consumed instant. A failure before the
// marker write leaves the task and its registration exactly as they were.
func (s *TaskTriggerScheduler) commitRunNowLocked(fresh *task.Task, r runNowReservation) (*task.Task, error) {
	if fresh.Status == task.StatusInProgress {
		return nil, ErrRunNowAlreadyRunning
	}
	s.mu.Lock()
	tracked, ok := s.taskToJob[fresh.ID]
	current := ok && tracked.jobID == r.jobID && tracked.generation == r.generation &&
		triggerGeneration(fresh.Trigger) == r.generation && !s.cs.JobDispatchInFlight(r.jobID)
	if current {
		job, found := s.cs.GetJob(r.jobID)
		current = found && job.State.NextRunAtMS != nil && *job.State.NextRunAtMS == r.instant
	}
	s.mu.Unlock()
	if !current {
		return nil, ErrRunNowScheduleChanged
	}

	reset, err := s.store.ConsumeOccurrenceForRun(fresh.ID, r.instant)
	if err != nil {
		if errors.Is(err, task.ErrAlreadyRunning) {
			return nil, ErrRunNowAlreadyRunning
		}
		return nil, fmt.Errorf("task_trigger: run-now: record the consumed occurrence: %w", err)
	}
	s.advancePastConsumedLocked(reset, r)
	return reset, nil
}

// advancePastConsumedLocked replaces the consumed registration with the next
// boundary strictly after max(now, marker), or removes it when none is left
// (a consumed once, a final COUNT occurrence). The consumption is already
// committed: a registration failure is logged and leaves no job (the same
// best-effort de-arm every other registration path uses); the run proceeds.
func (s *TaskTriggerScheduler) advancePastConsumedLocked(t *task.Task, r runNowReservation) {
	sched, err := desiredSchedule(t.Trigger, scheduleAnchorMs(t, s.now().UnixMilli()))
	if err != nil {
		s.OnTaskDeleted(t.ID)
		if !errors.Is(err, errNoFutureOccurrence) {
			slog.Error("task_trigger: run-now: cannot compute the occurrence after the consumed one",
				"task_id", t.ID, "consumed_occurrence_ms", r.instant, "error", err)
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.replaceJobLocked(t.ID, s.jobSpecFor(t, sched), r.generation)
	if err != nil {
		s.dearmAllLocked(t.ID)
		slog.Error("task_trigger: run-now: failed to arm the occurrence after the consumed one",
			"task_id", t.ID, "consumed_occurrence_ms", r.instant, "error", err)
		return
	}
	slog.Info("task_trigger: run-now consumed the armed occurrence",
		"task_id", t.ID, "consumed_occurrence_ms", r.instant, "next_job_id", job.ID)
}
