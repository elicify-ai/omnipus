//go:build goolm && stdjson

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wu3RegisteredTool(t *testing.T, api *restAPI, name string) tools.Tool {
	t.Helper()
	inst, ok := api.agentLoop.GetRegistry().GetAgent("omnipus")
	require.True(t, ok)
	tool, ok := inst.Tools.Get(name)
	require.True(t, ok, "real registered %s", name)
	return tool
}

// FR-085/§6.6. Hold the actual Settings config lock, launch an actual tool,
// and inspect the blocked goroutine's synchronization boundary. This is not a
// stopwatch oracle: completion while the lock is held is an immediate defect;
// otherwise its stack must wait on the gateway-config lock, not a loop lock.
func TestWU3SharedToolWaitsOnSettingsLockBeforeLoopMutation(t *testing.T) {
	api := wu3WiredAPI(t, nil)
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", Enabled: true}}
		return config.SaveConfig(api.configPath(), cfg)
	}))
	list := wu3RegisteredTool(t, api, "list_channels").Execute(context.Background(), nil)
	var projection map[string]any
	require.NoError(t, json.Unmarshal([]byte(list.ForLLM), &projection))
	var revision any
	for _, raw := range projection["channels"].([]any) {
		row := raw.(map[string]any)
		if row["id"] == "telegram" {
			revision = row["revision"]
		}
	}
	api.configMu.Lock()
	locked := true
	defer func() {
		if locked {
			api.configMu.Unlock()
		}
	}()
	done := make(chan *tools.ToolResult, 1)
	tool := wu3RegisteredTool(t, api, "disable_channel")
	go func() {
		done <- tool.Execute(context.Background(), map[string]any{"id": "telegram", "expected_revision": revision})
	}()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	blockedOnConfig := false
	for !blockedOnConfig {
		select {
		case result := <-done:
			api.configMu.Unlock()
			locked = false
			t.Fatalf("FR-085: disable_channel completed while Settings held config lock (loop mutation escaped boundary): %s", result.ForLLM)
		case <-deadline.C:
			t.Fatal("tool never reached an observable synchronization boundary")
		default:
			buffer := make([]byte, 1<<20)
			n := runtime.Stack(buffer, true)
			for _, stack := range strings.Split(string(buffer[:n]), "\n\n") {
				if !strings.Contains(stack, "wu3_shared_boundary_test.go") || !strings.Contains(stack, "ChannelDisableTool") {
					continue
				}
				if strings.Contains(stack, "internal/sync.(*Mutex).lockSlow") && !strings.Contains(stack, "AgentLoop.MutateConfig") {
					blockedOnConfig = true
				}
			}
			runtime.Gosched()
		}
	}
	// While the tool waits for configMu, the loop lock must remain available.
	probe := make(chan error, 1)
	go func() {
		probe <- api.agentLoop.MutateConfig(func(cfg *config.Config) error {
			assert.True(t, cfg.Channels["telegram"].Enabled, "waiting tool has not mutated live state")
			return nil
		})
	}()
	select {
	case err := <-probe:
		require.NoError(t, err)
	case <-deadline.C:
		t.Fatal("AB-BA: tool holds loop lock while waiting for config lock")
	}
	api.configMu.Unlock()
	locked = false
	select {
	case result := <-done:
		require.False(t, result.IsError, "correct token proceeds after lock released: %s", result.ForLLM)
	case <-time.After(10 * time.Second):
		t.Fatal("tool did not complete after Settings lock released")
	}
}

// BDD-11.9 MCP Settings contention. A REAL REST PATCH goes through the shared
// config lock; the registered tool must refuse the old token and keep winner.
func TestWU3MCPSettingsEditInvalidatesConfirmedToolRevision(t *testing.T) {
	api := wu3WiredAPI(t, nil)
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{"notion": {Type: "stdio", Command: "inert-command", Enabled: false}}
		return config.SaveConfig(api.configPath(), cfg)
	}))
	list := wu3RegisteredTool(t, api, "list_mcp_servers").Execute(context.Background(), nil)
	var data map[string]any
	require.NoError(t, json.Unmarshal([]byte(list.ForLLM), &data))
	servers := data["servers"].([]any)
	require.Len(t, servers, 1)
	old := servers[0].(map[string]any)["revision"]
	change := gen.McpServerUpdate{}
	// Use generated type to represent a Settings change; no invented wire struct.
	changedCommand := "settings-winner-command"
	change.Command = &changedCommand
	encoded, err := json.Marshal(change)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	api.HandleMCPServers(w, httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/notion", bytes.NewReader(encoded)))
	require.Equal(t, http.StatusOK, w.Code, "real Settings PATCH must commit: %s", w.Body.String())
	winner := api.agentLoop.GetConfig().Tools.MCP.Servers["notion"]
	result := wu3RegisteredTool(t, api, "remove_mcp_server").Execute(context.Background(), map[string]any{"name": "notion", "confirm": true, "expected_revision": old})
	assert.True(t, result.IsError, "tool must refuse stale summary after Settings changed entry")
	assert.Equal(t, winner, api.agentLoop.GetConfig().Tools.MCP.Servers["notion"], "Settings winner must not be deleted")
	var refused map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &refused))
	assert.Equal(t, "failed: nothing saved", refused["outcome"])
}
