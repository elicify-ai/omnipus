package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestADR090UpdateAgentRevisionConflictIsZeroWrite(t *testing.T) {
	api := buildExecutorTestAPI(t)
	store := agentstore.New(api.homePath)
	before, err := store.ReadState("test-agent")
	require.NoError(t, err)
	body := map[string]any{"revision": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "model": "changed-model"}
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "test-agent")
	require.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
	after, err := store.ReadState("test-agent")
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
}

func TestADR090UpdateAgentRejectsProtectedSameValueEchoBeforeAllowedChange(t *testing.T) {
	api := newSeededJudgeAPI(t)
	store := agentstore.New(api.homePath)
	before, err := store.ReadState("mia")
	require.NoError(t, err)
	body := map[string]any{"revision": before.Revision, "name": before.Agent.Name, "model": "changed-model"}
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "mia")
	require.Equal(t, http.StatusForbidden, w.Code, "body=%s", w.Body.String())
	after, err := store.ReadState("mia")
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
}

func TestADR090UpdateAgentExplicitEmptySkillsClearsAndOmissionPreserves(t *testing.T) {
	api := buildExecutorTestAPI(t)
	store := agentstore.New(api.homePath)
	state, err := store.ReadState("test-agent")
	require.NoError(t, err)
	_, err = store.MutateState("test-agent", state.Revision, func(agent *config.AgentConfig) error {
		agent.Skills = []string{"web-research"}
		return nil
	}, nil)
	require.NoError(t, err)
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))

	seeded, err := store.ReadState("test-agent")
	require.NoError(t, err)
	omissionRaw, err := json.Marshal(map[string]any{"revision": seeded.Revision, "description": "skills must survive omission"})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", bytes.NewReader(omissionRaw))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "test-agent")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	afterOmission, err := store.ReadState("test-agent")
	require.NoError(t, err)
	require.Equal(t, []string{"web-research"}, afterOmission.Agent.Skills)
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
	require.Equal(t, []string{"web-research"}, configuredAgent(t, api, "test-agent").Skills)

	clearRaw, err := json.Marshal(map[string]any{"revision": afterOmission.Revision, "skills": []string{}})
	require.NoError(t, err)
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", bytes.NewReader(clearRaw))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "test-agent")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	afterClear, err := store.ReadState("test-agent")
	require.NoError(t, err)
	require.Empty(t, afterClear.Agent.Skills)
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
	require.Empty(t, configuredAgent(t, api, "test-agent").Skills)
}

func configuredAgent(t *testing.T, api *restAPI, id string) config.AgentConfig {
	t.Helper()
	for _, candidate := range api.agentLoop.GetConfig().Agents.List {
		if candidate.ID == id {
			return candidate
		}
	}
	t.Fatalf("configured agent %q not found", id)
	return config.AgentConfig{}
}

func TestADR090UpdateAgentToolsPersistsOnlyOverrideNames(t *testing.T) {
	api := buildExecutorTestAPI(t)
	store := agentstore.New(api.homePath)
	state, err := store.ReadState("test-agent")
	require.NoError(t, err)
	complete := map[string]string{}
	for name := range buildKnownBuiltinToolNames() {
		policy := api.agentLoop.GetConfig().Sandbox.ToolPolicies[name]
		if policy == "" {
			policy = string(config.ToolPolicyDeny)
		}
		complete[name] = policy
	}
	complete["bash"] = "deny"
	raw, _ := json.Marshal(map[string]any{"revision": state.Revision, "override_names": []string{"bash"}, "builtin": map[string]any{"policies": complete}})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent/tools", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	api.updateAgentTools(w, r, "test-agent")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	after, err := store.ReadState("test-agent")
	require.NoError(t, err)
	require.Equal(t, 1, len(after.Agent.Tools.Builtin.Policies))
	require.Equal(t, "deny", string(after.Agent.Tools.Builtin.Policies["bash"]))
}

func TestADR090CreateAgentAcceptsSparsePolicyAndMCPPresence(t *testing.T) {
	api := buildExecutorTestAPI(t)
	if api.agentLoop.GetConfig().Tools.MCP.Servers == nil {
		api.agentLoop.GetConfig().Tools.MCP.Servers = map[string]config.MCPServerConfig{}
	}
	if api.agentLoop.GetConfig().Sandbox.ToolPolicies == nil {
		api.agentLoop.GetConfig().Sandbox.ToolPolicies = map[string]string{}
	}
	api.agentLoop.GetConfig().Tools.MCP.Servers["docs"] = config.MCPServerConfig{Enabled: true, Command: "test"}
	api.agentLoop.GetConfig().Sandbox.ToolPolicies["get_agent"] = string(config.ToolPolicyDeny)
	api.agentLoop.GetConfig().Sandbox.ToolPolicies["get_agent_tools"] = string(config.ToolPolicyDeny)
	body := `{"name":"Configured","type":"Main","soul":"persona","mcp_servers":[{"id":"docs","tools":[]}],"tool_policy_changes":{"set":{"bash":"deny"}}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	api.createAgent(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var response struct {
		Id string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	state, err := agentstore.New(api.homePath).ReadState(response.Id)
	require.NoError(t, err)
	require.Equal(t, "deny", string(state.Agent.Tools.Builtin.Policies["bash"]))
	require.Len(t, state.Agent.Tools.MCP.Servers, 1)
	require.True(t, state.Agent.Tools.MCP.Servers[0].ToolsSpecified)
	require.Empty(t, state.Agent.Tools.MCP.Servers[0].Tools)
	require.Equal(t, "persona", state.Soul)
}

func TestADR090CreateAgentRejectsNullAliasMembers(t *testing.T) {
	api := buildExecutorTestAPI(t)
	for _, body := range []string{
		`{"name":"Bad","type":"Main","soul":"persona","mcp_servers":null}`,
		`{"name":"Bad","type":"Main","soul":"persona","mcp_servers":[{"id":"docs","tools":null}]}`,
		`{"name":"Bad","type":"Main","soul":"persona","tool_policy_changes":{"set":null}}`,
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		api.createAgent(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	}
}
