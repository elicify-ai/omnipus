// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

import (
	"context"
	"net"
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
