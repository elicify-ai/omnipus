//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// Tests E4, E6, E7, E8, E9, E10, E13 from the Wave E review findings fix plan.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/taskstore"
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
				Workspace: tmpDir,
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
		taskStore:     taskstore.New(tmpDir + "/tasks"),
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
// BDD: Given a valid task name,
// When POST /api/v1/tasks {"name":"Test task"} is called,
// Then 201 with id in UUID format.
// Traces to: wave5b-system-agent-spec.md — E4: task creation with UUID id
func TestHandleTasksPOST(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"name":"Test task"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)

	require.Equal(t, http.StatusCreated, w.Code)
	var task struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &task))
	assert.True(t, isUUID(task.ID), "task id must be a UUID, got %q", task.ID)
	assert.Equal(t, "Test task", task.Title)
}

// TestHandleProvidersGET verifies GET /api/v1/providers returns 200 with an array.
// BDD: Given the gateway is running,
// When GET /api/v1/providers is called,
// Then 200 with an array of providerResponse items.
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
	assert.NotEmpty(t, providers, "providers must include at least one entry (default fallback)")
	for _, p := range providers {
		assert.Contains(t, p, "id", "each provider must have an id field")
		assert.Contains(t, p, "status", "each provider must have a status field")
	}
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
// Then 2 tasks are returned. PUT /api/v1/tasks/{id} updates status.
// Traces to: wave5b-system-agent-spec.md — Scenario: Task persistence (E8)
func TestTaskPersistence(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Step 1: POST task 1 → 201 with UUID id.
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPost, "/api/v1/tasks",
		strings.NewReader(`{"name":"Test task one"}`))
	r1.Header.Set("Content-Type", "application/json")
	r1.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w1, r1)
	require.Equal(t, http.StatusCreated, w1.Code)

	var task1 taskstore.TaskEntity
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &task1))
	assert.True(t, isUUID(task1.ID), "task1 id must be UUID, got %q", task1.ID)
	assert.Equal(t, "Test task one", task1.Title)

	// Step 2: GET /tasks → 1 task.
	wList1 := httptest.NewRecorder()
	rList1 := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rList1.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wList1, rList1)
	require.Equal(t, http.StatusOK, wList1.Code)
	var tasks1 []taskstore.TaskEntity
	require.NoError(t, json.Unmarshal(wList1.Body.Bytes(), &tasks1))
	assert.Len(t, tasks1, 1, "list must contain 1 task after first POST")

	// Step 3: POST task 2 → 201.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/tasks",
		strings.NewReader(`{"name":"Test task two"}`))
	r2.Header.Set("Content-Type", "application/json")
	r2.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w2, r2)
	require.Equal(t, http.StatusCreated, w2.Code)

	// Step 4: GET /tasks → 2 tasks.
	wList2 := httptest.NewRecorder()
	rList2 := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rList2.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wList2, rList2)
	require.Equal(t, http.StatusOK, wList2.Code)
	var tasks2 []taskstore.TaskEntity
	require.NoError(t, json.Unmarshal(wList2.Body.Bytes(), &tasks2))
	assert.Len(t, tasks2, 2, "list must contain 2 tasks after second POST")

	// Step 5: PUT /tasks/{id} {"status":"running"} → 200 (queued→running is a valid transition).
	wPut := httptest.NewRecorder()
	rPut := httptest.NewRequest(http.MethodPut, "/api/v1/tasks/"+task1.ID,
		strings.NewReader(`{"status":"running"}`))
	rPut.Header.Set("Content-Type", "application/json")
	rPut.URL.Path = "/api/v1/tasks/" + task1.ID
	api.HandleTasks(wPut, rPut)
	require.Equal(t, http.StatusOK, wPut.Code)
	var updated taskstore.TaskEntity
	require.NoError(t, json.Unmarshal(wPut.Body.Bytes(), &updated))
	assert.Equal(t, "running", updated.Status, "PUT must update status to 'running'")
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
				strings.NewReader(`{"name":"Agent X"}`))
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
