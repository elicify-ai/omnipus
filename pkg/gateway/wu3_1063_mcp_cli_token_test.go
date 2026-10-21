// wu3_1063_mcp_cli_token_test.go: #1063 F2 (WU-3 ruling Q3) — a CLI token is
// accepted by GET /api/v1/mcp-servers through the real auth wrapper.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestWU3_1063_MCPServersGET_AcceptsCLIToken(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	plainToken := "omnipus_" + strings.Repeat("c", 64)
	hash, err := bcrypt.GenerateFromPassword([]byte(plainToken), bcrypt.MinCost)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080,
			CLIToken: &config.TokenEntry{Hash: config.BcryptHash(hash)}},
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096}},
	}
	seedTestAgents(cfg)
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"docs": {Enabled: true, Type: "stdio", Command: "true"},
	}
	api := &restAPI{
		agentLoop:     mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{}),
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}
	handler := api.withAuth(api.HandleMCPServers)

	// Control: no credential is refused, so a 200 below is the token's doing.
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code, "instrument: an unauthenticated GET must be refused")

	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil)
	r.Header.Set("Authorization", "Bearer "+plainToken)
	handler(w, r)
	require.Equal(t, http.StatusOK, w.Code, "#1063 F2: the CLI token must be accepted: %s", w.Body.String())
	var servers []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &servers), "body must be a McpServer array: %s", w.Body.String())
	require.Len(t, servers, 1)
	require.Equal(t, "docs", servers[0]["name"])
}
