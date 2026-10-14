//go:build goolm && stdjson

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BDD-11.9 provider/Settings row: only the configured endpoint changes.
func TestWU3ProviderSettingsEditInvalidatesConfirmedToolRevision(t *testing.T) {
	api := wu3WiredAPI(t, nil)
	installEmbeddedCatalog(t, api)
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		cfg.Providers = []*config.ModelConfig{{Provider: "ollama", Model: "fixture-model", APIBase: "http://127.0.0.1:1/original/v1"}}
		return config.SaveConfig(api.configPath(), cfg)
	}))
	listed := wu3RegisteredTool(t, api, "list_providers").Execute(context.Background(), nil)
	var projection map[string]any
	require.NoError(t, json.Unmarshal([]byte(listed.ForLLM), &projection))
	items := wu3Array(t, projection["providers"])
	require.Len(t, items, 1)
	old := wu3Object(t, items[0])["revision"]
	endpoint := "http://127.0.0.1:1/settings-winner/v1"
	data, err := json.Marshal(gen.ProviderUpdateRequest{ApiBase: &endpoint})
	require.NoError(t, err)
	request := withReAuthAdmin(t, api, httptest.NewRequest(http.MethodPut, "/api/v1/providers/ollama", bytes.NewReader(data)))
	w := httptest.NewRecorder()
	api.HandleProviders(w, request)
	require.Equal(t, http.StatusOK, w.Code, "real Settings provider update commits: %s", w.Body.String())
	winner := api.agentLoop.GetConfig().Providers[0].APIBase
	require.Equal(t, endpoint, winner)
	result := wu3RegisteredTool(t, api, "configure_provider").Execute(context.Background(), map[string]any{"name": "ollama", "api_base": "http://127.0.0.1:1/loser/v1", "expected_revision": old})
	assert.True(t, result.IsError, "stale plan cannot overwrite Settings endpoint")
	assert.Equal(t, endpoint, api.agentLoop.GetConfig().Providers[0].APIBase)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body))
	assert.Equal(t, "failed: nothing saved", body["outcome"])
}

// BDD-11.9 channel/Settings row: binding-only change must bump token. No live
// platform is contacted. REST and registered tool share the real config owner.
func TestWU3ChannelSettingsBindingEditInvalidatesOldToolRevision(t *testing.T) {
	api := wu3WiredAPI(t, nil)
	require.NoError(t, workspace.SaveRecord(api.homePath, workspace.Workspace{ID: "sales", Name: "Sales", Status: "active", CoreTeam: []string{"mia", "jim"}}))
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", Enabled: false, WorkspaceID: "sales", Identity: &config.ChannelIdentity{Kind: "agent", ID: "mia"}}}
		return config.SaveConfig(api.configPath(), cfg)
	}))
	listed := wu3RegisteredTool(t, api, "list_channels").Execute(context.Background(), nil)
	var projection map[string]any
	require.NoError(t, json.Unmarshal([]byte(listed.ForLLM), &projection))
	var old any
	for _, raw := range wu3Array(t, projection["channels"]) {
		row := wu3Object(t, raw)
		if row["id"] == "telegram" {
			old = row["revision"]
		}
	}
	request := gen.ChannelRoutingUpdateRequest{WorkspaceId: "sales", DefaultAgentId: "jim"}
	data, err := json.Marshal(request)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	api.HandleChannels(w, httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/routing", bytes.NewReader(data)))
	require.Equal(t, http.StatusOK, w.Code, "real Settings binding must commit: %s", w.Body.String())
	winner := api.agentLoop.GetConfig().Channels["telegram"]
	require.NotNil(t, winner.Identity)
	require.Equal(t, "jim", winner.Identity.ID)
	result := wu3RegisteredTool(t, api, "enable_channel").Execute(context.Background(), map[string]any{"id": "telegram", "expected_revision": old})
	assert.True(t, result.IsError, "stale plan must not enable a re-bound outside connection")
	assert.Equal(t, winner, api.agentLoop.GetConfig().Channels["telegram"], "binding winner survives")
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body))
	assert.Equal(t, "failed: nothing saved", body["outcome"])
	errorBody, ok := body["error"].(map[string]any)
	require.True(t, ok, "refusal must carry a structured error, never panic on baseline")
	assert.Equal(t, "CONFLICT", errorBody["code"])
}
