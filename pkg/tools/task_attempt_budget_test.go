// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestEffectiveTaskMaxAttempts_IsTheTaskAttemptLimitNotTheGoalTryLimit pins the
// shared task attempt resolver (the executor enforces it, the task wire reports
// it). Founder decision 2026-09-14: task attempts (fresh runs of a task) and
// goal tries (tries within one run) are separate limits, so the goal try limit
// never feeds this resolver.
func TestEffectiveTaskMaxAttempts_IsTheTaskAttemptLimitNotTheGoalTryLimit(t *testing.T) {
	goalTries5 := config.PlanningConfig{GoalMaxRounds: 5}
	attempts4 := config.PlanningConfig{GoalMaxRounds: 5, TaskMaxAttempts: 4}

	t.Run("no task: the default of 3, whatever the goal try limit", func(t *testing.T) {
		assert.Equal(t, 3, EffectiveTaskMaxAttempts(goalTries5, nil))
	})
	t.Run("task without its own limit: the global task attempt limit", func(t *testing.T) {
		assert.Equal(t, 4, EffectiveTaskMaxAttempts(attempts4, &task.Task{}))
	})
	t.Run("task with its own max_attempts: that value", func(t *testing.T) {
		two := 2
		assert.Equal(t, 2, EffectiveTaskMaxAttempts(attempts4, &task.Task{MaxAttempts: &two}))
	})
}
