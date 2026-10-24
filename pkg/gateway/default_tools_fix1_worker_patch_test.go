package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pr-test T2 / SPEC.md Definitions: a Worker default is not a permanent lock.
// Both actual creation paths must apply the operator's patch AFTER defaults.
func TestDefaultToolsWorkerCreationPreservesOperatorPatch(t *testing.T) {
	for _, path := range []struct {
		name   string
		create func(*testing.T, *restAPI) *config.AgentConfig
	}{{"REST", fix1CreatePatchedWorkerREST}, {"create_agent", fix1CreatePatchedWorkerTool}} {
		t.Run(path.name, func(t *testing.T) {
			api := l1WU2WiredPermissionAPI(t)
			api.builtinRegistry, _ = buildCentralBuiltinRegistry(nil)
			stored := path.create(t, api)
			assert.Equal(t, "FIX1 Patched Worker", stored.Name)
			assert.Equal(t, config.AgentTypeWorker, stored.Type)
			require.NotNil(t, stored.Tools)
			resolver, _ := defaultToolsPolicies(api.agentLoop.GetConfig(), stored)
			state, err := agentstore.New(api.homePath).ReadState(stored.ID)
			require.NoError(t, err)
			raw := defaultToolsInventory(t, api.homePath, api.agentLoop.GetConfig(), api.builtinRegistry.All(), map[string]any{"id": stored.ID})
			header := fmt.Sprintf("agent: %s | name: FIX1 Patched Worker | kind: worker | revision: %s", stored.ID, state.Revision)
			readback := fix1InventoryPolicyRows(t, raw, header, api.builtinRegistry.All())
			for name, want := range map[string]string{
				"set_goal": "allow", "write_file": "deny", // explicit operator patch
				"create_task": "deny", "read_file": "allow", // untouched Worker defaults
			} {
				// SPEC.md Definitions / T2: saved and effective values must retain both named overrides and untouched defaults.
				assert.Equal(t, config.ToolPolicy(want), stored.Tools.Builtin.Policies[name], "persisted %s", name)
				assert.Equal(t, want, tools.ResolveEffectivePolicy(resolver, name), "effective %s", name)
				assert.Equal(t, want, readback[name], "compact read-back %s", name)
			}
		})
	}
}

func fix1CreatePatchedWorkerREST(t *testing.T, api *restAPI) *config.AgentConfig {
	t.Helper()
	const token = "fix1-worker-patch-token"
	t.Setenv("OMNIPUS_BEARER_TOKEN", token)
	api.agentLoop.GetConfig().Gateway.ValidateInbound = true
	description := "FIX1 native Worker patch precedence"
	request := gen.AgentCreateRequestSubagent{Type: "Subagent", Name: "FIX1 Patched Worker", Description: &description, Soul: "Complete the delegated task."}
	// Decode only the patch into the generated anonymous member; no handwritten wire struct.
	require.NoError(t, json.Unmarshal([]byte(`{"tool_policy_changes":{"set":{"set_goal":"allow","write_file":"deny"}}}`), &request))
	body, err := json.Marshal(request)
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/agents", api.withAuth(api.HandleAgents))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	buildProductionMiddlewareChain(api, mux).ServeHTTP(response, req)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	created := decodeAgentResp(t, response.Body.Bytes())
	stored, err := agentstore.New(api.homePath).Get(created.Id)
	require.NoError(t, err)
	return stored
}

func fix1CreatePatchedWorkerTool(t *testing.T, api *restAPI) *config.AgentConfig {
	t.Helper()
	deps := &systools.Deps{
		Home: api.homePath, ConfigPath: api.configPath(), GetCfg: api.agentLoop.GetConfig, MutateConfig: api.agentLoop.MutateConfig,
		SaveConfigLocked: func(cfg *config.Config) error { return config.SaveConfig(api.configPath(), cfg) },
	}
	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"agent_type": "Subagent", "name": "FIX1 Patched Worker", "description": "FIX1 native Worker patch precedence",
		"model": "scripted-model", "soul": "Complete the delegated task.",
		"tool_policy_changes": map[string]any{"set": map[string]any{"set_goal": "allow", "write_file": "deny"}},
	})
	require.False(t, result.IsError, result.ForLLM)
	var response map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &response))
	id, ok := response["id"].(string)
	require.True(t, ok)
	stored, err := agentstore.New(api.homePath).Get(id)
	require.NoError(t, err)
	return stored
}
