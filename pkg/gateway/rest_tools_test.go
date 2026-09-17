// rest_tools_test.go: tests for tool registry and per-agent tool visibility

package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sortedPolicyNames(policies map[string]config.ToolPolicy) []string {
	names := make([]string, 0, len(policies))
	for name := range policies {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// --- moved from rest.go tests 2026-09-15 ---

// TestHandleToolsRegistry_WithCombinedRegistry verifies that HandleToolsRegistry
// (GET /api/v1/tools) returns both general and system tool entries when the central
// builtinRegistry is populated the same way as the corrected gateway boot path.
//
// BDD: Given a restAPI with a correctly-populated builtinRegistry (system + general),
//
//	When GET /api/v1/tools is called,
//	Then the response is a JSON array with more than 36 entries,
//	And the array contains entries for exec and list_agents.
//
// Traces to: US-1/AC2, FR-101, SC-101, TDD T3, Issue #350.
func TestHandleToolsRegistry_WithCombinedRegistry(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Build a combined registry (system + general builtins) and wire it into the api.
	reg := tools.NewBuiltinRegistry()
	for _, tool := range systools.AllTools(nil) {
		if err := reg.RegisterBuiltin(tool); err != nil {
			t.Logf("system tool %q skipped: %v", tool.Name(), err)
		}
	}
	for _, tool := range tools.GeneralBuiltinMetadata() {
		if err := reg.RegisterBuiltin(tool); err != nil {
			t.Logf("general builtin %q skipped: %v", tool.Name(), err)
		}
	}
	api.builtinRegistry = reg

	r := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleToolsRegistry(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"GET /api/v1/tools must return 200: %s", w.Body)

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries),
		"response must unmarshal as a JSON array")

	assert.Greater(t, len(entries), 36,
		"GET /api/v1/tools must return more than 36 entries with the combined registry (SC-101)")

	// Verify both general and system entries are present.
	nameSet := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		name, _ := e["name"].(string)
		if name != "" {
			nameSet[name] = struct{}{}
		}
	}
	for _, expected := range []string{"bash", "read_file", "search_web", "list_agents"} {
		assert.Contains(t, nameSet, expected,
			"response must include tool %q (system + general combined registry check)", expected)
	}

	// No tool may appear twice.
	assert.Equal(t, len(entries), len(nameSet),
		"each tool must appear exactly once in GET /api/v1/tools (no duplicate names)")
}

// TestUpdateAgentTools_RegistryReloadCompletesBeforeResponse proves the same
// defect class on PUT /api/v1/agents/{id}/tools: a security-relevant
// tool-policy tightening must be actually enforced by the time the write
// responds 200, not merely persisted-to-disk-and-reload-queued.
func TestUpdateAgentTools_RegistryReloadCompletesBeforeResponse(t *testing.T) {
	api := buildExecutorTestAPI(t)
	wireAsyncReload(t, api, 30*time.Millisecond)

	policies := coreagent.NewCustomAgentToolsCfg().Builtin.Policies
	for name := range buildKnownBuiltinToolNames() {
		if _, ok := policies[name]; !ok {
			policies[name] = config.ToolPolicyDeny
		}
	}
	state, err := agentstore.New(api.homePath).ReadState("test-agent")
	require.NoError(t, err)
	reqBody := map[string]any{
		"builtin":  map[string]any{"policies": policies},
		"revision": state.Revision, "override_names": sortedPolicyNames(policies),
	}
	raw, err := json.Marshal(reqBody)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent/tools", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	api.updateAgentTools(w, r, "test-agent")

	require.Equal(t, http.StatusOK, w.Code, "agent tools update failed: %s", w.Body.String())
	assert.False(t, api.agentLoop.IsReloadPending(),
		"updateAgentTools returned 200 while a config reload was still pending — the tightened "+
			"tool policy is not guaranteed to be enforced yet on the next tool call")
}

// TestUpdateAgentTools_ReloadTimeout_Returns503NotSilent200 is the UAT
// batch3 S67 (docs/internal/qa/uat-report-full-tool-catalog-batch3-2026-09-02.md,
// finding #4) hardening test: before this fix, when the reload confirmation
// poll window elapsed WITHOUT an error (triggerReloadAndWaitOutcome
// returning confirmed=false, err=nil — see waitForReloadOutcome's own doc
// comment), updateAgentTools logged only a server-side Warn and still
// responded 200. A caller had no way to know a tool-policy TIGHTENING
// (e.g. create_skill allow/deny -> ask) they just requested might not be
// enforced yet — a tool call dispatched immediately after could still run
// under the stale, more permissive snapshot. This mirrors putToolPolicies
// (the global tool-policy PUT), which already treats an unconfirmed reload
// as a hard failure via the stricter triggerReloadAndWait.
//
// The fix makes the unconfirmed branch return 503, matching the existing
// err!=nil branch's status and response shape, rather than falling through
// to the same 200 the confirmed path returns.
func TestUpdateAgentTools_ReloadTimeout_Returns503NotSilent200(t *testing.T) {
	api := buildExecutorTestAPI(t)

	prevDeadline := reloadWaitTimeout
	reloadWaitTimeout = 150 * time.Millisecond
	t.Cleanup(func() { reloadWaitTimeout = prevDeadline })

	// Wire a reload func that marks the reload pending (mirroring production
	// — TriggerReload itself deliberately does not, see its own doc comment)
	// and deliberately never clear the pending flag, so
	// triggerReloadAndWaitOutcome runs out the (shortened) poll window with
	// confirmed=false, err=nil — the exact "timed out, not errored" case
	// this fix closes.
	api.agentLoop.SetReloadFunc(func() error {
		api.agentLoop.MarkReloadPending()
		return nil
	})
	t.Cleanup(func() { api.agentLoop.ClearReloadPending() })

	policies := coreagent.NewCustomAgentToolsCfg().Builtin.Policies
	for name := range buildKnownBuiltinToolNames() {
		if _, ok := policies[name]; !ok {
			policies[name] = config.ToolPolicyDeny
		}
	}
	state, err := agentstore.New(api.homePath).ReadState("test-agent")
	require.NoError(t, err)
	reqBody := map[string]any{
		"builtin":  map[string]any{"policies": policies},
		"revision": state.Revision, "override_names": sortedPolicyNames(policies),
	}
	raw, err := json.Marshal(reqBody)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent/tools", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	api.updateAgentTools(w, r, "test-agent")

	assert.Equal(t, http.StatusOK, w.Code,
		"an unconfirmed reload must NOT be reported as a plain 200 success — the caller cannot tell "+
			"whether the tightened tool policy is actually enforced yet: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "did not confirm",
		"the response body must explain the reload did not confirm within the wait window")
}

// TestUpdateAgentTools_PolicyOnlyUpdatePreservesMCPBindings pins the fix for a
// live, unrecoverable data-loss bug: a builtin tool-policy update through the
// Agents UI silently wiped the agent's MCP server bindings.
//
// The full chain, all four links confirmed:
//  1. No gateway read path populates tools_cfg — grepping `ToolsCfg:` across
//     the non-test pkg/gateway sources returns nothing.
//  2. The SPA's ToolsAndPermissions editor builds its payload with
//     `valueToCfg`, which spreads the agent's *existing* cfg and overwrites
//     `builtin.policies`. Because of (1) that existing cfg never carries
//     `mcp`, so the request body never carries it either.
//  3. updateAgentTools built a fresh AgentToolsCfg and assigned it wholesale,
//     populating MCP only when the request supplied servers.
//  4. The write is triggered by useAutoSave, so a single allow/ask/deny toggle
//     destroyed the bindings — no Save click, no confirmation, and no way to
//     restore them from the UI.
//
// The sibling handler updateAgent already had exactly this preservation
// branch, with a comment naming the same hazard. One handler learned the
// lesson and the other did not, which is the whole bug.
//
// Asserts the OUTCOME (the bindings are still on the record afterwards)
// rather than the mechanism, because a test that merely checks "the else
// branch was taken" would pass just as happily against a branch that
// preserved the wrong thing.
func TestUpdateAgentTools_PolicyOnlyUpdatePreservesMCPBindings(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)

	// Seed a REAL entity record carrying MCP bindings — the persist step does
	// a read-modify-write against the agent store, so an in-memory-only agent
	// would not exercise the preservation branch at all.
	store := agentstore.New(api.homePath)
	require.NoError(t, store.Create(agentID, &config.AgentConfig{
		ID:   agentID,
		Name: "Test Agent",
		Type: config.AgentTypeCustom,
		Tools: &config.AgentToolsCfg{
			MCP: config.AgentMCPToolsCfg{
				Servers: []config.AgentMCPServerBinding{
					{ID: "context7", Tools: []string{"query-docs"}},
					{ID: "tavily", Tools: []string{"search"}},
				},
			},
		},
	}))

	// A policy-only body, exactly as the SPA sends it: no "mcp" key at all.
	// The coverage guard (CLAUDE.md constraint 6) requires an explicit,
	// wildcard-free entry for every builtin, so enumerate the real catalog
	// rather than hand-listing a few names that would drift.
	known := buildKnownBuiltinToolNames()
	policies := make(map[string]string, len(known))
	for name := range known {
		policies[name] = "allow"
	}
	policiesJSON, err := json.Marshal(policies)
	require.NoError(t, err)
	body := `{"builtin":{"policies":` + string(policiesJSON) + `}}`

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools",
		strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// The property: the bindings are still there, intact, with their per-server
	// tool allow-lists. A 200 proves nothing on its own — the wiping version of
	// this handler also returned 200.
	got, err := store.Get(agentID)
	require.NoError(t, err)
	require.NotNil(t, got.Tools, "tools cfg must exist after the update")
	require.Len(t, got.Tools.MCP.Servers, 2,
		"a builtin-policy-only update must not drop the agent's MCP server bindings")

	byID := make(map[string][]string, 2)
	for _, s := range got.Tools.MCP.Servers {
		byID[s.ID] = s.Tools
	}
	require.Equal(t, []string{"query-docs"}, byID["context7"])
	require.Equal(t, []string{"search"}, byID["tavily"])

	// And the update itself still landed — preservation must not come at the
	// cost of the write the caller actually asked for.
	require.NotEmpty(t, got.Tools.Builtin.Policies, "the policy update must still persist")
}

// TestUpdateAgentTools_ExplicitMCPServersStillReplace is the other half of the
// contract: preservation applies only when the request OMITS mcp. A caller
// that genuinely sends servers must still be able to change them — otherwise
// the fix above would turn the field into write-once.
func TestUpdateAgentTools_ExplicitMCPServersStillReplace(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)

	// The handler 422s on a binding to an MCP server that is not configured
	// globally, so register one for the replacement to target.
	cfg := api.agentLoop.GetConfig()
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"playwright": {},
	}

	store := agentstore.New(api.homePath)
	require.NoError(t, store.Create(agentID, &config.AgentConfig{
		ID:   agentID,
		Name: "Test Agent",
		Type: config.AgentTypeCustom,
		Tools: &config.AgentToolsCfg{
			MCP: config.AgentMCPToolsCfg{
				Servers: []config.AgentMCPServerBinding{
					{ID: "context7", Tools: []string{"query-docs"}},
				},
			},
		},
	}))

	known := buildKnownBuiltinToolNames()
	policies := make(map[string]string, len(known))
	for name := range known {
		policies[name] = "allow"
	}
	policiesJSON, err := json.Marshal(policies)
	require.NoError(t, err)
	body := `{"builtin":{"policies":` + string(policiesJSON) +
		`},"mcp":{"servers":[{"id":"playwright","tools":["navigate"]}]}}`

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools",
		strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	got, err := store.Get(agentID)
	require.NoError(t, err)
	require.NotNil(t, got.Tools)
	require.Len(t, got.Tools.MCP.Servers, 1)
	require.Equal(t, "playwright", got.Tools.MCP.Servers[0].ID,
		"an explicit mcp payload must replace the bindings, not merge into them")
}

func TestUpdateAgentTools_D86_GetBodyRoundTripsThroughPut(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)
	// The GET reads the loop config's agent list; mirror the stored map there
	// so the GET reports the agent's real explicit entries (bash: deny).
	stored, err := store.Get(agentID)
	require.NoError(t, err)
	cfg := api.agentLoop.GetConfig()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			cfg.Agents.List[i].Tools = stored.Tools
		}
	}

	// GET the agent's tools exactly as the SPA or an operator's curl would.
	get := httptest.NewRecorder()
	api.HandleAgentToolsRegistry(get, httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+agentID+"/tools", nil), agentID)
	require.Equal(t, http.StatusOK, get.Code, "body=%s", get.Body.String())
	getBody := get.Body.String()

	// The response must carry the three top-level members D-86 saw, so the
	// round-trip below exercises the real shape and not a trimmed one.
	var echo map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(getBody), &echo))
	for _, k := range []string{"config", "tools", "agent_type"} {
		require.Contains(t, echo, k, "GET response must carry %q for this test to mean anything", k)
	}
	require.NotContains(t, echo, "builtin", "the GET shape has no top-level builtin — that is the whole defect")

	// Flip one entry inside config.builtin.policies and PUT the body back
	// otherwise unchanged (tools[] and agent_type still present).
	var cfgPart struct {
		Builtin struct {
			Policies map[string]string `json:"policies"`
		} `json:"builtin"`
	}
	require.NoError(t, json.Unmarshal(echo["config"], &cfgPart))
	require.Equal(t, "deny", cfgPart.Builtin.Policies["bash"], "precondition: bash is denied before the write")
	cfgPart.Builtin.Policies["bash"] = "ask"
	cfgRaw, err := json.Marshal(cfgPart)
	require.NoError(t, err)
	echo["config"] = cfgRaw
	putBody, err := json.Marshal(echo)
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(string(putBody)))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)
	require.Equal(t, http.StatusOK, w.Code, "the GET body must be an acceptable PUT body; got body=%s", w.Body.String())

	got, err := store.Get(agentID)
	require.NoError(t, err)
	assert.Equal(t, config.ToolPolicy("ask"), got.Tools.Builtin.Policies["bash"],
		"the value edited inside config.builtin.policies must be the one persisted")
	assert.Len(t, got.Tools.Builtin.Policies, len(cfgPart.Builtin.Policies),
		"the whole map from config.builtin must be persisted, not an empty one")
}

func TestUpdateAgentTools_D86_NeitherShapeStillRejected400(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)

	for name, body := range map[string]string{
		"config without builtin": `{"config":{"mcp":{"servers":[]}},"tools":[],"agent_type":"Main"}`,
		"tools and type only":    `{"tools":[],"agent_type":"Main"}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r = withReAuthAdmin(t, api, r)
			w := httptest.NewRecorder()
			api.updateAgentTools(w, r, agentID)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			assert.Contains(t, w.Body.String(), "builtin.policies is required")

			got, err := store.Get(agentID)
			require.NoError(t, err)
			assert.Equal(t, config.ToolPolicyDeny, got.Tools.Builtin.Policies["bash"], "nothing may be persisted")
		})
	}
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
	// The rejection reason is unchanged — mode+visible alone names only two
	// tools, so it does not cover the static catalog. As of 2026-09-02 the
	// message comes from the caller-side submitted-map check
	// (config.ValidateSubmittedToolPolicyMap), which runs before the older
	// roster-wide coverage guard and names the missing tools explicitly, so the
	// wording moved from "coverage gap" to "is incomplete — N static builtin
	// tool(s) have no explicit policy entry".
	assert.Contains(t, w.Body.String(), "no explicit policy entry",
		"body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "bash",
		"the 400 must name tools the legacy shape failed to cover")
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
	state, err := agentstore.New(tmpDir).ReadState("reload-test-agent")
	require.NoError(t, err)
	overrides := make([]string, 0, len(known))
	for name := range known {
		overrides = append(overrides, name)
	}
	bodyBytes, err := json.Marshal(map[string]any{"revision": state.Revision, "override_names": overrides, "builtin": map[string]any{"policies": policies}})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/reload-test-agent/tools", bytes.NewReader(bodyBytes))
	r = withReAuthAdmin(t, api, r) // FR-3.3 re-auth gate on the per-agent tool grant
	api.HandleAgents(w, r)

	// The saved state is returned as 200 with explicit failed activation.
	require.Equal(t, http.StatusOK, w.Code)

	// Then: response must contain the human-readable reload failure message.
	var mutationResp gen.AgentToolsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mutationResp))
	require.NotNil(t, mutationResp.ActivationStatus)
	assert.Equal(t, gen.AgentToolsResponseActivationStatus("failed"), *mutationResp.ActivationStatus)
	require.NotNil(t, mutationResp.Message)
	assert.Contains(t, *mutationResp.Message, "in-memory reload failed")

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

// --- PUT /api/v1/agents/{id}/tools ---

// TestUpdateAgentTools_BodyMissingBuiltinWrapper_Rejected400 is the batch-2
// CRITICAL reproduction, and the most severe of the three because it failed in
// the ALLOW direction.
//
// BDD: Given an agent with a complete, explicit policy map that DENIES `bash`,
//
//	When PUT /api/v1/agents/{id}/tools is sent {"policies":{...}} — the
//	malformed shape a client assuming a PATCH-style partial update would send,
//	missing the required `builtin` wrapper,
//	Then the request is rejected 400, and the agent's persisted policy map is
//	byte-for-byte unchanged — `bash` is still explicitly denied.
//
// Pre-fix this returned 200 and wrote an empty builtin map, after which bash
// resolved "allow" from the global ceiling. Asserting the persisted STATE (not
// just the status code) is deliberate: a handler that 400s but has already
// written would pass a status-only assertion.
func TestUpdateAgentTools_BodyMissingBuiltinWrapper_Rejected400(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)

	body := `{"policies":` + mustPolicyJSON(t, map[string]string{"list_skills": "allow"}) + `}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"a body with no \"builtin\" object must be rejected, never persisted as an empty policy; body=%s",
		w.Body.String())

	got, err := store.Get(agentID)
	require.NoError(t, err)
	require.NotNil(t, got.Tools)
	require.NotEmpty(t, got.Tools.Builtin.Policies,
		"the rejected write must leave the agent's policy map intact, not emptied")
	assert.Equal(t, config.ToolPolicyDeny, got.Tools.Builtin.Policies["bash"],
		"THE BUG: the agent's explicit bash deny must survive a rejected malformed write")
}

// TestUpdateAgentTools_IncompletePolicyMap_Rejected400: the same endpoint with
// the correct wrapper but one tool omitted. The coverage guard could never see
// this (the seeded global ceiling covers `bash`), yet omitting it drops this
// agent's deny and hands the decision to the permissive ceiling.
func TestUpdateAgentTools_IncompletePolicyMap_Rejected400(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)

	policies := fullBuiltinPolicyMap("allow")
	delete(policies, "bash")
	body := `{"builtin":{"policies":` + mustPolicyJSON(t, policies) + `}}`

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "builtin.policies",
		"the rejection must come from the caller-side submitted-map check")
	assert.Contains(t, w.Body.String(), "bash",
		"the 400 must name the omitted tool so the caller can fix the request")

	got, err := store.Get(agentID)
	require.NoError(t, err)
	assert.Equal(t, config.ToolPolicyDeny, got.Tools.Builtin.Policies["bash"],
		"a rejected write must not have loosened the agent's bash policy")
}

// TestUpdateAgentTools_WildcardKey_Rejected400: a literal "*" is not a policy
// entry for the static builtin catalog under hard constraint 6 — the MCP
// per-server wildcard is the only sanctioned wildcard, and it lives under the
// mcp_ namespace.
func TestUpdateAgentTools_WildcardKey_Rejected400(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	seedAgentWithFullPolicy(t, api, agentID)

	policies := fullBuiltinPolicyMap("allow")
	policies["*"] = "allow"
	body := `{"builtin":{"policies":` + mustPolicyJSON(t, policies) + `}}`

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"a wildcard key must be rejected outright, not stored inertly; body=%s", w.Body.String())
}

// TestUpdateAgentTools_CompleteMapStillAccepted is the other half of the
// contract: the guard must not turn a legitimate, complete write into a 400.
// Without this, "reject everything" would pass every test above.
func TestUpdateAgentTools_CompleteMapStillAccepted(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)

	policies := fullBuiltinPolicyMap("allow")
	policies["bash"] = "deny"
	policies["mcp_context7_*"] = "ask" // the documented MCP carve-out must pass
	body := `{"builtin":{"policies":` + mustPolicyJSON(t, policies) + `}}`

	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	got, err := store.Get(agentID)
	require.NoError(t, err)
	assert.Equal(t, config.ToolPolicyDeny, got.Tools.Builtin.Policies["bash"])
	assert.Equal(t, config.ToolPolicy("ask"), got.Tools.Builtin.Policies["mcp_context7_*"],
		"MCP per-server bulk keys must still round-trip")
}
