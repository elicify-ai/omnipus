package gateway

// mail_preview_ssrf_gap_test.go — delta-review round 2, item 1
// (security-lead): the prior round's fix added the RFC 6598 CGNAT range
// directly to the hand-rolled mailAddrForbidden instead of delegating to
// the canonical checker, pkg/security/ssrf.go::SSRFChecker.CheckIP. A
// hand-rolled duplicate misses whatever the canonical checker covers that
// the duplicate's author didn't think to copy — TEST-NET-1/2/3
// (RFC 5737), the benchmarking range (RFC 2544, 198.18.0.0/15), the IETF
// protocol-assignments range (192.0.0.0/24), and IPv6-embedded IPv4
// addresses (e.g. IPv4-mapped ::ffff:a.b.c.d) among them.
//
// The fix replaces mailAddrForbidden with mailImageAddrForbidden, which
// delegates to a zero-value security.SSRFChecker's CheckIP (fail-closed,
// always-on, independent of the operator's general SSRF toggle — see the
// doc comment on mailImageSSRFChecker in rest_mail_preview.go) instead of
// re-implementing an IP-range block list.
//
// RED today: mailImageAddrForbidden does not exist yet (mailAddrForbidden
// does, and lacks every range this test pins), so this file fails to
// compile against the pre-fix code — the strongest possible RED for a
// rename-and-delegate fix.

import (
	"net"
	"testing"
)

// TestMailImageAddrForbidden_DelegatesToCanonicalSSRFChecker pins every
// range the canonical checker (pkg/security/ssrf.go::SSRFChecker.CheckIP)
// covers that the deleted hand-rolled mailAddrForbidden did not, plus the
// RFC 6598 CGNAT boundary the previous round already fixed (kept working
// through the new delegating path), plus a public-IP negative control.
// Expected values come from the RFCs the ranges are defined by (RFC 6598,
// RFC 5737, RFC 2544, IANA IPv4 special-purpose registry), never from
// reading mailImageAddrForbidden's or SSRFChecker's implementation.
func TestMailImageAddrForbidden_DelegatesToCanonicalSSRFChecker(t *testing.T) {
	cases := []struct {
		name      string
		ip        string
		forbidden bool
	}{
		// RFC 6598 CGNAT (100.64.0.0/10) — the previous round's fix, now
		// proven through the new delegating path rather than the deleted
		// hand-rolled clause.
		{"CGNAT min-1 (100.63.255.255) is just outside the block", "100.63.255.255", false},
		{"CGNAT min (100.64.0.0) is the first CGNAT address", "100.64.0.0", true},
		{"CGNAT min+1 (100.64.0.1)", "100.64.0.1", true},
		{"CGNAT mid-range (100.100.100.100)", "100.100.100.100", true},
		{"CGNAT max-1 (100.127.255.254)", "100.127.255.254", true},
		{"CGNAT max (100.127.255.255) is the last CGNAT address", "100.127.255.255", true},
		{"CGNAT max+1 (100.128.0.0) is just outside the block", "100.128.0.0", false},

		// RFC 5737 documentation ranges (TEST-NET-1/2/3) — never routable,
		// but not caught by IsPrivate/IsLoopback/IsLinkLocal*/IsMulticast/
		// IsUnspecified, so the deleted hand-rolled function let them through.
		{"TEST-NET-1 (192.0.2.1)", "192.0.2.1", true},
		{"TEST-NET-2 (198.51.100.1)", "198.51.100.1", true},
		{"TEST-NET-3 (203.0.113.1)", "203.0.113.1", true},

		// RFC 2544 benchmarking range (198.18.0.0/15).
		{"benchmarking min (198.18.0.0)", "198.18.0.0", true},
		{"benchmarking max (198.19.255.255)", "198.19.255.255", true},
		{"just outside benchmarking (198.20.0.0)", "198.20.0.0", false},

		// IANA IPv4 special-purpose: IETF protocol assignments (192.0.0.0/24).
		{"IETF protocol assignments (192.0.0.8)", "192.0.0.8", true},

		// IPv6-embedded IPv4 (IPv4-mapped): must unwrap and check the
		// embedded IPv4 address against the same private-range rules.
		{"IPv4-mapped IPv6 embedding a private address (::ffff:192.168.1.1)", "::ffff:192.168.1.1", true},

		// Negative control: an ordinary public IP must stay allowed.
		{"public IP (8.8.8.8) stays allowed", "8.8.8.8", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("test setup: %q did not parse as an IP", tc.ip)
			}
			got := mailImageAddrForbidden(ip)
			if got != tc.forbidden {
				t.Fatalf("mailImageAddrForbidden(%s) = %v, want %v", tc.ip, got, tc.forbidden)
			}
		})
	}
}
