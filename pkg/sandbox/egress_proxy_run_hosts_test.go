// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// D-13 security-review fix (2026-09-24): an approved D8 network escalation
// used to widen only the kernel port rule — the SEPARATE egress proxy's own
// static host allow-list still 403'd every request ("host not in
// allow-list"). GrantRunHosts/RevokeRunToken/tokenFromRequest are the fix's
// mechanism: a per-run or per-session token, presented via this proxy's own
// Proxy-Authorization header, additively allows exactly the hosts granted
// under it. These tests use a loopback listener as the "approved host"
// stand-in (matching TestEgressProxy_AllowedRequestForwards's own pattern)
// rather than a real internet host, so they are deterministic and network-
// flake-free; pkg/tools' own tests separately prove the literal hostnames
// ("example.com") are extracted and carried onto the approval card.
package sandbox

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// startLoopbackUpstream starts a tiny HTTP server on 127.0.0.1:0 that
// answers every request with "upstream-ok", and returns its address.
func startLoopbackUpstream(t *testing.T) string {
	t.Helper()
	upstream := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("upstream-ok")) //nolint:errcheck
		}),
	}
	listener, err := listenLoopback(t)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = upstream.Serve(listener) }() //nolint:errcheck
	t.Cleanup(func() { _ = upstream.Close() })   //nolint:errcheck
	return listener.Addr().String()
}

// proxyClient returns an *http.Client routed through proxyAddr, presenting
// token as the proxy's Basic-auth username when non-empty (the same
// mechanism hardened_exec.go's Limits.EgressProxyToken uses in production —
// a userinfo-bearing proxy URL).
func proxyClient(t *testing.T, proxyAddr, token string) *http.Client {
	t.Helper()
	raw := "http://" + proxyAddr
	if token != "" {
		raw = "http://" + token + "@" + proxyAddr
	}
	proxyURL, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}
}

// TestEgressProxy_GrantRunHosts_TokenScopedAllow is the D-13 fix's core
// proof: a request presenting the token GrantRunHosts was called with
// reaches the granted host even though the proxy's STATIC allow-list is
// empty (deny-all); a request with NO token (or the wrong one) is still
// denied — the grant never leaks to an untokenised caller.
func TestEgressProxy_GrantRunHosts_TokenScopedAllow(t *testing.T) {
	upstreamAddr := startLoopbackUpstream(t)
	upstreamHost, _, err := net.SplitHostPort(upstreamAddr)
	if err != nil {
		t.Fatalf("split upstream addr: %v", err)
	}

	p, err := NewEgressProxy(nil, nil) // empty static allow-list: deny-all baseline
	if err != nil {
		t.Fatalf("NewEgressProxy: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck

	const token = "test-token-abc123"
	p.GrantRunHosts(token, []string{upstreamHost})

	// A request presenting the token reaches the granted host.
	resp, err := proxyClient(t, p.Addr(), token).Get("http://" + upstreamAddr + "/")
	if err != nil {
		t.Fatalf("tokenised client.Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)         //nolint:errcheck
	if string(body) != "upstream-ok" {
		t.Errorf("tokenised request body = %q, want upstream-ok (status %d)", body, resp.StatusCode)
	}

	// A request with NO token is still denied — the grant is not a global
	// allow, only a token-scoped one.
	resp2, err := proxyClient(t, p.Addr(), "").Get("http://" + upstreamAddr + "/")
	if err != nil {
		t.Fatalf("untokenised client.Get: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }() //nolint:errcheck
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("untokenised request status = %d, want 403 (D-13 grant must not leak to an untokenised caller)", resp2.StatusCode)
	}

	// A request with the WRONG token is also denied — "other.test" analog:
	// a session/run that was never granted this host must not slip through
	// by presenting some other, unrelated token.
	resp3, err := proxyClient(t, p.Addr(), "some-other-token").Get("http://" + upstreamAddr + "/")
	if err != nil {
		t.Fatalf("wrong-token client.Get: %v", err)
	}
	defer func() { _ = resp3.Body.Close() }() //nolint:errcheck
	if resp3.StatusCode != http.StatusForbidden {
		t.Errorf("wrong-token request status = %d, want 403", resp3.StatusCode)
	}
}

// TestEgressProxy_RevokeRunToken_RemovesGrant proves a revoked token
// immediately loses its host — the session-end / once-run cleanup path
// (AgentLoop.CloseSession, and the "Approve Once" single-use token) leaves
// no dangling access.
func TestEgressProxy_RevokeRunToken_RemovesGrant(t *testing.T) {
	upstreamAddr := startLoopbackUpstream(t)
	upstreamHost, _, err := net.SplitHostPort(upstreamAddr)
	if err != nil {
		t.Fatalf("split upstream addr: %v", err)
	}

	p, err := NewEgressProxy(nil, nil)
	if err != nil {
		t.Fatalf("NewEgressProxy: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck

	const token = "revoke-me-token"
	p.GrantRunHosts(token, []string{upstreamHost})

	resp, err := proxyClient(t, p.Addr(), token).Get("http://" + upstreamAddr + "/")
	if err != nil {
		t.Fatalf("pre-revoke client.Get: %v", err)
	}
	_ = resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pre-revoke status = %d, want 200 (setup failed)", resp.StatusCode)
	}

	p.RevokeRunToken(token)

	resp2, err := proxyClient(t, p.Addr(), token).Get("http://" + upstreamAddr + "/")
	if err != nil {
		t.Fatalf("post-revoke client.Get: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }() //nolint:errcheck
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("post-revoke status = %d, want 403 — RevokeRunToken must remove the grant", resp2.StatusCode)
	}
}

// TestEgressProxy_GrantRunHosts_StaticAllowListUnaffected proves the D-13
// fix is purely additive: a proxy WITH a static operator allow-list still
// honours it exactly as before, whether or not any token grant exists.
func TestEgressProxy_GrantRunHosts_StaticAllowListUnaffected(t *testing.T) {
	patterns, err := compileEgressAllowList([]string{"npmjs.org"})
	if err != nil {
		t.Fatalf("compileEgressAllowList: %v", err)
	}
	p := &EgressProxy{patterns: patterns, allowList: []string{"npmjs.org"}}

	if !p.hostAllowed("npmjs.org", "") {
		t.Error("static allow-list entry must still pass with no token")
	}
	if !p.hostAllowed("npmjs.org", "some-token-nobody-granted-anything-to") {
		t.Error("static allow-list entry must still pass even when an unrelated token is presented")
	}
	if p.hostAllowed("evil.example", "") {
		t.Error("a host on neither the static list nor any grant must stay denied")
	}
}
