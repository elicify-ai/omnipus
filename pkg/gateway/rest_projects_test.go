//go:build !cgo

// BDD: project REST API tests.
// Traces to: FR-001 (projects CRUD), FR-007 (cascade delete), FR-008 (sessions sub-resource).

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
)

// contextWithUserRole returns a new context that carries the given username (as a
// *config.UserConfig) and role, matching what the gateway auth middleware injects.
// Used in ownership-scoping tests to simulate authenticated requests.
func contextWithUserRole(parent context.Context, username string, role config.UserRole) context.Context {
	ctx := context.WithValue(parent, UserContextKey{}, &config.UserConfig{Username: username})
	return context.WithValue(ctx, RoleContextKey{}, role)
}

// writeTaskFile writes a minimal task JSON file for use in cascade-delete tests.
func writeTaskFile(t *testing.T, homePath, taskID, projectID, status string) {
	t.Helper()
	tasksDir := filepath.Join(homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	data := fmt.Sprintf(
		`{"id":%q,"name":"Task","status":%q,"project_id":%q,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		taskID,
		status,
		projectID,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, taskID+".json"), []byte(data), 0o600))
}

// createProjectViaAPI is a helper that POSTs a project and returns the id.
func createProjectViaAPI(t *testing.T, api *restAPI, name, description string) string {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"description":%q}`, name, description)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/projects"
	api.HandleProjects(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "create project should return 201; body=%s", w.Body.String())
	var proj gen.Project
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &proj))
	require.NotEmpty(t, proj.Id)
	return proj.Id
}

// TestHandleProjects_CreateAndList verifies POST creates a project and GET lists it.
// BDD: Given a valid ProjectCreateRequest,
// When POST /api/v1/projects is called,
// Then 201 with id, name="Alpha", status="active", pinned=false.
// When GET /api/v1/projects is called,
// Then 200 with array containing the created project.
// Traces to: FR-001, User Story 1 Acceptance Scenario 1
func TestHandleProjects_CreateAndList(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// POST /api/v1/projects → 201.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects",
		strings.NewReader(`{"name":"Alpha","description":"desc"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/projects"
	api.HandleProjects(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "POST /projects must return 201; body=%s", w.Body.String())
	var proj gen.Project
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &proj))
	assert.NotEmpty(t, proj.Id, "created project must have non-empty id")
	assert.Equal(t, "Alpha", proj.Name, "name must match request")
	assert.Equal(t, gen.ProjectStatusActive, proj.Status, "status must default to active")
	assert.False(t, proj.Pinned, "pinned must default to false")

	// GET /api/v1/projects → 200, array contains the project.
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rList.URL.Path = "/api/v1/projects"
	api.HandleProjects(wList, rList)

	require.Equal(t, http.StatusOK, wList.Code)
	var projects []gen.Project
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &projects))
	require.NotEmpty(t, projects, "GET /projects must return at least one project")
	found := false
	for _, p := range projects {
		if p.Id == proj.Id {
			found = true
			assert.Equal(t, "Alpha", p.Name)
		}
	}
	assert.True(t, found, "created project must appear in list response")
}

// TestHandleProjects_GetByID verifies GET /api/v1/projects/{id} returns the project.
// BDD: Given an existing project,
// When GET /api/v1/projects/{id} is called,
// Then 200 with correct fields.
// When GET /api/v1/projects/nonexistent is called,
// Then 404.
// Traces to: FR-001
func TestHandleProjects_GetByID(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create a project to GET later.
	id := createProjectViaAPI(t, api, "BetaProject", "beta description")

	// GET /api/v1/projects/{id} → 200 with correct fields.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+id, nil)
	rGet.URL.Path = "/api/v1/projects/" + id
	api.HandleProjects(wGet, rGet)

	require.Equal(t, http.StatusOK, wGet.Code, "GET /projects/{id} must return 200")
	var got gen.Project
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	assert.Equal(t, id, got.Id)
	assert.Equal(t, "BetaProject", got.Name)
	assert.Equal(t, gen.ProjectStatusActive, got.Status)

	// GET /api/v1/projects/nonexistent → 404.
	wNot := httptest.NewRecorder()
	rNot := httptest.NewRequest(http.MethodGet, "/api/v1/projects/01JXNOTEXISTENT00000000000", nil)
	rNot.URL.Path = "/api/v1/projects/01JXNOTEXISTENT00000000000"
	api.HandleProjects(wNot, rNot)
	assert.Equal(t, http.StatusNotFound, wNot.Code, "GET /projects/nonexistent must return 404")
}

// TestHandleProjects_Update verifies PUT /api/v1/projects/{id} updates the project.
// BDD: Given an existing project,
// When PUT /api/v1/projects/{id} with {"name":"Beta","pinned":true},
// Then 200 with name="Beta", pinned=true.
// When PUT to nonexistent ID,
// Then 404.
// When PUT with empty name,
// Then 400.
// Traces to: FR-001
func TestHandleProjects_Update(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createProjectViaAPI(t, api, "OriginalName", "")

	// PUT with new name + pinned=true → 200.
	wUp := httptest.NewRecorder()
	rUp := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+id,
		strings.NewReader(`{"name":"Beta","pinned":true}`))
	rUp.Header.Set("Content-Type", "application/json")
	rUp.URL.Path = "/api/v1/projects/" + id
	api.HandleProjects(wUp, rUp)

	require.Equal(t, http.StatusOK, wUp.Code, "PUT /projects/{id} must return 200; body=%s", wUp.Body.String())
	var updated gen.Project
	require.NoError(t, json.Unmarshal(wUp.Body.Bytes(), &updated))
	assert.Equal(t, "Beta", updated.Name, "name must be updated")
	assert.True(t, updated.Pinned, "pinned must be updated to true")

	// Differentiation test: a second PUT with a different name produces a different result.
	wUp2 := httptest.NewRecorder()
	rUp2 := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+id,
		strings.NewReader(`{"name":"Gamma"}`))
	rUp2.Header.Set("Content-Type", "application/json")
	rUp2.URL.Path = "/api/v1/projects/" + id
	api.HandleProjects(wUp2, rUp2)
	require.Equal(t, http.StatusOK, wUp2.Code)
	var updated2 gen.Project
	require.NoError(t, json.Unmarshal(wUp2.Body.Bytes(), &updated2))
	assert.Equal(t, "Gamma", updated2.Name, "second PUT must reflect the new name (not hardcoded)")
	assert.NotEqual(t, updated.Name, updated2.Name, "different inputs must produce different outputs")

	// PUT to nonexistent ID → 404.
	wNot := httptest.NewRecorder()
	rNot := httptest.NewRequest(http.MethodPut, "/api/v1/projects/01JXNOTEXISTENT00000000000",
		strings.NewReader(`{"name":"X"}`))
	rNot.Header.Set("Content-Type", "application/json")
	rNot.URL.Path = "/api/v1/projects/01JXNOTEXISTENT00000000000"
	api.HandleProjects(wNot, rNot)
	assert.Equal(t, http.StatusNotFound, wNot.Code, "PUT /projects/nonexistent must return 404")

	// PUT with empty name → 400.
	wBad := httptest.NewRecorder()
	rBad := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+id,
		strings.NewReader(`{"name":""}`))
	rBad.Header.Set("Content-Type", "application/json")
	rBad.URL.Path = "/api/v1/projects/" + id
	api.HandleProjects(wBad, rBad)
	assert.Equal(t, http.StatusBadRequest, wBad.Code, "PUT /projects/{id} with empty name must return 400")
}

// TestHandleProjects_Delete_Returns204 verifies DELETE /api/v1/projects/{id} returns 204.
// BDD: Given an existing project,
// When DELETE /api/v1/projects/{id} is called,
// Then 204, empty body.
// When DELETE nonexistent,
// Then 404.
// Traces to: FR-007
func TestHandleProjects_Delete_Returns204(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createProjectViaAPI(t, api, "ToDelete", "")

	// DELETE → 204.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+id, nil)
	rDel.URL.Path = "/api/v1/projects/" + id
	api.HandleProjects(wDel, rDel)
	assert.Equal(t, http.StatusNoContent, wDel.Code, "DELETE /projects/{id} must return 204")
	assert.Empty(t, wDel.Body.String(), "DELETE response body must be empty")

	// DELETE nonexistent → 404.
	wNot := httptest.NewRecorder()
	rNot := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/01JXNOTEXISTENT00000000000", nil)
	rNot.URL.Path = "/api/v1/projects/01JXNOTEXISTENT00000000000"
	api.HandleProjects(wNot, rNot)
	assert.Equal(t, http.StatusNotFound, wNot.Code, "DELETE /projects/nonexistent must return 404")
}

// TestHandleProjects_CascadeDelete_RemovesTasks verifies DELETE cascades to tasks.
// BDD: Given a project with an associated task file,
// When DELETE /api/v1/projects/{id} is called,
// Then 204 and the task file is removed.
// Traces to: FR-007
func TestHandleProjects_CascadeDelete_RemovesTasks(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createProjectViaAPI(t, api, "ProjectWithTasks", "")

	// Write a GTD task that belongs to this project.
	taskID := "01JXTASK00000000000000001"
	writeTaskFile(t, api.homePath, taskID, id, "inbox")

	// Verify the task file exists before delete.
	taskPath := filepath.Join(api.homePath, "tasks", taskID+".json")
	_, err := os.Stat(taskPath)
	require.NoError(t, err, "task file should exist before cascade delete")

	// DELETE project → 204.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+id, nil)
	rDel.URL.Path = "/api/v1/projects/" + id
	api.HandleProjects(wDel, rDel)
	require.Equal(t, http.StatusNoContent, wDel.Code, "DELETE /projects/{id} must return 204")

	// Task file must be gone.
	_, err = os.Stat(taskPath)
	assert.True(t, os.IsNotExist(err), "task file must be removed by cascade delete; stat err: %v", err)
}

// TestHandleProjects_Sessions verifies GET /api/v1/projects/{id}/sessions returns links.
// BDD: Given a project with session links,
// When GET /api/v1/projects/{id}/sessions is called,
// Then 200 with array containing the links.
// Traces to: FR-008
func TestHandleProjects_Sessions(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createProjectViaAPI(t, api, "ProjectWithSessions", "")

	// Write session link entries using the linker.
	linker := systools.NewProjectSessionLinker(api.homePath)
	linker.LinkSession(id, "sess-alpha-001")
	linker.LinkSession(id, "sess-alpha-002")

	// GET /api/v1/projects/{id}/sessions → 200 with links.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+id+"/sessions", nil)
	r.URL.Path = "/api/v1/projects/" + id + "/sessions"
	api.HandleProjects(w, r)

	require.Equal(t, http.StatusOK, w.Code, "GET /projects/{id}/sessions must return 200; body=%s", w.Body.String())
	var links []gen.ProjectSessionLink
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &links))
	assert.Len(t, links, 2, "sessions sub-resource must return 2 linked sessions")

	sessionIDs := make([]string, 0, len(links))
	for _, l := range links {
		sessionIDs = append(sessionIDs, l.SessionId)
	}
	assert.Contains(t, sessionIDs, "sess-alpha-001")
	assert.Contains(t, sessionIDs, "sess-alpha-002")
}

// TestHandleProjects_List_SortOrder verifies pinned projects come first.
// BDD: Given 3 projects where one is pinned with pin_order=1,
// When GET /api/v1/projects is called,
// Then the pinned project is first in the array.
// Traces to: FR-001
func TestHandleProjects_List_SortOrder(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create 3 projects with distinct names.
	_ = createProjectViaAPI(t, api, "UnpinnedFirst", "")
	_ = createProjectViaAPI(t, api, "UnpinnedSecond", "")
	id3 := createProjectViaAPI(t, api, "PinnedProject", "")

	// Sleep briefly so created_at times differ.
	time.Sleep(2 * time.Millisecond)

	// Pin project id3 with pin_order=1.
	wPin := httptest.NewRecorder()
	rPin := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+id3,
		strings.NewReader(`{"pinned":true,"pin_order":1}`))
	rPin.Header.Set("Content-Type", "application/json")
	rPin.URL.Path = "/api/v1/projects/" + id3
	api.HandleProjects(wPin, rPin)
	require.Equal(t, http.StatusOK, wPin.Code)

	// GET /api/v1/projects → first result must be the pinned one.
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rList.URL.Path = "/api/v1/projects"
	api.HandleProjects(wList, rList)

	require.Equal(t, http.StatusOK, wList.Code)
	var projects []gen.Project
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &projects))
	require.GreaterOrEqual(t, len(projects), 3, "must return at least 3 projects")
	assert.True(t, projects[0].Pinned, "first project in list must be pinned")
	assert.Equal(t, id3, projects[0].Id, "pinned project must be first")
}

// TestHandleProjects_UnknownSubPath_Returns404 verifies unknown sub-paths return 404.
// BDD: Given /api/v1/projects/{id}/unknown,
// When GET is called,
// Then 404.
// Traces to: FR-001
func TestHandleProjects_UnknownSubPath_Returns404(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createProjectViaAPI(t, api, "SubPathTest", "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+id+"/unknown", nil)
	r.URL.Path = "/api/v1/projects/" + id + "/unknown"
	api.HandleProjects(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code, "GET /projects/{id}/unknown must return 404")
}

// TestHandleProjects_MethodNotAllowed verifies PATCH /api/v1/projects returns 405.
// BDD: Given PATCH /api/v1/projects,
// When PATCH is called,
// Then 405.
// Traces to: FR-001
func TestHandleProjects_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/projects", nil)
	r.URL.Path = "/api/v1/projects"
	api.HandleProjects(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "PATCH /projects must return 405")
}

// TestHandleProjects_Boundaries verifies field-length validation on POST and PUT /api/v1/projects.
// BDD: Given POST /api/v1/projects with name > 200 chars,
// When the request is handled,
// Then 400.
// Given PUT /api/v1/projects/{id} with description > 2000 chars,
// When the request is handled,
// Then 400.
// Traces to: project-task-management-level1-spec.md FG-M7
func TestHandleProjects_Boundaries(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// POST with name > 200 chars → 400.
	longName := strings.Repeat("x", 201)
	body, err := json.Marshal(map[string]any{"name": longName})
	require.NoError(t, err)
	wLong := httptest.NewRecorder()
	rLong := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(string(body)))
	rLong.Header.Set("Content-Type", "application/json")
	rLong.URL.Path = "/api/v1/projects"
	api.HandleProjects(wLong, rLong)
	assert.Equal(t, http.StatusBadRequest, wLong.Code,
		"POST /projects with name > 200 chars must return 400; body=%s", wLong.Body.String())

	// POST with name exactly 200 chars → 201 (boundary: exactly at limit is accepted).
	exactName := strings.Repeat("y", 200)
	body200, err := json.Marshal(map[string]any{"name": exactName})
	require.NoError(t, err)
	wExact := httptest.NewRecorder()
	rExact := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(string(body200)))
	rExact.Header.Set("Content-Type", "application/json")
	rExact.URL.Path = "/api/v1/projects"
	api.HandleProjects(wExact, rExact)
	assert.Equal(t, http.StatusCreated, wExact.Code,
		"POST /projects with name exactly 200 chars must return 201; body=%s", wExact.Body.String())

	// Create a project and then PUT with description > 2000 chars → 400.
	projID := createProjectViaAPI(t, api, "BoundaryProject", "initial description")
	longDesc := strings.Repeat("d", 2001)
	updateBody, err := json.Marshal(map[string]any{"name": "BoundaryProject", "description": longDesc})
	require.NoError(t, err)
	wDesc := httptest.NewRecorder()
	rDesc := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+projID, strings.NewReader(string(updateBody)))
	rDesc.Header.Set("Content-Type", "application/json")
	rDesc.URL.Path = "/api/v1/projects/" + projID
	api.HandleProjects(wDesc, rDesc)
	assert.Equal(t, http.StatusBadRequest, wDesc.Code,
		"PUT /projects/{id} with description > 2000 chars must return 400; body=%s", wDesc.Body.String())

	// PUT with description exactly 2000 chars → 200 (boundary: at limit is accepted).
	exactDesc := strings.Repeat("e", 2000)
	exactDescBody, err := json.Marshal(map[string]any{"name": "BoundaryProject", "description": exactDesc})
	require.NoError(t, err)
	wExactDesc := httptest.NewRecorder()
	rExactDesc := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/projects/"+projID,
		strings.NewReader(string(exactDescBody)),
	)
	rExactDesc.Header.Set("Content-Type", "application/json")
	rExactDesc.URL.Path = "/api/v1/projects/" + projID
	api.HandleProjects(wExactDesc, rExactDesc)
	assert.Equal(t, http.StatusOK, wExactDesc.Code,
		"PUT /projects/{id} with description exactly 2000 chars must return 200; body=%s", wExactDesc.Body.String())
}

// TestHandleProjects_InvalidStatusFilter verifies GET /api/v1/projects?status=garbage returns 400.
// BDD: Given GET /api/v1/projects?status=garbage (an unrecognized status value),
// When the request is handled,
// Then 400.
// Traces to: project-task-management-level1-spec.md FG-M13
func TestHandleProjects_InvalidStatusFilter(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/projects?status=garbage", nil)
	r.URL.Path = "/api/v1/projects"
	api.HandleProjects(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"GET /projects?status=garbage must return 400; body=%s", w.Body.String())
}

// TestHandleProjects_CascadeDelete_TasksAndLinksGone verifies that DELETE /api/v1/projects/{id}
// removes the project, all its associated board tasks, and all its session links.
// BDD: Given a project P, a board task T associated with P, and a session link for P,
// When DELETE /api/v1/projects/{P} is called,
// Then 204, task file T is gone, ReadLinks for P returns nil, GET /projects/{P} returns 404.
// Traces to: project-task-management-level1-spec.md FG-M8
func TestHandleProjects_CascadeDelete_TasksAndLinksGone(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Step 1: Create a project via REST → get project ID P.
	projID := createProjectViaAPI(t, api, "CascadeProject", "cascade test project")

	// Step 2: Create a board task via REST with project_id=P → get task ID T.
	taskBody := fmt.Sprintf(`{"name":"CascadeTask","project_id":%q}`, projID)
	wTask := httptest.NewRecorder()
	rTask := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(taskBody))
	rTask.Header.Set("Content-Type", "application/json")
	rTask.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wTask, rTask)
	require.Equal(t, http.StatusCreated, wTask.Code, "create board task must return 201; body=%s", wTask.Body.String())
	var createdTask gen.BoardTask
	require.NoError(t, json.Unmarshal(wTask.Body.Bytes(), &createdTask))
	taskID := createdTask.Id
	require.NotEmpty(t, taskID, "created board task must have non-empty id")

	// Verify the task file exists before delete.
	taskPath := filepath.Join(api.homePath, "tasks", taskID+".json")
	_, statErr := os.Stat(taskPath)
	require.NoError(t, statErr, "task file must exist before cascade delete; path=%s", taskPath)

	// Step 3: Link a session to the project using the systools linker.
	linker := systools.NewProjectSessionLinker(api.homePath)
	linker.LinkSession(projID, "sess-cascade-1")

	// Verify the link exists before delete.
	linksBefore := systools.ReadLinks(api.homePath, projID)
	require.NotEmpty(t, linksBefore, "session links must exist before cascade delete")

	// Step 4: DELETE /api/v1/projects/{P} → 204.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+projID, nil)
	rDel.URL.Path = "/api/v1/projects/" + projID
	api.HandleProjects(wDel, rDel)
	require.Equal(
		t,
		http.StatusNoContent,
		wDel.Code,
		"DELETE /projects/{id} must return 204; body=%s",
		wDel.Body.String(),
	)

	// Step 5: Assert task file at tasks/{T}.json does NOT exist.
	_, statAfterErr := os.Stat(taskPath)
	assert.True(t, os.IsNotExist(statAfterErr),
		"task file must be removed by cascade delete; path=%s, stat error=%v", taskPath, statAfterErr)

	// Step 6: Assert ReadLinks returns nil or empty for the deleted project.
	linksAfter := systools.ReadLinks(api.homePath, projID)
	assert.Empty(t, linksAfter,
		"session links must be removed by cascade delete; got %d links", len(linksAfter))

	// Step 7: Assert GET /api/v1/projects/{P} returns 404.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projID, nil)
	rGet.URL.Path = "/api/v1/projects/" + projID
	api.HandleProjects(wGet, rGet)
	assert.Equal(t, http.StatusNotFound, wGet.Code,
		"GET /projects/{id} after delete must return 404; body=%s", wGet.Body.String())
}

// TestHandleProjects_ConcurrentDelete verifies that two simultaneous DELETE requests
// for the same project do not produce a 500 — each returns either 204 or 404.
// BDD: Given an existing project P and a board task T linked to P,
// When two goroutines simultaneously DELETE /api/v1/projects/{P},
// Then both respond with 204 or 404 (never 500) and the project file is absent afterward.
// Traces to: project-task-management-level1-spec.md — TST-001 (concurrent delete safety)
func TestHandleProjects_ConcurrentDelete(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Step 1: Create a project via POST /api/v1/projects.
	projID := createProjectViaAPI(t, api, "ConcurrentDeleteProject", "concurrent delete test")

	// Step 2: Create a board task linked to the project via POST /api/v1/board/tasks.
	taskBody := fmt.Sprintf(`{"name":"ConcurrentTask","project_id":%q}`, projID)
	wTask := httptest.NewRecorder()
	rTask := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(taskBody))
	rTask.Header.Set("Content-Type", "application/json")
	rTask.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wTask, rTask)
	require.Equal(t, http.StatusCreated, wTask.Code,
		"create board task must return 201; body=%s", wTask.Body.String())

	// Step 3: Launch 2 goroutines that simultaneously DELETE the project.
	type result struct {
		code int
		body string
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	for i := range 2 {
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+projID, nil)
			r.URL.Path = "/api/v1/projects/" + projID
			api.HandleProjects(w, r)
			results[i] = result{code: w.Code, body: w.Body.String()}
		}()
	}

	// Step 4: Wait for both goroutines to finish.
	wg.Wait()

	// Step 5: Assert both responses had status 204 or 404 (never 500).
	for i, res := range results {
		assert.True(t,
			res.code == http.StatusNoContent || res.code == http.StatusNotFound,
			"goroutine %d: concurrent DELETE must return 204 or 404, got %d; body=%s",
			i, res.code, res.body,
		)
		assert.NotEqual(t, http.StatusInternalServerError, res.code,
			"goroutine %d: concurrent DELETE must never return 500; body=%s", i, res.body)
	}

	// Step 6: Assert the project file is gone after both deletes.
	projectPath := filepath.Join(api.homePath, "projects", projID+".json")
	_, statErr := os.Stat(projectPath)
	assert.True(t, os.IsNotExist(statErr),
		"project file must not exist after concurrent deletes; path=%s, err=%v", projectPath, statErr)

	// Verify the project is truly gone by trying to GET it.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projID, nil)
	rGet.URL.Path = "/api/v1/projects/" + projID
	api.HandleProjects(wGet, rGet)
	assert.Equal(t, http.StatusNotFound, wGet.Code,
		"GET /projects/{id} after concurrent delete must return 404; body=%s", wGet.Body.String())
}

// ---------------------------------------------------------------------------
// Inbox auto-creation and protection tests (project-task-milestone-spec.md)
// ---------------------------------------------------------------------------

// TestHandleProjects_InboxAutoCreated verifies that GET /api/v1/projects on a fresh home dir
// returns a project with is_default=true and display name="Main".
//
// Item 1: the default project's DISPLAY name was renamed "Inbox" -> "Main" to
// avoid colliding with the "Inbox" board column/status. The rename touches the
// human display name ONLY — the default project is identified by the stable
// IsDefault flag (used by ensureInboxProject, the sidebar-first sort, and the
// not-deletable guard), never by the literal display string. This test pins the
// new display name AND that the default-project identity (is_default=true) is
// preserved so inbox routing is unaffected.
// BDD: Given a fresh home directory with no projects,
// When GET /api/v1/projects is called,
// Then 200 with an array containing a project where is_default=true and name="Main".
// Traces to: project-task-milestone-spec.md — FR-L2-001 (default-project auto-creation), FR-INX-1
func TestHandleProjects_InboxAutoCreated(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-L2-001 / FR-INX-1: Inbox auto-created on first use
	api := newTestRestAPIWithHome(t)

	// Trigger auto-creation by calling ensureInboxProject directly (same path the
	// HandleProjects list handler calls in production on first use).
	// The handler itself calls ensureInboxProject before listing.
	// We call it explicitly here to match how the gateway wires it at startup/first-use.
	require.NoError(t, ensureInboxProject(api.homePath),
		"ensureInboxProject must not error on fresh home")

	// GET /api/v1/projects → response must contain Inbox project.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	r.URL.Path = "/api/v1/projects"
	api.HandleProjects(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"GET /projects on fresh dir with inbox must return 200; body=%s", w.Body.String())
	var projects []gen.Project
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &projects))
	require.NotEmpty(t, projects, "GET /projects must return at least the Inbox project")

	// Content test: find the default project. Identity is the is_default flag
	// (stable routing key); the display name is "Main" after the Item 1 rename.
	foundInbox := false
	for _, p := range projects {
		if p.IsDefault != nil && *p.IsDefault {
			foundInbox = true
			assert.Equal(t, "Main", p.Name, "the default project's display name must be 'Main' (renamed from 'Inbox')")
			assert.Equal(t, gen.ProjectStatusActive, p.Status, "default project must be active")
		}
	}
	assert.True(t, foundInbox, "GET /projects must contain a project with is_default=true (the default/Main project)")
}

// TestHandleProjects_InboxNotDeletable verifies that DELETE /api/v1/projects/<inbox-id> returns 409.
// BDD: Given the Inbox project with is_default=true,
// When DELETE /api/v1/projects/{inbox-id} is called,
// Then 409 (cannot delete the default Inbox project).
// Traces to: project-task-milestone-spec.md — FR-INX-2 (Inbox not deletable), FR-L2-002
func TestHandleProjects_InboxNotDeletable(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-INX-2 / FR-L2-002: Inbox cannot be deleted
	api := newTestRestAPIWithHome(t)

	// Create the Inbox project directly via ensureInboxProject.
	require.NoError(t, ensureInboxProject(api.homePath))

	// Find the Inbox project ID by listing projects.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	r.URL.Path = "/api/v1/projects"
	api.HandleProjects(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var projects []gen.Project
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &projects))

	var inboxID string
	for _, p := range projects {
		if p.IsDefault != nil && *p.IsDefault {
			inboxID = p.Id
		}
	}
	require.NotEmpty(t, inboxID, "must find Inbox project in the list")

	// DELETE Inbox → 409.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+inboxID, nil)
	rDel.URL.Path = "/api/v1/projects/" + inboxID
	api.HandleProjects(wDel, rDel)

	assert.Equal(t, http.StatusConflict, wDel.Code,
		"DELETE /projects/{inbox-id} must return 409 (Inbox not deletable); body=%s", wDel.Body.String())

	// Verify Inbox still exists after failed delete.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+inboxID, nil)
	rGet.URL.Path = "/api/v1/projects/" + inboxID
	api.HandleProjects(wGet, rGet)
	assert.Equal(t, http.StatusOK, wGet.Code,
		"GET /projects/{inbox-id} after failed delete must still return 200")
}

// ---------------------------------------------------------------------------
// Ownership scoping tests (SEC-2)
// ---------------------------------------------------------------------------

// TestHandleProjects_OwnershipScoping verifies SEC-2: user A cannot see user B's project.
// BDD: Given user A (alice) creates project P with her credentials,
// When user B (bob, role=user) calls GET /api/v1/projects/{id} for P,
// Then 404 (resource enumeration prevention).
// When bob calls GET /api/v1/projects (list),
// Then P is absent from the list.
// When admin calls GET /api/v1/projects/{id} for P,
// Then 200 (admin sees everything).
// Traces to: SEC-2
func TestHandleProjects_OwnershipScoping(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// User alice creates project P.
	body := `{"name":"AliceProject","description":"owned by alice"}`
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(body))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/projects"
	rPost = rPost.WithContext(
		contextWithUserRole(rPost.Context(), "alice", config.UserRoleUser))
	api.HandleProjects(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code, "alice POST must return 201; body=%s", wPost.Body.String())
	var proj gen.Project
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &proj))
	projID := proj.Id
	require.NotEmpty(t, projID, "created project must have an id")

	// Verify the owner field is set to alice.
	require.NotNil(t, proj.Owner, "owner must be set on create")
	assert.Equal(t, "alice", *proj.Owner, "owner must equal the creating user's username")

	// User bob (role=user) calls GET /api/v1/projects/{id} → 404.
	wGetBob := httptest.NewRecorder()
	rGetBob := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projID, nil)
	rGetBob.URL.Path = "/api/v1/projects/" + projID
	rGetBob = rGetBob.WithContext(
		contextWithUserRole(rGetBob.Context(), "bob", config.UserRoleUser))
	api.HandleProjects(wGetBob, rGetBob)
	assert.Equal(t, http.StatusNotFound, wGetBob.Code,
		"bob must get 404 on alice's project; body=%s", wGetBob.Body.String())

	// User bob calls GET /api/v1/projects (list) — alice's project must not appear.
	wListBob := httptest.NewRecorder()
	rListBob := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rListBob.URL.Path = "/api/v1/projects"
	rListBob = rListBob.WithContext(
		contextWithUserRole(rListBob.Context(), "bob", config.UserRoleUser))
	api.HandleProjects(wListBob, rListBob)
	require.Equal(t, http.StatusOK, wListBob.Code)
	var bobProjects []gen.Project
	require.NoError(t, json.Unmarshal(wListBob.Body.Bytes(), &bobProjects))
	for _, p := range bobProjects {
		assert.NotEqual(t, projID, p.Id,
			"alice's project must NOT appear in bob's project list")
	}

	// Admin calls GET /api/v1/projects/{id} → 200 (admin sees all).
	wGetAdmin := httptest.NewRecorder()
	rGetAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projID, nil)
	rGetAdmin.URL.Path = "/api/v1/projects/" + projID
	rGetAdmin = rGetAdmin.WithContext(
		contextWithUserRole(rGetAdmin.Context(), "admin", config.UserRoleAdmin))
	api.HandleProjects(wGetAdmin, rGetAdmin)
	assert.Equal(t, http.StatusOK, wGetAdmin.Code,
		"admin must get 200 on alice's project; body=%s", wGetAdmin.Body.String())
}

// TestHandleProjects_LegacyUnownedAccessible verifies that projects with owner=""
// (legacy/shared) are readable by any authenticated user. This is the back-compat rule.
// BDD: Given a project with no owner (legacy data),
// When any user reads it,
// Then 200.
// Traces to: SEC-2 back-compat rule
func TestHandleProjects_LegacyUnownedAccessible(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create a project without a user context (dev-mode bypass → owner="").
	body := `{"name":"SharedProject"}`
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(body))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/projects"
	// No user context injected → callerIdentity returns ("", admin) in dev-mode bypass.
	api.HandleProjects(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code)
	var proj gen.Project
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &proj))
	projID := proj.Id

	// Any user (e.g. "bob") can read an unowned project.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projID, nil)
	rGet.URL.Path = "/api/v1/projects/" + projID
	rGet = rGet.WithContext(
		contextWithUserRole(rGet.Context(), "bob", config.UserRoleUser))
	api.HandleProjects(wGet, rGet)
	assert.Equal(t, http.StatusOK, wGet.Code,
		"unowned project must be visible to any user; body=%s", wGet.Body.String())
}

// TestHandleProjects_OwnerImmutableOnPut verifies that an owner field in a PUT body
// is ignored — the stored owner is never changed.
// BDD: Given alice owns project P,
// When alice sends PUT with {"owner":"bob"},
// Then the returned project still has owner="alice".
// Traces to: SEC-2
func TestHandleProjects_OwnerImmutableOnPut(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Alice creates project.
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/projects",
		strings.NewReader(`{"name":"AliceOwned"}`))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/projects"
	rPost = rPost.WithContext(
		contextWithUserRole(rPost.Context(), "alice", config.UserRoleUser))
	api.HandleProjects(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code)
	var proj gen.Project
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &proj))

	// Alice PUTs — the request body does not contain owner (immutable field).
	// Even if a malicious client included it, the handler would ignore it.
	wPut := httptest.NewRecorder()
	rPut := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+proj.Id,
		strings.NewReader(`{"name":"AliceOwnedRenamed"}`))
	rPut.Header.Set("Content-Type", "application/json")
	rPut.URL.Path = "/api/v1/projects/" + proj.Id
	rPut = rPut.WithContext(
		contextWithUserRole(rPut.Context(), "alice", config.UserRoleUser))
	api.HandleProjects(wPut, rPut)
	require.Equal(t, http.StatusOK, wPut.Code, "alice PUT must return 200")
	var updated gen.Project
	require.NoError(t, json.Unmarshal(wPut.Body.Bytes(), &updated))
	require.NotNil(t, updated.Owner, "owner must still be set after PUT")
	assert.Equal(t, "alice", *updated.Owner, "owner must remain alice after PUT")
}

// ---------------------------------------------------------------------------
// Repository URL scheme validation tests (SEC-5)
// ---------------------------------------------------------------------------

// TestHandleProjects_RepositorySchemeValidation_POST verifies SEC-5: POST rejects
// non-http/https repository URLs and accepts valid ones.
// Traces to: SEC-5
func TestHandleProjects_RepositorySchemeValidation_POST(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	cases := []struct {
		name     string
		repoURL  string
		wantCode int
	}{
		{"http accepted", "http://github.com/foo/bar", http.StatusCreated},
		{"https accepted", "https://github.com/foo/bar", http.StatusCreated},
		{"empty accepted", "", http.StatusCreated},
		{"javascript rejected", "javascript:alert(1)", http.StatusBadRequest},
		{"data rejected", "data:text/html,<h1>x</h1>", http.StatusBadRequest},
		{"ftp rejected", "ftp://example.com/repo", http.StatusBadRequest},
		{"no scheme rejected", "github.com/foo/bar", http.StatusBadRequest},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body string
			if tc.repoURL == "" {
				body = fmt.Sprintf(`{"name":"RepoTest%d"}`, i)
			} else {
				body = fmt.Sprintf(`{"name":"RepoTest%d","repository":%q}`, i, tc.repoURL)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/projects"
			api.HandleProjects(w, r)
			assert.Equal(t, tc.wantCode, w.Code,
				"[%s] POST with repository=%q must return %d; body=%s",
				tc.name, tc.repoURL, tc.wantCode, w.Body.String())
		})
	}
}

// TestHandleProjects_RepositorySchemeValidation_PUT verifies SEC-5: PUT rejects
// non-http/https repository URLs.
// Traces to: SEC-5
func TestHandleProjects_RepositorySchemeValidation_PUT(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "RepoPUTProject", "")

	// Valid https update.
	wOK := httptest.NewRecorder()
	rOK := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+projID,
		strings.NewReader(`{"repository":"https://github.com/ok/repo"}`))
	rOK.Header.Set("Content-Type", "application/json")
	rOK.URL.Path = "/api/v1/projects/" + projID
	api.HandleProjects(wOK, rOK)
	assert.Equal(t, http.StatusOK, wOK.Code, "PUT with https URL must return 200; body=%s", wOK.Body.String())

	// Invalid scheme — javascript.
	wBad := httptest.NewRecorder()
	rBad := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+projID,
		strings.NewReader(`{"repository":"javascript:alert(1)"}`))
	rBad.Header.Set("Content-Type", "application/json")
	rBad.URL.Path = "/api/v1/projects/" + projID
	api.HandleProjects(wBad, rBad)
	assert.Equal(t, http.StatusBadRequest, wBad.Code,
		"PUT with javascript: URL must return 400; body=%s", wBad.Body.String())
}
