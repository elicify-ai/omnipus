package sandbox_test

import (
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// TestBuildDevURL covers the table-driven cases described in the PR plan.
func TestBuildDevURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		agentID     string
		token       string
		gatewayHost string
		wantPath    string // must be a suffix of the result
		wantPrefix  string // expected scheme+host prefix (empty = path-only)
		pathOnly    bool   // when true, expect no scheme/host
	}{
		{
			name:     "empty gatewayHost returns path only",
			agentID:  "agent1",
			token:    "tok1",
			pathOnly: true,
			wantPath: "/dev/agent1/tok1/",
		},
		{
			name:        "bare host with port gets https:// prepended",
			agentID:     "agent2",
			token:       "tok2",
			gatewayHost: "127.0.0.1:5001",
			wantPrefix:  "https://127.0.0.1:5001",
			wantPath:    "/dev/agent2/tok2/",
		},
		{
			name:        "https:// host preserved as-is",
			agentID:     "agent3",
			token:       "tok3",
			gatewayHost: "https://example.com",
			wantPrefix:  "https://example.com",
			wantPath:    "/dev/agent3/tok3/",
		},
		{
			name:        "http:// host preserved as-is (operator opted out of TLS)",
			agentID:     "agent4",
			token:       "tok4",
			gatewayHost: "http://192.168.1.1:5001",
			wantPrefix:  "http://192.168.1.1:5001",
			wantPath:    "/dev/agent4/tok4/",
		},
		{
			name:        "trailing slash on gatewayHost stripped",
			agentID:     "agent5",
			token:       "tok5",
			gatewayHost: "https://example.com/",
			wantPrefix:  "https://example.com",
			wantPath:    "/dev/agent5/tok5/",
		},
		{
			name:        "host without scheme gets https:// — no trailing double-slash",
			agentID:     "agent6",
			token:       "tok6",
			gatewayHost: "preview.example.com",
			wantPrefix:  "https://preview.example.com",
			wantPath:    "/dev/agent6/tok6/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sandbox.BuildDevURL(tc.agentID, tc.token, tc.gatewayHost)

			if tc.pathOnly {
				if got != tc.wantPath {
					t.Errorf("BuildDevURL(%q, %q, %q) = %q; want %q",
						tc.agentID, tc.token, tc.gatewayHost, got, tc.wantPath)
				}
				return
			}

			// Verify scheme+host prefix.
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("BuildDevURL result %q does not start with %q", got, tc.wantPrefix)
			}
			// Verify path suffix.
			if !strings.HasSuffix(got, tc.wantPath) {
				t.Errorf("BuildDevURL result %q does not end with %q", got, tc.wantPath)
			}
			// Verify no double-slash at the prefix/path join.
			if strings.Contains(got, "//dev/") {
				t.Errorf("BuildDevURL result %q contains double-slash before /dev/", got)
			}
			// Verify no trailing double-slash anywhere.
			if strings.Contains(got[:len(got)-1], "//") && !strings.Contains(got, "://") {
				t.Errorf("BuildDevURL result %q contains unexpected double-slash", got)
			}
		})
	}
}

// TestBuildDevURL_IPv6 covers bare IPv6 and bracketed IPv6:port inputs.
// The function must bracket a bare IPv6 host and must not double-bracket
// an already-bracketed host:port.
//
// Traces to: quizzical-marinating-frog.md pr-test-analyzer Test-8.
func TestBuildDevURL_IPv6(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		agentID     string
		token       string
		gatewayHost string
		wantPrefix  string
		wantPath    string
	}{
		{
			name:        "bare IPv6 loopback is bracketed with https",
			agentID:     "agent-ipv6",
			token:       "tok-ipv6",
			gatewayHost: "::1",
			wantPrefix:  "https://[::1]",
			wantPath:    "/dev/agent-ipv6/tok-ipv6/",
		},
		{
			name:        "bracketed IPv6 with port is preserved",
			agentID:     "agent-ipv6b",
			token:       "tok-ipv6b",
			gatewayHost: "[::1]:5173",
			wantPrefix:  "https://[::1]:5173",
			wantPath:    "/dev/agent-ipv6b/tok-ipv6b/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sandbox.BuildDevURL(tc.agentID, tc.token, tc.gatewayHost)

			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("BuildDevURL(%q, %q, %q) = %q; want prefix %q",
					tc.agentID, tc.token, tc.gatewayHost, got, tc.wantPrefix)
			}
			if !strings.HasSuffix(got, tc.wantPath) {
				t.Errorf("BuildDevURL result %q does not end with %q", got, tc.wantPath)
			}
			// Verify the result contains no double-slash (except after scheme).
			if strings.Contains(got, "//dev/") {
				t.Errorf("BuildDevURL result %q contains double-slash before /dev/", got)
			}
		})
	}
}
