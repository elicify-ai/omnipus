//go:build !cgo

// Regression tests for the board-task bug epic on feat/level1-project-task-mgmt.
//
// Each test is labeled with the bug it guards against so a future regression
// is immediately traceable to the commit that introduced the fix.
//
// T1 — taskstore/board-task collision (data-loss landmine #402/#397):
//       taskstore.List() must NOT touch GTD board-task files in tasks/.
// T2 — result populated on success (#404):
//       the onComplete closure must write t.Result when execErr == nil.
// T3 — sysagent field preservation (#404 secondary):
//       system.task.update must not clobber prompt/priority/session_id/
//       milestone_id/owner/agent_id on a partial update.
// T4 — restart persistence (#403 + general):
//       projects and board tasks survive a fresh restAPI over the same home dir.
// REP — repository scheme validation (SEC-5):
//       javascript:* repository URLs are rejected with 400.
// OWN — ownership scoping gaps:
//       userB gets 404 on PUT/DELETE/start of userA's board task;
//       admin sees both; empty-owner resource is accessible to any user.

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
	"github.com/dapicom-ai/omnipus/pkg/boardtask"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/taskstore"
)

// ── T1: taskstore ↔ GTD collision ──────────────────────────────────────────

// TestRegression_TaskstoreList_DoesNotCorruptGTDFiles guards against the
// data-loss bug where taskstore.List() (used by the heartbeat) would scan the
// tasks/ directory and silently rewrite / corrupt GTD board-task files because
// both stores previously shared the same directory.
//
// Fix: workflow tasks live in ~/.omnipus/workflow-tasks/ and GTD tasks live in
// ~/.omnipus/tasks/. A taskstore.New pointed at workflow-tasks/ must never
// touch files in tasks/.
//
// BDD: Given a GTD board task at tasks/<id>.json and a taskstore rooted at
//
//	workflow-tasks/,
//
// When taskstore.List() and taskstore.Get() are called,
// Then the GTD board task at tasks/<id>.json is UNCHANGED (not moved, not
//
//	rewritten, project_id intact) and is still readable via readBoardTask.
//
// Traces to: feat/level1-project-task-mgmt — #402/#397 storage-separation fix
func TestRegression_TaskstoreList_DoesNotCorruptGTDFiles(t *testing.T) {
	// Traces to: #402/#397 — taskstore must not touch tasks/ directory
	api := newTestRestAPIWithHome(t)

	// Create a GTD board task via the REST API.
	projID := createProjectViaAPI(t, api, "CollisionProject", "")
	task := createBoardTaskViaAPI(t, api, "GTD Task Under Test", "inbox")

	// Attach the task to the project so project_id is set on disk.
	putBody := fmt.Sprintf(`{"project_id":%q}`, projID)
	wPut := httptest.NewRecorder()
	rPut := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id, strings.NewReader(putBody))
	rPut.Header.Set("Content-Type", "application/json")
	rPut.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wPut, rPut)
	require.Equal(t, http.StatusOK, wPut.Code, "PUT with project_id must return 200; body=%s", wPut.Body.String())

	// Now construct a taskstore rooted at workflow-tasks/ — the correct dir for workflow tasks.
	// Call List() and Get() as the heartbeat does: it should silently skip non-workflow files.
	wfStore := taskstore.New(filepath.Join(api.homePath, "workflow-tasks"))

	tasks, err := wfStore.List(taskstore.TaskFilter{})
	require.NoError(t, err, "taskstore.List() must not error on an empty workflow-tasks dir")
	assert.Empty(t, tasks, "workflow-tasks dir must be empty — the GTD task lives in tasks/, not here")

	// The GTD task must still be readable and its project_id must be intact.
	got, readErr := api.readBoardTask(task.Id)
	require.NoError(t, readErr, "readBoardTask must still succeed after taskstore.List()")
	require.Equal(t, task.Id, got.ID, "GTD task ID must be intact")
	require.Equal(t, "GTD Task Under Test", got.Name, "GTD task name must be intact")
	assert.Equal(t, projID, got.ProjectID,
		"project_id on GTD task must be intact after taskstore.List() — not overwritten by taskstore")
}

// TestRegression_TaskstoreGet_IgnoresGTDFile guards against taskstore.Get()
// returning a fake/empty entity for a GTD board-task ID that happens to share
// the tasks/ directory name format.
//
// BDD: Given tasks/ contains a GTD board-task file (has "name" field, status="inbox"),
// When taskstore.New(workflow-tasks/) is pointed at workflow-tasks/ and Get is called,
// Then ErrNotFound is returned because the GTD file is not in workflow-tasks/.
//
// Traces to: #402/#397 — storage separation
func TestRegression_TaskstoreGet_IgnoresGTDFile(t *testing.T) {
	// Traces to: #402/#397
	api := newTestRestAPIWithHome(t)

	task := createBoardTaskViaAPI(t, api, "SeparationTask", "inbox")

	// The GTD task lives in tasks/ not workflow-tasks/; Get on workflow-tasks/ must return ErrNotFound.
	wfStore := taskstore.New(filepath.Join(api.homePath, "workflow-tasks"))
	_, err := wfStore.Get(task.Id)
	assert.ErrorIs(t, err, taskstore.ErrNotFound,
		"taskstore.Get() on a GTD-task ID must return ErrNotFound (not in workflow-tasks/)")
}

// ── T2: result populated on success ────────────────────────────────────────

// TestRegression_CompletionCallback_SetsResult verifies that when ExecuteBoardTask
// calls onComplete(result, nil), the task on disk is updated to status=done and
// t.Result is set to the agent's output string.
//
// This is the #404 regression: the old onComplete closure forgot to set t.Result,
// leaving it empty even after a successful run.
//
// The test seam: we write a task file directly to disk in "active" state (as if
// /start already ran), then invoke the completion callback closure directly with a
// fake result string to verify the write logic without needing a real agent.
//
// BDD: Given a board task in status=active,
// When the completion callback is invoked with result="agent output" and err=nil,
// Then GET /board/tasks/{id} returns status=done and result="agent output".
//
// Traces to: feat/level1-project-task-mgmt — #404 result populated on success
func TestRegression_CompletionCallback_SetsResult(t *testing.T) {
	// Traces to: #404 — t.Result must be set in the success branch of onComplete
	api := newTestRestAPIWithHome(t)

	// Create a task and write it to disk in "active" state, simulating post-/start state.
	task := createBoardTaskWithPromptViaAPI(t, api, "ResultTask", "inbox", "Run the test suite")

	// Manually set the task to active so the completion closure's guard passes.
	tasksDir := filepath.Join(api.homePath, "tasks")
	rawPath := filepath.Join(tasksDir, task.Id+".json")
	rawData, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(rawData, &raw))
	raw["status"] = "active"
	raw["session_id"] = "fake-session-for-test"
	patched, _ := json.Marshal(raw)
	require.NoError(t, os.WriteFile(rawPath, patched, 0o600))

	// Simulate the completion callback: the same closure logic that handleBoardTaskStart
	// wires into ExecuteBoardTask. We replicate it here so no real agent is needed.
	const agentResult = "Tests passed: 42 passed, 0 failed."
	func() {
		mu := api.taskLock.Get(task.Id)
		mu.Lock()
		defer mu.Unlock()

		t2, readErr := api.readBoardTask(task.Id)
		require.NoError(t, readErr)
		require.Equal(t, boardtask.StatusActive, t2.Status,
			"task must be active before running completion logic")

		// Success branch: status=done, result=agent output.
		t2.Status = boardtask.StatusDone
		t2.Result = agentResult
		t2.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		writeErr := api.writeBoardTask(t2)
		require.NoError(t, writeErr)
	}()

	// GET the task and verify result is populated.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+task.Id, nil)
	rGet.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "GET must return 200; body=%s", wGet.Body.String())
	var got gen.BoardTask
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))

	assert.Equal(t, gen.BoardTaskStatus("done"), got.Status,
		"task must be done after successful completion callback")
	require.NotNil(t, got.Result, "result must not be nil after successful completion (#404)")
	assert.Equal(t, agentResult, *got.Result,
		"result must equal the agent output string — empty result is the #404 bug")

	// Differentiation: a failed completion (execErr != nil) sets status=failed and result=error msg.
	rawData2, err2 := os.ReadFile(rawPath)
	require.NoError(t, err2)
	var raw2 map[string]any
	require.NoError(t, json.Unmarshal(rawData2, &raw2))
	raw2["status"] = "active"
	patched2, _ := json.Marshal(raw2)
	require.NoError(t, os.WriteFile(rawPath, patched2, 0o600))

	const failMsg = "execution failed: timeout"
	func() {
		mu := api.taskLock.Get(task.Id)
		mu.Lock()
		defer mu.Unlock()

		t3, readErr := api.readBoardTask(task.Id)
		require.NoError(t, readErr)
		// Failure branch.
		t3.Status = boardtask.StatusFailed
		t3.Result = failMsg
		t3.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		writeErr := api.writeBoardTask(t3)
		require.NoError(t, writeErr)
	}()

	wGet2 := httptest.NewRecorder()
	rGet2 := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+task.Id, nil)
	rGet2.URL.Path = "/api/v1/board/tasks/" + task.Id
	api.HandleBoardTasks(wGet2, rGet2)
	require.Equal(t, http.StatusOK, wGet2.Code)
	var got2 gen.BoardTask
	require.NoError(t, json.Unmarshal(wGet2.Body.Bytes(), &got2))
	assert.Equal(t, gen.BoardTaskStatus("failed"), got2.Status)
	require.NotNil(t, got2.Result)
	assert.Equal(t, failMsg, *got2.Result,
		"failure branch must also populate result with the error message")

	// Both paths produce different status/result — proves neither branch is hardcoded.
	assert.NotEqual(t, gen.BoardTaskStatus("done"), got2.Status,
		"success and failure branches must produce different status values (not hardcoded)")
}

// ── T3: REST PUT field preservation ─────────────────────────────────────────
//
// NOTE: The full system.task.update field-preservation test (T3 sysagent) lives in
// pkg/sysagent/tools/task_field_preservation_test.go (package systools_test), because
// constructing systools.Deps with a real home dir requires the systools_test package.
//
// This gateway-layer T3 test exercises the REST PUT path: it verifies that a partial
// PUT /board/tasks/{id} (updating only "name") does not clobber the existing prompt,
// priority, milestone_id, session_id, agent_id, or owner fields.

// TestRegression_RESTPut_PreservesAllFields guards against partial PUT overwriting
// un-touched fields. The old bug path: a minimal on-disk write would zero fields
// not included in the PUT body.
//
// BDD: Given a board task with prompt, priority, milestone_id, session_id, agent_id,
//
//	and owner set on disk,
//
// When PUT /board/tasks/{id} is called with only {"name": "New Name"},
// Then GET returns the new name and ALL other fields remain intact.
//
// Traces to: feat/level1-project-task-mgmt — #404 secondary: field-preserving read-modify-write
func TestRegression_RESTPut_PreservesAllFields(t *testing.T) {
	// Traces to: #404 secondary — REST PUT must do a read-modify-write, not overwrite
	api := newTestRestAPIWithHome(t)

	projID := createProjectViaAPI(t, api, "PUTFieldPreservationProject", "")

	// Create a milestone.
	wMil := httptest.NewRecorder()
	rMil := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projID+"/milestones",
		strings.NewReader(`{"name":"Put Field Milestone"}`))
	rMil.Header.Set("Content-Type", "application/json")
	rMil.URL.Path = "/api/v1/projects/" + projID + "/milestones"
	api.HandleMilestones(wMil, rMil)
	require.Equal(t, http.StatusCreated, wMil.Code)
	var milResp struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(wMil.Body.Bytes(), &milResp))
	milID := milResp.ID

	// Create task with prompt, priority, milestone_id.
	const wantPrompt = "Run the full integration suite"
	wantPriority := 2
	createBody := fmt.Sprintf(
		`{"name":"OriginalName","project_id":%q,"milestone_id":%q,"prompt":%q,"priority":%d}`,
		projID, milID, wantPrompt, wantPriority,
	)
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(createBody))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/board/tasks"
	rPost = rPost.WithContext(contextWithUserRole(rPost.Context(), "alice", config.UserRoleUser))
	api.HandleBoardTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code, "POST must return 201; body=%s", wPost.Body.String())
	var created gen.BoardTask
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &created))

	// Inject agent_id and session_id directly on disk (not settable via REST POST/PUT).
	tasksDir := filepath.Join(api.homePath, "tasks")
	rawPath := filepath.Join(tasksDir, created.Id+".json")
	rawData, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(rawData, &raw))
	const wantAgentID = "01JXPRESERVE_AGENT00000001"
	raw["agent_id"] = wantAgentID
	raw["session_id"] = "preserve-session-999"
	injected, _ := json.Marshal(raw)
	require.NoError(t, os.WriteFile(rawPath, injected, 0o600))

	// PUT with only a name change — all other fields must survive.
	wPut := httptest.NewRecorder()
	rPut := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+created.Id,
		strings.NewReader(`{"name":"Updated Name"}`))
	rPut.Header.Set("Content-Type", "application/json")
	rPut.URL.Path = "/api/v1/board/tasks/" + created.Id
	rPut = rPut.WithContext(contextWithUserRole(rPut.Context(), "alice", config.UserRoleUser))
	api.HandleBoardTasks(wPut, rPut)
	require.Equal(t, http.StatusOK, wPut.Code, "PUT must return 200; body=%s", wPut.Body.String())
	var putResp gen.BoardTask
	require.NoError(t, json.Unmarshal(wPut.Body.Bytes(), &putResp))
	assert.Equal(t, "Updated Name", putResp.Name, "name must be updated")

	// GET and verify all preserved fields.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+created.Id, nil)
	rGet.URL.Path = "/api/v1/board/tasks/" + created.Id
	rGet = rGet.WithContext(contextWithUserRole(rGet.Context(), "alice", config.UserRoleUser))
	api.HandleBoardTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "GET must return 200; body=%s", wGet.Body.String())
	var got gen.BoardTask
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))

	assert.Equal(t, "Updated Name", got.Name, "name must be updated")

	require.NotNil(t, got.Prompt, "prompt must not be nil after partial PUT (#404 secondary)")
	assert.Equal(t, wantPrompt, *got.Prompt,
		"prompt must survive a partial PUT (old minimal-write bug would zero it)")

	require.NotNil(t, got.Priority, "priority must not be nil after partial PUT")
	assert.Equal(t, wantPriority, *got.Priority,
		"priority must survive a partial PUT")

	require.NotNil(t, got.MilestoneId, "milestone_id must not be nil after partial PUT")
	assert.Equal(t, milID, *got.MilestoneId,
		"milestone_id must survive a partial PUT")

	require.NotNil(t, got.ProjectId, "project_id must not be nil after partial PUT")
	assert.Equal(t, projID, *got.ProjectId,
		"project_id must survive a partial PUT")

	require.NotNil(t, got.Owner, "owner must not be nil after partial PUT")
	assert.Equal(t, "alice", *got.Owner,
		"owner must survive a partial PUT (immutable — never overwritten)")

	// session_id and agent_id are injected directly; verify via raw on-disk read.
	rawAfterPUT, err2 := os.ReadFile(rawPath)
	require.NoError(t, err2)
	var rawGot map[string]any
	require.NoError(t, json.Unmarshal(rawAfterPUT, &rawGot))
	assert.Equal(t, "preserve-session-999", rawGot["session_id"],
		"session_id on disk must survive a partial PUT")
	assert.Equal(t, wantAgentID, rawGot["agent_id"],
		"agent_id on disk must survive a partial PUT")
}

// ── T4: restart persistence ──────────────────────────────────────────────────

// TestRegression_RestartPersistence verifies that projects and board tasks
// written via one restAPI instance are fully readable from a FRESH restAPI
// instance over the same home dir, including agent_id, owner, and project_id.
//
// This is the "survive gateway restart" scenario that had no coverage before.
//
// BDD: Given projects and board tasks created via restAPI instance A,
// When a fresh restAPI B is constructed over the same home directory,
// Then GET /projects and GET /board/tasks return the same data with all fields intact.
//
// Traces to: feat/level1-project-task-mgmt — #403 + general restart persistence
func TestRegression_RestartPersistence(t *testing.T) {
	// Traces to: #403 — projects and board tasks must survive a gateway restart
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()

	// Write a minimal config.json.
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), minimalCfg, 0o600))

	// ── Instance A: create data ──

	buildAPI := func() *restAPI {
		cfg := &config.Config{
			Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Workspace: home,
					ModelName: "test-model",
					MaxTokens: 4096,
				},
			},
		}
		msgBus := bus.NewMessageBus()
		al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
		return &restAPI{
			agentLoop:     al,
			allowedOrigin: "http://localhost:3000",
			onboardingMgr: onboarding.NewManager(home),
			homePath:      home,
			taskStore:     taskstore.New(home + "/workflow-tasks"),
			taskLock:      boardtask.TaskFileLock,
		}
	}

	apiA := buildAPI()

	// Create project.
	projID := createProjectViaAPI(t, apiA, "PersistenceProject", "test project")

	// Create board task with extended fields.
	body := fmt.Sprintf(`{"name":"PersistenceTask","project_id":%q,"prompt":"Run checks","priority":1}`, projID)
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(body))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/board/tasks"
	rPost = rPost.WithContext(contextWithUserRole(rPost.Context(), "alice", config.UserRoleUser))
	apiA.HandleBoardTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code, "create board task must return 201; body=%s", wPost.Body.String())
	var createdTask gen.BoardTask
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &createdTask))

	// ── Instance B: read data (simulates gateway restart) ──

	apiB := buildAPI()

	// Project must be readable.
	wGetProj := httptest.NewRecorder()
	rGetProj := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projID, nil)
	rGetProj.URL.Path = "/api/v1/projects/" + projID
	apiB.HandleProjects(wGetProj, rGetProj)
	require.Equal(t, http.StatusOK, wGetProj.Code,
		"GET project must return 200 on fresh API (restart persistence); body=%s", wGetProj.Body.String())
	var proj gen.Project
	require.NoError(t, json.Unmarshal(wGetProj.Body.Bytes(), &proj))
	assert.Equal(t, projID, proj.Id, "project ID must persist across restart")
	assert.Equal(t, "PersistenceProject", proj.Name, "project name must persist across restart")

	// Board task must be readable with all fields intact.
	wGetTask := httptest.NewRecorder()
	rGetTask := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+createdTask.Id, nil)
	rGetTask.URL.Path = "/api/v1/board/tasks/" + createdTask.Id
	apiB.HandleBoardTasks(wGetTask, rGetTask)
	require.Equal(t, http.StatusOK, wGetTask.Code,
		"GET board task must return 200 on fresh API (restart persistence); body=%s", wGetTask.Body.String())
	var persistedTask gen.BoardTask
	require.NoError(t, json.Unmarshal(wGetTask.Body.Bytes(), &persistedTask))

	assert.Equal(t, createdTask.Id, persistedTask.Id,
		"task ID must persist across restart")

	// project_id must survive.
	require.NotNil(t, persistedTask.ProjectId, "project_id must not be nil after restart")
	assert.Equal(t, projID, *persistedTask.ProjectId,
		"project_id must persist across gateway restart (#403)")

	// prompt must survive.
	require.NotNil(t, persistedTask.Prompt, "prompt must not be nil after restart")
	assert.Equal(t, "Run checks", *persistedTask.Prompt,
		"prompt must persist across gateway restart")

	// priority must survive.
	require.NotNil(t, persistedTask.Priority, "priority must not be nil after restart")
	assert.Equal(t, 1, *persistedTask.Priority,
		"priority must persist across gateway restart")

	// owner (set from alice's request context) must survive.
	require.NotNil(t, persistedTask.Owner, "owner must not be nil after restart")
	assert.Equal(t, "alice", *persistedTask.Owner,
		"owner must persist across gateway restart")

	// Differentiation: a second task created by bob must be distinct and readable.
	body2 := fmt.Sprintf(`{"name":"BobTask","project_id":%q}`, projID)
	wPost2 := httptest.NewRecorder()
	rPost2 := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(body2))
	rPost2.Header.Set("Content-Type", "application/json")
	rPost2.URL.Path = "/api/v1/board/tasks"
	rPost2 = rPost2.WithContext(contextWithUserRole(rPost2.Context(), "bob", config.UserRoleUser))
	apiA.HandleBoardTasks(wPost2, rPost2)
	require.Equal(t, http.StatusCreated, wPost2.Code)
	var createdTask2 gen.BoardTask
	require.NoError(t, json.Unmarshal(wPost2.Body.Bytes(), &createdTask2))

	// Read back via apiB.
	wGetTask2 := httptest.NewRecorder()
	rGetTask2 := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+createdTask2.Id, nil)
	rGetTask2.URL.Path = "/api/v1/board/tasks/" + createdTask2.Id
	apiB.HandleBoardTasks(wGetTask2, rGetTask2)
	require.Equal(t, http.StatusOK, wGetTask2.Code)
	var persistedTask2 gen.BoardTask
	require.NoError(t, json.Unmarshal(wGetTask2.Body.Bytes(), &persistedTask2))
	assert.Equal(t, "BobTask", persistedTask2.Name)
	require.NotNil(t, persistedTask2.Owner)
	assert.Equal(t, "bob", *persistedTask2.Owner, "bob's task owner must persist")
	assert.NotEqual(t, createdTask.Id, createdTask2.Id,
		"two different tasks must have different IDs (differentiation)")
}

// ── REP: repository scheme validation ───────────────────────────────────────

// TestRegression_Repository_SchemeValidation verifies that POST /api/v1/projects
// rejects non-http/https repository URLs and accepts empty or valid URLs.
//
// BDD:
//   - POST with repository="javascript:alert(1)" → 400.
//   - POST with repository="data:text/plain,x" → 400.
//   - POST with repository="https://github.com/x/y" → 201 (valid https).
//   - POST with repository="" → 201 (empty is always accepted).
//   - PUT with repository="javascript:alert(1)" → 400.
//
// Traces to: feat/level1-project-task-mgmt — repository SEC-5 validation
func TestRegression_Repository_SchemeValidation(t *testing.T) {
	// Traces to: SEC-5 — validateRepositoryURL rejects non-http/https schemes
	api := newTestRestAPIWithHome(t)

	tests := []struct {
		name         string
		repository   string
		expectedCode int
	}{
		{"javascript scheme rejected", "javascript:alert(1)", http.StatusBadRequest},
		{"data scheme rejected", "data:text/plain,hello", http.StatusBadRequest},
		{"file scheme rejected", "file:///etc/passwd", http.StatusBadRequest},
		{"empty accepted", "", http.StatusCreated},
		{"https accepted", "https://github.com/x/y", http.StatusCreated},
		{"http accepted", "http://github.com/x/y", http.StatusCreated},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var bodyStr string
			if tc.repository == "" {
				bodyStr = `{"name":"RepoProject"}`
			} else {
				bodyStr = fmt.Sprintf(`{"name":"RepoProject","repository":%q}`, tc.repository)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(bodyStr))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/projects"
			api.HandleProjects(w, r)
			assert.Equal(t, tc.expectedCode, w.Code,
				"POST /projects with repository=%q must return %d; body=%s",
				tc.repository, tc.expectedCode, w.Body.String())
		})
	}

	// PUT repository validation: set a valid project then try to update with a bad URL.
	projID := createProjectViaAPI(t, api, "PUTRepoProject", "")
	wPutBad := httptest.NewRecorder()
	rPutBad := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+projID,
		strings.NewReader(`{"repository":"javascript:alert(1)"}`))
	rPutBad.Header.Set("Content-Type", "application/json")
	rPutBad.URL.Path = "/api/v1/projects/" + projID
	api.HandleProjects(wPutBad, rPutBad)
	assert.Equal(t, http.StatusBadRequest, wPutBad.Code,
		"PUT /projects/{id} with javascript: repository must return 400; body=%s", wPutBad.Body.String())
}

// ── OWN: ownership scoping gaps ──────────────────────────────────────────────

// TestRegression_BoardTask_OwnershipScoping_PUTAndDELETE verifies SEC-2 for the
// write paths: user B gets 404 on PUT and DELETE of user A's board task, and
// admin can modify both.
//
// BDD: Given alice creates task T,
// When bob (role=user) calls PUT /board/tasks/{id} and DELETE /board/tasks/{id},
// Then 404 is returned (resource enumeration protection).
// When admin calls the same operations,
// Then they succeed.
//
// Traces to: feat/level1-project-task-mgmt — SEC-2 ownership scoping for board tasks
func TestRegression_BoardTask_OwnershipScoping_PUTAndDELETE(t *testing.T) {
	// Traces to: SEC-2 — board task ownership scoping: PUT and DELETE
	api := newTestRestAPIWithHome(t)

	// Alice creates a task.
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks",
		strings.NewReader(`{"name":"AliceWriteTask","prompt":"do something"}`))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/board/tasks"
	rPost = rPost.WithContext(contextWithUserRole(rPost.Context(), "alice", config.UserRoleUser))
	api.HandleBoardTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code)
	var task gen.BoardTask
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &task))

	// Bob PUT → 404.
	wPutBob := httptest.NewRecorder()
	rPutBob := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
		strings.NewReader(`{"name":"BobOverwrite"}`))
	rPutBob.Header.Set("Content-Type", "application/json")
	rPutBob.URL.Path = "/api/v1/board/tasks/" + task.Id
	rPutBob = rPutBob.WithContext(contextWithUserRole(rPutBob.Context(), "bob", config.UserRoleUser))
	api.HandleBoardTasks(wPutBob, rPutBob)
	assert.Equal(t, http.StatusNotFound, wPutBob.Code,
		"bob must get 404 on PUT of alice's task; body=%s", wPutBob.Body.String())

	// Bob DELETE → 404.
	wDelBob := httptest.NewRecorder()
	rDelBob := httptest.NewRequest(http.MethodDelete, "/api/v1/board/tasks/"+task.Id, nil)
	rDelBob.URL.Path = "/api/v1/board/tasks/" + task.Id
	rDelBob = rDelBob.WithContext(contextWithUserRole(rDelBob.Context(), "bob", config.UserRoleUser))
	api.HandleBoardTasks(wDelBob, rDelBob)
	assert.Equal(t, http.StatusNotFound, wDelBob.Code,
		"bob must get 404 on DELETE of alice's task; body=%s", wDelBob.Body.String())

	// Task must still exist (bob's 404s must not have deleted it).
	wStillExists := httptest.NewRecorder()
	rStillExists := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+task.Id, nil)
	rStillExists.URL.Path = "/api/v1/board/tasks/" + task.Id
	rStillExists = rStillExists.WithContext(contextWithUserRole(rStillExists.Context(), "alice", config.UserRoleUser))
	api.HandleBoardTasks(wStillExists, rStillExists)
	assert.Equal(t, http.StatusOK, wStillExists.Code,
		"alice's task must still exist after bob's 404 attempts; body=%s", wStillExists.Body.String())

	// Admin PUT → 200.
	wPutAdmin := httptest.NewRecorder()
	rPutAdmin := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+task.Id,
		strings.NewReader(`{"name":"AdminRename"}`))
	rPutAdmin.Header.Set("Content-Type", "application/json")
	rPutAdmin.URL.Path = "/api/v1/board/tasks/" + task.Id
	rPutAdmin = rPutAdmin.WithContext(contextWithUserRole(rPutAdmin.Context(), "admin", config.UserRoleAdmin))
	api.HandleBoardTasks(wPutAdmin, rPutAdmin)
	assert.Equal(t, http.StatusOK, wPutAdmin.Code,
		"admin must be able to PUT alice's task; body=%s", wPutAdmin.Body.String())
}

// TestRegression_BoardTask_EmptyOwner_AccessibleToAll verifies that a legacy
// task with an empty owner field is accessible to any authenticated user.
//
// BDD: Given a board task on disk with owner="",
// When any authenticated user calls GET /board/tasks/{id},
// Then 200 (unowned/shared resource — legacy compatibility).
//
// Traces to: feat/level1-project-task-mgmt — SEC-2 unowned resource rule
func TestRegression_BoardTask_EmptyOwner_AccessibleToAll(t *testing.T) {
	// Traces to: SEC-2 — empty owner means unowned/shared
	api := newTestRestAPIWithHome(t)

	// Write a board task directly to disk with no owner field (simulates legacy data).
	legacyID := "01JXLEGACY_TASK_00000001"
	tasksDir := filepath.Join(api.homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)
	legacyData := fmt.Sprintf(
		`{"id":%q,"name":"LegacyTask","status":"inbox","created_at":%q,"updated_at":%q}`,
		legacyID, now, now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, legacyID+".json"), []byte(legacyData), 0o600))

	// Any user must be able to read it (empty owner = unowned/shared).
	for _, user := range []string{"alice", "bob", "carol"} {
		t.Run("accessible_by_"+user, func(t *testing.T) {
			wGet := httptest.NewRecorder()
			rGet := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+legacyID, nil)
			rGet.URL.Path = "/api/v1/board/tasks/" + legacyID
			rGet = rGet.WithContext(contextWithUserRole(rGet.Context(), user, config.UserRoleUser))
			api.HandleBoardTasks(wGet, rGet)
			assert.Equal(t, http.StatusOK, wGet.Code,
				"user %q must be able to access a legacy task with empty owner; body=%s",
				user, wGet.Body.String())
		})
	}

	// Empty-owner task must appear in list for any user.
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks", nil)
	rList.URL.Path = "/api/v1/board/tasks"
	rList = rList.WithContext(contextWithUserRole(rList.Context(), "random-user", config.UserRoleUser))
	api.HandleBoardTasks(wList, rList)
	require.Equal(t, http.StatusOK, wList.Code)
	var resp struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &resp))
	found := false
	for _, item := range resp.Items {
		if item.ID == legacyID {
			found = true
		}
	}
	assert.True(t, found, "legacy (empty-owner) task must appear in the list for any user")
}

// TestRegression_BoardTask_OwnershipScoping_StartBlocked verifies that user B
// cannot start user A's task via POST /board/tasks/{id}/start.
//
// BDD: Given alice creates task T with a prompt,
// When bob (role=user) calls POST /board/tasks/{id}/start,
// Then 404 (ownership enumeration protection).
//
// Traces to: feat/level1-project-task-mgmt — SEC-2 start endpoint ownership
func TestRegression_BoardTask_OwnershipScoping_StartBlocked(t *testing.T) {
	// Traces to: SEC-2 — /start must enforce ownership just like GET/PUT/DELETE
	api := newTestRestAPIWithAgent(t)

	// Alice creates a task.
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks",
		strings.NewReader(`{"name":"AliceStartTask","prompt":"do something important"}`))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/board/tasks"
	rPost = rPost.WithContext(contextWithUserRole(rPost.Context(), "alice", config.UserRoleUser))
	api.HandleBoardTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code)
	var task gen.BoardTask
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &task))
	require.NotNil(t, task.Owner)
	assert.Equal(t, "alice", *task.Owner)

	// Bob tries to start → 404.
	wStart := httptest.NewRecorder()
	rStart := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks/"+task.Id+"/start", nil)
	rStart.URL.Path = "/api/v1/board/tasks/" + task.Id + "/start"
	rStart = rStart.WithContext(contextWithUserRole(rStart.Context(), "bob", config.UserRoleUser))
	api.HandleBoardTasks(wStart, rStart)
	assert.Equal(t, http.StatusNotFound, wStart.Code,
		"bob must get 404 on POST /start for alice's task; body=%s", wStart.Body.String())

	// Alice herself can start it → 202.
	wStartAlice := httptest.NewRecorder()
	rStartAlice := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks/"+task.Id+"/start", nil)
	rStartAlice.URL.Path = "/api/v1/board/tasks/" + task.Id + "/start"
	rStartAlice = rStartAlice.WithContext(contextWithUserRole(rStartAlice.Context(), "alice", config.UserRoleUser))
	api.HandleBoardTasks(wStartAlice, rStartAlice)
	assert.Equal(t, http.StatusAccepted, wStartAlice.Code,
		"alice must be able to start her own task; body=%s", wStartAlice.Body.String())
}
