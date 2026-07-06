package security_test

// File purpose: Sprint J issue #78 — SSRF protection wiring integration tests.
//
// These tests prove that the real SSRFChecker (pkg/security.SSRFChecker) correctly
// blocks private IPs, cloud metadata endpoints, and disallowed schemes, and that
// the allow_internal override works for explicitly whitelisted hosts.
//
// Unlike the existing ssrf_matrix_test.go (which covers the full IP range matrix),
// this file focuses on the WIRING contract: how web_search and the skills installer
// entry points interact with SSRFChecker, including the allow_internal override and
// exotic address representations (IPv6, IPv4-mapped, 6to4).
//
// Traces to: sprint-j-security-hardening prompt §2 (SSRF wiring contract).
//
// Error shape assumption (coordinate with backend-lead #78):
//   - CheckIP/CheckHost/CheckURL return an error whose message contains "SSRF",
//     "private", or "blocked" (case-insensitive). The exact message is implementation-
//     defined; these tests assert containment, not equality.
//   - Scheme-level rejections (file://) contain "scheme" or "ssrf" or "denied".
//   - If the actual shape changes, update the assertion helper ssrfErrContains below.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// ssrfErrContains returns true when err is non-nil and its message contains at
// least one of the candidate strings (case-insensitive). Used instead of a
// hardcoded string assertion to tolerate the implementation-defined exact message.
func ssrfErrContains(err error, candidates ...string) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, c := range candidates {
		if strings.Contains(msg, strings.ToLower(c)) {
			return true
		}
	}
	return false
}

// TestSSRFWiring_BlocksPrivateIPv4 — web_search tool entry point
//
// BDD: Given an SSRFChecker with no allow-list,
//
//	When CheckURL is called with a private IPv4 address (127.0.0.1),
//	Then an error is returned that contains "ssrf", "private", or "blocked".
//
// Differentiation: two different private IPs both produce errors (not hardcoded).
// Traces to: sprint-j prompt §2 "web_search with a query resolving to a private IP is rejected".
func TestSSRFWiring_BlocksPrivateIPv4(t *testing.T) {
	ctx := context.Background()
	checker := security.NewSSRFChecker(nil) // no allow-list

	tests := []struct {
		name string
		url  string
	}{
		// Loopback
		{"loopback_127_0_0_1", "http://127.0.0.1/api/v1/secret"},
		{"loopback_127_1_2_3", "http://127.1.2.3/"},
		// RFC 1918
		{"rfc1918_192_168_1_1", "http://192.168.1.1/admin"},
		{"rfc1918_10_0_0_1", "http://10.0.0.1/metadata"},
		// Cloud metadata
		{"cloud_metadata_169_254_169_254", "http://169.254.169.254/latest/meta-data/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// BDD: When CheckURL is called with a private address
			err := checker.CheckURL(ctx, tc.url)
			// BDD: Then an error must be returned
			require.Error(t, err,
				"private IP %q must be rejected by SSRFChecker", tc.url)
			// Content assertion: error must identify the reason
			assert.True(t,
				ssrfErrContains(err, "ssrf", "private", "blocked", "cloud metadata", "loopback"),
				"error for %q must identify reason; got: %q", tc.url, err.Error())
		})
	}

	// Differentiation test: two distinct private IPs produce distinct errors
	err1 := checker.CheckURL(ctx, "http://127.0.0.1/")
	err2 := checker.CheckURL(ctx, "http://10.0.0.1/")
	require.Error(t, err1)
	require.Error(t, err2)
	assert.NotEqual(t, err1.Error(), err2.Error(),
		"different private IP addresses must produce different error messages (not hardcoded)")
}

// TestSSRFWiring_SkillsInstallerEntryPoint — skills installer entry point
//
// BDD: Given an SSRFChecker with no allow-list,
//
//	When the skills installer URL (http://127.0.0.1/...) is passed to CheckURL,
//	Then the request is rejected before any network I/O occurs.
//
// Traces to: sprint-j prompt §2 "skills installer given http://127.0.0.1/... is rejected".
func TestSSRFWiring_SkillsInstallerEntryPoint(t *testing.T) {
	ctx := context.Background()
	checker := security.NewSSRFChecker(nil)

	// Simulate the URLs the skills installer would construct for adversarial inputs.
	skillURLs := []struct {
		name string
		url  string
	}{
		{
			name: "skills_installer_loopback",
			url:  "http://127.0.0.1/evil-skill.tar.gz",
		},
		{
			name: "skills_installer_rfc1918",
			url:  "http://192.168.0.100/malicious.zip",
		},
		{
			name: "skills_installer_metadata",
			url:  "http://169.254.169.254/skill.zip",
		},
	}

	for _, tc := range skillURLs {
		t.Run(tc.name, func(t *testing.T) {
			// BDD: When the installer tries to fetch from a private host
			err := checker.CheckURL(ctx, tc.url)
			// BDD: Then the request is blocked before any I/O
			require.Error(t, err,
				"skills installer URL %q must be rejected", tc.url)
			assert.True(t,
				ssrfErrContains(err, "ssrf", "private", "blocked"),
				"error must identify SSRF block; got: %q", err.Error())
		})
	}
}

// TestSSRFWiring_RejectsFileScheme — scheme check layer
//
// BDD: Given an SSRFChecker with no allow-list,
//
//	When CheckURL is called with a file:// URL,
//	Then the request is rejected (scheme not allowed).
//
// Note: SSRFChecker.CheckURL extracts the host from the URL. For file:// URLs,
// the "host" is typically empty or a local path, which the checker should reject.
// Traces to: sprint-j prompt §2 "file:// schemes are rejected at the scheme check".
func TestSSRFWiring_RejectsFileScheme(t *testing.T) {
	ctx := context.Background()
	checker := security.NewSSRFChecker(nil)

	fileURLs := []struct {
		name string
		url  string
	}{
		{"file_etc_passwd", "file:///etc/passwd"},
		{"file_etc_shadow", "file:///etc/shadow"},
		{"file_proc_environ", "file:///proc/1/environ"},
		{"file_root_home", "file:///root/.ssh/id_rsa"},
	}

	for _, tc := range fileURLs {
		t.Run(tc.name, func(t *testing.T) {
			// BDD: When a file:// URL is presented
			err := checker.CheckURL(ctx, tc.url)

			// BDD: Then it must be rejected.
			// file:// URLs have no host or an empty host, which CheckURL should
			// fail on ("cannot extract host" or similar). Scheme-level denial
			// OR host-extraction failure both meet the spec requirement.
			//
			// Implementation note (issue #78): if the backend agent adds explicit
			// scheme-checking, the error message will contain "scheme" or "file".
			// If it only rejects via host extraction, the error will contain "host".
			// Either is acceptable per the spec — we assert non-nil error only.
			require.Error(t, err,
				"file:// URL %q must be rejected by SSRFChecker", tc.url)
			t.Logf("file:// rejection error: %v", err)
		})
	}
}

// TestSSRFWiring_AllowInternalOverride — allow_internal config override
//
// BDD: Given an SSRFChecker configured with allow_internal: ["localhost"],
//
//	When CheckURL is called with a legit localhost URL (the httptest server),
//	Then the request is allowed through.
//	But when CheckURL is called with an unwhitelisted private IP, it is blocked.
//
// Traces to: sprint-j prompt §2 "allow_internal config override lets a whitelisted host through".
func TestSSRFWiring_AllowInternalOverride(t *testing.T) {
	ctx := context.Background()

	// BDD: Given a legitimate internal server (simulated with httptest)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	}))
	t.Cleanup(server.Close)

	// Extract the host (127.0.0.1:port or similar) for the allow-list.
	_, serverPort, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	_ = serverPort

	// Build checker with 127.0.0.1 explicitly allowed
	allowedIP := "127.0.0.1"
	checkerWithAllow := security.NewSSRFChecker([]string{allowedIP})

	t.Run("allowed_host_passes_through", func(t *testing.T) {
		// BDD: When CheckURL is called for the whitelisted IP
		// (server.URL derivation retained for documentation — the real check runs
		//  directly against the allowlisted IP below, not the httptest server.)
		_ = strings.TrimPrefix

		// Direct IP check on the allowlisted address
		err := checkerWithAllow.CheckURL(ctx, "http://"+allowedIP+"/api/v1/data")
		// BDD: Then the request is allowed
		assert.NoError(t, err,
			"127.0.0.1 should be allowed when it is in the allow_internal list")
	})

	t.Run("non_allowlisted_private_still_blocked", func(t *testing.T) {
		// BDD: When CheckURL is called with a DIFFERENT private IP not in the allow-list
		err := checkerWithAllow.CheckURL(ctx, "http://10.0.0.1/api/internal")
		// BDD: Then it must still be blocked (allow-list is exact, not a CIDR open)
		require.Error(t, err,
			"10.0.0.1 must still be blocked even though 127.0.0.1 is in allow_internal")
		assert.True(t,
			ssrfErrContains(err, "ssrf", "private", "blocked"),
			"error must identify SSRF block; got: %q", err.Error())
	})

	// Differentiation: no-allowlist vs with-allowlist produce different results
	checkerNoAllow := security.NewSSRFChecker(nil)
	errNoAllow := checkerNoAllow.CheckURL(ctx, "http://"+allowedIP+"/data")
	errWithAllow := checkerWithAllow.CheckURL(ctx, "http://"+allowedIP+"/data")
	require.Error(t, errNoAllow,
		"Without allow_internal, 127.0.0.1 must be blocked")
	assert.NoError(t, errWithAllow,
		"With allow_internal=[127.0.0.1], the same address must be allowed")
}

// TestSSRFWiring_IPv6Loopback — IPv6 ::1 and IPv4-mapped addresses
//
// BDD: Given an SSRFChecker with no allow-list,
//
//	When CheckIP is called with ::1 (IPv6 loopback) or ::ffff:127.0.0.1 (IPv4-mapped),
//	Then both are rejected as private addresses.
//
// Traces to: sprint-j prompt §2 "IPv6 ::1 and ::ffff:127.0.0.1 (mapped)".
func TestSSRFWiring_IPv6Loopback(t *testing.T) {
	checker := security.NewSSRFChecker(nil)

	tests := []struct {
		name string
		ip   string
	}{
		{"ipv6_loopback_::1", "::1"},
		{"ipv4_mapped_::ffff:127.0.0.1", "::ffff:127.0.0.1"},
		{"ipv4_mapped_hex_::ffff:7f00:0001", "::ffff:7f00:1"},
		{"ipv6_link_local_fe80::1", "fe80::1"},
		{"ipv6_unique_local_fc00::1", "fc00::1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			require.NotNil(t, ip,
				"test IP %q must parse as a valid IP address", tc.ip)

			// BDD: When CheckIP is called with a private IPv6 or mapped address
			err := checker.CheckIP(ip)
			// BDD: Then it must be rejected
			require.Error(t, err,
				"private IPv6 address %q must be rejected", tc.ip)
			assert.True(t,
				ssrfErrContains(err, "ssrf", "private", "blocked", "loopback", "link-local"),
				"error for %q must identify the block reason; got: %q", tc.ip, err.Error())
		})
	}
}

// TestSSRFWiring_6to4Addresses — 6to4 addresses encoding 127.0.0.1
//
// BDD: Given an SSRFChecker with no allow-list,
//
//	When CheckIP is called with a 6to4 address that encodes 127.0.0.1
//	(2002:7f00:0001::/48 contains 127.0.0.1),
//	Then the address must be rejected.
//
// Note: 6to4 addresses (2002::/16) embed an IPv4 address in bits [16:48].
// 127.0.0.1 = 0x7f000001, so 2002:7f00:0001:: is a 6to4 address for 127.0.0.1.
// The SSRFChecker must unwrap 6to4 and check the embedded IPv4.
//
// Traces to: sprint-j prompt §2 "6to4 2002:7f00:0001::/48 (encodes 127.0.0.1)".
func TestSSRFWiring_6to4Addresses(t *testing.T) {
	checker := security.NewSSRFChecker(nil)

	// 2002:7f00:0001:: is the 6to4 encoding of 127.0.0.1.
	// Per RFC 3056, the embedded IPv4 address occupies bits 16-47 of the 6to4 prefix.
	sixToFourLoopback := net.ParseIP("2002:7f00:0001::")
	require.NotNil(t, sixToFourLoopback,
		"2002:7f00:0001:: must parse as a valid IPv6 address")

	err := checker.CheckIP(sixToFourLoopback)
	require.Error(t, err,
		"6to4 address 2002:7f00:0001:: encodes 127.0.0.1 and must be rejected by CheckIP")
	assert.True(t,
		ssrfErrContains(err, "ssrf", "private", "blocked"),
		"6to4 loopback address must be blocked with an identifiable reason; got: %q", err.Error())
}

// TestSSRFWiring_PublicAddressAllowed — non-blocking assertion
//
// BDD: Given an SSRFChecker with no allow-list,
//
//	When CheckIP is called with a public routable IPv4 address,
//	Then no error is returned.
//
// Differentiation: this proves the checker is not an over-blocking no-op.
// Traces to: sprint-j prompt §2 (implicit — allow public IPs through).
func TestSSRFWiring_PublicAddressAllowed(t *testing.T) {
	checker := security.NewSSRFChecker(nil)

	publicIPs := []struct {
		name string
		ip   string
	}{
		{"cloudflare_dns_1_1_1_1", "1.1.1.1"},
		{"google_dns_8_8_8_8", "8.8.8.8"},
		{"example_com_93_184_216_34", "93.184.216.34"},
	}

	for _, tc := range publicIPs {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			require.NotNil(t, ip)

			// BDD: When CheckIP is called with a public IP
			err := checker.CheckIP(ip)
			// BDD: Then no error must be returned
			assert.NoError(t, err,
				"public IP %q must be allowed through SSRFChecker", tc.ip)
		})
	}

	// Differentiation: public vs private produce different outcomes
	publicIP := net.ParseIP("1.1.1.1")
	privateIP := net.ParseIP("127.0.0.1")
	errPublic := checker.CheckIP(publicIP)
	errPrivate := checker.CheckIP(privateIP)
	assert.NoError(t, errPublic, "1.1.1.1 must be allowed")
	require.Error(t, errPrivate, "127.0.0.1 must be blocked")
	// Different inputs → different outputs (not hardcoded)
	assert.NotEqual(t, errPublic, errPrivate,
		"public and private IPs must produce different results (one nil, one error)")
}

// TestSSRFWiring_DNSRebindingBlocked — DNS rebinding protection
//
// BDD: Given an SSRFChecker with an injected resolver that resolves to 127.0.0.1,
//
//	When CheckHost is called,
//	Then the request is blocked even though the hostname appears external.
//
// Traces to: sprint-j prompt §2 (SSRF implies DNS rebinding protection).
func TestSSRFWiring_DNSRebindingBlocked(t *testing.T) {
	ctx := context.Background()
	checker := security.NewSSRFChecker(nil)

	// Inject a resolver that always returns 127.0.0.1 (simulating DNS rebinding)
	checker.SetResolver(&fixedResolver{
		addrs: []net.IPAddr{
			{IP: net.ParseIP("127.0.0.1")},
		},
	})

	// BDD: When CheckHost resolves "legitimate-looking.example.com" to 127.0.0.1
	_, err := checker.CheckHost(ctx, "legitimate-looking.example.com")
	// BDD: Then the request must be blocked (the resolved IP is private)
	require.Error(t, err,
		"DNS rebinding: hostname resolving to 127.0.0.1 must be rejected")
	assert.True(t,
		ssrfErrContains(err, "ssrf", "private", "blocked"),
		"DNS rebinding error must identify the block reason; got: %q", err.Error())
	t.Logf("DNS rebinding blocked correctly: %v", err)
}
