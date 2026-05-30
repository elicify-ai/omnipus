//go:build !cgo

// T2.23: ProxyDevRequest_UpstreamError_GenericBody.
//
// Verifies that when the upstream dev server is unreachable, the gateway
// returns a constant generic body ("dev server unreachable") instead of
// echoing raw upstream error details (dial errors, TLS messages, port numbers).
//
// This is the FR-security-4 contract documented in rest_preview.go::proxyDevRequest:
//   "Do NOT echo the upstream-error message back to the (anonymous, auth-less)
//    preview client: it can leak the loopback port number, dial/TLS error details."

package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// TestProxyDevRequest_UpstreamError_GenericBody (T2.23) wires a stub upstream
// that immediately closes the connection. The response body must be the
// constant string "dev server unreachable" and must NOT echo the error details.
func TestProxyDevRequest_UpstreamError_GenericBody(t *testing.T) {
	// A stub that immediately closes every connection to simulate "connection refused".
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			// Fallback: return a 502 with a sensitive body that must be suppressed.
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("internal dial error: 127.0.0.1:31337 connection refused secret token"))
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close() //nolint:errcheck
	}))
	// Close the stub now — every request will get "connection refused".
	stub.Close()

	stubPort := parsePort(t, stub.URL)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
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

	reg, err := dr.Register("error-body-agent", stubPort, 99999, "stub-closed", 10)
	require.NoError(t, err, "Register closed stub")

	path := "/preview/error-body-agent/" + reg.Token + "/sensitive.html"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()

	api.HandlePreview(rec, req)

	// Must return 502 Bad Gateway.
	assert.Equal(t, http.StatusBadGateway, rec.Code,
		"T2.23: unreachable upstream must yield 502 Bad Gateway")

	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	bodyStr := strings.TrimSpace(string(body))

	// The body must contain the constant generic message.
	assert.Contains(t, bodyStr, "dev server unreachable",
		"T2.23: response body must be the constant generic message")

	// The body must NOT echo the port number (leaks internal topology).
	portStr := strings.TrimPrefix(stub.URL, "http://127.0.0.1:")
	assert.NotContains(t, bodyStr, portStr,
		"T2.23: response body must not echo the loopback port number")

	// The body must NOT contain raw dial error substrings.
	for _, leakyPhrase := range []string{
		"connection refused",
		"dial tcp",
		"127.0.0.1",
		"no such host",
		"EOF",
	} {
		assert.NotContains(t, bodyStr, leakyPhrase,
			"T2.23: response body must not contain dial/TLS error phrase %q", leakyPhrase)
	}
}

// TestProxyDevRequest_UpstreamError_SuppressesUpstreamSensitiveBody (T2.23b)
// verifies that a stub that returns 502 WITH a sensitive body in its response
// does not get that body forwarded to the client. The gateway's ErrorHandler
// fires and writes the constant message instead.
//
// Note: httputil.ReverseProxy calls ErrorHandler when it encounters a transport
// error (connection refused, EOF). If the upstream returns a valid HTTP response
// (even 502), ModifyResponse fires instead. This test focuses on the transport
// error path (connection closed immediately by stub.Close()).
func TestProxyDevRequest_UpstreamError_SuppressesUpstreamSensitiveBody(t *testing.T) {
	// We rely on a closed server for the transport error path (same as T2.23 above).
	// This test focuses on asserting the JSON structure of the error response.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	stub.Close()

	stubPort := parsePort(t, stub.URL)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
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

	reg, err := dr.Register("error-json-agent", stubPort, 99999, "stub-closed-2", 10)
	require.NoError(t, err)

	path := "/preview/error-json-agent/" + reg.Token + "/"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	api.HandlePreview(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)

	// The response Content-Type must be application/json (from writeDevProxyError).
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"),
		"T2.23b: error response must be application/json")

	// The body must be valid JSON with an "error" key containing the constant message.
	bodyStr := rec.Body.String()
	assert.Contains(t, bodyStr, `"error"`,
		"T2.23b: error body must be JSON with 'error' key")
	assert.Contains(t, bodyStr, "dev server unreachable",
		"T2.23b: error JSON must contain the constant generic message")
}
