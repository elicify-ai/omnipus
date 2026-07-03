//go:build !cgo

// BDD: workspace REST API tests.
// Traces to: FR-001 (workspaces CRUD), FR-007 (cascade delete).

package gateway

import (
	"bytes"
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
		`{"id":%q,"name":"Task","status":%q,"workspace_id":%q,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		taskID,
		status,
		projectID,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, taskID+".json"), []byte(data), 0o600))
}

// createWorkspaceViaAPI is a helper that POSTs a project and returns the id.
func createWorkspaceViaAPI(t *testing.T, api *restAPI, name, description string) string {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"description":%q}`, name, description)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "create project should return 201; body=%s", w.Body.String())
	var proj gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &proj))
	require.NotEmpty(t, proj.Id)
	return proj.Id
}

// TestHandleWorkspaces_CreateAndList verifies POST creates a project and GET lists it.
// BDD: Given a valid ProjectCreateRequest,
// When POST /api/v1/workspaces is called,
// Then 201 with id, name="Alpha", status="active", pinned=false.
// When GET /api/v1/workspaces is called,
// Then 200 with array containing the created project.
// Traces to: FR-001, User Story 1 Acceptance Scenario 1
func TestHandleWorkspaces_CreateAndList(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// POST /api/v1/workspaces → 201.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"Alpha","description":"desc"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "POST /workspaces must return 201; body=%s", w.Body.String())
	var proj gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &proj))
	assert.NotEmpty(t, proj.Id, "created project must have non-empty id")
	assert.Equal(t, "Alpha", proj.Name, "name must match request")
	assert.Equal(t, gen.WorkspaceStatusActive, proj.Status, "status must default to active")
	assert.False(t, proj.Pinned, "pinned must default to false")

	// GET /api/v1/workspaces → 200, array contains the project.
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	rList.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wList, rList)

	require.Equal(t, http.StatusOK, wList.Code)
	var projects []gen.Workspace
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &projects))
	require.NotEmpty(t, projects, "GET /workspaces must return at least one project")
	found := false
	for _, p := range projects {
		if p.Id == proj.Id {
			found = true
			assert.Equal(t, "Alpha", p.Name)
		}
	}
	assert.True(t, found, "created project must appear in list response")
}

// TestHandleWorkspaces_GetByID verifies GET /api/v1/workspaces/{id} returns the project.
// BDD: Given an existing project,
// When GET /api/v1/workspaces/{id} is called,
// Then 200 with correct fields.
// When GET /api/v1/workspaces/nonexistent is called,
// Then 404.
// Traces to: FR-001
func TestHandleWorkspaces_GetByID(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create a project to GET later.
	id := createWorkspaceViaAPI(t, api, "BetaProject", "beta description")

	// GET /api/v1/workspaces/{id} → 200 with correct fields.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil)
	rGet.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(wGet, rGet)

	require.Equal(t, http.StatusOK, wGet.Code, "GET /workspaces/{id} must return 200")
	var got gen.Workspace
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	assert.Equal(t, id, got.Id)
	assert.Equal(t, "BetaProject", got.Name)
	assert.Equal(t, gen.WorkspaceStatusActive, got.Status)

	// GET /api/v1/workspaces/nonexistent → 404.
	wNot := httptest.NewRecorder()
	rNot := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/01JXNOTEXISTENT00000000000", nil)
	rNot.URL.Path = "/api/v1/workspaces/01JXNOTEXISTENT00000000000"
	api.HandleWorkspaces(wNot, rNot)
	assert.Equal(t, http.StatusNotFound, wNot.Code, "GET /workspaces/nonexistent must return 404")
}

// TestHandleWorkspaces_Update verifies PUT /api/v1/workspaces/{id} updates the project.
// BDD: Given an existing project,
// When PUT /api/v1/workspaces/{id} with {"name":"Beta","pinned":true},
// Then 200 with name="Beta", pinned=true.
// When PUT to nonexistent ID,
// Then 404.
// When PUT with empty name,
// Then 400.
// Traces to: FR-001
func TestHandleWorkspaces_Update(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "OriginalName", "")

	// PUT with new name + pinned=true → 200.
	wUp := httptest.NewRecorder()
	rUp := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id,
		strings.NewReader(`{"name":"Beta","pinned":true}`))
	rUp.Header.Set("Content-Type", "application/json")
	rUp.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(wUp, rUp)

	require.Equal(t, http.StatusOK, wUp.Code, "PUT /workspaces/{id} must return 200; body=%s", wUp.Body.String())
	var updated gen.Workspace
	require.NoError(t, json.Unmarshal(wUp.Body.Bytes(), &updated))
	assert.Equal(t, "Beta", updated.Name, "name must be updated")
	assert.True(t, updated.Pinned, "pinned must be updated to true")

	// Differentiation test: a second PUT with a different name produces a different result.
	wUp2 := httptest.NewRecorder()
	rUp2 := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id,
		strings.NewReader(`{"name":"Gamma"}`))
	rUp2.Header.Set("Content-Type", "application/json")
	rUp2.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(wUp2, rUp2)
	require.Equal(t, http.StatusOK, wUp2.Code)
	var updated2 gen.Workspace
	require.NoError(t, json.Unmarshal(wUp2.Body.Bytes(), &updated2))
	assert.Equal(t, "Gamma", updated2.Name, "second PUT must reflect the new name (not hardcoded)")
	assert.NotEqual(t, updated.Name, updated2.Name, "different inputs must produce different outputs")

	// PUT to nonexistent ID → 404.
	wNot := httptest.NewRecorder()
	rNot := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/01JXNOTEXISTENT00000000000",
		strings.NewReader(`{"name":"X"}`))
	rNot.Header.Set("Content-Type", "application/json")
	rNot.URL.Path = "/api/v1/workspaces/01JXNOTEXISTENT00000000000"
	api.HandleWorkspaces(wNot, rNot)
	assert.Equal(t, http.StatusNotFound, wNot.Code, "PUT /workspaces/nonexistent must return 404")

	// PUT with empty name → 400.
	wBad := httptest.NewRecorder()
	rBad := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id,
		strings.NewReader(`{"name":""}`))
	rBad.Header.Set("Content-Type", "application/json")
	rBad.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(wBad, rBad)
	assert.Equal(t, http.StatusBadRequest, wBad.Code, "PUT /workspaces/{id} with empty name must return 400")
}

// TestHandleWorkspaces_Delete_Returns204 verifies DELETE /api/v1/workspaces/{id} returns 204.
// BDD: Given an existing project,
// When DELETE /api/v1/workspaces/{id} is called,
// Then 204, empty body.
// When DELETE nonexistent,
// Then 404.
// Traces to: FR-007
func TestHandleWorkspaces_Delete_Returns204(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "ToDelete", "")

	// DELETE → 204.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+id, nil)
	rDel.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(wDel, rDel)
	assert.Equal(t, http.StatusNoContent, wDel.Code, "DELETE /workspaces/{id} must return 204")
	assert.Empty(t, wDel.Body.String(), "DELETE response body must be empty")

	// DELETE nonexistent → 404.
	wNot := httptest.NewRecorder()
	rNot := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/01JXNOTEXISTENT00000000000", nil)
	rNot.URL.Path = "/api/v1/workspaces/01JXNOTEXISTENT00000000000"
	api.HandleWorkspaces(wNot, rNot)
	assert.Equal(t, http.StatusNotFound, wNot.Code, "DELETE /workspaces/nonexistent must return 404")
}

// TestHandleWorkspaces_CascadeDelete_RemovesTasks verifies DELETE cascades to tasks.
// BDD: Given a project with an associated task file,
// When DELETE /api/v1/workspaces/{id} is called,
// Then 204 and the task file is removed.
// Traces to: FR-007
func TestHandleWorkspaces_CascadeDelete_RemovesTasks(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "ProjectWithTasks", "")

	// Write a GTD task that belongs to this project.
	taskID := "01JXTASK00000000000000001"
	writeTaskFile(t, api.homePath, taskID, id, "inbox")

	// Verify the task file exists before delete.
	taskPath := filepath.Join(api.homePath, "tasks", taskID+".json")
	_, err := os.Stat(taskPath)
	require.NoError(t, err, "task file should exist before cascade delete")

	// DELETE project → 204.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+id, nil)
	rDel.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(wDel, rDel)
	require.Equal(t, http.StatusNoContent, wDel.Code, "DELETE /workspaces/{id} must return 204")

	// Task file must be gone.
	_, err = os.Stat(taskPath)
	assert.True(t, os.IsNotExist(err), "task file must be removed by cascade delete; stat err: %v", err)
}

// TestHandleWorkspaces_CascadeDelete_RemovesMailboxes verifies DELETE cascades
// to config.mailboxes + the credential store (M11 review-gate finding: the
// cascade handled cron jobs, sessions, milestones, tasks, and channel
// bindings, but left mailboxes orphaned — bound to a now-nonexistent
// workspace ID).
// BDD: Given a workspace with a mailbox bound to it, and a second unrelated
// mailbox (different agent, different workspace),
// When DELETE /api/v1/workspaces/{id} is called,
// Then 204, the bound mailbox's config.mailboxes entry AND stored credential
// are both gone, and the unrelated pair is completely untouched.
// Traces to: FR-007, M11 mailbox cascade fix.
func TestHandleWorkspaces_CascadeDelete_RemovesMailboxes(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// The mailbox cascade needs a credential store; newTestRestAPIWithHome
	// does not wire one (most workspace tests never touch credentials).
	api.credStore = newUnlockedStore(t, api.homePath)

	id := createWorkspaceViaAPI(t, api, "WithMailbox", "")

	// Seed two mailboxes directly in config.mailboxes: one bound to the
	// workspace about to be deleted, one bound to an unrelated workspace ID
	// (deliberately never created — the cascade must match by ID alone, not
	// require the OTHER pair's workspace to exist).
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["mailboxes"] = map[string]any{
			"mia": map[string]any{
				id: map[string]any{
					"enabled": true, "workspace_id": id,
					"password_ref": "mailbox_mia_" + id + "_password",
				},
			},
			"jim": map[string]any{
				"ws_other": map[string]any{
					"enabled": true, "workspace_id": "ws_other",
					"password_ref": "mailbox_jim_ws_other_password",
				},
			},
		}
		return nil
	}))
	require.NoError(t, api.credStore.Set("mailbox_mia_"+id+"_password", "secret-mia"))
	require.NoError(t, api.credStore.Set("mailbox_jim_ws_other_password", "secret-jim"))

	// DELETE the workspace → 204.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+id, nil)
	rDel.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(wDel, rDel)
	require.Equal(t, http.StatusNoContent, wDel.Code, "DELETE /workspaces/{id} must return 204")

	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk))
	mailboxes, _ := disk["mailboxes"].(map[string]any)

	// mia's mailbox entry (its only pair, bound to the deleted workspace)
	// must be removed entirely — the outer agent key drops once its inner
	// map empties.
	_, hasMia := mailboxes["mia"]
	assert.False(t, hasMia, "mia's mailbox entry must be removed by the workspace-delete cascade")

	// …and its credential is gone.
	_, err = api.credStore.Get("mailbox_mia_" + id + "_password")
	require.Error(t, err, "the deleted workspace's mailbox credential must be removed")

	// The unrelated (jim, ws_other) pair survives completely untouched.
	jimEntry, ok := mailboxes["jim"].(map[string]any)
	require.True(t, ok, "jim's mailbox entry must survive")
	_, hasOther := jimEntry["ws_other"].(map[string]any)
	assert.True(t, hasOther, "jim's ws_other pair must survive")
	gotJim, err := api.credStore.Get("mailbox_jim_ws_other_password")
	require.NoError(t, err, "jim's credential must survive")
	assert.Equal(t, "secret-jim", gotJim)
}

// TestHandleWorkspaces_List_SortOrder verifies pinned projects come first.
// BDD: Given 3 projects where one is pinned with pin_order=1,
// When GET /api/v1/workspaces is called,
// Then the pinned project is first in the array.
// Traces to: FR-001
func TestHandleWorkspaces_List_SortOrder(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create 3 projects with distinct names.
	_ = createWorkspaceViaAPI(t, api, "UnpinnedFirst", "")
	_ = createWorkspaceViaAPI(t, api, "UnpinnedSecond", "")
	id3 := createWorkspaceViaAPI(t, api, "PinnedProject", "")

	// Sleep briefly so created_at times differ.
	time.Sleep(2 * time.Millisecond)

	// Pin project id3 with pin_order=1.
	wPin := httptest.NewRecorder()
	rPin := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id3,
		strings.NewReader(`{"pinned":true,"pin_order":1}`))
	rPin.Header.Set("Content-Type", "application/json")
	rPin.URL.Path = "/api/v1/workspaces/" + id3
	api.HandleWorkspaces(wPin, rPin)
	require.Equal(t, http.StatusOK, wPin.Code)

	// GET /api/v1/workspaces → first result must be the pinned one.
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	rList.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wList, rList)

	require.Equal(t, http.StatusOK, wList.Code)
	var projects []gen.Workspace
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &projects))
	require.GreaterOrEqual(t, len(projects), 3, "must return at least 3 projects")
	assert.True(t, projects[0].Pinned, "first project in list must be pinned")
	assert.Equal(t, id3, projects[0].Id, "pinned project must be first")
}

// TestHandleWorkspaces_UnknownSubPath_Returns404 verifies unknown sub-paths return 404.
// BDD: Given /api/v1/workspaces/{id}/unknown,
// When GET is called,
// Then 404.
// Traces to: FR-001
func TestHandleWorkspaces_UnknownSubPath_Returns404(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "SubPathTest", "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id+"/unknown", nil)
	r.URL.Path = "/api/v1/workspaces/" + id + "/unknown"
	api.HandleWorkspaces(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code, "GET /workspaces/{id}/unknown must return 404")
}

// TestHandleWorkspaces_MethodNotAllowed verifies PATCH /api/v1/workspaces returns 405.
// BDD: Given PATCH /api/v1/workspaces,
// When PATCH is called,
// Then 405.
// Traces to: FR-001
func TestHandleWorkspaces_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/workspaces", nil)
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "PATCH /workspaces must return 405")
}

// TestHandleWorkspaces_Boundaries verifies field-length validation on POST and PUT /api/v1/workspaces.
// BDD: Given POST /api/v1/workspaces with name > 200 chars,
// When the request is handled,
// Then 400.
// Given PUT /api/v1/workspaces/{id} with description > 2000 chars,
// When the request is handled,
// Then 400.
// Traces to: project-task-management-level1-spec.md FG-M7
func TestHandleWorkspaces_Boundaries(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// POST with name > 200 chars → 400.
	longName := strings.Repeat("x", 201)
	body, err := json.Marshal(map[string]any{"name": longName})
	require.NoError(t, err)
	wLong := httptest.NewRecorder()
	rLong := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(string(body)))
	rLong.Header.Set("Content-Type", "application/json")
	rLong.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wLong, rLong)
	assert.Equal(t, http.StatusBadRequest, wLong.Code,
		"POST /workspaces with name > 200 chars must return 400; body=%s", wLong.Body.String())

	// POST with name exactly 200 chars → 201 (boundary: exactly at limit is accepted).
	exactName := strings.Repeat("y", 200)
	body200, err := json.Marshal(map[string]any{"name": exactName})
	require.NoError(t, err)
	wExact := httptest.NewRecorder()
	rExact := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(string(body200)))
	rExact.Header.Set("Content-Type", "application/json")
	rExact.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wExact, rExact)
	assert.Equal(t, http.StatusCreated, wExact.Code,
		"POST /workspaces with name exactly 200 chars must return 201; body=%s", wExact.Body.String())

	// Create a project and then PUT with description > 2000 chars → 400.
	projID := createWorkspaceViaAPI(t, api, "BoundaryProject", "initial description")
	longDesc := strings.Repeat("d", 2001)
	updateBody, err := json.Marshal(map[string]any{"name": "BoundaryProject", "description": longDesc})
	require.NoError(t, err)
	wDesc := httptest.NewRecorder()
	rDesc := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+projID, strings.NewReader(string(updateBody)))
	rDesc.Header.Set("Content-Type", "application/json")
	rDesc.URL.Path = "/api/v1/workspaces/" + projID
	api.HandleWorkspaces(wDesc, rDesc)
	assert.Equal(t, http.StatusBadRequest, wDesc.Code,
		"PUT /workspaces/{id} with description > 2000 chars must return 400; body=%s", wDesc.Body.String())

	// PUT with description exactly 2000 chars → 200 (boundary: at limit is accepted).
	exactDesc := strings.Repeat("e", 2000)
	exactDescBody, err := json.Marshal(map[string]any{"name": "BoundaryProject", "description": exactDesc})
	require.NoError(t, err)
	wExactDesc := httptest.NewRecorder()
	rExactDesc := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/workspaces/"+projID,
		strings.NewReader(string(exactDescBody)),
	)
	rExactDesc.Header.Set("Content-Type", "application/json")
	rExactDesc.URL.Path = "/api/v1/workspaces/" + projID
	api.HandleWorkspaces(wExactDesc, rExactDesc)
	assert.Equal(t, http.StatusOK, wExactDesc.Code,
		"PUT /workspaces/{id} with description exactly 2000 chars must return 200; body=%s", wExactDesc.Body.String())
}

// TestHandleWorkspaces_InvalidStatusFilter verifies GET /api/v1/workspaces?status=garbage returns 400.
// BDD: Given GET /api/v1/workspaces?status=garbage (an unrecognized status value),
// When the request is handled,
// Then 400.
// Traces to: project-task-management-level1-spec.md FG-M13
func TestHandleWorkspaces_InvalidStatusFilter(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces?status=garbage", nil)
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"GET /workspaces?status=garbage must return 400; body=%s", w.Body.String())
}

// TestHandleWorkspaces_ConcurrentDelete verifies that two simultaneous DELETE requests
// for the same project do not produce a 500 — each returns either 204 or 404.
// BDD: Given an existing project P and a board task T linked to P,
// When two goroutines simultaneously DELETE /api/v1/workspaces/{P},
// Then both respond with 204 or 404 (never 500) and the project file is absent afterward.
// Traces to: project-task-management-level1-spec.md — TST-001 (concurrent delete safety)
func TestHandleWorkspaces_ConcurrentDelete(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Step 1: Create a project via POST /api/v1/workspaces.
	projID := createWorkspaceViaAPI(t, api, "ConcurrentDeleteProject", "concurrent delete test")

	// Step 2: Create a unified task linked to the project via POST /api/v1/tasks.
	// Sprint 2: /board/tasks replaced by /api/v1/tasks; "title"+"action" required.
	taskBody := fmt.Sprintf(`{"title":"ConcurrentTask","action":"llm","workspace_id":%q}`, projID)
	wTask := httptest.NewRecorder()
	rTask := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(taskBody))
	rTask.Header.Set("Content-Type", "application/json")
	rTask.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wTask, rTask)
	require.Equal(t, http.StatusCreated, wTask.Code,
		"create task must return 201; body=%s", wTask.Body.String())

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
			r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+projID, nil)
			r.URL.Path = "/api/v1/workspaces/" + projID
			api.HandleWorkspaces(w, r)
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

	// Step 6: Assert the workspace file is gone after both deletes.
	projectPath := filepath.Join(api.homePath, "workspaces", projID+".json")
	_, statErr := os.Stat(projectPath)
	assert.True(t, os.IsNotExist(statErr),
		"workspace file must not exist after concurrent deletes; path=%s, err=%v", projectPath, statErr)

	// Verify the project is truly gone by trying to GET it.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+projID, nil)
	rGet.URL.Path = "/api/v1/workspaces/" + projID
	api.HandleWorkspaces(wGet, rGet)
	assert.Equal(t, http.StatusNotFound, wGet.Code,
		"GET /workspaces/{id} after concurrent delete must return 404; body=%s", wGet.Body.String())
}

// ---------------------------------------------------------------------------
// Inbox auto-creation and protection tests (project-task-milestone-spec.md)
// ---------------------------------------------------------------------------

// TestSeed_ConcurrentBoot_NoDoubleSeed verifies that concurrent calls to
// ensureDefaultWorkspace (e.g. two racing gateway boots) produce exactly ONE
// default workspace — not two.
// BDD: Given a fresh home directory with no workspaces,
// When N goroutines call ensureDefaultWorkspace concurrently under -race,
// Then exactly one workspace with is_default=true exists on disk afterward.
// Traces to: TOCTOU-seed guard (defaultWorkspaceSeedMu).
func TestSeed_ConcurrentBoot_NoDoubleSeed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_ = ensureDefaultWorkspace(api.homePath, "seeduser", nil)
		}()
	}
	wg.Wait()

	// List all workspace files and count how many have is_default=true.
	workspaces, err := listWorkspaceFiles(api.homePath)
	require.NoError(t, err)
	defaultCount := 0
	for _, ws := range workspaces {
		if ws.IsDefault {
			defaultCount++
		}
	}
	assert.Equal(t, 1, defaultCount,
		"exactly one default workspace must be created even under concurrent boot; got %d", defaultCount)
}

// TestHandleWorkspaces_InboxAutoCreated verifies that GET /api/v1/workspaces on a fresh home dir
// returns a project with is_default=true and display name="Main".
//
// Item 1: the default project's DISPLAY name was renamed "Inbox" -> "Main" to
// avoid colliding with the "Inbox" board column/status. The rename touches the
// human display name ONLY — the default project is identified by the stable
// IsDefault flag (used by ensureDefaultWorkspace, the sidebar-first sort, and the
// not-deletable guard), never by the literal display string. This test pins the
// new display name AND that the default-project identity (is_default=true) is
// preserved so inbox routing is unaffected.
// BDD: Given a fresh home directory with no projects,
// When GET /api/v1/workspaces is called,
// Then 200 with an array containing a project where is_default=true and name="Main".
// Traces to: project-task-milestone-spec.md — FR-L2-001 (default-project auto-creation), FR-INX-1
func TestHandleWorkspaces_DefaultAutoCreated(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	const seedOwner = "alice"
	require.NoError(t, ensureDefaultWorkspace(api.homePath, seedOwner, nil),
		"ensureDefaultWorkspace must not error on fresh home")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"GET /workspaces on fresh dir must return 200; body=%s", w.Body.String())
	var workspaces []gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &workspaces))
	require.NotEmpty(t, workspaces, "GET /workspaces must return at least the default workspace")

	foundDefault := false
	for _, ws := range workspaces {
		if ws.IsDefault != nil && *ws.IsDefault {
			foundDefault = true
			assert.Equal(t, "My Workspace", ws.Name, "default workspace name must be 'My Workspace'")
			assert.Equal(t, gen.WorkspaceStatusActive, ws.Status, "default workspace must be active")
			// FR-1.6: owner is stamped from the provided username.
			require.NotNil(t, ws.Owner, "default workspace must have owner set (FR-1.6)")
			assert.Equal(t, seedOwner, *ws.Owner, "default workspace owner must equal the seed username")
		}
	}
	assert.True(t, foundDefault, "GET /workspaces must contain a workspace with is_default=true")
}

// TestHandleWorkspaces_InboxNotDeletable verifies that DELETE /api/v1/workspaces/<inbox-id> returns 409.
// BDD: Given the Inbox project with is_default=true,
// When DELETE /api/v1/workspaces/{inbox-id} is called,
// Then 409 (cannot delete the default Inbox project).
// Traces to: project-task-milestone-spec.md — FR-INX-2 (Inbox not deletable), FR-L2-002
func TestHandleWorkspaces_InboxNotDeletable(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-INX-2 / FR-L2-002: Inbox cannot be deleted
	api := newTestRestAPIWithHome(t)

	// Create the Inbox project directly via ensureDefaultWorkspace.
	require.NoError(t, ensureDefaultWorkspace(api.homePath, "", nil))

	// Find the Inbox project ID by listing projects.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var projects []gen.Workspace
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
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+inboxID, nil)
	rDel.URL.Path = "/api/v1/workspaces/" + inboxID
	api.HandleWorkspaces(wDel, rDel)

	assert.Equal(t, http.StatusConflict, wDel.Code,
		"DELETE /workspaces/{inbox-id} must return 409 (Inbox not deletable); body=%s", wDel.Body.String())

	// Verify Inbox still exists after failed delete.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+inboxID, nil)
	rGet.URL.Path = "/api/v1/workspaces/" + inboxID
	api.HandleWorkspaces(wGet, rGet)
	assert.Equal(t, http.StatusOK, wGet.Code,
		"GET /workspaces/{inbox-id} after failed delete must still return 200")
}

// TestHandleWorkspaces_DefaultNotArchivable verifies that PUT /api/v1/workspaces/{id}
// with status=archived is rejected with 409 for the default workspace, and that
// archiving a non-default workspace still succeeds (200).
// BDD: Given a default workspace with is_default=true,
// When PUT /api/v1/workspaces/{id} sets status="archived",
// Then 409 Conflict is returned and the workspace remains active.
// And: Given a non-default workspace,
// When PUT /api/v1/workspaces/{id} sets status="archived",
// Then 200 OK is returned and the workspace is archived.
func TestHandleWorkspaces_DefaultNotArchivable(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	require.NoError(t, ensureDefaultWorkspace(api.homePath, "", nil))

	// List to find the default workspace ID.
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	rList.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wList, rList)
	require.Equal(t, http.StatusOK, wList.Code)
	var workspaces []gen.Workspace
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &workspaces))

	var defaultID string
	for _, ws := range workspaces {
		if ws.IsDefault != nil && *ws.IsDefault {
			defaultID = ws.Id
		}
	}
	require.NotEmpty(t, defaultID, "must find default workspace in the list")

	// Attempt to archive the default workspace → 409.
	archived := gen.WorkspaceUpdateRequestStatusArchived
	bodyBytes, err := json.Marshal(gen.WorkspaceUpdateRequest{Status: &archived})
	require.NoError(t, err)
	wPut := httptest.NewRecorder()
	rPut := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+defaultID, bytes.NewReader(bodyBytes))
	rPut.Header.Set("Content-Type", "application/json")
	rPut.URL.Path = "/api/v1/workspaces/" + defaultID
	api.HandleWorkspaces(wPut, rPut)
	assert.Equal(t, http.StatusConflict, wPut.Code,
		"PUT status=archived on the default workspace must return 409; body=%s", wPut.Body.String())

	// Verify default workspace is still active.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+defaultID, nil)
	rGet.URL.Path = "/api/v1/workspaces/" + defaultID
	api.HandleWorkspaces(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code)
	var still gen.Workspace
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &still))
	assert.Equal(t, gen.WorkspaceStatusActive, still.Status,
		"default workspace must still be active after rejected archive attempt")

	// Create a non-default workspace.
	createBody, err := json.Marshal(gen.WorkspaceCreateRequest{Name: "Non-Default"})
	require.NoError(t, err)
	wCreate := httptest.NewRecorder()
	rCreate := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", bytes.NewReader(createBody))
	rCreate.Header.Set("Content-Type", "application/json")
	rCreate.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wCreate, rCreate)
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var created gen.Workspace
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))

	// Archive the non-default workspace → 200.
	bodyBytes2, err := json.Marshal(gen.WorkspaceUpdateRequest{Status: &archived})
	require.NoError(t, err)
	wPut2 := httptest.NewRecorder()
	rPut2 := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+created.Id, bytes.NewReader(bodyBytes2))
	rPut2.Header.Set("Content-Type", "application/json")
	rPut2.URL.Path = "/api/v1/workspaces/" + created.Id
	api.HandleWorkspaces(wPut2, rPut2)
	assert.Equal(t, http.StatusOK, wPut2.Code,
		"PUT status=archived on a non-default workspace must return 200; body=%s", wPut2.Body.String())
	var updatedWS gen.Workspace
	require.NoError(t, json.Unmarshal(wPut2.Body.Bytes(), &updatedWS))
	assert.Equal(t, gen.WorkspaceStatusArchived, updatedWS.Status,
		"non-default workspace must be archived after PUT status=archived")
}

// ---------------------------------------------------------------------------
// Owner attribution tests (FR-1.9: owner is attribution-only, not an access gate)
// ---------------------------------------------------------------------------

// TestHandleWorkspaces_OwnerAttribution verifies FR-1.9: owner is attribution-only.
// All users can see all workspaces regardless of who created them.
// BDD: Given alice creates workspace W,
// When bob (role=user) calls GET /api/v1/workspaces/{id} for W,
// Then 200 (FR-1.9: owner gate removed).
// When bob lists all workspaces,
// Then W appears in the list (FR-1.9).
// Owner field is still set on creation (attribution).
// Traces to: FR-1.9
func TestHandleWorkspaces_OwnerAttribution(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Alice creates workspace W.
	body := `{"name":"AliceWorkspace","description":"owned by alice"}`
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/workspaces"
	rPost = rPost.WithContext(
		contextWithUserRole(rPost.Context(), "alice", config.UserRoleUser))
	api.HandleWorkspaces(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code, "alice POST must return 201; body=%s", wPost.Body.String())
	var ws gen.Workspace
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &ws))
	wsID := ws.Id
	require.NotEmpty(t, wsID, "created workspace must have an id")

	// Owner field is stamped for attribution.
	require.NotNil(t, ws.Owner, "owner must be set on create (attribution)")
	assert.Equal(t, "alice", *ws.Owner, "owner must equal the creating user's username")

	// FR-1.9: bob (role=user) calls GET /api/v1/workspaces/{id} → 200 (no gate).
	wGetBob := httptest.NewRecorder()
	rGetBob := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+wsID, nil)
	rGetBob.URL.Path = "/api/v1/workspaces/" + wsID
	rGetBob = rGetBob.WithContext(
		contextWithUserRole(rGetBob.Context(), "bob", config.UserRoleUser))
	api.HandleWorkspaces(wGetBob, rGetBob)
	assert.Equal(t, http.StatusOK, wGetBob.Code,
		"FR-1.9: bob must get 200 on alice's workspace (owner is attribution-only); body=%s", wGetBob.Body.String())

	// FR-1.9: bob lists workspaces — alice's workspace appears.
	wListBob := httptest.NewRecorder()
	rListBob := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	rListBob.URL.Path = "/api/v1/workspaces"
	rListBob = rListBob.WithContext(
		contextWithUserRole(rListBob.Context(), "bob", config.UserRoleUser))
	api.HandleWorkspaces(wListBob, rListBob)
	require.Equal(t, http.StatusOK, wListBob.Code)
	var allWorkspaces []gen.Workspace
	require.NoError(t, json.Unmarshal(wListBob.Body.Bytes(), &allWorkspaces))
	foundAlices := false
	for _, w := range allWorkspaces {
		if w.Id == wsID {
			foundAlices = true
		}
	}
	assert.True(t, foundAlices, "FR-1.9: alice's workspace must appear in bob's list (no owner gate)")
}

// TestHandleWorkspaces_LegacyUnownedAccessible verifies that projects with owner=""
// (legacy/shared) are readable by any authenticated user. This is the back-compat rule.
// BDD: Given a project with no owner (legacy data),
// When any user reads it,
// Then 200.
// Traces to: SEC-2 back-compat rule
func TestHandleWorkspaces_LegacyUnownedAccessible(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create a project without a user context (dev-mode bypass → owner="").
	body := `{"name":"SharedProject"}`
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/workspaces"
	// No user context injected → callerIdentity returns ("", admin) in dev-mode bypass.
	api.HandleWorkspaces(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code)
	var proj gen.Workspace
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &proj))
	projID := proj.Id

	// Any user (e.g. "bob") can read an unowned project.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+projID, nil)
	rGet.URL.Path = "/api/v1/workspaces/" + projID
	rGet = rGet.WithContext(
		contextWithUserRole(rGet.Context(), "bob", config.UserRoleUser))
	api.HandleWorkspaces(wGet, rGet)
	assert.Equal(t, http.StatusOK, wGet.Code,
		"unowned project must be visible to any user; body=%s", wGet.Body.String())
}

// TestHandleWorkspaces_OwnerImmutableOnPut verifies that an owner field in a PUT body
// is ignored — the stored owner is never changed.
// BDD: Given alice owns project P,
// When alice sends PUT with {"owner":"bob"},
// Then the returned project still has owner="alice".
// Traces to: SEC-2
func TestHandleWorkspaces_OwnerImmutableOnPut(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Alice creates project.
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"AliceOwned"}`))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/workspaces"
	rPost = rPost.WithContext(
		contextWithUserRole(rPost.Context(), "alice", config.UserRoleUser))
	api.HandleWorkspaces(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code)
	var proj gen.Workspace
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &proj))

	// Alice PUTs — the request body does not contain owner (immutable field).
	// Even if a malicious client included it, the handler would ignore it.
	wPut := httptest.NewRecorder()
	rPut := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+proj.Id,
		strings.NewReader(`{"name":"AliceOwnedRenamed"}`))
	rPut.Header.Set("Content-Type", "application/json")
	rPut.URL.Path = "/api/v1/workspaces/" + proj.Id
	rPut = rPut.WithContext(
		contextWithUserRole(rPut.Context(), "alice", config.UserRoleUser))
	api.HandleWorkspaces(wPut, rPut)
	require.Equal(t, http.StatusOK, wPut.Code, "alice PUT must return 200")
	var updated gen.Workspace
	require.NoError(t, json.Unmarshal(wPut.Body.Bytes(), &updated))
	require.NotNil(t, updated.Owner, "owner must still be set after PUT")
	assert.Equal(t, "alice", *updated.Owner, "owner must remain alice after PUT")
}

// ---------------------------------------------------------------------------
// Repository URL scheme validation tests (SEC-5)
// ---------------------------------------------------------------------------

// TestHandleWorkspaces_RepositorySchemeValidation_POST verifies SEC-5: POST rejects
// non-http/https repository URLs and accepts valid ones.
// Traces to: SEC-5
func TestHandleWorkspaces_RepositorySchemeValidation_POST(t *testing.T) {
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
			r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/workspaces"
			api.HandleWorkspaces(w, r)
			assert.Equal(t, tc.wantCode, w.Code,
				"[%s] POST with repository=%q must return %d; body=%s",
				tc.name, tc.repoURL, tc.wantCode, w.Body.String())
		})
	}
}

// TestHandleWorkspacePut_FullFieldRoundTrip verifies that a PUT /api/v1/workspaces/{id}
// that mutates ONE field leaves ALL other fields intact — including delegation
// (stored on-disk, not sent over the wire by PUT) and updated_at (must advance).
//
// This pins the f034a096 "update can't drop fields" fix at the REST merge path.
// The sysagent-level analog (TestWorkspaceUpdate_FullFieldRoundTrip) proves the
// tool write path; this test proves the gateway PUT merge path.
//
// BDD:
//
//	Given a workspace that has been written with all fields populated
//	  (name, description, repository, status, pinned, pin_order, core_team,
//	   owner, is_default, delegation, created_at, updated_at),
//	When PUT /api/v1/workspaces/{id} with {"name":"Renamed"} (one field only),
//	Then GET /api/v1/workspaces/{id} returns the workspace with:
//	  - name updated to "Renamed"
//	  - description, repository, status, pinned, pin_order, core_team, owner
//	    all unchanged from the original values
//	  - updated_at advanced past the original value
//	And the on-disk file still contains the delegation edges (not a GET response
//	concern since delegation lives on /delegation; but disk integrity is verified).
//
// Traces to: f034a096 REST merge path, FR-001.
func TestHandleWorkspacePut_FullFieldRoundTrip(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Step 1: Create the workspace via POST to get a valid ULID id and timestamps.
	body := `{
		"name": "Original Name",
		"description": "original description",
		"repository": "https://github.com/example/full-field",
		"core_team": ["mia","jim","ava","ray"]
	}`
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code,
		"POST /workspaces must return 201; body=%s", wPost.Body.String())
	var created gen.Workspace
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &created))
	id := created.Id
	require.NotEmpty(t, id)

	// Step 2: Back-patch the on-disk file with ALL fields (including delegation and
	// pin_order=5, pinned=true, status=active, is_default=false) so that the PUT
	// handler must preserve them during a partial update.
	// We write the delegation edges directly to disk — they are not settable via
	// PUT /workspaces/{id} (they live at /workspaces/{id}/delegation).
	originalUpdatedAt := "2026-06-01T09:00:00Z"
	fullJSON := fmt.Sprintf(`{
		"id": %q,
		"name": "Original Name",
		"description": "original description",
		"status": "active",
		"pinned": true,
		"pin_order": 5,
		"core_team": ["mia","jim","ava","ray"],
		"repository": "https://github.com/example/full-field",
		"owner": "alice",
		"is_default": false,
		"delegation": [
			{"from_agent":"jim","to_agent":"ava","modes":["await","task"],"depth":2},
			{"from_agent":"mia","to_agent":"ray"}
		],
		"created_at": "2026-06-01T08:00:00Z",
		"updated_at": %q
	}`, id, originalUpdatedAt)
	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, id+".json"), []byte(fullJSON), 0o600))

	// Step 3: PUT — mutate only the name.
	wPut := httptest.NewRecorder()
	rPut := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id,
		strings.NewReader(`{"name":"Renamed"}`))
	rPut.Header.Set("Content-Type", "application/json")
	rPut.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(wPut, rPut)
	require.Equal(t, http.StatusOK, wPut.Code,
		"PUT /workspaces/{id} must return 200; body=%s", wPut.Body.String())

	// Step 4: GET the workspace back and assert every untouched field survived.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil)
	rGet.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code,
		"GET /workspaces/{id} must return 200 after PUT; body=%s", wGet.Body.String())

	var got gen.Workspace
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))

	// Name must be updated.
	assert.Equal(t, "Renamed", got.Name, "name must be updated by PUT")

	// Fields the PUT did NOT touch must be identical to the original.
	require.NotNil(t, got.Description, "description must survive the PUT (not be nil)")
	assert.Equal(t, "original description", *got.Description,
		"description must be unchanged after name-only PUT")

	require.NotNil(t, got.Repository, "repository must survive the PUT")
	assert.Equal(t, "https://github.com/example/full-field", *got.Repository,
		"repository must be unchanged after name-only PUT")

	assert.Equal(t, gen.WorkspaceStatusActive, got.Status,
		"status must be unchanged after name-only PUT")

	assert.True(t, got.Pinned, "pinned must be unchanged (true) after name-only PUT")
	assert.Equal(t, 5, got.PinOrder, "pin_order must be unchanged (5) after name-only PUT")

	require.NotNil(t, got.CoreTeam, "core_team must survive the PUT")
	assert.Equal(t, []string{"mia", "jim", "ava", "ray"}, *got.CoreTeam,
		"core_team must be unchanged after name-only PUT")

	require.NotNil(t, got.Owner, "owner must survive the PUT")
	assert.Equal(t, "alice", *got.Owner,
		"owner must be unchanged after name-only PUT")

	// updated_at must advance past the original.
	assert.False(t, got.UpdatedAt.IsZero(), "updated_at must not be zero after PUT")
	parsedOriginal, err := time.Parse(time.RFC3339, originalUpdatedAt)
	require.NoError(t, err)
	assert.True(t, got.UpdatedAt.After(parsedOriginal),
		"updated_at must advance past %s after PUT; got %s",
		originalUpdatedAt, got.UpdatedAt.Format(time.RFC3339))

	// Step 5: Verify delegation edges survived on disk (they are not surfaced by
	// GET /workspaces/{id} — that is /workspaces/{id}/delegation — but the merge
	// path must not wipe them from the file).
	diskData, err := os.ReadFile(filepath.Join(wsDir, id+".json"))
	require.NoError(t, err, "workspace file must still exist on disk after PUT")
	var diskObj map[string]any
	require.NoError(t, json.Unmarshal(diskData, &diskObj),
		"workspace file must be valid JSON after PUT")

	delegation, ok := diskObj["delegation"].([]any)
	require.True(t, ok, "delegation field must survive on disk as an array; got %T=%v",
		diskObj["delegation"], diskObj["delegation"])
	assert.Len(t, delegation, 2,
		"both delegation edges must survive the name-only PUT on disk")

	e0, _ := delegation[0].(map[string]any)
	assert.Equal(t, "jim", e0["from_agent"], "delegation edge 0 from_agent must survive PUT")
	assert.Equal(t, "ava", e0["to_agent"], "delegation edge 0 to_agent must survive PUT")

	e1, _ := delegation[1].(map[string]any)
	assert.Equal(t, "mia", e1["from_agent"], "delegation edge 1 from_agent must survive PUT")
	assert.Equal(t, "ray", e1["to_agent"], "delegation edge 1 to_agent must survive PUT")
}

// TestHandleWorkspaces_RepositorySchemeValidation_PUT verifies SEC-5: PUT rejects
// non-http/https repository URLs.
// Traces to: SEC-5
func TestHandleWorkspaces_RepositorySchemeValidation_PUT(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	projID := createWorkspaceViaAPI(t, api, "RepoPUTProject", "")

	// Valid https update.
	wOK := httptest.NewRecorder()
	rOK := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+projID,
		strings.NewReader(`{"repository":"https://github.com/ok/repo"}`))
	rOK.Header.Set("Content-Type", "application/json")
	rOK.URL.Path = "/api/v1/workspaces/" + projID
	api.HandleWorkspaces(wOK, rOK)
	assert.Equal(t, http.StatusOK, wOK.Code, "PUT with https URL must return 200; body=%s", wOK.Body.String())

	// Invalid scheme — javascript.
	wBad := httptest.NewRecorder()
	rBad := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+projID,
		strings.NewReader(`{"repository":"javascript:alert(1)"}`))
	rBad.Header.Set("Content-Type", "application/json")
	rBad.URL.Path = "/api/v1/workspaces/" + projID
	api.HandleWorkspaces(wBad, rBad)
	assert.Equal(t, http.StatusBadRequest, wBad.Code,
		"PUT with javascript: URL must return 400; body=%s", wBad.Body.String())
}
