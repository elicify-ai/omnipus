//go:build !cgo

// BDD: project REST API tests.
// Traces to: FR-001 (projects CRUD), FR-007 (cascade delete), FR-008 (sessions sub-resource).

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

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
)

// writeTaskFile writes a minimal task JSON file for use in cascade-delete tests.
func writeTaskFile(t *testing.T, homePath, taskID, projectID, status string) {
	t.Helper()
	tasksDir := filepath.Join(homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	data := fmt.Sprintf(
		`{"id":%q,"name":"Task","status":%q,"project_id":%q,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		taskID, status, projectID,
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
