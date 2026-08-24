// O3 two-field model: the agent {model, provider} pair round-trips through
// create / get / update. Proves:
//  1. POST with model+provider persists both and echoes provider.
//  2. GET echoes the persisted provider.
//  3. PUT changing provider updates it and echoes the new value.
//  4. PUT with provider:"" clears it (default-provider resolution).

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

func postAgentProvider(t *testing.T, api *restAPI, body string) gen.Agent {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	return decodeAgentResp(t, w.Body.Bytes())
}

func getAgentResp(t *testing.T, api *restAPI, id string) gen.Agent {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+id, nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "get body: %s", w.Body.String())
	return decodeAgentResp(t, w.Body.Bytes())
}

func TestAgentProvider_CreateGetUpdateRoundTrip(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// 1. Create with model + provider.
	created := postAgentProvider(t, api,
		`{"name":"ProvAgent","type":"Main","soul":"s","model":"google/gemini-2.5-flash","provider":"openrouter"}`)
	require.NotNil(t, created.Model)
	assert.Equal(t, "google/gemini-2.5-flash", *created.Model)
	require.NotNil(t, created.Provider, "create response must echo provider")
	assert.Equal(t, "openrouter", *created.Provider)

	// Persisted under the agent's own entity record (ADR-054), not config.json.
	prov := findModelProviderInStore(t, api, created.Id)
	assert.Equal(t, "openrouter", prov, "provider must persist to the agent entity record")

	// 2. GET echoes it.
	got := getAgentResp(t, api, created.Id)
	require.NotNil(t, got.Provider)
	assert.Equal(t, "openrouter", *got.Provider)

	// 3. PUT a new provider.
	{
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.Id,
			strings.NewReader(`{"provider":"anthropic"}`))
		r.Header.Set("Content-Type", "application/json")
		api.HandleAgents(w, r)
		require.Equal(t, http.StatusOK, w.Code, "put body: %s", w.Body.String())
		updated := decodeAgentResp(t, w.Body.Bytes())
		require.NotNil(t, updated.Provider)
		assert.Equal(t, "anthropic", *updated.Provider)
	}
	assert.Equal(t, "anthropic", findModelProviderInStore(t, api, created.Id))

	// 4. PUT provider:"" clears it.
	{
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.Id,
			strings.NewReader(`{"provider":""}`))
		r.Header.Set("Content-Type", "application/json")
		api.HandleAgents(w, r)
		require.Equal(t, http.StatusOK, w.Code, "put body: %s", w.Body.String())
		updated := decodeAgentResp(t, w.Body.Bytes())
		assert.Nil(t, updated.Provider, "cleared provider must be absent on the wire")
	}
	// NOTE: against the retired config.json reader this assertion was VACUOUS —
	// that helper always returned "" once agents.list stopped being persisted,
	// so it passed whether or not the provider was actually cleared. Reading the
	// entity record makes it a real check again.
	assert.Equal(t, "", findModelProviderInStore(t, api, created.Id),
		"provider must be cleared from the agent entity record")
}

// findModelProviderInStore returns the agent's model.provider from its
// per-entity record (entities/agents/<id>.json), or "" when the agent, its
// model block, or the provider field is absent.
//
// ADR-054: this replaced a config.json reader. The agent roster no longer
// round-trips through config.json at all (config.AgentsConfig.List is
// `json:"-"`), so the old agents.list[*].model.provider lookup could only ever
// return "" — it did not fail loudly, it silently asserted against an empty
// list. The entity store is the authoritative persistence location now.
func findModelProviderInStore(t *testing.T, api *restAPI, id string) string {
	t.Helper()
	rec, err := agentstore.New(api.homePath).Get(id)
	if err != nil || rec == nil || rec.Model == nil {
		return ""
	}
	return rec.Model.Provider
}

func readProviderTestConfigMap(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// TestAgentPUT_HeartbeatFieldsIgnored proves ADR-027: heartbeat is workspace-scoped.
// A PUT with heartbeat_enabled/heartbeat_interval fields on the agent endpoint is
// silently accepted (the fields exist on AgentUpdateRequest for backward compat) but
// NOT persisted on the agent config (heartbeat lives in workspace member_configs).
// The response does NOT carry HeartbeatEnabled/HeartbeatInterval fields at all.
func TestAgentPUT_HeartbeatFieldsIgnored(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Create a Main agent.
	created := postAgentProvider(t, api, `{"name":"HBAgent","type":"Main","soul":"s"}`)

	// PUT with legacy heartbeat fields → must succeed (200), not 400.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.Id,
		strings.NewReader(`{"heartbeat_enabled":true,"heartbeat_interval":30}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT with heartbeat fields must succeed: %s", w.Body.String())

	// The legacy heartbeat fields must NOT bleed into the global heartbeat block.
	// (ADR-027: heartbeat is now workspace-scoped; per-agent config is decommissioned.)
	m := readProviderTestConfigMap(t, api)
	if hb, ok := m["heartbeat"].(map[string]any); ok {
		if iv, ok := hb["interval"]; ok {
			assert.NotEqualValues(t, 30, iv,
				"per-agent heartbeat interval must not bleed into the global heartbeat block")
		}
	}
}

// TestCreateAgent_WorkerVoiceFieldRejected proves the unconditional
// strict-decode enforcement for the Main-only voice field:
// AgentCreateRequestSubagent (and AgentCreateRequestSubagent3p) structurally
// have no `voice` property at all (additionalProperties: false — the field
// matrix marks voice "Main-only among user types"). With a DEFAULT config
// (ValidateInbound off, the default in this harness), a Subagent create
// carrying a `voice` key is now rejected 400 by createAgent's strict decode,
// not silently dropped. (With ValidateInbound enabled the same body 400s at
// the schema gate instead.) The PUT-time guard (a worker cannot be given a
// non-empty voice) is unaffected — see TestUpdateAgent_RejectsVoiceOnWorker
// in rest_agent_executor_test.go.
func TestCreateAgent_WorkerVoiceFieldRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"W","type":"Subagent","description":"d","soul":"s","voice":"alloy"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "voice")
	assert.Contains(t, w.Body.String(), "AgentCreateRequestSubagent")
}

// TestAgentProvider_NeedsModelDerived — ADR-068 T068-08 regression (spec
// "Existing tests" table): the FR-014 derivation must leave needs_model
// FALSE for every fixture shape this file already exercises — an agent with
// an explicit (model, provider) pair, and one whose provider was cleared
// (passthrough resolution) — and TRUE only for a genuinely model-less or
// provider-less agent. Wire sites under test: listAgents / getAgent /
// updateAgent (rest.go).
func TestAgentProvider_NeedsModelDerived(t *testing.T) {
	api := buildExecutorTestAPI(t)
	// Configure the provider the fixtures reference — ON DISK, not only
	// in-memory: every agent POST/PUT runs refreshConfigAndRewireServices,
	// which reloads config.json and swaps the live config, so an in-memory
	// Providers append would silently vanish on the first write below.
	//
	// updated_at is required here, not decorative: isSeedTemplateRow
	// (rest.go, ADR-067 FR-029) treats a row with no credential, no
	// endpoint, no model list AND no updated_at as an un-configured
	// fresh-install template — indistinguishable from a real row unless one
	// of those is set. Every real write path (PUT /providers/{id}, both its
	// found-and new-entry branches) always stamps updated_at unconditionally
	// (ADR-068 MAJ-015), so a hand-written fixture that skips it is
	// simulating a row no real write path produces, not "a configured
	// provider with no extra fields".
	updatedAt := time.Now().UTC()
	diskCfg := fmt.Sprintf(`{"version":%d,"agents":{"defaults":{"workspace":%q,`+
		`"default_model":{"provider":"openrouter","model":"z-ai/glm-5.2"},"max_tokens":4096},"list":[]},`+
		`"providers":[{"model_name":"openrouter","model":"openrouter/auto","provider":"openrouter","updated_at":%q}]}`,
		config.CurrentVersion, api.homePath, updatedAt.Format(time.RFC3339))
	require.NoError(t, os.WriteFile(api.configPath(), []byte(diskCfg), 0o600))
	// Mirror into the live config for reads that happen before the first write.
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Name: "openrouter", Provider: "openrouter", Model: "openrouter/auto", UpdatedAt: &updatedAt,
	})
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{
		Provider: "openrouter", Model: "z-ai/glm-5.2",
	}

	// Fixture 1 (round-trip shape): explicit model + configured provider.
	created := postAgentProvider(t, api,
		`{"name":"NMAgent","type":"Main","soul":"s","model":"google/gemini-2.5-flash","provider":"openrouter"}`)
	got := getAgentResp(t, api, created.Id)
	assert.False(t, got.NeedsModel, "explicit pair on a configured provider must not need a model")

	// Fixture 2 (round-trip step 4 shape): provider cleared → passthrough
	// resolution through the configured openrouter row. The slug deliberately
	// carries no "vendor/" prefix: NormalizeAgents' migrateAgentPrimaryProvider
	// splits a prefixed slug into an EXPLICIT provider at every config load, so
	// the empty-provider passthrough state only exists for unprefixed slugs.
	cleared := postAgentProvider(t, api,
		`{"name":"NMAgent2","type":"Main","soul":"s","model":"glm-5.2-air","provider":"openrouter"}`)
	{
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+cleared.Id,
			strings.NewReader(`{"provider":""}`))
		r.Header.Set("Content-Type", "application/json")
		api.HandleAgents(w, r)
		require.Equal(t, http.StatusOK, w.Code, "put body: %s", w.Body.String())
		updated := decodeAgentResp(t, w.Body.Bytes())
		assert.False(t, updated.NeedsModel,
			"cleared provider resolves via the passthrough row — not needs_model; body: %s", w.Body.String())
	}

	// Fixture 3 (harness seed): "test-agent" has no own model; the default
	// model resolves through the same passthrough row → false.
	for _, ag := range listAgentsResp(t, api) {
		assert.False(t, ag.NeedsModel,
			"needs_model must be false for every existing fixture (agent %s)", ag.Id)
	}

	// Counter-case: re-point the agent at a provider with no configured row →
	// needs_model true on the PUT response and on GET.
	{
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.Id,
			strings.NewReader(`{"provider":"groq"}`))
		r.Header.Set("Content-Type", "application/json")
		api.HandleAgents(w, r)
		require.Equal(t, http.StatusOK, w.Code, "put body: %s", w.Body.String())
		updated := decodeAgentResp(t, w.Body.Bytes())
		assert.True(t, updated.NeedsModel,
			"an unconfigured explicit provider is 'provider not configured' (FR-014); body: %s", w.Body.String())
	}
	assert.True(t, getAgentResp(t, api, created.Id).NeedsModel)
}

// listAgentsResp decodes GET /api/v1/agents.
func listAgentsResp(t *testing.T, api *restAPI) []gen.Agent {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "list body: %s", w.Body.String())
	var out []gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}
