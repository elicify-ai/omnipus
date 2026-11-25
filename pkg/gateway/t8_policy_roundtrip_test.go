package gateway

// r17 19.14: real person handler GET→PUT bodies on fresh seeded policy maps.
// Re-auth is the existing person-session fixture; persistence/reload are real.
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8Permissions_PersonToolsAndGlobalScreensRoundTrip(t *testing.T) {
	t.Run("person_tools", func(t *testing.T) {
		api := newSeededJudgeAPI(t)
		cfg := api.agentLoop.GetConfig()
		cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
		custom := config.AgentConfig{ID: "t8-person-policy", Name: "T8 person policy", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}
		store := agentstore.New(api.homePath)
		_, err := store.CreateState(custom.ID, &custom, "custom policy colleague")
		require.NoError(t, err)
		cfg.Agents.List = append(cfg.Agents.List, custom)
		require.NoError(t, config.SaveConfig(api.configPath(), cfg))
		// Persisting a custom entity does not register its runtime instance.
		// Use the actual create path's registry wiring before the Tools GET;
		// an unrelated unregistered-agent 404 is not screens-save evidence.
		_, err = api.agentLoop.UpsertAgentFast(cfg, custom.ID)
		require.NoError(t, err, "fixture: register the real persisted custom agent")
		_, registered := api.agentLoop.GetRegistry().GetAgent(custom.ID)
		require.True(t, registered, "instrument: Tools GET can resolve the custom agent")
		before, err := store.ReadState(custom.ID)
		require.NoError(t, err)
		get := httptest.NewRecorder()
		api.HandleAgentToolsRegistry(get, withAdminRole(httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+custom.ID+"/tools", nil)), custom.ID)
		require.Equal(t, http.StatusOK, get.Code, "person Tools GET: %s", get.Body.String())
		var view map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(get.Body.Bytes(), &view))
		require.Contains(t, view, "config")
		require.Contains(t, view, "tools")
		put := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+custom.ID+"/tools", bytes.NewReader(get.Body.Bytes()))
		put.Header.Set("Content-Type", "application/json")
		put = withReAuthAdmin(t, api, put)
		saved := httptest.NewRecorder()
		api.updateAgentTools(saved, put, custom.ID)
		require.Equal(t, http.StatusOK, saved.Code, "19.14 saving the actual GET body: %s", saved.Body.String())
		after, err := store.ReadState(custom.ID)
		require.NoError(t, err)
		assert.Equal(t, before.Agent.Tools, after.Agent.Tools, "a no-edit person save keeps the seeded policy data")
	})
	t.Run("global_policies", func(t *testing.T) {
		// FIX ROUND 4, Part B row 6: the global screen GET -> PUT round trip used the retired
		// /security/tool-policies handler (C-22 row 6). Its replacement is the permissions read
		// model and PATCH (C-2), driven through the real route table. The oracle is unchanged: the
		// actual GET body must be accepted as a save body and leave the ceiling as it was. Under
		// PATCH that is "a request that names keys whose values equal the stored values is valid:
		// 200, nothing written" (C-2), so the whole read model, not only the policies map, must come
		// back identical.
		api, routes := newPermissionsAPI(t)
		cfg := api.agentLoop.GetConfig()
		cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
		require.NoError(t, config.SaveConfig(api.configPath(), cfg))
		globalGet := getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, globalGet.Code, "GET /security/permissions must exist (C-2): %s", globalGet.Body.String())
		var globalView map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(globalGet.Body.Bytes(), &globalView))
		require.Contains(t, globalView, "policies")
		var original map[string]string
		require.NoError(t, json.Unmarshal(globalView["policies"], &original))
		require.NotEmpty(t, original, "instrument: the global screen read a real complete ceiling")
		globalSaved := patchPermissions(t, api, routes, globalGet.Body.String())
		require.Equal(t, http.StatusOK, globalSaved.Code, "19.14 global screen GET body saves: %s", globalSaved.Body.String())
		reread := getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, reread.Code)
		var final map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(reread.Body.Bytes(), &final))
		var policies map[string]string
		require.NoError(t, json.Unmarshal(final["policies"], &policies))
		assert.Equal(t, original, policies)
		assert.JSONEq(t, globalGet.Body.String(), reread.Body.String(), "saving the GET body back changes nothing (C-2)")
	})
}
