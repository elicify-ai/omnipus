// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// EffectiveTaskMaxAttempts is the ONE resolver for a task's attempt limit — how
// many fresh runs the task gets before it ends Failed. The task executor
// enforces it (pkg/agent/task_executor.go::consumeAttemptOrRestart) and the task
// wire reports it (Task.effective_max_attempts), so the enforced value and the
// displayed value cannot diverge. Resolution: the task's own max_attempts, else
// the global planning.task_max_attempts, else config.DefaultTaskMaxAttempts.
//
// It deliberately does not read the goal try limit (Settings -> Performance):
// goal tries (inner, per run) and task attempts (outer, per task) are separate
// limits (founder decision 2026-09-14; issue #710 tracks whether to merge them).
func EffectiveTaskMaxAttempts(planning config.PlanningConfig, t *task.Task) int {
	var override *int
	if t != nil {
		override = t.MaxAttempts
	}
	return planning.EffectiveTaskMaxAttempts(override)
}
