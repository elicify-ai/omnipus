// rest_tasks_criteria_test.go — GOAL-FR-021/FR-029/FR-030/FR-047/FR-048,
// GOAL-MV-6, D-C (operator-ratified 2026-09-11): a task created OR edited
// through the API requires at least one acceptance criterion AND at least
// one definition-of-done item, uniformly at creation and at edit; a task's
// Definition of Done is authored onto its own paired goal record
// (pkg/goal), never a second persisted list on the task itself.
//
// Traces to: docs/internal/specs/goal-entity-spec.md FR-021, FR-029, FR-030,
// FR-047, FR-048, GOAL-MV-6; docs/internal/specs/adr-084-086-joint-delivery-
// plan.md wave E5.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// validCriteriaJSON / validDoDJSON are minimal, well-formed single-item
// criteria/dod JSON array fragments used across this file's create/patch
// bodies — the value under test is the PRESENCE/ABSENCE and EMPTINESS of
// the list, never its contents.
const (
	validCriteriaJSON = `[{"text":"the work is done","author":{"kind":"user","id":"tester"},"status":"pending"}]`
	validDoDJSON      = `[{"text":"no secrets in the output","author":{"kind":"user","id":"tester"},"status":"pending"}]`
)

// TestCreateTaskRejectsEmptyCriteria is the goal spec's own
// TestCreateTaskRejectsEmptyCriteria (FR-021, S-29, S-30), extended to also
// cover dod (GOAL-MV-6: the 400 must name WHICH of the two is missing) and
// the accept path. A test that greps this behaviour off a static schema
// cannot fail when the handler-level 400 regresses (GOAL-FR-047 states the
// rule is "not enforced as a JSON-Schema minItems... a 400, not schema-
// only") — this is why the assertion drives the real HTTP handler rather
// than validating the OpenAPI schema alone.
func TestCreateTaskRejectsEmptyCriteria(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	cases := []struct {
		name       string
		criteria   string // JSON fragment, or "" to omit the key entirely
		dod        string
		wantStatus int
		wantSubstr string // substring expected in the 400 body's error message
	}{
		{
			name:       "criteria absent",
			criteria:   "",
			dod:        validDoDJSON,
			wantStatus: http.StatusBadRequest,
			wantSubstr: "criteria is required",
		},
		{
			name:       "criteria empty array",
			criteria:   `[]`,
			dod:        validDoDJSON,
			wantStatus: http.StatusBadRequest,
			wantSubstr: "criteria is required",
		},
		{
			name:       "dod absent",
			criteria:   validCriteriaJSON,
			dod:        "",
			wantStatus: http.StatusBadRequest,
			wantSubstr: "dod is required",
		},
		{
			name:       "dod empty array",
			criteria:   validCriteriaJSON,
			dod:        `[]`,
			wantStatus: http.StatusBadRequest,
			wantSubstr: "dod is required",
		},
		{
			name:       "both present — accepted",
			criteria:   validCriteriaJSON,
			dod:        validDoDJSON,
			wantStatus: http.StatusCreated,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{
				"title":        "criteria/dod gate " + tc.name,
				"action":       "llm",
				"workspace_id": wsID,
			}
			if tc.criteria != "" {
				var raw any
				require.NoError(t, json.Unmarshal([]byte(tc.criteria), &raw))
				body["criteria"] = raw
			}
			if tc.dod != "" {
				var raw any
				require.NoError(t, json.Unmarshal([]byte(tc.dod), &raw))
				body["dod"] = raw
			}
			encoded, err := json.Marshal(body)
			require.NoError(t, err)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(string(encoded)))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/tasks"
			api.HandleTasks(w, r)

			require.Equal(t, tc.wantStatus, w.Code, "body=%s", w.Body.String())
			if tc.wantStatus == http.StatusBadRequest {
				assert.Contains(t, w.Body.String(), tc.wantSubstr,
					"the 400 body must name WHICH of criteria/dod is missing (GOAL-MV-6)")
			}
		})
	}
}

// TestCreateTaskPersistsGoalRecordWithCriteriaAndDoD proves GOAL-FR-003/
// FR-012/FR-021/FR-029: creating a task with criteria+dod creates a PAIRED
// goal record (pkg/goal, OwnerKind=task/OwnerID=the task's id) carrying
// both lists, in the "defining" phase (a task's goal is authored ahead of
// time and does not run until the task starts) — and that GET /tasks/{id}
// projects `dod` from that record onto the wire response (toWireDod),
// including the server-computed clause_count (JUDGE-FR-006b).
//
// This test FAILS on a reverted implementation two independent ways: (a) no
// goal record is created at all (goalStoreForTasks(...).GetByOwner returns
// goal.ErrOwnerNotFound), and (b) even if a record existed, GET would carry
// no `dod` field without toWireDod being wired into toWireTask.
func TestCreateTaskPersistsGoalRecordWithCriteriaAndDoD(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	body := `{"title":"paired goal record","action":"llm","workspace_id":"` + wsID + `",` +
		`"criteria":` + validCriteriaJSON + `,"dod":` + validDoDJSON + `}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotEmpty(t, created.Id)

	// The paired goal record exists, is task-owned, and is in "defining"
	// (GOAL-FR-012 — a task's goal does not activate until the task starts;
	// nothing in this create path may activate it).
	g, gErr := goalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, created.Id)
	require.NoError(t, gErr, "expected a paired goal record for the newly-created task")
	assert.Equal(t, gen.GoalOwnerKindTask, g.OwnerKind)
	assert.Equal(t, created.Id, g.OwnerID)
	assert.Equal(t, gen.GoalStateDefining, g.State,
		"a task's goal must not be active before the task itself starts (GOAL-FR-012)")
	require.Len(t, g.Criteria, 1)
	assert.Equal(t, "the work is done", g.Criteria[0].Text)
	require.Len(t, g.DoD, 1)
	assert.Equal(t, "no secrets in the output", g.DoD[0].Text)
	// JUDGE-FR-006b: clause_count is computed at normalise time, never left
	// zero, on every persisted criterion — including dod items.
	assert.GreaterOrEqual(t, g.DoD[0].ClauseCount, 1)

	// GET /tasks/{id} must project `dod` from the goal record onto the wire.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+created.Id, nil)
	rGet.URL.Path = "/api/v1/tasks/" + created.Id
	api.HandleTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "body=%s", wGet.Body.String())

	var fetched gen.Task
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &fetched))
	require.NotNil(t, fetched.Dod, "GET must carry the task's dod, sourced from its paired goal record")
	require.Len(t, *fetched.Dod, 1)
	assert.Equal(t, "no secrets in the output", (*fetched.Dod)[0].Text)
	require.NotNil(t, (*fetched.Dod)[0].ClauseCount,
		"clause_count must be projected onto the wire (C-58/JUDGE-FR-006b)")
	assert.GreaterOrEqual(t, *(*fetched.Dod)[0].ClauseCount, 1)

	// GET /tasks (list) must ALSO carry dod — proves the batch (taskGoalIndex)
	// path, not just the single-task GetByOwner path, is wired correctly.
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?workspace_id="+wsID, nil)
	rList.URL.Path = "/api/v1/tasks"
	rList.URL.RawQuery = "workspace_id=" + wsID
	api.HandleTasks(wList, rList)
	require.Equal(t, http.StatusOK, wList.Code, "body=%s", wList.Body.String())
	var listed []gen.Task
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &listed))
	var found *gen.Task
	for i := range listed {
		if listed[i].Id == created.Id {
			found = &listed[i]
		}
	}
	require.NotNil(t, found, "created task must appear in the list")
	require.NotNil(t, found.Dod, "list response must ALSO carry dod (batch taskGoalIndex path)")
	require.Len(t, *found.Dod, 1)
}

// TestPatchTaskRejectsEmptyCriteriaOrDoD is GOAL-FR-021/D-C's edit-time half
// (OQ-F answered: "Both. Uniformly, on every surface"): a PATCH that
// supplies criteria or dod and reduces either below one item is rejected,
// exactly like create — no creation-only carve-out. A PATCH that does not
// touch criteria/dod at all is unaffected.
func TestPatchTaskRejectsEmptyCriteriaOrDoD(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	createBody := `{"title":"patch gate","action":"llm","workspace_id":"` + wsID + `",` +
		`"criteria":` + validCriteriaJSON + `,"dod":` + validDoDJSON + `}`
	wCreate := httptest.NewRecorder()
	rCreate := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(createBody))
	rCreate.Header.Set("Content-Type", "application/json")
	rCreate.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wCreate, rCreate)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))

	patch := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+created.Id, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks/" + created.Id
		api.HandleTasks(w, r)
		return w
	}

	// A PATCH that reduces criteria to zero is rejected.
	wBadCriteria := patch(`{"criteria":[]}`)
	require.Equal(t, http.StatusBadRequest, wBadCriteria.Code, "body=%s", wBadCriteria.Body.String())
	assert.Contains(t, wBadCriteria.Body.String(), "criteria must not be empty")

	// A PATCH that reduces dod to zero is rejected.
	wBadDoD := patch(`{"dod":[]}`)
	require.Equal(t, http.StatusBadRequest, wBadDoD.Code, "body=%s", wBadDoD.Body.String())
	assert.Contains(t, wBadDoD.Body.String(), "dod must not be empty")

	// A PATCH that does not touch criteria/dod at all (e.g. a title rename)
	// is unaffected by the gate.
	wTitle := patch(`{"title":"renamed, criteria/dod untouched"}`)
	assert.Equal(t, http.StatusOK, wTitle.Code, "body=%s", wTitle.Body.String())
}

// TestPatchTaskUpdatesGoalRecordCriteria proves the edit-time half actually
// persists onto the paired goal record (not just onto the task's own
// dual-written Criteria field) — the goal record is the store GOAL-FR-029
// intends as authoritative going forward.
func TestPatchTaskUpdatesGoalRecordCriteria(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	createBody := `{"title":"patch persists to goal","action":"llm","workspace_id":"` + wsID + `",` +
		`"criteria":` + validCriteriaJSON + `,"dod":` + validDoDJSON + `}`
	wCreate := httptest.NewRecorder()
	rCreate := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(createBody))
	rCreate.Header.Set("Content-Type", "application/json")
	rCreate.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wCreate, rCreate)
	require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))

	newCriteriaJSON := `[{"text":"a DIFFERENT criterion","author":{"kind":"user","id":"tester"},"status":"pending"}]`
	wPatch := httptest.NewRecorder()
	rPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+created.Id,
		strings.NewReader(`{"criteria":`+newCriteriaJSON+`}`))
	rPatch.Header.Set("Content-Type", "application/json")
	rPatch.URL.Path = "/api/v1/tasks/" + created.Id
	api.HandleTasks(wPatch, rPatch)
	require.Equal(t, http.StatusOK, wPatch.Code, "body=%s", wPatch.Body.String())

	g, gErr := goalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, created.Id)
	require.NoError(t, gErr)
	require.Len(t, g.Criteria, 1)
	assert.Equal(t, "a DIFFERENT criterion", g.Criteria[0].Text,
		"the goal record's criteria must reflect the PATCH, not the original create")
	// dod was untouched by this PATCH — must survive unchanged on the goal record.
	require.Len(t, g.DoD, 1)
	assert.Equal(t, "no secrets in the output", g.DoD[0].Text)
}
