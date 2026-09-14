package tools

// task_goal_lifecycle_test.go — the agent-tool half of three task/goal
// lifecycle defects found by UAT against the REST surface. All three exist
// identically on this surface, because create_task / update_task / delete_task
// mirror the REST handlers rather than share them:
//
//   - A: the DISTINCTNESS half of the Definition-of-Done rule (GOAL-FR-021/
//     FR-047/FR-048, operator decision D-C) was advertised in this file's own
//     refusal message ("distinct from its acceptance criteria") and never
//     checked.
//   - B: create_task minted one set of criterion ids on the task and a SECOND
//     set on the paired goal record, so the same criterion could read `met` on
//     one and `pending` on the other.
//   - C: GOAL-FR-044 ("a goal MUST NOT outlive its owner as an unreferenced
//     record") had no implementation on the delete path.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// uatDuplicateSentence is the exact text a UAT tester pasted into BOTH the
// acceptance-criteria box and the Definition-of-Done box; the task saved.
const uatDuplicateSentence = "Running the script prints the exact line: Hello, UAT-T2"

// newGoalLifecycleTools builds the three task tools over one isolated home, so
// GoalStoreForTasks (which derives the goal root from the task store's PARENT
// directory) lands inside the test's own temp dir rather than beside it.
// goalLifecycleTools is the struct form of newGoalLifecycleTools. It exists so
// a test that needs only two of the five values can name those two instead of
// spelling three blank identifiers — which golangci-lint's dogsled linter
// rejects (it fired on this file and failed the lint gate).
type goalLifecycleTools struct {
	create    *TaskCreateTool
	update    *TaskUpdateTool
	del       *TaskDeleteTool
	taskStore *task.Store
	goalStore *goal.Store
}

func newGoalLifecycleTools2(t *testing.T) goalLifecycleTools {
	t.Helper()
	create, update, del, taskStore, goalStore := newGoalLifecycleTools(t)
	return goalLifecycleTools{create: create, update: update, del: del, taskStore: taskStore, goalStore: goalStore}
}

func newGoalLifecycleTools(t *testing.T) (*TaskCreateTool, *TaskUpdateTool, *TaskDeleteTool, *task.Store, *goal.Store) {
	t.Helper()
	home := t.TempDir()
	store := task.New(filepath.Join(home, "tasks"))
	create := NewTaskCreateTool(store)
	create.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
	create.SetBashPolicyChecker(func(string) (string, bool) { return "allow", true })
	update := NewTaskUpdateTool(store)
	update.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
	return create, update, NewTaskDeleteTool(store), store, goal.NewStore(home)
}

func goalLifecycleCtx() context.Context {
	return WithWorkspaceID(WithAgentID(context.Background(), "caller"), "ws-goal-lifecycle")
}

func proseArg(text string) []any {
	return []any{map[string]any{"kind": "prose", "text": text}}
}

// createGoalLifecycleTask creates one task through create_task and returns its
// id, failing the test if the create was refused.
func createGoalLifecycleTask(t *testing.T, tool *TaskCreateTool, store *task.Store, criteria, dod string) string {
	t.Helper()
	res := tool.Execute(goalLifecycleCtx(), map[string]any{
		"title":    "lifecycle",
		"prompt":   "do it",
		"agent_id": "agent-b",
		"criteria": proseArg(criteria),
		"dod":      proseArg(dod),
	})
	require.False(t, res.IsError, "create_task must succeed: %s", res.ForLLM)
	all, err := store.List(task.Filter{WorkspaceID: "ws-goal-lifecycle"})
	require.NoError(t, err)
	require.Len(t, all, 1)
	return all[0].ID
}

// TestTaskCreateRefusesDoDIdenticalToCriteria_GOALFR021 is DEFECT A on the tool
// surface. The tool's own refusal message promises the rule; only the count
// half was implemented.
func TestTaskCreateRefusesDoDIdenticalToCriteria_GOALFR021(t *testing.T) {
	t.Parallel()
	tools := newGoalLifecycleTools2(t)
	create, store := tools.create, tools.taskStore

	res := create.Execute(goalLifecycleCtx(), map[string]any{
		"title":    "UAT-T2",
		"prompt":   "do it",
		"agent_id": "agent-b",
		"criteria": proseArg(uatDuplicateSentence),
		"dod":      proseArg(uatDuplicateSentence),
	})

	require.True(t, res.IsError,
		"a byte-identical DoD item and acceptance criterion must be refused (GOAL-FR-021/D-C): %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "distinct",
		"the refusal must state the rule in the words it is advertised in")
	assert.Contains(t, res.ForLLM, uatDuplicateSentence,
		"the refusal must name the offending item so the agent can fix it rather than retry blindly")

	all, err := store.List(task.Filter{WorkspaceID: "ws-goal-lifecycle"})
	require.NoError(t, err)
	assert.Empty(t, all, "a refused create must not persist a task")
}

// TestTaskCreateAcceptsAGenuinelyDifferentDoD_GOALFR021 is the boundary on the
// other side: the rule must not refuse a legitimate near-paraphrase.
func TestTaskCreateAcceptsAGenuinelyDifferentDoD_GOALFR021(t *testing.T) {
	t.Parallel()
	tools := newGoalLifecycleTools2(t)
	create, store := tools.create, tools.taskStore
	createGoalLifecycleTask(t, create, store, uatDuplicateSentence,
		"the script is committed and prints the greeting line")
}

// TestTaskUpdateRefusesDoDIdenticalToCriteria_GOALFR048 closes the edit-time
// bypass: GOAL-FR-048 binds the rule at save, not only at create.
func TestTaskUpdateRefusesDoDIdenticalToCriteria_GOALFR048(t *testing.T) {
	t.Parallel()
	create, update, _, store, gs := newGoalLifecycleTools(t)
	id := createGoalLifecycleTask(t, create, store, "the work is done", "the reviewer signed it off")

	// Only `dod` is supplied — it must stay distinct from the criteria already
	// on the task record.
	res := update.Execute(goalLifecycleCtx(), map[string]any{
		"task_id": id,
		"dod":     proseArg("The Work Is Done"),
	})
	require.True(t, res.IsError,
		"an edit that makes the DoD restate an existing criterion must be refused: %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "distinct")

	g, err := gs.GetByOwner(generated.GoalOwnerKindTask, id)
	require.NoError(t, err)
	require.Len(t, g.DoD, 1)
	assert.Equal(t, "the reviewer signed it off", g.DoD[0].Text,
		"a refused edit must leave the previous Definition of Done in place")
}

// TestTaskUpdateRefusesCriteriaThatRestateThePersistedDoD_GOALFR048 is the
// mirror-image edit: replacing `criteria` alone must be checked against the DoD
// already on the paired goal record, which is the only place a task's DoD
// exists (ADR-086 D5). Without the goal-record read, this edit is the way
// around the rule.
func TestTaskUpdateRefusesCriteriaThatRestateThePersistedDoD_GOALFR048(t *testing.T) {
	t.Parallel()
	create, update, _, store, _ := newGoalLifecycleTools(t)
	id := createGoalLifecycleTask(t, create, store, "the work is done", "the reviewer signed it off")

	res := update.Execute(goalLifecycleCtx(), map[string]any{
		"task_id":  id,
		"criteria": proseArg("  The Reviewer   Signed It Off "),
	})
	require.True(t, res.IsError,
		"new criteria that restate the persisted DoD must be refused: %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "distinct")

	stored, err := store.Get(id)
	require.NoError(t, err)
	require.Len(t, stored.Criteria, 1)
	assert.Equal(t, "the work is done", stored.Criteria[0].Text,
		"a refused edit must not have written the new criteria to the task either")
}

// TestTaskCreateSharesCriterionIDsWithItsGoalRecord_GOALFR007 is DEFECT B.
// The criterion id is the join key the verdict projection de-unions the
// Judge's result on (GOAL-FR-007/FR-041); two sets of ids for one criterion
// means the task and its goal give contradictory answers to "was this met".
func TestTaskCreateSharesCriterionIDsWithItsGoalRecord_GOALFR007(t *testing.T) {
	t.Parallel()
	create, _, _, store, gs := newGoalLifecycleTools(t)
	id := createGoalLifecycleTask(t, create, store, "the work is done", "the reviewer signed it off")

	stored, err := store.Get(id)
	require.NoError(t, err)
	require.Len(t, stored.Criteria, 1)
	require.NotEmpty(t, stored.Criteria[0].ID)

	g, err := gs.GetByOwner(generated.GoalOwnerKindTask, id)
	require.NoError(t, err)
	require.Len(t, g.Criteria, 1)

	assert.Equal(t, stored.Criteria[0].ID, g.Criteria[0].ID,
		"the task and its paired goal record must carry the SAME id for the same criterion")
}

// TestTaskUpdateKeepsCriterionIDsInSync_GOALFR007 is the edit-time half of
// Defect B.
func TestTaskUpdateKeepsCriterionIDsInSync_GOALFR007(t *testing.T) {
	t.Parallel()
	create, update, _, store, gs := newGoalLifecycleTools(t)
	id := createGoalLifecycleTask(t, create, store, "the work is done", "the reviewer signed it off")

	res := update.Execute(goalLifecycleCtx(), map[string]any{
		"task_id":  id,
		"criteria": proseArg("the replacement criterion holds"),
	})
	require.False(t, res.IsError, "update_task must succeed: %s", res.ForLLM)

	stored, err := store.Get(id)
	require.NoError(t, err)
	require.Len(t, stored.Criteria, 1)

	g, err := gs.GetByOwner(generated.GoalOwnerKindTask, id)
	require.NoError(t, err)
	require.Len(t, g.Criteria, 1)

	assert.Equal(t, stored.Criteria[0].ID, g.Criteria[0].ID,
		"an edited criterion must carry one id across both records, not two")
}

// TestDeleteTaskRemovesItsGoalRecord_GOALFR044 is DEFECT C on the tool surface.
func TestDeleteTaskRemovesItsGoalRecord_GOALFR044(t *testing.T) {
	t.Parallel()
	create, _, del, store, gs := newGoalLifecycleTools(t)
	id := createGoalLifecycleTask(t, create, store, "the work is done", "the reviewer signed it off")

	_, err := gs.GetByOwner(generated.GoalOwnerKindTask, id)
	require.NoError(t, err, "fixture: the task must have a paired goal record before the delete")

	res := del.Execute(goalLifecycleCtx(), map[string]any{"task_id": id})
	require.False(t, res.IsError, "delete_task must succeed: %s", res.ForLLM)

	_, err = gs.GetByOwner(generated.GoalOwnerKindTask, id)
	require.ErrorIs(t, err, goal.ErrOwnerNotFound,
		"the deleted task's goal record must not outlive it (GOAL-FR-044/EC-4)")

	all, skipped, lErr := gs.List()
	require.NoError(t, lErr)
	assert.Empty(t, skipped)
	assert.Empty(t, all, "no goal record may survive the deletion of its only owner")
}

// TestTerminateGoalForOwnerDeletionTransitionsOnlyActiveRecords pins the
// "transition AND remove" half of FR-044 on the pure function the delete path
// runs inside its store mutation: an ACTIVE record ends with the explicit-clear
// vocabulary FR-028 reserves for an operator ending, and a `defining` record —
// a task deleted before it ever ran — is left alone rather than given a
// fabricated terminal verdict.
func TestTerminateGoalForOwnerDeletionTransitionsOnlyActiveRecords(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	author := task.CriterionAuthor{Kind: "user", ID: "tester"}
	crit := []task.AcceptanceCriterion{{Text: "the work is done", Status: task.CritPending, Author: author}}
	dod := []task.AcceptanceCriterion{{Text: "the reviewer signed it off", Status: task.CritPending, Author: author}}

	active, err := goal.New(generated.GoalOwnerKindTask, "task-1", generated.GoalSourceTaskExplicit,
		"do it", "", crit, dod, 3, now)
	require.NoError(t, err)
	require.NoError(t, active.Activate("session-1", now))

	require.NoError(t, terminateGoalForOwnerDeletion(active, now))
	assert.Equal(t, generated.GoalStateCleared, active.State)
	assert.Equal(t, goalTerminalReasonOwnerDeleted, active.TerminalReason)

	defining, err := goal.New(generated.GoalOwnerKindTask, "task-2", generated.GoalSourceTaskExplicit,
		"do it", "", crit, dod, 3, now)
	require.NoError(t, err)
	require.NoError(t, terminateGoalForOwnerDeletion(defining, now),
		"a never-started goal is not an error to delete")
	assert.Equal(t, generated.GoalStateDefining, defining.State,
		"a goal whose task never ran must not be given a fabricated terminal verdict")
}

// TestTaskDelete_GoalCleanupFailure_RefusesTheDeleteEntirely is the agent-tool
// half of the ONE answer all three task-delete surfaces now give when the
// paired goal record cannot be removed (GOAL-FR-044/EC-4).
//
// delete_task used to remove the task and THEN attempt the cleanup, reporting
// a `goal_cleanup_warning` alongside a successful delete when that attempt
// failed. The record was then a permanent orphan — active, owned by an id that
// no longer resolves — and nothing could undo it. RemoveTaskGoalRecords now
// runs first and a failure refuses the delete, so the orphan is prevented
// rather than reported and the caller has somewhere to go.
func TestTaskDelete_GoalCleanupFailure_RefusesTheDeleteEntirely(t *testing.T) {
	lt := newGoalLifecycleTools2(t)
	id := createGoalLifecycleTask(t, lt.create, lt.taskStore, "the work is done", "no secrets in the output")

	_, err := lt.goalStore.GetByOwner(generated.GoalOwnerKindTask, id)
	require.NoError(t, err, "fixture: the task must have a paired goal record before the delete")

	dir := lt.goalStore.Dir()
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

	res := lt.del.Execute(goalLifecycleCtx(), map[string]any{"task_id": id})
	require.True(t, res.IsError,
		"a delete that cannot remove the paired goal record must fail, not report success with a "+
			"warning field: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "goal record",
		"the refusal must say what actually failed")

	stored, gErr := lt.taskStore.Get(id)
	require.NoError(t, gErr, "the task must survive a refused delete — otherwise the refusal is a lie")
	require.Equal(t, id, stored.ID)
}
