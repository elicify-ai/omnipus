package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// sandbox_apply_gateway_port_test.go pins the one property whose absence caused
// CI's intermittent `net::ERR_NETWORK_ACCESS_DENIED` on the WebRTC live-video
// test: the gateway's OWN listening port must be in the Landlock bind AND
// connect allow-lists.
//
// Why it must be asserted rather than reasoned about: the managed Chrome is a
// child of the gateway process, so when it inherits the Landlock domain its
// connect(2) to `http://localhost:<gateway.port>/browser-start` and to
// `ws://127.0.0.1:<gateway.port>/api/v1/browser/capture-ingest` returns EACCES,
// which Chromium reports verbatim as ERR_NETWORK_ACCESS_DENIED. Nothing about
// that failure names a port, so the missing allow-list entry was invisible for
// as long as it existed.
//
// These tests exercise sandboxExtraPorts directly rather than applySandbox,
// deliberately: applySandbox's Linux path installs a REAL seccomp filter on the
// calling process (Step 7), which would permanently restrict the test binary.

func portSet(t *testing.T, ports []uint16) map[uint16]int {
	t.Helper()
	seen := make(map[uint16]int, len(ports))
	for _, p := range ports {
		seen[p]++
	}
	return seen
}

// TestSandboxExtraPorts_IncludesGatewayPort is the direct regression guard.
// Before the fix, only DevServerPortRange was returned and this failed for every
// realistic gateway port — production's 5000 and CI's 6060 alike.
func TestSandboxExtraPorts_IncludesGatewayPort(t *testing.T) {
	for _, gwPort := range []int{5000, 6060, 8080} {
		cfg := &config.Config{}
		cfg.Gateway.Port = gwPort
		cfg.Sandbox.DevServerPortRange = config.PortRange{18000, 18999}

		got := portSet(t, sandboxExtraPorts(cfg))
		if got[uint16(gwPort)] == 0 {
			t.Errorf("gateway port %d missing from the sandbox port allow-list; "+
				"the gateway's own managed Chrome cannot reach /browser-start, "+
				"/preview/ or the capture-ingest WS when it inherits the Landlock domain",
				gwPort)
		}
		// The dev-server range must survive the change that added the gateway port.
		for _, p := range []uint16{18000, 18500, 18999} {
			if got[p] == 0 {
				t.Errorf("gateway port %d: dev-server port %d was dropped from the allow-list", gwPort, p)
			}
		}
		if got[17999] != 0 || got[19000] != 0 {
			t.Errorf("gateway port %d: allow-list leaked outside DevServerPortRange", gwPort)
		}
	}
}

// TestSandboxExtraPorts_IncludesWebRTCMediaTCPPortWhenConfigured — the ICE-TCP
// listener is bound lazily, long after applySandbox has run, so an unlisted
// port is a bind denial at the moment a viewer first attaches.
func TestSandboxExtraPorts_IncludesWebRTCMediaTCPPortWhenConfigured(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Port = 5000
	cfg.Sandbox.DevServerPortRange = config.PortRange{18000, 18999}
	cfg.Tools.Browser.WebRTCMediaTCPPort = 50001

	got := portSet(t, sandboxExtraPorts(cfg))
	if got[50001] == 0 {
		t.Fatal("configured WebRTC ICE-TCP media port missing from the sandbox port allow-list")
	}
}

// TestSandboxExtraPorts_SkipsUnsetAndOutOfRangePorts — 0 means "not configured"
// for WebRTCMediaTCPPort, and port 0 is not a real port. Emitting a rule for it
// would be a meaningless kernel rule at best.
func TestSandboxExtraPorts_SkipsUnsetAndOutOfRangePorts(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Port = 0
	cfg.Tools.Browser.WebRTCMediaTCPPort = 0
	cfg.Sandbox.DevServerPortRange = config.PortRange{18000, 18001}

	got := sandboxExtraPorts(cfg)
	if len(got) != 2 {
		t.Fatalf("expected exactly the 2 dev-server ports, got %v", got)
	}
	for _, p := range got {
		if p == 0 {
			t.Fatal("port 0 must never be emitted as a Landlock rule")
		}
	}
}

// TestSandboxExtraPorts_NoDuplicateRules — a gateway port that already falls
// inside DevServerPortRange must not produce two identical kernel rules.
func TestSandboxExtraPorts_NoDuplicateRules(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Port = 18042 // deliberately inside the dev-server range
	cfg.Sandbox.DevServerPortRange = config.PortRange{18000, 18099}

	got := portSet(t, sandboxExtraPorts(cfg))
	if got[18042] != 1 {
		t.Fatalf("port 18042 emitted %d times; expected exactly 1", got[18042])
	}
}

// TestSandboxExtraPorts_NilConfig — applySandbox tolerates a nil Cfg on several
// paths, so the helper must too rather than panicking during boot.
func TestSandboxExtraPorts_NilConfig(t *testing.T) {
	if got := sandboxExtraPorts(nil); got != nil {
		t.Fatalf("expected nil for a nil config, got %v", got)
	}
}
