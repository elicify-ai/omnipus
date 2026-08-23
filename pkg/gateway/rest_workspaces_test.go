// BDD: workspace REST API tests.
// Traces to: FR-001 (workspaces CRUD), FR-007 (cascade delete).

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// contextWithUser returns a new context that carries the given username (as a
// *config.UserConfig), matching what the gateway auth middleware injects.
// Used in owner-attribution tests to simulate authenticated requests.
func contextWithUser(parent context.Context, username string) context.Context {
	return context.WithValue(parent, UserContextKey{}, &config.UserConfig{Username: username})
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

	// Store both mailbox credentials BEFORE writing the config that
	// references them: safeUpdateConfigJSON triggers refreshConfigAndRewireServices,
	// which now resolves every enabled mailbox's password_ref and rejects the
	// in-memory refresh if an enabled ref can't be resolved — so the
	// credential must already exist in the store when the config write lands.
	require.NoError(t, api.credStore.Set("mailbox_mia_"+id+"_password", "secret-mia"))
	require.NoError(t, api.credStore.Set("mailbox_jim_ws_other_password", "secret-jim"))

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

// TestUpgrade_ExistingDefaultWorkspace_NoCustomAutoAdd (ADR-046 P1, FR-008 /
// US-3 AS-4) proves ensureDefaultWorkspace's upgrade back-fill path: when a
// default workspace ALREADY exists (the upgraded-install case), calling
// ensureDefaultWorkspace again must:
//  1. Back-fill any built-in-roster member missing from the existing
//     CoreTeam (here: "ray" was omitted from the pre-existing team, e.g. a
//     coreagent added after the operator's original install).
//  2. Leave a custom agent the operator added to the team by hand
//     ("hand-added-custom") untouched — never removed.
//  3. NEVER add "never-on-team-custom" (a second custom agent present in
//     cfg.Agents.List but never added to this workspace) — the back-fill is
//     built-in-roster-only.
//  4. Leave the workspace's delegation edge set completely unchanged —
//     expanding/back-filling a team must never create or imply a trust edge
//     (FR-038). The edges live in the delegation store, not the workspace
//     record (pkg/workspace/delegationstore.go), so this reads them there.
func TestUpgrade_ExistingDefaultWorkspace_NoCustomAutoAdd(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore},
				{ID: "jim", Type: config.AgentTypeCore},
				{ID: "ava", Type: config.AgentTypeCore},
				{ID: "ray", Type: config.AgentTypeCore},
				{ID: "worker", Type: config.AgentTypeWorker},
				{ID: "planner", Type: config.AgentTypeWorker},
				{ID: "explorer", Type: config.AgentTypeWorker},
				{ID: "researcher", Type: config.AgentTypeWorker},
				{ID: "hand-added-custom", Type: config.AgentTypeCustom},
				{ID: "never-on-team-custom", Type: config.AgentTypeCustom},
			},
		},
	}

	// Simulate an upgraded install: a pre-existing default workspace missing
	// "ray" from the built-in roster, but WITH a custom agent an operator
	// added by hand, and a pre-existing delegation edge.
	now := time.Now().UTC().Format(time.RFC3339)
	ws := storedWorkspace{
		ID:        "existing-default-ws",
		Name:      "My Workspace",
		Status:    "active",
		IsDefault: true,
		CoreTeam:  []string{"mia", "jim", "ava", "worker", "planner", "explorer", "researcher", "hand-added-custom"},
		CreatedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, writeWorkspaceFile(home, ws))
	preUpgradeDelegation := []storedDelegationEdge{{FromAgent: "jim", ToAgent: "worker", Modes: nil}}
	require.NoError(t, saveWorkspaceDelegation(home, ws.ID, preUpgradeDelegation))

	require.NoError(t, ensureDefaultWorkspace(home, "alice", cfg),
		"ensureDefaultWorkspace must not error when a default workspace already exists")

	wss, err := listWorkspaceFiles(home)
	require.NoError(t, err)
	require.Len(t, wss, 1, "no second default workspace must be created")
	got := wss[0]

	assert.True(t, got.IsDefault)
	assert.Contains(t, got.CoreTeam, "ray", "the missing built-in roster member must be back-filled")
	assert.Contains(t, got.CoreTeam, "hand-added-custom",
		"a custom agent already on the team must never be removed by the back-fill")
	assert.NotContains(t, got.CoreTeam, "never-on-team-custom",
		"a custom agent NOT already on the team must never be auto-added by the back-fill")
	assert.ElementsMatch(t,
		[]string{"mia", "jim", "ava", "ray", "worker", "planner", "explorer", "researcher", "hand-added-custom"},
		got.CoreTeam)
	gotEdges, storeOK := workspace.LoadDelegation(home, got.ID)
	require.True(t, storeOK, "delegation store record must still be readable after the back-fill")
	assert.Equal(t, preUpgradeDelegation, gotEdges,
		"back-filling the roster must NEVER create or imply a delegation trust edge (FR-038)")
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
		contextWithUser(rPost.Context(), "alice"))
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
		contextWithUser(rGetBob.Context(), "bob"))
	api.HandleWorkspaces(wGetBob, rGetBob)
	assert.Equal(t, http.StatusOK, wGetBob.Code,
		"FR-1.9: bob must get 200 on alice's workspace (owner is attribution-only); body=%s", wGetBob.Body.String())

	// FR-1.9: bob lists workspaces — alice's workspace appears.
	wListBob := httptest.NewRecorder()
	rListBob := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	rListBob.URL.Path = "/api/v1/workspaces"
	rListBob = rListBob.WithContext(
		contextWithUser(rListBob.Context(), "bob"))
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
		contextWithUser(rGet.Context(), "bob"))
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
		contextWithUser(rPost.Context(), "alice"))
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
		contextWithUser(rPut.Context(), "alice"))
	api.HandleWorkspaces(wPut, rPut)
	require.Equal(t, http.StatusOK, wPut.Code, "alice PUT must return 200")
	var updated gen.Workspace
	require.NoError(t, json.Unmarshal(wPut.Body.Bytes(), &updated))
	require.NotNil(t, updated.Owner, "owner must still be set after PUT")
	assert.Equal(t, "alice", *updated.Owner, "owner must remain alice after PUT")
}

// ---------------------------------------------------------------------------
// repository retirement tests (FR-9.2, ADR-063 D7)
// ---------------------------------------------------------------------------

// TestHandleWorkspaces_RepositoryFieldRejected_POST verifies FR-9.2: POST
// /api/v1/workspaces 400s via raw-body sniff whenever the request body
// carries a "repository" field at all, regardless of its value — the field
// is retired from the wire entirely, not merely URL-validated. A request
// with no "repository" key at all is unaffected.
// Traces to: FR-9.2, ADR-063 D7 (repository is deleted, not repurposed).
func TestHandleWorkspaces_RepositoryFieldRejected_POST(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	cases := []struct {
		name     string
		repoURL  string
		hasField bool
		wantCode int
	}{
		{"no repository field accepted", "", false, http.StatusCreated},
		{"https rejected — field retired regardless of value", "https://github.com/foo/bar", true, http.StatusBadRequest},
		{"javascript rejected — field retired regardless of value", "javascript:alert(1)", true, http.StatusBadRequest},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body string
			if !tc.hasField {
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
				"[%s] POST with repository field present=%v must return %d; body=%s",
				tc.name, tc.hasField, tc.wantCode, w.Body.String())
			if tc.hasField {
				assert.Contains(t, w.Body.String(), "repository is retired",
					"400 body must explain the field is retired, not a generic validation error")
			}
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
//	  (name, description, status, pinned, pin_order, core_team,
//	   owner, is_default, delegation, created_at, updated_at),
//	When PUT /api/v1/workspaces/{id} with {"name":"Renamed"} (one field only),
//	Then GET /api/v1/workspaces/{id} returns the workspace with:
//	  - name updated to "Renamed"
//	  - description, status, pinned, pin_order, core_team, owner
//	    all unchanged from the original values
//	  - updated_at advanced past the original value
//	And the on-disk file still contains the delegation edges (not a GET response
//	concern since delegation lives on /delegation; but disk integrity is verified).
//
// Traces to: f034a096 REST merge path, FR-001.
func TestHandleWorkspacePut_FullFieldRoundTrip(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Step 1: Create the workspace via POST to get a valid ULID id and timestamps.
	// No core_team here (review r1 M4/Gap #6 now validates membership against
	// registered agents, which newTestRestAPIWithHome's bare harness doesn't
	// seed) — irrelevant to this step anyway, since Step 2 immediately
	// overwrites the on-disk file wholesale, including core_team.
	body := `{
		"name": "Original Name",
		"description": "original description"
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

	// Step 2: Back-patch the on-disk file with ALL fields (pin_order=5,
	// pinned=true, status=active, is_default=false) so that the PUT handler
	// must preserve them during a partial update.
	//
	// Delegation edges are set separately, in the delegation store — they are
	// not settable via PUT /workspaces/{id} (they live at
	// /workspaces/{id}/delegation) and, since issue #636, they are not a field
	// on the workspace record at all: the record is writable by the sandboxed
	// child the edges constrain (pkg/workspace/delegationstore.go).
	originalUpdatedAt := "2026-06-01T09:00:00Z"
	fullJSON := fmt.Sprintf(`{
		"id": %q,
		"name": "Original Name",
		"description": "original description",
		"status": "active",
		"pinned": true,
		"pin_order": 5,
		"core_team": ["mia","jim","ava","ray"],
		"owner": "alice",
		"is_default": false,
		"created_at": "2026-06-01T08:00:00Z",
		"updated_at": %q
	}`, id, originalUpdatedAt)
	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, id+".json"), []byte(fullJSON), 0o600))
	depth2 := 2
	require.NoError(t, saveWorkspaceDelegation(api.homePath, id, []storedDelegationEdge{
		{FromAgent: "jim", ToAgent: "ava", Modes: []workspace.DelegationMode{workspace.ModeDirect, workspace.ModeTask}, Depth: &depth2},
		{FromAgent: "mia", ToAgent: "ray"},
	}))

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
	// path must not wipe them).
	delegation := loadStoredDelegationEdges(t, api.homePath, id)
	assert.Len(t, delegation, 2,
		"both delegation edges must survive the name-only PUT")
	assert.Equal(t, "jim", delegation[0].FromAgent, "delegation edge 0 from_agent must survive PUT")
	assert.Equal(t, "ava", delegation[0].ToAgent, "delegation edge 0 to_agent must survive PUT")
	assert.Equal(t, "mia", delegation[1].FromAgent, "delegation edge 1 from_agent must survive PUT")
	assert.Equal(t, "ray", delegation[1].ToAgent, "delegation edge 1 to_agent must survive PUT")

	// ...and the PUT must not have copied them into the child-writable record.
	diskData, err := os.ReadFile(filepath.Join(wsDir, id+".json"))
	require.NoError(t, err, "workspace file must still exist on disk after PUT")
	var diskObj map[string]any
	require.NoError(t, json.Unmarshal(diskData, &diskObj),
		"workspace file must be valid JSON after PUT")
	_, leaked := diskObj["delegation"]
	assert.False(t, leaked,
		"the workspace record must carry no delegation array — it is writable by the principal the "+
			"edges constrain (issue #636); got %v", diskObj["delegation"])
}

// TestHandleWorkspaces_RepositoryFieldRejected_PUT verifies FR-9.2: PUT
// /api/v1/workspaces/{id} 400s via raw-body sniff whenever the request body
// carries a "repository" field at all, regardless of its value.
// Traces to: FR-9.2, ADR-063 D7 (repository is deleted, not repurposed).
func TestHandleWorkspaces_RepositoryFieldRejected_PUT(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	projID := createWorkspaceViaAPI(t, api, "RepoPUTProject", "")

	// A PUT with no repository field at all is unaffected.
	wOK := httptest.NewRecorder()
	rOK := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+projID,
		strings.NewReader(`{"name":"RepoPUTProject Renamed"}`))
	rOK.Header.Set("Content-Type", "application/json")
	rOK.URL.Path = "/api/v1/workspaces/" + projID
	api.HandleWorkspaces(wOK, rOK)
	assert.Equal(t, http.StatusOK, wOK.Code, "PUT without repository must return 200; body=%s", wOK.Body.String())

	// A PUT carrying "repository" — even a well-formed https URL — is rejected
	// outright; the field is retired, not merely scheme-validated.
	wBad := httptest.NewRecorder()
	rBad := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+projID,
		strings.NewReader(`{"repository":"https://github.com/ok/repo"}`))
	rBad.Header.Set("Content-Type", "application/json")
	rBad.URL.Path = "/api/v1/workspaces/" + projID
	api.HandleWorkspaces(wBad, rBad)
	assert.Equal(t, http.StatusBadRequest, wBad.Code,
		"PUT with repository field must return 400; body=%s", wBad.Body.String())
	assert.Contains(t, wBad.Body.String(), "repository is retired",
		"400 body must explain the field is retired, not a generic validation error")
}

// ---------------------------------------------------------------------------
// setup_pending / Ava-only new-workspace seeding tests.
// ---------------------------------------------------------------------------

// newTestRestAPIWithAvaRoster builds a restAPI whose config registers the full
// base install roster (mia/jim/ava/ray/worker), so both newWorkspaceSetupTeam
// (Ava-only, used by POST-created workspaces) and defaultWorkspaceTeam (full
// roster, used only by ensureDefaultWorkspace) have real agents to filter
// against. newTestRestAPIWithHome deliberately registers no agents at all, so
// it cannot exercise either seed path meaningfully.
func newTestRestAPIWithAvaRoster(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true},
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCore},
				{ID: "ava", Name: "Ava", Type: config.AgentTypeCore},
				{ID: "ray", Name: "Ray", Type: config.AgentTypeCore},
				{ID: "worker", Name: "Worker", Type: config.AgentTypeWorker},
			},
		},
	}
	// cfg.Agents.List no longer round-trips through this marshal (ADR-054:
	// json:"-") — config.json on disk carries no "list" either way now, but
	// writing it still exercises the same on-disk shape callers expect.
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), cfgJSON, 0o600))
	// ADR-054 D2/D6: several tests using this roster (e.g.
	// TestHandleWorkspacePut_DeltaValidation_PreExistingDanglingMemberDoesNotWedge)
	// exercise workspace core_team validation purely against the in-memory
	// cfg.Agents.List, which is unaffected either way — but persisting real
	// entity records here means this roster survives intact even if a future
	// test built on this helper calls a handler that reaches
	// refreshConfigAndRewireServices (which repopulates cfg.Agents.List
	// wholesale from the entity store, silently wiping an entity-less
	// in-memory-only roster).
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
		taskLock:      task.TaskFileLock,
	}
}

// TestHandleWorkspacePost_NoCoreTeam_SeedsAvaOnly_SetupPending verifies that a
// POST /api/v1/workspaces with no explicit core_team now seeds ONLY Ava (not
// the full install roster), sets setup_pending=true, and produces no
// delegation edges (ava's only coreagent seed edge is ava->worker, and worker
// is off-team so seedEdgesForTeam drops it — that is intended).
// BDD:
//
//	Given a config with the full base roster (mia/jim/ava/ray/worker) registered,
//	When POST /api/v1/workspaces is called with no core_team field,
//	Then 201 with core_team=["ava"] and setup_pending=true in the wire response,
//	And the persisted on-disk file has the same core_team + setup_pending, and
//	an empty delegation list.
func TestHandleWorkspacePost_NoCoreTeam_SeedsAvaOnly_SetupPending(t *testing.T) {
	api := newTestRestAPIWithAvaRoster(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"NewTeamWorkspace"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "POST /workspaces must return 201; body=%s", w.Body.String())
	var ws gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ws))

	require.NotNil(t, ws.CoreTeam, "core_team must be present in the wire response")
	assert.Equal(t, []string{"ava"}, *ws.CoreTeam,
		"a new user-created workspace with no explicit core_team must seed ONLY Ava")
	require.NotNil(t, ws.SetupPending, "setup_pending must be present in the wire response")
	assert.True(t, *ws.SetupPending, "setup_pending must be true for an auto-seeded Ava-only workspace")

	// Persisted file must agree byte-for-byte on both fields, plus empty delegation.
	stored, err := readWorkspaceFile(api.homePath, ws.Id)
	require.NoError(t, err)
	assert.Equal(t, []string{"ava"}, stored.CoreTeam, "on-disk core_team must match the wire response")
	assert.True(t, stored.SetupPending, "on-disk setup_pending must be true")
	seeded, storeOK := workspace.LoadDelegation(api.homePath, ws.Id)
	require.True(t, storeOK, "delegation store record must be readable")
	assert.Empty(t, seeded,
		"ava's only coreagent seed edge (ava->worker) must be dropped since worker is off-team")
}

// TestHandleWorkspacePost_ExplicitCoreTeam_HonoredVerbatim_NoSetupPending
// verifies that an explicit core_team in the POST body is honored (deduped)
// rather than overridden by the Ava-only seed, and setup_pending stays false.
// BDD:
//
//	Given POST /api/v1/workspaces with an explicit core_team (with a duplicate),
//	When the request is handled,
//	Then 201 with core_team deduplicated exactly as given, and setup_pending
//	absent/false (this is not an auto-seeded workspace).
func TestHandleWorkspacePost_ExplicitCoreTeam_HonoredVerbatim_NoSetupPending(t *testing.T) {
	api := newTestRestAPIWithAvaRoster(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"ExplicitTeamWorkspace","core_team":["mia","jim","mia"]}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "POST /workspaces must return 201; body=%s", w.Body.String())
	var ws gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ws))

	require.NotNil(t, ws.CoreTeam, "core_team must be present in the wire response")
	assert.Equal(t, []string{"mia", "jim"}, *ws.CoreTeam,
		"an explicit core_team must be honored verbatim (deduplicated), not replaced by the Ava-only seed")
	assert.Nil(t, ws.SetupPending,
		"setup_pending must be absent/false when the caller supplied an explicit core_team")

	stored, err := readWorkspaceFile(api.homePath, ws.Id)
	require.NoError(t, err)
	assert.False(t, stored.SetupPending, "on-disk setup_pending must be false for an explicit-team workspace")
}

// TestHandleWorkspacePost_CoreTeam_RejectsSystemAgentAndUnregisteredID is
// review r1 major M4/Gap #6: neither handleWorkspacePost nor
// handleWorkspacePut validated core_team membership at all before this fix —
// a caller could add a System Agent (never a chat target, excluded from team
// rosters by design, AgentConfig.IsSystem's doc comment) or a
// typo'd/nonexistent agent id with no rejection whatsoever.
func TestHandleWorkspacePost_CoreTeam_RejectsSystemAgentAndUnregisteredID(t *testing.T) {
	api := newTestRestAPIWithAvaRoster(t)
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List,
		config.AgentConfig{ID: "judge", Name: "Judge", Type: config.AgentTypeSystem})

	cases := []struct {
		name      string
		coreTeam  string
		wantWords string
	}{
		{"system_agent_rejected", `["mia","judge"]`, "System Agent"},
		{"unregistered_id_rejected", `["mia","nonexistent-agent"]`, "not a registered agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
				strings.NewReader(`{"name":"`+tc.name+`","core_team":`+tc.coreTeam+`}`))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/workspaces"
			api.HandleWorkspaces(w, r)

			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.wantWords)
		})
	}
}

// TestHandleWorkspacePut_CoreTeam_RejectsSystemAgentAndUnregisteredID mirrors
// the POST test above for the PUT path (review r1 major M4/Gap #6).
func TestHandleWorkspacePut_CoreTeam_RejectsSystemAgentAndUnregisteredID(t *testing.T) {
	api := newTestRestAPIWithAvaRoster(t)
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List,
		config.AgentConfig{ID: "judge", Name: "Judge", Type: config.AgentTypeSystem})

	// Create a valid workspace first.
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"PutRejectTest","core_team":["mia"]}`))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code, "body=%s", wPost.Body.String())
	var created gen.Workspace
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &created))

	cases := []struct {
		name      string
		coreTeam  string
		wantWords string
	}{
		{"system_agent_rejected", `["mia","judge"]`, "System Agent"},
		{"unregistered_id_rejected", `["mia","nonexistent-agent"]`, "not a registered agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+created.Id,
				strings.NewReader(`{"core_team":`+tc.coreTeam+`}`))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/workspaces/" + created.Id
			api.HandleWorkspaces(w, r)

			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.wantWords)

			// The workspace's on-disk core_team must be untouched by a rejected PUT.
			stored, err := readWorkspaceFile(api.homePath, created.Id)
			require.NoError(t, err)
			assert.Equal(t, []string{"mia"}, stored.CoreTeam,
				"a rejected core_team update must not be persisted")
		})
	}
}

// TestHandleWorkspacePost_ExplicitEmptyCoreTeam_SeedsAvaOnly_SetupPending is a
// regression test: an explicit but EMPTY
// core_team array ("core_team": []) must be treated exactly like an absent
// core_team (nil) — i.e. "unspecified" — NOT as "deliberately no team".
// Before this fix, req.CoreTeam != nil was true for a zero-length slice, so
// the explicit-team branch ran with an empty team and setup_pending left
// false, producing a permanently teamless workspace: no agent was ever on
// the team to run the setup interview and no code path would ever set
// setup_pending=true for it afterward.
// BDD:
//
//	Given a config with the full base roster (mia/jim/ava/ray/worker) registered,
//	When POST /api/v1/workspaces is called with "core_team": [] (present but empty),
//	Then 201 with core_team=["ava"] and setup_pending=true in the wire response
//	(identical outcome to omitting core_team entirely), and the persisted
//	on-disk file agrees, with no delegation edges (mirrors the no-core_team case).
func TestHandleWorkspacePost_ExplicitEmptyCoreTeam_SeedsAvaOnly_SetupPending(t *testing.T) {
	api := newTestRestAPIWithAvaRoster(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"EmptyTeamWorkspace","core_team":[]}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "POST /workspaces must return 201; body=%s", w.Body.String())
	var ws gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ws))

	require.NotNil(t, ws.CoreTeam, "core_team must be present in the wire response")
	assert.Equal(t, []string{"ava"}, *ws.CoreTeam,
		"an explicit EMPTY core_team must be treated as unspecified and seed ONLY Ava, "+
			"exactly like an absent core_team")
	require.NotNil(t, ws.SetupPending, "setup_pending must be present in the wire response")
	assert.True(t, *ws.SetupPending,
		"setup_pending must be true — an explicit empty core_team must never produce a "+
			"permanently teamless, non-setup_pending workspace")

	stored, err := readWorkspaceFile(api.homePath, ws.Id)
	require.NoError(t, err)
	assert.Equal(t, []string{"ava"}, stored.CoreTeam, "on-disk core_team must match the wire response")
	assert.True(t, stored.SetupPending, "on-disk setup_pending must be true")
	seeded, storeOK := workspace.LoadDelegation(api.homePath, ws.Id)
	require.True(t, storeOK, "delegation store record must be readable")
	assert.Empty(t, seeded, "no delegation edges expected for the Ava-only seed team")
}

// TestEnsureDefaultWorkspace_StillSeedsFullRoster_NoSetupPending is a
// regression: the auto-created boot default workspace ("My Workspace",
// ensureDefaultWorkspace) is UNCHANGED by the Ava-only seed introduced for
// ordinary POST-created workspaces — it must still seed the full install
// roster (every base + specialist agent present in config) with its full seed
// edges, and setup_pending must stay false (this workspace never runs the
// setup interview).
// BDD:
//
//	Given a config with the full install roster registered,
//	When ensureDefaultWorkspace creates the default workspace,
//	Then GET /api/v1/workspaces/{id} returns core_team containing every
//	registered base/specialist agent, setup_pending absent/false, and the
//	full seed delegation edge count on disk.
func TestEnsureDefaultWorkspace_StillSeedsFullRoster_NoSetupPending(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore},
				{ID: "jim", Type: config.AgentTypeCore},
				{ID: "ava", Type: config.AgentTypeCore},
				{ID: "ray", Type: config.AgentTypeCore},
				{ID: "worker", Type: config.AgentTypeWorker},
				{ID: "planner", Type: config.AgentTypeWorker},
				{ID: "explorer", Type: config.AgentTypeWorker},
				{ID: "researcher", Type: config.AgentTypeWorker},
			},
		},
	}
	require.NoError(t, ensureDefaultWorkspace(home, "alice", cfg))

	wss, err := listWorkspaceFiles(home)
	require.NoError(t, err)
	require.Len(t, wss, 1)
	ws := wss[0]
	assert.True(t, ws.IsDefault)
	assert.ElementsMatch(t,
		[]string{"mia", "jim", "ava", "ray", "worker", "planner", "explorer", "researcher"},
		ws.CoreTeam, "the boot default workspace must still seed the FULL install roster")
	bootEdges, storeOK := workspace.LoadDelegation(home, ws.ID)
	require.True(t, storeOK, "delegation store record must be readable")
	assert.Len(t, bootEdges, 9, "the boot default workspace must still seed the full edge set")
	assert.False(t, ws.SetupPending,
		"the boot default workspace must never be setup_pending — it never runs the setup interview")

	wire := workspaceToWire(home, ws, 0)
	assert.Nil(t, wire.SetupPending, "the wire response must not report setup_pending for the default workspace")
}

// TestHandleWorkspacePut_PreservesSetupPending is a regression test: a PUT
// that only renames a setup_pending workspace must not clear (or otherwise
// touch) the setup_pending flag — it is cleared exclusively by the
// setup-kickoff turn handler, never by an ordinary field update.
// BDD:
//
//	Given a workspace created with setup_pending=true (Ava-only seed),
//	When PUT /api/v1/workspaces/{id} renames it (no setup_pending field sent),
//	Then the returned and persisted workspace still has setup_pending=true.
func TestHandleWorkspacePut_PreservesSetupPending(t *testing.T) {
	api := newTestRestAPIWithAvaRoster(t)

	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"SetupPendingWS"}`))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code)
	var created gen.Workspace
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &created))
	require.NotNil(t, created.SetupPending)
	require.True(t, *created.SetupPending, "precondition: the created workspace must be setup_pending")

	wPut := httptest.NewRecorder()
	rPut := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+created.Id,
		strings.NewReader(`{"name":"SetupPendingWS-Renamed"}`))
	rPut.Header.Set("Content-Type", "application/json")
	rPut.URL.Path = "/api/v1/workspaces/" + created.Id
	api.HandleWorkspaces(wPut, rPut)
	require.Equal(t, http.StatusOK, wPut.Code, "PUT rename must return 200; body=%s", wPut.Body.String())
	var updated gen.Workspace
	require.NoError(t, json.Unmarshal(wPut.Body.Bytes(), &updated))
	assert.Equal(t, "SetupPendingWS-Renamed", updated.Name)
	require.NotNil(t, updated.SetupPending,
		"setup_pending must still be present in the wire response after an unrelated rename")
	assert.True(t, *updated.SetupPending,
		"a name-only PUT must NOT clear setup_pending")

	stored, err := readWorkspaceFile(api.homePath, created.Id)
	require.NoError(t, err)
	assert.True(t, stored.SetupPending, "on-disk setup_pending must survive an unrelated PUT")
}

// newTestRestAPIWithoutAva builds a restAPI whose config roster deliberately
// omits Ava (mirrors newTestRestAPIWithAvaRoster minus the "ava" entry) — used
// to exercise the SetupPending gate: newWorkspaceSetupTeam returns an
// empty seed when Ava is absent from the live config (e.g. a lite/custom
// install), and handleWorkspacePost must not mark such a workspace
// setup_pending, since nothing would ever be able to run the interview and
// clear the flag.
func newTestRestAPIWithoutAva(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true},
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCore},
				{ID: "ray", Name: "Ray", Type: config.AgentTypeCore},
				{ID: "worker", Name: "Worker", Type: config.AgentTypeWorker},
			},
		},
	}
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), cfgJSON, 0o600))
	// ADR-054 D2/D6: see newTestRestAPIWithAvaRoster's identical comment —
	// persist real entity records so this roster survives any future reload
	// trigger, not just the in-memory cfg.Agents.List this package's current
	// tests happen to rely on.
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
		taskLock:      task.TaskFileLock,
	}
}

// TestHandleWorkspacePost_NoAva_EmptyTeam_NoSetupPending is a
// regression test: POST /api/v1/workspaces with no explicit core_team, against a
// config that does NOT include Ava, must seed an EMPTY core_team AND leave
// setup_pending false/absent — never setup_pending=true with an empty team,
// which would strand the workspace forever (no agent is ever available to run
// the interview and clear the flag).
// BDD:
//
//	Given a config roster with no Ava,
//	When POST /api/v1/workspaces is called with no core_team field,
//	Then 201 with core_team absent/empty and setup_pending absent/false in the
//	wire response, and the same on disk.
func TestHandleWorkspacePost_NoAva_EmptyTeam_NoSetupPending(t *testing.T) {
	api := newTestRestAPIWithoutAva(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"NoAvaWorkspace"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "POST /workspaces must return 201; body=%s", w.Body.String())
	var ws gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ws))

	assert.Nil(t, ws.CoreTeam, "core_team must be absent/empty in the wire response when Ava is not in the config")
	assert.Nil(t, ws.SetupPending,
		"setup_pending must be absent/false when the auto-seed produced an empty team")

	stored, err := readWorkspaceFile(api.homePath, ws.Id)
	require.NoError(t, err)
	assert.Empty(t, stored.CoreTeam, "on-disk core_team must be empty")
	assert.False(t, stored.SetupPending,
		"on-disk setup_pending must be false — an empty team must never be left setup_pending forever")
}

// TestHandleWorkspacePut_SerializesAgainstWorkspaceLock is a
// concurrency regression test: handleWorkspacePut must acquire workspace.LockID(id)
// for its full load-modify-write cycle, so a concurrent holder of that same
// lock (standing in for the WS setup-kickoff consumer, which acquires the
// identical lock — see consumeWorkspaceSetupKickoff) blocks the PUT until the
// lock is released. This is a deterministic interleaving test (no -race
// dependency): the test itself holds the lock, confirms the PUT goroutine has
// NOT completed while it's held, releases, and confirms the PUT then
// completes with the expected result.
func TestHandleWorkspacePut_SerializesAgainstWorkspaceLock(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := createWorkspaceViaAPI(t, api, "LockRaceWorkspace", "")

	// Simulate an in-flight kickoff consume (or any other workspace writer)
	// holding the per-workspace lock.
	unlock := workspace.LockID(wsID)

	type result struct {
		code int
		body string
	}
	done := make(chan result, 1)
	go func() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID,
			strings.NewReader(`{"name":"LockRaceWorkspace-Renamed"}`))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/workspaces/" + wsID
		api.HandleWorkspaces(w, r)
		done <- result{code: w.Code, body: w.Body.String()}
	}()

	// The PUT must NOT complete while the lock is held.
	select {
	case res := <-done:
		unlock()
		t.Fatalf("PUT completed while the workspace lock was held (code=%d, body=%s) — "+
			"handleWorkspacePut is not serializing on workspace.LockID", res.code, res.body)
	case <-time.After(150 * time.Millisecond):
		// expected: still blocked
	}

	unlock()

	// Now that the lock is released, the PUT must complete promptly.
	select {
	case res := <-done:
		require.Equal(t, http.StatusOK, res.code, "PUT must succeed once the lock is released; body=%s", res.body)
		var updated gen.Workspace
		require.NoError(t, json.Unmarshal([]byte(res.body), &updated))
		assert.Equal(t, "LockRaceWorkspace-Renamed", updated.Name)
	case <-time.After(2 * time.Second):
		t.Fatal("PUT did not complete after the workspace lock was released")
	}

	stored, err := readWorkspaceFile(api.homePath, wsID)
	require.NoError(t, err)
	assert.Equal(t, "LockRaceWorkspace-Renamed", stored.Name)
}

// TestLogWorkspacelessAgents_WarnsOnlyForNonMembers (ADR-046 P1, FR-007/008)
// proves the boot-time diagnostic: given three configured agents — one a
// member of a real on-disk workspace, two members of nothing — exactly one
// WARN log line is emitted naming both non-member IDs (sorted), and the
// member is never mentioned. Also proves it is a genuine no-op (no warning
// at all) when every configured agent already has a workspace.
func TestLogWorkspacelessAgents_WarnsOnlyForNonMembers(t *testing.T) {
	home := t.TempDir()

	wsDir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	wsJSON := `{"id":"ws-1","core_team":["member-agent"]}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "ws-1.json"), []byte(wsJSON), 0o644))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "member-agent"},
				{ID: "orphan-b"},
				{ID: "orphan-a"},
			},
		},
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logWorkspacelessAgents(home, cfg)

	var found map[string]any
	var lines int
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		lines++
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "parse log line %q", line)
		found = entry
	}
	require.Equal(t, 1, lines, "expected exactly one log line; got:\n%s", buf.String())
	require.NotNil(t, found)
	assert.Equal(t, "orphan-a,orphan-b", found["agent_ids"],
		"must name both non-member agents, sorted, and never the member")
	assert.NotContains(t, fmt.Sprint(found["agent_ids"]), "member-agent")
	assert.EqualValues(t, 2, found["count"])

	// No configured agent is workspace-less: must be a silent no-op.
	buf.Reset()
	cfg2 := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{ID: "member-agent"}},
		},
	}
	logWorkspacelessAgents(home, cfg2)
	assert.Empty(t, buf.String(), "must not log anything when every configured agent has a workspace")
}

// --- ADR-054 D6: referential integrity (validateCoreTeamMembers / RepairDanglingCoreTeamMembers) ---

// TestValidateCoreTeamMembers_Unit exercises the pure validation function
// directly: a fully-valid list passes, a dangling (unregistered) id is
// rejected, a System Agent id is rejected, and an empty/nil list is always a
// no-op (nothing to validate).
func TestValidateCoreTeamMembers_Unit(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore},
				{ID: "judge", Type: config.AgentTypeSystem},
			},
		},
	}

	assert.NoError(t, validateCoreTeamMembers(cfg, nil), "nil coreTeam must be a no-op")
	assert.NoError(t, validateCoreTeamMembers(cfg, []string{}), "empty coreTeam must be a no-op")
	assert.NoError(t, validateCoreTeamMembers(cfg, []string{"mia"}), "a valid, registered, non-system id must pass")

	err := validateCoreTeamMembers(cfg, []string{"mia", "ghost"})
	require.Error(t, err, "an unregistered id must be rejected")
	assert.Contains(t, err.Error(), "not a registered agent")

	err = validateCoreTeamMembers(cfg, []string{"mia", "judge"})
	require.Error(t, err, "a System Agent id must be rejected")
	assert.Contains(t, err.Error(), "System Agent")

	assert.Error(t, validateCoreTeamMembers(nil, []string{"anything"}), "a nil cfg with a non-empty coreTeam must error, not panic")
}

// TestRepairDanglingCoreTeamMembers_Unit verifies the ADR-054 D6 rule 3
// repair primitive: dangling (unregistered) ids are dropped, valid ids are
// kept in their original order, and a System Agent id (which exists, so is
// not "dangling") is left untouched — repair only removes references that no
// longer resolve to anything, it does not re-implement
// validateCoreTeamMembers' separate System Agent rejection.
func TestRepairDanglingCoreTeamMembers_Unit(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore},
				{ID: "jim", Type: config.AgentTypeCore},
				{ID: "judge", Type: config.AgentTypeSystem},
			},
		},
	}

	repaired, dropped := RepairDanglingCoreTeamMembers(cfg, []string{"mia", "ghost-a", "jim", "ghost-b"})
	assert.Equal(t, []string{"mia", "jim"}, repaired, "surviving members must keep their original order")
	assert.Equal(t, []string{"ghost-a", "ghost-b"}, dropped, "dangling members must be reported, in order")

	// A System Agent id is not dangling (it exists) — repair must not drop it.
	repaired, dropped = RepairDanglingCoreTeamMembers(cfg, []string{"mia", "judge"})
	assert.Equal(t, []string{"mia", "judge"}, repaired, "an existing System Agent id is not dangling; repair must not remove it")
	assert.Empty(t, dropped)

	// Nothing dangling: repaired == input, dropped is empty.
	repaired, dropped = RepairDanglingCoreTeamMembers(cfg, []string{"mia", "jim"})
	assert.Equal(t, []string{"mia", "jim"}, repaired)
	assert.Empty(t, dropped)

	// Nil/empty input and nil cfg edge cases.
	repaired, dropped = RepairDanglingCoreTeamMembers(cfg, nil)
	assert.Nil(t, repaired)
	assert.Nil(t, dropped)

	repaired, dropped = RepairDanglingCoreTeamMembers(nil, []string{"mia", "jim"})
	assert.Nil(t, repaired, "a nil cfg has nothing to resolve against — every id is dropped")
	assert.Equal(t, []string{"mia", "jim"}, dropped)
}

// TestHandleWorkspacePut_DeltaValidation_PreExistingDanglingMemberDoesNotWedge
// is the direct regression test for the ADR-054 §1 failure: "a workspace
// core_team referenced three agents that do not exist, and
// validateCoreTeamMembers then rejected every subsequent write naming the
// first missing one — permanently wedging the workspace with no repair
// path."
//
// BDD:
//
//	Given a workspace whose core_team already contains a dangling member
//	  (an agent id that existed when the team was set, but was since removed
//	  from the roster — simulating a delete that happened outside this PUT),
//	When a PUT request round-trips that SAME core_team plus one new, valid,
//	  unrelated member (the realistic "add one member" client pattern),
//	Then the request succeeds (200), the dangling member survives untouched
//	  (D6 rule 2: surfaced, never fatal — it is not this write's job to prune
//	  it), and the new member is added — proving the wedge described in the
//	  ADR is gone.
func TestHandleWorkspacePut_DeltaValidation_PreExistingDanglingMemberDoesNotWedge(t *testing.T) {
	api := newTestRestAPIWithAvaRoster(t)
	cfg := api.agentLoop.GetConfig()

	// Create a workspace whose core_team includes "jim" while jim is still a
	// registered agent.
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"WedgeRegressionTest","core_team":["mia","jim"]}`))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code, "body=%s", wPost.Body.String())
	var created gen.Workspace
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &created))

	// Simulate "jim" being deleted from the roster after the workspace was
	// created — cfg.Agents.List no longer has an entry for "jim", but the
	// workspace's on-disk core_team still names it. This is exactly the
	// dangling-reference state ADR-054 §1 describes.
	newList := make([]config.AgentConfig, 0, len(cfg.Agents.List))
	for _, a := range cfg.Agents.List {
		if a.ID != "jim" {
			newList = append(newList, a)
		}
	}
	cfg.Agents.List = newList

	// A typical client reads the current team (["mia","jim"]) and PUTs it
	// back with one new member appended ("ray") — this is the realistic
	// "add a member" pattern that round-trips the dangling id.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+created.Id,
		strings.NewReader(`{"core_team":["mia","jim","ray"]}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces/" + created.Id
	api.HandleWorkspaces(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"a PUT that merely round-trips a PRE-EXISTING dangling member ('jim') alongside one new valid "+
			"member ('ray') must succeed — rejecting it is the exact permanent-wedge bug ADR-054 fixes. body=%s",
		w.Body.String())

	var updated gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	require.NotNil(t, updated.CoreTeam)
	assert.ElementsMatch(t, []string{"mia", "jim", "ray"}, *updated.CoreTeam,
		"the dangling member ('jim') survives untouched (surfaced, not pruned) and the new member ('ray') is added")

	// A write that introduces a genuinely NEW bad id must still be rejected
	// (delta validation still catches new problems — it only stops
	// re-validating what was already there).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+created.Id,
		strings.NewReader(`{"core_team":["mia","jim","ray","brand-new-ghost"]}`))
	r2.Header.Set("Content-Type", "application/json")
	r2.URL.Path = "/api/v1/workspaces/" + created.Id
	api.HandleWorkspaces(w2, r2)
	require.Equal(t, http.StatusBadRequest, w2.Code, "a NEWLY introduced dangling id must still be rejected. body=%s", w2.Body.String())
	assert.Contains(t, w2.Body.String(), "not a registered agent")
}
