// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// TestSSRFChecker_PrivateIPv4Ranges validates all RFC 1918 private ranges, link-local,
// loopback, cloud metadata, and unspecified address blocking.
// Traces to: wave2-security-layer-spec.md line 780 (TestSSRFChecker_PrivateIPv4Ranges)
// BDD: Given SSRF protection is enabled, When agent calls web_fetch with private/metadata IP,
// Then the request is blocked.
func TestSSRFChecker_PrivateIPv4Ranges(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 820 (Dataset: SSRF IP Validation rows 1–13)
	tests := []struct {
		name        string
		ip          string
		wantBlocked bool
		wantReason  string
	}{
		// Dataset row 1 — RFC 1918 Class A start
		{name: "10.0.0.1 private Class A", ip: "10.0.0.1", wantBlocked: true, wantReason: "private IP range"},
		// Dataset row 2 — RFC 1918 Class A end
		{
			name:        "10.255.255.255 private Class A boundary",
			ip:          "10.255.255.255",
			wantBlocked: true,
			wantReason:  "private IP range",
		},
		// Dataset row 3 — RFC 1918 Class B start
		{name: "172.16.0.1 private Class B", ip: "172.16.0.1", wantBlocked: true, wantReason: "private IP range"},
		// Dataset row 4 — RFC 1918 Class B end
		{
			name:        "172.31.255.255 private Class B boundary",
			ip:          "172.31.255.255",
			wantBlocked: true,
			wantReason:  "private IP range",
		},
		// Dataset row 5 — just outside Class B (must be allowed)
		{name: "172.32.0.1 outside private", ip: "172.32.0.1", wantBlocked: false},
		// Dataset row 6 — RFC 1918 Class C start
		{name: "192.168.0.1 private Class C", ip: "192.168.0.1", wantBlocked: true, wantReason: "private IP range"},
		// Dataset row 7 — RFC 1918 Class C end
		{
			name:        "192.168.255.255 private Class C boundary",
			ip:          "192.168.255.255",
			wantBlocked: true,
			wantReason:  "private IP range",
		},
		// Dataset row 8 — AWS/GCP/Azure cloud metadata (exact block)
		{
			name:        "169.254.169.254 cloud metadata",
			ip:          "169.254.169.254",
			wantBlocked: true,
			wantReason:  "cloud metadata endpoint",
		},
		// Dataset row 9 — link-local
		{name: "169.254.0.1 link-local", ip: "169.254.0.1", wantBlocked: true, wantReason: "private IP range"},
		// Dataset row 10 — loopback standard
		{name: "127.0.0.1 loopback", ip: "127.0.0.1", wantBlocked: true, wantReason: "private IP range"},
		// Dataset row 11 — non-standard loopback
		{name: "127.0.0.2 loopback alternate", ip: "127.0.0.2", wantBlocked: true, wantReason: "private IP range"},
		// Dataset row 12 — unspecified address
		{name: "0.0.0.0 unspecified", ip: "0.0.0.0", wantBlocked: true, wantReason: "private IP range"},
		// Dataset row 13 — public IP (must be allowed)
		{name: "8.8.8.8 public Google DNS", ip: "8.8.8.8", wantBlocked: false},
	}

	checker := security.NewSSRFChecker(nil)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			require.NotNil(t, ip, "test setup: failed to parse IP %s", tc.ip)

			err := checker.CheckIP(ip)
			if tc.wantBlocked {
				require.Error(t, err,
					"IP %s should be blocked but CheckIP returned nil", tc.ip)
				assert.Contains(t, err.Error(), "SSRF",
					"error must include 'SSRF' prefix")
				if tc.wantReason != "" {
					assert.Contains(t, err.Error(), tc.wantReason,
						"IP %s: error should contain %q, got %q", tc.ip, tc.wantReason, err.Error())
				}
			} else {
				assert.NoError(t, err,
					"IP %s should be allowed but got: %v", tc.ip, err)
			}
		})
	}
}

// TestSSRFChecker_PrivateIPv6Ranges validates IPv6 private ranges are blocked:
// loopback (::1), link-local (fe80::/10), unique local (fc00::/7),
// and IPv4-mapped IPv6 equivalents.
// Traces to: wave2-security-layer-spec.md line 781 (TestSSRFChecker_PrivateIPv6Ranges)
// BDD: SSRF edge case — IPv6 private ranges (spec line 299)
func TestSSRFChecker_PrivateIPv6Ranges(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 837 (Dataset rows 14–19)
	tests := []struct {
		name        string
		ip          string
		wantBlocked bool
		wantReason  string
	}{
		// Dataset row 14 — IPv6 loopback
		{name: "::1 IPv6 loopback", ip: "::1", wantBlocked: true, wantReason: "SSRF"},
		// Dataset row 15 — IPv6 link-local fe80::/10
		{name: "fe80::1 IPv6 link-local", ip: "fe80::1", wantBlocked: true, wantReason: "SSRF"},
		// Dataset row 16 — IPv6 unique local fc00::/7
		{name: "fc00::1 IPv6 unique local", ip: "fc00::1", wantBlocked: true, wantReason: "SSRF"},
		// Dataset row 17 — IPv4-mapped IPv6 private (10.0.0.1)
		{name: "::ffff:10.0.0.1 IPv4-mapped private", ip: "::ffff:10.0.0.1", wantBlocked: true, wantReason: "SSRF"},
		// Dataset row 18 — IPv4-mapped IPv6 metadata
		{
			name:        "::ffff:169.254.169.254 IPv4-mapped metadata",
			ip:          "::ffff:169.254.169.254",
			wantBlocked: true,
			wantReason:  "SSRF",
		},
		// Dataset row 19 — public IPv6 (must be allowed)
		{name: "2001:4860:4860::8888 public IPv6 Google DNS", ip: "2001:4860:4860::8888", wantBlocked: false},
	}

	checker := security.NewSSRFChecker(nil)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			require.NotNil(t, ip, "test setup: failed to parse IP %s", tc.ip)

			err := checker.CheckIP(ip)
			if tc.wantBlocked {
				require.Error(t, err,
					"IPv6 %s should be blocked", tc.ip)
				assert.Contains(t, err.Error(), "SSRF",
					"IPv6 block error must include SSRF prefix")
			} else {
				assert.NoError(t, err,
					"IPv6 %s should be allowed but got: %v", tc.ip, err)
			}
		})
	}
}

// TestSSRFChecker_Allowlist validates that IPs in the allowlist bypass SSRF blocking.
// Traces to: wave2-security-layer-spec.md line 782 (TestSSRFChecker_Allowlist)
// BDD: Scenario: Allowlisted internal IP is permitted (spec line 665)
func TestSSRFChecker_Allowlist(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 665 (Scenario: Allowlisted internal IP)
	allowlist := []string{"10.0.0.5", "192.168.1.100"}
	checker := security.NewSSRFChecker(allowlist)

	t.Run("allowlisted private IP 10.0.0.5 is permitted", func(t *testing.T) {
		ip := net.ParseIP("10.0.0.5")
		err := checker.CheckIP(ip)
		assert.NoError(t, err, "allowlisted IP 10.0.0.5 should not be blocked")
	})

	t.Run("allowlisted IP 192.168.1.100 is permitted", func(t *testing.T) {
		ip := net.ParseIP("192.168.1.100")
		err := checker.CheckIP(ip)
		assert.NoError(t, err, "allowlisted IP 192.168.1.100 should not be blocked")
	})

	t.Run("non-allowlisted private IP still blocked", func(t *testing.T) {
		ip := net.ParseIP("10.0.0.6") // different IP, not in allowlist
		err := checker.CheckIP(ip)
		assert.Error(t, err, "non-allowlisted private IP 10.0.0.6 should be blocked")
	})

	t.Run("allowlist does not affect public IPs", func(t *testing.T) {
		ip := net.ParseIP("8.8.8.8")
		err := checker.CheckIP(ip)
		assert.NoError(t, err, "public IP 8.8.8.8 should always be allowed regardless of allowlist")
	})
}

// TestSSRFChecker_DNSRebinding validates that DNS-resolved IPs are checked after resolution,
// preventing CNAME-chain and DNS rebinding SSRF attacks.
// Traces to: wave2-security-layer-spec.md line 783 (TestSSRFChecker_DNSRebinding)
// BDD: Scenario: DNS resolves to private IP (spec line 674)
func TestSSRFChecker_DNSRebinding(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 674 (Scenario: DNS resolves to private IP)
	checker := security.NewSSRFChecker(nil)
	ctx := context.Background()

	t.Run("resolved cloud metadata IP is blocked via CheckIP", func(t *testing.T) {
		// Simulate DNS resolution result: evil.example.com → 169.254.169.254
		resolvedIP := net.ParseIP("169.254.169.254")
		err := checker.CheckIP(resolvedIP)
		require.Error(t, err, "resolved cloud metadata IP should be blocked")
		assert.Contains(t, err.Error(), "169.254.169.254")
	})

	t.Run("resolved private IP is blocked via CheckIP", func(t *testing.T) {
		resolvedIP := net.ParseIP("192.168.1.1")
		err := checker.CheckIP(resolvedIP)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SSRF")
	})

	t.Run("CheckURL blocks private IP in URL directly", func(t *testing.T) {
		// Use context with no DNS resolution for numeric IPs
		err := checker.CheckURL(ctx, "http://10.0.0.5/api")
		require.Error(t, err, "URL pointing to private IP should be blocked")
		assert.Contains(t, err.Error(), "SSRF")
	})

	t.Run("CheckURL allows public IP in URL", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://8.8.8.8/dns")
		assert.NoError(t, err, "URL pointing to public IP should be allowed")
	})

	t.Run("CheckURL blocks cloud metadata URL", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 648 (Scenario: Cloud metadata endpoint blocked)
		err := checker.CheckURL(ctx, "http://169.254.169.254/latest/meta-data/")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SSRF")
	})
}

// ---------------------------------------------------------------------------
// CheckRedirect / SafeClient coverage — CheckRedirect (ssrf.go ~line 318) and
// the SafeClient() constructor that wires it into an http.Client had ZERO
// test coverage before this addition (the direct-dial path via SafeTransport
// is covered above, but nothing exercised the redirect-following defense).
// Gap identified in the whole-codebase Backend-High test-gap review
// (2026-07-07). These tests use a real httptest.Server issuing real 30x
// redirects, matching this file's existing DNS-rebinding-test style of
// exercising real behavior rather than mocking internals.
//
// Each test allowlists the httptest.Server's own loopback address (127.0.0.1)
// so the checker's dial-time check (SafeTransport) doesn't block the test
// fixture itself — the interesting assertion is what happens to the
// REDIRECT TARGET, which is deliberately NOT allowlisted.
// ---------------------------------------------------------------------------

// serverHostname extracts the bare hostname (no port) from an httptest.Server
// URL, for allowlisting the test server's own address.
func serverHostname(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return u.Hostname()
}

// TestSSRFChecker_CheckRedirect_BlocksPrivateIPTarget verifies that a redirect
// whose Location targets a private/internal IP is rejected by CheckRedirect
// before any dial to that internal target is attempted.
func TestSSRFChecker_CheckRedirect_BlocksPrivateIPTarget(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/redirect-private", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.1.2.3/internal", http.StatusFound)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	checker := security.NewSSRFChecker([]string{serverHostname(t, server.URL)})
	client := checker.SafeClient()

	resp, err := client.Get(server.URL + "/redirect-private")
	if resp != nil {
		resp.Body.Close()
	}
	require.Error(t, err, "redirect to private IP 10.1.2.3 must be rejected, not followed")
	assert.Contains(t, err.Error(), "SSRF")
	assert.Contains(t, err.Error(), "private IP range")
}

// TestSSRFChecker_CheckRedirect_BlocksCloudMetadataTarget verifies a redirect
// whose Location targets the cloud metadata endpoint (169.254.169.254) is
// rejected, surfacing CheckIP's dedicated metadata-endpoint error message.
func TestSSRFChecker_CheckRedirect_BlocksCloudMetadataTarget(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/redirect-metadata", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	checker := security.NewSSRFChecker([]string{serverHostname(t, server.URL)})
	client := checker.SafeClient()

	resp, err := client.Get(server.URL + "/redirect-metadata")
	if resp != nil {
		resp.Body.Close()
	}
	require.Error(t, err, "redirect to cloud metadata endpoint must be rejected, not followed")
	assert.Contains(t, err.Error(), "SSRF")
	assert.Contains(t, err.Error(), "cloud metadata endpoint")
}

// TestSSRFChecker_CheckRedirect_AllowsSameOriginRedirect verifies CheckRedirect
// does NOT over-block: an ordinary same-origin redirect must still be
// followed to completion and its real response body returned.
func TestSSRFChecker_CheckRedirect_AllowsSameOriginRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/landed", http.StatusFound)
	})
	mux.HandleFunc("/landed", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	checker := security.NewSSRFChecker([]string{serverHostname(t, server.URL)})
	client := checker.SafeClient()

	resp, err := client.Get(server.URL + "/start")
	require.NoError(t, err, "legitimate same-origin redirect must be followed, not blocked")
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body), "must have followed the redirect to /landed and read its real body")
}

// TestSSRFChecker_CheckRedirect_TooManyRedirectsRejected verifies the
// len(via) >= 10 redirect-count limit in CheckRedirect: a server that keeps
// issuing same-origin (otherwise-legal, allowlisted-host) redirects forever
// is eventually cut off — the count limit is enforced independently of the
// per-hop SSRF host check.
func TestSSRFChecker_CheckRedirect_TooManyRedirectsRejected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	checker := security.NewSSRFChecker([]string{serverHostname(t, server.URL)})
	client := checker.SafeClient()

	resp, err := client.Get(server.URL + "/loop")
	if resp != nil {
		resp.Body.Close()
	}
	require.Error(t, err, "an infinite same-origin redirect loop must eventually be rejected")
	assert.Contains(t, err.Error(), "too many redirects")
}
