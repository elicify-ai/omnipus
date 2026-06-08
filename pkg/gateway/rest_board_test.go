//go:build !cgo

// BDD: board tasks REST API tests.
// Traces to: FR-002 (GTD board CRUD), FR-002 (workflow task exclusion), FR-002 (MEDIUM-5 task_count fix).

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// createBoardTaskViaAPI is a helper that POSTs a board task and returns its id + wire struct.
func createBoardTaskViaAPI(t *testing.T, api *restAPI, name, status string) gen.BoardTask {
	t.Helper()
	var statusField string
	if status != "" {
		statusField = fmt.Sprintf(`,"status":%q`, status)
	}
	body := fmt.Sprintf(`{"name":%q%s}`, name, statusField)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "create board task must return 201; body=%s", w.Body.String())
	var task gen.BoardTask
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &task))
	require.NotEmpty(t, task.Id)
	return task
}

// writeWorkflowTaskFile writes a taskstore (workflow) task file — no "name" field, status=queued.
func writeWorkflowTaskFile(t *testing.T, homePath, taskID, projectID string) {
	t.Helper()
	tasksDir := filepath.Join(homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	data := fmt.Sprintf(
		`{"id":%q,"title":"workflow task","status":"queued","project_id":%q,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		taskID, projectID,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, taskID+".json"), []byte(data), 0o600))
}

// TestHandleBoardTasks_ListEmpty verifies GET /api/v1/board/tasks returns empty list on fresh dir.
// BDD: Given no board tasks exist,
// When GET /api/v1/board/tasks is called,
// Then 200 with {"items":[],"total":0}.
// Traces to: FR-002
func TestHandleBoardTasks_ListEmpty(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks", nil)
	r.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(w, r)

	require.Equal(t, http.StatusOK, w.Code, "GET /board/tasks on fresh dir must return 200")
	var resp struct {
		Items []any `json:"items"`
		Total int   `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Items, "items must be empty on fresh dir")
	assert.Equal(t, 0, resp.Total, "total must be 0 on fresh dir")
}

// TestHandleBoardTasks_CreateAndGet verifies POST creates a board task and GET retrieves it.
// BDD: Given a valid CreateBoardTask request,
// When POST /api/v1/board/tasks is called,
// Then 201 with id, name="Fix bug", status="inbox", non-zero created_at.
// When GET /api/v1/board/tasks/{id} is called,
// Then 200 with same data.
// Traces to: FR-002
func TestHandleBoardTasks_CreateAndGet(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// POST → 201.
	task := createBoardTaskViaAPI(t, api, "Fix bug", "inbox")
	assert.Equal(t, "Fix bug", task.Name, "name must match request")
	assert.Equal(t, gen.BoardTaskStatus("inbox"), task.Status, "status must be inbox")
	assert.False(t, task.CreatedAt.IsZero(), "created_at must not be zero")

	// GET /api/v1/board/tasks/{id} → 200 same data.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+task.Id, nil)
	rGet.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wGet, rGet)

	require.Equal(t, http.StatusOK, wGet.Code, "GET /board/tasks/{id} must return 200")
	var got gen.BoardTask
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	assert.Equal(t, task.Id, got.Id, "GET must return the same task id")
	assert.Equal(t, "Fix bug", got.Name, "GET must return the correct name")
	assert.Equal(t, gen.BoardTaskStatus("inbox"), got.Status, "GET must return correct status")
}

// TestHandleBoardTasks_Update verifies PUT /api/v1/board/tasks/{id} updates the task.
// BDD: Given an existing board task,
// When PUT /api/v1/board/tasks/{id} with {"status":"active"},
// Then 200 with status=active.
// When PUT to nonexistent ID,
// Then 404.
// Traces to: FR-002
func TestHandleBoardTasks_Update(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	task := createBoardTaskViaAPI(t, api, "UpdatableTask", "inbox")

	// PUT {"status":"active"} → 200.
	wUp := httptest.NewRecorder()
	rUp := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
		strings.NewReader(`{"status":"active"}`))
	rUp.Header.Set("Content-Type", "application/json")
	rUp.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wUp, rUp)

	require.Equal(t, http.StatusOK, wUp.Code, "PUT /board/tasks/{id} must return 200; body=%s", wUp.Body.String())
	var updated gen.BoardTask
	require.NoError(t, json.Unmarshal(wUp.Body.Bytes(), &updated))
	assert.Equal(t, gen.BoardTaskStatus("active"), updated.Status, "status must be updated to active")

	// Differentiation test: a second PUT with a different status returns a different result.
	wUp2 := httptest.NewRecorder()
	rUp2 := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
		strings.NewReader(`{"status":"done"}`))
	rUp2.Header.Set("Content-Type", "application/json")
	rUp2.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wUp2, rUp2)
	require.Equal(t, http.StatusOK, wUp2.Code)
	var updated2 gen.BoardTask
	require.NoError(t, json.Unmarshal(wUp2.Body.Bytes(), &updated2))
	assert.Equal(t, gen.BoardTaskStatus("done"), updated2.Status, "second PUT must reflect new status")
	assert.NotEqual(t, updated.Status, updated2.Status, "different inputs must produce different outputs")

	// PUT nonexistent → 404.
	wNot := httptest.NewRecorder()
	rNot := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/01JXNOTEXISTENT00000000000",
		strings.NewReader(`{"status":"active"}`))
	rNot.Header.Set("Content-Type", "application/json")
	rNot.URL.Path = "/api/v1/board/tasks/01JXNOTEXISTENT00000000000"
	api.HandleBoardTasks(wNot, rNot)
	assert.Equal(t, http.StatusNotFound, wNot.Code, "PUT /board/tasks/nonexistent must return 404")
}

// TestHandleBoardTasks_Delete_Returns204 verifies DELETE /api/v1/board/tasks/{id} returns 204.
// BDD: Given an existing board task,
// When DELETE /api/v1/board/tasks/{id} is called,
// Then 204.
// When GET same ID is called,
// Then 404.
// Traces to: FR-002
func TestHandleBoardTasks_Delete_Returns204(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	task := createBoardTaskViaAPI(t, api, "DeleteMe", "inbox")

	// DELETE → 204.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/board/tasks/"+task.Id, nil)
	rDel.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wDel, rDel)
	assert.Equal(t, http.StatusNoContent, wDel.Code, "DELETE /board/tasks/{id} must return 204")

	// GET after delete → 404.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+task.Id, nil)
	rGet.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wGet, rGet)
	assert.Equal(t, http.StatusNotFound, wGet.Code, "GET /board/tasks/{id} after delete must return 404")
}

// TestHandleBoardTasks_WorkflowTasksExcluded verifies workflow tasks (taskstore) are excluded.
// BDD: Given a taskstore task file (status="queued", no "name" field),
// When GET /api/v1/board/tasks is called,
// Then items does NOT include the workflow task.
// Traces to: FR-002 (GTD-only filter)
func TestHandleBoardTasks_WorkflowTasksExcluded(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Write a workflow task (taskstore entity — no name field, status=queued).
	workflowID := "01JXWORKFLOW000000000001"
	writeWorkflowTaskFile(t, api.homePath, workflowID, "")

	// Also create a real GTD task to confirm GTD tasks DO appear.
	gtdTask := createBoardTaskViaAPI(t, api, "Real GTD Task", "inbox")

	// GET /api/v1/board/tasks → workflow task must NOT appear.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks", nil)
	r.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	for _, item := range resp.Items {
		assert.NotEqual(t, workflowID, item.ID,
			"workflow task (queued status, no name) must not appear in board tasks")
	}

	// GTD task must appear.
	foundGTD := false
	for _, item := range resp.Items {
		if item.ID == gtdTask.Id {
			foundGTD = true
		}
	}
	assert.True(t, foundGTD, "real GTD task must appear in board tasks list")
}

// TestHandleBoardTasks_FilterByStatus verifies ?status= filter works.
// BDD: Given tasks with status inbox and active,
// When GET /api/v1/board/tasks?status=inbox is called,
// Then only inbox tasks returned.
// Traces to: FR-002
func TestHandleBoardTasks_FilterByStatus(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	inboxTask := createBoardTaskViaAPI(t, api, "InboxItem", "inbox")
	_ = createBoardTaskViaAPI(t, api, "ActiveItem", "active")

	// GET ?status=inbox → only inbox task.
	wInbox := httptest.NewRecorder()
	rInbox := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks?status=inbox", nil)
	rInbox.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wInbox, rInbox)

	require.Equal(t, http.StatusOK, wInbox.Code)
	var resp struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(wInbox.Body.Bytes(), &resp))

	for _, item := range resp.Items {
		assert.Equal(t, "inbox", item.Status, "status filter must exclude non-inbox tasks")
	}
	foundInbox := false
	for _, item := range resp.Items {
		if item.ID == inboxTask.Id {
			foundInbox = true
		}
	}
	assert.True(t, foundInbox, "inbox task must appear when ?status=inbox")
}

// TestHandleBoardTasks_Create_NameRequired verifies POST without name returns 400.
// BDD: Given a POST body with no name,
// When POST /api/v1/board/tasks is called,
// Then 400.
// Traces to: FR-002
func TestHandleBoardTasks_Create_NameRequired(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks",
		strings.NewReader(`{"status":"inbox"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "POST /board/tasks without name must return 400")
}

// TestHandleBoardTasks_TaskCount_ExcludesWorkflowTasks verifies project task_count
// only counts GTD tasks, not workflow tasks.
// BDD: Given a project with one GTD task and one workflow task,
// When GET /api/v1/projects/{id} is called,
// Then task_count == 1 (only GTD task counted).
// Traces to: FR-002, MEDIUM-5 fix
func TestHandleBoardTasks_TaskCount_ExcludesWorkflowTasks(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create a project.
	projID := createProjectViaAPI(t, api, "TaskCountProject", "")

	// Write a GTD task for the project.
	gtdTaskID := "01JXGTDCOUNT0000000000001"
	writeTaskFile(t, api.homePath, gtdTaskID, projID, "inbox")

	// Write a workflow task for the same project (taskstore entity — no name, status=queued).
	workflowTaskID := "01JXWFCOUNT00000000000001"
	tasksDir := filepath.Join(api.homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	workflowData := fmt.Sprintf(
		`{"id":%q,"title":"workflow task","status":"queued","project_id":%q,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		workflowTaskID, projID,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, workflowTaskID+".json"), []byte(workflowData), 0o600))

	// GET /api/v1/projects/{id} → task_count must be 1 (only GTD task counted).
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projID, nil)
	rGet.URL.Path = "/api/v1/projects/" + projID
	api.HandleProjects(wGet, rGet)

	require.Equal(t, http.StatusOK, wGet.Code)
	var proj gen.Project
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &proj))
	assert.Equal(t, 1, proj.TaskCount,
		"task_count must be 1 (GTD only); workflow tasks must not be counted")
}
