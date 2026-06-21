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
// caller).
var ErrAlreadyClaimed = errors.New("task already claimed")

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

// ClaimParentFollowUp atomically claims the right to launch the "all children
// done → resume parent" follow-up for the parent task id, returning true to
// exactly one caller. Mirrors ClaimForRun's CAS-style claim to close the
// duplicate-resume race under concurrent sibling completion.
func (s *Store) ClaimParentFollowUp(id string) (bool, error) {
	if err := validateID(id); err != nil {
		return false, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return false, err
	}
	if t.FollowedUp {
		return false, nil
	}
	t.FollowedUp = true
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return false, err
	}
	return true, nil
}
