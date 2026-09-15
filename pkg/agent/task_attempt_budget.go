// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// planningConfigOf returns al's live PlanningConfig, or the zero value (whose
// resolvers return the shipped defaults) when there is no loop or config.
func planningConfigOf(al *AgentLoop) config.PlanningConfig {
	if al == nil {
		return config.PlanningConfig{}
	}
	cfg := al.GetConfig()
	if cfg == nil {
		return config.PlanningConfig{}
	}
	return cfg.Planning
}

// goalTryLimit returns the LIVE global goal try limit (Settings -> Performance
// goal_max_rounds): how many tries a goal gets — in chat, and within one task
// run (the INNER limit). It is read at call time, never captured at boot, so a
// Settings change applies to every goal that starts after it; a task run
// snapshots it onto its goal record when the run activates the goal.
func goalTryLimit(al *AgentLoop) int {
	return planningConfigOf(al).EffectiveGoalMaxRounds()
}

// taskAttemptHardCeiling is the divergence brake on TASK ATTEMPTS (the outer
// counter, Task.AttemptCount; FR-047, GOAL-FR-026 "the 2 × effective budget
// hard ceiling"): twice the resolved task attempt limit. It is an independent
// second gate "regardless of pending re-dispatch or interrupt state"; see
// consumeAttemptOrRestart. Goal tries are bounded separately, by the goal
// record's own MaxRounds.
func taskAttemptHardCeiling(maxAttempts int) int {
	return 2 * maxAttempts
}

// resolveTaskMaxAttempts resolves t's attempt limit (how many fresh runs it
// gets) through the ONE shared resolver the task wire also reports
// (tools.EffectiveTaskMaxAttempts): per-task max_attempts, else the global
// planning.task_max_attempts, else config.DefaultTaskMaxAttempts (3).
func (te *TaskExecutor) resolveTaskMaxAttempts(t *task.Task) int {
	return tools.EffectiveTaskMaxAttempts(planningConfigOf(te.agentLoop), t)
}
