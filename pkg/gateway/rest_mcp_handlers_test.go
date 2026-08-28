// Tests for MCP backend handlers G6–G9.
//
//   - G6: listMCPServers reflects live manager status + tool_count + enabled.
//   - G7: testMCPServer returns success=false gracefully for an unreachable server.
//   - G8: patchMCPServer merges partial update (omitted fields preserved, enabled toggled).
//   - G9: addMCPServer persists headers, env_file.

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
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
//	Given a server "patch-srv" with a command and args set, and enabled=true,
//	When PATCH /api/v1/mcp-servers/patch-srv with {enabled: false},
//	Then the persisted entry still has the original command and enabled=false.
func TestPatchMCPServer_MergePreservesOmittedFields(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Add a server. Command is a nonexistent binary (not a real npx package)
	// so live reconciliation (triggered by the POST below) fails instantly
	// instead of attempting to download/spawn a real npx package — this test
	// asserts merge semantics, not connectivity.
	cmd := "/nonexistent-mcp-test-patch-bin"
	args := []string{"--foo", "everything"}
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
	assert.Equal(t, "/nonexistent-mcp-test-patch-bin", entry["command"], "command must be preserved after PATCH")
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

// TestPatchMCPServer_EnabledTruePatchFlipsGlobalKillSwitch proves E11: an
// explicit PATCH {enabled: true} also flips the global tools.mcp.enabled
// kill-switch on in the same write — closing the upgraded-install trap where
// an operator re-enables a server, Test succeeds, but nothing ever connects
// because the global flag itself was never on. PATCH {enabled: false} and a
// patch that doesn't set "enabled" at all must leave the global flag alone.
func TestPatchMCPServer_EnabledTruePatchFlipsGlobalKillSwitch(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", []byte(
		`{"version":1,"tools":{"mcp":{"enabled":false,"servers":{`+
			`"srv-a":{"type":"stdio","command":"echo","enabled":false},`+
			`"srv-b":{"type":"stdio","command":"echo","enabled":true}`+
			`}}},"agents":{"defaults":{},"list":[]},"providers":[]}`,
	), 0o600))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"srv-a": {Enabled: false, Type: "stdio", Command: "echo"},
					"srv-b": {Enabled: true, Type: "stdio", Command: "echo"},
				},
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, allowedOrigin: "http://localhost:3000", homePath: tmpDir}

	readGlobalEnabled := func(t *testing.T) bool {
		t.Helper()
		data, err := os.ReadFile(tmpDir + "/config.json")
		require.NoError(t, err)
		var raw map[string]any
		require.NoError(t, json.Unmarshal(data, &raw))
		toolsSection, _ := raw["tools"].(map[string]any)
		mcpSection, _ := toolsSection["mcp"].(map[string]any)
		enabled, _ := mcpSection["enabled"].(bool)
		return enabled
	}
	require.False(t, readGlobalEnabled(t), "precondition: global flag starts false")

	// A patch that doesn't touch "enabled" must leave the global flag alone.
	args := []string{"--verbose"}
	otherPatch := gen.McpServerUpdate{Args: &args}
	otherBytes, err := json.Marshal(otherPatch)
	require.NoError(t, err)
	wOther := httptest.NewRecorder()
	rOther := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/srv-b", bytes.NewReader(otherBytes))
	rOther.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wOther, rOther)
	require.Equal(t, http.StatusOK, wOther.Code, "PATCH must return 200: %s", wOther.Body.String())
	assert.False(t, readGlobalEnabled(t), "a patch that doesn't set enabled must not touch the global flag")

	// PATCH {enabled: false} must not touch the global flag either.
	disabled := false
	offPatch := gen.McpServerUpdate{Enabled: &disabled}
	offBytes, err := json.Marshal(offPatch)
	require.NoError(t, err)
	wOff := httptest.NewRecorder()
	rOff := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/srv-b", bytes.NewReader(offBytes))
	rOff.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wOff, rOff)
	require.Equal(t, http.StatusOK, wOff.Code, "PATCH must return 200: %s", wOff.Body.String())
	assert.False(t, readGlobalEnabled(t), "PATCH {enabled:false} must not flip the global flag on")

	// PATCH {enabled: true} on the other server must flip the global flag on.
	enabled := true
	onPatch := gen.McpServerUpdate{Enabled: &enabled}
	onBytes, err := json.Marshal(onPatch)
	require.NoError(t, err)
	wOn := httptest.NewRecorder()
	rOn := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/srv-a", bytes.NewReader(onBytes))
	rOn.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wOn, rOn)
	require.Equal(t, http.StatusOK, wOn.Code, "PATCH must return 200: %s", wOn.Body.String())
	assert.True(t, readGlobalEnabled(t), "PATCH {enabled:true} must flip the global kill-switch on")
}

// TestAddMCPServer_PersistsHeaders verifies that POST /api/v1/mcp-servers
// persists headers and env_file into config (G9).
//
// BDD:
//
//	Given a new sse server with headers={"Authorization": "Bearer tok"},
//	       env_file="/etc/mcp.env",
//	When POST /api/v1/mcp-servers is called,
//	Then the persisted config entry contains both fields.
func TestAddMCPServer_PersistsHeaders(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Loopback port 1 is a valid https URL per mcpURLSchemeValid (https is
	// always accepted regardless of host) but nothing listens there, so the
	// live reconciliation triggered by the POST below fails instantly instead
	// of attempting real egress to an external host — this test asserts
	// config persistence, not connectivity.
	mcpURL := "https://127.0.0.1:1/sse"
	transport := gen.McpServerCreateTransportSse
	headers := map[string]string{"Authorization": "Bearer tok"}
	envFile := "/etc/mcp.env"
	body := gen.McpServerCreate{
		Name:      "headed-srv",
		Transport: transport,
		Url:       &mcpURL,
		Headers:   &headers,
		EnvFile:   &envFile,
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
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
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
// returns the non-secret config fields for edit pre-fill (command/args/env_file)
// and env/header KEYS — but never env/header VALUES (secrets).
func TestListMCPServers_ReturnsNonSecretConfigForEdit(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"srv": {
						Enabled: true,
						Type:    "stdio",
						Command: "npx",
						Args:    []string{"server-everything"},
						EnvFile: "/etc/mcp.env",
						Env:     map[string]string{"API_KEY": "supersecretvalue"},
						Headers: map[string]string{"Authorization": "Bearer tok-secret"},
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
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"srv": {Enabled: true, Type: "stdio", Command: "echo"},
			}},
		},
	}
	require.NoError(t, os.WriteFile(
		tmpDir+"/config.json",
		[]byte(
			`{"version":1,"tools":{"mcp":{"servers":{"srv":{"type":"stdio","command":"echo","enabled":true}}}},"agents":{"defaults":{},"list":[]},"providers":[]}`,
		),
		0o600,
	))
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

// TestPatchMCPServer_RejectsCommandOnRemote proves the symmetric transport-consistency
// guard: setting a command on an sse/http server is 422 (the second, previously
// untested guard direction).
func TestPatchMCPServer_RejectsCommandOnRemote(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
				"remote": {Enabled: true, Type: "sse", URL: "https://mcp.example.com/sse"},
			}},
		},
	}
	require.NoError(t, os.WriteFile(
		tmpDir+"/config.json",
		[]byte(
			`{"version":1,"tools":{"mcp":{"servers":{"remote":{"type":"sse","url":"https://mcp.example.com/sse","enabled":true}}}},"agents":{"defaults":{},"list":[]},"providers":[]}`,
		),
		0o600,
	))
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, allowedOrigin: "http://localhost:3000", homePath: tmpDir}

	body := bytes.NewReader([]byte(`{"command":"evil"}`))
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/remote", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleMCPServers(w, r)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"setting a command on an sse server must be 422; body=%s", w.Body.String())
}

// TestAddMCPServer_LiveReconcileReportsErrorStatus proves that after
// the config write, addMCPServer live-reconciles the MCP manager
// (AgentLoop.ReconcileMCP) synchronously, so a server whose command cannot be
// spawned reports status "error" (not the old hardcoded "disconnected") straight
// from the 201 response, and a subsequent GET reflects the same live state.
//
// BDD:
//
//	Given a new stdio server whose command does not exist on disk,
//	When POST /api/v1/mcp-servers is called,
//	Then HTTP 201 is returned with status="error" and tool_count=0,
//	And a subsequent GET /api/v1/mcp-servers also reports status="error" for it.
func TestAddMCPServer_LiveReconcileReportsErrorStatus(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	cmd := "/nonexistent-mcp-test-bin"
	transport := gen.McpServerCreateTransportStdio
	body := gen.McpServerCreate{
		Name:      "unreachable-srv",
		Transport: transport,
		Command:   &cmd,
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	wAdd := httptest.NewRecorder()
	rAdd := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	rAdd.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wAdd, rAdd)
	require.Equal(t, http.StatusCreated, wAdd.Code, "POST must return 201: %s", wAdd.Body.String())

	var addResp gen.McpServer
	require.NoError(t, json.Unmarshal(wAdd.Body.Bytes(), &addResp))
	assert.Equal(
		t,
		gen.McpServerStatusError,
		addResp.Status,
		"a server whose command cannot be spawned must report status=error after live reconcile, not the old hardcoded disconnected",
	)
	assert.Equal(t, 0, addResp.ToolCount, "a server that failed to connect must report tool_count=0")

	// Subsequent GET must reflect the SAME live status — listMCPServers and
	// addMCPServer must not disagree (both go through mcpLiveStatus).
	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil)
	api.HandleMCPServers(wList, rList)
	require.Equal(t, http.StatusOK, wList.Code)

	var servers []gen.McpServer
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &servers))
	var found *gen.McpServer
	for i := range servers {
		if servers[i].Id == "unreachable-srv" {
			found = &servers[i]
			break
		}
	}
	require.NotNil(t, found, "unreachable-srv must appear in GET /api/v1/mcp-servers response")
	assert.Equal(t, gen.McpServerStatusError, found.Status,
		"GET list must report the same error status as the POST response")
}

// TestPatchMCPServer_LiveReconcileReportsErrorStatus proves patchMCPServer also
// runs live reconciliation after the config write: editing a server's command to
// a nonexistent binary flips its reported status to "error" (previously
// hardcoded "disconnected" regardless of the live outcome).
//
// BDD:
//
//	Given a server "patch-reconcile-srv" originally configured with command="echo",
//	When PATCH sets command to a nonexistent binary,
//	Then the response reports status="error" (live reconcile ran and the new
//	     command failed to connect), not the old hardcoded "disconnected".
func TestPatchMCPServer_LiveReconcileReportsErrorStatus(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	cmd := "echo"
	transport := gen.McpServerCreateTransportStdio
	addBody := gen.McpServerCreate{
		Name:      "patch-reconcile-srv",
		Transport: transport,
		Command:   &cmd,
	}
	addBytes, err := json.Marshal(addBody)
	require.NoError(t, err)

	wAdd := httptest.NewRecorder()
	rAdd := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(addBytes))
	rAdd.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wAdd, rAdd)
	require.Equal(t, http.StatusCreated, wAdd.Code, "POST must return 201: %s", wAdd.Body.String())

	badCmd := "/nonexistent-mcp-test-bin"
	patchBody := gen.McpServerUpdate{Command: &badCmd}
	patchBytes, err := json.Marshal(patchBody)
	require.NoError(t, err)

	wPatch := httptest.NewRecorder()
	rPatch := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/mcp-servers/patch-reconcile-srv",
		bytes.NewReader(patchBytes),
	)
	rPatch.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wPatch, rPatch)
	require.Equal(t, http.StatusOK, wPatch.Code, "PATCH must return 200: %s", wPatch.Body.String())

	var patchResp gen.McpServer
	require.NoError(t, json.Unmarshal(wPatch.Body.Bytes(), &patchResp))
	assert.Equal(t, gen.McpServerStatusError, patchResp.Status,
		"PATCH to a nonexistent command must report status=error after live reconcile")
}

// TestDeleteMCPServer_ReconcilesLiveManager proves deleteMCPServer runs live
// reconciliation after the config write: the handler must complete successfully
// (ReconcileMCP does not error out the request) and the removed server must no
// longer be present in a subsequent GET list.
//
// Deeper proof that the live manager actually disconnected/evicted a REAL
// connection (vs. simply no longer appearing because it left cfg.Tools.MCP.Servers)
// would require a genuinely connected server — i.e. the pkg/mcp stub_mcp_server
// test binary — which is too heavy for this gateway-package unit test; that
// end-to-end connect/disconnect proof is left to QA's integration coverage.
//
// BDD:
//
//	Given a server "delete-reconcile-srv" added (and reconciled to status=error,
//	     since its command cannot be spawned),
//	When DELETE /api/v1/mcp-servers/delete-reconcile-srv is called,
//	Then HTTP 200 is returned,
//	And a subsequent GET /api/v1/mcp-servers no longer contains it.
func TestDeleteMCPServer_ReconcilesLiveManager(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	cmd := "/nonexistent-mcp-test-bin"
	transport := gen.McpServerCreateTransportStdio
	addBody := gen.McpServerCreate{
		Name:      "delete-reconcile-srv",
		Transport: transport,
		Command:   &cmd,
	}
	addBytes, err := json.Marshal(addBody)
	require.NoError(t, err)

	wAdd := httptest.NewRecorder()
	rAdd := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(addBytes))
	rAdd.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wAdd, rAdd)
	require.Equal(t, http.StatusCreated, wAdd.Code, "POST must return 201: %s", wAdd.Body.String())

	wDelete := httptest.NewRecorder()
	rDelete := httptest.NewRequest(http.MethodDelete, "/api/v1/mcp-servers/delete-reconcile-srv", nil)
	api.HandleMCPServers(wDelete, rDelete)
	require.Equal(t, http.StatusOK, wDelete.Code,
		"DELETE must return 200 even though live reconcile runs post-write: %s", wDelete.Body.String())

	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil)
	api.HandleMCPServers(wList, rList)
	require.Equal(t, http.StatusOK, wList.Code)

	var servers []gen.McpServer
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &servers))
	for _, s := range servers {
		assert.NotEqual(t, "delete-reconcile-srv", s.Id,
			"deleted server must not appear in GET /api/v1/mcp-servers after live reconcile")
	}
}

// buildStubMCPServer compiles pkg/mcp/testdata/stub_mcp_server (a real,
// minimal stdio MCP server — see pkg/mcp/stub_mcp_server_test.go's
// buildStubServer, which builds the identical binary for pkg/mcp's own
// tests) and returns the path to the resulting binary. Building a genuinely
// connectable server — rather than only asserting the failure path — lets
// TestTestMCPServer_SuccessHealsDisconnectedEnabledServer prove the heal
// end-to-end: a real successful /test call actually reconciles the live
// manager, not just a mocked "success" response.
func buildStubMCPServer(t *testing.T) string {
	t.Helper()

	cwd, err := os.Getwd()
	require.NoError(t, err, "buildStubMCPServer: getwd")
	// pkg/gateway is this package's directory during `go test`; the stub
	// server source lives under the sibling pkg/mcp package's testdata.
	srcDir := filepath.Join(cwd, "..", "mcp", "testdata", "stub_mcp_server")
	_, err = os.Stat(filepath.Join(srcDir, "main.go"))
	require.NoError(t, err, "buildStubMCPServer: stub source not found at %s", srcDir)

	outDir := t.TempDir()
	binPath := filepath.Join(outDir, "stub_mcp_server")

	cmd := exec.Command("go", "build", "-o", binPath, srcDir)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, buildErr := cmd.CombinedOutput()
	require.NoError(t, buildErr, "buildStubMCPServer: go build failed:\n%s", out)

	return binPath
}

// mcpToolByPrefix returns the Name() of the first tool registered on inst
// whose name starts with prefix, or "" if none matches. Used to find the
// sanitized/hashed name an MCPTool ends up with (tools.NewMCPTool.Name()
// appends a hash suffix whenever sanitization is lossy, e.g. "." in an MCP
// tool name like "stub.echo") without hardcoding that derivation here.
func mcpToolByPrefix(inst *agent.AgentInstance, prefix string) string {
	for _, tl := range inst.Tools.GetAll() {
		if strings.HasPrefix(tl.Name(), prefix) {
			return tl.Name()
		}
	}
	return ""
}

// TestTestMCPServer_SuccessHealsDisconnectedEnabledServer proves that a
// SUCCESSFUL POST /test for a server that is enabled in config but not yet
// connected in the live manager triggers a real AgentLoop.ReconcileMCP pass,
// bringing the live connection in line with what the test just proved
// reachable — rather than leaving the operator to separately toggle
// enabled off/on to force a reconnect.
//
// BDD:
//
//	Given a server "heal-srv" enabled in config, pointing at a real (stub)
//	     MCP binary, but never reconciled (MCPServerStatus reports
//	     "disconnected"),
//	When POST /api/v1/mcp-servers/heal-srv/test succeeds,
//	Then the response reports success=true with the stub's 2 tools,
//	And a.agentLoop.MCPServerStatus("heal-srv") now reports "connected",
//	And calling the healed server's stub.echo tool through the default
//	     agent's live tool registry actually succeeds and echoes the
//	     argument back — proving the underlying *mcp.Manager connection is
//	     itself usable, not merely that the status map says "connected"
//	     (the throwaway /test connection is torn down by this handler's own
//	     deferred cleanup only after the reconcile above already established
//	     the real, independent connection).
func TestTestMCPServer_SuccessHealsDisconnectedEnabledServer(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	binPath := buildStubMCPServer(t)
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			// This test asks the registry for the default agent and requires
			// it: MCP tools are registered onto that agent. There is no
			// implicit "main" sentinel to be it any more (ADR-064).
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				// Global kill-switch on, matching what a real add-server flow
				// would set — this test targets the /test heal gate
				// specifically, not the kill-switch itself.
				ToolConfig: config.ToolConfig{Enabled: true},
				Servers: map[string]config.MCPServerConfig{
					"heal-srv": {Enabled: true, Command: binPath, Type: "stdio"},
				},
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, allowedOrigin: "http://localhost:3000", homePath: tmpDir}

	// Precondition: never reconciled, so the live manager doesn't have it yet.
	preStatus, _, _ := al.MCPServerStatus("heal-srv")
	require.Equal(t, "disconnected", preStatus,
		"precondition: server must not be live before the /test call")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers/heal-srv/test", nil)
	api.HandleMCPServers(w, r)
	require.Equal(t, http.StatusOK, w.Code, "POST /test must return 200: %s", w.Body.String())

	var resp gen.McpServerTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Success, "test against a real stub server must succeed: %s", resp.Message)
	require.NotNil(t, resp.ToolCount)
	assert.Equal(t, 2, *resp.ToolCount, "stub server advertises 2 tools")
	require.NotNil(t, resp.Tools)
	assert.Equal(t, []string{"stub.echo", "stub.noop"}, *resp.Tools)

	// The heal: the successful test must have triggered a real reconcile, so
	// the live manager now has the server connected — proving this is a real
	// heal and not just an artifact of the throwaway /test connection, which
	// this handler's own deferred cleanup closes only once the reconcile
	// above already established the real, independent connection.
	postStatus, _, _ := al.MCPServerStatus("heal-srv")
	assert.Equal(t, "connected", postStatus,
		"a successful test on an enabled-but-not-live server must heal it into the live manager")

	// Liveness proof: MCPServerStatus reporting "connected" only proves the
	// manager's server map has an entry for the name — it would report the
	// same even if the stdio child were killed moments after connecting (the
	// register-then-kill lifetime class of bug pkg/mcp's own connect-ctx
	// tests guard against). Calling the healed tool through the same live
	// manager the reconcile registered it into closes that gap: a dead child
	// fails this call even though the status above still says "connected".
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "default agent must exist for tool execution")
	echoToolName := mcpToolByPrefix(defaultAgent, "mcp_heal-srv_")
	require.NotEmpty(t, echoToolName,
		"heal-srv's tools must be registered on the default agent after the heal reconcile")

	callResult := defaultAgent.Tools.Execute(context.Background(), echoToolName,
		map[string]any{"message": "liveness-check"})
	require.NotNil(t, callResult)
	assert.False(t, callResult.IsError,
		"calling the healed server's tool through the live manager must succeed: %s", callResult.ForLLM)
	assert.Contains(t, callResult.ForLLM, "liveness-check",
		"the stub server must echo the message argument back through the live connection")
}

// TestTestMCPServer_FailureDoesNotHeal proves the heal gate is
// one-directional: a FAILED test (server unreachable) must never trigger
// live reconciliation — neither for the server under test, nor as a
// side effect for any OTHER enabled server. Unlike
// TestTestMCPServer_SuccessHealsDisconnectedEnabledServer (which proves the
// positive case with a real, connectable stub server), the primary negative
// case only needs an unreachable command — a nonexistent binary is
// sufficient there. The second, perfectly reachable stub server exists
// purely to catch a heal-on-failure regression that reconciles the whole
// desired set instead of doing nothing: that bug would connect it even
// though the failing /test call targeted a different server entirely.
//
// BDD:
//
//	Given a server "heal-fail-srv" added with a nonexistent command (so
//	     addMCPServer's own reconcile pass already recorded a connect
//	     error — status "error"), and a second server
//	     "heal-fail-control-srv" enabled and pointing at a real (stub) MCP
//	     binary, added directly to config so it starts genuinely
//	     un-reconciled,
//	When POST /api/v1/mcp-servers/heal-fail-srv/test is called and fails,
//	Then the response reports success=false,
//	And the live status/error recorded for heal-fail-srv is UNCHANGED by
//	     the failed test — no reconnect attempt was triggered,
//	And heal-fail-control-srv — despite being reachable — remains
//	     "disconnected" too.
func TestTestMCPServer_FailureDoesNotHeal(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	stubBinPath := buildStubMCPServer(t)

	cmd := "/nonexistent-mcp-test-heal-bin"
	transport := gen.McpServerCreateTransportStdio
	addBody := gen.McpServerCreate{
		Name:      "heal-fail-srv",
		Transport: transport,
		Command:   &cmd,
	}
	addBytes, err := json.Marshal(addBody)
	require.NoError(t, err)

	wAdd := httptest.NewRecorder()
	rAdd := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(addBytes))
	rAdd.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wAdd, rAdd)
	require.Equal(t, http.StatusCreated, wAdd.Code, "POST must return 201: %s", wAdd.Body.String())

	// Precondition established by addMCPServer's own reconcile pass (a
	// sibling path, not what this test targets): the server is already live
	// "error" since the command cannot be spawned.
	preStatus, _, preErr := api.agentLoop.MCPServerStatus("heal-fail-srv")
	require.Equal(t, "error", preStatus)
	require.NotEmpty(t, preErr)

	// A second, perfectly reachable enabled server, added directly to the
	// live config (bypassing addMCPServer's own POST-triggered reconcile,
	// which would connect it immediately and defeat the assertion below) so
	// it starts this test genuinely un-reconciled.
	require.NoError(t, api.agentLoop.MutateConfig(func(c *config.Config) error {
		c.Tools.MCP.Servers["heal-fail-control-srv"] = config.MCPServerConfig{
			Enabled: true,
			Command: stubBinPath,
			Type:    "stdio",
		}
		return nil
	}))
	preControlStatus, _, _ := api.agentLoop.MCPServerStatus("heal-fail-control-srv")
	require.Equal(t, "disconnected", preControlStatus,
		"precondition: the control server must not be live before the /test call")

	wTest := httptest.NewRecorder()
	rTest := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers/heal-fail-srv/test", nil)
	api.HandleMCPServers(wTest, rTest)
	require.Equal(t, http.StatusOK, wTest.Code)

	var resp gen.McpServerTestResponse
	require.NoError(t, json.Unmarshal(wTest.Body.Bytes(), &resp))
	assert.False(t, resp.Success, "test against a nonexistent binary must fail")

	// The gate: a FAILED test must not have triggered reconciliation, so the
	// live status is exactly what it was before /test ran — the endpoint made
	// no state change on the failure path.
	postStatus, _, postErr := api.agentLoop.MCPServerStatus("heal-fail-srv")
	assert.Equal(t, "error", postStatus, "a failed test must not change live status")
	assert.Equal(t, preErr, postErr, "a failed test must not re-trigger reconcile / change the recorded error")

	// A heal-on-failure regression would reconcile the WHOLE desired set, not
	// just the server under test — which would connect this otherwise-healthy
	// control server as a side effect of a failed /test on a different server.
	postControlStatus, _, _ := api.agentLoop.MCPServerStatus("heal-fail-control-srv")
	assert.Equal(t, "disconnected", postControlStatus,
		"a failed test must not reconcile an unrelated, otherwise-healthy enabled server either")
}
