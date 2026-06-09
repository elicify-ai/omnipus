//go:build !cgo

// BDD: milestone REST API tests.
// Traces to: project-task-milestone-spec.md — FR-L2-009 (milestone CRUD),
// FR-L2-010 (milestone progress), FR-L2-011 (cascade clear milestone_id on tasks),
// FR-L2-013 (milestone sort order), FR-L2-028 (project cascade milestones).

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// milestoneResponse mirrors the JSON shape returned by milestone GET/POST/PUT.
// The handler returns milestoneWithProgress which is a local struct (not the
// generated gen.Milestone which lacks a progress field).
type milestoneResponse struct { // not-wire-format: test-local decode struct matching milestoneWithProgress JSON
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	DueDate     *string   `json:"due_date,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Progress    float64   `json:"progress"`
}

// milestoneListResponse matches the JSON shape from GET /projects/{id}/milestones.
type milestoneListResponse struct { // not-wire-format: test-local decode struct matching handleMilestoneList shim
	Milestones []milestoneResponse `json:"milestones"`
	Total      int                 `json:"total"`
}

// createMilestoneViaAPI creates a milestone for the given project and returns the decoded response.
func createMilestoneViaAPI(
	t *testing.T,
	api *restAPI,
	projectID, name string,
	dueDate *string,
) milestoneResponse {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q}`, name)
	if dueDate != nil {
		body = fmt.Sprintf(`{"name":%q,"due_date":%q}`, name, *dueDate)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/projects/"+projectID+"/milestones",
		strings.NewReader(body),
	)
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/projects/" + projectID + "/milestones"
	api.HandleMilestones(w, r)
	require.Equal(
		t,
		http.StatusCreated,
		w.Code,
		"create milestone must return 201; body=%s",
		w.Body.String(),
	)
	var m milestoneResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	require.NotEmpty(t, m.ID, "created milestone must have non-empty id")
	return m
}

// createBoardTaskWithMilestone creates a board task linked to a milestone via REST.
func createBoardTaskWithMilestone(
	t *testing.T,
	api *restAPI,
	name, projectID, milestoneID, status string,
) string {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"project_id":%q,"milestone_id":%q,"status":%q}`,
		name, projectID, milestoneID, status)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(w, r)
	require.Equal(
		t,
		http.StatusCreated,
		w.Code,
		"create board task must return 201; body=%s",
		w.Body.String(),
	)
	var resp struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.ID
}

// setTaskDoneViaAPI updates a board task status to "done" via REST.
func setTaskStatusViaAPI(t *testing.T, api *restAPI, taskID, newStatus string) {
	t.Helper()
	body := fmt.Sprintf(`{"status":%q}`, newStatus)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+taskID, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	// "active" requires agent context; use agent context header for any status here.
	r.Header.Set("X-Omnipus-Agent-Context", "true")
	r.URL.Path = "/api/v1/board/tasks/" + taskID
	api.HandleBoardTasks(w, r)
	require.Equal(
		t,
		http.StatusOK,
		w.Code,
		"update board task status must return 200; body=%s",
		w.Body.String(),
	)
}

// getMilestoneViaAPI fetches a single milestone and returns the response struct.
func getMilestoneViaAPI(
	t *testing.T,
	api *restAPI,
	projectID, milestoneID string,
) (milestoneResponse, int) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/projects/"+projectID+"/milestones/"+milestoneID,
		nil,
	)
	r.URL.Path = "/api/v1/projects/" + projectID + "/milestones/" + milestoneID
	api.HandleMilestones(w, r)
	if w.Code != http.StatusOK {
		return milestoneResponse{}, w.Code
	}
	var m milestoneResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m, w.Code
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Create
// ---------------------------------------------------------------------------

// TestHandleMilestones_Create verifies POST /projects/{id}/milestones returns 201 with
// milestone fields and progress=0.
// BDD: Given a valid project,
// When POST /api/v1/projects/{id}/milestones with {"name":"Sprint 1"},
// Then 201, id non-empty, name="Sprint 1", progress=0.0, milestone file exists on disk.
// Traces to: project-task-milestone-spec.md — FR-L2-009; BDD Scenario "Create milestone"
func TestHandleMilestones_Create(t *testing.T) {
	// Traces to: project-task-milestone-spec.md line ~418 (BDD: Create milestone / happy path)
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "MilestoneProject", "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projID+"/milestones",
		strings.NewReader(`{"name":"Sprint 1"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/projects/" + projID + "/milestones"
	api.HandleMilestones(w, r)

	require.Equal(
		t,
		http.StatusCreated,
		w.Code,
		"POST /projects/{id}/milestones must return 201; body=%s",
		w.Body.String(),
	)
	var m milestoneResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))

	// Content test: assert on actual field values, not just shape.
	assert.NotEmpty(t, m.ID, "created milestone must have non-empty id")
	assert.Equal(t, "Sprint 1", m.Name, "name must match request")
	assert.Equal(t, projID, m.ProjectID, "project_id must match request")
	assert.Equal(t, 0.0, m.Progress, "progress must be 0 for a new milestone with no tasks")
	assert.False(t, m.CreatedAt.IsZero(), "created_at must not be zero")

	// Persistence test: milestone file must exist on disk.
	milestoneFile := filepath.Join(api.homePath, "milestones", m.ID+".json")
	_, err := os.Stat(milestoneFile)
	assert.NoError(t, err, "milestone file must exist on disk at %s", milestoneFile)

	// Differentiation test: a second milestone with a different name has a different id and name.
	m2 := createMilestoneViaAPI(t, api, projID, "Sprint 2", nil)
	assert.NotEqual(t, m.ID, m2.ID, "two milestones must have different IDs")
	assert.NotEqual(t, m.Name, m2.Name, "two different inputs must produce different names")
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Create_ProjectNotFound
// ---------------------------------------------------------------------------

// TestHandleMilestones_Create_ProjectNotFound verifies POST to a non-existent project returns 404.
// BDD: Given no project with ID "01JXNOEXISTPROJECT000001",
// When POST /api/v1/projects/01JXNOEXISTPROJECT000001/milestones,
// Then 404.
// Traces to: project-task-milestone-spec.md — FR-L2-009 (error path)
func TestHandleMilestones_Create_ProjectNotFound(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — BDD Scenario "Milestone create project not found"
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/projects/01JXNOEXISTPROJECT000001/milestones",
		strings.NewReader(`{"name":"Orphan Milestone"}`),
	)
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/projects/01JXNOEXISTPROJECT000001/milestones"
	api.HandleMilestones(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"POST /projects/missing/milestones must return 404; body=%s", w.Body.String())
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Create_NameTooLong
// ---------------------------------------------------------------------------

// TestHandleMilestones_Create_NameTooLong verifies a name of 201 chars returns 400.
// BDD: Given a POST body with name longer than 200 characters,
// When POST /api/v1/projects/{id}/milestones,
// Then 400.
// Traces to: project-task-milestone-spec.md — FR-L2-009, dataset row #3 boundary validation
func TestHandleMilestones_Create_NameTooLong(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — Milestone name validation (maxLength: 200)
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "ValidationProject", "")

	longName := strings.Repeat("x", 201)
	body, err := json.Marshal(map[string]any{"name": longName})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projID+"/milestones",
		strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/projects/" + projID + "/milestones"
	api.HandleMilestones(w, r)

	assert.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"POST /projects/{id}/milestones with name > 200 chars must return 400; body=%s",
		w.Body.String(),
	)

	// Boundary: exactly 200 chars must be accepted.
	exactName := strings.Repeat("y", 200)
	body200, err := json.Marshal(map[string]any{"name": exactName})
	require.NoError(t, err)
	w200 := httptest.NewRecorder()
	r200 := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projID+"/milestones",
		strings.NewReader(string(body200)))
	r200.Header.Set("Content-Type", "application/json")
	r200.URL.Path = "/api/v1/projects/" + projID + "/milestones"
	api.HandleMilestones(w200, r200)
	assert.Equal(
		t,
		http.StatusCreated,
		w200.Code,
		"POST /projects/{id}/milestones with name exactly 200 chars must return 201; body=%s",
		w200.Body.String(),
	)
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Create_DescriptionTooLong
// ---------------------------------------------------------------------------

// TestHandleMilestones_Create_DescriptionTooLong verifies description of 2001 chars returns 400.
// BDD: Given a POST body with description longer than 2000 characters,
// When POST /api/v1/projects/{id}/milestones,
// Then 400.
// Traces to: project-task-milestone-spec.md — FR-L2-009 (description maxLength: 2000)
func TestHandleMilestones_Create_DescriptionTooLong(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — Milestone description validation
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "DescValidProject", "")

	longDesc := strings.Repeat("d", 2001)
	body, err := json.Marshal(map[string]any{"name": "Valid Name", "description": longDesc})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projID+"/milestones",
		strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/projects/" + projID + "/milestones"
	api.HandleMilestones(w, r)

	assert.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"POST /projects/{id}/milestones with description > 2000 chars must return 400; body=%s",
		w.Body.String(),
	)

	// Boundary: exactly 2000 chars must be accepted.
	exactDesc := strings.Repeat("e", 2000)
	body2000, err := json.Marshal(map[string]any{"name": "Valid Name", "description": exactDesc})
	require.NoError(t, err)
	w2000 := httptest.NewRecorder()
	r2000 := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projID+"/milestones",
		strings.NewReader(string(body2000)))
	r2000.Header.Set("Content-Type", "application/json")
	r2000.URL.Path = "/api/v1/projects/" + projID + "/milestones"
	api.HandleMilestones(w2000, r2000)
	assert.Equal(
		t,
		http.StatusCreated,
		w2000.Code,
		"POST /projects/{id}/milestones with description exactly 2000 chars must return 201; body=%s",
		w2000.Body.String(),
	)
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Get
// ---------------------------------------------------------------------------

// TestHandleMilestones_Get verifies GET /projects/{id}/milestones/{mid} returns 200 with correct fields.
// BDD: Given an existing milestone M in project P,
// When GET /api/v1/projects/P/milestones/M,
// Then 200 with id=M, name matching create request, project_id=P, progress=0.
// Traces to: project-task-milestone-spec.md — FR-L2-009 (get milestone)
func TestHandleMilestones_Get(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — BDD Scenario "Get milestone returns correct fields"
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "GetMilestoneProject", "")
	m := createMilestoneViaAPI(t, api, projID, "Alpha Release", nil)

	got, code := getMilestoneViaAPI(t, api, projID, m.ID)

	require.Equal(t, http.StatusOK, code, "GET /projects/{id}/milestones/{mid} must return 200")
	// Content test: verify all key fields match what was created.
	assert.Equal(t, m.ID, got.ID, "GET must return the correct milestone id")
	assert.Equal(t, "Alpha Release", got.Name, "GET must return the correct name")
	assert.Equal(t, projID, got.ProjectID, "GET must return the correct project_id")
	assert.Equal(t, 0.0, got.Progress, "GET of new milestone must have progress=0")
	assert.False(t, got.CreatedAt.IsZero(), "created_at must not be zero")
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Get_NotFound
// ---------------------------------------------------------------------------

// TestHandleMilestones_Get_NotFound verifies GET with a non-existent milestone ID returns 404.
// BDD: Given an existing project P but no milestone with ID "01JXNOMILESTONE0000000001",
// When GET /api/v1/projects/P/milestones/01JXNOMILESTONE0000000001,
// Then 404.
// Traces to: project-task-milestone-spec.md — FR-L2-009 (error path)
func TestHandleMilestones_Get_NotFound(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — Milestone not found → 404
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "GetNotFoundProject", "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/projects/"+projID+"/milestones/01JXNOMILESTONE0000000001",
		nil,
	)
	r.URL.Path = "/api/v1/projects/" + projID + "/milestones/01JXNOMILESTONE0000000001"
	api.HandleMilestones(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"GET /projects/{id}/milestones/nonexistent must return 404; body=%s", w.Body.String())
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_List
// ---------------------------------------------------------------------------

// TestHandleMilestones_List verifies GET /projects/{id}/milestones returns all milestones for the project.
// BDD: Given project P with 2 milestones,
// When GET /api/v1/projects/P/milestones,
// Then 200 with total=2 and both milestones present.
// Traces to: project-task-milestone-spec.md — FR-L2-009 (list milestones)
func TestHandleMilestones_List(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — BDD Scenario "List milestones returns all for project"
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "ListMilestonesProject", "")
	m1 := createMilestoneViaAPI(t, api, projID, "Milestone One", nil)
	m2 := createMilestoneViaAPI(t, api, projID, "Milestone Two", nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projID+"/milestones", nil)
	r.URL.Path = "/api/v1/projects/" + projID + "/milestones"
	api.HandleMilestones(w, r)

	require.Equal(
		t,
		http.StatusOK,
		w.Code,
		"GET /projects/{id}/milestones must return 200; body=%s",
		w.Body.String(),
	)
	var resp milestoneListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, 2, resp.Total, "total must be 2")
	require.Len(t, resp.Milestones, 2, "milestones array must have 2 items")

	ids := make([]string, 0, 2)
	for _, m := range resp.Milestones {
		ids = append(ids, m.ID)
	}
	assert.Contains(t, ids, m1.ID, "milestone 1 must appear in list")
	assert.Contains(t, ids, m2.ID, "milestone 2 must appear in list")
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_List_SortedByDueDate
// ---------------------------------------------------------------------------

// TestHandleMilestones_List_SortedByDueDate verifies milestones are sorted earliest due_date first,
// no-due_date last.
// BDD: Given 3 milestones: M-later (due 2027-06-01), M-earlier (due 2026-12-01), M-noduedate (no due_date),
// When GET /api/v1/projects/{id}/milestones,
// Then order is M-earlier, M-later, M-noduedate.
// Traces to: project-task-milestone-spec.md — FR-L2-013 (sort order: due_date ASC, no-date last)
func TestHandleMilestones_List_SortedByDueDate(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-L2-013
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "SortMilestonesProject", "")

	laterDue := "2027-06-01"
	earlierDue := "2026-12-01"

	// Create in reverse order of expected output to prove sorting is real, not insertion order.
	mNoDueDate := createMilestoneViaAPI(t, api, projID, "No Due Date", nil)
	mLater := createMilestoneViaAPI(t, api, projID, "Later Due", &laterDue)
	time.Sleep(2 * time.Millisecond) // ensure distinct created_at for tiebreaker
	mEarlier := createMilestoneViaAPI(t, api, projID, "Earlier Due", &earlierDue)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projID+"/milestones", nil)
	r.URL.Path = "/api/v1/projects/" + projID + "/milestones"
	api.HandleMilestones(w, r)

	require.Equal(
		t,
		http.StatusOK,
		w.Code,
		"GET /projects/{id}/milestones must return 200; body=%s",
		w.Body.String(),
	)
	var resp milestoneListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp.Milestones, 3, "must return 3 milestones")
	// Earlier due date must come before later; no-due-date must be last.
	assert.Equal(
		t,
		mEarlier.ID,
		resp.Milestones[0].ID,
		"earliest due_date must be first; got id=%s name=%s",
		resp.Milestones[0].ID,
		resp.Milestones[0].Name,
	)
	assert.Equal(
		t,
		mLater.ID,
		resp.Milestones[1].ID,
		"later due_date must be second; got id=%s name=%s",
		resp.Milestones[1].ID,
		resp.Milestones[1].Name,
	)
	assert.Equal(
		t,
		mNoDueDate.ID,
		resp.Milestones[2].ID,
		"no due_date must be last; got id=%s name=%s",
		resp.Milestones[2].ID,
		resp.Milestones[2].Name,
	)
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Progress
// ---------------------------------------------------------------------------

// TestHandleMilestones_Progress verifies that milestone progress equals done_tasks/total_tasks.
// BDD: Given milestone M in project P with 2 tasks (1 done, 1 next),
// When GET /api/v1/projects/P/milestones/M,
// Then progress=0.5.
// Traces to: project-task-milestone-spec.md — FR-L2-010 (milestone progress)
func TestHandleMilestones_Progress(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-L2-010: progress = done/total
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "ProgressMilestoneProject", "")
	m := createMilestoneViaAPI(t, api, projID, "Progress Test", nil)

	// Create 2 tasks linked to milestone M: 1 done, 1 next.
	taskDoneID := createBoardTaskWithMilestone(t, api, "Done Task", projID, m.ID, "next")
	_ = createBoardTaskWithMilestone(t, api, "Next Task", projID, m.ID, "next")
	setTaskStatusViaAPI(t, api, taskDoneID, "done")

	got, code := getMilestoneViaAPI(t, api, projID, m.ID)
	require.Equal(t, http.StatusOK, code)

	// Content test: progress must be exactly 0.5 (1 done / 2 total).
	assert.InDelta(t, 0.5, got.Progress, 0.001,
		"progress must be 0.5 (1 done / 2 total); got %f", got.Progress)
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Progress_FailedCountsAsDenominator
// ---------------------------------------------------------------------------

// TestHandleMilestones_Progress_FailedCountsAsDenominator verifies that failed tasks
// count toward the denominator but not the done count.
// BDD: Given milestone M with 1 done task and 1 failed task,
// When GET /api/v1/projects/P/milestones/M,
// Then progress=0.5 (1 done / 2 total).
// Traces to: project-task-milestone-spec.md — FR-L2-010 (failed tasks count toward total)
func TestHandleMilestones_Progress_FailedCountsAsDenominator(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-L2-010: failed counts as total, not done
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "FailedProgressProject", "")
	m := createMilestoneViaAPI(t, api, projID, "Failed Progress Test", nil)

	taskDoneID := createBoardTaskWithMilestone(t, api, "Done Task", projID, m.ID, "next")
	taskFailedID := createBoardTaskWithMilestone(t, api, "Failed Task", projID, m.ID, "next")
	setTaskStatusViaAPI(t, api, taskDoneID, "done")
	setTaskStatusViaAPI(t, api, taskFailedID, "failed")

	got, code := getMilestoneViaAPI(t, api, projID, m.ID)
	require.Equal(t, http.StatusOK, code)

	// Content test: 1 done + 1 failed = 2 total; only done counts toward numerator.
	assert.InDelta(
		t,
		0.5,
		got.Progress,
		0.001,
		"progress must be 0.5 (1 done / 2 total, failed counts as denominator only); got %f",
		got.Progress,
	)
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Update
// ---------------------------------------------------------------------------

// TestHandleMilestones_Update verifies PUT /projects/{id}/milestones/{mid} updates the milestone.
// BDD: Given milestone M with name "Old Name",
// When PUT /api/v1/projects/P/milestones/M with {"name":"New Name"},
// Then 200, subsequent GET returns name="New Name".
// Traces to: project-task-milestone-spec.md — FR-L2-009 (update milestone)
func TestHandleMilestones_Update(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — BDD Scenario "Update milestone name"
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "UpdateMilestoneProject", "")
	m := createMilestoneViaAPI(t, api, projID, "Old Name", nil)

	// PUT with new name → 200.
	wUp := httptest.NewRecorder()
	rUp := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+projID+"/milestones/"+m.ID,
		strings.NewReader(`{"name":"New Name"}`))
	rUp.Header.Set("Content-Type", "application/json")
	rUp.URL.Path = "/api/v1/projects/" + projID + "/milestones/" + m.ID
	api.HandleMilestones(wUp, rUp)

	require.Equal(
		t,
		http.StatusOK,
		wUp.Code,
		"PUT /projects/{id}/milestones/{mid} must return 200; body=%s",
		wUp.Body.String(),
	)
	var updated milestoneResponse
	require.NoError(t, json.Unmarshal(wUp.Body.Bytes(), &updated))
	assert.Equal(t, "New Name", updated.Name, "name must be updated to 'New Name'")

	// Differentiation test: GET confirms the change persisted.
	got, code := getMilestoneViaAPI(t, api, projID, m.ID)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "New Name", got.Name, "GET after PUT must return the updated name")
	assert.NotEqual(t, m.Name, got.Name, "different PUT inputs must produce different stored names")
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Update_ClearDueDate
// ---------------------------------------------------------------------------

// TestHandleMilestones_Update_ClearDueDate verifies PUT with due_date: null clears the due_date.
// BDD: Given milestone M with due_date="2026-12-31",
// When PUT /api/v1/projects/P/milestones/M with {"due_date":null},
// Then 200, GET shows due_date absent.
// Traces to: project-task-milestone-spec.md — FR-L2-009 (due_date null clears field)
//
// BUG DETECTED: This test is expected to FAIL.
// Production bug in rest_milestones.go:
//
//	milestoneUpdateRequest.DueDate is `*json.RawMessage` with `json:"due_date,omitempty"`.
//	The `omitempty` tag causes JSON null to be treated as absent (pointer stays nil),
//	so the null-clearing branch (raw == "null") is never reached. The due_date is NOT
//	cleared when PUT {"due_date":null} is sent.
//	Fix needed: remove `omitempty` from the DueDate tag in milestoneUpdateRequest.
//	Filed as production defect — backend-lead must fix this in rest_milestones.go.
func TestHandleMilestones_Update_ClearDueDate(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — MilestoneUpdateRequest due_date nullable
	// BLOCKED: production bug — omitempty on *json.RawMessage eats JSON null values.
	// This test deliberately fails to surface the defect. Do NOT remove or skip.
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "ClearDueDateProject", "")
	dueDate := "2026-12-31"
	m := createMilestoneViaAPI(t, api, projID, "Dated Milestone", &dueDate)
	require.NotNil(t, m.DueDate, "milestone must have due_date after creation")
	assert.Equal(t, "2026-12-31", *m.DueDate, "due_date must match create request")

	// PUT {"due_date":null} → 200, due_date cleared.
	wUp := httptest.NewRecorder()
	rUp := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+projID+"/milestones/"+m.ID,
		strings.NewReader(`{"due_date":null}`))
	rUp.Header.Set("Content-Type", "application/json")
	rUp.URL.Path = "/api/v1/projects/" + projID + "/milestones/" + m.ID
	api.HandleMilestones(wUp, rUp)

	require.Equal(
		t,
		http.StatusOK,
		wUp.Code,
		"PUT with due_date:null must return 200; body=%s",
		wUp.Body.String(),
	)

	// GET confirms due_date is cleared.
	// CURRENTLY FAILS: due_date is not cleared because omitempty on *json.RawMessage
	// prevents the null from reaching the handler.
	got, code := getMilestoneViaAPI(t, api, projID, m.ID)
	require.Equal(t, http.StatusOK, code)
	assert.Nil(t, got.DueDate, "due_date must be absent after PUT with null")
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Delete
// ---------------------------------------------------------------------------

// TestHandleMilestones_Delete verifies DELETE /projects/{id}/milestones/{mid} returns 204
// and subsequent GET returns 404.
// BDD: Given milestone M in project P,
// When DELETE /api/v1/projects/P/milestones/M,
// Then 204; GET /api/v1/projects/P/milestones/M → 404.
// Traces to: project-task-milestone-spec.md — FR-L2-009 (delete milestone)
func TestHandleMilestones_Delete(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — BDD Scenario "Delete milestone"
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "DeleteMilestoneProject", "")
	m := createMilestoneViaAPI(t, api, projID, "To Be Deleted", nil)

	// DELETE → 204.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/projects/"+projID+"/milestones/"+m.ID,
		nil,
	)
	rDel.URL.Path = "/api/v1/projects/" + projID + "/milestones/" + m.ID
	api.HandleMilestones(wDel, rDel)

	assert.Equal(t, http.StatusNoContent, wDel.Code,
		"DELETE /projects/{id}/milestones/{mid} must return 204; body=%s", wDel.Body.String())

	// GET after delete → 404.
	_, code := getMilestoneViaAPI(t, api, projID, m.ID)
	assert.Equal(t, http.StatusNotFound, code,
		"GET /projects/{id}/milestones/{mid} after delete must return 404")

	// Persistence test: milestone file must not exist on disk.
	milestoneFile := filepath.Join(api.homePath, "milestones", m.ID+".json")
	_, err := os.Stat(milestoneFile)
	assert.True(t, os.IsNotExist(err),
		"milestone file must be removed from disk after delete; stat err: %v", err)
}

// ---------------------------------------------------------------------------
// TestHandleMilestones_Delete_ClearsMilestoneIDOnTasks
// ---------------------------------------------------------------------------

// TestHandleMilestones_Delete_ClearsMilestoneIDOnTasks verifies that deleting milestone M
// clears milestone_id on all tasks that referenced it (FR-L2-011).
// BDD: Given task T with milestone_id=M,
// When DELETE /api/v1/projects/P/milestones/M,
// Then 204; GET /api/v1/board/tasks/T → milestone_id absent.
// Traces to: project-task-milestone-spec.md — FR-L2-011 (cascade clear milestone_id)
func TestHandleMilestones_Delete_ClearsMilestoneIDOnTasks(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-L2-011
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "CascadeClearProject", "")
	m := createMilestoneViaAPI(t, api, projID, "Cascade Milestone", nil)

	// Create a task with milestone_id=M.
	taskID := createBoardTaskWithMilestone(t, api, "Cascaded Task", projID, m.ID, "inbox")

	// Verify task has milestone_id before delete.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+taskID, nil)
	rGet.URL.Path = "/api/v1/board/tasks/" + taskID
	api.HandleBoardTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code)
	var taskBefore struct {
		MilestoneID *string `json:"milestone_id,omitempty"`
	}
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &taskBefore))
	require.NotNil(t, taskBefore.MilestoneID, "task must have milestone_id before milestone delete")
	assert.Equal(
		t,
		m.ID,
		*taskBefore.MilestoneID,
		"task milestone_id must match the created milestone",
	)

	// DELETE milestone M → 204.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/projects/"+projID+"/milestones/"+m.ID,
		nil,
	)
	rDel.URL.Path = "/api/v1/projects/" + projID + "/milestones/" + m.ID
	api.HandleMilestones(wDel, rDel)
	require.Equal(t, http.StatusNoContent, wDel.Code, "DELETE milestone must return 204")

	// GET task after milestone delete → milestone_id must be absent.
	wGetAfter := httptest.NewRecorder()
	rGetAfter := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+taskID, nil)
	rGetAfter.URL.Path = "/api/v1/board/tasks/" + taskID
	api.HandleBoardTasks(wGetAfter, rGetAfter)
	require.Equal(t, http.StatusOK, wGetAfter.Code)
	var taskAfter struct {
		MilestoneID *string `json:"milestone_id,omitempty"`
	}
	require.NoError(t, json.Unmarshal(wGetAfter.Body.Bytes(), &taskAfter))
	assert.Nil(t, taskAfter.MilestoneID,
		"task milestone_id must be cleared after milestone delete (FR-L2-011)")
}

// ---------------------------------------------------------------------------
// TestHandleProjects_Delete_CascadesMilestones
// ---------------------------------------------------------------------------

// TestHandleProjects_Delete_CascadesMilestones verifies that deleting project P
// removes all its milestones (FR-L2-028).
// BDD: Given project P with milestone M,
// When DELETE /api/v1/projects/P,
// Then 204; milestone file for M is deleted from disk.
// Traces to: project-task-milestone-spec.md — FR-L2-028 (project cascade deletes milestones)
func TestHandleProjects_Delete_CascadesMilestones(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-L2-028
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "CascadeProjectWithMilestones", "")
	m := createMilestoneViaAPI(t, api, projID, "Will Be Deleted", nil)

	// Verify milestone file exists.
	milestoneFile := filepath.Join(api.homePath, "milestones", m.ID+".json")
	_, err := os.Stat(milestoneFile)
	require.NoError(t, err, "milestone file must exist before project delete")

	// DELETE project → 204.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+projID, nil)
	rDel.URL.Path = "/api/v1/projects/" + projID
	api.HandleProjects(wDel, rDel)
	require.Equal(t, http.StatusNoContent, wDel.Code, "DELETE project must return 204")

	// Milestone file must be gone.
	_, err = os.Stat(milestoneFile)
	assert.True(t, os.IsNotExist(err),
		"milestone file must be deleted by project cascade; stat err: %v", err)
}
