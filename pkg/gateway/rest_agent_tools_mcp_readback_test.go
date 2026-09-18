package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// GET /agents/{id}/tools MCP JSON contract (AgentToolsCfg.yaml + ADR-090
// Settings round-trip):
//
//   - no assignments → config.mcp.servers is present and equal to []
//   - omitted tools on a binding → the "tools" key is ABSENT (all tools of
//     that assigned server)
//   - explicit [] → the "tools" key is present and equal to [] (grant none)
//   - selected names → the "tools" key is those names, in stored order
//
// Assertions use the raw JSON object. Unmarshalling into the generated
// AgentToolsResponse type collapses omitted and empty slices and cannot
// fail this test for the right reason.
func TestGETAgentTools_MCPBindingsPreserveOmitEmptyAndSelected(t *testing.T) {
	cases := []struct {
		name     string
		bindings []config.AgentMCPServerBinding
		check    func(t *testing.T, servers []any)
	}{
		{
			name:     "no-assignments-empty-servers-array",
			bindings: nil,
			check: func(t *testing.T, servers []any) {
				require.NotNil(t, servers)
				require.Empty(t, servers)
			},
		},
		{
			name: "omitted-all-tools-key-absent",
			bindings: []config.AgentMCPServerBinding{
				{ID: "docs"},
			},
			check: func(t *testing.T, servers []any) {
				require.Len(t, servers, 1)
				server := asObject(t, servers[0])
				require.Equal(t, "docs", server["id"])
				_, hasTools := server["tools"]
				require.False(t, hasTools, "omitted tools must not serialize a tools key; got %#v", server)
			},
		},
		{
			name: "explicit-empty-tools-is-empty-array",
			bindings: []config.AgentMCPServerBinding{
				{ID: "docs", Tools: []string{}, ToolsSpecified: true},
			},
			check: func(t *testing.T, servers []any) {
				require.Len(t, servers, 1)
				server := asObject(t, servers[0])
				require.Equal(t, "docs", server["id"])
				tools, ok := server["tools"].([]any)
				require.True(t, ok, "explicit empty must serialize tools as an array, got %T %#v", server["tools"], server)
				require.Empty(t, tools)
			},
		},
		{
			name: "selected-tool-names",
			bindings: []config.AgentMCPServerBinding{
				{ID: "docs", Tools: []string{"search", "fetch"}, ToolsSpecified: true},
			},
			check: func(t *testing.T, servers []any) {
				require.Len(t, servers, 1)
				server := asObject(t, servers[0])
				require.Equal(t, "docs", server["id"])
				require.Equal(t, []any{"search", "fetch"}, server["tools"])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHomeAndAgent(t)
			persistAgentMCPBindings(t, api, "mia", tc.bindings)

			r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/mia/tools", nil)
			r = withAdminRole(r)
			w := httptest.NewRecorder()
			api.HandleAgentToolsRegistry(w, r, "mia")
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

			tc.check(t, mcpServersFromGETBody(t, w.Body.Bytes()))
		})
	}
}

func persistAgentMCPBindings(t *testing.T, api *restAPI, id string, bindings []config.AgentMCPServerBinding) {
	t.Helper()
	store := agentstore.New(api.homePath)
	for _, agent := range api.agentLoop.GetConfig().Agents.List {
		if agent.ID == id {
			require.NoError(t, store.Create(id, &agent))
			break
		}
	}
	state, err := store.ReadState(id)
	require.NoError(t, err, "GET tools also reads revision from the entity store")

	_, err = store.MutateState(id, state.Revision, func(a *config.AgentConfig) error {
		if a.Tools == nil {
			a.Tools = &config.AgentToolsCfg{}
		}
		a.Tools.MCP.Servers = bindings
		return nil
	}, nil)
	require.NoError(t, err)

	after, err := store.ReadState(id)
	require.NoError(t, err)
	require.NotNil(t, after.Agent)
	cfg := api.agentLoop.GetConfig()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == id {
			cfg.Agents.List[i].Tools = after.Agent.Tools
			return
		}
	}
	t.Fatalf("agent %q is not on cfg.Agents.List", id)
}

func mcpServersFromGETBody(t *testing.T, body []byte) []any {
	t.Helper()
	var root map[string]any
	require.NoError(t, json.Unmarshal(body, &root))
	cfg, ok := root["config"].(map[string]any)
	require.True(t, ok, "config must be an object")
	mcp, ok := cfg["mcp"].(map[string]any)
	require.True(t, ok, "config.mcp must be present so Settings can round-trip assignment; body=%s", body)
	servers, ok := mcp["servers"].([]any)
	require.True(t, ok, "config.mcp.servers must be a JSON array (empty means none assigned); got %T", mcp["servers"])
	return servers
}

func asObject(t *testing.T, v any) map[string]any {
	t.Helper()
	obj, ok := v.(map[string]any)
	require.True(t, ok, "server entry must be an object, got %T", v)
	return obj
}
