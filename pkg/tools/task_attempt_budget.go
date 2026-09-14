// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// RunningGoalMaxRounds returns the goal try limit snapshotted onto a task's
// paired goal record for its current (or most recent) run, or 0 when there is
// nothing to honour: no paired record, or a record still in the defining
// phase. A defining record's MaxRounds is only the value in force when the
// task was CREATED; the run has not started, so it has no limit of its own yet
// and the live Settings value applies (TaskExecutor.activateTaskGoal stamps
// the live value onto the record the moment the run starts, mirroring a chat
// goal's snapshot at `/goal` set time).
func RunningGoalMaxRounds(g *goal.Goal) int {
	if g == nil || g.IsDefining() {
		return 0
	}
	return g.MaxRounds
}

// EffectiveTaskMaxAttempts is the ONE resolver for a task's attempt ceiling —
// the number the task executor enforces (pkg/agent/task_executor.go::
// consumeAttemptOrExhaust) and the number the task wire reports
// (Task.effective_max_attempts). Both call this, so the enforced value and the
// displayed value cannot diverge. It resolves from the single global goal try
// limit (Settings -> Performance goal_max_rounds, founder decision 2026-09-14,
// ADR-086 GOAL-FR-024/D-D/D-E) through config.PlanningConfig.
// EffectiveTaskMaxAttempts: per-task max_attempts override, else the running
// goal's snapshot, else the live global value. pairedGoal may be nil.
func EffectiveTaskMaxAttempts(planning config.PlanningConfig, t *task.Task, pairedGoal *goal.Goal) int {
	var override *int
	if t != nil {
		override = t.MaxAttempts
	}
	return planning.EffectiveTaskMaxAttempts(override, RunningGoalMaxRounds(pairedGoal))
}
