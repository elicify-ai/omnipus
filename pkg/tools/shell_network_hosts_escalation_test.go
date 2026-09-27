// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// D-13 security-review fix (2026-09-24): "Today an approved D8 network
// escalation widens only the kernel ports. The egress proxy... still
// returns 403 'host not in allow-list'." These tests drive the REAL
// enforceShellPermissionMode path with a REAL sandbox.EgressProxy attached
// (tool.proxy), proving end to end that an approved escalation actually
// lets the named host through the proxy, that the card carries the hosts,
// and that the grant is scoped to the session (a different host still
// escalates).
package tools

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// startLoopbackHTTPServer starts a tiny HTTP server on 127.0.0.1:0 and
// returns its address — used as the "approved host" stand-in so this test
// suite never depends on real internet access (matching the analogous
// pkg/sandbox proxy tests' own pattern).
func startLoopbackHTTPServer(t *testing.T) string {
	t.Helper()
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("upstream-ok"))
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// TestEnforceShellPermissionMode_D13_ApprovedHostReachesRealProxy is the
// end-to-end proof: approving a curl call escalation actually lets THAT
// host through the shared sandbox.EgressProxy, via the egressToken this
// call's own Limits.EgressProxyToken embeds.
func TestEnforceShellPermissionMode_D13_ApprovedHostReachesRealProxy(t *testing.T) {
	upstreamAddr := startLoopbackHTTPServer(t)

	proxy, err := sandbox.NewEgressProxy(nil, nil) // empty static allow-list: deny-all baseline
	require.NoError(t, err)
	t.Cleanup(func() { _ = proxy.Close() })

	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)
	tool.proxy = proxy

	cmd := "curl -sI http://" + upstreamAddr + "/"
	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result, "an approved network escalation must not refuse the command")
	require.NotNil(t, perm)
	require.NotEmpty(t, perm.egressToken, "D-13: an approved host escalation must mint an egress-proxy token")

	// The card must have carried the extracted host.
	require.NotNil(t, requester.lastArg)
	hosts, _ := requester.lastArg["hosts"].([]string)
	assert.Contains(t, hosts, hostOnly(t, upstreamAddr))
	note, _ := requester.lastArg["note"].(string)
	assert.Contains(t, note, "Allows network access to:")

	// The token this call was granted actually opens the proxy for that
	// host — the D-13 fix's whole point.
	proxyURL, err := url.Parse("http://" + perm.egressToken + "@" + proxy.Addr())
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + upstreamAddr + "/")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "upstream-ok", string(body), "the approved host must actually be reachable through the proxy (status %d)", resp.StatusCode)
}

// TestEnforceShellPermissionMode_D13_DifferentHostStillEscalates proves the
// grant is scoped to the SPECIFIC approved host(s), not to "the session has
// approved SOME network access": a second command in the same session,
// naming a DIFFERENT host, still asks.
func TestEnforceShellPermissionMode_D13_DifferentHostStillEscalates(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)

	_, result1 := tool.enforceShellPermissionMode(ctx, "curl https://example.com")
	require.Nil(t, result1)
	require.Equal(t, 1, requester.callCount(), "the first, never-before-approved host must escalate")

	_, result2 := tool.enforceShellPermissionMode(ctx, "curl https://other.test")
	require.Nil(t, result2)
	assert.Equal(t, 2, requester.callCount(), "D-13: a DIFFERENT host in the same session must still escalate — the grant is scoped to example.com, not to \"network access in general\"")

	// A repeat of the FIRST host now runs silently.
	_, result3 := tool.enforceShellPermissionMode(ctx, "curl https://example.com")
	require.Nil(t, result3)
	assert.Equal(t, 2, requester.callCount(), "a repeat of an already-approved host must not re-prompt")
}

// TestEnforceShellPermissionMode_D13_BlindNetworkNeedCardSaysSo proves
// founder decision B point 3: a flagged command with no extractable host
// must say so on the card, not silently claim a host it never found.
func TestEnforceShellPermissionMode_D13_BlindNetworkNeedCardSaysSo(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)

	_, result := tool.enforceShellPermissionMode(ctx, "npm install")
	require.Nil(t, result)
	require.NotNil(t, requester.lastArg)
	_, hasHosts := requester.lastArg["hosts"]
	assert.False(t, hasHosts, "a blind network need must carry no \"hosts\" field")
	note, _ := requester.lastArg["note"].(string)
	assert.Contains(t, note, "no specific host could be identified")
}

// hostOnly strips the port from a host:port address for the assertion
// above (ExtractNetworkHosts never includes the port).
func hostOnly(t *testing.T, hostPort string) string {
	t.Helper()
	h, _, err := net.SplitHostPort(hostPort)
	require.NoError(t, err)
	return h
}
