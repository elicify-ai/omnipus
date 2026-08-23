// T2.3 + T2.4: Preview proxy security and first-serve audit tests.
//
// T2.3: When the dev-server registry has an active registration, hitting
//
//	/preview/<agent>/<token>/foo must:
//	(a) NOT forward the Authorization header to the upstream dev server.
//	(b) Strip upstream Content-Security-Policy and X-Frame-Options headers.
//	(c) Inject gateway-controlled Content-Security-Policy in the response.
//
// T2.4: /preview/<agent>/<token>/ serves registered static content and emits
//
//	a serve.served audit entry on first serve.

package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// newDevProxyTestAPI returns a restAPI with DevServerRegistry (and optionally
// ServedSubdirs) wired. A single agentLoop is created and registered for cleanup.
func newDevProxyTestAPI(t *testing.T) (*restAPI, *sandbox.DevServerRegistry) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	dr := sandbox.NewDevServerRegistry()
	t.Cleanup(dr.Close)

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		devServers:    dr,
	}
	return api, dr
}

// newDevProxyTestAPIWithServe is like newDevProxyTestAPI but also wires ServedSubdirs.
func newDevProxyTestAPIWithServe(t *testing.T) (*restAPI, *agent.ServedSubdirs, *sandbox.DevServerRegistry) {
	t.Helper()
	api, dr := newDevProxyTestAPI(t)

	ss := agent.NewServedSubdirs()
	t.Cleanup(ss.Stop)
	api.servedSubdirs = ss
	return api, ss, dr
}

// parsePort parses the port number from a URL string like "http://127.0.0.1:PORT".
func parsePort(t *testing.T, rawURL string) int32 {
	t.Helper()
	idx := strings.LastIndex(rawURL, ":")
	require.Greater(t, idx, 0, "URL must contain a colon: %s", rawURL)
	portStr := rawURL[idx+1:]
	var port int32
	_, err := fmt.Sscanf(portStr, "%d", &port)
	require.NoError(t, err, "parse port from %q", portStr)
	return port
}

// TestHandlePreview_DevProxy_StripsAuthorizationHeader (T2.3a) verifies that the
// reverse proxy does NOT forward the caller's Authorization header to the upstream
// dev server. This prevents leaking the admin bearer token to an agent-owned process.
func TestHandlePreview_DevProxy_StripsAuthorizationHeader(t *testing.T) {
	// Spin up a stub upstream that records what headers it received.
	var receivedAuthHeader string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Security-Policy", "default-src 'unsafe-inline'")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>dev app</html>"))
	}))
	t.Cleanup(stub.Close)

	stubPort := parsePort(t, stub.URL)

	api, dr := newDevProxyTestAPI(t)

	reg, err := dr.Register("proxy-test-agent", stubPort, 99999, "stub", 10)
	require.NoError(t, err, "Register stub dev server")

	path := "/preview/proxy-test-agent/" + reg.Token + "/foo"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	// Set an Authorization header — the proxy must strip it before forwarding.
	req.Header.Set("Authorization", "Bearer super-secret-admin-token")
	rec := httptest.NewRecorder()

	api.HandlePreview(rec, req)

	// The upstream stub should have responded with 200.
	assert.Equal(t, http.StatusOK, rec.Code,
		"proxy to live stub must return 200; got %d body=%s", rec.Code, rec.Body.String())

	// Authorization must NOT have reached the upstream (T2.3a).
	assert.Empty(t, receivedAuthHeader,
		"T2.3a: upstream dev server must NOT receive Authorization header; got %q", receivedAuthHeader)
}

// TestHandlePreview_DevProxy_StripsUpstreamCSP (T2.3b) verifies that upstream
// Content-Security-Policy and X-Frame-Options headers are stripped by the proxy
// and replaced by gateway-controlled headers.
func TestHandlePreview_DevProxy_StripsUpstreamCSP(t *testing.T) {
	const upstreamCSP = "default-src 'unsafe-inline'; script-src *"

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", upstreamCSP)
		w.Header().Set("Content-Security-Policy-Report-Only", "default-src 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>dev app</html>"))
	}))
	t.Cleanup(stub.Close)

	stubPort := parsePort(t, stub.URL)
	api, dr := newDevProxyTestAPI(t)

	reg, err := dr.Register("csp-strip-agent", stubPort, 99999, "stub", 10)
	require.NoError(t, err)

	path := "/preview/csp-strip-agent/" + reg.Token + "/"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	api.HandlePreview(rec, req)

	require.Equal(t, http.StatusOK, rec.Code,
		"proxy to stub must return 200; got %d body=%s", rec.Code, rec.Body.String())

	// Upstream CSP must be stripped (T2.3b: FR-007d).
	// The upstream sent "script-src *" — that wildcard must not appear in the gateway response.
	// (The gateway's own injected CSP may contain 'unsafe-inline' for hosted SPAs, but must
	// not echo the upstream's "script-src *" wildcard that allows all script sources.)
	assert.NotContains(t, rec.Header().Get("Content-Security-Policy"), "script-src *",
		"T2.3b: upstream CSP 'script-src *' wildcard must be stripped from proxy response")
	assert.Empty(t, rec.Header().Get("Content-Security-Policy-Report-Only"),
		"T2.3b: upstream CSP-Report-Only must be stripped")
	assert.Empty(t, rec.Header().Get("X-Frame-Options"),
		"T2.3b: upstream X-Frame-Options must be stripped")

	// Gateway must inject its own CSP (T2.3b).
	gatewayCsp := rec.Header().Get("Content-Security-Policy")
	assert.NotEmpty(t, gatewayCsp,
		"T2.3b: gateway must inject Content-Security-Policy in proxy response")
	assert.Contains(t, gatewayCsp, "frame-ancestors",
		"T2.3b: gateway CSP must include frame-ancestors directive")
}

// TestHandlePreview_StaticServe_EmitsAuditOnFirstServe (T2.4) verifies that
// /preview/<agent>/<token>/:
// (a) Serves the registered static content with HTTP 200.
// (b) The first serve emits a serve.served audit-style marker via markFirstServed.
func TestHandlePreview_StaticServe_EmitsAuditOnFirstServe(t *testing.T) {
	api, ss, _ := newDevProxyTestAPIWithServe(t)
	workDir := t.TempDir()

	indexPath := filepath.Join(workDir, "index.html")
	require.NoError(t, os.WriteFile(indexPath, []byte("<h1>preview serve</h1>"), 0o644))

	token, _, err := ss.Register("serve-compat-agent", workDir, time.Hour)
	require.NoError(t, err)

	// First markFirstServed call for this token must return true (audit trigger).
	firstMark := api.markFirstServed(token)
	assert.True(t, firstMark,
		"T2.4: first markFirstServed call for new token must return true (triggers audit)")

	// Second call must return false (no duplicate audit).
	secondMark := api.markFirstServed(token)
	assert.False(t, secondMark,
		"T2.4: second markFirstServed call must return false (no duplicate audit)")

	// Verify the /preview/ path itself serves the content correctly.
	req := httptest.NewRequest(http.MethodGet, "/preview/serve-compat-agent/"+token+"/", nil)
	rec := httptest.NewRecorder()
	api.HandlePreview(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "T2.4: /preview/ must return 200")
	assert.Contains(t, rec.Body.String(), "preview serve",
		"T2.4: /preview/ response must contain the file content")
}
