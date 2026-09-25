// Issue #638: an MCP server's Authorization bearer (and the other secret
// header names the issue names: Proxy-Authorization, Cookie) is never stored
// in config.json and never returned by GET /api/v1/config.
//
// The secret is not deleted. #638 requires it to resolve from the encrypted
// credential store at connect time, the same way env values already do. The
// ref field's exact name is not fixed ("headers_ref or per-header ref"), so
// this test accepts any stored credential whose value is the secret.

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

const (
	mcpAuthSecret   = "gwsec-638-auth-sentinel"
	mcpProxySecret  = "gwsec-638-proxy-sentinel"
	mcpCookieSecret = "gwsec-638-cookie-sentinel"
)

func mcpSecretHeaders() map[string]string {
	return map[string]string{
		"Authorization":       "Bearer " + mcpAuthSecret,
		"Proxy-Authorization": "Bearer " + mcpProxySecret,
		"Cookie":              "session=" + mcpCookieSecret,
	}
}

func assertMCPSecretsAbsent(t *testing.T, body, where string) {
	t.Helper()
	for _, secret := range []string{mcpAuthSecret, mcpProxySecret, mcpCookieSecret} {
		assert.NotContains(t, body, secret,
			"issue #638: %s must not contain MCP header secret %q", where, secret)
	}
}

func credentialStoreHolds(t *testing.T, store *credentials.Store, secret string) bool {
	t.Helper()
	names, err := store.List()
	require.NoError(t, err)
	for _, name := range names {
		val, err := store.Get(name)
		if err != nil {
			continue
		}
		if val == secret || bytes.Contains([]byte(val), []byte(secret)) {
			return true
		}
	}
	return false
}

func TestGetConfig_MCPAuthorizationBearer_IsAbsent(t *testing.T) {
	api, _ := newTestRestAPI(t)
	cfg := api.agentLoop.GetConfig()
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"remote": {
			Enabled: true,
			Type:    "http",
			URL:     "https://127.0.0.1:1/mcp",
			Headers: mcpSecretHeaders(),
		},
	}

	w := httptest.NewRecorder()
	api.HandleConfig(w, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assertMCPSecretsAbsent(t, w.Body.String(), "GET /api/v1/config")
}

func TestAddMCPServer_AuthorizationBearer_NotInConfigJSON(t *testing.T) {
	api, store := newTestRestAPIWithHomeAndCredStore(t)
	mcpURL := "https://127.0.0.1:1/sse"
	headers := mcpSecretHeaders()
	body := gen.McpServerCreate{
		Name:      "remote-sec",
		Transport: gen.McpServerCreateTransportSse,
		Url:       &mcpURL,
		Headers:   &headers,
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(rawBody))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "POST must still create the server; body=%s", w.Body.String())

	onDisk, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	assertMCPSecretsAbsent(t, string(onDisk), "config.json")

	got := httptest.NewRecorder()
	api.HandleConfig(got, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body.String())
	assertMCPSecretsAbsent(t, got.Body.String(), "GET /api/v1/config after create")

	for _, secret := range []string{mcpAuthSecret, mcpProxySecret, mcpCookieSecret} {
		assert.True(t, credentialStoreHolds(t, store, secret),
			"issue #638: secret %q must be in the encrypted credential store, not dropped", secret)
	}
}

func TestPatchMCPServer_AuthorizationBearer_NotInConfigJSON(t *testing.T) {
	api, store := newTestRestAPIWithHomeAndCredStore(t)
	mcpURL := "https://127.0.0.1:1/sse"
	created := gen.McpServerCreate{
		Name:      "remote-sec",
		Transport: gen.McpServerCreateTransportSse,
		Url:       &mcpURL,
	}
	createdRaw, err := json.Marshal(created)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(createdRaw))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "seed POST must return 201; body=%s", w.Body.String())

	headers := mcpSecretHeaders()
	patch := gen.McpServerUpdate{Headers: &headers}
	patchRaw, err := json.Marshal(patch)
	require.NoError(t, err)
	wp := httptest.NewRecorder()
	rp := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/remote-sec", bytes.NewReader(patchRaw))
	rp.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(wp, rp)
	require.Equal(t, http.StatusOK, wp.Code, "PATCH must still succeed; body=%s", wp.Body.String())

	onDisk, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	assertMCPSecretsAbsent(t, string(onDisk), "config.json after PATCH")

	got := httptest.NewRecorder()
	api.HandleConfig(got, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body.String())
	assertMCPSecretsAbsent(t, got.Body.String(), "GET /api/v1/config after PATCH")

	for _, secret := range []string{mcpAuthSecret, mcpProxySecret, mcpCookieSecret} {
		assert.True(t, credentialStoreHolds(t, store, secret),
			"issue #638: secret %q must be in the encrypted credential store, not dropped", secret)
	}
}
