// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

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

// TestSSRFChecker_6to4AndTeredoAddresses validates the IPv6 tunneling-scheme
// unwrap logic in CheckIP: 6to4 (2002::/16, RFC 3056) embeds an IPv4 address in
// bytes [2:6]; Teredo (2001:0000::/32, RFC 4380) embeds a XOR-inverted client
// IPv4 in bytes [12:16]. Both must be unwrapped and re-checked against the
// IPv4 rules so a tunneled private/metadata address cannot bypass SSRF
// blocking by wrapping itself in an IPv6 tunneling prefix.
//
// Ported from the former pkg/tools.TestIsPrivateOrRestrictedIP_Table 6to4/Teredo
// rows during the web_fetch SSRF consolidation (2026-07-07): WebFetchTool's
// hand-rolled isPrivateOrRestrictedIP had byte-identical 6to4/Teredo unwrap
// logic to CheckIP below, but CheckIP itself had no direct test coverage for
// these two schemes before this addition — closing that gap here benefits
// every SSRFChecker caller (browser tools, exec proxy, egress proxy,
// web_search), not just web_fetch.
func TestSSRFChecker_6to4AndTeredoAddresses(t *testing.T) {
	checker := security.NewSSRFChecker(nil)

	tests := []struct {
		name        string
		ip          string
		wantBlocked bool
	}{
		{name: "6to4 embedding 127.x (loopback)", ip: "2002:7f00:0001::1", wantBlocked: true},
		{name: "6to4 embedding 10.0.0.1 (RFC 1918)", ip: "2002:0a00:0001::1", wantBlocked: true},
		{name: "6to4 embedding 8.1.1.1 (public)", ip: "2002:0801:0101::1", wantBlocked: false},
		{
			name:        "Teredo with client IPv4 10.0.0.1 (private, XOR-inverted)",
			ip:          "2001:0000:4136:e378:8000:63bf:f5ff:fffe",
			wantBlocked: true,
		},
		{
			name:        "Teredo with client IPv4 8.9.1.1 (public, XOR-inverted)",
			ip:          "2001:0000:4136:e378:8000:63bf:f7f6:fefe",
			wantBlocked: false,
		},
		{
			name:        "ordinary public IPv6 (Google) unaffected by tunneling checks",
			ip:          "2607:f8b0:4004:800::200e",
			wantBlocked: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			require.NotNil(t, ip, "test setup: failed to parse IP %s", tc.ip)

			err := checker.CheckIP(ip)
			if tc.wantBlocked {
				require.Error(t, err, "tunneled address %s should be blocked", tc.ip)
				assert.Contains(t, err.Error(), "SSRF")
			} else {
				assert.NoError(t, err, "tunneled address %s should be allowed but got: %v", tc.ip, err)
			}
		})
	}
}

// TestSSRFChecker_MulticastAndUnspecified validates that multicast
// (224.0.0.0/4 IPv4, ff00::/8 IPv6) and unspecified (0.0.0.0, ::) addresses
// are blocked. These are not meaningful unicast SSRF targets, but the former
// pkg/tools.isPrivateOrRestrictedIP blocked them explicitly (via
// net.IP.IsMulticast()/IsUnspecified()) and CheckIP's CIDR list did not cover
// the multicast ranges or the IPv6 unspecified address "::" before this
// addition — identified and closed during the web_fetch SSRF consolidation
// (2026-07-07) so no protection was silently dropped in the migration.
func TestSSRFChecker_MulticastAndUnspecified(t *testing.T) {
	checker := security.NewSSRFChecker(nil)

	tests := []struct {
		name string
		ip   string
	}{
		{name: "IPv4 multicast", ip: "224.0.0.1"},
		{name: "IPv4 multicast upper bound", ip: "239.255.255.255"},
		{name: "IPv6 multicast", ip: "ff02::1"},
		{name: "IPv6 unspecified", ip: "::"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			require.NotNil(t, ip, "test setup: failed to parse IP %s", tc.ip)

			err := checker.CheckIP(ip)
			require.Error(t, err, "%s should be blocked", tc.ip)
			assert.Contains(t, err.Error(), "SSRF")
		})
	}

	// 0.0.0.0 (IPv4 unspecified) was already covered by the 0.0.0.0/8 CIDR
	// entry before this change; assert it is still blocked with its existing
	// "private IP range" reason (see TestSSRFChecker_PrivateIPv4Ranges row 12)
	// so the ordering of the new multicast/unspecified handling doesn't shift
	// which message fires first for that address.
	t.Run("IPv4 unspecified (0.0.0.0) retains its existing reason", func(t *testing.T) {
		err := checker.CheckIP(net.ParseIP("0.0.0.0"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "private IP range")
	})
}

// TestSSRFChecker_ZeroValue_FailsClosed proves the hardening fix: a zero-value
// SSRFChecker (`var sc security.SSRFChecker`, e.g. constructed by mistake
// without NewSSRFChecker, as a same-package test elsewhere in this codebase
// does for WebFetchTool) must still block private/reserved IPs via CheckIP —
// not silently pass every private IP as "safe" because its block-list slices
// were never populated. Before the ensureInit/sync.Once fix, this test would
// fail: CheckIP on the zero value returned nil (no error) for 10.0.0.1.
func TestSSRFChecker_ZeroValue_FailsClosed(t *testing.T) {
	var sc security.SSRFChecker

	t.Run("blocks RFC1918 private IP", func(t *testing.T) {
		err := sc.CheckIP(net.ParseIP("10.0.0.1"))
		require.Error(t, err, "zero-value SSRFChecker must block private IPs, not fail open")
		assert.Contains(t, err.Error(), "SSRF")
	})

	t.Run("blocks cloud metadata endpoint", func(t *testing.T) {
		err := sc.CheckIP(net.ParseIP("169.254.169.254"))
		require.Error(t, err, "zero-value SSRFChecker must block the cloud metadata endpoint")
		assert.Contains(t, err.Error(), "cloud metadata endpoint")
	})

	t.Run("blocks IPv6 unique-local", func(t *testing.T) {
		err := sc.CheckIP(net.ParseIP("fc00::1"))
		require.Error(t, err, "zero-value SSRFChecker must block private IPv6 ranges")
		assert.Contains(t, err.Error(), "SSRF")
	})

	t.Run("still allows public IP", func(t *testing.T) {
		err := sc.CheckIP(net.ParseIP("8.8.8.8"))
		assert.NoError(t, err, "zero-value SSRFChecker must still allow public IPs")
	})
}

// TestSSRFChecker_ZeroValue_CheckHost_DoesNotPanic proves CheckHost on a
// zero-value SSRFChecker no longer panics on the nil resolver field (the
// second half of the fail-open/panic gap this hardening closes) and instead
// enforces the same block-list as CheckIP. Using a literal-IP host exercises
// CheckHost's net.ParseIP fast path without depending on network access.
func TestSSRFChecker_ZeroValue_CheckHost_DoesNotPanic(t *testing.T) {
	var sc security.SSRFChecker

	require.NotPanics(t, func() {
		_, err := sc.CheckHost(context.Background(), "192.168.1.1")
		require.Error(t, err, "zero-value SSRFChecker must block private IPs via CheckHost")
		assert.Contains(t, err.Error(), "SSRF")
	})
}

// TestSSRFChecker_SafeDialContext_NilDialer proves SafeDialContext no longer
// panics deep inside the returned closure when called with a nil dialer —
// it now returns a clear "SSRF: SafeDialContext called with nil dialer"
// error on first invocation instead.
func TestSSRFChecker_SafeDialContext_NilDialer(t *testing.T) {
	checker := security.NewSSRFChecker(nil)
	dialContext := checker.SafeDialContext(nil)

	require.NotPanics(t, func() {
		_, err := dialContext(context.Background(), "tcp", "example.com:443")
		require.Error(t, err, "nil dialer must produce a clear error, not a panic")
		assert.Contains(t, err.Error(), "nil dialer")
	})
}

// TestSSRFChecker_SafeDialContext_RealDNSResolution exercises SafeDialContext
// end-to-end with a real OS DNS resolution (hostname "localhost", not a
// literal IP) and a real listener, verifying the allowlist decides whether a
// private-resolving hostname may be dialed. Ported from WebFetchTool's former
// TestNewSafeDialContext_* tests during the SSRF consolidation (2026-07-07):
// SafeDialContext (extracted from the former unexported safeDialContext so
// WebFetchTool and other custom-transport callers can reuse it) previously had
// only indirect coverage through SafeClient()/CheckRedirect tests using
// httptest.Server's literal 127.0.0.1 address, which short-circuits the
// resolver via CheckHost's net.ParseIP fast path. This test forces the actual
// net.Resolver.LookupIPAddr code path.
func TestSSRFChecker_SafeDialContext_RealDNSResolution(t *testing.T) {
	t.Run("blocks localhost resolution without allowlist", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		// Test cleanup: Close error is inconsequential.
		defer func() {
			if closeErr := listener.Close(); closeErr != nil {
				_ = closeErr
			}
		}()

		_, port, err := net.SplitHostPort(listener.Addr().String())
		require.NoError(t, err)

		checker := security.NewSSRFChecker(nil)
		dialContext := checker.SafeDialContext(&net.Dialer{Timeout: time.Second})
		_, err = dialContext(context.Background(), "tcp", net.JoinHostPort("localhost", port))
		require.Error(t, err, "expected localhost DNS resolution to be blocked without an allowlist entry")
		assert.Contains(t, err.Error(), "SSRF")
	})

	t.Run("allows localhost resolution when allowlisted by CIDR", func(t *testing.T) {
		// Dual-stack loopback listener + both-loopback-family allowlist so the
		// test is robust to how the OS resolver maps "localhost". GitHub Actions
		// runners resolve localhost to ::1 (IPv6), which an IPv4-only listener
		// ("127.0.0.1:0") + IPv4-only allowlist ("127.0.0.0/8") spuriously
		// rejects (the resolved ::1 is neither allowlisted nor reachable on the
		// v4 listener). The wildcard ":0" listener accepts both 127.0.0.1 and
		// ::1 connections (Go dual-stack, IPv4-mapped), and both loopback CIDRs
		// are allowlisted — the security assertion (allowlisted loopback is
		// permitted) is unchanged, only the address family is made env-agnostic.
		listener, err := net.Listen("tcp", ":0")
		require.NoError(t, err)
		// Test cleanup: Close error is inconsequential.
		defer func() {
			if closeErr := listener.Close(); closeErr != nil {
				_ = closeErr
			}
		}()

		accepted := make(chan struct{}, 1)
		go func() {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			// Test goroutine cleanup: Close error not actionable here.
			if closeErr := conn.Close(); closeErr != nil {
				_ = closeErr
			}
			accepted <- struct{}{}
		}()

		_, port, err := net.SplitHostPort(listener.Addr().String())
		require.NoError(t, err)

		checker := security.NewSSRFChecker([]string{"127.0.0.0/8", "::1/128"})
		dialContext := checker.SafeDialContext(&net.Dialer{Timeout: time.Second})
		conn, err := dialContext(context.Background(), "tcp", net.JoinHostPort("localhost", port))
		require.NoError(t, err, "expected localhost DNS resolution to succeed once allowlisted")
		if closeErr := conn.Close(); closeErr != nil {
			_ = closeErr
		}

		select {
		case <-accepted:
		case <-time.After(time.Second):
			t.Fatal("expected localhost listener to accept a connection")
		}
	})
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
		// SSRF-blocked/error response body; draining is unnecessary and
		// the Close error is not actionable in an already-error test path.
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
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
		// SSRF-blocked/error response body; draining is unnecessary and
		// the Close error is not actionable in an already-error test path.
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
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
		if _, err := w.Write([]byte("ok")); err != nil {
			_ = err
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	checker := security.NewSSRFChecker([]string{serverHostname(t, server.URL)})
	client := checker.SafeClient()

	resp, err := client.Get(server.URL + "/start")
	require.NoError(t, err, "legitimate same-origin redirect must be followed, not blocked")
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
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
		// SSRF-blocked/error response body; draining is unnecessary and
		// the Close error is not actionable in an already-error test path.
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}
	require.Error(t, err, "an infinite same-origin redirect loop must eventually be rejected")
	assert.Contains(t, err.Error(), "too many redirects")
}

// ---------------------------------------------------------------------------
// TestBrowserSSRFAllowsGatewayLocalhostOnly — FR-018 / ADR-044 D2 / r4 OBS-003
// Traces to: docs/internal/specs/preview-on-main-listener-spec.md
//
//	US-10 (Agent built-in browser reaches the preview, SSRF scoped, new code)
//	S21   (Browser SSRF allows the gateway origin only, localhost form)
//	TDD Plan row 23: TestBrowserSSRFAllowsGatewayLocalhostOnly
//
// serve_web emits the literal, pre-resolution form
// "http://localhost:<gateway.port>/preview/…" when gateway.public_url is
// unset (D2). The checker is otherwise port-blind (extractHost historically
// strips the port) and 127.0.0.0/8 is a blanket CIDR block, so without a
// port-aware exception the built-in browser could never reach its own
// gateway. AllowGatewayOrigin's exception MUST be scoped to the exact
// gateway host:port — every other local port, and blanket loopback, must
// stay blocked (Non-Behaviors: "MUST NOT allowlist 'all localhost'").
// ---------------------------------------------------------------------------
func TestBrowserSSRFAllowsGatewayLocalhostOnly(t *testing.T) {
	const gwPort = 5000
	const otherPort = 5001

	ctx := context.Background()

	t.Run("literal localhost:<gateway.port> preview URL passes", func(t *testing.T) {
		checker := security.NewSSRFChecker(nil)
		checker.AllowGatewayOrigin("localhost", gwPort)

		err := checker.CheckURL(ctx, "http://localhost:5000/preview/agent-1/tok3n/")
		assert.NoError(t, err, "the gateway's own literal localhost:<port> preview URL must pass SSRF")
	})

	t.Run("resolved-loopback form 127.0.0.1:<gateway.port> passes", func(t *testing.T) {
		checker := security.NewSSRFChecker(nil)
		checker.AllowGatewayOrigin("localhost", gwPort)

		err := checker.CheckURL(ctx, "http://127.0.0.1:5000/preview/agent-1/tok3n/")
		assert.NoError(t, err, "the resolved IPv4 loopback form at the gateway port must pass")
	})

	t.Run("resolved-loopback form [::1]:<gateway.port> passes", func(t *testing.T) {
		checker := security.NewSSRFChecker(nil)
		checker.AllowGatewayOrigin("localhost", gwPort)

		err := checker.CheckURL(ctx, "http://[::1]:5000/preview/agent-1/tok3n/")
		assert.NoError(t, err, "the resolved IPv6 loopback form at the gateway port must pass")
	})

	t.Run("a DIFFERENT local port is still blocked", func(t *testing.T) {
		checker := security.NewSSRFChecker(nil)
		checker.AllowGatewayOrigin("localhost", gwPort)

		err := checker.CheckURL(ctx, fmt.Sprintf("http://localhost:%d/x", otherPort))
		require.Error(t, err, "a different local port must remain blocked even with the gateway origin allowed")
		assert.Contains(t, err.Error(), "SSRF")
	})

	t.Run("127.0.0.1 on a non-gateway port is still blocked", func(t *testing.T) {
		checker := security.NewSSRFChecker(nil)
		checker.AllowGatewayOrigin("localhost", gwPort)

		err := checker.CheckURL(ctx, fmt.Sprintf("http://127.0.0.1:%d/x", otherPort))
		require.Error(t, err, "the resolved-loopback form on a non-gateway port must remain blocked")
		assert.Contains(t, err.Error(), "SSRF")
	})

	t.Run(
		"no gateway origin configured: localhost at the would-be gateway port is blocked (fail closed)",
		func(t *testing.T) {
			checker := security.NewSSRFChecker(nil) // AllowGatewayOrigin never called

			err := checker.CheckURL(ctx, "http://localhost:5000/x")
			require.Error(t, err, "with no gateway origin configured, localhost must stay blocked by default")
			assert.Contains(t, err.Error(), "SSRF")
		},
	)

	t.Run("public host still passes, independent of the gateway origin exception", func(t *testing.T) {
		checker := security.NewSSRFChecker(nil)
		checker.AllowGatewayOrigin("localhost", gwPort)

		err := checker.CheckURL(ctx, "http://8.8.8.8/dns")
		assert.NoError(t, err, "a public IP must still pass — unaffected by the gateway-origin exception")
	})

	t.Run("private non-gateway IP still blocked", func(t *testing.T) {
		checker := security.NewSSRFChecker(nil)
		checker.AllowGatewayOrigin("localhost", gwPort)

		err := checker.CheckURL(ctx, "http://10.0.0.5/api")
		require.Error(t, err, "a private IP unrelated to the gateway origin must remain blocked")
		assert.Contains(t, err.Error(), "SSRF")
	})

	t.Run("clearing the gateway origin restores the fail-closed default", func(t *testing.T) {
		checker := security.NewSSRFChecker(nil)
		checker.AllowGatewayOrigin("localhost", gwPort)
		checker.AllowGatewayOrigin("", 0) // explicit clear (empty host)

		err := checker.CheckURL(ctx, "http://localhost:5000/x")
		require.Error(t, err, "clearing the gateway origin must restore the fail-closed default")
	})

	t.Run(
		"non-localhost configured gateway host: exact literal match passes, no loopback expansion",
		func(t *testing.T) {
			checker := security.NewSSRFChecker(nil)
			checker.AllowGatewayOrigin("gateway.internal.example", gwPort)

			err := checker.CheckURL(ctx, "http://gateway.internal.example:5000/preview/agent-1/tok3n/")
			assert.NoError(t, err, "an exact literal host:port match must pass without needing DNS resolution")

			errLoopback := checker.CheckURL(ctx, "http://127.0.0.1:5000/preview/agent-1/tok3n/")
			require.Error(
				t,
				errLoopback,
				"loopback expansion must NOT apply when the configured gateway host isn't 'localhost'",
			)
		},
	)
}

// TestCloneWithGatewayOrigin_DoesNotMutateSingleton is the regression for
// code-review M2: the gateway-origin exception is granted to the agent browser
// via a CLONE, so it must NOT leak onto the shared singleton that provider
// base_url / skill-installer URL validation also consult.
func TestCloneWithGatewayOrigin_DoesNotMutateSingleton(t *testing.T) {
	ctx := context.Background()
	const gwPort = 5000

	singleton := security.NewSSRFChecker(nil)
	clone := singleton.CloneWithGatewayOrigin("localhost", gwPort)

	// The CLONE reaches the gateway's own preview origin (literal + resolved forms).
	assert.NoError(t, clone.CheckURL(ctx, "http://localhost:5000/preview/a/tok/"),
		"the clone must allow the gateway's own localhost:<port> preview origin")
	assert.NoError(t, clone.CheckURL(ctx, "http://127.0.0.1:5000/preview/a/tok/"),
		"the clone must allow the resolved-loopback gateway origin")

	// ADR-073: host:port matching alone is NOT sufficient — the exception is
	// scoped to the /preview/ path prefix. Same host:port, non-/preview/ path
	// (e.g. the gateway's own internal REST API) must still be blocked, even
	// though it would have passed before ADR-073.
	require.Error(t, clone.CheckURL(ctx, "http://localhost:5000/api/v1/config"),
		"ADR-073: the gateway-origin exception must not extend beyond /preview/")
	require.Error(t, clone.CheckURL(ctx, "http://127.0.0.1:5000/api/v1/config"),
		"ADR-073: the resolved-loopback form must also be scoped to /preview/")
	require.Error(t, clone.CheckURL(ctx, "http://localhost:5000/"),
		"ADR-073: the gateway origin's own root is not /preview/ and must still be blocked")

	// The shared singleton is UNCHANGED — still blocks the gateway origin, so
	// provider/skill URL validation is not silently widened.
	require.Error(t, singleton.CheckURL(ctx, "http://localhost:5000/preview/a/tok/"),
		"the shared singleton must NOT be mutated — localhost:<port> stays blocked for provider/skill validation")
	require.Error(t, singleton.CheckURL(ctx, "http://127.0.0.1:5000/preview/a/tok/"),
		"the shared singleton must still block the resolved-loopback gateway origin")

	// The clone still enforces the shared base block-list — it is not a wide-open
	// checker, and its exception is scoped to exactly the gateway port.
	require.Error(t, clone.CheckURL(ctx, "http://169.254.169.254/latest/meta-data/"),
		"the clone must still block cloud-metadata (shares the base block-list)")
	require.Error(t, clone.CheckURL(ctx, "http://127.0.0.1:5001/preview/a/tok/"),
		"the clone's exception is scoped to the gateway port only")
	assert.NoError(t, clone.CheckURL(ctx, "http://8.8.8.8/dns"),
		"the clone still passes public hosts")

	// Clearing the exception on the clone must not affect any base behavior.
	clone.AllowGatewayOrigin("", 0)
	require.Error(t, clone.CheckURL(ctx, "http://127.0.0.1:5000/preview/a/tok/"),
		"after clearing, the clone falls back to the fail-closed default")
}

// TestGatewayOriginException_ScopedToPreviewPath is the dedicated ADR-073
// regression test: the gateway-origin SSRF exception must require BOTH the
// exact host:port AND a /preview/ path prefix, not host:port alone. Before
// ADR-073 this exception matched any path on the gateway's own origin,
// which — since it runs before every other SSRF check — let the agent's
// browser reach the gateway's internal REST API (e.g. /api/v1/config)
// through browser_navigate, bypassing tool-level access restrictions like a
// denied read_file/http tool.
func TestGatewayOriginException_ScopedToPreviewPath(t *testing.T) {
	ctx := context.Background()
	checker := security.NewSSRFChecker(nil)
	checker.AllowGatewayOrigin("localhost", 5000)

	allowed := []string{
		"http://localhost:5000/preview/",
		"http://localhost:5000/preview/agent-a/tok123/",
		"http://localhost:5000/preview/agent-a/tok123/index.html?x=1",
		"https://localhost:5000/preview/agent-a/tok123/",
	}
	for _, u := range allowed {
		assert.NoError(t, checker.CheckURL(ctx, u), "expected %q to pass the /preview/ exception", u)
	}

	blocked := []string{
		"http://localhost:5000/",
		"http://localhost:5000/api/v1/config",
		"http://localhost:5000/api/v1/agents",
		"http://localhost:5000/previewer/not-actually-preview",
		"http://localhost:5000/preview",
	}
	for _, u := range blocked {
		require.Error(t, checker.CheckURL(ctx, u), "expected %q to be blocked despite matching host:port", u)
	}
}

// TestAllowGatewayOrigin_ConcurrentWithCheckURL exercises the gwMu RWMutex under
// concurrent writers (AllowGatewayOrigin) and readers (CheckURL) — meaningful
// under `go test -race`. Uses only literal-IP URLs so no goroutine touches DNS.
func TestAllowGatewayOrigin_ConcurrentWithCheckURL(t *testing.T) {
	ctx := context.Background()
	checker := security.NewSSRFChecker(nil)

	const workers = 8
	const iters = 50
	var wg sync.WaitGroup

	// Writers: toggle the exception on/off for varying ports.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				checker.AllowGatewayOrigin("localhost", 5000+port)
				if ignoredErr := checker.CheckURL(ctx, "http://127.0.0.1:5000/x"); ignoredErr != nil {
					_ = ignoredErr
				}
				checker.AllowGatewayOrigin("", 0)
			}
		}(i)
	}
	// Readers: pure CheckURL against a literal loopback IP (no DNS).
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				if ignoredErr := checker.CheckURL(ctx, "http://127.0.0.1:5000/x"); ignoredErr != nil {
					_ = ignoredErr
				}
			}
		}()
	}
	wg.Wait()
	// Reaching here without the race detector firing means the RWMutex is sound.
}
