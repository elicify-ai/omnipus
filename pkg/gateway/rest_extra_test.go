package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRestAPIWithHome creates a restAPI with homePath and onboardingMgr wired.
// This is used for tests that exercise tasks, state, onboarding, and config mutation endpoints.
// It writes a minimal config.json into the temp dir so safeUpdateConfigJSON can read and mutate it.
func newTestRestAPIWithHome(t *testing.T) *restAPI {
	t.Helper()
	return newTestRestAPIWithHomeDevModeBypass(t, false)
}

// newTestRestAPIWithHomeDevModeBypass is newTestRestAPIWithHome with
// gateway.dev_mode_bypass set explicitly, for tests pinning behavior that
// differs under bypass (e.g. TestHandleStateGET_DevModeBypass).
func newTestRestAPIWithHomeDevModeBypass(t *testing.T, devModeBypass bool) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: devModeBypass},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	// Write a minimal config.json so safeUpdateConfigJSON can read and atomically update it
	// without writing to the committed pkg/gateway/config.json fixture.
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		taskLock:      task.TaskFileLock,
	}
}

// isUUID returns true if s matches the UUID v4 format.
var isUUID = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
).MatchString

// TestHandleTasksGET verifies GET /api/v1/tasks returns 200 with an array.
// BDD: Given no tasks exist,
// When GET /api/v1/tasks is called,
// Then 200 with an empty array.
// Traces to: wave5b-system-agent-spec.md — E4: tasks endpoint
func TestHandleTasksGET(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	r.RequestURI = "/api/v1/tasks"
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var tasks []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.NotNil(t, tasks, "tasks list must be an array, not null")
}

// TestHandleTasksPOST verifies POST /api/v1/tasks returns 201 with a UUID id.
// BDD: Given a valid TaskCreateRequest (title + action + workspace_id),
// When POST /api/v1/tasks is called,
// Then 201 with id in UUID format and title matching the request.
// Traces to: wave5b-system-agent-spec.md — E4: task creation with UUID id
// Sprint 2: uses unified task model (title+action+workspace_id, not legacy name).
func TestHandleTasksPOST(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := createWorkspaceViaAPI(t, api, "E4TestWorkspace", "")

	w := httptest.NewRecorder()
	body := fmt.Sprintf(`{"title":"Test task","action":"llm","workspace_id":%q,`+minimalCriteriaDodJSON+`}`, wsID)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)

	require.Equal(t, http.StatusCreated, w.Code,
		"POST /api/v1/tasks must return 201; body=%s", w.Body.String())
	var created struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.True(t, isUUID(created.ID), "task id must be a UUID, got %q", created.ID)
	assert.Equal(t, "Test task", created.Title, "title must match the request")
}

// --- E7: Onboarding persistence test ---

// TestOnboardingPersistence verifies that PATCH /api/v1/state persists
// the onboarding_complete=true flag and GET reflects it.
// BDD: Given a fresh install with onboarding_complete=false,
// When PATCH /api/v1/state is called,
// Then GET /api/v1/state returns onboarding_complete=true.
// Traces to: wave5b-system-agent-spec.md — Scenario: Onboarding state persistence (E7)
func TestOnboardingPersistence(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Step 1: GET /state → onboarding_complete=false (fresh install).
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	api.HandleState(getW, getR)
	require.Equal(t, http.StatusOK, getW.Code)

	var initialState map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &initialState))
	assert.Equal(t, false, initialState["onboarding_complete"],
		"fresh install must have onboarding_complete=false")

	// Step 2: PATCH /state → 200.
	patchW := httptest.NewRecorder()
	patchR := httptest.NewRequest(http.MethodPatch, "/api/v1/state",
		strings.NewReader(`{"onboarding_complete":true}`))
	patchR.Header.Set("Content-Type", "application/json")
	api.HandleState(patchW, patchR)
	require.Equal(t, http.StatusOK, patchW.Code)

	// Step 3: GET /state → onboarding_complete=true (persisted).
	getW2 := httptest.NewRecorder()
	getR2 := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	api.HandleState(getW2, getR2)
	require.Equal(t, http.StatusOK, getW2.Code)

	var updatedState map[string]any
	require.NoError(t, json.Unmarshal(getW2.Body.Bytes(), &updatedState))
	assert.Equal(t, true, updatedState["onboarding_complete"],
		"after PATCH, GET must return onboarding_complete=true")
}

// --- E8: Task persistence test ---

// TestTaskPersistence verifies that tasks created via POST /api/v1/tasks
// are persisted and returned by GET /api/v1/tasks.
// BDD: Given an empty task store,
// When POST /api/v1/tasks is called twice and GET /api/v1/tasks is called,
// Then 2 tasks are returned. PATCH /api/v1/tasks/{id} updates status.
// Traces to: wave5b-system-agent-spec.md — Scenario: Task persistence (E8)
// Sprint 2: rewritten to use unified task model (gen.Task, PATCH for updates).
func TestTaskPersistence(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := createWorkspaceViaAPI(t, api, "E8PersistWorkspace", "")

	postTask := func(title string) gen.Task {
		t.Helper()
		// Include a prompt so the task is fully-captured: per Detail #8 a partial
		// task (no prompt/description) cannot be advanced to next (422). This test
		// exercises the PATCH→next persistence path, so the task must be complete.
		body := fmt.Sprintf(`{"title":%q,"prompt":"do the thing","action":"llm","workspace_id":%q,`+minimalCriteriaDodJSON+`}`, title, wsID)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)
		require.Equal(t, http.StatusCreated, w.Code, "POST /tasks must return 201; body=%s", w.Body.String())
		var tsk gen.Task
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))
		return tsk
	}

	// Step 1: POST task 1 → 201 with UUID id.
	task1 := postTask("Test task one")
	assert.True(t, isUUID(task1.Id), "task1 id must be UUID, got %q", task1.Id)
	assert.Equal(t, "Test task one", task1.Title, "title must match request")
	assert.Equal(t, gen.TaskStatus("inbox"), task1.Status, "default status must be inbox")

	// Step 2: GET /tasks → 1 task (persistence test: must be on disk).
	wList1 := httptest.NewRecorder()
	rList1 := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rList1.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wList1, rList1)
	require.Equal(t, http.StatusOK, wList1.Code)
	var tasks1 []gen.Task
	require.NoError(t, json.Unmarshal(wList1.Body.Bytes(), &tasks1))
	assert.Len(t, tasks1, 1, "list must contain 1 task after first POST")

	// Step 3: POST task 2 → 201.
	task2 := postTask("Test task two")
	assert.True(t, isUUID(task2.Id), "task2 id must be UUID")
	assert.NotEqual(t, task1.Id, task2.Id, "each task must get a unique ID")

	// Step 4: GET /tasks → 2 tasks (persistence: both must survive).
	wList2 := httptest.NewRecorder()
	rList2 := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rList2.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wList2, rList2)
	require.Equal(t, http.StatusOK, wList2.Code)
	var tasks2 []gen.Task
	require.NoError(t, json.Unmarshal(wList2.Body.Bytes(), &tasks2))
	assert.Len(t, tasks2, 2, "list must contain 2 tasks after second POST")

	// Step 5: PATCH /tasks/{id} {"status":"next"} → 200, status persists on read-back.
	// Sprint 2: PUT is replaced by PATCH; "running" → "next" (unified 7-state vocabulary).
	wPatch := httptest.NewRecorder()
	rPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+task1.Id,
		strings.NewReader(`{"status":"next"}`))
	rPatch.Header.Set("Content-Type", "application/json")
	rPatch.URL.Path = "/api/v1/tasks/" + task1.Id
	api.HandleTasks(wPatch, rPatch)
	require.Equal(t, http.StatusOK, wPatch.Code,
		"PATCH /tasks/{id} status=next must return 200; body=%s", wPatch.Body.String())
	var updated gen.Task
	require.NoError(t, json.Unmarshal(wPatch.Body.Bytes(), &updated))
	assert.Equal(t, gen.TaskStatus("next"), updated.Status,
		"PATCH must update status to 'next' (unified vocabulary)")
}

// --- Doctor action_link test (US-B4) ---

// TestDoctorIssuesHaveActionLinks verifies that every issue returned by
// runDiagnosticChecks carries non-empty action_link and action_label fields so
// the frontend DiagnosticsSection.tsx can render "fix this" links for each
// score deduction (US-B4 / #330).
//
// The test triggers the "sandbox-disabled" issue by using the default test
// config (sandbox mode = "off" in the test environment). At least one issue
// must carry both fields.
//
// BDD: Given the gateway runs without a sandbox configured,
// When POST /api/v1/doctor is called,
// Then each returned issue has a non-empty "action_link" and "action_label".
func TestDoctorIssuesHaveActionLinks(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/doctor", nil)
	api.HandleDoctor(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	rawIssues, ok := resp["issues"]
	require.True(t, ok, "response must contain 'issues'")
	issues, ok := rawIssues.([]any)
	require.True(t, ok, "issues must be an array")

	// Confirm at least one issue is present so the assertion below is not vacuously true.
	require.NotEmpty(t, issues, "at least one issue must be present (sandbox-disabled in test env)")

	for i, rawIssue := range issues {
		issue, ok := rawIssue.(map[string]any)
		require.True(t, ok, "issue[%d] must be a map", i)

		actionLink, hasLink := issue["action_link"].(string)
		assert.True(t, hasLink, "issue[%d] (id=%s) must have 'action_link'", i, issue["id"])
		assert.NotEmpty(t, actionLink, "issue[%d] action_link must not be empty", i)

		actionLabel, hasLabel := issue["action_label"].(string)
		assert.True(t, hasLabel, "issue[%d] (id=%s) must have 'action_label'", i, issue["id"])
		assert.NotEmpty(t, actionLabel, "issue[%d] action_label must not be empty", i)
	}
}

// TestDoctorGodModeArmed_HighSeverity proves runDiagnosticChecks (D6) flags an
// armed god-mode as a distinct, high-severity Security Health issue. Triggered
// on cfg.Sandbox.GodMode (covers both S1 armed-via-UI-pending-restart and S2
// live-active) — NOT on cfg.Sandbox.GodModeAllowed alone, which is S3
// (authorized in the past, currently disabled: genuinely inert) and must not
// false-positive.
//
// BDD: Given god-mode is armed (sandbox.god_mode=true),
// When the doctor's diagnostic checks run,
// Then a "god-mode-armed" issue is produced with severity "high" and a
// complete title/description/recommendation/action_link/action_label.
// Given only sandbox.god_mode_allowed=true (god-mode NOT currently armed),
// Then no "god-mode-armed" issue is produced.
func TestDoctorGodModeArmed_HighSeverity(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	cfg := api.agentLoop.GetConfig()

	// S3: authorized in the past, not currently armed — must NOT trigger
	// (this is the false-positive the RCA explicitly calls out).
	cfg.Sandbox.GodModeAllowed = true
	cfg.Sandbox.GodMode = false
	issuesInert := api.runDiagnosticChecks(cfg)
	for _, iss := range issuesInert {
		assert.NotEqual(t, "god-mode-armed", iss["id"],
			"GodModeAllowed alone (S3, genuinely inert) must not trigger the god-mode-armed check")
	}

	// S1/S2: armed (whether pending restart or already live) — MUST trigger.
	cfg.Sandbox.GodMode = true
	issuesArmed := api.runDiagnosticChecks(cfg)
	var found map[string]any
	for _, iss := range issuesArmed {
		if iss["id"] == "god-mode-armed" {
			found = iss
			break
		}
	}
	require.NotNil(t, found, "armed god-mode (sandbox.god_mode=true) must produce a god-mode-armed issue")
	assert.Equal(t, "high", found["severity"],
		"god-mode-armed must be high severity — strictly worse than the medium sandbox-disabled check, "+
			"since it disables the sandbox's filesystem confinement AND opens egress simultaneously, "+
			"floored at allow for every tool (it does not disable the shell's outside-workspace write refusal)")
	assert.NotEmpty(t, found["title"])
	assert.NotEmpty(t, found["description"])
	assert.NotEmpty(t, found["recommendation"])
	assert.NotEmpty(t, found["action_link"])
	assert.NotEmpty(t, found["action_label"])
}
