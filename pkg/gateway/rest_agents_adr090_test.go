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
	raw, _ := json.Marshal(map[string]any{"revision": state.Revision, "skills": []string{}})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "test-agent")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	after, err := store.ReadState("test-agent")
	require.NoError(t, err)
	require.Empty(t, after.Agent.Skills)
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
