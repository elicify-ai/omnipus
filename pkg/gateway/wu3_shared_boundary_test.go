//go:build goolm && stdjson

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	for _, verb := range []string{"disable_channel", "remove_mcp_server", "configure_provider"} {
		t.Run(verb, func(t *testing.T) { wu3SharedLockProbe(t, verb) })
	}
}

func wu3SharedLockProbe(t *testing.T, verb string) {
	t.Helper()
	api := wu3WiredAPI(t, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"models":[{"name":"fixture-model"}]}`)
	}))
	defer server.Close()
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", Enabled: true}}
		cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{"notion": {Type: "stdio", Command: "inert", Enabled: false}}
		cfg.Providers = []*config.ModelConfig{{Provider: "ollama", Model: "fixture-model", APIBase: server.URL + "/v1"}}
		return config.SaveConfig(api.configPath(), cfg)
	}))
	readName, array, key, id, typeName := "list_channels", "channels", "id", "telegram", "ChannelDisableTool"
	args := map[string]any{"id": "telegram"}
	if verb == "remove_mcp_server" {
		readName, array, key, id, typeName = "list_mcp_servers", "servers", "name", "notion", "MCPRemoveTool"
		args = map[string]any{"name": "notion", "confirm": true}
	}
	if verb == "configure_provider" {
		readName, array, key, id, typeName = "list_providers", "providers", "name", "ollama", "ProviderConfigureTool"
		args = map[string]any{"name": "ollama", "api_base": server.URL + "/v1"}
	}
	list := wu3RegisteredTool(t, api, readName).Execute(context.Background(), nil)
	var projection map[string]any
	require.NoError(t, json.Unmarshal([]byte(list.ForLLM), &projection))
	for _, raw := range wu3Array(t, projection[array]) {
		row := wu3Object(t, raw)
		if row[key] == id {
			args["expected_revision"] = row["revision"]
		}
	}
	before, err := json.Marshal(api.agentLoop.GetConfig())
	require.NoError(t, err)
	api.configMu.Lock()
	locked := true
	defer func() {
		if locked {
			api.configMu.Unlock()
		}
	}()
	done := make(chan *tools.ToolResult, 1)
	tool := wu3RegisteredTool(t, api, verb)
	go func() { done <- tool.Execute(context.Background(), args) }()
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
				if !strings.Contains(stack, "wu3_shared_boundary_test.go") || !strings.Contains(stack, typeName) {
					continue
				}
				// The address of the real, currently held Settings mutex, not an
				// invented internal helper name or a timing proxy.
				if strings.Contains(stack, fmt.Sprintf("(%p)", &api.configMu)) {
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
			after, marshalErr := json.Marshal(cfg)
			if marshalErr != nil {
				return marshalErr
			}
			assert.Equal(t, before, after, "waiting tool has not mutated live state")
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
	servers := wu3Array(t, data["servers"])
	require.Len(t, servers, 1)
	old := wu3Object(t, servers[0])["revision"]
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
