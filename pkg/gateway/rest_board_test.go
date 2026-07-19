//go:build !cgo

// rest_board_test.go — unified task REST API tests (Sprint 2).
//
// Sprint 2 replaced pkg/boardtask (GTD board) and pkg/taskstore (workflow queue)
// with ONE unified store (pkg/task) and folded /board/tasks into /api/v1/tasks.
//
// REMOVED in Sprint 2 (design decisions — not regressions):
//   - POST /api/v1/board/tasks/{id}/start endpoint (tasks start via PATCH status=in_progress)
//   - "active" status (replaced by "in_progress")
//   - "waiting" status (replaced by "blocked")
//   - PUT cannot set "active" rule (no longer applicable)
//   - GTD-vs-workflow title disambiguator (unified entity)
//   - scope=both merge filter (unified entity)
//   - boardtask.HandleBoardTasks, readBoardTask, writeBoardTask, listBoardTasks
//   - reconcileStuckBoardTasks (replaced by reconcileStuckTasks)
//
// Tests that remain map to the same invariants on the unified /api/v1/tasks surface.
// Traces to: FR-002 (unified task CRUD), project-task-milestone-spec.md

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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// --- helpers for the unified /api/v1/tasks surface ---------------------------

// createTaskViaAPI posts a minimal task (title + action + workspace_id) and
// returns the wire struct. workspace_id defaults to a synthetic fixture if empty.
func createTaskViaAPI(t *testing.T, api *restAPI, title, workspaceID string) gen.Task {
	t.Helper()
	if workspaceID == "" {
		workspaceID = ensureTestWorkspace(t, api)
	}
	body := fmt.Sprintf(`{"title":%q,"action":"llm","workspace_id":%q}`, title, workspaceID)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)
	require.Equal(t, http.StatusCreated, w.Code,
		"createTaskViaAPI: POST /api/v1/tasks must return 201; body=%s", w.Body.String())
	var tsk gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))
	require.NotEmpty(t, tsk.Id)
	return tsk
}

// createTaskWithStatusViaAPI creates a task and advances it to the requested
// status via the correct legal transition path. Legal paths are:
//
//	inbox → next (needs description for the partial-task guard)
//	inbox → next → planning
//	inbox → next → in_progress
//	inbox → next → in_progress → done
//	inbox → next → in_progress → failed
//
// `blocked` is a derived side-state and cannot be set via PATCH; seed to disk
// for tests that need a task in the `blocked` status.
func createTaskWithStatusViaAPI(t *testing.T, api *restAPI, title, workspaceID, status string) gen.Task {
	t.Helper()
	if workspaceID == "" {
		workspaceID = ensureTestWorkspace(t, api)
	}
	tsk := createTaskViaAPI(t, api, title, workspaceID)
	if status == "" || status == "inbox" {
		return tsk // inbox is the default
	}
	// inbox → next (all further transitions require next first)
	wNext := patchTask(t, api, tsk.Id, `{"status":"next","description":"task ready"}`)
	require.Equal(t, http.StatusOK, wNext.Code,
		"createTaskWithStatusViaAPI: inbox→next must return 200; body=%s", wNext.Body.String())
	if status == "next" {
		var updated gen.Task
		require.NoError(t, json.Unmarshal(wNext.Body.Bytes(), &updated))
		return updated
	}
	// next → planning
	if status == "planning" {
		w := patchTask(t, api, tsk.Id, `{"status":"planning"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"createTaskWithStatusViaAPI: next→planning must return 200; body=%s", w.Body.String())
		var updated gen.Task
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
		return updated
	}
	// next → in_progress
	wIP := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, wIP.Code,
		"createTaskWithStatusViaAPI: next→in_progress must return 200; body=%s", wIP.Body.String())
	if status == "in_progress" {
		var updated gen.Task
		require.NoError(t, json.Unmarshal(wIP.Body.Bytes(), &updated))
		return updated
	}
	// in_progress → done or failed
	w := patchTask(t, api, tsk.Id, fmt.Sprintf(`{"status":%q}`, status))
	require.Equal(t, http.StatusOK, w.Code,
		"createTaskWithStatusViaAPI: in_progress→%s must return 200; body=%s", status, w.Body.String())
	var updated gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	return updated
}

// patchTask is a small helper that PATCHes a task and returns the recorder.
func patchTask(t *testing.T, api *restAPI, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks/" + id
	api.HandleTasks(w, r)
	return w
}

// getTaskStatus reads back a task via GET /api/v1/tasks/{id} and returns its status.
func getTaskStatus(t *testing.T, api *restAPI, id string) gen.TaskStatus {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id, nil)
	r.URL.Path = "/api/v1/tasks/" + id
	api.HandleTasks(w, r)
	require.Equal(t, http.StatusOK, w.Code, "getTaskStatus GET must return 200; body=%s", w.Body.String())
	var tsk gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))
	return tsk.Status
}

// getTaskSessionID reads back a task via GET /api/v1/tasks/{id} and returns
// its session_id ("" when unset).
func getTaskSessionID(t *testing.T, api *restAPI, id string) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id, nil)
	r.URL.Path = "/api/v1/tasks/" + id
	api.HandleTasks(w, r)
	require.Equal(t, http.StatusOK, w.Code, "getTaskSessionID GET must return 200; body=%s", w.Body.String())
	var tsk gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))
	if tsk.SessionId == nil {
		return ""
	}
	return *tsk.SessionId
}

// ensureTestWorkspace creates a workspace (via the workspaces API) and returns its ID.
func ensureTestWorkspace(t *testing.T, api *restAPI) string {
	t.Helper()
	return createWorkspaceViaAPI(t, api, "TestWorkspace_"+t.Name(), "")
}

// setWorkspaceCoreTeam overwrites the given workspace's core_team on disk
// (direct file read/mutate/write, bypassing the PUT API) so a test can put a
// specific agent ID on a workspace's TEAM roster — the set validateTaskAgentID
// checks a task's agent_id against (workspace.TeamSet: core_team ∪ delegation
// edge endpoints, see rest_tasks.go). Used by task-assignment tests that need
// an agent other than Ava — the only agent a fresh POST-created workspace's
// implicit core_team seeds by default (newWorkspaceSetupTeam) — to be
// a legitimate assignee. (The full mia/jim/ava/ray/worker/planner/explorer/
// researcher roster is still seeded by defaultWorkspaceTeam, but that is now
// reserved for the one auto-created boot default workspace, ensureDefaultWorkspace.)
func setWorkspaceCoreTeam(t *testing.T, api *restAPI, workspaceID string, team []string) {
	t.Helper()
	ws, err := readWorkspaceFile(api.homePath, workspaceID)
	require.NoError(t, err, "setWorkspaceCoreTeam: read workspace %q", workspaceID)
	ws.CoreTeam = team
	require.NoError(t, writeWorkspaceFile(api.homePath, ws),
		"setWorkspaceCoreTeam: write workspace %q", workspaceID)
}

// seedBlockedTask creates a task (via API) with the given blocked_by list and
// seeds it directly to disk in `blocked` status. It is used by tests that need
// a task in the derived `blocked` side-state, since PATCH status=blocked is
// correctly rejected at the gateway seam (Fix #6).
func seedBlockedTask(t *testing.T, api *restAPI, id, title, workspaceID string, blockedBy []string) {
	t.Helper()
	tasksDir := filepath.Join(api.homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	now := "2026-01-01T00:00:00Z"
	deps := ""
	for i, dep := range blockedBy {
		if i > 0 {
			deps += ","
		}
		deps += fmt.Sprintf("%q", dep)
	}
	data := fmt.Sprintf(
		`{"id":%q,"title":%q,"action":"llm","status":"blocked","workspace_id":%q,"blocked_by":[%s],"created_at":%q,"updated_at":%q}`,
		id,
		title,
		workspaceID,
		deps,
		now,
		now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, id+".json"), []byte(data), 0o600))
}

// writeUnifiedTaskFile writes a raw task JSON file for direct-on-disk seeding in tests
// that need to bypass API validation (filter tests, etc.).
func writeUnifiedTaskFile(t *testing.T, homePath, taskID, workspaceID, status string) {
	t.Helper()
	tasksDir := filepath.Join(homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	data := fmt.Sprintf(
		`{"id":%q,"title":"Task","action":"llm","status":%q,"workspace_id":%q,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		taskID,
		status,
		workspaceID,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, taskID+".json"), []byte(data), 0o600))
}

// --- BDD tests ---------------------------------------------------------------

// TestHandleTasks_ListEmpty verifies GET /api/v1/tasks returns empty list on fresh dir.
// BDD: Given no tasks exist,
// When GET /api/v1/tasks is called,
// Then 200 with an empty JSON array.
// Traces to: FR-002
func TestHandleTasks_ListEmpty(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)

	require.Equal(t, http.StatusOK, w.Code, "GET /tasks on fresh dir must return 200")
	var items []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &items))
	assert.Empty(t, items, "items must be empty on fresh dir")
}

// TestHandleTasks_CreateAndGet verifies POST creates a task and GET retrieves it.
// BDD: Given a valid TaskCreateRequest,
// When POST /api/v1/tasks is called,
// Then 201 with id, title="Fix bug", status="inbox", non-zero created_at.
// When GET /api/v1/tasks/{id} is called,
// Then 200 with same data.
// Traces to: FR-002
func TestHandleTasks_CreateAndGet(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	// POST → 201.
	tsk := createTaskViaAPI(t, api, "Fix bug", wsID)
	assert.Equal(t, "Fix bug", tsk.Title, "title must match request")
	assert.Equal(t, gen.TaskStatus("inbox"), tsk.Status, "status must be inbox (default)")
	assert.False(t, tsk.CreatedAt.IsZero(), "created_at must not be zero")
	assert.Equal(t, wsID, tsk.WorkspaceId, "workspace_id must match")

	// GET /api/v1/tasks/{id} → 200 same data.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+tsk.Id, nil)
	rGet.URL.Path = "/api/v1/tasks/" + tsk.Id
	api.HandleTasks(wGet, rGet)

	require.Equal(t, http.StatusOK, wGet.Code, "GET /tasks/{id} must return 200")
	var got gen.Task
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	assert.Equal(t, tsk.Id, got.Id, "GET must return the same task id")
	assert.Equal(t, "Fix bug", got.Title, "GET must return the correct title")
	assert.Equal(t, gen.TaskStatus("inbox"), got.Status, "GET must return correct status")
}

// TestHandleTasks_Update verifies PATCH /api/v1/tasks/{id} updates the task.
// BDD: Given an existing task,
// When PATCH /api/v1/tasks/{id} with {"status":"next"},
// Then 200 with status=next.
// When PATCH /api/v1/tasks/{id} with {"status":"in_progress"},
// Then 200 (in_progress is directly settable via PATCH in Sprint 2 — /start is removed).
// When PATCH to nonexistent ID,
// Then 404.
// Traces to: FR-002
func TestHandleTasks_Update(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	tsk := createTaskViaAPI(t, api, "UpdatableTask", "")

	// PATCH {"status":"next","description":"..."} → 200.
	// (Description required by the partial-task guard for advancing to "next".)
	wUp := patchTask(t, api, tsk.Id, `{"status":"next","description":"task is ready"}`)
	require.Equal(t, http.StatusOK, wUp.Code,
		"PATCH /tasks/{id} status=next must return 200; body=%s", wUp.Body.String())
	var updated gen.Task
	require.NoError(t, json.Unmarshal(wUp.Body.Bytes(), &updated))
	assert.Equal(t, gen.TaskStatus("next"), updated.Status, "status must be updated to next")

	// PATCH {"status":"in_progress"} is now directly settable (no /start required).
	wInProg := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, wInProg.Code,
		"PATCH status=in_progress must return 200 (directly settable in Sprint 2); body=%s", wInProg.Body.String())
	var inProg gen.Task
	require.NoError(t, json.Unmarshal(wInProg.Body.Bytes(), &inProg))
	assert.Equal(t, gen.TaskStatus("in_progress"), inProg.Status, "status must be in_progress")

	// Differentiation: a second PATCH with a different status returns a different result.
	wUp2 := patchTask(t, api, tsk.Id, `{"status":"done"}`)
	require.Equal(t, http.StatusOK, wUp2.Code)
	var updated2 gen.Task
	require.NoError(t, json.Unmarshal(wUp2.Body.Bytes(), &updated2))
	assert.Equal(t, gen.TaskStatus("done"), updated2.Status, "second PATCH must reflect new status")
	assert.NotEqual(t, inProg.Status, updated2.Status,
		"different inputs must produce different outputs")

	// PATCH nonexistent → 404.
	wNot := patchTask(t, api, "01JXNOTEXISTENT00000000000", `{"status":"next"}`)
	assert.Equal(t, http.StatusNotFound, wNot.Code, "PATCH /tasks/nonexistent must return 404")
}

// TestHandleTasks_Delete_Returns204 verifies DELETE /api/v1/tasks/{id} returns 204.
// BDD: Given an existing task,
// When DELETE /api/v1/tasks/{id} is called,
// Then 204.
// When GET same ID is called,
// Then 404.
// Traces to: FR-002
func TestHandleTasks_Delete_Returns204(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	tsk := createTaskViaAPI(t, api, "DeleteMe", "")

	// DELETE → 204.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+tsk.Id, nil)
	rDel.URL.Path = "/api/v1/tasks/" + tsk.Id
	api.HandleTasks(wDel, rDel)
	assert.Equal(t, http.StatusNoContent, wDel.Code, "DELETE /tasks/{id} must return 204")

	// GET after delete → 404.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+tsk.Id, nil)
	rGet.URL.Path = "/api/v1/tasks/" + tsk.Id
	api.HandleTasks(wGet, rGet)
	assert.Equal(t, http.StatusNotFound, wGet.Code,
		"GET /tasks/{id} after delete must return 404")
}

// TestHandleTasks_FilterByStatus verifies ?status= filter works.
// BDD: Given tasks with status inbox and in_progress,
// When GET /api/v1/tasks?status=inbox is called,
// Then only inbox tasks returned.
// Traces to: FR-002
func TestHandleTasks_FilterByStatus(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	inboxTask := createTaskViaAPI(t, api, "InboxItem", wsID)
	ipTask := createTaskWithStatusViaAPI(t, api, "InProgressItem", wsID, "in_progress")
	_ = ipTask

	// GET ?status=inbox → only inbox task.
	wInbox := httptest.NewRecorder()
	rInbox := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?status=inbox", nil)
	rInbox.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wInbox, rInbox)

	require.Equal(t, http.StatusOK, wInbox.Code)
	var items []gen.Task
	require.NoError(t, json.Unmarshal(wInbox.Body.Bytes(), &items))

	for _, item := range items {
		assert.Equal(t, gen.TaskStatus("inbox"), item.Status,
			"status filter must exclude non-inbox tasks")
	}
	foundInbox := false
	for _, item := range items {
		if item.Id == inboxTask.Id {
			foundInbox = true
		}
	}
	assert.True(t, foundInbox, "inbox task must appear when ?status=inbox")
}

// TestHandleTasks_Create_TitleRequired verifies POST without title returns 400.
// BDD: Given a POST body with no title,
// When POST /api/v1/tasks is called,
// Then 400.
// Traces to: FR-002
func TestHandleTasks_Create_TitleRequired(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	w := httptest.NewRecorder()
	body := fmt.Sprintf(`{"action":"llm","workspace_id":%q}`, wsID)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "POST /tasks without title must return 400")
}

// TestHandleTasks_Create_WorkspaceIDRequired verifies POST without workspace_id returns 400.
// BDD: Given a POST body with no workspace_id,
// When POST /api/v1/tasks is called,
// Then 400.
// Traces to: FR-002
func TestHandleTasks_Create_WorkspaceIDRequired(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks",
		strings.NewReader(`{"title":"No workspace","action":"llm"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "POST /tasks without workspace_id must return 400")
}

// TestHandleTasks_Boundaries verifies field-length validation on POST and PATCH.
// BDD: Given POST /api/v1/tasks with title > 200 chars → 400.
// Given POST /api/v1/tasks with title exactly 200 chars → 201.
// Given POST /api/v1/tasks with description > 2000 chars → 400.
// Given PATCH /api/v1/tasks/{id} with description > 2000 chars → 400.
// Given GET /api/v1/tasks?status=bogus → 400.
// Traces to: project-task-milestone-spec.md FG-M7, FG-M13
func TestHandleTasks_Boundaries(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	// POST with title > 200 chars → 400.
	longTitle := strings.Repeat("x", 201)
	longTitleBody := fmt.Sprintf(`{"title":%q,"action":"llm","workspace_id":%q}`, longTitle, wsID)
	wLong := httptest.NewRecorder()
	rLong := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(longTitleBody))
	rLong.Header.Set("Content-Type", "application/json")
	rLong.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wLong, rLong)
	assert.Equal(t, http.StatusBadRequest, wLong.Code,
		"POST /tasks with title > 200 chars must return 400; body=%s", wLong.Body.String())

	// POST with title exactly 200 chars → 201 (at the limit is accepted).
	exactTitle := strings.Repeat("y", 200)
	exactTitleBody := fmt.Sprintf(`{"title":%q,"action":"llm","workspace_id":%q}`, exactTitle, wsID)
	wExact := httptest.NewRecorder()
	rExact := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(exactTitleBody))
	rExact.Header.Set("Content-Type", "application/json")
	rExact.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wExact, rExact)
	assert.Equal(t, http.StatusCreated, wExact.Code,
		"POST /tasks with title exactly 200 chars must return 201; body=%s", wExact.Body.String())

	// POST with description > 2000 chars → 400.
	longDesc := fmt.Sprintf(`{"title":"ok","action":"llm","workspace_id":%q,"description":%q}`,
		wsID, strings.Repeat("d", 2001))
	wLongDesc := httptest.NewRecorder()
	rLongDesc := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(longDesc))
	rLongDesc.Header.Set("Content-Type", "application/json")
	rLongDesc.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wLongDesc, rLongDesc)
	assert.Equal(t, http.StatusBadRequest, wLongDesc.Code,
		"POST /tasks with description > 2000 chars must return 400; body=%s", wLongDesc.Body.String())

	// PATCH with description > 2000 chars → 400.
	descTask := createTaskViaAPI(t, api, "desc-test-task", wsID)
	longDescPatch := fmt.Sprintf(`{"description":%q}`, strings.Repeat("d", 2001))
	wPatchLongDesc := patchTask(t, api, descTask.Id, longDescPatch)
	assert.Equal(t, http.StatusBadRequest, wPatchLongDesc.Code,
		"PATCH /tasks/{id} with description > 2000 chars must return 400; body=%s", wPatchLongDesc.Body.String())

	// GET ?status=bogus → 400 (completely unrecognized status).
	wBogus := httptest.NewRecorder()
	rBogus := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?status=bogus", nil)
	rBogus.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wBogus, rBogus)
	assert.Equal(t, http.StatusBadRequest, wBogus.Code,
		"GET /tasks?status=bogus must return 400; body=%s", wBogus.Body.String())
}

// TestHandleTasks_FKValidation verifies that POST enforces FK integrity on workspace_id.
//
// BDD: Given POST /api/v1/tasks with a workspace_id that does not exist,
// When the request is handled,
// Then 400 with a body containing "workspace".
//
// BDD: Given POST /api/v1/tasks with workspace_id containing path-traversal characters,
// When the request is handled,
// Then 400.
//
// Traces to: project-task-milestone-spec.md FK validation (B4-001)
func TestHandleTasks_FKValidation(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	nonExistentWSID := "01JXNOEXISTPROJECT000001"

	// POST with non-existent workspace_id → 400 containing "workspace".
	postBody := fmt.Sprintf(`{"title":"Task with bad workspace","action":"llm","workspace_id":%q}`, nonExistentWSID)
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(postBody))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wPost, rPost)

	assert.Equal(t, http.StatusBadRequest, wPost.Code,
		"POST /tasks with non-existent workspace_id must return 400; body=%s", wPost.Body.String())
	assert.Contains(t, strings.ToLower(wPost.Body.String()), "workspace",
		"400 body for non-existent workspace_id must mention 'workspace'; body=%s", wPost.Body.String())

	// POST with path-traversal workspace_id → 400.
	traversalCases := []struct {
		name string
		wsID string
	}{
		{"dotdot-slash", "../etc/passwd"},
		{"deep-traversal", "../../etc/shadow"},
	}
	for _, tc := range traversalCases {
		t.Run("path_traversal_"+tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"title":"traversal test","action":"llm","workspace_id":%q}`, tc.wsID)
			wTrav := httptest.NewRecorder()
			rTrav := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
			rTrav.Header.Set("Content-Type", "application/json")
			rTrav.URL.Path = "/api/v1/tasks"
			api.HandleTasks(wTrav, rTrav)

			assert.Equal(t, http.StatusBadRequest, wTrav.Code,
				"POST /tasks with path-traversal workspace_id=%q must return 400; body=%s",
				tc.wsID, wTrav.Body.String())
		})
	}
}

// ---------------------------------------------------------------------------
// Extended field and filter tests (project-task-milestone-spec.md Wave 1a)
// ---------------------------------------------------------------------------

// TestHandleTasks_ExtendedFields verifies that POST /api/v1/tasks accepts
// prompt, priority, and milestone_id and that GET echoes them back.
// BDD: Given a workspace P with milestone M,
// When POST /api/v1/tasks with prompt="Do this", priority=2, milestone_id=M,
// Then 201; GET same task returns prompt="Do this", priority=2, milestone_id=M.
// Traces to: project-task-milestone-spec.md — FR-L2-005 prompt, FR-L2-007 priority, FR-L2-008 milestone_id
func TestHandleTasks_ExtendedFields(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	projID := createWorkspaceViaAPI(t, api, "ExtendedFieldsProject", "")

	// Create a milestone in the project.
	midBody := `{"name":"Ext Test Milestone"}`
	wMil := httptest.NewRecorder()
	rMil := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+projID+"/milestones",
		strings.NewReader(midBody))
	rMil.Header.Set("Content-Type", "application/json")
	rMil.URL.Path = "/api/v1/workspaces/" + projID + "/milestones"
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
		`{"title":"Extended Task","action":"llm","workspace_id":%q,"milestone_id":%q,"prompt":"Do this","priority":%d}`,
		projID, milID, priority,
	)
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code,
		"POST /tasks with extended fields must return 201; body=%s", wPost.Body.String())

	var created gen.Task
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
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+created.Id, nil)
	rGet.URL.Path = "/api/v1/tasks/" + created.Id
	api.HandleTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code)
	var got gen.Task
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	require.NotNil(t, got.Prompt, "GET: prompt must persist")
	assert.Equal(t, "Do this", *got.Prompt, "GET: prompt must equal created value")
	require.NotNil(t, got.Priority, "GET: priority must persist")
	assert.Equal(t, priority, *got.Priority, "GET: priority must equal created value")
	require.NotNil(t, got.MilestoneId, "GET: milestone_id must persist")
	assert.Equal(t, milID, *got.MilestoneId, "GET: milestone_id must equal created value")
}

// TestHandleTasks_Priority_DefaultsToThree verifies POST without priority returns priority=3.
// BDD: Given a POST body without priority,
// When POST /api/v1/tasks,
// Then 201; GET returns priority=3.
// Traces to: project-task-milestone-spec.md — FR-L2-007 (priority defaults to 3)
func TestHandleTasks_Priority_DefaultsToThree(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	wPost := httptest.NewRecorder()
	body := fmt.Sprintf(`{"title":"NoPriorityTask","action":"llm","workspace_id":%q}`, wsID)
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code,
		"POST /tasks without priority must return 201; body=%s", wPost.Body.String())

	var created gen.Task
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &created))
	require.NotNil(t, created.Priority, "priority must not be nil in response")
	assert.Equal(t, 3, *created.Priority, "priority must default to 3 when omitted")

	// GET must also return priority=3 (from disk).
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+created.Id, nil)
	rGet.URL.Path = "/api/v1/tasks/" + created.Id
	api.HandleTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code)
	var got gen.Task
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	require.NotNil(t, got.Priority, "GET: priority must not be nil")
	assert.Equal(t, 3, *got.Priority, "GET: priority must be 3 when not set on create")
}

// TestHandleTasks_Priority_Validation verifies priority range validation.
// BDD:
//   - priority=0 → 201 (treated as unset; Sprint 2 unified store defaults to 3 on read).
//   - priority=6 → 400 (out of range 1–5).
//   - priority=1 → 201 (boundary min).
//   - priority=5 → 201 (boundary max).
//
// Sprint 2 change: priority=0 is no longer rejected with 400. The unified store
// treats 0 as "unset" and stores/returns effective priority=3 (default). Old "400 on 0"
// was the legacy boardtask behavior. The new store.Create normalizes 0 → 3.
// Traces to: project-task-milestone-spec.md — FR-L2-007 (priority 1–5, 0=unset/default)
func TestHandleTasks_Priority_Validation(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	tests := []struct {
		name         string
		priority     int
		expectedCode int
	}{
		{"priority zero", 0, http.StatusCreated}, // Sprint 2: 0=unset→default 3 (not 400)
		{"priority six", 6, http.StatusBadRequest},
		{"priority one boundary min", 1, http.StatusCreated},
		{"priority five boundary max", 5, http.StatusCreated},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"title":"PrioTask %s","action":"llm","workspace_id":%q,"priority":%d}`,
				tc.name, wsID, tc.priority)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/tasks"
			api.HandleTasks(w, r)
			assert.Equal(t, tc.expectedCode, w.Code,
				"POST /tasks with priority=%d must return %d; body=%s",
				tc.priority, tc.expectedCode, w.Body.String())
		})
	}
}

// TestHandleTasks_Result_TooLong verifies PATCH result of 50001 chars returns 400.
// BDD: Given an existing task,
// When PATCH /api/v1/tasks/{id} with result of 50001 characters,
// Then 400.
// Traces to: project-task-milestone-spec.md — FR-L2-009 result maxLength: 50000
func TestHandleTasks_Result_TooLong(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	tsk := createTaskViaAPI(t, api, "ResultTask", "")

	longResult := strings.Repeat("r", 50001)
	body, err := json.Marshal(map[string]any{"result": longResult})
	require.NoError(t, err)

	wPatch := patchTask(t, api, tsk.Id, string(body))
	assert.Equal(t, http.StatusBadRequest, wPatch.Code,
		"PATCH /tasks/{id} with result > 50000 chars must return 400; body=%s", wPatch.Body.String())

	// Boundary: exactly 50000 chars must be accepted.
	exactResult := strings.Repeat("s", 50000)
	body50k, err := json.Marshal(map[string]any{"result": exactResult})
	require.NoError(t, err)
	wOK := patchTask(t, api, tsk.Id, string(body50k))
	assert.Equal(t, http.StatusOK, wOK.Code,
		"PATCH /tasks/{id} with result exactly 50000 chars must return 200; body=%s", wOK.Body.String())
}

// TestHandleTasks_FilterByAgentID verifies GET ?agent_id= filter returns only matching tasks.
// BDD: Given task T1 with agent_id=A and task T2 with agent_id=B,
// When GET /api/v1/tasks?agent_id=A,
// Then only T1 is returned; T2 is absent.
// Traces to: project-task-milestone-spec.md — FR-L2-029 (agent_id filter)
func TestHandleTasks_FilterByAgentID(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	const alphaAgentID = "01JXAGENT0ALPHA00000000001"
	const betaAgentID = "01JXAGENT0BETA000000000002"

	// Write tasks directly to disk so the filter test is decoupled from POST agent_id validation.
	alphaTaskID := "01JXFILTERALPHA0000000001"
	betaTaskID := "01JXFILTERBET000000000002"
	tasksDir := filepath.Join(api.homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	alphaData := fmt.Sprintf(
		`{"id":%q,"title":"Alpha Task","action":"llm","agent_id":%q,"status":"inbox","workspace_id":%q,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		alphaTaskID,
		alphaAgentID,
		wsID,
	)
	betaData := fmt.Sprintf(
		`{"id":%q,"title":"Beta Task","action":"llm","agent_id":%q,"status":"inbox","workspace_id":%q,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		betaTaskID,
		betaAgentID,
		wsID,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, alphaTaskID+".json"), []byte(alphaData), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, betaTaskID+".json"), []byte(betaData), 0o600))

	// GET ?agent_id=alpha → only alpha task returned.
	wFilter := httptest.NewRecorder()
	rFilter := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?agent_id="+alphaAgentID, nil)
	rFilter.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wFilter, rFilter)
	require.Equal(t, http.StatusOK, wFilter.Code)

	var alphaItems []gen.Task
	require.NoError(t, json.Unmarshal(wFilter.Body.Bytes(), &alphaItems))

	foundAlpha, foundBeta := false, false
	for _, item := range alphaItems {
		if item.Id == alphaTaskID {
			foundAlpha = true
		}
		if item.Id == betaTaskID {
			foundBeta = true
		}
	}
	assert.True(t, foundAlpha, "alpha task must appear in ?agent_id=alpha response")
	assert.False(t, foundBeta, "beta task must NOT appear in ?agent_id=alpha response")

	// Differentiation test: ?agent_id=beta returns only the beta task.
	wFilter2 := httptest.NewRecorder()
	rFilter2 := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?agent_id="+betaAgentID, nil)
	rFilter2.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wFilter2, rFilter2)
	require.Equal(t, http.StatusOK, wFilter2.Code)
	var betaItems []gen.Task
	require.NoError(t, json.Unmarshal(wFilter2.Body.Bytes(), &betaItems))

	foundAlpha2, foundBeta2 := false, false
	for _, item := range betaItems {
		if item.Id == alphaTaskID {
			foundAlpha2 = true
		}
		if item.Id == betaTaskID {
			foundBeta2 = true
		}
	}
	assert.True(t, foundBeta2, "beta task must appear in ?agent_id=beta response")
	assert.False(t, foundAlpha2, "alpha task must NOT appear in ?agent_id=beta response")

	// Differentiation test: the two filters return different task sets (not hardcoded).
	alphaIDs := make([]string, 0, len(alphaItems))
	for _, item := range alphaItems {
		alphaIDs = append(alphaIDs, item.Id)
	}
	betaIDs := make([]string, 0, len(betaItems))
	for _, item := range betaItems {
		betaIDs = append(betaIDs, item.Id)
	}
	assert.NotEqual(t, alphaIDs, betaIDs,
		"filter by different agent_id must produce different task sets (not hardcoded)")
}

// TestHandleTasks_FilterByMilestoneID verifies GET ?milestone_id= returns only matching tasks.
// BDD: Given task T1 with milestone_id=M1 and task T2 with milestone_id=M2,
// When GET /api/v1/tasks?milestone_id=M1,
// Then only T1 is returned.
// Traces to: project-task-milestone-spec.md — FR-L2-030 (milestone_id filter)
func TestHandleTasks_FilterByMilestoneID(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Set up two projects, each with a milestone.
	proj1ID := createWorkspaceViaAPI(t, api, "MilFilterProj1", "")
	proj2ID := createWorkspaceViaAPI(t, api, "MilFilterProj2", "")

	// Create milestone M1 in proj1.
	wM1 := httptest.NewRecorder()
	rM1 := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+proj1ID+"/milestones",
		strings.NewReader(`{"name":"Milestone Filter 1"}`))
	rM1.Header.Set("Content-Type", "application/json")
	rM1.URL.Path = "/api/v1/workspaces/" + proj1ID + "/milestones"
	api.HandleMilestones(wM1, rM1)
	require.Equal(t, http.StatusCreated, wM1.Code)
	var m1 struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(wM1.Body.Bytes(), &m1))

	// Create milestone M2 in proj2.
	wM2 := httptest.NewRecorder()
	rM2 := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+proj2ID+"/milestones",
		strings.NewReader(`{"name":"Milestone Filter 2"}`))
	rM2.Header.Set("Content-Type", "application/json")
	rM2.URL.Path = "/api/v1/workspaces/" + proj2ID + "/milestones"
	api.HandleMilestones(wM2, rM2)
	require.Equal(t, http.StatusCreated, wM2.Code)
	var m2 struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(wM2.Body.Bytes(), &m2))

	// Create one task per milestone.
	body1 := fmt.Sprintf(`{"title":"Task for M1","action":"llm","workspace_id":%q,"milestone_id":%q}`, proj1ID, m1.ID)
	wT1 := httptest.NewRecorder()
	rT1 := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body1))
	rT1.Header.Set("Content-Type", "application/json")
	rT1.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wT1, rT1)
	require.Equal(t, http.StatusCreated, wT1.Code)
	var task1 gen.Task
	require.NoError(t, json.Unmarshal(wT1.Body.Bytes(), &task1))

	body2 := fmt.Sprintf(`{"title":"Task for M2","action":"llm","workspace_id":%q,"milestone_id":%q}`, proj2ID, m2.ID)
	wT2 := httptest.NewRecorder()
	rT2 := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body2))
	rT2.Header.Set("Content-Type", "application/json")
	rT2.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wT2, rT2)
	require.Equal(t, http.StatusCreated, wT2.Code)
	var task2 gen.Task
	require.NoError(t, json.Unmarshal(wT2.Body.Bytes(), &task2))

	// GET ?milestone_id=M1 → only task1 returned.
	wFilter := httptest.NewRecorder()
	rFilter := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?milestone_id="+m1.ID, nil)
	rFilter.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wFilter, rFilter)
	require.Equal(t, http.StatusOK, wFilter.Code)
	var respItems []gen.Task
	require.NoError(t, json.Unmarshal(wFilter.Body.Bytes(), &respItems))

	m1IDs := make([]string, 0, len(respItems))
	for _, item := range respItems {
		m1IDs = append(m1IDs, item.Id)
	}
	assert.Contains(t, m1IDs, task1.Id, "task1 must appear in ?milestone_id=M1 response")
	assert.NotContains(t, m1IDs, task2.Id, "task2 must NOT appear in ?milestone_id=M1 response")
}

// TestHandleTasks_TaskCount_IncludesAllTasks verifies project task_count counts all unified tasks.
// BDD: Given a project with tasks,
// When GET /api/v1/workspaces/{id} is called,
// Then task_count reflects the total count.
// Traces to: FR-002 (unified task count)
func TestHandleTasks_TaskCount_IncludesAllTasks(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	projID := createWorkspaceViaAPI(t, api, "TaskCountProject", "")

	// Write a unified task directly to disk (with correct workspace_id).
	gtdTaskID := "01JXGTDCOUNT0000000000001"
	writeUnifiedTaskFile(t, api.homePath, gtdTaskID, projID, "inbox")

	// GET /api/v1/workspaces/{id} → task_count must be 1.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+projID, nil)
	rGet.URL.Path = "/api/v1/workspaces/" + projID
	api.HandleWorkspaces(wGet, rGet)

	require.Equal(t, http.StatusOK, wGet.Code)
	var proj gen.Workspace
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &proj))
	assert.Equal(t, 1, proj.TaskCount,
		"task_count must be 1 for the unified task written directly to disk")
}

// TestHandleTasks_OwnershipScoping verifies FR-1.9: owner is attribution-only;
// any authenticated user can access any task.
// BDD: Given alice creates task T with owner "alice",
// When bob (role=user) calls GET /api/v1/tasks/{id},
// Then 200 (FR-1.9: owner gate removed).
// When admin calls GET /api/v1/tasks/{id},
// Then 200.
// Traces to: FR-1.9 (owner gate removed)
func TestHandleTasks_OwnershipScoping(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	// Alice creates a task.
	body := fmt.Sprintf(`{"title":"AliceTask","action":"llm","workspace_id":%q,"prompt":"do something"}`, wsID)
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/tasks"
	rPost = rPost.WithContext(contextWithUser(rPost.Context(), "alice"))
	api.HandleTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code, "alice POST must return 201; body=%s", wPost.Body.String())
	var tsk gen.Task
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &tsk))
	require.NotEmpty(t, tsk.Id)

	// Verify owner is set to alice (attribution).
	assert.Equal(t, "alice", tsk.Owner, "owner must equal alice")

	// FR-1.9: Bob calls GET /api/v1/tasks/{id} → 200 (owner gate removed).
	wGetBob := httptest.NewRecorder()
	rGetBob := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+tsk.Id, nil)
	rGetBob.URL.Path = "/api/v1/tasks/" + tsk.Id
	rGetBob = rGetBob.WithContext(contextWithUser(rGetBob.Context(), "bob"))
	api.HandleTasks(wGetBob, rGetBob)
	assert.Equal(t, http.StatusOK, wGetBob.Code,
		"FR-1.9: bob must get 200 on alice's task (owner attribution-only); body=%s", wGetBob.Body.String())

	// FR-1.9: Bob calls GET /api/v1/tasks (list) — alice's task must appear.
	wListBob := httptest.NewRecorder()
	rListBob := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rListBob.URL.Path = "/api/v1/tasks"
	rListBob = rListBob.WithContext(contextWithUser(rListBob.Context(), "bob"))
	api.HandleTasks(wListBob, rListBob)
	require.Equal(t, http.StatusOK, wListBob.Code)
	var listItems []gen.Task
	require.NoError(t, json.Unmarshal(wListBob.Body.Bytes(), &listItems))
	foundAlice := false
	for _, item := range listItems {
		if item.Id == tsk.Id {
			foundAlice = true
		}
	}
	assert.True(t, foundAlice, "FR-1.9: alice's task must appear in bob's task list (no owner gate)")

	// Admin calls GET /api/v1/tasks/{id} → 200.
	wGetAdmin := httptest.NewRecorder()
	rGetAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+tsk.Id, nil)
	rGetAdmin.URL.Path = "/api/v1/tasks/" + tsk.Id
	rGetAdmin = rGetAdmin.WithContext(contextWithUser(rGetAdmin.Context(), "admin"))
	api.HandleTasks(wGetAdmin, rGetAdmin)
	assert.Equal(t, http.StatusOK, wGetAdmin.Code,
		"admin must get 200 on alice's task; body=%s", wGetAdmin.Body.String())
}

// TestEnsureDefaultWorkspace verifies the ensureDefaultWorkspace function is idempotent.
// BDD: Given a fresh home directory,
// When ensureDefaultWorkspace is called,
// Then GET /api/v1/workspaces returns exactly one project with is_default=true.
// When ensureDefaultWorkspace is called again,
// Then still exactly one default project (idempotent).
// Traces to: FR-L2-001 (auto-create default project)
func TestEnsureDefaultWorkspace(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// First call: must create the default project.
	require.NoError(t, ensureDefaultWorkspace(api.homePath, "", nil),
		"first ensureDefaultWorkspace call must not return an error")

	// GET /api/v1/workspaces → must contain exactly one default project named "My Workspace".
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	rList.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wList, rList)
	require.Equal(t, http.StatusOK, wList.Code, "GET /workspaces must return 200")

	var projects []gen.Workspace
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &projects))

	defaultCount := 0
	for _, p := range projects {
		if p.IsDefault != nil && *p.IsDefault {
			defaultCount++
			assert.Equal(t, "My Workspace", p.Name, "default project display name must be 'My Workspace'")
			assert.Equal(t, gen.WorkspaceStatusActive, p.Status, "default project must have status=active")
		}
	}
	assert.Equal(t, 1, defaultCount, "exactly one default project must exist after first call")

	// Second call: must be idempotent — still exactly one default project.
	require.NoError(t, ensureDefaultWorkspace(api.homePath, "", nil),
		"second ensureDefaultWorkspace call must not return an error")

	wList2 := httptest.NewRecorder()
	rList2 := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	rList2.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(wList2, rList2)
	require.Equal(t, http.StatusOK, wList2.Code)

	var projects2 []gen.Workspace
	require.NoError(t, json.Unmarshal(wList2.Body.Bytes(), &projects2))

	defaultCount2 := 0
	for _, p := range projects2 {
		if p.IsDefault != nil && *p.IsDefault {
			defaultCount2++
		}
	}
	assert.Equal(t, 1, defaultCount2,
		"ensureDefaultWorkspace must be idempotent: still exactly one default project after second call")
}

// Ensure bytes import is used — bytes.NewReader is used in patchTask via bytes pkg.
var _ = bytes.NewReader
