//go:build !cgo

// O3 two-field model: the agent {model, provider} pair round-trips through
// create / get / update. Proves:
//  1. POST with model+provider persists both and echoes provider.
//  2. GET echoes the persisted provider.
//  3. PUT changing provider updates it and echoes the new value.
//  4. PUT with provider:"" clears it (default-provider resolution).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
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
		`{"name":"ProvAgent","soul":"s","model":"google/gemini-2.5-flash","provider":"openrouter"}`)
	require.NotNil(t, created.Model)
	assert.Equal(t, "google/gemini-2.5-flash", *created.Model)
	require.NotNil(t, created.Provider, "create response must echo provider")
	assert.Equal(t, "openrouter", *created.Provider)

	// Persisted under agents.list[*].model.provider.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	prov := findModelProviderInConfig(t, m, created.Id)
	assert.Equal(t, "openrouter", prov, "provider must persist to config.json")

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
	assert.Equal(t, "anthropic", findModelProviderInConfig(t, readProviderTestConfigMap(t, api), created.Id))

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
	assert.Equal(t, "", findModelProviderInConfig(t, readProviderTestConfigMap(t, api), created.Id),
		"provider must be cleared from config.json")
}

// findModelProviderInConfig returns the agents.list[id].model.provider string, or
// "" when absent.
func findModelProviderInConfig(t *testing.T, m map[string]any, id string) string {
	t.Helper()
	agents, _ := m["agents"].(map[string]any)
	list, _ := agents["list"].([]any)
	for _, e := range list {
		entry, _ := e.(map[string]any)
		if entry == nil || entry["id"] != id {
			continue
		}
		model, _ := entry["model"].(map[string]any)
		if model == nil {
			return ""
		}
		p, _ := model["provider"].(string)
		return p
	}
	return ""
}

func readProviderTestConfigMap(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// TestAgentHeartbeat_PerAgentNoGlobalBleed proves the O6 fix: a PUT setting an
// agent's heartbeat persists under THAT agent's entry, never the global
// "heartbeat" block (which previously bled onto every agent).
func TestAgentHeartbeat_PerAgentNoGlobalBleed(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Create a Main agent.
	created := postAgentProvider(t, api, `{"name":"HBAgent","soul":"s"}`)

	// Enable heartbeat with a 30-minute interval.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.Id,
		strings.NewReader(`{"heartbeat_enabled":true,"heartbeat_interval":30}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "put body: %s", w.Body.String())
	updated := decodeAgentResp(t, w.Body.Bytes())
	assert.True(t, updated.HeartbeatEnabled, "response must echo per-agent heartbeat enabled")
	assert.Equal(t, 30, updated.HeartbeatInterval)

	// Persisted under agents.list[id], NOT under a global "heartbeat" block.
	m := readProviderTestConfigMap(t, api)
	agents, _ := m["agents"].(map[string]any)
	list, _ := agents["list"].([]any)
	var entry map[string]any
	for _, e := range list {
		em, _ := e.(map[string]any)
		if em != nil && em["id"] == created.Id {
			entry = em
		}
	}
	require.NotNil(t, entry)
	assert.Equal(t, true, entry["heartbeat_enabled"], "heartbeat must persist on the agent entry")
	assert.EqualValues(t, 30, entry["heartbeat_interval"])

	// The per-agent interval (30) must NOT have bled into the global heartbeat
	// block. The global block may legitimately exist from config defaults, but it
	// must never carry the value the per-agent PUT set — that was the O6 bug.
	if hb, ok := m["heartbeat"].(map[string]any); ok {
		if iv, ok := hb["interval"]; ok {
			assert.NotEqualValues(t, 30, iv,
				"per-agent heartbeat interval must not bleed into the global heartbeat block")
		}
	}
}

// TestCreateAgent_WorkerRejectsHeartbeatAndVoice proves the O6/O12.1 form-matrix
// gate at create time: a Subagent (worker) create that sets heartbeat or voice is
// rejected (heartbeat + voice are Main-only).
func TestCreateAgent_WorkerRejectsHeartbeatAndVoice(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			"heartbeat_enabled",
			`{"name":"W","type":"Subagent","description":"d","soul":"s","executor":{"kind":"native"},"heartbeat_enabled":true}`,
			"heartbeat",
		},
		{
			"heartbeat_interval",
			`{"name":"W","type":"Subagent","description":"d","soul":"s","executor":{"kind":"native"},"heartbeat_interval":15}`,
			"heartbeat",
		},
		{
			"voice",
			`{"name":"W","type":"Subagent","description":"d","soul":"s","executor":{"kind":"native"},"voice":"alloy"}`,
			"voice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.want)
		})
	}
}

// TestCreateAgent_MainAcceptsHeartbeat proves a Main agent create CAN set
// heartbeat (the inherit/form matrix permits it for Main) and it round-trips.
func TestCreateAgent_MainAcceptsHeartbeat(t *testing.T) {
	api := buildExecutorTestAPI(t)
	created := postAgentProvider(t, api,
		`{"name":"MainHB","soul":"s","heartbeat_enabled":true,"heartbeat_interval":20}`)
	assert.True(t, created.HeartbeatEnabled, "Main create must accept heartbeat_enabled")
	assert.Equal(t, 20, created.HeartbeatInterval)
}
