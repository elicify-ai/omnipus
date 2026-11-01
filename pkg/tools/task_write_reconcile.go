// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// task_write_reconcile.go — the shared task write with its post-write
// reconciliation (tool-parity spec r17 §7.2 TK-03 MAJ-007/R4-MAJ-004,
// TK-30). The REST PATCH handler and update_task both write through
// WriteTaskReconciled, which holds the per-task coordination lock
// (task.LockTaskWriteOrder) across the task write and the paired goal's
// synchronisation, so writers of one task are strictly ordered and a stale
// snapshot can never overwrite a newer goal prompt. It is the only writer of
// a task goal's prompt after a task write.
//
// Each reconciliation step runs if reached; a failure is reported in the
// outcome, never swallowed, and never undoes the task write that already
// landed — the caller tells the person or agent the truth (REST 500, tool
// success with goal_sync_warning).

import (
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// goalPromptMaxRunes is how much of the task prompt a paired goal carries
// (r17 TK-03: the first 4,000 characters).
const goalPromptMaxRunes = 4000

// TaskWriteRequest is one reconciled task write.
type TaskWriteRequest struct {
	Patch task.Patch
	// Prior, when true, also returns the task as it was immediately before
	// the write, read under the same store lock (UpdateWithPrior).
	Prior bool
	// SyncGoalDefinition, when non-nil, carries a criteria / Definition of
	// Done change onto the paired goal record. It runs under the lock after
	// the task write and before the prompt synchronisation.
	SyncGoalDefinition func(updated *task.Task) error
	// RetryWithoutStatus, when non-nil, is consulted after a refused write: if
	// it returns true the write is retried once with Patch.Status cleared
	// (the plan-detach rule, r17 TK-11).
	RetryWithoutStatus func(err error) bool
}

// TaskWriteOutcome is the result of a reconciled write that landed.
type TaskWriteOutcome struct {
	Updated *task.Task
	Prior   *task.Task
	// StatusDropped reports that RetryWithoutStatus cleared the status.
	StatusDropped bool
	// GoalDefinitionErr and GoalPromptErr are the paired goal
	// synchronisation failures, if any. The task write landed regardless.
	GoalDefinitionErr error
	GoalPromptErr     error
}

// WriteTaskReconciled writes req.Patch to task id and reconciles the paired
// goal record under one hold of the task's coordination lock. The error is
// the task write's own; reconciliation failures are in the outcome.
func WriteTaskReconciled(store *task.Store, id string, req TaskWriteRequest) (TaskWriteOutcome, error) {
	unlock := task.LockTaskWriteOrder(id)
	defer unlock()

	var out TaskWriteOutcome
	write := func() error {
		var err error
		if req.Prior {
			out.Updated, out.Prior, err = store.UpdateWithPrior(id, req.Patch)
		} else {
			out.Updated, err = store.Update(id, req.Patch)
		}
		return err
	}
	err := write()
	if err != nil && req.Patch.Status != nil && req.RetryWithoutStatus != nil && req.RetryWithoutStatus(err) {
		req.Patch.Status = nil
		out.StatusDropped = true
		err = write()
	}
	if err != nil {
		return TaskWriteOutcome{}, err
	}
	if req.SyncGoalDefinition != nil {
		out.GoalDefinitionErr = req.SyncGoalDefinition(out.Updated)
	}
	if req.Patch.Prompt != nil || req.Patch.Title != nil {
		out.GoalPromptErr = SyncTaskGoalPrompt(GoalStoreForTasks(store), out.Updated)
	}
	return out, nil
}

// SyncTaskGoalPrompt sets the prompt of the goal record paired with t to the
// first 4,000 characters of t's prompt, or t's title when the prompt is
// empty. A task with no paired goal gets none (r17 TK-03).
func SyncTaskGoalPrompt(gs *goal.Store, t *task.Task) error {
	existing, err := gs.GetByOwner(generated.GoalOwnerKindTask, t.ID)
	if err != nil {
		if errors.Is(err, goal.ErrOwnerNotFound) {
			return nil
		}
		return fmt.Errorf("load paired goal record: %w", err)
	}
	want := GoalPromptForTask(t)
	if existing.Prompt == want {
		return nil
	}
	if _, err := gs.Update(existing.GoalID, func(g *goal.Goal) error {
		g.Prompt = want
		return nil
	}); err != nil {
		return fmt.Errorf("update paired goal prompt: %w", err)
	}
	return nil
}

// GoalPromptForTask is the goal prompt a task's prompt and title imply.
func GoalPromptForTask(t *task.Task) string {
	p := t.Prompt
	if strings.TrimSpace(p) == "" {
		p = t.Title
	}
	if r := []rune(p); len(r) > goalPromptMaxRunes {
		p = string(r[:goalPromptMaxRunes])
	}
	return p
}
