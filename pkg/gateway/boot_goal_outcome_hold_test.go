// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestBootReconcile_GoalOutcomeHeldUntilRecorderInstalled (6b re-review N2):
// the boot reconciles interrupted tasks before any task producer starts —
// earlier than the goal outcome recorder and its session stores are wired.
// The goal transition that reconcile performs must not lose its lasting
// goal_outcome line: it is held and handed to the recorder once installed.
func TestBootReconcile_GoalOutcomeHeldUntilRecorderInstalled(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	taskID, goalID := startTaskWithActiveGoal(t, api, wsID, "stranded by a crash")

	restore := tools.SetTaskGoalEndedHook(nil) // early boot: no recorder yet
	defer restore()
	tools.HoldTaskGoalEndedUntilReplay()
	defer func() { _ = tools.ReplayHeldTaskGoalEnded() }() // never leave the hold on for later tests

	api.reconcileStuckTasks()
	stored, err := api.taskStore.Get(taskID)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, stored.Status, "fixture: the reconcile must reset the stuck task")

	var mu sync.Mutex
	var delivered []goal.Goal
	var statuses []task.Status
	tools.SetTaskGoalEndedHook(func(ended goal.Goal, status task.Status) {
		mu.Lock()
		defer mu.Unlock()
		delivered = append(delivered, ended)
		statuses = append(statuses, status)
	})
	require.NoError(t, tools.ReplayHeldTaskGoalEnded())

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, delivered, 1,
		"the goal transition made before the recorder existed must reach it once it is installed")
	require.Equal(t, goalID, delivered[0].GoalID)
	require.Equal(t, gen.GoalStateExhausted, delivered[0].State)
	require.Equal(t, task.StatusFailed, statuses[0])
}
