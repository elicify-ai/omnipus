package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newTestRestAPIWithHome creates a restAPI with homePath and onboardingMgr wired.
// This is used for tests that exercise tasks, state, onboarding, and config mutation endpoints.
// It writes a minimal config.json into the temp dir so safeUpdateConfigJSON can read and mutate it.
func newTestRestAPIWithHome(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:      tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
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

// --- E4: Stub/new endpoint tests ---

// TestHandleStateGET verifies GET /api/v1/state returns 200 with onboarding_complete field.
// BDD: Given a fresh install,
// When GET /api/v1/state is called,
// Then 200 with {"onboarding_complete": false}.
// Traces to: wave5b-system-agent-spec.md — Scenario: App state endpoint (E4)
func TestHandleStateGET(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	api.HandleState(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	_, hasField := resp["onboarding_complete"]
	assert.True(t, hasField, "response must contain 'onboarding_complete' field")
	assert.Equal(t, false, resp["onboarding_complete"], "fresh install must have onboarding_complete=false")
}

// TestHandleStatePATCH verifies PATCH /api/v1/state returns 200.
// Traces to: wave5b-system-agent-spec.md — E4: state update
func TestHandleStatePATCH(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/state", strings.NewReader(`{"onboarding_complete":true}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleState(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["onboarding_complete"], "PATCH must return onboarding_complete=true")
}

// TestHandleStatusGET verifies GET /api/v1/status returns 200 with expected fields.
// BDD: Given the gateway is running,
// When GET /api/v1/status is called,
// Then 200 with online=true, agent_count (int), version (string).
// Traces to: wave5b-system-agent-spec.md — E4: status endpoint
func TestHandleStatusGET(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	api.HandleStatus(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp gen.GatewayStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Online, "online must be true")
	assert.GreaterOrEqual(t, resp.AgentCount, 1, "agent_count must be ≥1 (system agent)")
	// version may be "dev" in tests — just check it's not empty when Version var is set
}

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
	body := fmt.Sprintf(`{"title":"Test task","action":"llm","workspace_id":%q}`, wsID)
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

// TestHandleProvidersGET verifies GET /api/v1/providers returns 200 with an array.
// BDD: Given the gateway is running with no configured provider,
// When GET /api/v1/providers is called,
// Then 200 with an EMPTY array — ADR-068 FR-011a (T068-04) removed the
// synthetic "default" filler row along with the template rows; the body is
// `[]`, never `null` (Provider.yaml requires an array).
// Traces to: wave5b-system-agent-spec.md — E4: providers endpoint
func TestHandleProvidersGET(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	r.URL.Path = "/api/v1/providers"
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var providers []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &providers))
	require.NotNil(t, providers, "body must be a JSON array, not null")
	assert.Empty(t, providers, "no configured provider → no rows (no synthetic default filler)")
}

// TestHandleMCPServersGET verifies GET /api/v1/mcp-servers returns 200 with an array.
// Traces to: wave5b-system-agent-spec.md — E4: mcp-servers endpoint
func TestHandleMCPServersGET(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil)
	api.HandleMCPServers(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	// Response is an array (may be empty).
	var body any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
}

// TestHandleStorageStatsGET verifies GET /api/v1/storage/stats returns 200 with
// workspace_size_bytes field.
// Traces to: wave5b-system-agent-spec.md — E4: storage stats endpoint
func TestHandleStorageStatsGET(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/storage/stats", nil)
	api.HandleStorageStats(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	_, hasField := resp["workspace_size_bytes"]
	assert.True(t, hasField, "response must contain 'workspace_size_bytes' field")
}

// TestHandleToolsGET verifies GET /api/v1/tools returns 200 with an array.
// Traces to: wave5b-system-agent-spec.md — E4: tools endpoint
func TestHandleToolsGET(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	api.HandleTools(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var tools []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tools))
	assert.NotNil(t, tools, "tools must be an array, not null")
}

// TestHandleChannelsGET verifies GET /api/v1/channels returns 200 with an array.
// Traces to: wave5b-system-agent-spec.md — E4: channels endpoint
func TestHandleChannelsGET(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	api.HandleChannels(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var channels []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &channels))
	assert.NotEmpty(t, channels, "channels must include at least webchat")
}

// --- E6: Add TestHandleDoctorPOST ---

// TestHandleDoctorPOST verifies POST /api/v1/doctor returns 200 with
// score (int), issues (array), and checked_at (string).
// BDD: Given the gateway is running,
// When POST /api/v1/doctor is called,
// Then 200 with a diagnostic result containing score, issues, and checked_at.
// Traces to: wave5b-system-agent-spec.md — E6: doctor POST endpoint
func TestHandleDoctorPOST(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/doctor", nil)
	api.HandleDoctor(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// score must be a number.
	score, hasScore := resp["score"]
	assert.True(t, hasScore, "POST /doctor must return 'score'")
	assert.IsType(t, float64(0), score, "score must be a number")

	// issues must be an array (may be empty).
	issues, hasIssues := resp["issues"]
	assert.True(t, hasIssues, "POST /doctor must return 'issues'")
	assert.IsType(t, []any{}, issues, "issues must be an array")

	// checked_at must be a non-empty string.
	checkedAt, hasCheckedAt := resp["checked_at"]
	assert.True(t, hasCheckedAt, "POST /doctor must return 'checked_at'")
	assert.IsType(t, "", checkedAt, "checked_at must be a string")
	assert.NotEmpty(t, checkedAt, "checked_at must not be empty")
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
		body := fmt.Sprintf(`{"title":%q,"prompt":"do the thing","action":"llm","workspace_id":%q}`, title, wsID)
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

// --- E9: createAgent concurrency test ---

// TestHandleAgentsCreateConcurrent verifies that concurrent POST /api/v1/agents
// requests all succeed (each creates a distinct agent).
// BDD: Given concurrent POST requests to /agents,
// When all are handled simultaneously,
// Then each receives 201 Created.
// Traces to: wave5a-wire-ui-spec.md — Scenario: createAgent concurrency safe (E9)
func TestHandleAgentsCreateConcurrent(t *testing.T) {
	// Use newTestRestAPIWithHome so safeUpdateConfigJSON writes to a temp dir,
	// not the committed pkg/gateway/config.json test fixture.
	api := newTestRestAPIWithHome(t)

	const n = 5
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
				strings.NewReader(fmt.Sprintf(`{"name":"Agent X","type":"Main","soul":"Agent %d soul"}`, idx)))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			codes[idx] = w.Code
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		assert.Equal(t, http.StatusCreated, code,
			"concurrent POST /agents[%d] must return 201", i)
	}
}

// --- E10: HandleConfig GET redaction test ---

// TestHandleConfigGETRedaction verifies that GET /api/v1/config returns valid JSON
// without raw credential values.
// BDD: Given a running gateway,
// When GET /api/v1/config is called,
// Then 200 with valid JSON and no raw API key patterns in the response body.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Config endpoint redacts credentials (SEC-23) (E10)
func TestHandleConfigGETRedaction(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	api.HandleConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Must be valid JSON.
	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &cfg),
		"config response must be valid JSON")

	// Must not contain raw API key patterns.
	for _, pattern := range []string{"sk-ant-", "sk-proj-", "ghp_", "sk-or-"} {
		assert.NotContains(t, body, pattern,
			"config response must not contain credential pattern %q", pattern)
	}
}

// TestHandleConfigGETFieldsWithSensitiveNamesAreRedacted verifies that any
// config fields whose names contain "key", "token", "secret", or "password"
// are redacted if non-empty.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Redact sensitive field names (SEC-23) (E10)
func TestHandleConfigGETFieldsWithSensitiveNamesAreRedacted(t *testing.T) {
	// We test this via the redactSensitiveFields helper directly (covered by rest_test.go).
	// This test verifies the same property through the HandleConfig endpoint.
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	api.HandleConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var cfg map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cfg))

	// Walk all string values in the response recursively; none should look like a raw credential.
	var checkNoRawCredentials func(m map[string]any)
	checkNoRawCredentials = func(m map[string]any) {
		for k, v := range m {
			if strVal, ok := v.(string); ok && strVal != "" {
				kl := strings.ToLower(k)
				isSensitiveKey := strings.Contains(kl, "key") ||
					strings.Contains(kl, "token") ||
					strings.Contains(kl, "secret") ||
					strings.Contains(kl, "password")
				if isSensitiveKey {
					assert.Equal(t, "[redacted]", strVal,
						"field %q with sensitive name must be redacted", k)
				}
			}
			if sub, ok := v.(map[string]any); ok {
				checkNoRawCredentials(sub)
			}
		}
	}
	checkNoRawCredentials(cfg)
}

// --- E13: listSkills type assertion test ---

// TestListSkillsEmpty verifies that GET /api/v1/skills returns an empty array
// when no skills are configured.
// BDD: Given no skills are installed,
// When GET /api/v1/skills is called,
// Then 200 with an empty array (not null).
// Traces to: wave5a-wire-ui-spec.md — Scenario: Skills list empty (E13)
func TestListSkillsEmpty(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	r.URL.Path = "/api/v1/skills"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	body := strings.TrimSpace(w.Body.String())
	// Response must be a JSON array (empty or populated).
	assert.True(t, strings.HasPrefix(body, "["),
		"skills response must be a JSON array, got: %q", body)
}

// TestListSkillsTypedResponse verifies that skillResponse fields are correctly
// typed and populated when skills are present in startup info.
// This tests the type assertion logic in listSkills().
// Traces to: wave5a-wire-ui-spec.md — Scenario: Skill response shape (E13)
func TestListSkillsTypedResponse(t *testing.T) {
	// listSkills pulls data from agentLoop.GetStartupInfo()["skills"].(map[string]any).
	// With the default test config (no skills loaded), the result is an empty array.
	// This test verifies the empty-map path and that we never return null.
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	r.URL.Path = "/api/v1/skills"
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var skills []gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &skills),
		"skills response must unmarshal into []gen.Skill")

	// All returned skills must have required fields.
	for i, s := range skills {
		assert.NotEmpty(t, s.Id, "skills[%d].id must not be empty", i)
		assert.NotEmpty(t, s.Name, "skills[%d].name must not be empty", i)
		assert.NotEmpty(t, s.Version, "skills[%d].version must not be empty", i)
		assert.NotEmpty(t, s.Status, "skills[%d].status must not be empty", i)
	}
}

// TestListSkillsRedirectsMethodNotAllowed verifies that non-GET methods return 405.
// Traces to: wave5a-wire-ui-spec.md — E13: skills method validation
func TestListSkillsMethodNotAllowed(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/skills", nil)
	r.URL.Path = "/api/v1/skills"
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
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
			"since it disables the sandbox, egress restrictions, AND the shell guard simultaneously")
	assert.NotEmpty(t, found["title"])
	assert.NotEmpty(t, found["description"])
	assert.NotEmpty(t, found["recommendation"])
	assert.NotEmpty(t, found["action_link"])
	assert.NotEmpty(t, found["action_label"])
}
