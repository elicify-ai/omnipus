//go:build !cgo

// Tests for Epic #314 cleanup-be fixes:
//   - Fix #3: MCP server URL scheme backend validation (addMCPServer).
//   - Fix #5: validateSkillIDs fail-open when no skills are installed.

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
)

// --- Fix #3: MCP server POST URL-scheme validation ---

// TestAddMCPServer_SSE_ValidHTTPS verifies that POST /api/v1/mcp-servers with
// transport=sse and a valid https:// URL returns 201 and persists the url field
// in tools.mcp_servers. This guards the exact regression where the url value
// was not being stored.
//
// BDD:
//
//	Given a running gateway with a writable config,
//	When POST /api/v1/mcp-servers with transport=sse and url="https://mcp.example.com/sse",
//	Then 201 is returned and tools.mcp.servers[name].url == "https://mcp.example.com/sse".
func TestAddMCPServer_SSE_ValidHTTPS(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	transport := gen.McpServerCreateTransportSse
	mcpURL := "https://mcp.example.com/sse"
	body := gen.McpServerCreate{
		Name:      "my-sse-server",
		Transport: transport,
		Url:       &mcpURL,
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)

	require.Equal(t, http.StatusCreated, w.Code,
		"POST with valid https url must return 201: %s", w.Body.String())

	// Verify the persisted config contains the url field.
	configPath := api.homePath + "/config.json"
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, json.Unmarshal(data, &cfg))

	tools, _ := cfg["tools"].(map[string]any)
	require.NotNil(t, tools, "config must have tools section after MCP server add")

	mcp, _ := tools["mcp"].(map[string]any)
	require.NotNil(t, mcp, "config must have tools.mcp section")

	servers, _ := mcp["servers"].(map[string]any)
	require.NotNil(t, servers, "config must have tools.mcp.servers section")

	entry, ok := servers["my-sse-server"].(map[string]any)
	require.True(t, ok, "my-sse-server entry must be present in persisted config")

	assert.Equal(t, mcpURL, entry["url"],
		"persisted tools.mcp.servers[my-sse-server].url must match the submitted URL")
	assert.Equal(t, "sse", entry["type"],
		"persisted entry type must be 'sse'")
}

// TestAddMCPServer_SSE_NoURL verifies that POST with transport=sse but no url
// returns 422.
//
// BDD:
//
//	Given a running gateway,
//	When POST /api/v1/mcp-servers with transport=sse and no url,
//	Then 422 Unprocessable Entity is returned.
func TestAddMCPServer_SSE_NoURL(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	transport := gen.McpServerCreateTransportSse
	body := gen.McpServerCreate{
		Name:      "no-url-server",
		Transport: transport,
		// Url deliberately omitted.
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"POST with sse transport and no url must return 422")
}

// TestAddMCPServer_SSE_NonHTTPS_NonLoopback verifies that POST with transport=sse
// and a plain http:// URL pointing to a non-loopback host returns 422.
// This is the key security regression guard: the SPA validation can be bypassed
// by calling the API directly without this backend check.
//
// BDD:
//
//	Given a running gateway,
//	When POST /api/v1/mcp-servers with transport=sse and url="http://evil.example.com/sse",
//	Then 422 Unprocessable Entity is returned.
func TestAddMCPServer_SSE_NonHTTPS_NonLoopback(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	transport := gen.McpServerCreateTransportSse
	mcpURL := "http://evil.example.com/sse"
	body := gen.McpServerCreate{
		Name:      "plain-http-server",
		Transport: transport,
		Url:       &mcpURL,
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"POST with plain http (non-loopback) url must return 422: %s", w.Body.String())
}

// TestAddMCPServer_SSE_HTTP_Loopback verifies that POST with transport=sse and
// an http:// loopback URL is accepted (returns 201).
//
// BDD:
//
//	Given a running gateway,
//	When POST /api/v1/mcp-servers with transport=sse and url="http://localhost:3000/sse",
//	Then 201 is returned (loopback http is whitelisted).
func TestAddMCPServer_SSE_HTTP_Loopback(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	transport := gen.McpServerCreateTransportSse
	mcpURL := "http://localhost:3000/sse"
	body := gen.McpServerCreate{
		Name:      "loopback-server",
		Transport: transport,
		Url:       &mcpURL,
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)

	assert.Equal(t, http.StatusCreated, w.Code,
		"POST with http://localhost must return 201 (loopback is whitelisted): %s", w.Body.String())
}

// TestAddMCPServer_Stdio_NoCommand verifies that POST with transport=stdio but
// no command returns 422.
//
// BDD:
//
//	Given a running gateway,
//	When POST /api/v1/mcp-servers with transport=stdio and no command,
//	Then 422 Unprocessable Entity is returned.
func TestAddMCPServer_Stdio_NoCommand(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	transport := gen.McpServerCreateTransportStdio
	body := gen.McpServerCreate{
		Name:      "no-cmd-server",
		Transport: transport,
		// Command deliberately omitted.
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"POST with stdio transport and no command must return 422")
}

// --- Fix #5: validateSkillIDs fail-open when no skills are installed ---

// TestCreateAgent_WithSkillID_NoInstalledSkills verifies the deliberate
// fail-open behavior: when the installed-skills set is empty (fresh install,
// test environment with no skills directory), POST /api/v1/agents with an
// arbitrary skill id returns 201. The runtime filter in the agent loop is the
// final enforcement gate; the REST layer must not block agents on unknown skill
// ids when it cannot tell what is installed.
//
// BDD:
//
//	Given a running gateway with no installed skills directory,
//	When POST /api/v1/agents with skills=["some-unknown-skill"],
//	Then 201 is returned (fail-open: no installed set → accept any id).
func TestCreateAgent_WithSkillID_NoInstalledSkills(t *testing.T) {
	// Isolate from any real ~/.omnipus/skills directory and from the working-dir
	// skills (if any). This gives the "no skills installed" fail-open scenario.
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	t.Setenv("OMNIPUS_BUILTIN_SKILLS", t.TempDir())
	// newTestRestAPIWithHome uses an empty tmpDir as agentLoop's config, which
	// provides no skills — exactly the "no skills directory" scenario.
	api := newTestRestAPIWithHome(t)

	skillID := "some-unknown-skill"
	skills := []string{skillID}
	body := map[string]any{
		"name":   "skill-test-agent",
		"soul":   "s",
		"skills": skills,
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusCreated, w.Code,
		"createAgent must return 201 (fail-open) when no skills are installed: %s", w.Body.String())
}
