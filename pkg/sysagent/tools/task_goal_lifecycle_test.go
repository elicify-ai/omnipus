// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// task_goal_lifecycle_test.go — the cross-workspace-tool half of three
// task/goal lifecycle defects found by UAT against the REST surface. All three
// exist identically here, because create_task_in_workspace /
// update_task_in_workspace / delete_task_in_workspace MIRROR the REST handlers
// and the plain task tools rather than share them — which is exactly why a fix
// applied to one surface has repeatedly left the other two broken.
//
//   - A: the DISTINCTNESS half of the Definition-of-Done rule (GOAL-FR-021/
//     FR-047/FR-048, operator decision D-C) was advertised in this tool's own
//     refusal message and never checked.
//   - B: create minted one set of criterion ids on the task and a SECOND set on
//     the paired goal record.
//   - C: GOAL-FR-044 ("a goal MUST NOT outlive its owner as an unreferenced
//     record") had no implementation on the delete path.

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// uatDuplicateSentenceWorkspace is the exact text a UAT tester pasted into BOTH
// the acceptance-criteria box and the Definition-of-Done box; the task saved.
const uatDuplicateSentenceWorkspace = "Running the script prints the exact line: Hello, UAT-T2"

func workspaceProseArg(text string) []any {
	return []any{map[string]any{"kind": "prose", "text": text}}
}

// createWorkspaceTaskForGoalLifecycle creates one task through
// create_task_in_workspace and returns its id.
func createWorkspaceTaskForGoalLifecycle(
	t *testing.T, deps *systools.Deps, criteria, dod string,
) string {
	t.Helper()
	res := systools.NewTaskCreateTool(deps).Execute(
		tools.WithAgentID(context.Background(), "jim"),
		map[string]any{
			"name":         "lifecycle",
			"workspace_id": testWorkspaceID,
			"agent_id":     "worker-agent",
			"criteria":     workspaceProseArg(criteria),
			"dod":          workspaceProseArg(dod),
		})
	require.False(t, res.IsError, "create_task_in_workspace must succeed: %s", res.ForLLM)
	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out))
	require.NotEmpty(t, out.ID)
	return out.ID
}

// TestCreateTaskInWorkspaceRefusesDoDIdenticalToCriteria_GOALFR021 is DEFECT A.
func TestCreateTaskInWorkspaceRefusesDoDIdenticalToCriteria_GOALFR021(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)

	res := systools.NewTaskCreateTool(deps).Execute(
		tools.WithAgentID(context.Background(), "jim"),
		map[string]any{
			"name":         "UAT-T2",
			"workspace_id": testWorkspaceID,
			"agent_id":     "worker-agent",
			"criteria":     workspaceProseArg(uatDuplicateSentenceWorkspace),
			"dod":          workspaceProseArg(uatDuplicateSentenceWorkspace),
		})

	require.True(t, res.IsError,
		"a byte-identical DoD item and acceptance criterion must be refused (GOAL-FR-021/D-C): %s",
		res.ForLLM)
	assert.Contains(t, res.ForLLM, "distinct",
		"the refusal must state the rule in the words it is advertised in")

	rows, err := task.New(home + "/tasks").List(task.Filter{WorkspaceID: testWorkspaceID})
	require.NoError(t, err)
	assert.Empty(t, rows, "a refused create must not persist a task")
}

// TestUpdateTaskInWorkspaceRefusesDoDIdenticalToCriteria_GOALFR048 closes the
// edit-time bypass: GOAL-FR-048 binds the rule at save, not only at create.
func TestUpdateTaskInWorkspaceRefusesDoDIdenticalToCriteria_GOALFR048(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	id := createWorkspaceTaskForGoalLifecycle(t, deps, "the work is done", "the reviewer signed it off")

	res := systools.NewTaskUpdateTool(deps).Execute(
		tools.WithAgentID(context.Background(), "jim"),
		map[string]any{"id": id, "dod": workspaceProseArg("The Work Is Done")})

	require.True(t, res.IsError,
		"an edit that makes the DoD restate an existing criterion must be refused: %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "distinct")

	g, err := goal.NewStore(home).GetByOwner(generated.GoalOwnerKindTask, id)
	require.NoError(t, err)
	require.Len(t, g.DoD, 1)
	assert.Equal(t, "the reviewer signed it off", g.DoD[0].Text,
		"a refused edit must leave the previous Definition of Done in place")
}

// TestCreateTaskInWorkspaceSharesCriterionIDsWithItsGoal_GOALFR007 is DEFECT B.
// The criterion id is the join key the verdict projection de-unions the Judge's
// result on (GOAL-FR-007/FR-041).
func TestCreateTaskInWorkspaceSharesCriterionIDsWithItsGoal_GOALFR007(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	id := createWorkspaceTaskForGoalLifecycle(t, deps, "the work is done", "the reviewer signed it off")

	stored, err := task.New(home + "/tasks").Get(id)
	require.NoError(t, err)
	require.Len(t, stored.Criteria, 1)
	require.NotEmpty(t, stored.Criteria[0].ID)

	g, err := goal.NewStore(home).GetByOwner(generated.GoalOwnerKindTask, id)
	require.NoError(t, err)
	require.Len(t, g.Criteria, 1)

	assert.Equal(t, stored.Criteria[0].ID, g.Criteria[0].ID,
		"the task and its paired goal record must carry the SAME id for the same criterion")
}

// TestDeleteTaskInWorkspaceRemovesItsGoalRecord_GOALFR044 is DEFECT C.
func TestDeleteTaskInWorkspaceRemovesItsGoalRecord_GOALFR044(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	id := createWorkspaceTaskForGoalLifecycle(t, deps, "the work is done", "the reviewer signed it off")

	gs := goal.NewStore(home)
	_, err := gs.GetByOwner(generated.GoalOwnerKindTask, id)
	require.NoError(t, err, "fixture: the task must have a paired goal record before the delete")

	res := systools.NewTaskDeleteTool(deps).Execute(
		tools.WithAgentID(context.Background(), "jim"),
		map[string]any{"id": id, "confirm": true})
	require.False(t, res.IsError, "delete_task_in_workspace must succeed: %s", res.ForLLM)

	_, err = gs.GetByOwner(generated.GoalOwnerKindTask, id)
	require.ErrorIs(t, err, goal.ErrOwnerNotFound,
		"the deleted task's goal record must not outlive it (GOAL-FR-044/EC-4)")

	all, skipped, lErr := gs.List()
	require.NoError(t, lErr)
	assert.Empty(t, skipped)
	assert.Empty(t, all, "no goal record may survive the deletion of its only owner")
}

// TestDeleteTaskInWorkspace_GoalCleanupFailure_RefusesTheDeleteEntirely is the
// System Agent half of the ONE answer all three task-delete surfaces now give
// when the paired goal record cannot be removed (GOAL-FR-044/EC-4).
//
// delete_task_in_workspace used to remove the task and then surface a
// `goal_cleanup_warning` alongside a successful delete. The orphaned record was
// permanent. tools.RemoveTaskGoalRecords now runs before the task file is
// removed and a failure refuses the delete outright.
func TestDeleteTaskInWorkspace_GoalCleanupFailure_RefusesTheDeleteEntirely(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	id := createWorkspaceTaskForGoalLifecycle(t, deps, "the work is done", "the reviewer signed it off")

	gs := goal.NewStore(home)
	_, err := gs.GetByOwner(generated.GoalOwnerKindTask, id)
	require.NoError(t, err, "fixture: the task must have a paired goal record before the delete")

	dir := gs.Dir()
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() {
		if cErr := os.Chmod(dir, 0o700); cErr != nil {
			t.Logf("restore goal dir permissions: %v", cErr)
		}
	})
	if _, rErr := os.ReadDir(dir); rErr == nil {
		t.Skip("goal entity directory is still readable with mode 0000 (running as root?) — " +
			"this test cannot create the storage fault it is about")
	}

	res := systools.NewTaskDeleteTool(deps).Execute(
		tools.WithAgentID(context.Background(), "jim"),
		map[string]any{"id": id, "confirm": true})
	require.True(t, res.IsError,
		"a delete that cannot remove the paired goal record must fail, not report success with a "+
			"warning field: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "goal record", "the refusal must say what actually failed")

	require.NoError(t, os.Chmod(dir, 0o700))
	stored, gErr := task.New(home + "/tasks").Get(id)
	require.NoError(t, gErr, "the task must survive a refused delete — otherwise the refusal is a lie")
	require.Equal(t, id, stored.ID)
}
