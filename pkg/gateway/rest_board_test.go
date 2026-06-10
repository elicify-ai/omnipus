//go:build !cgo

// BDD: board tasks REST API tests.
// Traces to: FR-002 (GTD board CRUD), FR-002 (workflow task exclusion), FR-002 (MEDIUM-5 task_count fix).

package gateway

import (
	"bytes"
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
	"github.com/dapicom-ai/omnipus/pkg/boardtask"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/taskstore"
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
	require.Equal(
		t,
		http.StatusCreated,
		w.Code,
		"create board task must return 201; body=%s",
		w.Body.String(),
	)
	var task gen.BoardTask
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &task))
	require.NotEmpty(t, task.Id)
	return task
}

// createBoardTaskWithPromptViaAPI creates a task with a name, status, and a non-empty prompt.
// Use this helper for /start tests that require a prompt to dispatch the agent.
func createBoardTaskWithPromptViaAPI(t *testing.T, api *restAPI, name, status, prompt string) gen.BoardTask {
	t.Helper()
	var statusField string
	if status != "" {
		statusField = fmt.Sprintf(`,"status":%q`, status)
	}
	body := fmt.Sprintf(`{"name":%q%s,"prompt":%q}`, name, statusField, prompt)
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
		taskID,
		projectID,
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
// When PUT /api/v1/board/tasks/{id} with {"status":"next"},
// Then 200 with status=next.
// When PUT /api/v1/board/tasks/{id} with {"status":"active"} (even with X-Omnipus-Agent-Context),
// Then 403 — status=active is blocked unconditionally by PUT; only /start may set it.
// When PUT to nonexistent ID,
// Then 404.
// Traces to: FR-002, FR-L2-006 (SEC-3: spoofable header removed)
func TestHandleBoardTasks_Update(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	task := createBoardTaskViaAPI(t, api, "UpdatableTask", "inbox")

	// PUT {"status":"next"} → 200.
	wUp := httptest.NewRecorder()
	rUp := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
		strings.NewReader(`{"status":"next"}`))
	rUp.Header.Set("Content-Type", "application/json")
	rUp.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wUp, rUp)

	require.Equal(
		t,
		http.StatusOK,
		wUp.Code,
		"PUT /board/tasks/{id} status=next must return 200; body=%s",
		wUp.Body.String(),
	)
	var updated gen.BoardTask
	require.NoError(t, json.Unmarshal(wUp.Body.Bytes(), &updated))
	assert.Equal(
		t,
		gen.BoardTaskStatus("next"),
		updated.Status,
		"status must be updated to next",
	)

	// PUT {"status":"active"} — blocked unconditionally (SEC-3: header gate removed).
	// Neither presence nor absence of X-Omnipus-Agent-Context matters; /start is the only path.
	for _, withHeader := range []bool{true, false} {
		wActive := httptest.NewRecorder()
		rActive := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
			strings.NewReader(`{"status":"active"}`))
		rActive.Header.Set("Content-Type", "application/json")
		if withHeader {
			rActive.Header.Set("X-Omnipus-Agent-Context", "true")
		}
		rActive.URL.Path = "/api/v1/board/tasks/" + task.Id
		api.HandleBoardTasks(wActive, rActive)
		require.Equal(
			t,
			http.StatusForbidden,
			wActive.Code,
			"PUT status=active must always return 403 (withHeader=%v); body=%s",
			withHeader, wActive.Body.String(),
		)
	}

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
	assert.Equal(
		t,
		gen.BoardTaskStatus("done"),
		updated2.Status,
		"second PUT must reflect new status",
	)
	assert.NotEqual(
		t,
		updated.Status,
		updated2.Status,
		"different inputs must produce different outputs",
	)

	// PUT nonexistent → 404.
	wNot := httptest.NewRecorder()
	rNot := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/01JXNOTEXISTENT00000000000",
		strings.NewReader(`{"status":"next"}`))
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
	assert.Equal(
		t,
		http.StatusNotFound,
		wGet.Code,
		"GET /board/tasks/{id} after delete must return 404",
	)
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

// TestHandleBoardTasks_Boundaries verifies field-length and status validation on
// POST and GET /api/v1/board/tasks.
// BDD: Given POST /api/v1/board/tasks with name > 200 chars,
// When the request is handled,
// Then 400.
// Given GET /api/v1/board/tasks?status=queued (a workflow-only status),
// When the request is handled,
// Then 400.
// Given GET /api/v1/board/tasks?status=bogus (an unrecognized status),
// When the request is handled,
// Then 400.
// Traces to: project-task-management-level1-spec.md FG-M7, FG-M13
func TestHandleBoardTasks_Boundaries(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// POST with name > 200 chars → 400.
	longName := strings.Repeat("x", 201)
	longNameBody := fmt.Sprintf(`{"name":%q}`, longName)
	wLong := httptest.NewRecorder()
	rLong := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/board/tasks",
		strings.NewReader(longNameBody),
	)
	rLong.Header.Set("Content-Type", "application/json")
	rLong.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wLong, rLong)
	assert.Equal(t, http.StatusBadRequest, wLong.Code,
		"POST /board/tasks with name > 200 chars must return 400; body=%s", wLong.Body.String())

	// POST with name exactly 200 chars → 201 (at the limit is accepted).
	exactName := strings.Repeat("y", 200)
	exactNameBody := fmt.Sprintf(`{"name":%q}`, exactName)
	wExact := httptest.NewRecorder()
	rExact := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/board/tasks",
		strings.NewReader(exactNameBody),
	)
	rExact.Header.Set("Content-Type", "application/json")
	rExact.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wExact, rExact)
	assert.Equal(
		t,
		http.StatusCreated,
		wExact.Code,
		"POST /board/tasks with name exactly 200 chars must return 201; body=%s",
		wExact.Body.String(),
	)

	// POST with description > 2000 chars → 400.
	longDesc := fmt.Sprintf(`{"name":"ok","description":%q}`, strings.Repeat("d", 2001))
	wLongDesc := httptest.NewRecorder()
	rLongDesc := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/board/tasks",
		strings.NewReader(longDesc),
	)
	rLongDesc.Header.Set("Content-Type", "application/json")
	rLongDesc.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wLongDesc, rLongDesc)
	assert.Equal(
		t,
		http.StatusBadRequest,
		wLongDesc.Code,
		"POST /board/tasks with description > 2000 chars must return 400; body=%s",
		wLongDesc.Body.String(),
	)

	// PUT with description > 2000 chars → 400.
	descTask := createBoardTaskViaAPI(t, api, "desc-test-task", "inbox")
	longDescPut := fmt.Sprintf(`{"description":%q}`, strings.Repeat("d", 2001))
	wPutLongDesc := httptest.NewRecorder()
	rPutLongDesc := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/board/tasks/"+descTask.Id,
		strings.NewReader(longDescPut),
	)
	rPutLongDesc.Header.Set("Content-Type", "application/json")
	rPutLongDesc.URL.Path = "/api/v1/board/tasks/" + descTask.Id
	api.HandleBoardTasks(wPutLongDesc, rPutLongDesc)
	assert.Equal(
		t,
		http.StatusBadRequest,
		wPutLongDesc.Code,
		"PUT /board/tasks/{id} with description > 2000 chars must return 400; body=%s",
		wPutLongDesc.Body.String(),
	)

	// GET ?status=queued → 400 (queued is a workflow task status, not a valid GTD status filter).
	wQueued := httptest.NewRecorder()
	rQueued := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks?status=queued", nil)
	rQueued.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wQueued, rQueued)
	assert.Equal(
		t,
		http.StatusBadRequest,
		wQueued.Code,
		"GET /board/tasks?status=queued must return 400 (workflow status rejected); body=%s",
		wQueued.Body.String(),
	)

	// GET ?status=bogus → 400 (completely unrecognized status).
	wBogus := httptest.NewRecorder()
	rBogus := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks?status=bogus", nil)
	rBogus.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wBogus, rBogus)
	assert.Equal(t, http.StatusBadRequest, wBogus.Code,
		"GET /board/tasks?status=bogus must return 400; body=%s", wBogus.Body.String())
}

// TestHandleBoardTasks_FKValidation verifies that POST and PUT /api/v1/board/tasks
// enforce FK integrity on the project_id field.
//
// BDD: Given a POST /api/v1/board/tasks with a project_id that does not exist,
// When the request is handled,
// Then 400 with a body containing "project".
//
// BDD: Given a valid board task (no project_id),
// When PUT /api/v1/board/tasks/{id} is called with a project_id that does not exist,
// Then 400 with a body containing "project".
//
// BDD: Given a POST /api/v1/board/tasks with project_id containing path-traversal characters,
// When the request is handled,
// Then 400 (validateEntityID rejects the value before any filesystem access).
//
// Traces to: project-task-management-level1-spec.md FK validation (B4-001)
func TestHandleBoardTasks_FKValidation(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	nonExistentProjectID := "01JXNOEXISTPROJECT000001"

	// --- POST with non-existent project_id → 400 containing "project" ---
	postBody := `{"name":"Task with bad project","project_id":"` + nonExistentProjectID + `"}`
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/board/tasks",
		strings.NewReader(postBody),
	)
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wPost, rPost)

	assert.Equal(
		t,
		http.StatusBadRequest,
		wPost.Code,
		"POST /board/tasks with non-existent project_id must return 400; body=%s",
		wPost.Body.String(),
	)
	assert.Contains(t, strings.ToLower(wPost.Body.String()), "project",
		"400 body for non-existent project_id must mention 'project'; body=%s", wPost.Body.String())

	// --- PUT with non-existent project_id → 400 ---
	// First create a valid task without a project_id.
	existingTask := createBoardTaskViaAPI(t, api, "TaskForPUTFKTest", "inbox")

	putBody := `{"project_id":"` + nonExistentProjectID + `"}`
	wPut := httptest.NewRecorder()
	rPut := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/board/tasks/"+existingTask.Id,
		strings.NewReader(putBody),
	)
	rPut.Header.Set("Content-Type", "application/json")
	rPut.URL.Path = "/api/v1/board/tasks/" + existingTask.Id
	api.HandleBoardTasks(wPut, rPut)

	assert.Equal(
		t,
		http.StatusBadRequest,
		wPut.Code,
		"PUT /board/tasks/{id} with non-existent project_id must return 400; body=%s",
		wPut.Body.String(),
	)
	assert.Contains(
		t,
		strings.ToLower(wPut.Body.String()),
		"project",
		"400 body for non-existent project_id (PUT) must mention 'project'; body=%s",
		wPut.Body.String(),
	)

	// --- POST with path-traversal project_id → 400 (validateEntityID catches it) ---
	traversalCases := []struct {
		name      string
		projectID string
	}{
		{"dotdot-slash", "../etc/passwd"},
		{"deep-traversal", "../../etc/shadow"},
	}
	for _, tc := range traversalCases {
		t.Run("path_traversal_"+tc.name, func(t *testing.T) {
			body := `{"name":"traversal test","project_id":"` + tc.projectID + `"}`
			wTrav := httptest.NewRecorder()
			rTrav := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/board/tasks",
				strings.NewReader(body),
			)
			rTrav.Header.Set("Content-Type", "application/json")
			rTrav.URL.Path = "/api/v1/board/tasks"
			api.HandleBoardTasks(wTrav, rTrav)

			assert.Equal(t, http.StatusBadRequest, wTrav.Code,
				"POST /board/tasks with path-traversal project_id=%q must return 400; body=%s",
				tc.projectID, wTrav.Body.String())
		})
	}
}

// ---------------------------------------------------------------------------
// Extended field and filter tests (project-task-milestone-spec.md Wave 1a)
// ---------------------------------------------------------------------------

// TestHandleBoardTasks_ExtendedFields verifies that POST /api/v1/board/tasks accepts
// prompt, priority, and milestone_id and that GET echoes them back.
// BDD: Given a project P with milestone M,
// When POST /api/v1/board/tasks with prompt="Do this", priority=2, milestone_id=M,
// Then 201; GET same task returns prompt="Do this", priority=2, milestone_id=M.
// Traces to: project-task-milestone-spec.md — extended BoardTask fields (FR-L2-005–FR-L2-008)
func TestHandleBoardTasks_ExtendedFields(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-L2-005 prompt, FR-L2-007 priority, FR-L2-008 milestone_id
	api := newTestRestAPIWithHome(t)
	projID := createProjectViaAPI(t, api, "ExtendedFieldsProject", "")

	// Create a milestone in the project.
	midBody := `{"name":"Ext Test Milestone"}`
	wMil := httptest.NewRecorder()
	rMil := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/projects/"+projID+"/milestones",
		strings.NewReader(midBody),
	)
	rMil.Header.Set("Content-Type", "application/json")
	rMil.URL.Path = "/api/v1/projects/" + projID + "/milestones"
	api.HandleMilestones(wMil, rMil)
	require.Equal(t, http.StatusCreated, wMil.Code, "create milestone for test must return 201")
	var milResp struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(wMil.Body.Bytes(), &milResp))
	milID := milResp.ID

	// POST task with extended fields.
	priority := 2
	body := fmt.Sprintf(
		`{"name":"Extended Task","project_id":%q,"milestone_id":%q,"prompt":"Do this","priority":%d}`,
		projID,
		milID,
		priority,
	)
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(body))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code,
		"POST /board/tasks with extended fields must return 201; body=%s", wPost.Body.String())

	var created gen.BoardTask
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &created))

	// Content tests: each field must be echoed back exactly.
	require.NotNil(t, created.Prompt, "prompt must not be nil in response")
	assert.Equal(t, "Do this", *created.Prompt, "prompt must match request")
	require.NotNil(t, created.Priority, "priority must not be nil in response")
	assert.Equal(t, priority, *created.Priority, "priority must match request")
	require.NotNil(t, created.MilestoneId, "milestone_id must not be nil in response")
	assert.Equal(t, milID, *created.MilestoneId, "milestone_id must match request")

	// GET same task — fields must persist.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+created.Id, nil)
	rGet.URL.Path = "/api/v1/board/tasks/" + created.Id
	api.HandleBoardTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code)
	var got gen.BoardTask
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	require.NotNil(t, got.Prompt, "GET: prompt must persist")
	assert.Equal(t, "Do this", *got.Prompt, "GET: prompt must equal created value")
	require.NotNil(t, got.Priority, "GET: priority must persist")
	assert.Equal(t, priority, *got.Priority, "GET: priority must equal created value")
	require.NotNil(t, got.MilestoneId, "GET: milestone_id must persist")
	assert.Equal(t, milID, *got.MilestoneId, "GET: milestone_id must equal created value")
}

// TestHandleBoardTasks_Priority_DefaultsToThree verifies POST without priority returns priority=3.
// BDD: Given a POST body without priority,
// When POST /api/v1/board/tasks,
// Then 201; GET returns priority=3.
// Traces to: project-task-milestone-spec.md — FR-L2-007 (priority defaults to 3)
func TestHandleBoardTasks_Priority_DefaultsToThree(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-L2-007: priority defaults to P3 when omitted
	api := newTestRestAPIWithHome(t)

	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks",
		strings.NewReader(`{"name":"NoPriorityTask"}`))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code,
		"POST /board/tasks without priority must return 201; body=%s", wPost.Body.String())

	var created gen.BoardTask
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &created))
	require.NotNil(t, created.Priority, "priority must not be nil in response")
	assert.Equal(t, 3, *created.Priority, "priority must default to 3 when omitted")

	// GET must also return priority=3 (from disk).
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+created.Id, nil)
	rGet.URL.Path = "/api/v1/board/tasks/" + created.Id
	api.HandleBoardTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code)
	var got gen.BoardTask
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	require.NotNil(t, got.Priority, "GET: priority must not be nil")
	assert.Equal(t, 3, *got.Priority, "GET: priority must be 3 when not set on create")
}

// TestHandleBoardTasks_Priority_Validation verifies that out-of-range priority values are rejected.
// BDD: Given priority=0 → 400; priority=6 → 400; priority=5 → 201.
// Traces to: project-task-milestone-spec.md — FR-L2-007 (priority 1–5)
func TestHandleBoardTasks_Priority_Validation(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-L2-007 dataset: priority boundaries
	api := newTestRestAPIWithHome(t)

	tests := []struct {
		name         string
		priority     int
		expectedCode int
	}{
		{"priority zero", 0, http.StatusBadRequest},
		{"priority six", 6, http.StatusBadRequest},
		{"priority one boundary min", 1, http.StatusCreated},
		{"priority five boundary max", 5, http.StatusCreated},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":"PrioTask %s","priority":%d}`, tc.name, tc.priority)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/board/tasks",
				strings.NewReader(body),
			)
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/board/tasks"
			api.HandleBoardTasks(w, r)
			assert.Equal(t, tc.expectedCode, w.Code,
				"POST /board/tasks with priority=%d must return %d; body=%s",
				tc.priority, tc.expectedCode, w.Body.String())
		})
	}
}

// TestHandleBoardTasks_Result_TooLong verifies PUT result of 50001 chars returns 400.
// BDD: Given an existing board task,
// When PUT /api/v1/board/tasks/{id} with result of 50001 characters,
// Then 400.
// Traces to: project-task-milestone-spec.md — FR-L2-009 result maxLength: 50000
func TestHandleBoardTasks_Result_TooLong(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — BoardTaskUpdateRequest result maxLength
	api := newTestRestAPIWithHome(t)
	task := createBoardTaskViaAPI(t, api, "ResultTask", "inbox")

	longResult := strings.Repeat("r", 50001)
	body, err := json.Marshal(map[string]any{"result": longResult})
	require.NoError(t, err)

	wPut := httptest.NewRecorder()
	rPut := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/board/tasks/"+task.Id,
		strings.NewReader(string(body)),
	)
	rPut.Header.Set("Content-Type", "application/json")
	rPut.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wPut, rPut)
	assert.Equal(
		t,
		http.StatusBadRequest,
		wPut.Code,
		"PUT /board/tasks/{id} with result > 50000 chars must return 400; body=%s",
		wPut.Body.String(),
	)

	// Boundary: exactly 50000 chars must be accepted.
	exactResult := strings.Repeat("s", 50000)
	body50k, err := json.Marshal(map[string]any{"result": exactResult})
	require.NoError(t, err)
	wOK := httptest.NewRecorder()
	rOK := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/board/tasks/"+task.Id,
		strings.NewReader(string(body50k)),
	)
	rOK.Header.Set("Content-Type", "application/json")
	rOK.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wOK, rOK)
	assert.Equal(
		t,
		http.StatusOK,
		wOK.Code,
		"PUT /board/tasks/{id} with result exactly 50000 chars must return 200; body=%s",
		wOK.Body.String(),
	)
}

// TestHandleBoardTasks_FilterByAgentID verifies GET ?agent_id= filter returns only matching tasks.
// BDD: Given task T1 with agent_id=A and task T2 with agent_id=B,
// When GET /api/v1/board/tasks?agent_id=A,
// Then only T1 is returned; T2 is absent.
// Traces to: project-task-milestone-spec.md — FR-L2-029 (agent_id filter)
//
// Note: tasks are written directly to disk (bypassing POST) because the agent IDs used
// are test fixtures that are not registered in the minimal test agent loop; the filter
// test's purpose is to verify the GET query filter, not POST agent_id validation (A2).
func TestHandleBoardTasks_FilterByAgentID(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-L2-029
	api := newTestRestAPIWithHome(t)

	const alphaAgentID = "01JXAGENT0ALPHA00000000001"
	const betaAgentID = "01JXAGENT0BETA000000000002"

	// Write tasks directly to disk so the filter test is decoupled from POST validation.
	alphaTaskID := "01JXFILTERALPHA0000000001"
	betaTaskID := "01JXFILTERBET000000000002"
	tasksDir := filepath.Join(api.homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	alphaData := fmt.Sprintf(
		`{"id":%q,"name":"Alpha Task","agent_id":%q,"status":"inbox","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		alphaTaskID,
		alphaAgentID,
	)
	betaData := fmt.Sprintf(
		`{"id":%q,"name":"Beta Task","agent_id":%q,"status":"inbox","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		betaTaskID,
		betaAgentID,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, alphaTaskID+".json"), []byte(alphaData), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, betaTaskID+".json"), []byte(betaData), 0o600))

	// Build minimal structs for assertions below.
	type taskRef struct{ Id string }
	taskAlpha := taskRef{alphaTaskID}
	taskBeta := taskRef{betaTaskID}

	// GET ?agent_id=alpha → only alpha task returned.
	wFilter := httptest.NewRecorder()
	rFilter := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/board/tasks?agent_id=01JXAGENT0ALPHA00000000001",
		nil,
	)
	rFilter.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wFilter, rFilter)
	require.Equal(t, http.StatusOK, wFilter.Code)

	var resp struct {
		Items []struct {
			ID      string  `json:"id"`
			AgentID *string `json:"agent_id,omitempty"`
		} `json:"items"`
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(wFilter.Body.Bytes(), &resp))

	// Content test: only alpha task must appear.
	foundAlpha, foundBeta := false, false
	for _, item := range resp.Items {
		if item.ID == taskAlpha.Id {
			foundAlpha = true
		}
		if item.ID == taskBeta.Id {
			foundBeta = true
		}
	}
	assert.True(t, foundAlpha, "alpha task must appear in ?agent_id=alpha response")
	assert.False(t, foundBeta, "beta task must NOT appear in ?agent_id=alpha response")

	// Differentiation test: ?agent_id=beta returns only the beta task.
	wFilter2 := httptest.NewRecorder()
	rFilter2 := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/board/tasks?agent_id=01JXAGENT0BETA000000000002",
		nil,
	)
	rFilter2.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wFilter2, rFilter2)
	require.Equal(t, http.StatusOK, wFilter2.Code)
	var resp2 struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(wFilter2.Body.Bytes(), &resp2))
	betaIDs := make([]string, 0, len(resp2.Items))
	for _, item := range resp2.Items {
		betaIDs = append(betaIDs, item.ID)
	}
	assert.Contains(t, betaIDs, taskBeta.Id, "beta task must appear in ?agent_id=beta response")
	assert.NotContains(
		t,
		betaIDs,
		taskAlpha.Id,
		"alpha task must NOT appear in ?agent_id=beta response",
	)

	// Differentiation test: the two filters return different task sets (not hardcoded).
	alphaIDs := make([]string, 0, len(resp.Items))
	for _, item := range resp.Items {
		alphaIDs = append(alphaIDs, item.ID)
	}
	// Alpha filter has alpha task; beta filter has beta task — they must differ.
	assert.NotEqual(t, alphaIDs, betaIDs,
		"filter by different agent_id must produce different task sets (not hardcoded)")
}

// TestHandleBoardTasks_FilterByMilestoneID verifies GET ?milestone_id= returns only matching tasks.
// BDD: Given task T1 with milestone_id=M1 and task T2 with milestone_id=M2,
// When GET /api/v1/board/tasks?milestone_id=M1,
// Then only T1 is returned.
// Traces to: project-task-milestone-spec.md — FR-L2-030 (milestone_id filter)
func TestHandleBoardTasks_FilterByMilestoneID(t *testing.T) {
	// Traces to: project-task-milestone-spec.md — FR-L2-030
	api := newTestRestAPIWithHome(t)

	// Set up two projects, each with a milestone.
	proj1ID := createProjectViaAPI(t, api, "MilFilterProj1", "")
	proj2ID := createProjectViaAPI(t, api, "MilFilterProj2", "")

	// Create milestone M1 in proj1.
	wM1 := httptest.NewRecorder()
	rM1 := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+proj1ID+"/milestones",
		strings.NewReader(`{"name":"Milestone Filter 1"}`))
	rM1.Header.Set("Content-Type", "application/json")
	rM1.URL.Path = "/api/v1/projects/" + proj1ID + "/milestones"
	api.HandleMilestones(wM1, rM1)
	require.Equal(t, http.StatusCreated, wM1.Code)
	var m1 struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(wM1.Body.Bytes(), &m1))

	// Create milestone M2 in proj2.
	wM2 := httptest.NewRecorder()
	rM2 := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+proj2ID+"/milestones",
		strings.NewReader(`{"name":"Milestone Filter 2"}`))
	rM2.Header.Set("Content-Type", "application/json")
	rM2.URL.Path = "/api/v1/projects/" + proj2ID + "/milestones"
	api.HandleMilestones(wM2, rM2)
	require.Equal(t, http.StatusCreated, wM2.Code)
	var m2 struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(wM2.Body.Bytes(), &m2))

	// Create one task per milestone.
	body1 := fmt.Sprintf(`{"name":"Task for M1","project_id":%q,"milestone_id":%q}`, proj1ID, m1.ID)
	wT1 := httptest.NewRecorder()
	rT1 := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(body1))
	rT1.Header.Set("Content-Type", "application/json")
	rT1.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wT1, rT1)
	require.Equal(t, http.StatusCreated, wT1.Code)
	var task1 struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(wT1.Body.Bytes(), &task1))

	body2 := fmt.Sprintf(`{"name":"Task for M2","project_id":%q,"milestone_id":%q}`, proj2ID, m2.ID)
	wT2 := httptest.NewRecorder()
	rT2 := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(body2))
	rT2.Header.Set("Content-Type", "application/json")
	rT2.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wT2, rT2)
	require.Equal(t, http.StatusCreated, wT2.Code)
	var task2 struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(wT2.Body.Bytes(), &task2))

	// GET ?milestone_id=M1 → only task1 returned.
	wFilter := httptest.NewRecorder()
	rFilter := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks?milestone_id="+m1.ID, nil)
	rFilter.URL.Path = "/api/v1/board/tasks"
	api.HandleBoardTasks(wFilter, rFilter)
	require.Equal(t, http.StatusOK, wFilter.Code)
	var respM1 struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(wFilter.Body.Bytes(), &respM1))

	m1IDs := make([]string, 0, len(respM1.Items))
	for _, item := range respM1.Items {
		m1IDs = append(m1IDs, item.ID)
	}
	assert.Contains(t, m1IDs, task1.ID, "task1 must appear in ?milestone_id=M1 response")
	assert.NotContains(t, m1IDs, task2.ID, "task2 must NOT appear in ?milestone_id=M1 response")
}

// TestHandleBoardTasks_ActiveRequiresStart verifies PUT status=active is blocked
// unconditionally — the spoofable X-Omnipus-Agent-Context header gate is removed (SEC-3).
// Status "active" may only be set via POST /start; PUT always returns 403.
// BDD: Given an existing board task with status "inbox",
// When PUT /api/v1/board/tasks/{id} with {"status":"active"} (with or without header),
// Then 403 — only POST /start may reach status=active.
// When POST /api/v1/board/tasks/{id}/start (with agent loop),
// Then 202 and status=active.
// Traces to: project-task-milestone-spec.md — FR-L2-006; SEC-3 (header gate removed)
func TestHandleBoardTasks_ActiveRequiresStart(t *testing.T) {
	// Traces to: FR-L2-006: status "active" may only be set via /start, not PUT
	api := newTestRestAPIWithHome(t)
	task := createBoardTaskViaAPI(t, api, "ActiveGuardTask", "inbox")

	// PUT {"status":"active"} without header → 403.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
		strings.NewReader(`{"status":"active"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(w, r)

	assert.Equal(
		t,
		http.StatusForbidden,
		w.Code,
		"PUT status=active must return 403 (only /start may set active); body=%s",
		w.Body.String(),
	)

	// PUT {"status":"active"} WITH the old header → still 403 (header ignored).
	wHdr := httptest.NewRecorder()
	rHdr := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
		strings.NewReader(`{"status":"active"}`))
	rHdr.Header.Set("Content-Type", "application/json")
	rHdr.Header.Set("X-Omnipus-Agent-Context", "true")
	rHdr.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wHdr, rHdr)
	assert.Equal(
		t,
		http.StatusForbidden,
		wHdr.Code,
		"PUT status=active WITH X-Omnipus-Agent-Context must also return 403 (header has no effect); body=%s",
		wHdr.Body.String(),
	)

	// Differentiation: PUT status=next IS allowed.
	wOK := httptest.NewRecorder()
	rOK := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
		strings.NewReader(`{"status":"next"}`))
	rOK.Header.Set("Content-Type", "application/json")
	rOK.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wOK, rOK)
	assert.Equal(
		t,
		http.StatusOK,
		wOK.Code,
		"PUT status=next must return 200; body=%s",
		wOK.Body.String(),
	)
}

// ---------------------------------------------------------------------------
// End of extended field and filter tests
// ---------------------------------------------------------------------------

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
		workflowTaskID,
		projID,
	)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(tasksDir, workflowTaskID+".json"), []byte(workflowData), 0o600),
	)

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

// newTestRestAPIWithAgent creates a restAPI backed by an agent loop that has one enabled
// agent in its config. This is required for the /start endpoint's happy path, which
// resolves the default/first-enabled agent to own the new session.
func newTestRestAPIWithAgent(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	agentWorkspace := filepath.Join(tmpDir, "agents", "01JXTESTAGENTSTARTTEST001")
	require.NoError(t, os.MkdirAll(agentWorkspace, 0o700))
	agentEnabled := true
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{
					ID:        "01JXTESTAGENTSTARTTEST001",
					Name:      "Test Agent",
					Default:   true,
					Enabled:   &agentEnabled,
					Type:      config.AgentTypeCustom,
					Workspace: agentWorkspace,
				},
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	// H1: drain any in-flight board-task goroutines before TempDir teardown fires.
	// LIFO cleanup ordering: WaitForActiveRequests runs before al.Close (registered
	// after mustAgentLoop's al.Close cleanup) and before t.TempDir cleanup.
	t.Cleanup(func() { al.WaitForActiveRequests() })
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     taskstore.New(tmpDir + "/workflow-tasks"),
		taskLock:      boardtask.TaskFileLock,
	}
}

// postBoardTaskStart calls POST /api/v1/board/tasks/{id}/start and returns the recorder.
func postBoardTaskStart(t *testing.T, api *restAPI, taskID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks/"+taskID+"/start", nil)
	r.URL.Path = "/api/v1/board/tasks/" + taskID + "/start"
	api.HandleBoardTasks(w, r)
	return w
}

// TestHandleBoardTasks_Start verifies POST /api/v1/board/tasks/{id}/start.
//
// BDD scenarios:
//
//	Scenario 1 — Happy path (202): create inbox task, POST /start → 202, status=active, session_id non-empty.
//	  GET same task confirms persisted state. Session metadata (TaskID, Title) and user-turn
//	  transcript written synchronously before goroutine dispatch.
//	Scenario 2 — 409 active: start task, start again → 409.
//	Scenario 3 — 409 done: set status=done via PUT, POST /start → 409.
//	Scenario 4 — 409 failed: set status=failed via PUT, POST /start → 409.
//	Scenario 5 — 404: POST /start on non-existent task ID → 404.
//	Scenario 6 — Session uniqueness: start two tasks → distinct session_ids.
//	Scenario 7 — Metadata + transcript: task with prompt gets SetMeta and AppendTranscript written.
//
// Traces to: project-task-management-level1-spec.md — FR-L2-006 (start creates session)
func TestHandleBoardTasks_Start(t *testing.T) {
	t.Run("happy path 202", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		task := createBoardTaskWithPromptViaAPI(t, api, "StartHappyTask", "inbox", "Run startup checks")

		w := postBoardTaskStart(t, api, task.Id)
		require.Equal(
			t,
			http.StatusAccepted,
			w.Code,
			"POST /start on inbox task must return 202; body=%s",
			w.Body.String(),
		)
		var started gen.BoardTask
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &started))
		assert.Equal(
			t,
			gen.BoardTaskStatus("active"),
			started.Status,
			"status must be active after start",
		)
		require.NotNil(t, started.SessionId, "session_id must be non-nil after start")
		assert.NotEmpty(t, *started.SessionId, "session_id must be non-empty after start")

		// GET same task — state must be persisted to disk.
		wGet := httptest.NewRecorder()
		rGet := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+task.Id, nil)
		rGet.URL.Path = "/api/v1/board/tasks/" + task.Id
		api.HandleBoardTasks(wGet, rGet)
		require.Equal(t, http.StatusOK, wGet.Code)
		var persisted gen.BoardTask
		require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &persisted))
		assert.Equal(
			t,
			gen.BoardTaskStatus("active"),
			persisted.Status,
			"GET: status must persist as active",
		)
		require.NotNil(t, persisted.SessionId, "GET: session_id must persist")
		assert.Equal(
			t,
			*started.SessionId,
			*persisted.SessionId,
			"GET: persisted session_id must match the one returned by start",
		)
	})

	t.Run("409 already active", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		task := createBoardTaskWithPromptViaAPI(t, api, "DoubleStartTask", "inbox", "Run the test suite")

		// First start → 202.
		w1 := postBoardTaskStart(t, api, task.Id)
		require.Equal(
			t,
			http.StatusAccepted,
			w1.Code,
			"first POST /start must return 202; body=%s",
			w1.Body.String(),
		)

		// Second start → 409.
		w2 := postBoardTaskStart(t, api, task.Id)
		assert.Equal(
			t,
			http.StatusConflict,
			w2.Code,
			"second POST /start on active task must return 409; body=%s",
			w2.Body.String(),
		)
	})

	t.Run("409 done", func(t *testing.T) {
		// Use the standard scaffold; set status=done via PUT with agent-context header.
		api := newTestRestAPIWithHome(t)
		task := createBoardTaskViaAPI(t, api, "DoneTask", "inbox")

		// PUT status=done (agent-context required for "active"; "done" has no such guard).
		wPut := httptest.NewRecorder()
		rPut := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
			strings.NewReader(`{"status":"done"}`))
		rPut.Header.Set("Content-Type", "application/json")
		rPut.URL.Path = "/api/v1/board/tasks/" + task.Id
		api.HandleBoardTasks(wPut, rPut)
		require.Equal(t, http.StatusOK, wPut.Code, "PUT status=done must return 200")

		// POST /start → 409.
		w := postBoardTaskStart(t, api, task.Id)
		assert.Equal(
			t,
			http.StatusConflict,
			w.Code,
			"POST /start on done task must return 409; body=%s",
			w.Body.String(),
		)
	})

	t.Run("409 failed", func(t *testing.T) {
		// Set status=failed via PUT (no agent-context required for "failed").
		api := newTestRestAPIWithHome(t)
		task := createBoardTaskViaAPI(t, api, "FailedTask", "inbox")

		wPut := httptest.NewRecorder()
		rPut := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
			strings.NewReader(`{"status":"failed"}`))
		rPut.Header.Set("Content-Type", "application/json")
		rPut.URL.Path = "/api/v1/board/tasks/" + task.Id
		api.HandleBoardTasks(wPut, rPut)
		require.Equal(t, http.StatusOK, wPut.Code, "PUT status=failed must return 200")

		w := postBoardTaskStart(t, api, task.Id)
		assert.Equal(
			t,
			http.StatusConflict,
			w.Code,
			"POST /start on failed task must return 409; body=%s",
			w.Body.String(),
		)
	})

	t.Run("404 nonexistent", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		w := postBoardTaskStart(t, api, "01JXNOEXISTTASKSTRTTEST01")
		assert.Equal(
			t,
			http.StatusNotFound,
			w.Code,
			"POST /start on non-existent task must return 404; body=%s",
			w.Body.String(),
		)
	})

	t.Run("session uniqueness", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		task1 := createBoardTaskWithPromptViaAPI(t, api, "UniqueSession1", "inbox", "Run task one")
		task2 := createBoardTaskWithPromptViaAPI(t, api, "UniqueSession2", "inbox", "Run task two")

		w1 := postBoardTaskStart(t, api, task1.Id)
		require.Equal(
			t,
			http.StatusAccepted,
			w1.Code,
			"start task1 must return 202; body=%s",
			w1.Body.String(),
		)
		w2 := postBoardTaskStart(t, api, task2.Id)
		require.Equal(
			t,
			http.StatusAccepted,
			w2.Code,
			"start task2 must return 202; body=%s",
			w2.Body.String(),
		)

		var started1, started2 gen.BoardTask
		require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &started1))
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &started2))

		require.NotNil(t, started1.SessionId, "task1 session_id must be non-nil")
		require.NotNil(t, started2.SessionId, "task2 session_id must be non-nil")
		assert.NotEmpty(t, *started1.SessionId, "task1 session_id must be non-empty")
		assert.NotEmpty(t, *started2.SessionId, "task2 session_id must be non-empty")
		assert.NotEqual(
			t,
			*started1.SessionId,
			*started2.SessionId,
			"two different tasks must get distinct session_ids",
		)
	})

	// Scenario 7 — Metadata + transcript written synchronously before goroutine dispatch.
	// The task is created with a prompt so AppendTranscript is exercised.
	// SetMeta is always called regardless of prompt.
	t.Run("session metadata and transcript written on start", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		const agentID = "01JXTESTAGENTSTARTTEST001"

		// Create a task with a prompt so the user-turn transcript entry is written.
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks",
			strings.NewReader(`{"name":"MetaTranscriptTask","prompt":"Run the integration suite"}`))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/board/tasks"
		api.HandleBoardTasks(w, r)
		require.Equal(t, http.StatusCreated, w.Code, "create task must return 201; body=%s", w.Body.String())
		var task gen.BoardTask
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &task))
		require.NotEmpty(t, task.Id)

		// POST /start → 202.
		wStart := postBoardTaskStart(t, api, task.Id)
		require.Equal(
			t,
			http.StatusAccepted,
			wStart.Code,
			"POST /start must return 202; body=%s",
			wStart.Body.String(),
		)
		var started gen.BoardTask
		require.NoError(t, json.Unmarshal(wStart.Body.Bytes(), &started))
		require.NotNil(t, started.SessionId, "session_id must be non-nil")
		sessionID := *started.SessionId
		require.NotEmpty(t, sessionID, "session_id must be non-empty")

		// Resolve the store the same way handleBoardTaskStart does:
		// GetAgentStore first, fall back to shared store.
		store := api.agentLoop.GetAgentStore(agentID)
		if store == nil {
			store = api.agentLoop.GetSessionStore()
		}
		require.NotNil(t, store, "session store must be resolvable in test")

		// Verify SetMeta wrote TaskID and Title.
		meta, err := store.GetMeta(sessionID)
		require.NoError(t, err, "GetMeta must succeed for the new session")
		assert.Equal(t, task.Id, meta.TaskID, "session meta TaskID must match the board task ID")
		assert.Equal(t, task.Name, meta.Title, "session meta Title must match the board task name")

		// Verify AppendTranscript wrote the user-turn entry.
		entries, err := store.ReadTranscript(sessionID)
		require.NoError(t, err, "ReadTranscript must succeed")
		require.NotEmpty(t, entries, "expected user-turn transcript entry after /start with non-empty prompt")
		assert.Equal(t, "user", entries[0].Role, "first transcript entry must have role=user")
		assert.NotEmpty(t, entries[0].Content, "user transcript entry must have non-empty content")
	})

	// Scenario 8 — 422 when task has no prompt or description: dispatching a task
	// with nothing to execute would leave it stuck in "active" forever.
	t.Run("422 no prompt or description", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)

		// Create a task with only a name — no prompt, no description.
		wCreate := httptest.NewRecorder()
		rCreate := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks",
			strings.NewReader(`{"name":"NopromptTask","status":"inbox"}`))
		rCreate.Header.Set("Content-Type", "application/json")
		rCreate.URL.Path = "/api/v1/board/tasks"
		api.HandleBoardTasks(wCreate, rCreate)
		require.Equal(t, http.StatusCreated, wCreate.Code, "create must return 201; body=%s", wCreate.Body.String())
		var task gen.BoardTask
		require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &task))

		wStart := postBoardTaskStart(t, api, task.Id)
		assert.Equal(t, http.StatusUnprocessableEntity, wStart.Code,
			"POST /start on task with no prompt must return 422; body=%s", wStart.Body.String())
	})

	// Scenario 9 — 403 clearing session_id on an active task:
	// prevents the UI from orphaning a live agent session.
	t.Run("403 clear session_id on active task", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)

		// Create a task with a prompt so /start succeeds.
		wCreate := httptest.NewRecorder()
		rCreate := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks",
			strings.NewReader(`{"name":"ActiveSessionTask","prompt":"Do something"}`))
		rCreate.Header.Set("Content-Type", "application/json")
		rCreate.URL.Path = "/api/v1/board/tasks"
		api.HandleBoardTasks(wCreate, rCreate)
		require.Equal(t, http.StatusCreated, wCreate.Code)
		var task gen.BoardTask
		require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &task))

		wStart := postBoardTaskStart(t, api, task.Id)
		require.Equal(t, http.StatusAccepted, wStart.Code, "start must return 202; body=%s", wStart.Body.String())

		// Attempt to clear session_id while task is active → 403 (server-side guard).
		body, _ := json.Marshal(map[string]any{"session_id": ""})
		wPut := httptest.NewRecorder()
		rPut := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id, bytes.NewReader(body))
		rPut.Header.Set("Content-Type", "application/json")
		rPut.URL.Path = "/api/v1/board/tasks/" + task.Id
		api.HandleBoardTasks(wPut, rPut)
		assert.Equal(t, http.StatusForbidden, wPut.Code,
			"clearing session_id on active task must return 403; body=%s", wPut.Body.String())
	})

	// Scenario 10 — session_id auto-cleared on retry: when a failed task is
	// PUT to status=next, the session_id is cleared automatically (#405).
	// Setting a non-empty session_id via PUT is no longer permitted (SEC-3).
	t.Run("session_id auto-cleared on retry status transition", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		task := createBoardTaskWithPromptViaAPI(t, api, "FailedRetryTask", "inbox", "Do work")

		// Start the task to get a session_id populated via /start.
		wStart := postBoardTaskStart(t, api, task.Id)
		require.Equal(t, http.StatusAccepted, wStart.Code, "start must return 202; body=%s", wStart.Body.String())
		var started gen.BoardTask
		require.NoError(t, json.Unmarshal(wStart.Body.Bytes(), &started))
		require.NotNil(t, started.SessionId, "session_id must be set after /start")

		// Directly write a failed status to simulate a completed failure (bypass the
		// goroutine so the test is synchronous). The task on disk has session_id set.
		tasksDir := filepath.Join(api.homePath, "tasks")
		rawPath := filepath.Join(tasksDir, task.Id+".json")
		rawData, readErr := os.ReadFile(rawPath)
		require.NoError(t, readErr)
		var raw map[string]any
		require.NoError(t, json.Unmarshal(rawData, &raw))
		raw["status"] = "failed"
		patched, marshalErr := json.Marshal(raw)
		require.NoError(t, marshalErr)
		require.NoError(t, os.WriteFile(rawPath, patched, 0o600))

		// Now PUT status=next (retry) — session_id must be auto-cleared (#405).
		clearBody, _ := json.Marshal(map[string]any{"status": "next"})
		wClear := httptest.NewRecorder()
		rClear := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id, bytes.NewReader(clearBody))
		rClear.Header.Set("Content-Type", "application/json")
		rClear.URL.Path = "/api/v1/board/tasks/" + task.Id
		api.HandleBoardTasks(wClear, rClear)
		assert.Equal(t, http.StatusOK, wClear.Code,
			"PUT status=next on failed task must return 200; body=%s", wClear.Body.String())
		var updated gen.BoardTask
		require.NoError(t, json.Unmarshal(wClear.Body.Bytes(), &updated))
		assert.Equal(t, gen.BoardTaskStatus("next"), updated.Status)
		assert.Nil(t, updated.SessionId, "session_id must be auto-cleared on retry status transition (#405)")

		// Differentiation: setting a non-empty session_id via PUT is now forbidden (SEC-3).
		forbidBody, _ := json.Marshal(map[string]any{"session_id": "some-session"})
		wForbid := httptest.NewRecorder()
		rForbid := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id, bytes.NewReader(forbidBody))
		rForbid.Header.Set("Content-Type", "application/json")
		rForbid.URL.Path = "/api/v1/board/tasks/" + task.Id
		api.HandleBoardTasks(wForbid, rForbid)
		assert.Equal(t, http.StatusForbidden, wForbid.Code,
			"PUT with non-empty session_id must return 403 (SEC-3); body=%s", wForbid.Body.String())
	})
}

// TestEnsureInboxProject verifies the ensureInboxProject function is idempotent and
// creates the Inbox default project on first call.
//
// BDD:
//
//	Given a fresh home directory,
//	When ensureInboxProject is called,
//	Then GET /api/v1/projects?status=active returns exactly one project with is_default=true and name="Inbox".
//	When ensureInboxProject is called again,
//	Then GET /api/v1/projects?status=active still returns exactly one default project (idempotent).
//
// Traces to: FR-L2-001 (auto-create Inbox project)
func TestEnsureInboxProject(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// First call: must create the Inbox project.
	require.NoError(
		t,
		ensureInboxProject(api.homePath),
		"first ensureInboxProject call must not return an error",
	)

	// GET /api/v1/projects → must contain exactly one default project named "Inbox".
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rList.URL.Path = "/api/v1/projects"
	api.HandleProjects(wList, rList)
	require.Equal(t, http.StatusOK, wList.Code, "GET /projects must return 200")

	var projects []gen.Project
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &projects))

	defaultCount := 0
	for _, p := range projects {
		if p.IsDefault != nil && *p.IsDefault {
			defaultCount++
			assert.Equal(t, "Inbox", p.Name, "default project must be named Inbox")
			assert.Equal(t, gen.ProjectStatusActive, p.Status, "Inbox must have status=active")
		}
	}
	assert.Equal(t, 1, defaultCount, "exactly one default project must exist after first call")

	// Second call: must be idempotent — still exactly one default project.
	require.NoError(
		t,
		ensureInboxProject(api.homePath),
		"second ensureInboxProject call must not return an error",
	)

	wList2 := httptest.NewRecorder()
	rList2 := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rList2.URL.Path = "/api/v1/projects"
	api.HandleProjects(wList2, rList2)
	require.Equal(t, http.StatusOK, wList2.Code)

	var projects2 []gen.Project
	require.NoError(t, json.Unmarshal(wList2.Body.Bytes(), &projects2))

	defaultCount2 := 0
	for _, p := range projects2 {
		if p.IsDefault != nil && *p.IsDefault {
			defaultCount2++
		}
	}
	assert.Equal(
		t,
		1,
		defaultCount2,
		"ensureInboxProject must be idempotent: still exactly one default project after second call",
	)
}

// ---------------------------------------------------------------------------
// Board task ownership scoping tests (SEC-2)
// ---------------------------------------------------------------------------

// TestHandleBoardTasks_OwnershipScoping verifies SEC-2: user A cannot see user B's task.
// BDD: Given alice creates task T,
// When bob (role=user) calls GET /api/v1/board/tasks/{id},
// Then 404.
// When bob calls GET /api/v1/board/tasks (list),
// Then T is absent.
// When admin calls GET /api/v1/board/tasks/{id},
// Then 200.
// Traces to: SEC-2
func TestHandleBoardTasks_OwnershipScoping(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Alice creates a board task.
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks",
		strings.NewReader(`{"name":"AliceTask","prompt":"do something"}`))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/board/tasks"
	rPost = rPost.WithContext(contextWithUserRole(rPost.Context(), "alice", config.UserRoleUser))
	api.HandleBoardTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code, "alice POST must return 201; body=%s", wPost.Body.String())
	var task gen.BoardTask
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &task))
	require.NotEmpty(t, task.Id)

	// Verify owner is set to alice.
	require.NotNil(t, task.Owner, "owner must be set on create")
	assert.Equal(t, "alice", *task.Owner, "owner must equal alice")

	// Bob calls GET /api/v1/board/tasks/{id} → 404.
	wGetBob := httptest.NewRecorder()
	rGetBob := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+task.Id, nil)
	rGetBob.URL.Path = "/api/v1/board/tasks/" + task.Id
	rGetBob = rGetBob.WithContext(contextWithUserRole(rGetBob.Context(), "bob", config.UserRoleUser))
	api.HandleBoardTasks(wGetBob, rGetBob)
	assert.Equal(t, http.StatusNotFound, wGetBob.Code,
		"bob must get 404 on alice's task; body=%s", wGetBob.Body.String())

	// Bob calls GET /api/v1/board/tasks (list) — alice's task must not appear.
	wListBob := httptest.NewRecorder()
	rListBob := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks", nil)
	rListBob.URL.Path = "/api/v1/board/tasks"
	rListBob = rListBob.WithContext(contextWithUserRole(rListBob.Context(), "bob", config.UserRoleUser))
	api.HandleBoardTasks(wListBob, rListBob)
	require.Equal(t, http.StatusOK, wListBob.Code)
	var listResp struct {
		Items []gen.BoardTask `json:"items"`
		Total int             `json:"total"`
	}
	require.NoError(t, json.Unmarshal(wListBob.Body.Bytes(), &listResp))
	for _, item := range listResp.Items {
		assert.NotEqual(t, task.Id, item.Id, "alice's task must NOT appear in bob's task list")
	}

	// Admin calls GET /api/v1/board/tasks/{id} → 200.
	wGetAdmin := httptest.NewRecorder()
	rGetAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+task.Id, nil)
	rGetAdmin.URL.Path = "/api/v1/board/tasks/" + task.Id
	rGetAdmin = rGetAdmin.WithContext(contextWithUserRole(rGetAdmin.Context(), "admin", config.UserRoleAdmin))
	api.HandleBoardTasks(wGetAdmin, rGetAdmin)
	assert.Equal(t, http.StatusOK, wGetAdmin.Code,
		"admin must get 200 on alice's task; body=%s", wGetAdmin.Body.String())
}
