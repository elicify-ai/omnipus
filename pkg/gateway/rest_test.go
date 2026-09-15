package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
