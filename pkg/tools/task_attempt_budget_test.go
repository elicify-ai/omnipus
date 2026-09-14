// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestEffectiveTaskMaxAttempts_HonoursOnlyAStartedRunsSnapshot pins the shared
// task attempt ceiling resolver (the executor enforces it, the task wire
// reports it). Founder decision 2026-09-14: the Settings goal try limit bounds
// tasks, and a goal already running keeps the limit it started with — so only
// a STARTED run's snapshot is honoured; a record still in the defining phase
// (created, not started) follows the live setting.
func TestEffectiveTaskMaxAttempts_HonoursOnlyAStartedRunsSnapshot(t *testing.T) {
	now := time.Now().UTC()
	author := task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"}
	crit := []task.AcceptanceCriterion{{Text: "the report is written", Status: task.CritPending, Author: author}}
	dod := []task.AcceptanceCriterion{{Text: "nothing else broke", Status: task.CritPending, Author: author}}
	newRecord := func(maxRounds int) *goal.Goal {
		g, err := goal.New(generated.GoalOwnerKindTask, "task-1", generated.TaskExplicit,
			"write it", "", crit, dod, maxRounds, now)
		require.NoError(t, err)
		return g
	}
	live5 := config.PlanningConfig{GoalMaxRounds: 5}
	live10 := config.PlanningConfig{GoalMaxRounds: 10}

	t.Run("no task and no record: the live goal try limit", func(t *testing.T) {
		assert.Equal(t, 5, EffectiveTaskMaxAttempts(live5, nil, nil))
	})
	t.Run("no paired record: the live goal try limit", func(t *testing.T) {
		assert.Equal(t, 5, EffectiveTaskMaxAttempts(live5, &task.Task{}, nil))
	})
	t.Run("record created under an older limit but not started: the live limit", func(t *testing.T) {
		assert.Equal(t, 5, EffectiveTaskMaxAttempts(live5, &task.Task{}, newRecord(20)))
	})
	t.Run("started run: the limit it started with, not a later Settings value", func(t *testing.T) {
		g := newRecord(5)
		require.NoError(t, g.Activate("session-1", now))
		assert.Equal(t, 5, EffectiveTaskMaxAttempts(live10, &task.Task{}, g))
	})
	t.Run("per-task max_attempts wins over a started run and the live limit", func(t *testing.T) {
		g := newRecord(5)
		require.NoError(t, g.Activate("session-1", now))
		two := 2
		assert.Equal(t, 2, EffectiveTaskMaxAttempts(live10, &task.Task{MaxAttempts: &two}, g))
	})
}
