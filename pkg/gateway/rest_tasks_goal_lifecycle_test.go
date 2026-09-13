// rest_tasks_goal_lifecycle_test.go — three task/goal lifecycle defects found
// by UAT against a live gateway, all of the same shape this delivery keeps
// producing: a rule stated at the door and never actually enforced, or a
// record written twice from two sources that disagree.
//
//   - A: the distinctness half of the Definition-of-Done rule (GOAL-FR-021/
//     FR-047/FR-048, operator decision D-C) was advertised in the refusal
//     message and never checked. A tester pasted the same sentence into both
//     boxes and the task saved.
//   - B: task creation minted one set of criterion ids and the paired goal
//     record minted a SECOND set for the same text, so the same criterion read
//     `met` on the task and `pending` on its goal.
//   - C: GOAL-FR-044 ("a goal MUST NOT outlive its owner as an unreferenced
//     record") had no implementation on the delete path at all.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// uatDuplicateSentence is the exact text the UAT tester pasted into BOTH the
// acceptance-criteria box and the Definition-of-Done box on task
// 2baecb84-17fe-41b1-a243-ec9722022abb, which saved with HTTP 200.
const uatDuplicateSentence = "Running the script prints the exact line: Hello, UAT-T2"

// deleteTaskRec DELETEs /api/v1/tasks/{id} and returns the recorder.
func deleteTaskRec(t *testing.T, api *restAPI, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+id, nil)
	r.URL.Path = "/api/v1/tasks/" + id
	api.HandleTasks(w, r)
	return w
}

// oneCriterionJSON renders a one-item criteria/dod array carrying text.
func oneCriterionJSON(text string) string {
	return `[{"text":` + jsonQuote(text) + `,"author":{"kind":"user","id":"tester"},"status":"pending"}]`
}

// TestCreateTaskRefusesDoDIdenticalToCriteria_GOALFR021 is DEFECT A.
//
// The refusal the API already emits when `dod` is missing promises the rule:
// "a task must have at least one definition-of-done item, DISTINCT FROM its
// acceptance criteria". Only the count half was implemented. A Definition of
// Done that restates an acceptance criterion adds nothing to the judged
// contract — it is scored twice against the same sentence — so accepting it
// makes the product's own stated rule a lie.
func TestCreateTaskRefusesDoDIdenticalToCriteria_GOALFR021(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	w := postTaskJSON(t, api, `{"title":"UAT-T2","action":"llm","workspace_id":"`+wsID+`",`+
		`"criteria":`+oneCriterionJSON(uatDuplicateSentence)+`,`+
		`"dod":`+oneCriterionJSON(uatDuplicateSentence)+`}`)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"a byte-identical DoD item and acceptance criterion must be refused (GOAL-FR-021/FR-047/D-C); body=%s",
		w.Body.String())
	assert.Contains(t, w.Body.String(), "distinct",
		"the refusal must say WHY, in the same words the rule is advertised in")
	assert.Contains(t, w.Body.String(), uatDuplicateSentence,
		"the refusal must name the offending item so the author can fix it")
}

// TestCreateTaskRefusesDoDDifferingOnlyByCaseAndSpacing_GOALFR021 pins the
// chosen distinctness rule at its exact boundary: comparison is on the item's
// text with surrounding and repeated whitespace collapsed and case folded, and
// NOTHING more. A copy-paste that picked up a trailing space or a different
// capitalisation is the same defect wearing a hat.
func TestCreateTaskRefusesDoDDifferingOnlyByCaseAndSpacing_GOALFR021(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	w := postTaskJSON(t, api, `{"title":"UAT-T2","action":"llm","workspace_id":"`+wsID+`",`+
		`"criteria":`+oneCriterionJSON(uatDuplicateSentence)+`,`+
		`"dod":`+oneCriterionJSON("  running the SCRIPT prints   the exact line: Hello, UAT-T2 ")+`}`)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"whitespace and case must not be enough to make a DoD item distinct; body=%s", w.Body.String())
}

// TestCreateTaskAcceptsAGenuinelyDifferentDoD_GOALFR021 is the other half of
// the boundary, and it is what stops the rule from over-reaching: a DoD item
// that is a near-paraphrase of a criterion at a different altitude is a
// LEGITIMATE Definition of Done and must still save.
func TestCreateTaskAcceptsAGenuinelyDifferentDoD_GOALFR021(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	w := postTaskJSON(t, api, `{"title":"UAT-T2","action":"llm","workspace_id":"`+wsID+`",`+
		`"criteria":`+oneCriterionJSON(uatDuplicateSentence)+`,`+
		`"dod":`+oneCriterionJSON("the script is committed and prints the greeting line")+`}`)

	require.Equal(t, http.StatusCreated, w.Code,
		"a genuinely different DoD item must still be accepted; body=%s", w.Body.String())
}

// TestPatchTaskRefusesDoDIdenticalToCriteria_GOALFR048 closes the edit-time
// bypass. GOAL-FR-048 binds the rule at save, not only at create: a task
// created with a distinct DoD that is then edited into a duplicate ends up in
// exactly the state Defect A describes, by a different door.
func TestPatchTaskRefusesDoDIdenticalToCriteria_GOALFR048(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	id := createTaskWithGoal(t, api, wsID, "editable task")

	// Only `dod` is supplied — the criteria it must stay distinct from are the
	// ones already on the task record ("the work is done", validCriteriaJSON).
	w := patchTaskJSON(t, api, id, `{"dod":`+oneCriterionJSON("The Work Is Done")+`}`)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"an edit that makes the DoD restate an existing criterion must be refused; body=%s", w.Body.String())

	// Nothing may have landed: the task's stored DoD must be untouched.
	g, gErr := tools.GoalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, id)
	require.NoError(t, gErr)
	require.Len(t, g.DoD, 1)
	assert.Equal(t, "no secrets in the output", g.DoD[0].Text,
		"a refused edit must leave the previous Definition of Done in place")
}

// TestPatchWithUnreadableGoalDoDRefusesBeforeWriting covers the failure mode
// the distinctness rule introduces on the criteria-only edit path: the rule
// needs the DoD already on file, and that read can fail.
//
// Two things must hold, and the second is the one worth the test. It must fail
// VISIBLY (500, not a silently skipped rule and a cheerful 200) — a rule that
// quietly does not run is the entire defect class this change closes. And it
// must fail BEFORE the write, leaving the task exactly as it was, rather than
// the older shape where the task record was updated and only then did the goal
// write fail, so the caller was told "no" about a change that had half landed.
func TestPatchWithUnreadableGoalDoDRefusesBeforeWriting(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	id := createTaskWithGoal(t, api, wsID, "unreadable dod on edit")

	before, err := api.taskStore.Get(id)
	require.NoError(t, err)
	require.Len(t, before.Criteria, 1)

	// Corrupt the paired goal record so every read of it fails with something
	// that is NOT goal.ErrOwnerNotFound.
	duplicateGoalRecordForOwner(t, api, id)

	w := patchTaskJSON(t, api, id, `{"criteria":`+oneCriterionJSON("an edited criterion")+`}`)
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"an edit whose distinctness rule could not be evaluated must fail visibly, never quietly "+
			"skip the rule: body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "Definition of Done",
		"the 500 must name what could not be read: body=%s", w.Body.String())

	after, err := api.taskStore.Get(id)
	require.NoError(t, err)
	require.Len(t, after.Criteria, 1)
	assert.Equal(t, before.Criteria[0].Text, after.Criteria[0].Text,
		"a refused edit must leave the task untouched — the check runs before the store write")
}

// TestTaskAndGoalShareCriterionIDs_GOALFR007 is DEFECT B.
//
// A criterion's id is the join key between the two records the judge writes
// back to (GOAL-FR-007/FR-041 de-unions the Judge's result "by criterion id").
// createTask minted ids by handing the criteria to task.Store.Create, then
// handed the PRE-normalisation slice to syncTaskGoalRecord, which minted a
// SECOND set for the same text. The projection then had no id to match on:
// the same criterion read `met` on the task and `pending` on its goal record —
// two contradictory answers to "was this met".
func TestTaskAndGoalShareCriterionIDs_GOALFR007(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	id := createTaskWithGoal(t, api, wsID, "id-parity task")

	stored, err := api.taskStore.Get(id)
	require.NoError(t, err)
	require.Len(t, stored.Criteria, 1)
	require.NotEmpty(t, stored.Criteria[0].ID, "the task's criterion must carry a minted id")

	g, err := tools.GoalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, id)
	require.NoError(t, err)
	require.Len(t, g.Criteria, 1)

	assert.Equal(t, stored.Criteria[0].ID, g.Criteria[0].ID,
		"the task and its paired goal record must carry the SAME id for the same criterion text — "+
			"the id is the join key the verdict projection matches on (GOAL-FR-007/FR-041)")
}

// TestPatchTaskKeepsCriterionIDsInSync_GOALFR007 is the edit-time half of
// Defect B: the PATCH path had the identical two-normalisations shape.
func TestPatchTaskKeepsCriterionIDsInSync_GOALFR007(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	id := createTaskWithGoal(t, api, wsID, "id-parity edit")

	w := patchTaskJSON(t, api, id, `{"criteria":`+oneCriterionJSON("the replacement criterion holds")+`}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	stored, err := api.taskStore.Get(id)
	require.NoError(t, err)
	require.Len(t, stored.Criteria, 1)

	g, err := tools.GoalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, id)
	require.NoError(t, err)
	require.Len(t, g.Criteria, 1)

	assert.Equal(t, stored.Criteria[0].ID, g.Criteria[0].ID,
		"an edited criterion must carry one id across both records, not two")
}

// TestDeletingTaskRemovesItsGoalRecord_GOALFR044 is DEFECT C.
//
// GOAL-FR-044: "A goal MUST NOT outlive its owner as an unreferenced record.
// Deleting a task MUST transition and remove its goal." Nothing on any of the
// three delete surfaces did either, so every deleted task left a goal record
// behind, permanently `defining` or `active`, owned by a task id that no
// longer resolves.
func TestDeletingTaskRemovesItsGoalRecord_GOALFR044(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	id := createTaskWithGoal(t, api, wsID, "doomed task")

	gs := tools.GoalStoreForTasks(api.taskStore)
	_, err := gs.GetByOwner(gen.GoalOwnerKindTask, id)
	require.NoError(t, err, "fixture: the task must have a paired goal record before the delete")

	w := deleteTaskRec(t, api, id)
	require.Equal(t, http.StatusNoContent, w.Code, "body=%s", w.Body.String())

	_, err = gs.GetByOwner(gen.GoalOwnerKindTask, id)
	require.ErrorIs(t, err, goal.ErrOwnerNotFound,
		"the deleted task's goal record must not outlive it (GOAL-FR-044/EC-4)")

	all, skipped, lErr := gs.List()
	require.NoError(t, lErr)
	assert.Empty(t, skipped)
	assert.Empty(t, all, "no goal record may survive the deletion of its only owner")
}

// TestDeletingAnActiveTaskRemovesItsGoalRecord_GOALFR044 is the same
// requirement for a goal the task actually STARTED. An active record is the
// one a keeper loop may still be holding, and it is the state the UAT instance
// left three goals stranded in.
func TestDeletingAnActiveTaskRemovesItsGoalRecord_GOALFR044(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	id := createTaskWithGoal(t, api, wsID, "running task")

	gs := tools.GoalStoreForTasks(api.taskStore)
	rec, err := gs.GetByOwner(gen.GoalOwnerKindTask, id)
	require.NoError(t, err)

	// Drive the record into the active phase, as starting the task would.
	_, err = gs.Update(rec.GoalID, func(g *goal.Goal) error {
		return g.Activate("session-under-test", time.Now().UTC())
	})
	require.NoError(t, err)

	w := deleteTaskRec(t, api, id)
	require.Equal(t, http.StatusNoContent, w.Code, "body=%s", w.Body.String())

	_, err = gs.GetByOwner(gen.GoalOwnerKindTask, id)
	require.ErrorIs(t, err, goal.ErrOwnerNotFound,
		"an active goal must not outlive the deletion of its owning task (GOAL-FR-044/EC-4)")
}

// The pure-function half of FR-044 ("transition AND remove", not only remove)
// is covered where the one implementation now lives:
// pkg/tools/task_goal_lifecycle_test.go's
// TestTerminateGoalForOwnerDeletionTransitionsOnlyActiveRecords. This file used
// to carry a byte-identical copy of that test against a byte-identical private
// copy of the function; both are deleted. The REST-level behaviour — that a
// DELETE actually removes the record — stays covered by the handler test above.
