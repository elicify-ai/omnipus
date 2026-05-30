// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import "testing"

// TestDefaultPolicy_NetRulesNilByDefault verifies that callers who pass
// nil for the bind-port list get a SandboxPolicy with no BindPortRules.
// This is the legacy default and ensures pre-ABI-v4 kernels remain
// unrestricted on the network axis.
func TestDefaultPolicy_NetRulesNilByDefault(t *testing.T) {
	policy := DefaultPolicy("/tmp/home", nil, nil, nil)
	if len(policy.BindPortRules) != 0 {
		t.Errorf("BindPortRules: got %d, want 0", len(policy.BindPortRules))
	}
}

// TestDefaultPolicy_NetRulesExpanded verifies the gateway expansion: a
// dev-server range becomes one NetPortRule per port in BindPortRules.
// 18000-18002 is three bind ports.
func TestDefaultPolicy_NetRulesExpanded(t *testing.T) {
	bindPorts := []uint16{18000, 18001, 18002}
	policy := DefaultPolicy("/tmp/home", nil, nil, bindPorts)

	if got, want := len(policy.BindPortRules), 3; got != want {
		t.Errorf("BindPortRules count: got %d, want %d", got, want)
	}
	// Order is preserved — the gateway relies on this when emitting
	// rules in the same order they appear in cfg.Sandbox.DevServerPortRange.
	for i, want := range bindPorts {
		if got := policy.BindPortRules[i].Port; got != want {
			t.Errorf("BindPortRules[%d]: got %d, want %d", i, got, want)
		}
	}
}

// TestDefaultPolicy_NetRulesEmptyVsNil ensures a zero-length non-nil slice
// behaves the same as nil — both leave the policy's BindPortRules empty.
// Important because callers may build a slice and then never append.
func TestDefaultPolicy_NetRulesEmptyVsNil(t *testing.T) {
	empty := []uint16{}
	policy := DefaultPolicy("/tmp/home", nil, nil, empty)
	if len(policy.BindPortRules) != 0 {
		t.Errorf("BindPortRules from empty slice: got %d, want 0", len(policy.BindPortRules))
	}
}

// TestDefaultPolicy_ConnectRulesSeeded verifies v0.2 (#155 item 4): every
// SandboxPolicy returned by DefaultPolicy carries the baseline
// connect-port allow-list (DefaultConnectPorts = {53, 80, 443}). On
// Landlock ABI v4+ this becomes kernel-enforced — outbound TCP to any
// port outside this set returns EACCES from the gateway and every forked
// child. Pre-ABI-v4 kernels compute the same rules but the kernel ignores
// them; a boot-time WARN documents the degradation.
//
// This test is a regression guard against an accidental revert of the
// connect-port enforcement. Removing DefaultConnectPorts seeding here
// would silently re-open the raw-TCP-egress hole that was closed by
// re-introducing ConnectPortRules.
func TestDefaultPolicy_ConnectRulesSeeded(t *testing.T) {
	policy := DefaultPolicy("/tmp/home", nil, nil, nil)

	if got, want := len(policy.ConnectPortRules), len(DefaultConnectPorts); got != want {
		t.Fatalf("ConnectPortRules count: got %d, want %d", got, want)
	}

	// Order must match DefaultConnectPorts exactly so operators reading
	// the audit log can correlate the slice index with the documented
	// default-list ordering.
	for i, want := range DefaultConnectPorts {
		if got := policy.ConnectPortRules[i].Port; got != want {
			t.Errorf("ConnectPortRules[%d]: got %d, want %d", i, got, want)
		}
	}

	// The default list MUST contain at least 53/80/443. If a future
	// refactor swaps in different ports, this assertion forces the
	// author to revisit the rationale (DNS/HTTP/HTTPS coverage for the
	// gateway's outbound LLM and provider calls).
	wanted := map[uint16]bool{53: false, 80: false, 443: false}
	for _, rule := range policy.ConnectPortRules {
		if _, ok := wanted[rule.Port]; ok {
			wanted[rule.Port] = true
		}
	}
	for port, found := range wanted {
		if !found {
			t.Errorf("DefaultConnectPorts missing required port %d (DNS=53, HTTP=80, HTTPS=443 are mandatory)", port)
		}
	}
}

// TestDefaultPolicy_ConnectRulesNotMutatedByCallers verifies that the
// slice returned to callers is independent of DefaultConnectPorts — a
// caller mutating policy.ConnectPortRules must not alter the package-
// level baseline used by future DefaultPolicy() invocations. Without
// this isolation, an agent loop that appends extra ports for one agent
// would silently widen every other agent's allow-list.
func TestDefaultPolicy_ConnectRulesNotMutatedByCallers(t *testing.T) {
	policy := DefaultPolicy("/tmp/home", nil, nil, nil)
	// Mutate the returned slice.
	if len(policy.ConnectPortRules) > 0 {
		policy.ConnectPortRules[0].Port = 9999
	}

	// A fresh call must still see the original {53, 80, 443}.
	fresh := DefaultPolicy("/tmp/home", nil, nil, nil)
	for i, want := range DefaultConnectPorts {
		if got := fresh.ConnectPortRules[i].Port; got != want {
			t.Errorf("DefaultConnectPorts polluted by caller mutation: fresh[%d]=%d, want %d", i, got, want)
		}
	}
}
