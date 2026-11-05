package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Update-only tests start with an existing real record. This does not mask F-05:
// createSubagent3p and TestRestExternalCreate_SoulContract still drive HTTP create
// without SOUL. No production callback, policy gate or store is mocked here.
func t8ExistingExternalWorker(t *testing.T) (*restAPI, string) {
	t.Helper()
	api := buildExecutorTestAPI(t)
	id := "existing-external"
	record := config.AgentConfig{ID: id, Name: "Existing external", Type: config.AgentTypeWorker,
		Tools:     api.agentLoop.GetConfig().Agents.List[0].Tools,
		Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex"}}}
	require.NoError(t, agentstore.New(api.homePath).Create(id, &record))
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List, record)
	return api, id
}

func t8AssertExecutorRefusal(t *testing.T, api *restAPI, id, body, field, want string) {
	t.Helper()
	store := agentstore.New(api.homePath)
	before, err := store.ReadState(id)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	req := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, "r17 refusal: %s", w.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, field, out["field"])
	assert.Equal(t, want, out["error"])
	after, err := store.ReadState(id)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a refused executor mutation leaves the entire state and revision unchanged")
}

func t8CurrentForbiddenCreateFixture(t *testing.T) []byte {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(gen.FixtureAgentCreateRequestSubagent3p_ForbiddenFieldJSON(), &body))
	// The shared factory is not test-owned; report its drift instead of editing
	// production/generated files. Keep its deliberate tools_cfg negative input.
	require.Contains(t, body, "tools_cfg")
	delete(body, "soul")
	executor, ok := body["executor"].(map[string]any)
	require.True(t, ok)
	delete(executor, "kind")
	delete(executor, "cli_path")
	result, err := json.Marshal(body)
	require.NoError(t, err)
	return result
}
