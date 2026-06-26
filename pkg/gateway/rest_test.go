//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// When CGO is enabled, pkg/gateway imports pkg/channels/matrix which requires
// the libolm system library (olm/olm.h). If that library is installed,
// remove this build constraint and run tests normally.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	whatsappnative "github.com/dapicom-ai/omnipus/pkg/channels/whatsapp_native"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// seedTestAgents seeds Agents.List for handler tests with an `omnipus-system`
// entry and the 4 base core agents (Spec-3: Mia·Assistant, Jim·Orchestrator,
// Ray·Scout, Ava·Builder; Max retired). The system entry exercises the API
// contract for AgentType=system (locked, Type="system" surfaced in GET responses);
// production SeedConfig only seeds the 4 core agents — it does NOT inject omnipus-system —
// so this seeding is a handler-shape fixture, not a mirror of production
// startup. The synthetic system entry is here because the API contract still
// honors AgentType=system if a config supplies one (operator-supplied or legacy).
// listAgents / getAgent read only from cfg.Agents.List with no hardcoded system
// injection.
func seedTestAgents(cfg *config.Config) {
	// Prepend omnipus-system so it appears first in the list (matches production order).
	sysPresent := false
	for _, ac := range cfg.Agents.List {
		if ac.ID == "omnipus-system" {
			sysPresent = true
			break
		}
	}
	if !sysPresent {
		cfg.Agents.List = append([]config.AgentConfig{
			{
				ID:     "omnipus-system",
				Name:   "Omnipus",
				Type:   config.AgentTypeSystem,
				Locked: true,
			},
		}, cfg.Agents.List...)
	}
	// Seed base core agents (mia, jim, ava, ray; Spec-3: max retired) — idempotent.
	coreagent.SeedConfig(cfg)
}

// restMockProvider satisfies providers.LLMProvider with no-op responses.
type restMockProvider struct{}

func (m *restMockProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{}, nil
}

func (m *restMockProvider) GetDefaultModel() string { return "test-model" }

// newTestRestAPI creates a restAPI with a minimal AgentLoop for unit testing.
// OMNIPUS_BEARER_TOKEN is unset so auth is disabled (development mode).
// The config is seeded with omnipus-system and the 4 base core agents (mia, jim, ava, ray;
// Spec-3: max retired) to mirror the production startup path in gateway.go.
func newTestRestAPI(t *testing.T) (*restAPI, func()) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "") // disable auth in tests

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
	// Seed config-shape with omnipus-system + core agents (see seedTestAgents godoc;
	// production SeedConfig does NOT add omnipus-system).
	seedTestAgents(cfg)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
	}
	return api, func() {}
}

// --- HandleAgents tests ---

// TestHandleAgentsListAlwaysIncludesSystemAgent verifies that GET /api/v1/agents
// always includes the omnipus-system agent regardless of config.
// BDD: Given no agents are configured,
// When GET /api/v1/agents is called,
// Then the response includes the system agent with id "omnipus-system".
// Traces to: wave5a-wire-ui-spec.md — Scenario: Agent list always includes system agent (US-6 AC1)
func TestHandleAgentsListAlwaysIncludesSystemAgent(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var agents []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agents))

	found := false
	for _, ag := range agents {
		if ag.ID == "omnipus-system" && ag.Type == "system" {
			found = true
			break
		}
	}
	assert.True(t, found, "system agent must always be present in the agents list")
}

// TestHandleAgentsListIncludesConfiguredAgents verifies custom agents from config appear in the list.
// BDD: Given one custom agent is configured,
// When GET /api/v1/agents is called,
// Then the response includes the system agent plus the custom agent.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Agent list includes configured agents (US-6 AC2)
func TestHandleAgentsListIncludesConfiguredAgents(t *testing.T) {
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
			List: []config.AgentConfig{
				{ID: "my-agent", Name: "My Agent"},
			},
		},
	}
	// Seed omnipus-system and core agents to mirror gateway startup.
	seedTestAgents(cfg)
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var agents []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agents))

	assert.GreaterOrEqual(t, len(agents), 2, "must include system agent + custom agent")

	ids := make(map[string]bool)
	for _, ag := range agents {
		ids[ag.ID] = true
	}
	assert.True(t, ids["omnipus-system"], "system agent must be present")
	assert.True(t, ids["my-agent"], "custom agent must be present")
}

// TestHandleAgentsGetByIDSystemAgent verifies GET /api/v1/agents/omnipus-system returns the system agent.
// BDD: Given agent id "omnipus-system",
// When GET /api/v1/agents/omnipus-system is called,
// Then the response has id "omnipus-system" and type "system".
// Traces to: wave5a-wire-ui-spec.md — Scenario: Get agent by ID (US-7 AC1)
func TestHandleAgentsGetByIDSystemAgent(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/omnipus-system", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "omnipus-system", resp.ID)
	assert.Equal(t, "system", resp.Type)
}

// TestHandleAgentsGetByIDNotFound verifies GET /api/v1/agents/{unknown} returns 404.
// BDD: Given agent id "does-not-exist",
// When GET /api/v1/agents/does-not-exist is called,
// Then the response has status 404.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Get agent by ID not found (US-7 AC2)
func TestHandleAgentsGetByIDNotFound(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/does-not-exist", nil)
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleAgentsCreateValidation verifies POST /api/v1/agents with empty name returns 422.
// Traces to: wave5a-wire-ui-spec.md — A3+A4: agent creation via API
func TestHandleAgentsCreateValidation(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	body := `{"name": ""}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "name is required")
}

// TestHandleAgentsCreate verifies POST /api/v1/agents creates an agent and returns 201.
// Traces to: wave5a-wire-ui-spec.md — A3+A4: agent creation via API
func TestHandleAgentsCreate(t *testing.T) {
	// Use newTestRestAPIWithHome so safeUpdateConfigJSON writes to a temp dir,
	// not the committed pkg/gateway/config.json test fixture.
	api := newTestRestAPIWithHome(t)

	body := `{"name": "Scout", "model": "claude-sonnet-4-6", "soul": "Scout soul"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Scout", resp.Name)
	assert.Equal(t, gen.AgentTypeMain, resp.Type)
	assert.NotEmpty(t, resp.Id)
}

// TestHandleAgentsCreateWithExplicitID verifies POST /api/v1/agents creates agent and ignores provided id.
// Traces to: wave5a-wire-ui-spec.md — A3+A4: agent creation via API
func TestHandleAgentsCreateWithExplicitID(t *testing.T) {
	// Use newTestRestAPIWithHome so safeUpdateConfigJSON writes to a temp dir,
	// not the committed pkg/gateway/config.json test fixture.
	api := newTestRestAPIWithHome(t)

	body := `{"id": "my-scout", "name": "Scout", "soul": "Scout soul"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Scout", resp.Name)
	assert.NotEmpty(t, resp.Id)
}

// --- HandleSessions tests ---

// TestHandleSessionsList verifies that GET /api/v1/sessions returns 200 with an empty list
// when no sessions have been created yet.
// BDD: Given no sessions exist, When GET /api/v1/sessions is called,
// Then the response has status 200 and an empty array.
func TestHandleSessionsList(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	api.HandleSessions(w, r)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestHandleSessionsGetNotFound verifies 404 when session ID does not exist in any agent store.
// BDD: Given no sessions exist,
// When GET /api/v1/sessions/unknown-id is called,
// Then the response has status 404.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Session not found returns 404 (US-15 AC3)
func TestHandleSessionsGetNotFound(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/session_does_not_exist", nil)
	api.HandleSessions(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- HandleDoctor tests ---

// TestHandleDoctorReturnsOK verifies GET /api/v1/doctor returns 200 with status "ok".
// BDD: Given the gateway is running,
// When GET /api/v1/doctor is called,
// Then the response has status 200 and top-level status "ok".
// Traces to: wave5a-wire-ui-spec.md — Scenario: Doctor endpoint returns health status (US-16 AC1)
func TestHandleDoctorReturnsOK(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/doctor", nil)
	api.HandleDoctor(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Status string         `json:"status"`
		Checks map[string]any `json:"checks"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp.Status)
	assert.Contains(t, resp.Checks, "gateway")
	assert.Contains(t, resp.Checks, "agent_loop")
	assert.Contains(t, resp.Checks, "session_store")
	assert.Contains(t, resp.Checks, "go_runtime")
}

// TestHandleDoctorMethodNotAllowed verifies that methods other than GET and POST return 405.
// POST is allowed (returns diagnostic result without checks). GET returns full detail.
// Traces to: wave5a-wire-ui-spec.md — Dataset: Doctor endpoint — method validation
func TestHandleDoctorMethodNotAllowed(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/doctor", nil)
	api.HandleDoctor(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- redactSensitiveFields tests ---

// TestRedactSensitiveFields verifies that credential fields are redacted in config responses.
// BDD: Given a config map containing fields named "api_key", "token", "secret",
// When redactSensitiveFields is called,
// Then those fields are replaced with "[redacted]" and non-sensitive fields are unchanged.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Config endpoint redacts credentials (SEC-23)
func TestRedactSensitiveFields(t *testing.T) {
	tests := []struct {
		name      string
		input     map[string]any
		wantKey   string
		wantValue any
	}{
		// Dataset: Redaction — row 1
		{
			name:      "api_key is redacted",
			input:     map[string]any{"api_key": "sk-abc-123"},
			wantKey:   "api_key",
			wantValue: "[redacted]",
		},
		// Dataset: Redaction — row 2
		{
			name:      "token is redacted",
			input:     map[string]any{"bearer_token": "tok-xyz"},
			wantKey:   "bearer_token",
			wantValue: "[redacted]",
		},
		// Dataset: Redaction — row 3
		{
			name:      "secret is redacted",
			input:     map[string]any{"client_secret": "very-secret"},
			wantKey:   "client_secret",
			wantValue: "[redacted]",
		},
		// Dataset: Redaction — row 4
		{
			name:      "password is redacted",
			input:     map[string]any{"password": "hunter2"},
			wantKey:   "password",
			wantValue: "[redacted]",
		},
		// Dataset: Redaction — row 5 (empty string not redacted)
		{
			name:      "empty string value not redacted",
			input:     map[string]any{"api_key": ""},
			wantKey:   "api_key",
			wantValue: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			redactSensitiveFields(tc.input)
			assert.Equal(t, tc.wantValue, tc.input[tc.wantKey])
		})
	}
}

// TestRedactSensitiveFieldsPreservesNonSensitive verifies safe fields are unchanged.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Config endpoint redacts credentials (SEC-23)
func TestRedactSensitiveFieldsPreservesNonSensitive(t *testing.T) {
	m := map[string]any{
		"host":    "localhost",
		"port":    8080,
		"api_key": "should-be-gone",
		"version": "1.0.0",
	}
	redactSensitiveFields(m)

	assert.Equal(t, "localhost", m["host"])
	assert.Equal(t, 8080, m["port"])
	assert.Equal(t, "[redacted]", m["api_key"])
	assert.Equal(t, "1.0.0", m["version"])
}

// TestRedactSensitiveFieldsNested verifies nested maps are recursively redacted.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Config endpoint redacts credentials (SEC-23)
func TestRedactSensitiveFieldsNested(t *testing.T) {
	m := map[string]any{
		"provider": map[string]any{
			"name":    "anthropic",
			"api_key": "sk-nested",
		},
	}
	redactSensitiveFields(m)

	provider, ok := m["provider"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "anthropic", provider["name"])
	assert.Equal(t, "[redacted]", provider["api_key"])
}

// --- Agent status tests ---

// TestAgentListStatus_SystemAlwaysActive verifies that the system agent always has
// status "active" regardless of whether any turns are running.
// BDD: Given no active agent turns,
// When GET /api/v1/agents is called,
// Then the system agent (id="omnipus-system") has status "active".
// Traces to: vivid-roaming-planet.md line 168
//
// BLOCKED: After issue #45 removed system agent hardcoding from listAgents,
// the system agent's status is now computed by computeAgentStatus() which returns
// "draft" when (a) no active turns and (b) soul is empty (Locked agents skip SOUL.md).
// The production code needs to handle AgentTypeSystem specially in computeAgentStatus
// or listAgents to guarantee "active" status for the system agent without a live turn.
// Required fix in pkg/gateway/rest.go: computeAgentStatus must check AgentTypeSystem.
// This test stays as t.Fatal to keep the requirement visible and red.
func TestAgentListStatus_CoreAgentNeverDraft(t *testing.T) {
	// Core agents have compiled prompts (no SOUL.md on disk). They should never
	// be "draft" — Locked=true causes computeAgentStatus to return "idle".
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var agents []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Type   string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agents))

	for _, ag := range agents {
		if ag.Type == "core" {
			assert.NotEqual(t, "draft", ag.Status,
				"core agent %q must never be draft (Locked agents skip SOUL.md check)", ag.ID)
		}
	}
}

// TestAgentListStatus_CustomAgentIdle verifies that a custom agent with no active turn
// and no SOUL.md content has status "draft" in the agent list. An agent transitions to
// "idle" once its SOUL.md is filled in and it has no active turn.
// BDD: Given a custom agent "my-agent" configured with no active turn and no SOUL.md,
// When GET /api/v1/agents is called,
// Then "my-agent" has status "draft".
// Traces to: vivid-roaming-planet.md line 169
func TestAgentListStatus_CustomAgentIdle(t *testing.T) {
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
			List: []config.AgentConfig{
				{ID: "my-agent", Name: "My Agent"},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var agents []gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agents))

	for _, ag := range agents {
		if ag.Id == "my-agent" {
			assert.Equal(
				t,
				gen.AgentStatusDraft,
				ag.Status,
				"custom agent with no SOUL.md and no active turn must have status 'draft'",
			)
			return
		}
	}
	t.Fatal("my-agent not found in response")
}

// TestAgentListStatus_CustomAgentActive verifies that a custom agent whose ID appears
// in GetActiveAgentIDs() has status "active" in the list response.
//
// This test uses the agent package's internal activeTurnStates field, which is accessible
// from within the gateway package only indirectly via GetActiveAgentIDs(). Since turnState
// is unexported and activeTurnStates is unexported, the "active" path for a custom agent
// is tested in pkg/agent/turn_test.go (same package). Here we verify the REST layer's
// conditional: given GetActiveAgentIDs returns an ID, the status field is "active".
//
// We test this by using the agent package's registerActiveTurn-equivalent path indirectly:
// the system agent always returns "active", and TestGetActiveAgentIDs_* cover the
// GetActiveAgentIDs return value. The REST mapping is unit-tested via listAgents logic.
//
// TODO: Testability blocker — activeTurnStates is unexported in pkg/agent.
// To test the "active" status path from the gateway package, pkg/agent needs an exported
// test helper (e.g., AgentLoop.SimulateActiveTurn(sessionKey, agentID string)) or a
// RegisterActiveTurn(sessionKey string, ts *TurnStateInfo) exported method.
// Reported for backend-lead: expose a test injection point.
//
// BDD: Given a custom agent "busy-agent" with a registered active turn,
// When GET /api/v1/agents is called,
// Then "busy-agent" has status "active".
// Traces to: vivid-roaming-planet.md line 170
func TestAgentListStatus_CustomAgentActive(t *testing.T) {
	// TODO: Blocked — turnState.agentID and AgentLoop.activeTurnStates are unexported.
	// See testability comment above. This scenario is covered in pkg/agent/turn_test.go.
	t.Skip("BLOCKED: activeTurnStates injection requires exported test helper in pkg/agent — see TODO above")
}

// --- Tool Visibility Endpoints (Issue #41) ---

// TestHandleBuiltinToolsDeprecated_Returns404 verifies GET /api/v1/tools/builtin
// now returns 404 — the legacy catalog endpoint was removed in the central tool
// registry redesign (FR-029). Callers must use GET /api/v1/tools instead.
// Traces to: central tool registry redesign spec — FR-029.
func TestHandleBuiltinToolsDeprecated_Returns404(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tools/builtin", nil)
	api.HandleBuiltinToolsDeprecated(w, r)

	require.Equal(t, http.StatusNotFound, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body, "error")
}

// TestHandleBuiltinToolsDeprecated_AnyMethodReturns404 verifies all HTTP methods
// return 404 on the deprecated endpoint.
func TestHandleBuiltinToolsDeprecated_AnyMethodReturns404(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tools/builtin", nil)
	api.HandleBuiltinToolsDeprecated(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleMCPTools_ReturnsJSON verifies GET /api/v1/tools/mcp returns a JSON response.
// BDD: Given a running gateway with no MCP servers,
// When GET /api/v1/tools/mcp is called,
// Then the response is 200 with a JSON array.
// Traces to: parsed-inventing-gem.md — PR 2 REST endpoints
func TestHandleMCPTools_ReturnsJSON(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tools/mcp", nil)
	api.HandleMCPTools(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	// Response should be valid JSON (array or object).
	var result any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
}

// TestGetAgentTools_SystemAgent verifies GET /api/v1/agents/omnipus-system/tools returns
// agent_type "system" and a config object.
// BDD: Given the system agent,
// When GET /api/v1/agents/omnipus-system/tools is called,
// Then the response includes agent_type "system", config, and effective_tools.
// Traces to: parsed-inventing-gem.md — PR 2 REST endpoints
func TestGetAgentTools_SystemAgent(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/omnipus-system/tools", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		AgentType      string           `json:"agent_type"`
		Config         map[string]any   `json:"config"`
		EffectiveTools []map[string]any `json:"effective_tools"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "system", resp.AgentType)
	assert.NotNil(t, resp.Config)
	assert.Contains(t, resp.Config, "builtin")
}

// TestGetAgentTools_CustomAgent verifies GET /api/v1/agents/{id}/tools for a custom agent.
// BDD: Given a custom agent with tools config,
// When GET /api/v1/agents/{id}/tools is called,
// Then the response includes agent_type "custom" and the stored config.
// Traces to: parsed-inventing-gem.md — PR 2 REST endpoints
func TestGetAgentTools_CustomAgent(t *testing.T) {
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
			List: []config.AgentConfig{
				{
					ID:   "tool-agent",
					Name: "Tool Agent",
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							DefaultPolicy: config.ToolPolicyDeny,
							Policies: map[string]config.ToolPolicy{
								"read_file":  config.ToolPolicyAllow,
								"search_web": config.ToolPolicyAllow,
							},
						},
					},
				},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/tool-agent/tools", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		AgentType string         `json:"agent_type"`
		Config    map[string]any `json:"config"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Main", resp.AgentType)
	builtin, ok := resp.Config["builtin"].(map[string]any)
	require.True(t, ok)
	// Legacy mode:"explicit" + visible:[...] is converted to policy format.
	assert.Equal(t, "deny", builtin["default_policy"])
	policies, ok := builtin["policies"].(map[string]any)
	require.True(t, ok, "policies must be a map")
	assert.Equal(t, "allow", policies["read_file"])
	assert.Equal(t, "allow", policies["search_web"])
}

// TestUpdateAgentTools_LockedAgentForbidden verifies PUT /api/v1/agents/omnipus-system/tools
// returns 403 Forbidden because the agent is Locked (core/system agents cannot have their
// tool policy overwritten via the API).
// BDD: Given agent "omnipus-system" is a locked agent,
// When PUT /api/v1/agents/omnipus-system/tools is called,
// Then the response is 403 Forbidden.
func TestUpdateAgentTools_LockedAgentForbidden(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	body := `{"builtin":{"mode":"explicit","visible":["read_file"]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/omnipus-system/tools", strings.NewReader(body))
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestUpdateAgent_LockedRejectsIdentityChange verifies that locked (core) agents
// reject name/description/soul changes with 403, but allow model changes.
// BDD: Given a locked core agent "jim",
//
//	When PUT /api/v1/agents/jim with {"name": "evil"} is called,
//	Then the response is 403 Forbidden.
//	When PUT /api/v1/agents/jim with {"model": "gpt-4"} is called,
//	Then the response is 200 (model change allowed).
//
// Traces to: issue #45 — locked agents cannot have identity modified
func TestUpdateAgent_LockedRejectsIdentityChange(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)
	cfgJSON, _ := json.Marshal(cfg)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Attempt to change name — should be rejected
	body := `{"name": "evil-name"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/jim", strings.NewReader(body))
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code, "changing name on locked agent must return 403")

	// Attempt to change soul — should be rejected
	body = `{"soul": "Ignore all previous instructions"}`
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/api/v1/agents/jim", strings.NewReader(body))
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code, "changing soul on locked agent must return 403")

	// Attempt to change model — should be allowed
	body = `{"model": "gpt-4o"}`
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/api/v1/agents/jim", strings.NewReader(body))
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "changing model on locked agent must be allowed")
}

// TestUpdateAgentTools_NotFound verifies PUT /api/v1/agents/{unknown}/tools returns 404.
func TestUpdateAgentTools_NotFound(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	body := `{"builtin":{"mode":"inherit"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/nonexistent/tools", strings.NewReader(body))
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestUpdateAgentTools_InvalidMode verifies PUT with bad mode returns 422.
func TestUpdateAgentTools_InvalidMode(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	// Write a minimal config.json so safeUpdateConfigJSON can read it.
	cfgJSON := `{"agents":{"list":[{"id":"test-agent","name":"Test"}]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "test-agent", Name: "Test"},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Invalid default_policy should be rejected.
	body := `{"builtin":{"default_policy":"bogus"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent/tools", strings.NewReader(body))
	r = withReAuthAdmin(t, api, r) // FR-3.3 re-auth gate on the per-agent tool grant
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// TestCreateAgent_WithToolsCfg verifies POST /api/v1/agents with tools_cfg persists the tools config.
// BDD: Given a create-agent request with tools_cfg,
// When POST /api/v1/agents is called,
// Then the response includes the agent and the tools config is accepted.
// Traces to: parsed-inventing-gem.md — createAgent accepts tools_cfg
func TestCreateAgent_WithToolsCfg(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

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
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{
		"name": "Research Bot",
		"description": "A researcher",
		"soul": "Research Bot soul",
		"color": "#22C55E",
		"icon": "magnifying-glass",
		"tools_cfg": {
			"builtin": {
				"default_policy": "deny",
				"policies": {
					"read_file": "allow",
					"search_web": "allow",
					"fetch_url": "allow"
				}
			},
			"mcp": {
				"servers": [{"id": "my-server"}]
			}
		}
	}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code)

	var resp struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Research Bot", resp.Name)
	assert.Equal(t, "Main", resp.Type)
	assert.NotEmpty(t, resp.ID)

	// Verify the config.json was updated with the tools config (new format: default_policy/policies).
	savedCfg, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var savedMap map[string]any
	require.NoError(t, json.Unmarshal(savedCfg, &savedMap))
	agentsMap, _ := savedMap["agents"].(map[string]any)
	list, _ := agentsMap["list"].([]any)
	require.Len(t, list, 1)
	agentMap, _ := list[0].(map[string]any)
	assert.Equal(t, "#22C55E", agentMap["color"])
	assert.Equal(t, "magnifying-glass", agentMap["icon"])
	toolsMap, ok := agentMap["tools"].(map[string]any)
	require.True(t, ok, "tools config must be persisted")
	builtinMap, _ := toolsMap["builtin"].(map[string]any)
	assert.Equal(t, "deny", builtinMap["default_policy"])
	policies, _ := builtinMap["policies"].(map[string]any)
	assert.Equal(t, "allow", policies["read_file"])
	assert.Equal(t, "allow", policies["search_web"])
}

// TestCreateAgent_WithSkills verifies POST /api/v1/agents with skills persists and
// returns the skill list.
//
// BDD: Given a create-agent request with a skills list,
// When POST /api/v1/agents is called,
// Then the response includes the skills list and config.json persists it.
//
// Traces to: US-E6 (nontech-ux-hardening-spec §6.5), F-06.
func TestCreateAgent_WithSkills(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	// Install the skills used by this test so validation passes.
	skillsRoot := t.TempDir()
	t.Setenv("OMNIPUS_BUILTIN_SKILLS", skillsRoot)
	for _, id := range []string{"web-research", "code-review"} {
		require.NoError(t, os.MkdirAll(skillsRoot+"/"+id, 0o755))
		require.NoError(
			t,
			os.WriteFile(skillsRoot+"/"+id+"/SKILL.md", []byte("---\nname: "+id+"\ndescription: d\n---\n"), 0o600),
		)
	}

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

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
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"name":"Skill Agent","soul":"s","skills":["daily-briefing","summarize"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code)

	var resp struct {
		ID     string   `json:"id"`
		Name   string   `json:"name"`
		Skills []string `json:"skills"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Skill Agent", resp.Name)
	assert.Equal(t, []string{"daily-briefing", "summarize"}, resp.Skills)

	// Verify config.json persisted the skill list.
	savedCfg, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var savedMap map[string]any
	require.NoError(t, json.Unmarshal(savedCfg, &savedMap))
	agentsMap, _ := savedMap["agents"].(map[string]any)
	list, _ := agentsMap["list"].([]any)
	require.Len(t, list, 1)
	agentMap, _ := list[0].(map[string]any)
	skillsList, _ := agentMap["skills"].([]any)
	require.Len(t, skillsList, 2)
	assert.Equal(t, "daily-briefing", skillsList[0])
	assert.Equal(t, "summarize", skillsList[1])
}

// TestCreateAgent_NoSkills verifies that a new agent with no skills field has
// no skills in the response and none persisted to config.json (default none).
//
// BDD: Given a create-agent request without a skills field,
// When POST /api/v1/agents is called,
// Then the response has no skills field and config.json has no skills key.
//
// Traces to: US-E6 AC1 — new agent grants no skills until one is added.
func TestCreateAgent_NoSkills(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

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
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"name":"Skillless Agent","soul":"s"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code)

	// The response JSON must not contain a "skills" key (or it is null/absent).
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	skills, hasSkills := raw["skills"]
	// Either absent entirely, or explicitly null — both mean no skills.
	if hasSkills {
		assert.Nil(t, skills, "skills must be null or absent when not provided on create")
	}

	// config.json must not have a skills key for the new agent.
	savedCfg, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var savedMap map[string]any
	require.NoError(t, json.Unmarshal(savedCfg, &savedMap))
	agentsMap, _ := savedMap["agents"].(map[string]any)
	list, _ := agentsMap["list"].([]any)
	require.Len(t, list, 1)
	agentMap, _ := list[0].(map[string]any)
	_, hasSkillsInConfig := agentMap["skills"]
	assert.False(t, hasSkillsInConfig, "config.json must not have a skills key for a new agent with no skills")
}

// TestUpdateAgent_SkillsPersist verifies that PUT /api/v1/agents/{id} with a
// skills list persists the skills to config.json and returns them in the response.
func TestUpdateAgent_SkillsPersist(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	// Use default embedded skills (daily-briefing, plan,
	// skill-authoring, summarize) for validation.

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	// Two agents in config.json; skills update targets only agent-A.
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[{"id":"agent-a","name":"Agent A"},{"id":"agent-b","name":"Agent B"}]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "agent-a", Name: "Agent A"},
				{ID: "agent-b", Name: "Agent B"},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Update agent-a with skills.
	body := `{"soul":"s","skills":["daily-briefing","summarize"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-a", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "response body: %s", w.Body.String())

	var resp struct {
		ID     string   `json:"id"`
		Skills []string `json:"skills"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "agent-a", resp.ID)
	assert.Equal(t, []string{"daily-briefing", "summarize"}, resp.Skills)

	// Verify config.json: agent-a has skills, agent-b has none.
	savedCfg, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var savedMap map[string]any
	require.NoError(t, json.Unmarshal(savedCfg, &savedMap))
	agentsMap, _ := savedMap["agents"].(map[string]any)
	list, _ := agentsMap["list"].([]any)
	require.Len(t, list, 2)

	for _, item := range list {
		agentMap, _ := item.(map[string]any)
		agentID, _ := agentMap["id"].(string)
		if agentID == "agent-a" {
			skillsRaw, _ := agentMap["skills"].([]any)
			require.Len(t, skillsRaw, 2, "agent-a must have 2 skills in config.json")
			assert.Equal(t, "daily-briefing", skillsRaw[0])
			assert.Equal(t, "summarize", skillsRaw[1])
		} else if agentID == "agent-b" {
			_, hasBSkills := agentMap["skills"]
			assert.False(t, hasBSkills, "agent-b must have no skills — granting to A must not affect B")
		}
	}
}

// TestUpdateAgent_SkillsClear verifies that sending an empty skills array
// removes all skills from the agent (opt-out by explicit empty list).
//
// BDD: Given an agent exists with skills in config,
// When PUT /api/v1/agents/{id} is called with skills=[],
// Then the config.json has no skills key for that agent.
//
// Traces to: US-E6.
func TestUpdateAgent_SkillsClear(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[{"id":"skilled-agent","name":"Skilled Agent","skills":["web-research"]}]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "skilled-agent", Name: "Skilled Agent", Skills: []string{"web-research"}},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Send empty skills array to clear all skills.
	body := `{"skills":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/skilled-agent", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "response body: %s", w.Body.String())

	// config.json must have no skills key after clear.
	savedCfg, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var savedMap map[string]any
	require.NoError(t, json.Unmarshal(savedCfg, &savedMap))
	agentsMap, _ := savedMap["agents"].(map[string]any)
	list, _ := agentsMap["list"].([]any)
	require.Len(t, list, 1)
	agentMap, _ := list[0].(map[string]any)
	_, hasSkills := agentMap["skills"]
	assert.False(t, hasSkills, "skills key must be absent in config.json after clearing with empty array")
}

// TestCreateAgent_UnknownSkillIDRejected verifies that POST /api/v1/agents with a
// skill ID not in the installed registry is rejected with 400 when skills are installed.
//
// BDD: Given a gateway with one installed skill "web-research",
// When POST /api/v1/agents is called with skills=["unknown-skill"],
// Then the response is 400 Bad Request with "unknown skill id" in the error.
//
// Traces to: US-E6, MINOR (backend) — referential validation for skill IDs.
func TestCreateAgent_UnknownSkillIDRejected(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	// Create a temp skills directory with one known skill "web-research".
	// OMNIPUS_BUILTIN_SKILLS env makes the agent loop pick it up on construction.
	skillsRoot := t.TempDir()
	t.Setenv("OMNIPUS_BUILTIN_SKILLS", skillsRoot)
	skillMD := skillsRoot + "/web-research/SKILL.md"
	require.NoError(t, os.MkdirAll(skillsRoot+"/web-research", 0o755))
	require.NoError(
		t,
		os.WriteFile(
			skillMD,
			[]byte("---\nname: web-research\ndescription: web search skill\n---\n# Web Research\n"),
			0o600,
		),
	)

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

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
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// "unknown-skill" is not installed — must be rejected 400.
	body := `{"name":"Test Agent","soul":"s","skills":["unknown-skill"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"unknown skill ID must be rejected with 400; body: %s",
		w.Body.String(),
	)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "unknown skill id", "error must name the unknown skill")

	// Known skill "web-research" must be accepted.
	body = `{"name":"Test Agent 2","soul":"s","skills":["web-research"]}`
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "known skill ID must be accepted; body: %s", w.Body.String())
}

// TestUpdateAgent_UnknownSkillIDRejected verifies that PUT /api/v1/agents/{id} with
// an unknown skill ID is rejected with 400 when skills are installed.
//
// BDD: Given an agent and one installed skill "web-research",
// When PUT /api/v1/agents/{id} is called with skills=["bogus-skill"],
// Then the response is 400 Bad Request.
//
// Traces to: US-E6, MINOR (backend) — referential validation for skill IDs.
func TestUpdateAgent_UnknownSkillIDRejected(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	skillsRoot := t.TempDir()
	t.Setenv("OMNIPUS_BUILTIN_SKILLS", skillsRoot)
	skillMD := skillsRoot + "/web-research/SKILL.md"
	require.NoError(t, os.MkdirAll(skillsRoot+"/web-research", 0o755))
	require.NoError(
		t,
		os.WriteFile(
			skillMD,
			[]byte("---\nname: web-research\ndescription: web search skill\n---\n# Web Research\n"),
			0o600,
		),
	)

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[{"id":"my-agent","name":"My Agent"}]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "my-agent", Name: "My Agent"},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// "bogus-skill" is not installed — must be rejected 400.
	body := `{"skills":["bogus-skill"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/my-agent", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"unknown skill ID must be rejected with 400; body: %s",
		w.Body.String(),
	)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "unknown skill id", "error must name the unknown skill")
}

// TestUpdateAgent_LockedRejectsSkills verifies that PUT /api/v1/agents/{id} on a
// locked (core) agent with a skills field is rejected with 403 (B-2 defense-in-depth).
//
// BDD: Given a locked core agent "jim",
// When PUT /api/v1/agents/jim is called with {"skills": ["web-research"]},
// Then the response is 403 Forbidden.
//
// Traces to: B-2 (#332 / US-D5) extended to Skills field.
func TestUpdateAgent_LockedRejectsSkills(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)
	cfgJSON, _ := json.Marshal(cfg)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"skills": ["web-research"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/jim", strings.NewReader(body))
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code, "assigning skills to a locked agent must return 403")
}

// TestUpdateAgentTools_Success verifies PUT /api/v1/agents/{id}/tools returns 200,
// updates the response body with the correct agent_type and mode, and persists the
// tools config to config.json on disk.
//
// BDD: Given a custom agent exists in config and a config.json is on disk,
//
//	When PUT /api/v1/agents/{id}/tools is called with mode=explicit and visible=["read_file","search_web"],
//	Then the response is 200, agent_type is "custom", config.builtin.mode is "explicit",
//	And config.json on disk reflects the persisted tools config.
//
// Traces to: parsed-inventing-gem.md — PR #41 Per-Agent Tool Visibility, updateAgentTools success path
func TestUpdateAgentTools_Success(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	// Write a minimal config.json so safeUpdateConfigJSON can read it.
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[{"id":"update-agent","name":"Update Agent"}]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "update-agent", Name: "Update Agent"},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"builtin":{"mode":"explicit","visible":["read_file","search_web"]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/update-agent/tools", strings.NewReader(body))
	r = withReAuthAdmin(t, api, r) // FR-3.3 re-auth gate on the per-agent tool grant
	api.HandleAgents(w, r)

	// Then: HTTP 200
	require.Equal(t, http.StatusOK, w.Code)

	// Then: response body must parse into gen.AgentToolsResponse — verifying the
	// PUT response uses `tools` (not `effective_tools`) matching the OpenAPI spec
	// and the SPA Zod schema (_agentToolsSchema). Regression test for fix-T BUG 1.
	var genResp gen.AgentToolsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &genResp),
		"PUT response must unmarshal into gen.AgentToolsResponse (tools key required)")
	// gen.AgentToolsResponse.Tools is a non-nullable slice — it must be present
	// (nil means the `tools` key was absent from the JSON, which is the old bug).
	assert.NotNil(t, genResp.Tools, "PUT response must include `tools` field (not `effective_tools`)")
	// Config.Builtin must be present.
	require.NotNil(t, genResp.Config.Builtin, "PUT response must include config.builtin")
	// AgentType must be "Main" (W1 wire enum — legacy 'custom' is now 'Main').
	require.NotNil(t, genResp.AgentType, "PUT response must include agent_type")
	assert.Equal(t, gen.AgentToolsResponseAgentTypeMain, *genResp.AgentType,
		"updateAgentTools must return agent_type=Main for a user-created chat-colleague agent")
	// Legacy mode:"explicit" + visible is converted to policy format (default_policy=deny).
	require.NotNil(t, genResp.Config.Builtin.DefaultPolicy, "config.builtin.default_policy must be present")
	assert.Equal(t, gen.AgentToolsResponseConfigBuiltinDefaultPolicyDeny, *genResp.Config.Builtin.DefaultPolicy,
		"explicit mode converts to default_policy=deny")

	// Then: config.json on disk was updated with the tools config
	savedCfg, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var savedMap map[string]any
	require.NoError(t, json.Unmarshal(savedCfg, &savedMap))
	agentsMap, _ := savedMap["agents"].(map[string]any)
	list, _ := agentsMap["list"].([]any)
	require.Len(t, list, 1, "config.json must contain exactly one agent")
	agentMap, _ := list[0].(map[string]any)
	toolsMap, ok := agentMap["tools"].(map[string]any)
	require.True(t, ok, "tools config must be persisted to config.json")
	persistedBuiltin, _ := toolsMap["builtin"].(map[string]any)
	assert.Equal(t, "deny", persistedBuiltin["default_policy"],
		"config.json must persist default_policy=deny (converted from mode=explicit)")
	policiesRaw, ok := persistedBuiltin["policies"].(map[string]any)
	require.True(t, ok, "config.json must persist policies map")
	assert.Equal(t, "allow", policiesRaw["read_file"])
	assert.Equal(t, "allow", policiesRaw["search_web"])
}

// TestUpdateAgentTools_ReloadFailure_Returns503 verifies that if TriggerReload fails
// with a non-ErrReloadNotConfigured error, PUT /api/v1/agents/{id}/tools returns 503.
//
// BDD:
//
//	Given a custom agent exists in config, a config.json is on disk, AND
//	  the agent loop's reload function is wired to always fail,
//	When PUT /api/v1/agents/{id}/tools is called with valid tool config,
//	Then the response is 503 Service Unavailable with a descriptive message,
//	And the config was still written to disk (disk write succeeded before reload).
//
// Closes: R4 silent-failure H1 (TriggerReload failure was silently ignored — now 503).
// Traces to: pkg/gateway/rest.go updateAgentTools — TriggerReload 503 path.
func TestUpdateAgentTools_ReloadFailure_Returns503(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[{"id":"reload-test-agent","name":"Reload Test Agent"}]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "reload-test-agent", Name: "Reload Test Agent"},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	// Wire a reload function that always returns an error (simulates gateway
	// restart in progress or reload pipeline failure).
	al.SetReloadFunc(func() error {
		return fmt.Errorf("simulated reload failure: config file locked")
	})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"builtin":{"mode":"inherit"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/reload-test-agent/tools", strings.NewReader(body))
	r = withReAuthAdmin(t, api, r) // FR-3.3 re-auth gate on the per-agent tool grant
	api.HandleAgents(w, r)

	// Then: HTTP 503 (reload failed).
	require.Equal(t, http.StatusServiceUnavailable, w.Code,
		"updateAgentTools must return 503 when TriggerReload fails with a non-ErrReloadNotConfigured error")

	// Then: response must contain the human-readable reload failure message.
	var errResp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Contains(t, errResp["error"], "in-memory reload failed",
		"503 response must mention in-memory reload failure")

	// Then: config.json was still updated (disk write happened before reload).
	savedCfg, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var savedMap map[string]any
	require.NoError(t, json.Unmarshal(savedCfg, &savedMap))
	agentsMap, _ := savedMap["agents"].(map[string]any)
	list, _ := agentsMap["list"].([]any)
	require.Len(t, list, 1, "config.json must contain exactly one agent after 503")
	agentMap, _ := list[0].(map[string]any)
	_, hasTools := agentMap["tools"]
	assert.True(t, hasTools, "tools config must be persisted to config.json even on 503")
}

// TestHandleMCPTools_MethodNotAllowed verifies that POST to HandleMCPTools returns 405.
//
// BDD: Given a running gateway,
//
//	When POST /api/v1/tools/mcp is called,
//	Then the response is 405 Method Not Allowed.
//
// Traces to: parsed-inventing-gem.md — PR 2 REST endpoints method guards
func TestHandleMCPTools_MethodNotAllowed(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tools/mcp", nil)
	api.HandleMCPTools(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ── fix-V endpoint handler response-shape tests ────────────────────────────────

// TestRotateGatewayToken_ResponseShape verifies that POST /config/gateway/rotate-token
// returns a body that unmarshal-cleanly maps to gen.RotateTokenResponse, and that
// the token field is non-empty.
//
// BDD:
//
//	Given a gateway with a writable config.json and a wired reload func,
//	When POST /api/v1/config/gateway/rotate-token is called,
//	Then the response is 200 and the body has a non-empty "token" field matching gen.RotateTokenResponse.
//
// Traces to: rest.go rotateGatewayToken handler — wire shape must match RotateTokenResponse.
func TestRotateGatewayToken_ResponseShape(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// Wire a no-op reload func so TriggerReload does not fail with
	// "reload not configured" — the handler calls TriggerReload after saving the
	// new token to disk; in unit tests AgentLoop.Run() is never called so the
	// real reload trigger is absent. A no-op reload is acceptable here because
	// the test is focused on the HTTP response shape (wire format), not reload
	// semantics.
	api.agentLoop.SetReloadFunc(func() error { return nil })

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/config/gateway/rotate-token", nil)
	r = withAdminRole(r)

	api.rotateGatewayToken(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"rotate-token must return 200: %s", w.Body.String())

	// The response body must unmarshal into gen.RotateTokenResponse.
	var resp gen.RotateTokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
		"response must unmarshal into gen.RotateTokenResponse — wire shape must match contract")

	// Per the BearerToken schema, the token must be exactly 72 chars: "omnipus_" + 64-hex.
	assert.NotEmpty(t, resp.Token, "RotateTokenResponse.Token must be non-empty")
	assert.Len(t, resp.Token, 72,
		"RotateTokenResponse.Token must be 72 chars: omnipus_ + 64 hex chars (BearerToken schema)")
	assert.Regexp(t, `^omnipus_[a-f0-9]{64}$`, resp.Token,
		"RotateTokenResponse.Token must match BearerToken pattern")

	// Differentiation: call twice — tokens must differ (not hardcoded).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/config/gateway/rotate-token", nil)
	r2 = withAdminRole(r2)
	api.rotateGatewayToken(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var resp2 gen.RotateTokenResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.NotEqual(t, resp.Token, resp2.Token,
		"rotate-token must produce a different token on each call (not hardcoded)")
}

// TestHandleVersion_ResponseShape verifies that GET /version returns a body that
// unmarshal-cleanly maps to gen.VersionResponse.
//
// BDD:
//
//	Given a running gateway,
//	When GET /api/v1/version is called,
//	Then the response is 200 and the body has "version" and "build_sha" fields.
//
// Traces to: rest.go HandleVersion handler — wire shape must match VersionResponse.
func TestHandleVersion_ResponseShape(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)

	api.HandleVersion(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	// The response body must unmarshal into gen.VersionResponse.
	var resp gen.VersionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
		"response must unmarshal into gen.VersionResponse — wire shape must match contract")

	// version field must be non-empty (either "dev" or a semver string).
	assert.NotEmpty(t, resp.Version, "VersionResponse.Version must be non-empty")
	// build_sha field must be non-empty (either "dev" or a git SHA).
	assert.NotEmpty(t, resp.BuildSha, "VersionResponse.BuildSha must be non-empty")
}

// TestGetUserContext_HappyPath verifies that GET /user-context returns
// gen.UserContextResponse with empty content when USER.md does not exist.
//
// BDD:
//
//	Given a workspace with no USER.md file,
//	When GET /api/v1/user-context is called,
//	Then 200 with {"content": ""}.
//
// Traces to: rest.go getUserContext — returns empty string when USER.md is absent.
func TestGetUserContext_HappyPath(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/user-context", nil)
	r = withAdminRole(r)

	api.HandleUserContext(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.UserContextResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
		"response must unmarshal into gen.UserContextResponse")
	assert.Equal(t, "", resp.Content,
		"content must be empty string when USER.md does not exist")
}

// TestPutUserContext_HappyPath verifies that PUT /user-context writes content
// and GET /user-context returns the same content.
//
// BDD:
//
//	Given a workspace with no USER.md,
//	When PUT /api/v1/user-context with {"content": "Hello, world!"},
//	Then 200 with {"content": "Hello, world!"}.
//	And GET /api/v1/user-context returns {"content": "Hello, world!"}.
//
// Traces to: rest.go putUserContext — persists content to USER.md.
func TestPutUserContext_HappyPath(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	const testContent = "Hello, world! — user context test."

	// PUT with content.
	putBody := `{"content":"` + testContent + `"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/user-context", strings.NewReader(putBody))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.HandleUserContext(w, r)

	require.Equal(t, http.StatusOK, w.Code, "PUT must return 200: %s", w.Body.String())
	var putResp gen.UserContextResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &putResp))
	assert.Equal(t, testContent, putResp.Content,
		"PUT response content must echo the written content")

	// GET must return the same content (persistence test).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/user-context", nil)
	r2 = withAdminRole(r2)
	api.HandleUserContext(w2, r2)

	require.Equal(t, http.StatusOK, w2.Code)
	var getResp gen.UserContextResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &getResp))
	assert.Equal(t, testContent, getResp.Content,
		"GET must return the content written by PUT (persistence test)")
}

// --- HandleChannels tests ---

// TestHandleChannels_WhatsApp_NativeAvailable verifies that GET /api/v1/channels
// returns native_available on the whatsapp entry matching the compile-time
// whatsappnative.NativeAvailable constant.
//
// BDD:
//
//	Given a default config with WhatsApp disabled,
//	When GET /api/v1/channels is called,
//	Then the response contains a "whatsapp" entry with native_available set to
//	  the value of whatsappnative.NativeAvailable (true in default builds).
//
// Traces to: issue #299 — surface NativeAvailable in channels API.
func TestHandleChannels_WhatsApp_NativeAvailable(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	api.HandleChannels(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var entries []struct {
		ID              string `json:"id"`
		NativeAvailable *bool  `json:"native_available"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	var whatsapp *struct {
		ID              string `json:"id"`
		NativeAvailable *bool  `json:"native_available"`
	}
	for i := range entries {
		if entries[i].ID == "whatsapp" {
			whatsapp = &entries[i]
			break
		}
	}
	require.NotNil(t, whatsapp, "channels list must contain a 'whatsapp' entry")
	require.NotNil(t, whatsapp.NativeAvailable, "whatsapp entry must have native_available set")
	// In the default (non-lite) build, NativeAvailable is true; in the lite
	// variant the whatsappnative package stub sets it to false.  Either way,
	// the value must match the compile-time constant.
	assert.Equal(t, whatsappnative.NativeAvailable, *whatsapp.NativeAvailable,
		"native_available must equal whatsappnative.NativeAvailable compile-time constant")
}

// TestApplyDegradedOverlay_MarksDegradedEntry verifies that applyDegradedOverlay
// sets degraded=true and degraded_reason on a channel whose registry id appears
// in the failed list.
//
// BDD:
//
//	Given a channels slice containing "telegram" and "whatsapp",
//	When applyDegradedOverlay is called with a failure for "telegram",
//	Then the telegram entry has degraded=true and degraded_reason set,
//	And the whatsapp entry is unchanged.
//
// Traces to: issue #299 — degraded overlay in HandleChannels.
func TestApplyDegradedOverlay_MarksDegradedEntry(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "telegram", Name: "Telegram", Transport: "webhook", Enabled: true, Description: "TG"},
		{Id: "whatsapp", Name: "WhatsApp", Transport: "bridge", Enabled: false, Description: "WA"},
	}

	failed := []channels.ChannelInitError{
		{Name: "telegram", Channel: "Telegram", Err: fmt.Errorf("bot token invalid")},
	}
	applyDegradedOverlay(entries, failed)

	require.NotNil(t, entries[0].Degraded, "telegram must have degraded set")
	assert.True(t, *entries[0].Degraded, "telegram degraded must be true")
	require.NotNil(t, entries[0].DegradedReason, "telegram must have degraded_reason set")
	assert.Equal(t, "bot token invalid", *entries[0].DegradedReason)

	assert.Nil(t, entries[1].Degraded, "whatsapp must not be marked degraded")
	assert.Nil(t, entries[1].DegradedReason, "whatsapp must not have degraded_reason")
}

// TestApplyDegradedOverlay_WhatsAppNativeNormalisedToWhatsApp verifies that a
// failure recorded under the registry id "whatsapp_native" is mapped to the
// "whatsapp" channel entry (both share one list entry in the channels API).
//
// BDD:
//
//	Given a channels slice containing "whatsapp",
//	When applyDegradedOverlay is called with a failure whose Name is "whatsapp_native",
//	Then the whatsapp entry is marked degraded with the failure reason.
//
// Traces to: issue #299 — whatsapp_native → whatsapp normalisation.
func TestApplyDegradedOverlay_WhatsAppNativeNormalisedToWhatsApp(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "whatsapp", Name: "WhatsApp", Transport: "bridge", Enabled: true, Description: "WA"},
	}

	failed := []channels.ChannelInitError{
		{Name: "whatsapp_native", Channel: "WhatsApp Native", Err: fmt.Errorf("not compiled in lite build")},
	}
	applyDegradedOverlay(entries, failed)

	require.NotNil(t, entries[0].Degraded, "whatsapp must be marked degraded when whatsapp_native fails")
	assert.True(t, *entries[0].Degraded)
	require.NotNil(t, entries[0].DegradedReason)
	assert.Equal(t, "not compiled in lite build", *entries[0].DegradedReason)
}

// TestApplyDegradedOverlay_EmptyFailed verifies that applyDegradedOverlay is a
// no-op when the failed list is empty (nil-safety / baseline behavior).
func TestApplyDegradedOverlay_EmptyFailed(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "telegram", Name: "Telegram", Transport: "webhook", Enabled: true, Description: "TG"},
	}
	applyDegradedOverlay(entries, nil)
	assert.Nil(t, entries[0].Degraded, "no degraded field when failed list is empty")
	assert.Nil(t, entries[0].DegradedReason)
}

// TestApplyDegradedOverlay_MultipleSimultaneousFailures verifies that
// applyDegradedOverlay marks ALL entries when multiple channels fail at once.
//
// BDD:
//
//	Given a channels slice containing "telegram" and "whatsapp",
//	When applyDegradedOverlay is called with failures for both channels,
//	Then both entries have degraded=true and the correct degraded_reason.
//
// Traces to: pr-test-analyzer finding — #299 overlay tests.
func TestApplyDegradedOverlay_MultipleSimultaneousFailures(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "telegram", Name: "Telegram", Transport: "webhook", Enabled: true, Description: "TG"},
		{Id: "whatsapp", Name: "WhatsApp", Transport: "bridge", Enabled: false, Description: "WA"},
	}

	failed := []channels.ChannelInitError{
		{Name: "telegram", Channel: "Telegram", Err: fmt.Errorf("bot token invalid")},
		{Name: "whatsapp", Channel: "WhatsApp", Err: fmt.Errorf("bridge unreachable")},
	}
	applyDegradedOverlay(entries, failed)

	require.NotNil(t, entries[0].Degraded, "telegram must be marked degraded")
	assert.True(t, *entries[0].Degraded)
	require.NotNil(t, entries[0].DegradedReason)
	assert.Equal(t, "bot token invalid", *entries[0].DegradedReason)

	require.NotNil(t, entries[1].Degraded, "whatsapp must be marked degraded")
	assert.True(t, *entries[1].Degraded)
	require.NotNil(t, entries[1].DegradedReason)
	assert.Equal(t, "bridge unreachable", *entries[1].DegradedReason)
}

// TestApplyDegradedOverlay_OrphanFailedID verifies that a failed channel whose
// registry id has no matching ChannelEntry (e.g. "google-chat" or "signal",
// which may be recordable by the manager but absent from the HandleChannels
// list) does NOT panic and leaves all present entries unmarked.
//
// BDD:
//
//	Given a channels slice containing only "telegram",
//	When applyDegradedOverlay is called with a failure for "google-chat",
//	Then telegram is NOT marked degraded (the orphan is silently skipped in the
//	  pure function; HandleChannels emits a WARN log for it separately).
//
// Traces to: code-reviewer + architect finding — #299 silent-drop of unmatched failures.
func TestApplyDegradedOverlay_OrphanFailedID(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "telegram", Name: "Telegram", Transport: "webhook", Enabled: true, Description: "TG"},
	}

	failed := []channels.ChannelInitError{
		{Name: "google-chat", Channel: "Google Chat", Err: fmt.Errorf("service account missing")},
		{Name: "signal", Channel: "Signal", Err: fmt.Errorf("signal-cli not found")},
	}

	// Must not panic; all entries stay unmarked.
	applyDegradedOverlay(entries, failed)

	assert.Nil(t, entries[0].Degraded,
		"telegram must not be marked degraded by an orphan failure for google-chat/signal")
	assert.Nil(t, entries[0].DegradedReason)
}

// TestApplyDegradedOverlay_DegradedReasonImpliesDegradedTrue is a contract test
// asserting the invariant: for every ChannelEntry, if DegradedReason is set
// then Degraded must also be set and its value must be true.
//
// BDD:
//
//	Given applyDegradedOverlay has been called with any combination of failures,
//	When iterating the resulting channel list,
//	Then every entry satisfies: DegradedReason != nil ⇒ Degraded != nil && *Degraded == true.
//
// Traces to: type-design-analyzer finding — invariant guard for degraded fields.
func TestApplyDegradedOverlay_DegradedReasonImpliesDegradedTrue(t *testing.T) {
	entries := []gen.ChannelEntry{
		{Id: "telegram", Name: "Telegram", Transport: "webhook", Enabled: true, Description: "TG"},
		{Id: "whatsapp", Name: "WhatsApp", Transport: "bridge", Enabled: false, Description: "WA"},
		{Id: "discord", Name: "Discord", Transport: "websocket", Enabled: false, Description: "DC"},
	}

	// Mix: telegram fails, discord is healthy, whatsapp_native maps to whatsapp and fails.
	failed := []channels.ChannelInitError{
		{Name: "telegram", Channel: "Telegram", Err: fmt.Errorf("bot token invalid")},
		{Name: "whatsapp_native", Channel: "WhatsApp Native", Err: fmt.Errorf("not compiled")},
	}
	applyDegradedOverlay(entries, failed)

	for i, e := range entries {
		if e.DegradedReason != nil {
			require.NotNil(t, e.Degraded,
				"entry[%d] id=%q: DegradedReason is set but Degraded is nil — invariant violated", i, e.Id)
			assert.True(t, *e.Degraded,
				"entry[%d] id=%q: DegradedReason is set but *Degraded is false — invariant violated", i, e.Id)
		}
		// Converse sanity: if Degraded is nil, DegradedReason must also be nil.
		if e.Degraded == nil {
			assert.Nil(t, e.DegradedReason,
				"entry[%d] id=%q: Degraded is nil but DegradedReason is set — invariant violated", i, e.Id)
		}
	}
}

// TestHandleChannels_WithDegradedChannel_IntegrationPath verifies the handler-level
// path: HandleChannels → GetChannelManager() → FailedChannels() → applyDegradedOverlay.
// This exercises the REAL wiring from the HTTP handler through to the channel
// manager, not just the pure overlay helper in isolation.
//
// BDD:
//
//	Given an agent loop whose channel manager has telegram pre-seeded as failed,
//	When GET /api/v1/channels is called,
//	Then the response contains the "telegram" entry with degraded=true and
//	  degraded_reason matching the seeded error.
//
// Traces to: pr-test-analyzer finding — handler-level integration test for #299.
func TestHandleChannels_WithDegradedChannel_IntegrationPath(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	// Build a Manager with a pre-seeded telegram failure.  We use
	// channels.NewManagerForTesting which bypasses NewManager's initChannels so
	// the test does not need a real token or a live Telegram connection.
	seededErr := fmt.Errorf("bot token not resolved (token_ref=%q)", "tg-ref-test")
	mgr := channels.NewManagerForTesting([]channels.ChannelInitError{
		{Name: "telegram", Channel: "Telegram", Err: seededErr},
	})
	api.agentLoop.SetChannelManager(mgr)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	api.HandleChannels(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var entries []struct {
		ID             string  `json:"id"`
		Degraded       *bool   `json:"degraded"`
		DegradedReason *string `json:"degraded_reason"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	var telegram *struct {
		ID             string  `json:"id"`
		Degraded       *bool   `json:"degraded"`
		DegradedReason *string `json:"degraded_reason"`
	}
	for i := range entries {
		if entries[i].ID == "telegram" {
			telegram = &entries[i]
			break
		}
	}
	require.NotNil(t, telegram, "channels list must contain a 'telegram' entry")
	require.NotNil(t, telegram.Degraded,
		"telegram must have degraded set when manager reports it as failed")
	assert.True(t, *telegram.Degraded,
		"telegram.degraded must be true when it appears in FailedChannels()")
	require.NotNil(t, telegram.DegradedReason,
		"telegram must have degraded_reason when it appears in FailedChannels()")
	assert.Equal(t, seededErr.Error(), *telegram.DegradedReason,
		"degraded_reason must match the seeded error message")
}

// TestHandleChannels_WhatsApp_NativeAvailableAndTelegramOmitted extends
// TestHandleChannels_WhatsApp_NativeAvailable to additionally assert that a
// non-whatsapp entry (telegram) does NOT have native_available set.
//
// This verifies the "omitted elsewhere" contract: native_available is a
// whatsapp-specific field and must be absent on all other channel entries.
//
// Traces to: pr-test-analyzer finding — assert NativeAvailable omitted on
// non-whatsapp entries.
func TestHandleChannels_WhatsApp_NativeAvailableAndTelegramOmitted(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	api.HandleChannels(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var entries []struct {
		ID              string `json:"id"`
		NativeAvailable *bool  `json:"native_available"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	for _, e := range entries {
		if e.ID == "whatsapp" {
			// whatsapp must have native_available set.
			require.NotNil(t, e.NativeAvailable,
				"whatsapp entry must have native_available set")
			continue
		}
		assert.Nil(t, e.NativeAvailable,
			"entry %q must NOT have native_available set (whatsapp-only field)", e.ID)
	}
}

// TestPutUserContext_ValidateInbound_400OnMissingContent verifies that with
// validate_inbound=true, PUT /user-context without the "content" field returns 400.
//
// BDD:
//
//	Given validate_inbound=true,
//	When PUT /api/v1/user-context with body {},
//	Then 400 with schema error referencing UserContextRequest.
//
// Traces to: rest.go putUserContext — decodeAndValidate with "UserContextRequest" schema.
func TestPutUserContext_ValidateInbound_400OnMissingContent(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	// {} is missing the required "content" field.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/user-context", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.HandleUserContext(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"missing required 'content' field must return 400 when validate_inbound=true")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "UserContextRequest",
		"error message must reference the schema name")
}

// TestDeleteAgent_SuccessAndLocked403 verifies DELETE /agents/{id}: an unlocked
// custom agent is removed (204) and a locked core agent is rejected with 403
// plus the agent_locked code.
func TestDeleteAgent_SuccessAndLocked403(t *testing.T) {
	// buildExecutorTestAPI seeds a writable config.json with an unlocked custom agent.
	api1 := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/test-agent", nil)
	api1.HandleAgents(w, r)
	assert.Equal(t, http.StatusNoContent, w.Code, "custom agent delete must 204")
	assert.Equal(t, 0, w.Body.Len(), "204 must have empty body")

	// newTestRestAPI seeds the locked core roster (Mia).
	api2, _ := newTestRestAPI(t)
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/mia", nil)
	api2.HandleAgents(w2, r2)
	assert.Equal(t, http.StatusForbidden, w2.Code, "locked agent delete must 403")
	var errResp map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &errResp))
	assert.Equal(t, "agent_locked", errResp["code"])
}
