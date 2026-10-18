package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const train1ChannelDeleteTarget = "train1-channel-delete"

// ADR-029 workspace-delete cascade and review F1: HTTP is the existing
// control, not an oracle copied from a new implementation. Both paths must
// clear both bindings, disable only the target's instances and persist it.
func TestTrain1OmnipusWorkspaceTools_DeleteBoundChannels(t *testing.T) {
	for _, entry := range []string{"http", "tool"} {
		for _, failConfig := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/config_failure_%t", entry, failConfig), func(t *testing.T) {
				provider := testutil.NewScenario()
				api, reg, tab, msgBus, mainID := train1WorkspacePolicyHarness(t, provider)
				before := train1SeedBoundDeletionChannels(t, api)
				state, err := workspace.ReadState(api.homePath, train1ChannelDeleteTarget)
				require.NoError(t, err)
				workPath := filepath.Join(workspace.WorkspaceDir(api.homePath, train1ChannelDeleteTarget), "work", "keep.txt")
				require.NoError(t, os.MkdirAll(filepath.Dir(workPath), 0o700))
				require.NoError(t, os.WriteFile(workPath, []byte("channel-cleanup failure must retain workspace data\n"), 0o600))
				if entry == "http" {
					if failConfig {
						train1BreakChannelCleanupConfig(t, api)
					}
					response := httptest.NewRecorder()
					request := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, train1ChannelDeleteTarget), nil)
					api.HandleWorkspaces(response, withAdminRole(request))
					wantStatus := http.StatusOK
					if failConfig {
						wantStatus = http.StatusInternalServerError
					}
					require.Equal(t, wantStatus, response.Code, "real HTTP deletion control: %s", response.Body.String())
				} else {
					args := map[string]any{"id": train1ChannelDeleteTarget, "revision": state.Revision, "confirm": true}
					encoded, encodeErr := json.Marshal(args)
					require.NoError(t, encodeErr)
					provider.WithToolCall("ToolSearch", `{"names":["delete_workspace"]}`).
						WithToolCall("delete_workspace", string(encoded)).WithText("Deletion request handled.")
					ctx := train1StartMainTurn(t, api, msgBus, mainID, "Delete the channel-bound workspace")
					var approval gen.ToolApprovalRequiredFrame
					select {
					case raw := <-tab.sendCh:
						require.NoError(t, json.Unmarshal(raw, &approval))
					case reply := <-msgBus.OutboundChan():
						t.Fatalf("delete returned before approval: %s", reply.Content)
					case <-ctx.Done():
						t.Fatal("the real bound-workspace deletion did not ask for approval")
					}
					require.Equal(t, "delete_workspace", approval.ToolName)
					require.Equal(t, mainID, approval.SessionId)
					require.Equal(t, args, approval.Args)
					require.Len(t, reg.pendingApprovals(), 1)
					assert.Equal(t, before, api.agentLoop.GetConfig().Channels, "no channel mutation before approval")
					assert.True(t, workspace.Exists(api.homePath, train1ChannelDeleteTarget))
					if failConfig {
						train1BreakChannelCleanupConfig(t, api)
					}
					resolved, gone := reg.resolve(approval.ApprovalId, ApprovalActionApprove, false)
					require.True(t, resolved)
					require.False(t, gone)
					train1AssertChannelDeleteReply(t, ctx.Done(), msgBus, mainID)
					train1AssertChannelDeleteToolResult(t, provider, failConfig)
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
			})
		}
	}
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

func train1AssertChannelDeleteReply(t *testing.T, done <-chan struct{}, msgBus *bus.MessageBus, mainID string) {
	t.Helper()
	select {
	case reply := <-msgBus.OutboundChan():
		require.Equal(t, "Deletion request handled.", reply.Content)
		require.Equal(t, mainID, reply.SessionID)
	case <-done:
		t.Fatal("the approved deletion did not return")
	}
}

func train1ChannelDeleteToolResult(t *testing.T, provider *testutil.ScenarioProvider) map[string]any {
	t.Helper()
	var result map[string]any
	found := 0
	for _, message := range provider.LastMessages() {
		if message.Role == "tool" && message.ToolCallID == "delete_workspace-0" {
			found++
			require.NoError(t, json.Unmarshal([]byte(message.Content), &result))
		}
	}
	require.Equal(t, 1, found, "inspect the actual tool result, not the scripted final answer")
	return result
}

func train1AssertChannelDeleteToolResult(t *testing.T, provider *testutil.ScenarioProvider, failConfig bool) {
	t.Helper()
	result := train1ChannelDeleteToolResult(t, provider)
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

// The shared transaction may save the unbind and then fail to refresh the
// running config. Do not report "nothing saved" or delete the workspace.
func TestTrain1OmnipusWorkspaceTools_DeleteBoundChannelsRefreshFailure(t *testing.T) {
	provider := testutil.NewScenario()
	api, reg, tab, msgBus, mainID := train1WorkspacePolicyHarness(t, provider)
	before := train1SeedBoundDeletionChannels(t, api)
	state, err := workspace.ReadState(api.homePath, train1ChannelDeleteTarget)
	require.NoError(t, err)
	args, err := json.Marshal(map[string]any{"id": train1ChannelDeleteTarget, "revision": state.Revision, "confirm": true})
	require.NoError(t, err)
	provider.WithToolCall("ToolSearch", `{"names":["delete_workspace"]}`).
		WithToolCall("delete_workspace", string(args)).WithText("Deletion request handled.")
	ctx := train1StartMainTurn(t, api, msgBus, mainID, "Delete the channel-bound workspace")
	var approval gen.ToolApprovalRequiredFrame
	select {
	case raw := <-tab.sendCh:
		require.NoError(t, json.Unmarshal(raw, &approval))
	case reply := <-msgBus.OutboundChan():
		t.Fatalf("delete returned before approval: %s", reply.Content)
	case <-ctx.Done():
		t.Fatal("the real bound-workspace deletion did not ask for approval")
	}
	require.Equal(t, "delete_workspace", approval.ToolName)
	require.Equal(t, mainID, approval.SessionId)
	data, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var rawConfig map[string]any
	require.NoError(t, json.Unmarshal(data, &rawConfig))
	// Valid JSON permits the raw write, while the unsupported schema version
	// deterministically rejects the following real config reload on every OS.
	rawConfig["version"] = config.CurrentVersion + 1
	data, err = json.Marshal(rawConfig)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), data, 0o600))
	_, loadErr := config.LoadConfig(api.configPath())
	require.EqualError(t, loadErr, fmt.Sprintf("unsupported config version: %d", config.CurrentVersion+1), "positive refresh-failure instrument")
	resolved, gone := reg.resolve(approval.ApprovalId, ApprovalActionApprove, false)
	require.True(t, resolved)
	require.False(t, gone)
	train1AssertChannelDeleteReply(t, ctx.Done(), msgBus, mainID)
	result := train1ChannelDeleteToolResult(t, provider)
	assert.Equal(t, false, result["success"])
	assert.Equal(t, "partly saved", result["outcome"], "committed channel changes must not be reported as undone")
	assert.Equal(t, []any{"whatsapp.target-disabled", "whatsapp.target-enabled"}, result["channels_disabled_and_unbound"])
	assert.Equal(t, []any{"channels"}, result["changed_fields"])
	errorInfo, ok := result["error"].(map[string]any)
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
}
