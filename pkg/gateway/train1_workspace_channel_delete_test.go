package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const train1ChannelDeleteTarget = "train1-channel-delete"

// Retained #1363 hardening: test the real HTTP entry and registered tool
// BODY, not an agent turn. Omnipus's seeded deletion policy remains Deny.
func TestTrain1WorkspaceDeleteTool_BoundChannelsMatchHTTP(t *testing.T) {
	for _, entry := range []string{"http", "registered_body"} {
		for _, failConfig := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/config_failure_%t", entry, failConfig), func(t *testing.T) {
				provider := testutil.NewScenario()
				api, reg, tab, _, _ := train1WorkspacePolicyHarness(t, provider)
				before := train1SeedBoundDeletionChannels(t, api)
				state, err := workspace.ReadState(api.homePath, train1ChannelDeleteTarget)
				require.NoError(t, err)
				workPath := filepath.Join(workspace.WorkspaceDir(api.homePath, train1ChannelDeleteTarget), "work", "keep.txt")
				require.NoError(t, os.MkdirAll(filepath.Dir(workPath), 0o700))
				require.NoError(t, os.WriteFile(workPath, []byte("channel-cleanup failure must retain workspace data\n"), 0o600))
				if failConfig {
					train1BreakChannelCleanupConfig(t, api)
				}
				if entry == "http" {
					response := httptest.NewRecorder()
					request := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, train1ChannelDeleteTarget), nil)
					api.HandleWorkspaces(response, withAdminRole(request))
					wantStatus := http.StatusOK
					if failConfig {
						wantStatus = http.StatusInternalServerError
					}
					require.Equal(t, wantStatus, response.Code, "real HTTP deletion control: %s", response.Body.String())
				} else {
					result := train1WorkspaceDeleteBody(t, api, map[string]any{
						"id": train1ChannelDeleteTarget, "revision": state.Revision, "confirm": true,
					})
					assert.Equal(t, failConfig, result.IsError)
					var payload map[string]any
					require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &payload))
					train1AssertChannelDeleteToolResult(t, payload, failConfig)
				}
				if failConfig {
					after, readErr := workspace.ReadState(api.homePath, train1ChannelDeleteTarget)
					require.NoError(t, readErr, "config failure must keep the workspace")
					assert.Equal(t, state, after)
					assert.Equal(t, before, api.agentLoop.GetConfig().Channels, "failed cleanup cannot pretend its changes are live")
					data, readErr := os.ReadFile(workPath)
					require.NoError(t, readErr)
					assert.Equal(t, "channel-cleanup failure must retain workspace data\n", string(data))
				} else {
					assert.False(t, workspace.Exists(api.homePath, train1ChannelDeleteTarget))
					saved, loadErr := config.LoadConfig(api.configPath())
					require.NoError(t, loadErr)
					train1AssertChannelsCleaned(t, before, api.agentLoop.GetConfig().Channels)
					train1AssertChannelsCleaned(t, before, saved.Channels)
				}
				assert.Zero(t, provider.CallCount(), "lower-layer body coverage does not enable Omnipus deletion")
				assert.Empty(t, reg.pendingApprovals(), "lower-layer deletion must not request approval")
				assert.Empty(t, tab.sendCh, "lower-layer deletion must not emit an approval frame")
			})
		}
	}
}

func train1WorkspaceDeleteBody(t *testing.T, api *restAPI, args map[string]any) *tools.ToolResult {
	t.Helper()
	policy, covered := api.agentLoop.ResolveRegisteredToolPolicy("omnipus", "delete_workspace")
	require.True(t, covered)
	require.Equal(t, "deny", policy, "no test-fixture policy loosening")
	instance, found := api.agentLoop.GetRegistry().GetAgent("omnipus")
	require.True(t, found)
	body, registered := instance.Tools.Get("delete_workspace")
	require.True(t, registered)
	result := body.Execute(tools.WithAgentID(context.Background(), "omnipus"), args)
	require.NotNil(t, result)
	return result
}

func train1SeedBoundDeletionChannels(t *testing.T, api *restAPI) map[string]config.ChannelInstanceConfig {
	t.Helper()
	seedAgentEntities(t, api.homePath, api.agentLoop.GetConfig().Agents.List)
	for _, id := range []string{train1ChannelDeleteTarget, "train1-other-workspace"} {
		require.NoError(t, workspace.SaveRecord(api.homePath, workspace.Workspace{
			ID: id, Name: id, Status: "active", CoreTeam: []string{"mia"},
		}))
	}
	cfg, err := api.agentLoop.GetConfig().Clone()
	require.NoError(t, err)
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"whatsapp.target-enabled": {Type: "whatsapp", Enabled: true, WorkspaceID: train1ChannelDeleteTarget,
			Identity: &config.ChannelIdentity{Kind: "agent", ID: "mia"}, SessionStorePath: "keep-target-store"},
		"whatsapp.target-disabled": {Type: "whatsapp", Enabled: false, WorkspaceID: train1ChannelDeleteTarget,
			Identity: &config.ChannelIdentity{Kind: "agent", ID: "mia"}},
		"whatsapp.other": {Type: "whatsapp", Enabled: true, WorkspaceID: "train1-other-workspace",
			Identity: &config.ChannelIdentity{Kind: "agent", ID: "mia"}, SessionStorePath: "keep-other-store"},
	}
	require.NoError(t, config.SaveConfig(api.configPath(), cfg))
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
	snapshot, err := api.agentLoop.GetConfig().Clone()
	require.NoError(t, err)
	require.Len(t, snapshot.Channels, 3)
	require.True(t, snapshot.Channels["whatsapp.target-enabled"].Enabled, "the bound enabled instance is the failure-detection control")
	require.Equal(t, train1ChannelDeleteTarget, snapshot.Channels["whatsapp.target-enabled"].WorkspaceID)
	return snapshot.Channels
}

// A directory at config.json gives a real, portable IO failure without
// chmod/root assumptions or a mocked cleanup operation. Restore for cleanup.
func train1BreakChannelCleanupConfig(t *testing.T, api *restAPI) {
	t.Helper()
	path := api.configPath()
	backup := path + ".before-channel-delete"
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700))
	t.Cleanup(func() {
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Rename(backup, path))
	})
}

func train1AssertChannelDeleteToolResult(t *testing.T, result map[string]any, failConfig bool) {
	t.Helper()
	if failConfig {
		assert.Equal(t, false, result["success"])
		errorInfo, ok := result["error"].(map[string]any)
		require.True(t, ok, "a config failure must be an explicit tool error: %v", result)
		assert.Equal(t, "SAVE_FAILED", errorInfo["code"])
		assert.NotEmpty(t, errorInfo["message"])
		assert.NotEqual(t, true, result["deleted"])
		return
	}
	assert.Equal(t, true, result["deleted"])
	assert.Equal(t, []any{"whatsapp.target-disabled", "whatsapp.target-enabled"}, result["channels_disabled_and_unbound"],
		"report the exact committed instance IDs, sorted; unrelated channels are not cleanup results")
}

func train1AssertChannelsCleaned(t *testing.T, before, after map[string]config.ChannelInstanceConfig) {
	t.Helper()
	require.Len(t, after, len(before), "unbinding does not delete configured channel instances")
	for key, original := range before {
		want := original
		if original.WorkspaceID == train1ChannelDeleteTarget {
			want.Enabled = false
			want.WorkspaceID = ""
			want.Identity = nil
		}
		actual, exists := after[key]
		require.True(t, exists, "configured instance %s must remain", key)
		assert.Equal(t, want, actual, "only the deleted workspace's bindings/enabled flags may change: %s", key)
	}
}

// Preserve committed-write reporting independently of the denied agent path.
func TestTrain1WorkspaceDeleteTool_BoundChannelsRefreshFailure(t *testing.T) {
	provider := testutil.NewScenario()
	api, reg, tab, _, _ := train1WorkspacePolicyHarness(t, provider)
	before := train1SeedBoundDeletionChannels(t, api)
	state, err := workspace.ReadState(api.homePath, train1ChannelDeleteTarget)
	require.NoError(t, err)
	data, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var rawConfig map[string]any
	require.NoError(t, json.Unmarshal(data, &rawConfig))
	rawConfig["version"] = config.CurrentVersion + 1
	data, err = json.Marshal(rawConfig)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), data, 0o600))
	_, loadErr := config.LoadConfig(api.configPath())
	require.EqualError(t, loadErr, fmt.Sprintf("unsupported config version: %d", config.CurrentVersion+1), "positive refresh-failure instrument")
	result := train1WorkspaceDeleteBody(t, api, map[string]any{
		"id": train1ChannelDeleteTarget, "revision": state.Revision, "confirm": true,
	})
	require.True(t, result.IsError)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &payload))
	assert.Equal(t, false, payload["success"])
	assert.Equal(t, "partly saved", payload["outcome"], "committed channel changes must not be reported as undone")
	assert.Equal(t, []any{"whatsapp.target-disabled", "whatsapp.target-enabled"}, payload["channels_disabled_and_unbound"])
	assert.Equal(t, []any{"channels"}, payload["changed_fields"])
	errorInfo, ok := payload["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "SAVE_FAILED", errorInfo["code"])
	assert.Equal(t, before, api.agentLoop.GetConfig().Channels, "a failed reload cannot pretend the live config changed")
	after, err := workspace.ReadState(api.homePath, train1ChannelDeleteTarget)
	require.NoError(t, err)
	assert.Equal(t, state, after)
	data, err = os.ReadFile(api.configPath())
	require.NoError(t, err)
	var saved config.Config
	require.NoError(t, json.Unmarshal(data, &saved))
	train1AssertChannelsCleaned(t, before, saved.Channels)
	assert.Zero(t, provider.CallCount())
	assert.Empty(t, reg.pendingApprovals(), "direct body failure must not request approval")
	assert.Empty(t, tab.sendCh, "direct body failure must not emit an approval frame")
}
