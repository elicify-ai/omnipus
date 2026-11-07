// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// task_executor_run_now.go — the ONE trigger-aware run-now operation (T10
// spec r6 FR-16/FR-17). Every surface that starts a task outside its own due
// fire reaches it before any status write:
//
//   - the task-runs REST run-now (StartOccurrenceRun), with or without an
//     explicit occurrence;
//   - the run_task tool (StartTaskNowAs) for a schedule-bearing task.
//
// Order: reservation (route decision under the task's coordination lock,
// in-memory, writes nothing — task_trigger_run_now.go) → admission checks with
// the lock released (the test seam, the caller's cancellation, draining, the
// agent, the plan gate, unmet dependencies and the ACTUAL caller's delegation
// authority) → commitment under the lock (registration identity, then the
// consumption marker in one task write) → dispatch through executeTask, the
// same claim-and-launch path a scheduled fire uses, as a manual run carrying
// the consumed instant. Any failure before the commitment leaves the armed
// occurrence intact: nothing durable was written.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// SetRunNowAdmissionHookForTest installs a callback invoked at the run-now
// reservation→admission edge, with the caller's context and the task ID,
// while no lock is held. A non-nil error refuses the run before anything
// durable is written. Pass nil to clear it; production never sets one.
func (te *TaskExecutor) SetRunNowAdmissionHookForTest(fn func(ctx context.Context, taskID string) error) {
	te.mu.Lock()
	te.runNowAdmissionHook = fn
	te.mu.Unlock()
}

// triggerScheduler returns the installed task-trigger scheduler, nil when
// none is wired (unit tests without a gateway).
func (te *TaskExecutor) triggerScheduler() *TaskTriggerScheduler {
	if te.agentLoop == nil {
		return nil
	}
	return te.agentLoop.taskTriggerScheduler()
}

// reserveRunNow is the reservation phase: the eligibility table applied to
// the fresh task under its coordination lock. Without a scheduler only the
// trigger-independent rows apply (a future instant is never runnable, a past
// one re-runs as today, no instant is ordinary dispatch).
func (te *TaskExecutor) reserveRunNow(taskID string, occurrenceMs *int64) (runNowReservation, error) {
	unlock := task.LockTaskWriteOrder(taskID)
	defer unlock()
	t, err := te.store.Get(taskID)
	if err != nil {
		return runNowReservation{}, fmt.Errorf("task_executor: run now: get task %q: %w", taskID, err)
	}
	if s := te.triggerScheduler(); s != nil {
		return s.reserveRunNowLocked(t, occurrenceMs)
	}
	r := runNowReservation{taskID: taskID}
	if occurrenceMs != nil {
		if *occurrenceMs > time.Now().UnixMilli() {
			return r, ErrRunNowNotArmed
		}
		r.route, r.instant = runNowHistorical, *occurrenceMs
	}
	return r, nil
}

// checkRunNowAdmission runs the dispatch-phase checks of a run-now with no
// lock held: an assigned and registered agent, the plan gate, satisfied
// dependencies, and — for an agent's run_task — the ACTUAL caller's
// delegation authority against the live graph (FR-17(c)). The stored task
// authority never substitutes for the caller.
func (te *TaskExecutor) checkRunNowAdmission(ctx context.Context, t *task.Task, initiator *task.Initiator) error {
	if t.AgentID == "" {
		return fmt.Errorf("task_executor: run now: task %q has no agent assigned", t.ID)
	}
	if _, ok := te.agentLoop.GetRegistry().GetAgent(t.AgentID); !ok {
		return fmt.Errorf("task_executor: run now: agent %q not found", t.AgentID)
	}
	if err := te.requirePlanExecuting(t); err != nil {
		return err
	}
	for _, depID := range t.BlockedBy {
		dep, err := te.store.Get(depID)
		if err != nil || dep.Status != task.StatusDone {
			return fmt.Errorf("task_executor: run now: task %q is blocked by %q (not done)", t.ID, depID)
		}
	}
	if initiator != nil {
		if _, denial := te.agentLoop.authorizeInitiatedRun(ctx, *initiator, t.AgentID, t.WorkspaceID); denial != nil {
			return fmt.Errorf("task_executor: run now: refused by the delegation policy: %s", denial.Reason)
		}
	}
	return nil
}

// admitRunNow takes a consume reservation through admission and commitment
// and dispatches the consumed occurrence. It returns the run's session ID.
func (te *TaskExecutor) admitRunNow(ctx context.Context, r runNowReservation, initiator *task.Initiator) (string, error) {
	te.mu.Lock()
	hook := te.runNowAdmissionHook
	te.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, r.taskID); err != nil {
			return "", fmt.Errorf("task_executor: run now refused before admission: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("task_executor: run now: the request ended before admission: %w", err)
	}
	if te.draining.Load() {
		return "", ErrExecutorDraining
	}
	t, err := te.store.Get(r.taskID)
	if err != nil {
		return "", fmt.Errorf("task_executor: run now: get task %q: %w", r.taskID, err)
	}
	if err := te.checkRunNowAdmission(ctx, t, initiator); err != nil {
		return "", err
	}
	s := te.triggerScheduler()
	if s == nil {
		return "", ErrRunNowScheduleChanged
	}

	unlock := task.LockTaskWriteOrder(r.taskID)
	fresh, err := te.store.Get(r.taskID)
	if err == nil && te.runSlotTaken(r.taskID) {
		err = ErrRunNowAlreadyRunning
	}
	if err == nil {
		_, err = s.commitRunNowLocked(fresh, r)
	}
	unlock()
	if err != nil {
		return "", err
	}

	instant := r.instant
	sessionID, err := te.dispatchRunNow(r.taskID, &instant, initiator)
	if err != nil {
		// Committed: the occurrence is consumed (FR-17(b), the same outcome as
		// an automatic fire lost after collection). Report it loudly.
		slog.Error("task_executor: run-now consumed the armed occurrence but its dispatch failed",
			"task_id", r.taskID, "consumed_occurrence_ms", r.instant, "error", err)
	}
	return sessionID, err
}

// runSlotTaken reports whether taskID already holds a live or reserved run slot.
func (te *TaskExecutor) runSlotTaken(taskID string) bool {
	te.mu.Lock()
	defer te.mu.Unlock()
	_, taken := te.running[taskID]
	return taken
}

// dispatchRunNow launches the run through executeTask — the claim-and-launch
// path every scheduled fire uses — as a manual run carrying occurrenceMs as
// its calendar key. The run is detached from the caller's context (see
// StartOccurrenceRun) and unattended (#1263 R5). An agent caller's authority
// is re-checked after the claim with the same live graph; nil is a person.
func (te *TaskExecutor) dispatchRunNow(taskID string, occurrenceMs *int64, initiator *task.Initiator) (string, error) {
	var resolve initiatorResolver
	if initiator != nil {
		resolve = func(*task.Task) (*task.Initiator, bool, error) { return initiator, false, nil }
	}
	ctx := tools.WithAutoDenyAsk(context.Background(), true)
	if err := te.executeTask(ctx, taskID, occurrenceMs, task.RunKindManual, false, resolve); err != nil {
		return "", err
	}
	started, err := te.store.Get(taskID)
	if err != nil {
		return "", fmt.Errorf("task_executor: run now: started task %q unreadable: %w", taskID, err)
	}
	return started.SessionID, nil
}

// startScheduledTaskNow is StartTaskNowAs for a schedule-bearing task (the
// run_task tool, which no longer writes a status first for such a task): the
// armed occurrence is consumed; with nothing armed the task gets today's
// run-aware ordinary dispatch (reset, claim, launch) under the caller's
// authority.
func (te *TaskExecutor) startScheduledTaskNow(ctx context.Context, taskID string, initiator *task.Initiator) (string, error) {
	r, err := te.reserveRunNow(taskID, nil)
	if err != nil {
		return "", err
	}
	if r.route == runNowConsume {
		return te.admitRunNow(ctx, r, initiator)
	}
	t, err := te.store.Get(taskID)
	if err != nil {
		return "", fmt.Errorf("task_executor: run now: get task %q: %w", taskID, err)
	}
	if err := te.checkRunNowAdmission(ctx, t, initiator); err != nil {
		return "", err
	}
	if _, err := te.store.SpawnReset(taskID); err != nil {
		if errors.Is(err, task.ErrAlreadyRunning) {
			return "", ErrRunNowAlreadyRunning
		}
		return "", fmt.Errorf("task_executor: run now: reset task %q: %w", taskID, err)
	}
	return te.dispatchRunNow(taskID, nil, initiator)
}
