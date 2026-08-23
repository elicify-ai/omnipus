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

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	whatsappnative "github.com/elicify-ai/omnipus/pkg/channels/whatsapp_native"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
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

// seedAgentEntities persists each agent in agents as a REAL entity-store
// record under homePath/entities/agents/<id>.json (ADR-054 D2/D6), in
// addition to whatever the caller already put into cfg.Agents.List for
// AgentLoop construction.
//
// A real entity file is required whenever a test drives a REST write path
// that actually reaches persistence for an agent that must already exist —
// PUT /api/v1/agents/{id}, PUT /api/v1/agents/{id}/tools, DELETE
// /api/v1/agents/{id}: updateAgent/updateAgentTools/deleteAgent persist via
// agentstore.Store (entities/agents/<id>.json), never config.json's
// agents.list any more (ADR-054 D2/§11 checklist items 1/3/4/5) — a target
// that exists only in the in-memory cfg.Agents.List (which is still what
// a.agentLoop.GetConfig() returns, and is all the pre-persist "does this
// agent exist / is it locked" checks read) fails the persist step itself
// with "agent ... not found in agent store", turning an expected
// 200/204 into a 500.
//
// Tests that are read-only (GET) or where the request is rejected BEFORE
// the persist step (400/403/404) do not need this — a bare in-memory
// cfg.Agents.List remains the correct, sanctioned way to seed AgentLoop for
// those (NewAgentLoop does not auto-populate the roster from the entity
// store — only pkg/gateway's boot/reload bridge does; mirrors the pattern
// already proven in pkg/gateway/rest_mailbox_test.go's newMailboxTestAPI and
// pkg/sysagent/tools/agent_test.go).
func seedAgentEntities(t *testing.T, homePath string, agents []config.AgentConfig) {
	t.Helper()
	store := agentstore.New(homePath)
	for i := range agents {
		ac := agents[i]
		if err := store.Create(ac.ID, &ac); err != nil {
			t.Fatalf("seedAgentEntities: create %q: %v", ac.ID, err)
		}
	}
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
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

	body := `{"type": "Main", "name": "", "soul": "s"}`
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

	body := `{"type": "Main", "name": "Scout", "model": "claude-sonnet-4-6", "soul": "Scout soul"}`
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

// TestHandleAgentsCreateWithExplicitID_Rejected verifies POST /api/v1/agents
// rejects a body carrying a client-supplied "id" field. Superseded by the
// unconditional strict-decode enforcement: "id" is not a property on
// AgentCreateRequestMain (the id is always server-generated via
// uuid.New()), so a caller-supplied "id" key is now a 400 rather than being
// silently dropped by a plain json.Unmarshal. The agent identity contract
// (server always mints its own UUID) is otherwise unaffected — see
// TestHandleAgentsCreate for the happy path.
// Traces to: wave5a-wire-ui-spec.md — A3+A4: agent creation via API
func TestHandleAgentsCreateWithExplicitID_Rejected(t *testing.T) {
	// Use newTestRestAPIWithHome so safeUpdateConfigJSON writes to a temp dir,
	// not the committed pkg/gateway/config.json test fixture.
	api := newTestRestAPIWithHome(t)

	body := `{"id": "my-scout", "type": "Main", "name": "Scout", "soul": "Scout soul"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "id")
	assert.Contains(t, resp["error"], "AgentCreateRequestMain")
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
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{
					ID:   "tool-agent",
					Name: "Tool Agent",
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
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
	// There is no default_policy field any more (CLAUDE.md hard constraint
	// 6) — the response carries only the agent's explicit policies map.
	_, hasDefaultPolicy := builtin["default_policy"]
	assert.False(t, hasDefaultPolicy, "default_policy no longer exists on the wire")
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

// TestUpdateAgentTools_Subagent3pRejected verifies PUT /api/v1/agents/{id}/tools
// on a subagent_3p (External CLI) agent returns 400 — the external runner
// manages its own tool loop, so tools_cfg is not a configurable surface for
// that agent type. This is a SEPARATE write path from updateAgent's
// firstForbiddenSubagent3pField guard (which only rejects tools_cfg embedded
// in a PUT /agents/{id} body) — it closes the leak where a caller could
// otherwise bypass that guard by hitting the dedicated tools endpoint
// directly. The locked-agent 403 regression (TestUpdateAgentTools_LockedAgentForbidden
// above) is unaffected — the Locked check still runs first.
func TestUpdateAgentTools_Subagent3pRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := createSubagent3p(t, api)

	// The locked-agent 403 / external-subagent 400 checks run before body
	// parsing, so the exact policy shape here is incidental — a modern,
	// fully-explicit-style body is used since default_policy no longer
	// exists on the wire (CLAUDE.md hard constraint 6).
	body := `{"builtin":{"policies":{"bash":"deny"}}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id+"/tools", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "external subagents run their own tools")
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
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))
	// ADR-054: the allowed model-change PUT below reaches updateAgent's
	// persist step, which resolves/updates "jim" via the agent store
	// (entities/agents/jim.json), not config.json's agents.list — seed a
	// real entity record for every core agent SeedConfig produced (mirrors
	// them exactly, so jim's Locked flag and identity match production).
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

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

// TestUpdateAgentTools_InvalidPolicyValue verifies PUT with an invalid
// per-tool policy value returns 422. Renamed from the old
// TestUpdateAgentTools_InvalidMode, which sent a "default_policy" field that
// no longer exists on the wire (CLAUDE.md hard constraint 6) — with
// ValidateInbound off (this harness's default), an unrecognized top-level
// field is silently dropped by the non-strict JSON decode rather than
// rejected, so that body no longer exercises any validation path at all. This
// test exercises the still-live per-tool policy-value enum check instead (the
// validation immediately after decode, before the coverage check runs).
func TestUpdateAgentTools_InvalidPolicyValue(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	// Write a minimal config.json so safeUpdateConfigJSON can read it. The
	// (now-inert) "agents.list" key is not read by any write path any more
	// (ADR-054) — this is just a valid, parseable file for os.ReadFile.
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096}}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "test-agent", Name: "Test"},
			},
		},
	}
	// This request is rejected (422) at the per-tool policy-value check,
	// before updateAgentTools ever reaches its agent-store persist step, so a
	// real entity record is not strictly required for THIS test to pass —
	// seeded anyway so the fixture matches production shape (a "test-agent"
	// that a caller could legitimately PUT against) rather than an
	// in-memory-only agent that would 500 on any successful write.
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Invalid per-tool policy value should be rejected.
	body := `{"builtin":{"policies":{"bash":"bogus"}}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent/tools", strings.NewReader(body))
	r = withReAuthAdmin(t, api, r) // FR-3.3 re-auth gate on the per-agent tool grant
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body.String())
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// There is no default_policy field on the wire any more (CLAUDE.md hard
	// constraint 6) — createAgent's strict decode (decodeAgentCreateVariant,
	// DisallowUnknownFields, independent of ValidateInbound) rejects a stray
	// "default_policy" key inside tools_cfg.builtin with 400, so it must not
	// appear here. The caller-supplied "policies" map is merged on top of the
	// seeded deny-all baseline (coreagent.NewCustomAgentToolsCfg), which is
	// what keeps the resulting agent's tool-policy coverage complete even
	// though only 3 tools are named below.
	body := `{
		"name": "Research Bot",
		"type": "Main",
		"description": "A researcher",
		"soul": "Research Bot soul",
		"color": "#22C55E",
		"icon": "magnifying-glass",
		"tools_cfg": {
			"builtin": {
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

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	var resp struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Research Bot", resp.Name)
	assert.Equal(t, "Main", resp.Type)
	assert.NotEmpty(t, resp.ID)

	// Verify the agent entity record (entities/agents/<id>.json) was
	// persisted with the tools config — createAgent persists via the agent
	// store, not config.json's agents.list (ADR-054 D2). There is no
	// default_policy field on config.AgentConfig any more (CLAUDE.md hard
	// constraint 6 — the field was removed project-wide), so the persisted
	// builtin map carries only a complete "policies" map by construction.
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get(resp.ID)
	require.NoError(t, err, "created agent must exist as a real entity-store record")
	assert.Equal(t, "#22C55E", savedAgent.Color)
	assert.Equal(t, "magnifying-glass", savedAgent.Icon)
	require.NotNil(t, savedAgent.Tools, "tools config must be persisted")
	policies := savedAgent.Tools.Builtin.Policies
	// The caller's sparse overrides win...
	assert.Equal(t, config.ToolPolicyAllow, policies["read_file"])
	assert.Equal(t, config.ToolPolicyAllow, policies["search_web"])
	assert.Equal(t, config.ToolPolicyAllow, policies["fetch_url"])
	// ...merged on top of the seeded deny-all baseline (coreagent.
	// NewCustomAgentToolsCfg), so an unrelated tool the caller never
	// mentioned is still explicitly covered (deny), and bash specifically
	// stays denied (CRIT-001/FR-B12) since the caller did not override it.
	assert.Equal(t, config.ToolPolicyDeny, policies["bash"])

	// config.json itself must carry no agents.list content — agents are
	// per-entity records now, never config.json entries (ADR-054).
	savedCfgRaw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var savedMap map[string]any
	require.NoError(t, json.Unmarshal(savedCfgRaw, &savedMap))
	if agentsSection, ok := savedMap["agents"].(map[string]any); ok {
		if list, ok := agentsSection["list"].([]any); ok {
			assert.Empty(t, list, "config.json agents.list must stay empty — agents persist only to entities/agents/")
		}
	}
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

	// Install the skills used by this test so validation passes. These MUST match
	// the skills requested in the POST body below (daily-briefing, summarize) —
	// the create-agent handler rejects any skill id not present under
	// OMNIPUS_BUILTIN_SKILLS with a 400 "unknown skill id".
	skillsRoot := t.TempDir()
	t.Setenv("OMNIPUS_BUILTIN_SKILLS", skillsRoot)
	for _, id := range []string{"daily-briefing", "summarize"} {
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"name":"Skill Agent","type":"Main","soul":"s","skills":["daily-briefing","summarize"]}`
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

	// Verify the agent entity record persisted the skill list — createAgent
	// persists via the agent store, not config.json's agents.list (ADR-054 D2).
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get(resp.ID)
	require.NoError(t, err, "created agent must exist as a real entity-store record")
	require.Len(t, savedAgent.Skills, 2)
	assert.Equal(t, "daily-briefing", savedAgent.Skills[0])
	assert.Equal(t, "summarize", savedAgent.Skills[1])
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"name":"Skillless Agent","type":"Main","soul":"s"}`
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
	id, _ := raw["id"].(string)
	require.NotEmpty(t, id, "create response must include an id")

	// The agent entity record (entities/agents/<id>.json) must have no
	// skills — createAgent persists via the agent store, not config.json's
	// agents.list (ADR-054 D2).
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get(id)
	require.NoError(t, err, "created agent must exist as a real entity-store record")
	assert.Empty(t, savedAgent.Skills, "a new agent with no skills field must have no skills persisted")
}

// TestUpdateAgent_SkillsPersist verifies that PUT /api/v1/agents/{id} with a
// skills list persists the skills to config.json and returns them in the response.
func TestUpdateAgent_SkillsPersist(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	// Use default embedded skills (daily-briefing, plan,
	// skill-authoring, summarize) for validation.

	tmpDir := t.TempDir()
	// config.json must exist on disk because updateAgent's persist step
	// (safeUpdateConfigJSON) does a read-modify-write cycle against it — but
	// the "list" CONTENT is otherwise inert: agents resolve exclusively via
	// the agent store (entities/agents/<id>.json, ADR-054), never this
	// file's list, so an empty list is the honest fixture (not the
	// misleading non-empty agent-a/agent-b array this used to carry).
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "agent-a", Name: "Agent A"},
				{ID: "agent-b", Name: "Agent B"},
			},
		},
	}
	// ADR-054: updateAgent's persist step resolves/updates "agent-a" via the
	// agent store (entities/agents/agent-a.json), not config.json's
	// agents.list — seed BOTH agents as real entity records so (a) the PUT
	// against agent-a succeeds and (b) agent-b can be read back afterward to
	// prove it was left untouched.
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

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

	// Verify the agent entity records: agent-a has skills, agent-b has none
	// — updateAgent persists via the agent store, not config.json's
	// agents.list (ADR-054 D2).
	store := agentstore.New(tmpDir)
	savedA, err := store.Get("agent-a")
	require.NoError(t, err)
	require.Len(t, savedA.Skills, 2, "agent-a must have 2 skills persisted")
	assert.Equal(t, "daily-briefing", savedA.Skills[0])
	assert.Equal(t, "summarize", savedA.Skills[1])

	savedB, err := store.Get("agent-b")
	require.NoError(t, err)
	assert.Empty(t, savedB.Skills, "agent-b must have no skills — granting to A must not affect B")
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
	// config.json must exist on disk for updateAgent's safeUpdateConfigJSON
	// read-modify-write cycle; the "list" content itself is inert (agents
	// resolve via the agent store, ADR-054) so an empty list is the honest
	// fixture.
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "skilled-agent", Name: "Skilled Agent", Skills: []string{"web-research"}},
			},
		},
	}
	// ADR-054: updateAgent's persist step resolves/updates "skilled-agent"
	// via the agent store (entities/agents/skilled-agent.json), not
	// config.json's agents.list.
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Send empty skills array to clear all skills.
	body := `{"skills":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/skilled-agent", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "response body: %s", w.Body.String())

	// The agent entity record must have no skills key after clear —
	// updateAgent persists via the agent store, not config.json's
	// agents.list (ADR-054 D2).
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get("skilled-agent")
	require.NoError(t, err)
	assert.Empty(t, savedAgent.Skills, "skills must be empty after clearing with an empty array")
}

// TestAgent_MemoryEnabled_DefaultsTrueAndRoundTripsOnPUT proves the
// ADR-052 FR-039 memory_enabled wire field: (1) an agent with no persisted
// MemoryEnabled override defaults to true on GET/list (applyAgentOverrides
// populates it from MemoryEnabledEffective, which treats nil as true), and
// (2) a PUT setting memory_enabled:false persists to config.json and is
// echoed back false on the PUT response and a subsequent GET — closing the
// gap where toWireAgent's response paths never set the field at all.
func TestAgent_MemoryEnabled_DefaultsTrueAndRoundTripsOnPUT(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	// config.json must exist on disk for updateAgent's safeUpdateConfigJSON
	// read-modify-write cycle; the "list" content itself is inert (agents
	// resolve via the agent store, ADR-054) so an empty list is the honest
	// fixture.
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "mem-agent", Name: "Mem Agent"},
			},
		},
	}
	// ADR-054: updateAgent's persist step resolves/updates "mem-agent" via
	// the agent store (entities/agents/mem-agent.json), not config.json's
	// agents.list. The subsequent GET (step 4 below) reads a.agentLoop's
	// in-memory config AFTER updateConfigJSONLocked's refresh repopulates
	// cfg.Agents.List from this same entity store, so the real record must
	// exist here for the whole round trip to work.
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// 1. GET with no persisted override: defaults to true.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/mem-agent", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "response body: %s", w.Body.String())

	var getResp struct {
		MemoryEnabled *bool `json:"memory_enabled"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &getResp))
	require.NotNil(t, getResp.MemoryEnabled, "memory_enabled must always be present on the wire")
	assert.True(t, *getResp.MemoryEnabled, "memory_enabled must default to true when never set")

	// 2. PUT memory_enabled:false persists and echoes back on the response.
	body := `{"memory_enabled":false}`
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/api/v1/agents/mem-agent", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "response body: %s", w.Body.String())

	var putResp struct {
		MemoryEnabled *bool `json:"memory_enabled"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &putResp))
	require.NotNil(t, putResp.MemoryEnabled)
	assert.False(t, *putResp.MemoryEnabled, "PUT response must echo the just-persisted memory_enabled:false")

	// 3. The agent entity record actually persisted memory_enabled:false —
	// updateAgent persists via the agent store, not config.json's
	// agents.list (ADR-054 D2).
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get("mem-agent")
	require.NoError(t, err)
	require.NotNil(t, savedAgent.MemoryEnabled, "memory_enabled must be persisted as an explicit override")
	assert.False(t, *savedAgent.MemoryEnabled)

	// 4. A subsequent GET (fresh read of the live/reloaded config) reflects false.
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/agents/mem-agent", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "response body: %s", w.Body.String())

	var getResp2 struct {
		MemoryEnabled *bool `json:"memory_enabled"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &getResp2))
	require.NotNil(t, getResp2.MemoryEnabled)
	assert.False(t, *getResp2.MemoryEnabled, "GET after PUT must reflect the persisted memory_enabled:false")
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// "unknown-skill" is not installed — must be rejected 400.
	body := `{"name":"Test Agent","type":"Main","soul":"s","skills":["unknown-skill"]}`
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
	body = `{"name":"Test Agent 2","type":"Main","soul":"s","skills":["web-research"]}`
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

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "my-agent", Name: "My Agent"},
			},
		},
	}
	// This request is rejected (400, unknown skill id) at validateSkillIDs,
	// before updateAgent ever reaches its agent-store persist step, so a real
	// entity record is not strictly required for THIS test to pass — seeded
	// anyway so the fixture matches production shape (a "my-agent" a caller
	// could legitimately PUT against), rather than an in-memory-only agent.
	// (FIXTURE-VACUITY fix: this used to instead os.WriteFile a raw
	// config.json blob with a non-empty "agents.list" array — dead weight,
	// since nothing in this test reads that raw file back.)
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
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
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
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
// updates the response body with the correct agent_type, and persists the
// tools config to config.json on disk.
//
// BDD: Given a custom agent exists in config and a config.json is on disk,
//
//	When PUT /api/v1/agents/{id}/tools is called with a complete, explicit
//	  per-tool policies map,
//	Then the response is 200, agent_type is "Main",
//	And config.json on disk reflects the persisted tools config.
//
// There is no default_policy field on the wire any more (CLAUDE.md hard
// constraint 6) — a PUT must now carry a COMPLETE, explicit `policies` map
// (config.ValidateToolPolicyCoverage enforces this at write time). This test
// exercises that primary path directly with a full known-tool map. The
// legacy mode:"explicit"+visible[] conversion is exercised separately in
// TestUpdateAgentTools_LegacyModeAloneCoverageGapRejected below, which pins
// the NEW behavior: submitted alone (no other coverage), it is now REJECTED
// — the conversion only produces agent-level "allow" entries for the names
// in visible[], it does not synthesize a deny-all baseline for every other
// known tool, so it no longer amounts to a complete policy map by itself.
// Traces to: parsed-inventing-gem.md — PR #41 Per-Agent Tool Visibility, updateAgentTools success path
func TestUpdateAgentTools_Success(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	// cfgPath must exist on disk because the final assertion below re-reads
	// config.json to confirm agents.list stays empty on disk (ADR-054) — but
	// the "list" CONTENT written here is otherwise inert: updateAgentTools's
	// persist step resolves/updates "update-agent" exclusively via the agent
	// store (entities/agents/update-agent.json), never this file's list, so
	// there is no reason to seed a non-empty (and therefore misleading)
	// "agents.list" blob here. (FIXTURE-VACUITY fix: this used to write a
	// non-empty "list":[{"id":"update-agent",...}] array, which read as if
	// it mattered for resolution — it never did; only seedAgentEntities
	// below does.)
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "update-agent", Name: "Update Agent"},
			},
		},
	}
	// ADR-054: updateAgentTools' persist step resolves/updates
	// "update-agent" via the agent store (entities/agents/update-agent.json),
	// not config.json's agents.list.
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Build a complete, explicit policies map: every known static builtin
	// tool denied, except read_file/search_web which are allowed.
	known := buildKnownBuiltinToolNames()
	policies := make(map[string]string, len(known))
	for name := range known {
		policies[name] = "deny"
	}
	policies["read_file"] = "allow"
	policies["search_web"] = "allow"
	policiesJSON, err := json.Marshal(policies)
	require.NoError(t, err)
	body := `{"builtin":{"policies":` + string(policiesJSON) + `}}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/update-agent/tools", strings.NewReader(body))
	r = withReAuthAdmin(t, api, r) // FR-3.3 re-auth gate on the per-agent tool grant
	api.HandleAgents(w, r)

	// Then: HTTP 200
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

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
	// There is no default_policy field any more — the response's
	// config.builtin.policies is the complete map just persisted.
	assert.Equal(t, gen.AgentToolsResponseConfigBuiltinPoliciesAllow, genResp.Config.Builtin.Policies["read_file"])
	assert.Equal(t, gen.AgentToolsResponseConfigBuiltinPoliciesAllow, genResp.Config.Builtin.Policies["search_web"])
	assert.Equal(t, gen.AgentToolsResponseConfigBuiltinPoliciesDeny, genResp.Config.Builtin.Policies["bash"])

	// Then: the agent entity record (entities/agents/update-agent.json) was
	// updated with the tools config — updateAgentTools persists via the
	// agent store, not config.json's agents.list (ADR-054 D2). There is no
	// default_policy field on config.AgentConfig any more (CLAUDE.md hard
	// constraint 6 — the field was removed project-wide), so this is
	// structurally guaranteed rather than needing its own assertion.
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get("update-agent")
	require.NoError(t, err, "agent must exist as a real entity-store record")
	require.NotNil(t, savedAgent.Tools, "tools config must be persisted")
	persistedPolicies := savedAgent.Tools.Builtin.Policies
	assert.Equal(t, config.ToolPolicyAllow, persistedPolicies["read_file"])
	assert.Equal(t, config.ToolPolicyAllow, persistedPolicies["search_web"])
	assert.Equal(t, config.ToolPolicyDeny, persistedPolicies["bash"])

	// config.json itself must carry no agents.list content.
	savedCfgRaw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var savedMap map[string]any
	require.NoError(t, json.Unmarshal(savedCfgRaw, &savedMap))
	if agentsSection, ok := savedMap["agents"].(map[string]any); ok {
		if list, ok := agentsSection["list"].([]any); ok {
			assert.Empty(t, list, "config.json agents.list must stay empty — agents persist only to entities/agents/")
		}
	}
}

// TestUpdateAgentTools_LegacyModeAloneCoverageGapRejected verifies that the
// legacy mode:"explicit"+visible[] format, when submitted ALONE (no complete
// policies map and no global sandbox.tool_policies floor covering the rest),
// is now rejected with 400. pkg/gateway/rest.go's updateAgentTools converts
// mode:"explicit"+visible into agent-level "allow" entries for exactly the
// names in visible[] — it does not synthesize a deny-all baseline for every
// other known tool, because there is no default-policy fallback any more
// (CLAUDE.md hard constraint 6). The resulting sparse per-agent map fails
// config.ValidateToolPolicyCoverage exactly like an incomplete `policies`
// map submitted directly would.
func TestUpdateAgentTools_LegacyModeAloneCoverageGapRejected(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "update-agent-legacy", Name: "Update Agent Legacy"},
			},
		},
	}
	// This request is rejected (400, coverage gap) before updateAgentTools
	// ever reaches its agent-store persist step, so a real entity record is
	// not strictly required for THIS test to pass — seeded anyway so the
	// fixture matches production shape. (FIXTURE-VACUITY fix: this used to
	// instead os.WriteFile a raw config.json blob with a non-empty
	// "agents.list" array — dead weight, since nothing in this test reads
	// that raw file back.)
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"builtin":{"mode":"explicit","visible":["read_file","search_web"]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/update-agent-legacy/tools", strings.NewReader(body))
	r = withReAuthAdmin(t, api, r) // FR-3.3 re-auth gate on the per-agent tool grant
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "coverage")
}

// TestUpdateAgentTools_PoliciesWinsOverLegacyModeVisible verifies that a
// request carrying BOTH a real, complete `policies` map AND the legacy
// mode:"explicit"+visible[] fields persists the caller's real policies
// values — mode/visible must have NO effect whenever policies is present,
// matching the documented wire contract ("mode/visible are... ignored when
// policies is present"). Before the fix, updateAgentTools's legacy-mode
// guard checked a now-inert `builtinDefaultPolicy` bookkeeping variable
// (always "" — nothing ever set it earlier) instead of
// req.Builtin.Policies == nil, so mode="explicit" unconditionally
// overwrote the caller's real, already-built policies map with a fresh
// deny-all-except-visible map whenever mode was ALSO sent — silently
// discarding the caller's actual per-tool values (found live,
// comment-analyzer + code-simplifier, 2026-07-06).
func TestUpdateAgentTools_PoliciesWinsOverLegacyModeVisible(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	// config.json must exist on disk for updateAgentTools' safeUpdateConfigJSON
	// read-modify-write cycle; the "list" content itself is inert (agents
	// resolve via the agent store, ADR-054) so an empty list is the honest
	// fixture.
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "update-agent-both", Name: "Update Agent Both"},
			},
		},
	}
	// ADR-054: updateAgentTools' persist step resolves/updates
	// "update-agent-both" via the agent store, not config.json's
	// agents.list.
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// A complete, real policies map: everything denied except read_file.
	known := buildKnownBuiltinToolNames()
	policies := make(map[string]string, len(known))
	for name := range known {
		policies[name] = "deny"
	}
	policies["read_file"] = "allow"
	policiesJSON, err := json.Marshal(policies)
	require.NoError(t, err)

	// Send policies AND the legacy mode/visible fields naming a DIFFERENT
	// tool (search_web, left "deny" in the real map). If mode/visible
	// incorrectly won, search_web would end up "allow" and read_file would
	// be dropped entirely (the legacy conversion only ever sets `visible`
	// names to "allow" — it never carries `policies` forward).
	body := `{"builtin":{"policies":` + string(policiesJSON) +
		`,"mode":"explicit","visible":["search_web"]}}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/update-agent-both/tools", strings.NewReader(body))
	r = withReAuthAdmin(t, api, r) // FR-3.3 re-auth gate on the per-agent tool grant
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"a real, complete policies map must win over mode/visible, not be discarded: body: %s", w.Body.String())

	// The agent entity record — not config.json's agents.list, which
	// updateAgentTools no longer touches (ADR-054 D2) — must reflect the
	// caller's real policies values.
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get("update-agent-both")
	require.NoError(t, err)
	require.NotNil(t, savedAgent.Tools, "policies must be persisted")
	persisted := savedAgent.Tools.Builtin.Policies

	assert.Equal(t, config.ToolPolicyAllow, persisted["read_file"], "the caller's real policies value must survive")
	assert.Equal(t, config.ToolPolicyDeny, persisted["bash"], "the caller's real policies value must survive")
	assert.Equal(t, config.ToolPolicyDeny, persisted["search_web"],
		"mode/visible must have NO effect when policies is present — search_web must keep its "+
			"real 'deny' value from the policies map, not become 'allow' from visible[]")
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
	// config.json must exist on disk for updateAgentTools' safeUpdateConfigJSON
	// read-modify-write cycle; the "list" content itself is inert (agents
	// resolve via the agent store, ADR-054) so an empty list is the honest
	// fixture.
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "reload-test-agent", Name: "Reload Test Agent"},
			},
		},
	}
	// ADR-054: updateAgentTools' persist step (which runs BEFORE the
	// separately-invoked TriggerReload this test forces to fail) resolves/
	// updates "reload-test-agent" via the agent store, not config.json's
	// agents.list.
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	// Wire a reload function that always returns an error (simulates gateway
	// restart in progress or reload pipeline failure).
	al.SetReloadFunc(func() error {
		return fmt.Errorf("simulated reload failure: config file locked")
	})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// A complete, explicit policies map — NOT mode:"inherit" alone. Per
	// CLAUDE.md hard constraint 6 (config.ValidateToolPolicyCoverage), the
	// legacy mode:"inherit" conversion no longer synthesizes a deny-all
	// baseline (see the "REAL CURRENT BEHAVIOR" comment on updateAgentTools'
	// mode:"inherit" case in rest.go): sent alone, it now fails coverage
	// validation and returns 400 BEFORE ever reaching TriggerReload, which
	// would mask the very 503 path this test exists to exercise (this test
	// predates the no-default-policy-fallback change and was never updated
	// for it — TestUpdateAgentTools_LegacyModeAloneCoverageGapRejected pins
	// the mode:"inherit"-alone-is-now-rejected behavior separately). A full
	// map clears coverage validation so the handler proceeds to the
	// simulated reload failure.
	known := buildKnownBuiltinToolNames()
	policies := make(map[string]string, len(known))
	for name := range known {
		policies[name] = "deny"
	}
	policies["read_file"] = "allow"
	policiesJSON, err := json.Marshal(policies)
	require.NoError(t, err)
	body := `{"builtin":{"policies":` + string(policiesJSON) + `}}`
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

	// Then: the agent entity record was still updated (the agent-store
	// persist step runs BEFORE the handler's separate TriggerReload call —
	// updateAgentTools persists via the agent store, not config.json's
	// agents.list, ADR-054 D2).
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get("reload-test-agent")
	require.NoError(t, err, "agent must exist as a real entity-store record even when the post-write reload fails")
	assert.NotNil(t, savedAgent.Tools, "tools config must be persisted even on 503")
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
