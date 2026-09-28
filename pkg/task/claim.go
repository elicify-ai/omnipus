// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"errors"
	"fmt"
	"time"
)

// ErrAlreadyClaimed is returned by ClaimForRun when the task is not in a
// dispatchable state (already in_progress, terminal, or claimed by a concurrent
// caller). It is a control-flow sentinel, NOT a wire validation error.
var ErrAlreadyClaimed = errors.New("task already claimed")

// ErrAlreadyRunning means "a run is already in flight for this task — do not
// stomp it". Two producers share it, both control flow:
//
//   - SpawnReset: a trigger fire would stomp a task that is already
//     in_progress (the trigger overlap guard).
//   - The task executor's launch-slot claim (task_executor.go::StartTaskNow
//     and dispatchLaunchedTask): a concurrent start lost the race for the
//     task's single launch slot. The callers of StartTaskNow
//     (rest_tasks.go::launchIfStarted, run_task.go::Execute) map it to the
//     documented safe no-op — never a revert of the winner's live state.
//
// Handle both with errors.Is and treat as a benign skip / idempotent no-op,
// never a failure.
var ErrAlreadyRunning = errors.New("task already running")

// ClaimForRun atomically transitions a dispatchable task to in_progress under
// the per-task lock, making the transition a single critical section. A task is
// dispatchable when its status is `next` (triaged & ready) — the executor only
// dispatches `next` tasks whose dependencies are all `done`.
//
// This prevents the TOCTOU race where two concurrent callers (the heartbeat and
// advanceBlockedTasks) both pass a status guard and both dispatch a goroutine.
//
// Returns (updated task, nil) on success; (nil, ErrAlreadyClaimed) when the
// task is not `next`; (nil, ErrNotFound) when absent; (nil, err) on I/O.
func (s *Store) ClaimForRun(id string, startedAt time.Time) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if t.Status != StatusNext {
		return nil, fmt.Errorf("task: %w: task %q is %q, not next", ErrAlreadyClaimed, id, t.Status)
	}
	t.Status = StatusInProgress
	t.StartedAt = startedAt.UTC().Format(time.RFC3339)
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
}
