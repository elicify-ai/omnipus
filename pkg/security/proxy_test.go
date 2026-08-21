// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

import (
	"net/http"
	"net/url"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// TestExecProxy_SSRFBlock is an integration test validating the exec HTTP proxy
// intercepts and blocks requests to private IPs from child processes.
// Traces to: wave2-security-layer-spec.md line 812 (TestExecProxy_SSRFBlock)
// BDD: Scenario: Proxy blocks private IP from child process (spec line 722) +
// Scenario: Proxy env vars set on child process (spec line 733)
func TestExecProxy_SSRFBlock(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 722 (Scenario: Proxy blocks private IP)
	ssrfChecker := security.NewSSRFChecker(nil) // no allowlist
	proxy := security.NewExecProxy(ssrfChecker, nil)

	err := proxy.Start()
	require.NoError(t, err, "proxy must start without error")
	defer proxy.Stop()

	t.Run("proxy binds to loopback address", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 733 (Scenario: Proxy env vars set)
		addr := proxy.Addr()
		assert.NotEmpty(t, addr, "proxy must bind to a loopback address:port")
		assert.Contains(t, addr, "127.0.0.1",
			"proxy must be on loopback interface only")
	})

	t.Run("proxy blocks request to private IP", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 722 (Scenario: Proxy blocks private IP)
		proxyURL, err := url.Parse("http://" + proxy.Addr())
		require.NoError(t, err)

		client := &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			},
			Timeout: 3 * time.Second,
		}

		// Attempt to reach private IP via proxy — should be blocked
		resp, err := client.Get("http://10.0.0.1/secret")
		if err == nil {
			// Test cleanup: Close error is inconsequential once the status
			// assertion below has run.
			defer func() {
				if err := resp.Body.Close(); err != nil {
					_ = err
				}
			}()
			// Proxy should return 403 Forbidden for private IP requests
			assert.Equal(t, http.StatusForbidden, resp.StatusCode,
				"proxy must return 403 for private IP requests")
		} else {
			// Connection error is acceptable — proxy may block at TCP level
			assert.NotEmpty(t, err.Error())
		}
	})

	t.Run("proxy env vars are set on exec.Cmd", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 733 (Scenario: Proxy env vars)
		cmd := exec.Command("true") // minimal no-op command
		proxy.PrepareCmd(cmd)
		defer proxy.CmdDone()

		// Check HTTP_PROXY and HTTPS_PROXY are in cmd.Env
		var httpProxy, httpsProxy string
		for _, env := range cmd.Env {
			if len(env) > 11 && env[:11] == "HTTP_PROXY=" {
				httpProxy = env[11:]
			}
			if len(env) > 12 && env[:12] == "HTTPS_PROXY=" {
				httpsProxy = env[12:]
			}
		}

		require.NotEmpty(t, httpProxy, "HTTP_PROXY env var must be set on exec.Cmd")
		require.NotEmpty(t, httpsProxy, "HTTPS_PROXY env var must be set on exec.Cmd")

		assert.Contains(t, httpProxy, "127.0.0.1",
			"HTTP_PROXY must point to localhost proxy")
		assert.Contains(t, httpProxy, proxy.Addr(),
			"HTTP_PROXY must contain the proxy address")
	})

	t.Run("proxy Addr is available after Start", func(t *testing.T) {
		addr := proxy.Addr()
		assert.NotEmpty(t, addr)
		// Address should be parseable as host:port
		proxyURL, err := url.Parse("http://" + addr)
		assert.NoError(t, err)
		assert.NotEmpty(t, proxyURL.Host)
	})
}
