package email

// RED round 2 - team-lead's explicit ask: direct unit coverage of
// transport.go::isLoopbackAddr, the shared helper behind both loopback
// plaintext exceptions (imapDial in pkg/email/transport.go and the SMTP send
// path in pkg/email/smtp_send.go, commit 89e16221f). It previously had zero
// direct test coverage.
//
// Oracle: the helper's documented contract (transport.go::isLoopbackAddr +
// the D36 loopback-sink rationale in the same file): it recognizes ONLY
// literal IP-loopback forms and the exact string "localhost" (case
// insensitive). It NEVER performs DNS resolution - a hostname that would
// resolve to 127.0.0.1 (an attacker-chosen name under an attacker-chosen
// zone, or a /etc/hosts alias like ip6-localhost) must return false, because
// resolving it would let DNS decide where plaintext credentials may travel.
// Every subtest's comment states WHY, so a future reader reads this as
// intentional behavior, not an oversight.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want bool
		why  string
	}{
		{
			name: "ipv4 loopback literal",
			addr: "127.0.0.1:587",
			want: true,
			why:  "literal 127.0.0.1 is the canonical loopback IP; net.ParseIP succeeds and IsLoopback is true",
		},
		{
			name: "bracketed ipv6 loopback literal",
			addr: "[::1]:993",
			want: true,
			why:  "bracketed ::1 is the v6 loopback literal; SplitHostPort strips the brackets and ParseIP succeeds",
		},
		{
			name: "whole 127/8 block is loopback",
			addr: "127.0.0.2:587",
			want: true,
			why:  "RFC 5735: the whole 127.0.0.0/8 block is loopback; IsLoopback is true for any 127.x, not only .1",
		},
		{
			name: "exact localhost string",
			addr: "localhost:587",
			want: true,
			why:  "the exact string localhost is the one name the helper trusts",
		},
		{
			name: "case-insensitive exact localhost",
			addr: "LOCALHOST:587",
			want: true,
			why:  "strings.EqualFold compare - case variants of the exact string match; hostnames are case-insensitive",
		},
		{
			name: "localhost-prefixed hostname is NOT loopback",
			addr: "localhost.evil.example:587",
			want: false,
			why:  "SECURITY CASE: whole-string compare, never substring - a localhost-prefixed name in an attacker zone must not match; nor may it be resolved to 127.0.0.1 and trusted",
		},
		{
			name: "hosts-alias for loopback is NOT loopback",
			addr: "ip6-localhost:993",
			want: false,
			why:  "SECURITY CASE: ip6-localhost resolves to ::1 on most Unix hosts, but the helper never resolves DNS - only literal IPs and the exact string localhost match, so a resolver-level alias cannot win plaintext transport",
		},
		{
			name: "ordinary hostname",
			addr: "mail.example.com:587",
			want: false,
			why:  "an ordinary hostname is neither a literal IP nor the exact string localhost",
		},
		{
			name: "non-loopback literal IP",
			addr: "169.254.1.1:587",
			want: false,
			why:  "a literal NON-loopback IP parses but IsLoopback is false - the helper is not a bare parse-success check",
		},
		{
			name: "host part without port",
			addr: "127.0.0.1",
			want: true,
			why:  "characterization of the defensive path: SplitHostPort fails on a portless host, the helper treats the whole string as host and still recognizes the literal loopback",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isLoopbackAddr(tc.addr)
			require.Equal(t, tc.want, got, tc.why)
		})
	}
}
