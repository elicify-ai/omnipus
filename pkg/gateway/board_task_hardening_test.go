// board_task_hardening_test.go — task hardening tests (Sprint 2).
//
// Sprint 2 removals (design decisions — not regressions):
//   - /start endpoint DELETED: C1 (concurrent /start race), C2 (real-path /start→completion),
//     M3 (panic recovery via /start), TestBoardTaskPost_RejectsUnknownAgentID (/board/tasks),
//     TestRegression_CompletionCallback_SetsResult_RealPath (/start) — ALL DELETED.
//   - validateStatusTransition DELETED: H3 test DELETED.
//   - listBoardTasks / readBoardTask / writeBoardTask / reconcileStuckBoardTasks DELETED.
//   - MIG (workflow-vs-GTD discriminator) DELETED — unified store, one entity type.
//
// Tests that remain:
//   - newTestRestAPIWithAgent — KEPT (shared helper)
//   - REC — reconcileStuckTasks (Sprint 2 successor to reconcileStuckBoardTasks)
//   - REC2 — reconcileOrphanBlockedByEdges
//
// Traces to: project-task-management-level1-spec.md — H1, REC.

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

// ── H1: WaitForActiveRequests drain helpers ───────────────────────────────────

// newTestRestAPIWithAgent builds a restAPI with one enabled default agent
// (01JXTESTAGENTSTARTTEST001). It is used by tests that exercise the real
// agent-loop path. The test task store uses the unified pkg/task store.
func newTestRestAPIWithAgent(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	agentWorkspace := filepath.Join(tmpDir, "agents", "01JXTESTAGENTSTARTTEST001")
	require.NoError(t, os.MkdirAll(agentWorkspace, 0o700))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{
					ID:      "01JXTESTAGENTSTARTTEST001",
					Name:    "Test Agent",
					Default: true,
					Type:    config.AgentTypeCustom,
					Home:    agentWorkspace,
				},
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		taskLock:      task.TaskFileLock,
	}
	t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })
	return api
}

// ── REC: reconcileStuckTasks ─────────────────────────────────────────────────

// TestReconcileStuckTasks verifies the boot reconciler resets stuck tasks.
//
// Sprint 2: reconcileStuckBoardTasks is renamed to reconcileStuckTasks and
// targets the unified 7-state vocabulary. "active" no longer exists; the
// stuck status is "in_progress".
//
// BDD:
//
//	Given a task file with status="in_progress" in tasks/,
//	When reconcileStuckTasks() is called,
//	Then the task status is "failed" with a non-empty result.
//	Given a task file with status="done",
//	Then the task status is still "done" (untouched).
//	Given an empty tasks directory,
//	Then reconcileStuckTasks() is a no-op (no error).
//
// Traces to: project-task-management-level1-spec.md — REC (boot reconcile)
func TestReconcileStuckTasks(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	tasksDir := filepath.Join(api.homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)

	// Write an in_progress (stuck) task directly to disk.
	const inProgID = "test-reconcile-in-prog-001"
	inProgData := fmt.Sprintf(
		`{"id":%q,"title":"StuckInProgressTask","action":"llm","status":"in_progress","workspace_id":%q,"session_id":"orphaned","created_at":%q,"updated_at":%q}`,
		inProgID,
		wsID,
		now,
		now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, inProgID+".json"), []byte(inProgData), 0o600))

	// Write a done task (must not be modified).
	const doneID = "test-reconcile-done-001"
	doneData := fmt.Sprintf(
		`{"id":%q,"title":"DoneTask","action":"llm","status":"done","workspace_id":%q,"result":"all tests passed","created_at":%q,"updated_at":%q}`,
		doneID,
		wsID,
		now,
		now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, doneID+".json"), []byte(doneData), 0o600))

	// Run the reconciler.
	api.reconcileStuckTasks()

	// In-progress task must now be failed with a non-empty diagnostic result.
	gotInProg, err := api.taskStore.Get(inProgID)
	require.NoError(t, err, "taskStore.Get (in_progress→failed) must not error")
	assert.Equal(t, task.StatusFailed, gotInProg.Status,
		"reconcile must reset in_progress task to failed")
	assert.NotEmpty(t, gotInProg.Result,
		"reconciled in_progress task must have a non-empty diagnostic result")

	// Done task must be unchanged.
	gotDone, err := api.taskStore.Get(doneID)
	require.NoError(t, err, "taskStore.Get (done) must not error")
	assert.Equal(t, task.StatusDone, gotDone.Status,
		"reconcile must leave done tasks untouched")
	assert.Equal(t, "all tests passed", gotDone.Result,
		"done task result must not be modified by reconcile")

	// Differentiation: the two tasks end in different statuses (not both set to failed).
	assert.NotEqual(t, gotInProg.Status, gotDone.Status,
		"in_progress and done tasks must produce different outcomes (not both set to failed)")

	// Empty-directory no-op: a fresh api with no tasks dir must not error.
	apiEmpty := newTestRestAPIWithHome(t)
	apiEmpty.reconcileStuckTasks() // must not panic or error

	emptyTasksDir := filepath.Join(apiEmpty.homePath, "tasks")
	entries, readDirErr := os.ReadDir(emptyTasksDir)
	if readDirErr != nil && !os.IsNotExist(readDirErr) {
		require.NoError(t, readDirErr)
	}
	assert.Empty(t, entries, "empty tasks dir must remain empty after reconcile no-op")
}

// ── REC2: reconcileOrphanBlockedByEdges ──────────────────────────────────────

// TestReconcileOrphanBlockedByEdges verifies that the orphan-edge reconciler
// removes blocked_by references that point to non-existent tasks.
//
// BDD:
//
//	Given task B has blocked_by=[A, C] where A exists and C does not,
//	When reconcileOrphanBlockedByEdges() is called,
//	Then B.blocked_by is [A] (C removed as orphan).
//	Given task D has blocked_by=[E] where E does not exist,
//	When reconcileOrphanBlockedByEdges() is called,
//	Then D.blocked_by is [] (all edges removed, status resets to next).
//
// Traces to: project-task-management-level1-spec.md — REC (orphan edge cleanup)
func TestReconcileOrphanBlockedByEdges(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	tasksDir := filepath.Join(api.homePath, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)

	// Write task A (exists — the valid dependency).
	const aID = "test-orphan-a-001"
	aData := fmt.Sprintf(
		`{"id":%q,"title":"TaskA","action":"llm","status":"next","workspace_id":%q,"created_at":%q,"updated_at":%q}`,
		aID, wsID, now, now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, aID+".json"), []byte(aData), 0o600))

	// Write task B with blocked_by=[A, orphanC].
	const bID = "test-orphan-b-001"
	const orphanCID = "test-orphan-c-nonexistent"
	bData := fmt.Sprintf(
		`{"id":%q,"title":"TaskB","action":"llm","status":"blocked","workspace_id":%q,"blocked_by":[%q,%q],"created_at":%q,"updated_at":%q}`,
		bID,
		wsID,
		aID,
		orphanCID,
		now,
		now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, bID+".json"), []byte(bData), 0o600))

	// Run the orphan-edge reconciler.
	api.reconcileOrphanBlockedByEdges()

	// B's blocked_by must have C removed (A still valid).
	gotB, err := api.taskStore.Get(bID)
	require.NoError(t, err, "taskStore.Get(B) must not error after orphan reconcile")
	require.NotNil(t, gotB.BlockedBy, "blocked_by must not be nil after partial cleanup")
	// A must still be there; C must be gone.
	foundA, foundC := false, false
	for _, depID := range gotB.BlockedBy {
		if depID == aID {
			foundA = true
		}
		if depID == orphanCID {
			foundC = true
		}
	}
	assert.True(t, foundA, "valid dependency A must remain in blocked_by after orphan reconcile")
	assert.False(t, foundC, "orphan dependency C must be removed by orphan reconcile")
}

// ── H2: agent_id existence validation on the unified /tasks surface ──────────

// TestTask_Post_RejectsUnknownAgentID verifies the A2 validation logic
// on the unified /api/v1/tasks surface.
//
// BDD:
//
//	Given a registry with exactly one agent (01JXTESTAGENTSTARTTEST001),
//	When POST /api/v1/tasks with agent_id="01JXUNKNOWNAGENT0000001X",
//	Then 400 with the unknown ID in the body.
//	When POST /api/v1/tasks with agent_id=<registered id>,
//	Then 201.
//	When PATCH /api/v1/tasks/{id} with unknown agent_id,
//	Then 400.
//
// Previously BLOCKED (A2): validateTaskAgentID is now implemented in
// pkg/gateway/rest_tasks.go and wired into both handleTaskCreate and
// handleTaskPatch. When the registry is non-empty it rejects unknown agent IDs
// with 400; when the registry is empty it skips the check (skip-on-empty-registry
// is documented in validateTaskAgentID's docstring).
//
// Traces to: project-task-management-level1-spec.md — A2 (agent_id existence)
func TestTask_Post_RejectsUnknownAgentID(t *testing.T) {
	const registeredID = "01JXTESTAGENTSTARTTEST001"
	const unknownID = "01JXUNKNOWNAGENT0000001X"

	// Subtest 1: populated registry rejects an unknown agent_id on POST → 400.
	// Differentiation: the known-ID subtest (below) returns 201, proving the guard
	// is not returning 400 for everything.
	t.Run("populated registry rejects unknown agent_id on POST", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		wsID := ensureTestWorkspace(t, api)

		body := fmt.Sprintf(
			`{"title":"UnknownAgentTest","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":%q}`,
			wsID, unknownID,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)

		require.Equal(t, http.StatusBadRequest, w.Code,
			"POST with unknown agent_id must return 400; body=%s", w.Body.String())
		assert.Contains(t, w.Body.String(), unknownID,
			"400 body must reference the unknown agent ID so the caller can diagnose the problem")
	})

	// Subtest 2 (differentiation): the SAME registry accepts the registered agent_id → 201.
	// Two different inputs → two different status codes proves the guard is not hardcoded.
	t.Run("populated registry accepts registered agent_id on POST", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		wsID := ensureTestWorkspace(t, api)
		// registeredID is a custom agent, not one of defaultWorkspaceTeam's fixed
		// base-roster candidates (mia/jim/ava/ray/planner/explorer/researcher), so
		// it must be put on this workspace's team explicitly for the
		// team-membership half of validateTaskAgentID to accept it.
		setWorkspaceCoreTeam(t, api, wsID, []string{registeredID})

		body := fmt.Sprintf(
			`{"title":"AgentAcceptTest","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":%q}`,
			wsID, registeredID,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)

		require.Equal(t, http.StatusCreated, w.Code,
			"POST with registered agent_id must return 201; body=%s", w.Body.String())
	})

	// Subtest 3: empty agent_id bypasses the A2 check (documented skip).
	t.Run("empty agent_id skips A2 validation", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		wsID := ensureTestWorkspace(t, api)

		body := fmt.Sprintf(`{"title":"NoAgentIDTask","action":"llm","workspace_id":%q,"prompt":"do it"}`, wsID)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)

		require.Equal(t, http.StatusCreated, w.Code,
			"POST without agent_id must return 201 (A2 skip: empty agent_id is valid); body=%s", w.Body.String())
	})

	// Subtest 4: PATCH path also rejects an unknown agent_id → 400.
	// Creates a task with the known agent, then PATCHes it to an unknown agent_id.
	t.Run("populated registry rejects unknown agent_id on PATCH", func(t *testing.T) {
		api := newTestRestAPIWithAgent(t)
		wsID := ensureTestWorkspace(t, api)

		// Create a valid task first.
		created := createTaskViaAPI(t, api, "PatchAgentTest", wsID)

		// PATCH: attempt to reassign to an unknown agent_id.
		wPatch := patchTask(t, api, created.Id,
			fmt.Sprintf(`{"agent_id":%q}`, unknownID),
		)
		require.Equal(t, http.StatusBadRequest, wPatch.Code,
			"PATCH with unknown agent_id must return 400; body=%s", wPatch.Body.String())
		assert.Contains(t, wPatch.Body.String(), unknownID,
			"400 body must reference the unknown agent ID")

		// Content check: the task's agent_id must not have changed to the unknown value.
		wGet := httptest.NewRecorder()
		rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+created.Id, nil)
		rGet.URL.Path = "/api/v1/tasks/" + created.Id
		api.HandleTasks(wGet, rGet)
		require.Equal(t, http.StatusOK, wGet.Code)
		var got gen.Task
		require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
		agentID := ""
		if got.AgentId != nil {
			agentID = *got.AgentId
		}
		assert.NotEqual(t, unknownID, agentID,
			"task must not have the unknown agent_id after a rejected PATCH")
	})
}
