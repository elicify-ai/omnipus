// Regression tests for board-task / unified-task bugs.
//
// Sprint 2 changes:
//   - T1 (taskstore ↔ boardtask directory collision) is DELETED — there is now ONE unified
//     store in tasks/, so the dual-store collision is structurally impossible.
//   - T2 (completion callback sets result) is REWRITTEN to use taskStore.Update directly
//     and GET /api/v1/tasks/{id} for verification.
//   - T3 (REST PATCH field preservation) is REWRITTEN to use PATCH /api/v1/tasks/{id}.
//   - T4 (restart persistence) is REWRITTEN to use POST/GET /api/v1/tasks.
//   - OWN tests are REWRITTEN to use /api/v1/tasks.
//   - TestRegression_BoardTask_OwnershipScoping_StartAllowed is DELETED — /start is REMOVED.
//   - REP (repository scheme validation) is UNCHANGED (uses /api/v1/workspaces only).
//
// Traces to: feat/level1-project-task-mgmt regressions #402/#397/#404/#403 + FR-1.9.

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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// ── T2: result populated on success ────────────────────────────────────────

// TestRegression_CompletionCallback_SetsResult verifies that when a task
// completes, the result field is persisted and readable via GET /api/v1/tasks/{id}.
//
// Sprint 2: the completion callback now uses task.Store.Update (Patch) to set
// result + status=done atomically. The old readBoardTask/writeBoardTask seam is
// replaced by taskStore.Get / taskStore.Update.
//
// BDD: Given a task in status=in_progress,
// When the completion callback writes result="agent output" and status=done,
// Then GET /api/v1/tasks/{id} returns status=done and result="agent output".
//
// Traces to: feat/level1-project-task-mgmt — #404 result populated on success
func TestRegression_CompletionCallback_SetsResult(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	// Create a task.
	tsk := createTaskViaAPI(t, api, "ResultTask", wsID)

	// Advance to in_progress (simulating /start being replaced by PATCH status=in_progress).
	wInProg := patchTask(t, api, tsk.Id, `{"status":"in_progress","prompt":"Run the test suite"}`)
	require.Equal(t, http.StatusOK, wInProg.Code,
		"PATCH status=in_progress must return 200; body=%s", wInProg.Body.String())

	// Simulate the completion callback: write status=done + result via taskStore.Update directly.
	const agentResult = "Tests passed: 42 passed, 0 failed."
	doneStatus := task.StatusDone
	resultStr := agentResult
	nowStr := time.Now().UTC().Format(time.RFC3339)
	patch := task.Patch{
		Status:      &doneStatus,
		Result:      &resultStr,
		CompletedAt: &nowStr,
	}
	_, err := api.taskStore.Update(tsk.Id, patch)
	require.NoError(t, err, "taskStore.Update with status=done+result must succeed")

	// GET the task and verify result is populated.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+tsk.Id, nil)
	rGet.URL.Path = "/api/v1/tasks/" + tsk.Id
	api.HandleTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "GET must return 200; body=%s", wGet.Body.String())
	var got gen.Task
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))

	assert.Equal(t, gen.TaskStatus("done"), got.Status,
		"task must be done after successful completion callback")
	require.NotNil(t, got.Result, "result must not be nil after successful completion (#404)")
	assert.Equal(t, agentResult, *got.Result,
		"result must equal the agent output string — empty result is the #404 bug")

	// Differentiation: a failed run sets status=failed and a different result.
	// Create a SEPARATE fresh task for this branch — the done task above is
	// terminal and cannot be restored to in_progress (done is frozen by design).
	tsk2 := createTaskViaAPI(t, api, "FailedResultTask", wsID)
	wInProg2 := patchTask(t, api, tsk2.Id, `{"status":"in_progress","prompt":"Run failing suite"}`)
	require.Equal(t, http.StatusOK, wInProg2.Code,
		"PATCH status=in_progress on second task must return 200; body=%s", wInProg2.Body.String())

	const failMsg = "execution failed: timeout"
	failedStatus := task.StatusFailed
	failResultStr := failMsg
	_, err3 := api.taskStore.Update(tsk2.Id, task.Patch{
		Status: &failedStatus,
		Result: &failResultStr,
	})
	require.NoError(t, err3, "taskStore.Update with status=failed+result must succeed")

	wGet2 := httptest.NewRecorder()
	rGet2 := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+tsk2.Id, nil)
	rGet2.URL.Path = "/api/v1/tasks/" + tsk2.Id
	api.HandleTasks(wGet2, rGet2)
	require.Equal(t, http.StatusOK, wGet2.Code, "GET after failure must return 200; body=%s", wGet2.Body.String())
	var got2 gen.Task
	require.NoError(t, json.Unmarshal(wGet2.Body.Bytes(), &got2))
	assert.Equal(t, gen.TaskStatus("failed"), got2.Status,
		"task must be failed after failure callback")
	require.NotNil(t, got2.Result, "result must be set on failure branch too (#404)")
	assert.Equal(t, failMsg, *got2.Result,
		"failure branch must populate result with the error message")

	// Both paths produce different status/result — proves neither is hardcoded.
	assert.NotEqual(t, gen.TaskStatus("done"), got2.Status,
		"success and failure branches must produce different status values (not hardcoded)")
}

// ── T3: REST PATCH field preservation ─────────────────────────────────────────

// TestRegression_RESTPatch_PreservesAllFields guards against partial PATCH
// clobbering un-touched fields. The old bug path: a minimal write would zero
// fields not included in the PATCH body (old PUT semantic from boardtask era).
//
// BDD: Given a task with prompt, priority, tags, session_id, agent_id
//
//	set on disk,
//
// When PATCH /api/v1/tasks/{id} is called with only {"title": "New Title"},
// Then GET returns the new title and ALL other fields remain intact.
//
// Traces to: feat/level1-project-task-mgmt — #404 secondary: field-preserving PATCH
func TestRegression_RESTPatch_PreservesAllFields(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	projID := createWorkspaceViaAPI(t, api, "PATCHFieldPreservationProject", "")

	// Create task with prompt, priority, tags.
	const wantPrompt = "Run the full integration suite"
	wantPriority := 2
	createBody := fmt.Sprintf(
		`{"title":"OriginalTitle","action":"llm","workspace_id":%q,"tags":["release-1"],"prompt":%q,"priority":%d}`,
		projID, wantPrompt, wantPriority,
	)
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(createBody))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/tasks"
	rPost = rPost.WithContext(contextWithUser(rPost.Context(), "alice"))
	api.HandleTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code, "POST must return 201; body=%s", wPost.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &created))

	// Inject agent_id and session_id directly on disk
	// (agent_id is set via PATCH; session_id is set by the run engine on disk).
	tasksDir := filepath.Join(api.homePath, "tasks")
	rawPath := filepath.Join(tasksDir, created.Id+".json")
	rawData, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(rawData, &raw))
	const wantAgentID = "01JXPRESERVE_AGENT00000001"
	raw["agent_id"] = wantAgentID
	raw["session_id"] = "preserve-session-999"
	injected, err := json.Marshal(raw)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(rawPath, injected, 0o600))

	// PATCH with only a title change — all other fields must survive.
	wPatch := patchTask(t, api, created.Id, `{"title":"Updated Title"}`)
	require.Equal(t, http.StatusOK, wPatch.Code, "PATCH must return 200; body=%s", wPatch.Body.String())
	var patchResp gen.Task
	require.NoError(t, json.Unmarshal(wPatch.Body.Bytes(), &patchResp))
	assert.Equal(t, "Updated Title", patchResp.Title, "title must be updated")

	// GET and verify all preserved fields.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+created.Id, nil)
	rGet.URL.Path = "/api/v1/tasks/" + created.Id
	rGet = rGet.WithContext(contextWithUser(rGet.Context(), "alice"))
	api.HandleTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "GET must return 200; body=%s", wGet.Body.String())
	var got gen.Task
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))

	assert.Equal(t, "Updated Title", got.Title, "title must be updated")

	require.NotNil(t, got.Prompt, "prompt must not be nil after partial PATCH (#404 secondary)")
	assert.Equal(t, wantPrompt, *got.Prompt,
		"prompt must survive a partial PATCH (old minimal-write bug would zero it)")

	require.NotNil(t, got.Priority, "priority must not be nil after partial PATCH")
	assert.Equal(t, wantPriority, *got.Priority,
		"priority must survive a partial PATCH")

	require.NotNil(t, got.Tags, "tags must not be nil after partial PATCH")
	assert.Contains(t, *got.Tags, "release-1",
		"tags must survive a partial PATCH")

	assert.Equal(t, projID, got.WorkspaceId,
		"workspace_id must survive a partial PATCH")

	assert.Equal(t, "alice", got.Owner,
		"owner must survive a partial PATCH (immutable — attribution only)")

	// session_id and agent_id are injected directly; verify via raw on-disk read.
	rawAfterPATCH, err2 := os.ReadFile(rawPath)
	require.NoError(t, err2)
	var rawGot map[string]any
	require.NoError(t, json.Unmarshal(rawAfterPATCH, &rawGot))
	assert.Equal(t, "preserve-session-999", rawGot["session_id"],
		"session_id on disk must survive a partial PATCH")
	assert.Equal(t, wantAgentID, rawGot["agent_id"],
		"agent_id on disk must survive a partial PATCH")
}

// ── T4: restart persistence ──────────────────────────────────────────────────

// TestRegression_RestartPersistence verifies that workspaces and tasks
// written via one restAPI instance are fully readable from a FRESH restAPI
// instance over the same home dir.
//
// BDD: Given a workspace and task created via restAPI instance A,
// When a fresh restAPI B is constructed over the same home directory,
// Then GET /workspaces and GET /tasks return the same data with all fields intact.
//
// Traces to: feat/level1-project-task-mgmt — #403 + general restart persistence
func TestRegression_RestartPersistence(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()

	// Write a minimal config.json.
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), minimalCfg, 0o600))

	buildAPI := func() *restAPI {
		cfg := &config.Config{
			Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home:         home,
					DefaultModel: config.DefaultModel{Model: "test-model"},
					MaxTokens:    4096,
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
			taskStore:     task.New(home + "/tasks"),
			taskLock:      task.TaskFileLock,
		}
	}

	apiA := buildAPI()

	// Create workspace.
	projID := createWorkspaceViaAPI(t, apiA, "PersistenceProject", "test project")

	// Create a task with extended fields.
	createBody := fmt.Sprintf(
		`{"title":"PersistenceTask","action":"llm","workspace_id":%q,"prompt":"Run checks","priority":1}`,
		projID,
	)
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(createBody))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/tasks"
	rPost = rPost.WithContext(contextWithUser(rPost.Context(), "alice"))
	apiA.HandleTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code, "create task must return 201; body=%s", wPost.Body.String())
	var createdTask gen.Task
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &createdTask))

	// ── Instance B: read data (simulates gateway restart) ──
	apiB := buildAPI()

	// Workspace must be readable.
	wGetProj := httptest.NewRecorder()
	rGetProj := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+projID, nil)
	rGetProj.URL.Path = "/api/v1/workspaces/" + projID
	apiB.HandleWorkspaces(wGetProj, rGetProj)
	require.Equal(t, http.StatusOK, wGetProj.Code,
		"GET workspace must return 200 on fresh API (restart persistence); body=%s", wGetProj.Body.String())
	var proj gen.Workspace
	require.NoError(t, json.Unmarshal(wGetProj.Body.Bytes(), &proj))
	assert.Equal(t, projID, proj.Id, "workspace ID must persist across restart")
	assert.Equal(t, "PersistenceProject", proj.Name, "project name must persist across restart")

	// Task must be readable with all fields intact.
	wGetTask := httptest.NewRecorder()
	rGetTask := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+createdTask.Id, nil)
	rGetTask.URL.Path = "/api/v1/tasks/" + createdTask.Id
	apiB.HandleTasks(wGetTask, rGetTask)
	require.Equal(t, http.StatusOK, wGetTask.Code,
		"GET task must return 200 on fresh API (restart persistence); body=%s", wGetTask.Body.String())
	var persistedTask gen.Task
	require.NoError(t, json.Unmarshal(wGetTask.Body.Bytes(), &persistedTask))

	assert.Equal(t, createdTask.Id, persistedTask.Id,
		"task ID must persist across restart")
	assert.Equal(t, projID, persistedTask.WorkspaceId,
		"workspace_id must persist across gateway restart (#403)")
	require.NotNil(t, persistedTask.Prompt, "prompt must not be nil after restart")
	assert.Equal(t, "Run checks", *persistedTask.Prompt,
		"prompt must persist across gateway restart")
	require.NotNil(t, persistedTask.Priority, "priority must not be nil after restart")
	assert.Equal(t, 1, *persistedTask.Priority,
		"priority must persist across gateway restart")
	assert.Equal(t, "alice", persistedTask.Owner,
		"owner must persist across gateway restart")

	// Differentiation: a second task created by bob must be distinct and readable.
	createBody2 := fmt.Sprintf(
		`{"title":"BobTask","action":"llm","workspace_id":%q}`,
		projID,
	)
	wPost2 := httptest.NewRecorder()
	rPost2 := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(createBody2))
	rPost2.Header.Set("Content-Type", "application/json")
	rPost2.URL.Path = "/api/v1/tasks"
	rPost2 = rPost2.WithContext(contextWithUser(rPost2.Context(), "bob"))
	apiA.HandleTasks(wPost2, rPost2)
	require.Equal(t, http.StatusCreated, wPost2.Code, "bob's task POST must return 201; body=%s", wPost2.Body.String())
	var createdTask2 gen.Task
	require.NoError(t, json.Unmarshal(wPost2.Body.Bytes(), &createdTask2))

	// Read back via apiB.
	wGetTask2 := httptest.NewRecorder()
	rGetTask2 := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+createdTask2.Id, nil)
	rGetTask2.URL.Path = "/api/v1/tasks/" + createdTask2.Id
	apiB.HandleTasks(wGetTask2, rGetTask2)
	require.Equal(
		t,
		http.StatusOK,
		wGetTask2.Code,
		"GET bob's task via apiB must return 200; body=%s",
		wGetTask2.Body.String(),
	)
	var persistedTask2 gen.Task
	require.NoError(t, json.Unmarshal(wGetTask2.Body.Bytes(), &persistedTask2))
	assert.Equal(t, "BobTask", persistedTask2.Title, "bob's task title must persist")
	assert.Equal(t, "bob", persistedTask2.Owner, "bob's task owner must persist")
	assert.NotEqual(t, createdTask.Id, createdTask2.Id,
		"two different tasks must have different IDs (differentiation)")
}

// ── REP: repository field retirement ────────────────────────────────────────

// TestRegression_Repository_FieldRetired verifies that POST and PUT
// /api/v1/workspaces reject any request body carrying a "repository" field
// outright (FR-9.2, ADR-063 D7) — the field was deleted from the wire with no
// back-compat, superseding the earlier SEC-5 URL-scheme-only validation this
// test used to pin.
//
// BDD:
//   - POST with repository="javascript:alert(1)" → 400 (field retired).
//   - POST with repository="https://github.com/x/y" → 400 (field retired,
//     even though the URL itself would have been valid under the old rule).
//   - POST with no repository field at all → 201 (unaffected).
//   - PUT with repository="javascript:alert(1)" → 400 (field retired).
//
// Traces to: FR-9.2, ADR-063 D7.
func TestRegression_Repository_FieldRetired(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	tests := []struct {
		name         string
		repository   string
		hasField     bool
		expectedCode int
	}{
		{"javascript rejected — field retired", "javascript:alert(1)", true, http.StatusBadRequest},
		{"https rejected — field retired regardless of value", "https://github.com/x/y", true, http.StatusBadRequest},
		{"no field accepted", "", false, http.StatusCreated},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var bodyStr string
			if !tc.hasField {
				bodyStr = `{"name":"RepoProject"}`
			} else {
				bodyStr = fmt.Sprintf(`{"name":"RepoProject","repository":%q}`, tc.repository)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(bodyStr))
			r.Header.Set("Content-Type", "application/json")
			r.URL.Path = "/api/v1/workspaces"
			api.HandleWorkspaces(w, r)
			assert.Equal(t, tc.expectedCode, w.Code,
				"POST /workspaces with repository field present=%v must return %d; body=%s",
				tc.hasField, tc.expectedCode, w.Body.String())
		})
	}

	// PUT with a repository field is rejected outright, same as POST.
	projID := createWorkspaceViaAPI(t, api, "PUTRepoProject", "")
	wPutBad := httptest.NewRecorder()
	rPutBad := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+projID,
		strings.NewReader(`{"repository":"javascript:alert(1)"}`))
	rPutBad.Header.Set("Content-Type", "application/json")
	rPutBad.URL.Path = "/api/v1/workspaces/" + projID
	api.HandleWorkspaces(wPutBad, rPutBad)
	assert.Equal(t, http.StatusBadRequest, wPutBad.Code,
		"PUT /workspaces/{id} with repository field must return 400; body=%s", wPutBad.Body.String())
}

// ── OWN: ownership scoping gaps ──────────────────────────────────────────────

// TestRegression_Task_OwnershipScoping_PATCHAndDELETE verifies FR-1.9 for the
// write paths: owner is attribution-only (single-user), so any authenticated user
// may PATCH or DELETE any task — cross-owner access is no longer denied.
//
// BDD: Given alice creates task T (owner="alice"),
// When bob (role=user) calls PATCH /tasks/{id},
// Then 200 is returned and the task title is updated.
// When admin calls PATCH /tasks/{id},
// Then 200 is returned.
// When bob calls DELETE /tasks/{id},
// Then 204 is returned and the task no longer exists.
//
// Traces to: feat/level1-project-task-mgmt — FR-1.9 owner attribution-only
func TestRegression_Task_OwnershipScoping_PATCHAndDELETE(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	// Alice creates a task.
	body := fmt.Sprintf(`{"title":"AliceWriteTask","action":"llm","workspace_id":%q,"prompt":"do something"}`, wsID)
	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	rPost.Header.Set("Content-Type", "application/json")
	rPost.URL.Path = "/api/v1/tasks"
	rPost = rPost.WithContext(contextWithUser(rPost.Context(), "alice"))
	api.HandleTasks(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code, "alice's POST must return 201; body=%s", wPost.Body.String())
	var tsk gen.Task
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &tsk))

	// Bob PATCH → 200 (cross-owner access allowed — FR-1.9 gate removed).
	wPatchBob := patchTask(t, api, tsk.Id, `{"title":"BobOverwrite"}`)
	wPatchBob.Body.Bytes() // ensure body is consumed for the context check below
	// Note: context is set on the request object, not the recorder — we need to
	// pass context directly. Use a raw request here:
	wPatchBobRec := httptest.NewRecorder()
	rPatchBob := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+tsk.Id,
		strings.NewReader(`{"title":"BobOverwrite"}`))
	rPatchBob.Header.Set("Content-Type", "application/json")
	rPatchBob.URL.Path = "/api/v1/tasks/" + tsk.Id
	rPatchBob = rPatchBob.WithContext(contextWithUser(rPatchBob.Context(), "bob"))
	api.HandleTasks(wPatchBobRec, rPatchBob)
	assert.Equal(t, http.StatusOK, wPatchBobRec.Code,
		"bob must get 200 on PATCH of alice's task (FR-1.9: no ownership gate); body=%s", wPatchBobRec.Body.String())
	var patchResult gen.Task
	require.NoError(t, json.Unmarshal(wPatchBobRec.Body.Bytes(), &patchResult))
	assert.Equal(t, "BobOverwrite", patchResult.Title,
		"PATCH must actually update the task title — not a no-op")

	// Admin PATCH → 200.
	wPatchAdminRec := httptest.NewRecorder()
	rPatchAdmin := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+tsk.Id,
		strings.NewReader(`{"title":"AdminRename"}`))
	rPatchAdmin.Header.Set("Content-Type", "application/json")
	rPatchAdmin.URL.Path = "/api/v1/tasks/" + tsk.Id
	rPatchAdmin = rPatchAdmin.WithContext(contextWithUser(rPatchAdmin.Context(), "admin"))
	api.HandleTasks(wPatchAdminRec, rPatchAdmin)
	assert.Equal(t, http.StatusOK, wPatchAdminRec.Code,
		"admin must be able to PATCH alice's task; body=%s", wPatchAdminRec.Body.String())
	var adminPatchResult gen.Task
	require.NoError(t, json.Unmarshal(wPatchAdminRec.Body.Bytes(), &adminPatchResult))
	assert.Equal(t, "AdminRename", adminPatchResult.Title,
		"admin PATCH must update the task title")

	// Differentiation: bob and admin got different results with different inputs.
	assert.NotEqual(t, patchResult.Title, adminPatchResult.Title,
		"two different PATCH inputs must produce two different titles (not hardcoded)")

	// Bob DELETE → 204 (cross-owner delete now allowed — FR-1.9 gate removed).
	wDelBobRec := httptest.NewRecorder()
	rDelBob := httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+tsk.Id, nil)
	rDelBob.URL.Path = "/api/v1/tasks/" + tsk.Id
	rDelBob = rDelBob.WithContext(contextWithUser(rDelBob.Context(), "bob"))
	api.HandleTasks(wDelBobRec, rDelBob)
	assert.Equal(t, http.StatusNoContent, wDelBobRec.Code,
		"bob must get 204 on DELETE of alice's task (FR-1.9: no ownership gate); body=%s", wDelBobRec.Body.String())

	// Task must be gone after bob's successful DELETE.
	wGone := httptest.NewRecorder()
	rGone := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+tsk.Id, nil)
	rGone.URL.Path = "/api/v1/tasks/" + tsk.Id
	rGone = rGone.WithContext(contextWithUser(rGone.Context(), "alice"))
	api.HandleTasks(wGone, rGone)
	assert.Equal(t, http.StatusNotFound, wGone.Code,
		"task must be gone after bob's DELETE (204 was real, not a no-op); body=%s", wGone.Body.String())
}

// TestRegression_Task_EmptyOwner_AccessibleToAll verifies that a legacy task
// with an empty owner field is accessible to any authenticated user.
//
// BDD: Given a task on disk with owner="",
// When any authenticated user calls GET /api/v1/tasks/{id},
// Then 200 (unowned/shared resource — legacy compatibility).
//
// Traces to: feat/level1-project-task-mgmt — SEC-2 unowned resource rule
func TestRegression_Task_EmptyOwner_AccessibleToAll(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	// Write a task directly to disk with no owner field (simulates legacy data).
	legacyID := "test-legacy-task-empty-owner-001"
	tasksDir := filepath.Join(api.homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)
	legacyData := fmt.Sprintf(
		`{"id":%q,"title":"LegacyTask","action":"llm","status":"inbox","workspace_id":%q,"created_at":%q,"updated_at":%q}`,
		legacyID,
		wsID,
		now,
		now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, legacyID+".json"), []byte(legacyData), 0o600))

	// Any user must be able to read it (empty owner = unowned/shared).
	for _, user := range []string{"alice", "bob", "carol"} {
		t.Run("accessible_by_"+user, func(t *testing.T) {
			wGet := httptest.NewRecorder()
			rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+legacyID, nil)
			rGet.URL.Path = "/api/v1/tasks/" + legacyID
			rGet = rGet.WithContext(contextWithUser(rGet.Context(), user))
			api.HandleTasks(wGet, rGet)
			assert.Equal(t, http.StatusOK, wGet.Code,
				"user %q must be able to access a legacy task with empty owner; body=%s",
				user, wGet.Body.String())
		})
	}

	// Empty-owner task must appear in list for any user.
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rList.URL.Path = "/api/v1/tasks"
	rList = rList.WithContext(contextWithUser(rList.Context(), "random-user"))
	api.HandleTasks(wList, rList)
	require.Equal(t, http.StatusOK, wList.Code, "GET /tasks list must return 200; body=%s", wList.Body.String())
	var listItems []gen.Task
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &listItems))
	found := false
	for _, item := range listItems {
		if item.Id == legacyID {
			found = true
		}
	}
	assert.True(t, found, "legacy (empty-owner) task must appear in the list for any user")
}
