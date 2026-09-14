// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
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
// goal_max_rounds) — the ONE setting that bounds how many tries a goal gets,
// in chat and on a task alike (founder decision 2026-09-14, D-D/D-E). It is
// read at call time, never captured at boot, so a Settings change applies to
// every goal that starts after it.
func goalTryLimit(al *AgentLoop) int {
	return planningConfigOf(al).EffectiveGoalMaxRounds()
}

// taskAttemptHardCeiling is the task path's divergence brake (FR-047,
// GOAL-FR-026 "the 2 × effective budget hard ceiling"): twice the RESOLVED
// attempt ceiling, so it follows the goal try limit (or a per-task override)
// rather than any fixed number. It is an independent second gate "regardless
// of pending re-dispatch or interrupt state"; see consumeAttemptOrExhaust.
func taskAttemptHardCeiling(maxAttempts int) int {
	return 2 * maxAttempts
}

// resolveTaskMaxAttempts resolves t's attempt ceiling through the ONE shared
// resolver (tools.EffectiveTaskMaxAttempts): per-task max_attempts, else the
// limit snapshotted onto the task's paired goal record when this run started,
// else the live goal try limit.
//
// A goal-store read fault is logged at WARN and resolves as "no snapshot",
// i.e. the live goal try limit. That is the value activateTaskGoal stamped
// onto the record at run start unless Settings changed mid-run, so the
// fallback can only ever differ from the snapshot by honouring the current
// Settings value — it never falls back to a hardcoded number.
func (te *TaskExecutor) resolveTaskMaxAttempts(t *task.Task) int {
	var paired *goal.Goal
	g, err := resolveGoalRecordStore().GetByOwner(generated.GoalOwnerKindTask, t.ID)
	switch {
	case err == nil:
		paired = g
	case errors.Is(err, goal.ErrOwnerNotFound):
		// A task with no paired goal record (pre-D-C, GOAL-FR-023): the live
		// goal try limit applies. Normal, not a fault.
	default:
		logger.WarnCF("task_executor",
			"goal-loop: could not read the paired goal record to resolve the attempt ceiling; "+
				"using the live goal try limit",
			map[string]any{"task_id": t.ID, "error": err.Error()})
	}
	return tools.EffectiveTaskMaxAttempts(planningConfigOf(te.agentLoop), t, paired)
}
