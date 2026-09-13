// rest_tasks_goal_visibility_test.go — review findings 8, SF-5 and SF-6, plus
// the stale-comment evidence drop in toWireJudgeVerdict.
//
// The shared defect shape across all four: a failure that the caller could not
// see. PATCH logged a lost Definition of Done and answered 200 with the task
// body; toWireTask answered 200 with `dod` simply absent, which is the same
// bytes a task with no DoD produces; toWireJudgeVerdict dropped four evidence
// fields the WS frame sends, so evidence vanished on page reload. In every
// case the caller was told the thing they asked for had worked.
//
// Traces to: ADR-086 (GOAL-FR-021/FR-029/FR-048), operator decision D-C
// (criteria + definition-of-done mandatory at creation AND edit), ADR-084
// revision 9 (JUDGE-FR-070a evidence reporting).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// postTaskJSON POSTs body to /api/v1/tasks and returns the recorder.
func postTaskJSON(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)
	return w
}

// patchTaskJSON PATCHes body to /api/v1/tasks/{id} and returns the recorder.
func patchTaskJSON(t *testing.T, api *restAPI, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks/" + id
	api.HandleTasks(w, r)
	return w
}

// getTaskRec GETs /api/v1/tasks/{id} and returns the recorder.
func getTaskRec(t *testing.T, api *restAPI, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id, nil)
	r.URL.Path = "/api/v1/tasks/" + id
	api.HandleTasks(w, r)
	return w
}

// duplicateGoalRecordForOwner copies the paired goal record of task ownerID to
// a SECOND file under a different goal id, leaving two records claiming the
// same task owner. goal.Store.GetByOwner then refuses to guess between them
// and returns a real error that is NOT goal.ErrOwnerNotFound — which is
// precisely the "the DoD exists but could not be read" state SF-6 is about,
// reachable through nothing but the filesystem (goal.Store.Create's own R-04
// check forbids reaching it through the API).
func duplicateGoalRecordForOwner(t *testing.T, api *restAPI, ownerID string) {
	t.Helper()
	gs := goalStoreForTasks(api.taskStore)
	orig, err := gs.GetByOwner(gen.GoalOwnerKindTask, ownerID)
	require.NoError(t, err, "fixture: the task must already have a paired goal record")

	raw, err := os.ReadFile(filepath.Join(gs.Dir(), orig.GoalID+".json"))
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))

	dupID := orig.GoalID + "-dup"
	doc["goal_id"] = dupID
	dup, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(gs.Dir(), dupID+".json"), dup, 0o600))

	// Confirm the fixture really did make the read fail — otherwise the
	// assertions below would pass for the wrong reason.
	_, err = gs.GetByOwner(gen.GoalOwnerKindTask, ownerID)
	require.Error(t, err, "fixture: the duplicate must make GetByOwner fail")
}

// createTaskWithGoal creates a task through the REST API (so it gets a paired
// goal record) and returns its id.
func createTaskWithGoal(t *testing.T, api *restAPI, wsID, title string) string {
	t.Helper()
	w := postTaskJSON(t, api, `{"title":`+jsonQuote(title)+`,"action":"llm","workspace_id":"`+wsID+`",`+
		`"criteria":`+validCriteriaJSON+`,"dod":`+validDoDJSON+`}`)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotEmpty(t, created.Id)
	return created.Id
}

// jsonQuote JSON-quotes s for embedding in a request-body literal.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestPatchTaskAddingDoDAloneFailsVisibly is review finding 8.
//
// A task created before D-C has no paired goal record. Editing it to ADD a
// Definition of Done sends `dod` with no `criteria` — exactly the shape
// syncTaskGoalRecord refuses (GOAL-FR-048: bootstrapping a record needs both
// lists). The PATCH handler used to swallow that refusal in an slog.Error and
// return 200 with the task body, so the API said "saved" and the next GET
// returned no dod at all. The create path 500s on the identical condition;
// the two disagreed.
//
// This asserts the visible failure AND that nothing was persisted — a caller
// must be able to read the status code as the truth about what landed.
func TestPatchTaskAddingDoDAloneFailsVisibly(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	// A pre-D-C task: written straight to the task store, so it has no paired
	// goal record. This is the only way to produce one — the REST create path
	// requires criteria and dod together.
	legacy := &task.Task{
		Title:       "pre-wave task",
		Action:      task.ActionLLM,
		WorkspaceID: wsID,
		Status:      task.StatusInbox,
	}
	require.NoError(t, api.taskStore.Create(legacy))

	w := patchTaskJSON(t, api, legacy.ID, `{"dod":`+validDoDJSON+`}`)

	require.NotEqual(t, http.StatusOK, w.Code,
		"a PATCH whose Definition of Done was NOT saved must not answer 200: body=%s", w.Body.String())
	require.Equal(t, http.StatusBadRequest, w.Code,
		"supplying only one of criteria/dod when bootstrapping a record is the caller's "+
			"to fix, so it is a 400: body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "BOTH together",
		"the error must tell the caller how to succeed (GOAL-FR-048)")

	// And nothing landed: a follow-up GET must not carry a dod.
	wGet := getTaskRec(t, api, legacy.ID)
	require.Equal(t, http.StatusOK, wGet.Code, "body=%s", wGet.Body.String())
	var fetched gen.Task
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &fetched))
	assert.Nil(t, fetched.Dod, "the refused PATCH must not have persisted a Definition of Done")
}

// TestPatchTaskBothListsBootstrapsRecord is the acceptance pairing for the
// rejection above (Binding Rule 4): the SAME pre-D-C task, edited with BOTH
// lists, succeeds and really does get a paired goal record. Without this, the
// fix above could be satisfied by refusing every PATCH.
func TestPatchTaskBothListsBootstrapsRecord(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	legacy := &task.Task{
		Title:       "pre-wave task, edited properly",
		Action:      task.ActionLLM,
		WorkspaceID: wsID,
		Status:      task.StatusInbox,
	}
	require.NoError(t, api.taskStore.Create(legacy))

	w := patchTaskJSON(t, api, legacy.ID,
		`{"criteria":`+validCriteriaJSON+`,"dod":`+validDoDJSON+`}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	g, err := goalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, legacy.ID)
	require.NoError(t, err, "a PATCH carrying both lists must bootstrap the paired goal record")
	require.Len(t, g.DoD, 1)
	assert.Equal(t, "no secrets in the output", g.DoD[0].Text)

	wGet := getTaskRec(t, api, legacy.ID)
	require.Equal(t, http.StatusOK, wGet.Code)
	var fetched gen.Task
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &fetched))
	require.NotNil(t, fetched.Dod, "the saved Definition of Done must read back")
	require.Len(t, *fetched.Dod, 1)
}

// TestPatchTaskGoalWriteFailureReturns500 is SF-5: a PATCH whose goal-record
// write FAILS for a storage reason must not answer 200 with the task body the
// user expected. The task's own record did update — that is exactly why the
// old code rationalised the 200 — but the criteria/dod the user submitted did
// not land anywhere, and only the status code can say so.
func TestPatchTaskGoalWriteFailureReturns500(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	id := createTaskWithGoal(t, api, wsID, "goal write fails")

	duplicateGoalRecordForOwner(t, api, id)

	newCriteria := `[{"text":"an edited criterion","author":{"kind":"user","id":"tester"},"status":"pending"}]`
	w := patchTaskJSON(t, api, id, `{"criteria":`+newCriteria+`}`)

	require.NotEqual(t, http.StatusOK, w.Code,
		"a PATCH whose criteria could not be persisted must not answer 200: body=%s", w.Body.String())
	require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "Definition of Done could not be persisted",
		"the 500 must say what was lost: body=%s", w.Body.String())
}

// TestGetTaskWithUnreadableGoalRecordReturns500 is SF-6. `dod` is omitempty,
// so a failed read of the paired goal record used to render byte-identically
// to a task that genuinely has no Definition of Done. With D-C making a DoD
// mandatory, a reader seeing an empty one concludes the task has none — the
// single most misleading thing this endpoint could say.
func TestGetTaskWithUnreadableGoalRecordReturns500(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	id := createTaskWithGoal(t, api, wsID, "unreadable dod")

	// Sanity: it reads fine before the fixture breaks it.
	wOK := getTaskRec(t, api, id)
	require.Equal(t, http.StatusOK, wOK.Code)
	var before gen.Task
	require.NoError(t, json.Unmarshal(wOK.Body.Bytes(), &before))
	require.NotNil(t, before.Dod)

	duplicateGoalRecordForOwner(t, api, id)

	w := getTaskRec(t, api, id)
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"a task whose Definition of Done could not be READ must not be served as one that "+
			"has none: body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "Definition of Done is unavailable",
		"the 500 must distinguish 'could not read' from 'has none': body=%s", w.Body.String())
}

// TestGetTaskWithNoGoalRecordStillReturns200 is the pairing that keeps the
// SF-6 fix honest: "no goal record at all" is goal.ErrOwnerNotFound, the
// normal state of a pre-D-C task, and must stay a 200 with `dod` absent. A fix
// that 500s on every task without a DoD would pass the test above and break
// every legacy board.
func TestGetTaskWithNoGoalRecordStillReturns200(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	legacy := &task.Task{
		Title:       "no goal record at all",
		Action:      task.ActionLLM,
		WorkspaceID: wsID,
		Status:      task.StatusInbox,
	}
	require.NoError(t, api.taskStore.Create(legacy))

	w := getTaskRec(t, api, legacy.ID)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var fetched gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &fetched))
	assert.Nil(t, fetched.Dod, "a task with no goal record genuinely has no dod")
}

// TestToWireJudgeVerdictCarriesEvidenceFields covers the stale-comment drop:
// toWireJudgeVerdict discarded Evidence, EvidenceSource, EvidenceTarget and
// Provenance on a comment asserting the Go fields did not exist yet. They do
// (pkg/task/verdict.go), and replay.go's WS frame already sends all four — so
// the live frame showed the judge's evidence and a page reload, which re-reads
// through REST, silently erased it.
func TestToWireJudgeVerdictCarriesEvidenceFields(t *testing.T) {
	v := task.JudgeVerdict{
		ID:    "verdict-1",
		Scope: task.VerdictScopeTask,
		Round: 1,
		Met:   true,
		PerCriterion: []task.CriterionVerdict{{
			CriterionID:    "crit-1",
			Met:            true,
			Reason:         "both clauses check out",
			EvidenceQuote:  "func Foo() error { return nil }",
			EvidenceSource: task.EvidenceSourceFileRead,
			EvidenceTarget: "pkg/foo/foo.go",
			Provenance:     task.ProvenanceJudgeRead,
			Evidence: []task.CriterionEvidenceEntry{
				{Part: "it compiles", Quote: "exit=0", Source: "machine_check", Target: "go build"},
				{Part: "it is documented", Quote: "// Foo does the thing", Source: "file_read", Target: "pkg/foo/foo.go"},
			},
		}},
	}

	out := toWireJudgeVerdict(v)

	require.Len(t, out.PerCriterion, 1)
	pc := out.PerCriterion[0]

	require.NotNil(t, pc.EvidenceSource, "evidence_source must reach the wire")
	assert.Equal(t, gen.JudgeVerdictPerCriterionEvidenceSourceFileRead, *pc.EvidenceSource)

	require.NotNil(t, pc.EvidenceTarget, "evidence_target must reach the wire")
	assert.Equal(t, "pkg/foo/foo.go", *pc.EvidenceTarget)

	require.NotNil(t, pc.Provenance, "provenance must reach the wire")
	assert.Equal(t, gen.JudgeVerdictPerCriterionProvenanceJudgeRead, *pc.Provenance)

	require.NotNil(t, pc.Evidence, "per-clause evidence must reach the wire")
	require.Len(t, *pc.Evidence, 2)
	assert.Equal(t, "it compiles", (*pc.Evidence)[0].Part)
	assert.Equal(t, "exit=0", (*pc.Evidence)[0].Quote)
	require.NotNil(t, (*pc.Evidence)[0].Source)
	assert.Equal(t, "machine_check", *(*pc.Evidence)[0].Source)
	require.NotNil(t, (*pc.Evidence)[0].Target)
	assert.Equal(t, "go build", *(*pc.Evidence)[0].Target)
	assert.Equal(t, "it is documented", (*pc.Evidence)[1].Part)

	// The pre-existing fields must be untouched by the addition.
	require.NotNil(t, pc.EvidenceQuote)
	assert.Equal(t, "func Foo() error { return nil }", *pc.EvidenceQuote)
	assert.Equal(t, "crit-1", pc.CriterionId)
	assert.True(t, pc.Met)
}

// TestToWireJudgeVerdictOmitsAbsentEvidenceFields pins the empty-safe half
// (JUDGE-FR-074): a verdict carrying none of the four new fields must render
// exactly as it did before they were mapped — absent, never empty strings or
// an empty array. Without this, the fix above could be satisfied by always
// emitting the fields, changing every legacy verdict's wire shape.
func TestToWireJudgeVerdictOmitsAbsentEvidenceFields(t *testing.T) {
	v := task.JudgeVerdict{
		ID:    "verdict-2",
		Scope: task.VerdictScopeTask,
		Met:   false,
		PerCriterion: []task.CriterionVerdict{{
			CriterionID: "crit-1", Met: false, Reason: "not done",
		}},
	}

	out := toWireJudgeVerdict(v)
	require.Len(t, out.PerCriterion, 1)
	pc := out.PerCriterion[0]

	assert.Nil(t, pc.EvidenceSource)
	assert.Nil(t, pc.EvidenceTarget)
	assert.Nil(t, pc.Provenance)
	assert.Nil(t, pc.Evidence)
	assert.Nil(t, pc.EvidenceQuote)

	// And the whole per-criterion object must carry no key for any of them.
	raw, err := json.Marshal(pc)
	require.NoError(t, err)
	for _, key := range []string{"evidence_source", "evidence_target", "provenance", "evidence", "evidence_quote"} {
		assert.NotContains(t, string(raw), `"`+key+`"`,
			"an absent reporting field must not appear on the wire at all: %s", raw)
	}
}
