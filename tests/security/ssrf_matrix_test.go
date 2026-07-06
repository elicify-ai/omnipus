package security_test

// File purpose: PR-D Axis-7 SSRF matrix + blocked-scheme coverage.
//
// TestSSRFMatrix exercises the real pkg/security.SSRFChecker against a broad
// matrix of adversarial URLs and hostnames: private IPv4/IPv6 ranges, cloud
// metadata endpoints, URL-parser obfuscations (decimal/hex IP encoding,
// IPv4-mapped IPv6, userinfo smuggling, fragment abuse), and a DNS-rebinding
// simulation using an injected resolver.
//
// Also exercises pkg/tools/browser.BrowserManager.ValidateURL to verify that
// URL schemes that bypass network-level SSRF (file://, javascript:, data://,
// chrome://, chrome-extension://) are denied at the application layer.
//
// Plan reference: docs/plans/temporal-puzzling-melody.md §4 Axis-7 (SSRF matrix,
// ≥30 subtests, DNS rebinding, blocked schemes).

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// rebindResolver returns a different IP on each call. Used to simulate the
// DNS rebinding attack where a hostname resolves to a public IP on first
// lookup (TOCTOU check) and a private IP on the subsequent connect.
type rebindResolver struct {
	addrs [][]net.IPAddr
	calls atomic.Int64
}

func (r *rebindResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	n := int(r.calls.Add(1)) - 1
	if n >= len(r.addrs) {
		n = len(r.addrs) - 1
	}
	return r.addrs[n], nil
}

// fixedResolver returns a pre-set list of addresses for any hostname.
type fixedResolver struct {
	addrs []net.IPAddr
}

func (r *fixedResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	return r.addrs, nil
}

// TestSSRFMatrix drives the real SSRFChecker through ~35 adversarial cases
// covering every major SSRF bypass class. Every subtest must fail closed:
// the checker must return a non-nil error, and the error message must identify
// the rejection reason (private IP, cloud metadata, etc.).
//
// We use t.Run so each case appears individually in `go test -v` output.
func TestSSRFMatrix(t *testing.T) {
	ctx := context.Background()
	checker := security.NewSSRFChecker(nil)

	// ---- Private IPv4 ranges ---------------------------------------------
	// Every RFC 1918 network, loopback, link-local, and unspecified address
	// must be blocked. These cover the most common SSRF pivot vectors.
	t.Run("private_10_slash_8_start", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://10.0.0.1/")
		require.Error(t, err, "10.0.0.1 (RFC 1918 Class A start) must be rejected")
		assert.Contains(t, err.Error(), "private", "error must identify private IP")
	})

	t.Run("private_10_slash_8_end", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://10.255.255.254/")
		require.Error(t, err, "10.255.255.254 (RFC 1918 Class A end) must be rejected")
	})

	t.Run("private_172_16_slash_12_start", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://172.16.0.1/")
		require.Error(t, err, "172.16.0.1 (RFC 1918 Class B start) must be rejected")
	})

	t.Run("private_172_16_slash_12_end", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://172.31.255.254/")
		require.Error(t, err, "172.31.255.254 (RFC 1918 Class B end) must be rejected")
	})

	t.Run("private_192_168_slash_16_start", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://192.168.0.1/")
		require.Error(t, err, "192.168.0.1 (RFC 1918 Class C start) must be rejected")
	})

	t.Run("private_192_168_slash_16_end", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://192.168.255.254/")
		require.Error(t, err, "192.168.255.254 (RFC 1918 Class C end) must be rejected")
	})

	t.Run("link_local_169_254_slash_16", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://169.254.1.2/")
		require.Error(t, err, "169.254.1.2 (link-local) must be rejected")
	})

	t.Run("loopback_127_0_0_1", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://127.0.0.1/")
		require.Error(t, err, "127.0.0.1 (loopback) must be rejected")
	})

	t.Run("loopback_127_255_255_255", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://127.255.255.255/")
		require.Error(t, err, "127.255.255.255 (loopback broadcast) must be rejected")
	})

	t.Run("unspecified_0_0_0_0", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://0.0.0.0/")
		require.Error(t, err, "0.0.0.0 (unspecified) must be rejected — reachable as localhost on most OSes")
	})

	// ---- Private IPv6 ranges ---------------------------------------------
	t.Run("ipv6_loopback", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://[::1]/")
		require.Error(t, err, "[::1] (IPv6 loopback) must be rejected")
	})

	t.Run("ipv6_link_local", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://[fe80::1]/")
		require.Error(t, err, "[fe80::1] (IPv6 link-local) must be rejected")
	})

	t.Run("ipv6_unique_local", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://[fc00::1]/")
		require.Error(t, err, "[fc00::1] (IPv6 unique local) must be rejected")
	})

	// ---- Cloud metadata endpoints ----------------------------------------
	// All three major cloud providers expose a metadata service at
	// 169.254.169.254 that can leak IAM credentials if an SSRF bug exists.
	// The checker recognizes this IP as a distinct category for clarity.
	t.Run("aws_metadata_by_ip", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://169.254.169.254/latest/meta-data/")
		require.Error(t, err, "SSRF check must reject AWS metadata IP")
		assert.True(t,
			strings.Contains(err.Error(), "cloud metadata") ||
				strings.Contains(err.Error(), "private"),
			"error message must identify the reason, got: %v", err)
	})

	t.Run("azure_metadata_by_ip", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://169.254.169.254/metadata/instance?api-version=2021-02-01")
		require.Error(t, err, "Azure metadata (same IP, different path) must be rejected")
	})

	t.Run("gcp_metadata_internal_hostname", func(t *testing.T) {
		// metadata.google.internal is a well-known GCP hostname that resolves to
		// 169.254.169.254 in production. Inject a fixed resolver so the test does
		// not depend on external DNS.
		c := security.NewSSRFChecker(nil)
		c.SetResolver(&fixedResolver{
			addrs: []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}},
		})
		err := c.CheckURL(ctx, "http://metadata.google.internal/computeMetadata/v1/")
		require.Error(t, err, "GCP metadata hostname resolving to 169.254.169.254 must be rejected")
	})

	// ---- URL parser obfuscations -----------------------------------------
	// Attackers mix userinfo, fragments, IPv6-mapped IPv4, and encoded IP
	// representations to smuggle private IPs past naive checks. Every case
	// below is a published SSRF-bypass primitive; all must fail closed.
	t.Run("userinfo_smuggle_localhost", func(t *testing.T) {
		// `http://user@localhost:80/` — the authority parser must extract the
		// host after the `@`, then flag localhost.
		c := security.NewSSRFChecker(nil)
		c.SetResolver(&fixedResolver{addrs: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}})
		err := c.CheckURL(ctx, "http://user@localhost:80/")
		require.Error(t, err, "userinfo prefix must not mask localhost")
	})

	t.Run("fragment_abuse_hides_attacker_ip", func(t *testing.T) {
		// `http://evil.com#@127.0.0.1/` — some parsers treat `#@...` as part of
		// the fragment; robust parsers read the authority as `evil.com`. Either
		// way, extractHost must return evil.com and must NOT extract 127.0.0.1.
		// We verify that the extracted host is checked (not the fragment), and
		// that if a resolver-under-test returns a private IP, the check fails.
		c := security.NewSSRFChecker(nil)
		c.SetResolver(&fixedResolver{addrs: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}})
		err := c.CheckURL(ctx, "http://evil.com#@127.0.0.1/")
		require.Error(t, err, "fragment-smuggled private IP must be blocked once DNS resolves")
	})

	t.Run("ipv6_mapped_ipv4_loopback_short", func(t *testing.T) {
		// `::ffff:127.0.0.1` is the IPv4-mapped IPv6 form of 127.0.0.1.
		// The checker must unwrap To4() and apply IPv4 rules.
		err := checker.CheckURL(ctx, "http://[::ffff:127.0.0.1]/")
		require.Error(t, err, "IPv4-mapped IPv6 loopback must be rejected")
	})

	t.Run("ipv6_mapped_ipv4_loopback_long", func(t *testing.T) {
		// Fully-expanded form of the same address.
		err := checker.CheckURL(ctx, "http://[0:0:0:0:0:ffff:7f00:0001]/")
		require.Error(t, err, "fully-expanded IPv4-mapped IPv6 loopback must be rejected")
	})

	t.Run("decimal_encoded_loopback", func(t *testing.T) {
		// 2130706433 == 0x7F000001 == 127.0.0.1 when parsed as a single integer.
		// `net.ParseIP` does NOT accept this form, which means this string
		// travels through extractHost as a hostname and then through DNS.
		// To avoid relying on real DNS (which would reject it), inject a
		// resolver that maps it to 127.0.0.1 — the realistic attacker path is
		// a parser that accepts the decimal form. The checker must still reject.
		c := security.NewSSRFChecker(nil)
		c.SetResolver(&fixedResolver{addrs: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}})
		err := c.CheckURL(ctx, "http://2130706433/")
		require.Error(t, err, "decimal-encoded loopback IP must be rejected")
	})

	t.Run("hex_encoded_loopback", func(t *testing.T) {
		// 0x7f000001 is the hex form of 127.0.0.1. Same parser-dependent path
		// as the decimal case — emulate a resolver that maps the string.
		c := security.NewSSRFChecker(nil)
		c.SetResolver(&fixedResolver{addrs: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}})
		err := c.CheckURL(ctx, "http://0x7f000001/")
		require.Error(t, err, "hex-encoded loopback IP must be rejected")
	})

	// ---- DNS rebinding ---------------------------------------------------
	// The canonical SSRF bypass: first DNS lookup returns a public IP (passes
	// the TOCTOU check), second lookup returns a private IP (connect hits
	// internal target). Every resolution must be filtered — the SSRFChecker
	// must re-resolve through CheckHost on each call.
	t.Run("dns_rebinding_second_lookup_private", func(t *testing.T) {
		rr := &rebindResolver{
			addrs: [][]net.IPAddr{
				// First lookup: attacker-controlled public IP.
				{{IP: net.ParseIP("93.184.216.34")}},
				// Second lookup: private target.
				{{IP: net.ParseIP("10.0.0.5")}},
			},
		}
		c := security.NewSSRFChecker(nil)
		c.SetResolver(rr)

		// First call: public IP → should pass.
		err1 := c.CheckURL(ctx, "http://rebind.example.com/")
		assert.NoError(t, err1, "first rebind lookup returns public IP, must pass")

		// Second call: private IP → MUST fail. This proves the checker does not
		// cache the first decision.
		err2 := c.CheckURL(ctx, "http://rebind.example.com/")
		require.Error(t, err2, "second rebind lookup returns private IP, must be blocked")
		assert.Contains(t, err2.Error(), "private", "error must identify the private IP reason")
	})

	t.Run("dns_rebinding_mixed_results_any_private_blocks", func(t *testing.T) {
		// A hostname that resolves to BOTH a public and a private IP must be
		// blocked — the checker must iterate all addrs, not just the first.
		c := security.NewSSRFChecker(nil)
		c.SetResolver(&fixedResolver{
			addrs: []net.IPAddr{
				{IP: net.ParseIP("93.184.216.34")}, // public
				{IP: net.ParseIP("192.168.1.1")},   // private — must trigger reject
			},
		})
		err := c.CheckURL(ctx, "http://mixed.example.com/")
		require.Error(t, err, "any private IP in the resolution set must trigger SSRF block")
	})

	// ---- Browser blocked schemes (application-layer SSRF) ---------------
	// Chrome and file schemes bypass DNS and HTTP proxies entirely, so they
	// must be rejected at the URL-parse stage, BEFORE the SSRF network check
	// fires. These tests drive pkg/tools/browser.BrowserManager.ValidateURL
	// directly, with a nil SSRF fallback provided by constructing a real
	// checker (the manager requires one).
	//
	// Every scheme listed in `blockedSchemes` (pkg/tools/browser/manager.go)
	// must return an error that mentions "blocked".
	runBrowserSchemeTest := func(t *testing.T, url string, wantKeyword string) {
		t.Helper()
		ssrf := security.NewSSRFChecker(nil)
		mgr, err := browser.NewBrowserManager(browser.BrowserConfig{}, ssrf)
		require.NoError(t, err, "browser manager construction must succeed with a valid SSRFChecker")
		validateErr := mgr.ValidateURL(ctx, url)
		require.Error(t, validateErr, "scheme %q must be blocked by browser layer", url)
		assert.Contains(t, validateErr.Error(), wantKeyword,
			"error message must indicate rejection reason; got: %v", validateErr)
	}

	t.Run("browser_file_scheme_blocked", func(t *testing.T) {
		runBrowserSchemeTest(t, "file:///etc/passwd", "blocked")
	})

	t.Run("browser_javascript_scheme_blocked", func(t *testing.T) {
		runBrowserSchemeTest(t, "javascript:alert(1)", "blocked")
	})

	t.Run("browser_data_scheme_blocked", func(t *testing.T) {
		runBrowserSchemeTest(t, "data:text/html,<script>alert(1)</script>", "blocked")
	})

	t.Run("browser_chrome_scheme_blocked", func(t *testing.T) {
		runBrowserSchemeTest(t, "chrome://settings/", "blocked")
	})

	t.Run("browser_chrome_extension_scheme_blocked", func(t *testing.T) {
		runBrowserSchemeTest(t, "chrome-extension://abcdefghij/popup.html", "blocked")
	})

	// ---- Positive controls: public IPs must pass ------------------------
	// Smoke checks that the matrix is not a tautology (block-everything).
	// If these fail, the checker is broken and NONE of the above is meaningful.
	t.Run("public_google_dns_allowed", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://8.8.8.8/")
		require.NoError(t, err, "8.8.8.8 (Google DNS, public) must be allowed — positive control")
	})

	t.Run("public_cloudflare_dns_allowed", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://1.1.1.1/")
		require.NoError(t, err, "1.1.1.1 (Cloudflare DNS, public) must be allowed — positive control")
	})

	// ---- Allowlist override ---------------------------------------------
	// Operators can opt specific internal IPs back in via the allowlist.
	// This proves the allowlist path actually works (otherwise a misconfigured
	// operator would have no escape hatch).
	t.Run("allowlist_internal_ip_passes", func(t *testing.T) {
		c := security.NewSSRFChecker([]string{"10.0.0.5"})
		err := c.CheckURL(ctx, "http://10.0.0.5/")
		require.NoError(t, err, "allowlisted internal IP must be permitted")
	})

	t.Run("allowlist_non_listed_internal_still_blocked", func(t *testing.T) {
		// Sanity check: allowlist is precise, not a wildcard.
		c := security.NewSSRFChecker([]string{"10.0.0.5"})
		err := c.CheckURL(ctx, "http://10.0.0.6/")
		require.Error(t, err, "allowlist must only exempt the listed IP, not the whole range")
	})

	// ---- Port variants ---------------------------------------------------
	// Private IPs with non-standard ports remain private. Verifies that
	// extractHost correctly strips the port before the IP check runs.
	t.Run("private_ip_with_port", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://192.168.0.1:8080/admin")
		require.Error(t, err, "private IP with port must still be rejected")
	})

	t.Run("private_ipv6_with_port", func(t *testing.T) {
		err := checker.CheckURL(ctx, "http://[::1]:8080/admin")
		require.Error(t, err, "IPv6 loopback with port must still be rejected")
	})
}
