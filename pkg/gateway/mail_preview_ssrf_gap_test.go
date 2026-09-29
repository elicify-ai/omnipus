package gateway

// mail_preview_ssrf_gap_test.go — feature-gate round 1, security-lead
// MEDIUM finding: mailAddrForbidden (rest_mail_preview.go, MC-41's SSRF
// screen for the remote-image proxy) is a weaker duplicate of the
// canonical checker, pkg/security/ssrf.go::SSRFChecker.CheckIP. That
// canonical list — pkg/security/ssrf.go's blockedCIDRs — includes
// "100.64.0.0/10, // Shared address space (CGN)" (RFC 6598's CGNAT range),
// which mailAddrForbidden's own loopback/private/link-local/multicast/
// unspecified checks never cover: net.IP.IsPrivate() only recognizes
// 10.0.0.0/8, 172.16.0.0/12 and 192.168.0.0/16 — RFC 6598's shared address
// space is none of those, so a CGNAT-range IP sails through unblocked
// today. The expected boundary below comes from RFC 6598 itself, not from
// running mailAddrForbidden.
//
// RED today: mailAddrForbidden has no RFC 6598 clause, so every address
// inside 100.64.0.0/10 returns false (allowed) instead of true (forbidden).

import (
	"net"
	"testing"
)

// TestMailAddrForbidden_CGNATRange pins the RFC 6598 shared address space
// (100.64.0.0/10 = 100.64.0.0 - 100.127.255.255) as forbidden, with the
// min-1/min/min+1/max-1/max/max+1 boundary the range's own definition
// implies. The two out-of-range boundary cases (min-1, max+1) also confirm
// mailAddrForbidden's EXISTING loopback/private/link-local/multicast/
// unspecified checks stay silent on them (they are ordinary public-looking
// unicast addresses outside every other clause too), so the test isolates
// the CGNAT gap specifically rather than tripping on an unrelated clause.
func TestMailAddrForbidden_CGNATRange(t *testing.T) {
	cases := []struct {
		name      string
		ip        string
		forbidden bool
	}{
		{"min-1 (100.63.255.255) is just outside the block — not CGNAT", "100.63.255.255", false},
		{"min (100.64.0.0) is the first CGNAT address — forbidden", "100.64.0.0", true},
		{"min+1 (100.64.0.1) — the finding's own example — forbidden", "100.64.0.1", true},
		{"mid-range (100.100.100.100) — forbidden", "100.100.100.100", true},
		{"max-1 (100.127.255.254) — forbidden", "100.127.255.254", true},
		{"max (100.127.255.255) is the last CGNAT address — forbidden", "100.127.255.255", true},
		{"max+1 (100.128.0.0) is just outside the block — not CGNAT", "100.128.0.0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("test setup: %q did not parse as an IP", tc.ip)
			}
			got := mailAddrForbidden(ip)
			if got != tc.forbidden {
				t.Fatalf("mailAddrForbidden(%s) = %v, want %v (RFC 6598 100.64.0.0/10 CGNAT range)",
					tc.ip, got, tc.forbidden)
			}
		})
	}
}
