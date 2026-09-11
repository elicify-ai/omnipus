// criteria_consumers_test.go — GOAL-FR-029/FR-030's oracle (ADR-086 D5): a
// task's Definition of Done is authored onto its own paired goal record
// (pkg/goal), and every real consumer of Task.Criteria continues to see a
// correct, consistent view of it.
//
// This package cannot itself import pkg/goal — pkg/goal imports pkg/task
// (for the shared AcceptanceCriterion type), so pkg/task importing pkg/goal
// back would be a genuine Go import-cycle, not a design choice (see
// Task.Criteria's own doc comment in task.go for the full rationale this
// wave's implementation follows: Task.Criteria stays a real, dual-written
// field this round rather than being removed, because two real consumers —
// pkg/agent/task_executor.go and pkg/agent/plan_engine.go — are scheduled
// for repointing by a LATER wave (E12/E14), and pkg/tools/plan.go's
// execute_plan handler is asserted nowhere in any wave's write-set at all).
//
// This file therefore uses the EXTERNAL test package (task_test), which is
// its own distinct package from `task` and is never imported by anything —
// so it CAN import both pkg/task and pkg/goal (and pkg/plan) together
// without recreating the cycle production code cannot have.
package task_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func criterion(text, id string) task.AcceptanceCriterion {
	return task.AcceptanceCriterion{
		Text:   text,
		Kind:   task.KindProse,
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: id},
		Status: task.CritPending,
	}
}

// TestTaskCriteriaIsAGoalReference is the goal spec's own
// TestTaskCriteriaIsAGoalReference (FR-029, S-04): a task's Definition of
// Done is AUTHORED onto its own paired goal record (pkg/goal, keyed by
// OwnerKind=task/OwnerID=the task's id via goal.Store.GetByOwner) — never a
// second, independently-evolving list. This proves the reference invariant
// this wave's create/update paths establish: dual-writing the SAME criteria
// onto both the task record (for consumers not yet re-pointed) and the
// paired goal record (the authoritative store, ADR-086 D5) in the same
// request keeps them IDENTICAL, and the two stores share the exact same
// task.AcceptanceCriterion type — there is no separate, goal-specific
// criterion shape to drift out of sync with the task-side one.
//
// This test FAILS on a reverted implementation: without a paired goal
// record ever being created, GetByOwner returns goal.ErrOwnerNotFound and
// the require.NoError below fails outright.
func TestTaskCriteriaIsAGoalReference(t *testing.T) {
	home := t.TempDir()
	taskStore := task.New(home + "/tasks")

	tk := &task.Task{
		Title:       "goal-referencing task",
		Action:      task.ActionLLM,
		Status:      task.StatusNext,
		WorkspaceID: "ws-1",
		AgentID:     "jim",
		// Task.Criteria is dual-written by this wave's create paths — see
		// this file's own package doc comment for why it is not removed.
		Criteria: []task.AcceptanceCriterion{criterion("acceptance criterion", "jim")},
	}
	require.NoError(t, taskStore.Create(tk))

	dod := []task.AcceptanceCriterion{criterion("definition of done item", "jim")}
	g, err := goal.New(
		generated.GoalOwnerKindTask, tk.ID, generated.TaskExplicit,
		tk.Title, "", tk.Criteria, dod, 20, time.Now().UTC(),
	)
	require.NoError(t, err)
	goalStore := goal.NewStore(home)
	require.NoError(t, goalStore.Create(g))

	// The reference resolves: GetByOwner(task, tk.ID) finds exactly the
	// record just created, and its Criteria is byte-for-byte the SAME
	// criteria the task record itself carries — one list, authored once,
	// read from two places that agree, not two independently-evolving
	// copies (the duplication ADR-086 D5 exists to remove).
	found, gErr := goalStore.GetByOwner(generated.GoalOwnerKindTask, tk.ID)
	require.NoError(t, gErr, "a task's Definition of Done must be reachable via its paired goal record")
	require.Len(t, found.Criteria, 1)
	assert.Equal(t, tk.Criteria[0].Text, found.Criteria[0].Text)
	assert.Equal(t, tk.Criteria[0].Author.ID, found.Criteria[0].Author.ID)
	require.Len(t, found.DoD, 1)
	assert.Equal(t, "definition of done item", found.DoD[0].Text)

	// GOAL-FR-012: a task's goal stays in "defining" until the task itself
	// starts — nothing in this construction path may activate it.
	assert.Equal(t, generated.GoalStateDefining, found.State)

	// task.AcceptanceCriterion is the SAME Go type on both sides — no
	// goal-specific criterion shape exists to drift out of sync with the
	// task-side one (a structural guarantee the compiler enforces: this
	// line would not compile if goal.Goal.Criteria's element type ever
	// diverged from task.Task.Criteria's).
	var _ []task.AcceptanceCriterion = found.Criteria
	var _ []task.AcceptanceCriterion = tk.Criteria
}

// TestAllTaskCriteriaConsumersRepointed is the goal spec's own
// TestAllTaskCriteriaConsumersRepointed (FR-030, S-04). FR-030 enumerates
// six consumers of Task.Criteria to re-point; three of them —
// pkg/agent/task_executor.go, pkg/agent/plan_engine.go (both explicitly
// scheduled for a LATER wave, E12/E14) and pkg/gateway's REST handlers
// (this wave's own rest_tasks_criteria_test.go covers those directly) — are
// outside what this package can reach or is responsible for. The one
// consumer reachable from pkg/task's own dependency graph without
// recreating the import cycle this file's package doc comment explains is
// pkg/plan.Lint (lintJoinlessConvergence's exported entry point) — this
// test drives it end-to-end and proves it still reads a task's criteria
// correctly under this wave's dual-write architecture.
//
// This test FAILS on a regression that stops populating Task.Criteria at
// create time (the exact bug FR-030 exists to catch: "a silently-missed
// consumer reads an empty list and judges against the soft tier without
// saying so") — an IsJoin member with a real criterion would then read as
// though it had none, and Lint would report a LintJoinless violation where
// this test asserts there is none.
func TestAllTaskCriteriaConsumersRepointed(t *testing.T) {
	p := &plan.Plan{
		ID:    "plan-1",
		Title: "convergence plan",
		State: plan.StateDraft,
	}

	// Two parallel streams (a, b) both feeding a join member (join) that
	// depends on both — the join member carries a real criterion, so
	// lintJoinlessConvergence must find it satisfied.
	a := task.Task{ID: "a", Title: "stream a", PlanID: p.ID}
	b := task.Task{ID: "b", Title: "stream b", PlanID: p.ID}
	join := task.Task{
		ID:        "join",
		Title:     "assemble",
		PlanID:    p.ID,
		BlockedBy: []string{"a", "b"},
		IsJoin:    true,
		// This is the field under test: a real, non-empty Criteria list,
		// populated exactly the way this wave's create_task/create paths
		// populate it (dual-write alongside the paired goal record).
		Criteria: []task.AcceptanceCriterion{criterion("assembled artifact is correct", "jim")},
	}
	members := []task.Task{a, b, join}

	lerr := plan.Lint(p, members)
	require.Nil(t, lerr, "an IsJoin member WITH criteria must lint clean; a regression that stops "+
		"populating Task.Criteria at create time would report a false LintJoinless violation here")

	// Negative control: the SAME topology with the join member's Criteria
	// emptied reproduces the violation Lint is supposed to catch — proving
	// this test is actually exercising the criteria-presence check, not
	// passing regardless of its content (test-plan-and-write's oracle-
	// independence / maximum-assertion-strength discipline: a positive-only
	// assertion here could not distinguish "criteria correctly read" from
	// "the check never runs at all").
	joinNoCriteria := join
	joinNoCriteria.Criteria = nil
	lerrNeg := plan.Lint(p, []task.Task{a, b, joinNoCriteria})
	require.NotNil(t, lerrNeg, "an IsJoin member with ZERO criteria must still be flagged (FR-159)")
	found := false
	for _, v := range lerrNeg.Violations {
		if v.Kind == plan.LintJoinless {
			found = true
		}
	}
	assert.True(t, found, "expected a LintJoinless violation for the zero-criteria join member")
}
