//go:build !cgo

// Tests for MCP backend handlers G6–G9.
//
//   - G6: listMCPServers reflects live manager status + tool_count + enabled.
//   - G7: testMCPServer returns success=false gracefully for an unreachable server.
//   - G8: patchMCPServer merges partial update (omitted fields preserved, enabled toggled).
//   - G9: addMCPServer persists headers, env_file, requires_admin_ask.

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// TestListMCPServers_EnabledField verifies that GET /api/v1/mcp-servers populates
// the enabled field from config (G6).
//
// BDD:
//
//	Given a server "test-srv" configured with enabled=true in the in-memory config,
//	When GET /api/v1/mcp-servers is called,
//	Then the response entry for "test-srv" has enabled=true and status="disconnected"
//	     (no live manager in unit tests).
func TestListMCPServers_EnabledField(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	enabled := true
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"test-srv": {
						Enabled: enabled,
						Command: "echo",
						Type:    "stdio",
					},
				},
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil)
	api.HandleMCPServers(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var servers []gen.McpServer
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &servers))

	var found *gen.McpServer
	for i := range servers {
		if servers[i].Id == "test-srv" {
			found = &servers[i]
			break
		}
	}
	require.NotNil(t, found, "test-srv must appear in GET /api/v1/mcp-servers response")
	assert.Equal(t, gen.McpServerStatusDisconnected, found.Status,
		"status must be disconnected (no live manager in unit test)")
	require.NotNil(t, found.Enabled, "enabled field must be present")
	assert.True(t, *found.Enabled, "enabled must be true for a configured-enabled server")
}

// TestTestMCPServer_BogusServer verifies that POST /api/v1/mcp-servers/{id}/test
// returns HTTP 200 with success=false when the server is unreachable (G7).
//
// BDD:
//
//	Given a server "bogus-srv" configured with command="/nonexistent-binary",
//	When POST /api/v1/mcp-servers/bogus-srv/test is called,
//	Then HTTP 200 is returned and the body has success=false with a non-empty message.
func TestTestMCPServer_BogusServer(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Add a server whose binary does not exist so connection will fail.
	cmd := "/nonexistent-binary-for-mcp-test"
	transport := gen.McpServerCreateTransportStdio
	body := gen.McpServerCreate{
		Name:      "bogus-srv",
		Transport: transport,
		Command:   &cmd,
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	wPost := httptest.NewRecorder()
	rPost := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	rPost.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wPost, rPost)
	require.Equal(t, http.StatusCreated, wPost.Code)

	// Now call /test.
	wTest := httptest.NewRecorder()
	rTest := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers/bogus-srv/test", nil)
	api.HandleMCPServers(wTest, rTest)

	// Must be HTTP 200 — success=false is a valid business result, not an HTTP error.
	assert.Equal(t, http.StatusOK, wTest.Code,
		"testMCPServer must always return 200 (success=false is not an HTTP error): %s", wTest.Body.String())

	var resp gen.McpServerTestResponse
	require.NoError(t, json.Unmarshal(wTest.Body.Bytes(), &resp))
	assert.False(t, resp.Success, "success must be false for an unreachable server")
	assert.NotEmpty(t, resp.Message, "message must explain the failure")
}

// TestTestMCPServer_NotFound verifies that POST /api/v1/mcp-servers/{id}/test
// returns 404 when the server ID is not in config (G7).
//
// BDD:
//
//	Given no server named "nonexistent",
//	When POST /api/v1/mcp-servers/nonexistent/test is called,
//	Then 404 is returned.
func TestTestMCPServer_NotFound(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers/nonexistent/test", nil)
	api.HandleMCPServers(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"testMCPServer must return 404 for unknown server id: %s", w.Body.String())
}

// TestPatchMCPServer_MergePreservesOmittedFields verifies that PATCH only changes
// the supplied fields and preserves all others (G8).
//
// BDD:
//
//	Given a server "patch-srv" with command="npx" and enabled=true,
//	When PATCH /api/v1/mcp-servers/patch-srv with {enabled: false},
//	Then the persisted entry still has command="npx" and enabled=false.
func TestPatchMCPServer_MergePreservesOmittedFields(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Add a server.
	cmd := "npx"
	args := []string{"@modelcontextprotocol/server-everything", "everything"}
	transport := gen.McpServerCreateTransportStdio
	addBody := gen.McpServerCreate{
		Name:      "patch-srv",
		Transport: transport,
		Command:   &cmd,
		Args:      &args,
	}
	addBytes, err := json.Marshal(addBody)
	require.NoError(t, err)

	wAdd := httptest.NewRecorder()
	rAdd := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(addBytes))
	rAdd.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wAdd, rAdd)
	require.Equal(t, http.StatusCreated, wAdd.Code, "POST must return 201: %s", wAdd.Body.String())

	// PATCH only enabled=false — command and args must be preserved.
	disabled := false
	patchBody := gen.McpServerUpdate{
		Enabled: &disabled,
	}
	patchBytes, err := json.Marshal(patchBody)
	require.NoError(t, err)

	wPatch := httptest.NewRecorder()
	rPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/patch-srv", bytes.NewReader(patchBytes))
	rPatch.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wPatch, rPatch)
	require.Equal(t, http.StatusOK, wPatch.Code, "PATCH must return 200: %s", wPatch.Body.String())

	var patchResp gen.McpServer
	require.NoError(t, json.Unmarshal(wPatch.Body.Bytes(), &patchResp))
	require.NotNil(t, patchResp.Enabled)
	assert.False(t, *patchResp.Enabled, "enabled must be false after PATCH")

	// Verify persisted config contains both updated and preserved fields.
	configPath := api.homePath + "/config.json"
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(data, &cfg))

	tools, _ := cfg["tools"].(map[string]any)
	mcpSection, _ := tools["mcp"].(map[string]any)
	servers, _ := mcpSection["servers"].(map[string]any)
	entry, ok := servers["patch-srv"].(map[string]any)
	require.True(t, ok, "patch-srv must be present in persisted config")

	assert.Equal(t, false, entry["enabled"], "persisted enabled must be false")
	assert.Equal(t, "npx", entry["command"], "command must be preserved after PATCH")
}

// TestPatchMCPServer_NotFound verifies that PATCH returns 404 for unknown id (G8).
//
// BDD:
//
//	Given no server named "ghost",
//	When PATCH /api/v1/mcp-servers/ghost with {enabled: true},
//	Then 404 is returned.
func TestPatchMCPServer_NotFound(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	enabled := true
	body := gen.McpServerUpdate{Enabled: &enabled}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/ghost", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"PATCH on unknown server must return 404: %s", w.Body.String())
}

// TestAddMCPServer_PersistsHeaders verifies that POST /api/v1/mcp-servers
// persists headers, env_file, and requires_admin_ask into config (G9).
//
// BDD:
//
//	Given a new sse server with headers={"Authorization": "Bearer tok"},
//	       env_file="/etc/mcp.env", requires_admin_ask=["dangerous_tool"],
//	When POST /api/v1/mcp-servers is called,
//	Then the persisted config entry contains all three fields.
func TestAddMCPServer_PersistsHeaders(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	mcpURL := "https://mcp.example.com/sse"
	transport := gen.McpServerCreateTransportSse
	headers := map[string]string{"Authorization": "Bearer tok"}
	envFile := "/etc/mcp.env"
	adminAsk := []string{"dangerous_tool"}
	body := gen.McpServerCreate{
		Name:             "headed-srv",
		Transport:        transport,
		Url:              &mcpURL,
		Headers:          &headers,
		EnvFile:          &envFile,
		RequiresAdminAsk: &adminAsk,
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "POST must return 201: %s", w.Body.String())

	// Verify persisted config.
	data, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(data, &cfg))

	tools, _ := cfg["tools"].(map[string]any)
	mcpSection, _ := tools["mcp"].(map[string]any)
	servers, _ := mcpSection["servers"].(map[string]any)
	entry, ok := servers["headed-srv"].(map[string]any)
	require.True(t, ok, "headed-srv must be present in persisted config")

	// headers
	persistedHeaders, ok := entry["headers"].(map[string]any)
	require.True(t, ok, "headers must be persisted")
	assert.Equal(t, "Bearer tok", persistedHeaders["Authorization"])

	// env_file
	assert.Equal(t, "/etc/mcp.env", entry["env_file"], "env_file must be persisted")

	// requires_admin_ask
	rawAdminAsk, ok := entry["requires_admin_ask"].([]any)
	require.True(t, ok, "requires_admin_ask must be persisted as array")
	require.Len(t, rawAdminAsk, 1)
	assert.Equal(t, "dangerous_tool", rawAdminAsk[0])
}

// TestListMCPServers_ReportsToolCountFromRegistry proves the G6 wiring: tool_count
// in GET /api/v1/mcp-servers reflects the tools the server registered in the live
// MCP registry (not the old hardcoded 0). Status remains "disconnected" here
// because there is no live manager in a unit test (connected-status is covered by
// the live smoke).
func TestListMCPServers_ReportsToolCountFromRegistry(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"test-srv": {Enabled: true, Command: "echo", Type: "stdio"},
				},
			},
		},
	}
	require.NoError(t, os.WriteFile(tmpDir+"/config.json",
		[]byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`), 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// Seed the MCP registry with two tools owned by "test-srv".
	mcpReg := tools.NewMCPRegistry()
	builtins := tools.NewBuiltinRegistry()
	require.Empty(t, mcpReg.RegisterServerTools("test-srv", []tools.Tool{
		&registryMCPTestTool{name: "alpha"},
		&registryMCPTestTool{name: "beta"},
	}, builtins), "test MCP tool registration must not collide")

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
		mcpRegistry:   mcpReg,
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil)
	api.HandleMCPServers(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var servers []gen.McpServer
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &servers))
	var found *gen.McpServer
	for i := range servers {
		if servers[i].Id == "test-srv" {
			found = &servers[i]
			break
		}
	}
	require.NotNil(t, found, "test-srv must appear in the response")
	assert.Equal(t, 2, found.ToolCount,
		"tool_count must reflect the 2 tools registered for test-srv (G6 wiring)")
}

// TestListMCPServers_ReturnsNonSecretConfigForEdit proves #437: GET /mcp-servers
// returns the non-secret config fields for edit pre-fill (command/args/env_file/
// requires_admin_ask) and env/header KEYS — but never env/header VALUES (secrets).
func TestListMCPServers_ReturnsNonSecretConfigForEdit(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"srv": {
						Enabled:          true,
						Type:             "stdio",
						Command:          "npx",
						Args:             []string{"server-everything"},
						EnvFile:          "/etc/mcp.env",
						RequiresAdminAsk: []string{"danger"},
						Env:              map[string]string{"API_KEY": "supersecretvalue"},
						Headers:          map[string]string{"Authorization": "Bearer tok-secret"},
					},
				},
			},
		},
	}
	require.NoError(t, os.WriteFile(tmpDir+"/config.json",
		[]byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`), 0o600))
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, allowedOrigin: "http://localhost:3000", homePath: tmpDir}

	w := httptest.NewRecorder()
	api.HandleMCPServers(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Secret VALUES must never appear in the response.
	assert.NotContains(t, body, "supersecretvalue", "env values must not be returned")
	assert.NotContains(t, body, "tok-secret", "header values must not be returned")

	var servers []gen.McpServer
	require.NoError(t, json.Unmarshal([]byte(body), &servers))
	require.Len(t, servers, 1)
	s := servers[0]
	require.NotNil(t, s.Command)
	assert.Equal(t, "npx", *s.Command)
	require.NotNil(t, s.EnvFile)
	assert.Equal(t, "/etc/mcp.env", *s.EnvFile)
	require.NotNil(t, s.EnvKeys)
	assert.Equal(t, []string{"API_KEY"}, *s.EnvKeys, "env keys returned, values hidden")
	require.NotNil(t, s.HeaderNames)
	assert.Equal(t, []string{"Authorization"}, *s.HeaderNames, "header names returned, values hidden")
}

// TestPatchMCPServer_RejectsTransportMismatch proves the PATCH transport-consistency
// guard: setting a url on a stdio server is 422.
func TestPatchMCPServer_RejectsTransportMismatch(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"srv": {Enabled: true, Type: "stdio", Command: "echo"},
			}},
		},
	}
	require.NoError(t, os.WriteFile(tmpDir+"/config.json",
		[]byte(`{"version":1,"tools":{"mcp":{"servers":{"srv":{"type":"stdio","command":"echo","enabled":true}}}},"agents":{"defaults":{},"list":[]},"providers":[]}`), 0o600))
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, allowedOrigin: "http://localhost:3000", homePath: tmpDir}

	body := bytes.NewReader([]byte(`{"url":"https://mcp.example.com/sse"}`))
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/srv", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleMCPServers(w, r)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"setting a url on a stdio server must be 422; body=%s", w.Body.String())
}
