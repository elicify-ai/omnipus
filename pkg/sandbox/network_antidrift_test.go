// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 D8/FR-042/FR-044 — network pre-flight rendering tests. Unlike D7's
// filesystem grant (a per-path {path, operation} value the anti-drift
// comparator checks one row at a time), D8's grant is SESSION-scoped and
// binary in shape: TurnPolicyInput.NetworkAutoDeny/NetworkGranted gate the
// kernel's ConnectPortRules/BindPortRules directly, independent of which
// specific command triggered the escalation (the ADR's own text: "widens
// the SESSION's rendered ConnectPortRules"). So there is no per-command
// {path,operation}-shaped matrix to compare here — these tests instead pin
// the two kernel states FR-042/FR-044 define, plus that every OTHER caller
// (NetworkAutoDeny=false — Ask, God Mode, every non-bash per-turn policy)
// keeps today's DefaultConnectPorts-seeded behavior byte-identical.
package sandbox_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

func connectPorts(policy sandbox.SandboxPolicy) map[uint16]bool {
	out := make(map[uint16]bool, len(policy.ConnectPortRules))
	for _, r := range policy.ConnectPortRules {
		out[r.Port] = true
	}
	return out
}

// TestD8_BashAutoNetwork_DeniesByDefault is FR-042: bash's Auto per-turn
// render, with no network grant, produces EMPTY ConnectPortRules and
// BindPortRules — a true kernel-enforced deny-all on Linux ABI v4+ (handled
// unconditionally at the backend level, sandbox_linux.go::computeRights)
// and on macOS Seatbelt (seatbelt_profile.go renders ConnectPortRules
// identically), not "no restriction."
func TestD8_BashAutoNetwork_DeniesByDefault(t *testing.T) {
	home := t.TempDir()
	policy := fspolicy.FSPolicy{WorkDir: home}
	kernel := sandbox.DeriveKernelPolicy(policy, sandbox.TurnPolicyInput{
		HomePath:        home,
		Model:           sandbox.FilesystemModelConfined,
		NetworkAutoDeny: true,
	})
	if len(kernel.ConnectPortRules) != 0 {
		t.Fatalf("NetworkAutoDeny with no grant: ConnectPortRules = %+v, want empty", kernel.ConnectPortRules)
	}
	if len(kernel.BindPortRules) != 0 {
		t.Fatalf("NetworkAutoDeny: BindPortRules = %+v, want empty", kernel.BindPortRules)
	}
}

// TestD8_BashAutoNetwork_GrantWidensToDefaultConnectPorts is FR-044: once
// granted, the render is EXACTLY DefaultConnectPorts — port-level, not
// domain-level, and NOT also the dev-server range TurnPolicyInput.ConnectPorts
// would otherwise carry (a granted bash network escalation is about outbound
// need, not binding a local listener — BindPortRules stays empty even after
// a grant).
func TestD8_BashAutoNetwork_GrantWidensToDefaultConnectPorts(t *testing.T) {
	home := t.TempDir()
	policy := fspolicy.FSPolicy{WorkDir: home}
	kernel := sandbox.DeriveKernelPolicy(policy, sandbox.TurnPolicyInput{
		HomePath:        home,
		Model:           sandbox.FilesystemModelConfined,
		NetworkAutoDeny: true,
		NetworkGranted:  true,
		// A widened bash render does NOT also inherit the dev-server range —
		// this must be ignored entirely once NetworkAutoDeny is set.
		ConnectPorts: []uint16{5173},
	})

	got := connectPorts(kernel)
	for _, want := range sandbox.DefaultConnectPorts {
		if !got[want] {
			t.Errorf("missing DefaultConnectPorts entry %d after network grant; got %+v", want, kernel.ConnectPortRules)
		}
	}
	if len(kernel.ConnectPortRules) != len(sandbox.DefaultConnectPorts) {
		t.Errorf("got %d connect rules %+v, want exactly the %d DefaultConnectPorts entries (dev-server range must NOT be included)",
			len(kernel.ConnectPortRules), kernel.ConnectPortRules, len(sandbox.DefaultConnectPorts))
	}
	if len(kernel.BindPortRules) != 0 {
		t.Fatalf("BindPortRules = %+v after a network grant, want empty — a network grant is about outbound connect, not binding", kernel.BindPortRules)
	}
}

// TestD8_NetworkAutoDenyFalse_KeepsTodaysBehavior is the negative control:
// every caller that leaves NetworkAutoDeny false (Ask, God Mode, every
// non-bash per-turn policy) keeps DefaultPolicyForModel's unconditional
// DefaultConnectPorts seed exactly as it was before D8, including honoring
// the ConnectPorts dev-server-range extension.
func TestD8_NetworkAutoDenyFalse_KeepsTodaysBehavior(t *testing.T) {
	home := t.TempDir()
	policy := fspolicy.FSPolicy{WorkDir: home}
	kernel := sandbox.DeriveKernelPolicy(policy, sandbox.TurnPolicyInput{
		HomePath:     home,
		Model:        sandbox.FilesystemModelConfined,
		ConnectPorts: []uint16{5173},
	})

	got := connectPorts(kernel)
	for _, want := range sandbox.DefaultConnectPorts {
		if !got[want] {
			t.Errorf("missing default port %d when NetworkAutoDeny=false", want)
		}
	}
	if !got[5173] {
		t.Errorf("missing dev-server extension port 5173 when NetworkAutoDeny=false; got %+v", kernel.ConnectPortRules)
	}
}
