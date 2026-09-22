// Omnipus - Ultra-lightweight personal AI gateway
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// applyLinuxSandbox distinguishes exactly one class of ApplyWithMode
// failure for graceful degradation: a landlock_create_ruleset rejection of
// the requested rights mask (sandbox.ErrLandlockRulesetRejected), which
// means the running kernel does not support a bit the ABI-gating table
// asked for. Every other Apply failure — a net-port rule rejected by a
// kernel that claims to support it (B1.4-c), a restrict_self failure —
// keeps the existing hard-fail: it must still reach SandboxBootError
// (exit 78), because it signals a capable kernel behaving inconsistently
// rather than a genuinely unsupported feature.
//
// Hard Constraint #4 (CLAUDE.md) requires graceful degradation on kernels
// that cannot deliver what the operator asked for. This file proves both
// halves of that contract: the sentinel error degrades to FallbackBackend,
// and every other error still boots to exit 78.

package gateway

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// linuxBackendRejectingApply is a SandboxBackend stub that satisfies the
// linuxApplier interface (so applySandbox takes the Linux apply path) and
// returns a fixed error from every Apply / ApplyWithMode invocation. It is
// the test stand-in for a LinuxBackend whose underlying kernel rejected
// something at Apply time.
//
// applyErr is the error returned; abiVersion is what buildPolicy reads to
// gate bind/connect port rules and to stamp the result's apply state.
type linuxBackendRejectingApply struct {
	name       string
	abiVersion int
	applyErr   error
}

func (b *linuxBackendRejectingApply) Name() string    { return b.name }
func (b *linuxBackendRejectingApply) Available() bool { return true }
func (b *linuxBackendRejectingApply) Apply(_ sandbox.SandboxPolicy) error {
	return b.applyErr
}
func (b *linuxBackendRejectingApply) ApplyWithMode(_ sandbox.SandboxPolicy, _ sandbox.Mode) error {
	return b.applyErr
}
func (b *linuxBackendRejectingApply) ApplyToCmd(_ *exec.Cmd, _ sandbox.SandboxPolicy) error {
	return nil
}
func (b *linuxBackendRejectingApply) ABIVersion() int     { return b.abiVersion }
func (b *linuxBackendRejectingApply) PolicyApplied() bool { return false }

// TestApplySandbox_LinuxRulesetRejected_DegradesToFallback is the contract
// test for the boot path's graceful-degradation behaviour when the Linux
// backend's ApplyWithMode returns an error wrapping
// sandbox.ErrLandlockRulesetRejected. Hard Constraint #4 is the policy:
// refuse-to-boot on a capable-kernel ruleset rejection is forbidden in the
// default mode; the boot must continue with application-level enforcement
// and a WARN log the operator can act on.
//
// `mode="enforce"` exercises the path operators land on by default (the
// fresh-install default is enforce on a capable kernel; resolveMode maps
// "no operator config, no CLI flag" to enforce).
func TestApplySandbox_LinuxRulesetRejected_DegradesToFallback(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sandbox.Mode = "enforce"

	stub := &linuxBackendRejectingApply{
		name:       "landlock-v1",
		abiVersion: 1,
		applyErr:   fmt.Errorf("%w: invalid argument", sandbox.ErrLandlockRulesetRejected),
	}

	result, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  stub,
		GetEnv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("applySandbox must degrade to FallbackBackend in default mode, got error: %v", err)
	}
	if result == nil {
		t.Fatal("applySandbox returned a nil result alongside a nil error — caller cannot inspect what happened")
	}
	if result.BackendName != "fallback" {
		t.Errorf("backend should be swapped to fallback, got BackendName=%q (Backend=%T)",
			result.BackendName, result.Backend)
	}
	if result.ApplyState.LandlockEnforced {
		t.Error("must not report LandlockEnforced=true after fallback")
	}
	if result.ApplyState.SeccompEnforced {
		t.Error("must not report SeccompEnforced=true after fallback (seccomp is gated on Linux backend selection per FR-J-014)")
	}
	// The degradation note must name the kernel rejection AND the
	// original backend the operator saw before the swap, so /health and
	// the audit log tell the operator what was attempted. Match the
	// non-Linux path's contract — see applyNonLinuxSandbox / ExtraNotes.
	foundKernelRejected := false
	for _, note := range result.ApplyState.ExtraNotes {
		if strings.Contains(note, "kernel rejected the Landlock ruleset") &&
			strings.Contains(note, "landlock-v1") &&
			strings.Contains(note, "application-level enforcement") {
			foundKernelRejected = true
			break
		}
	}
	if !foundKernelRejected {
		t.Errorf("expected a degradation note naming the kernel rejection and the original backend; got %v",
			result.ApplyState.ExtraNotes)
	}
	// DisabledBy must NOT carry "kernel_too_old_or_non_linux" or any other
	// auto-detected reason — those are platform-level. The Linux rejection
	// path is its own operator-meaningful reason; the boot's outcome is
	// "degraded to app-level", not "disabled by X".
	if result.DisabledBy != "" {
		t.Errorf("DisabledBy should be empty after a degradation; got %q", result.DisabledBy)
	}
}

// TestApplySandbox_LinuxRulesetRejected_DegradesInPermissiveModeToo makes
// the same contract explicit for permissive mode. Permissive on Landlock
// computes and audit-logs the policy without restricting anything
// (LinuxBackend::ApplyWithMode skips landlock_restrict_self on
// kernels ≤ 6.11), but create_ruleset still runs so a ruleset rejection
// still surfaces. The degradation contract holds regardless of mode: the
// operator asked for a sandbox, the kernel refused the ruleset, and the
// boot degrades rather than refusing to boot.
func TestApplySandbox_LinuxRulesetRejected_DegradesInPermissiveModeToo(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sandbox.Mode = "permissive"

	stub := &linuxBackendRejectingApply{
		name:       "landlock-v3",
		abiVersion: 3,
		applyErr:   fmt.Errorf("%w", sandbox.ErrLandlockRulesetRejected),
	}

	result, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  stub,
		GetEnv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("applySandbox in permissive mode must also degrade, got error: %v", err)
	}
	if result.BackendName != "fallback" {
		t.Errorf("backend should be swapped to fallback in permissive mode too, got %q", result.BackendName)
	}
	if result.Mode != sandbox.ModePermissive {
		t.Errorf("mode should be preserved as permissive, got %q", result.Mode)
	}
}

// TestApplySandbox_LinuxNetPortRuleRejected_HardFailsToBootError is the
// negative half of the degrade contract (B1.4-c): a kernel that claims
// NET_BIND_TCP/NET_CONNECT_TCP support (ABI >= 4) but rejects a net-port
// rule at landlock_add_rule is a kernel bug or inconsistent feature flag,
// not an unsupported ruleset. That error does not wrap
// sandbox.ErrLandlockRulesetRejected, so it must still reach
// SandboxBootError (exit 78) rather than silently degrading to a backend
// with no bind-port allow-list — an operator cannot detect a partial
// allow-list from the outside, so booting with one undetected is worse
// than refusing to boot.
func TestApplySandbox_LinuxNetPortRuleRejected_HardFailsToBootError(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sandbox.Mode = "enforce"

	stub := &linuxBackendRejectingApply{
		name:       "landlock-v4",
		abiVersion: 4,
		applyErr: fmt.Errorf(
			"landlock: kernel (ABI v4) rejected net bind rule for port %d: %w"+
				" — kernel claims net rule support but rejected the syscall; this indicates"+
				" a kernel bug or unsupported feature flag on this build",
			8080, errors.New("invalid argument")),
	}

	result, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  stub,
		GetEnv:   func(string) string { return "" },
	})
	if err == nil {
		t.Fatalf("applySandbox must hard-fail on a net-port rule rejection, got nil error (result=%+v)", result)
	}
	if errors.Is(err, sandbox.ErrLandlockRulesetRejected) {
		t.Errorf("net-port rule rejection must NOT be classified as ErrLandlockRulesetRejected: %v", err)
	}
	if result == nil {
		t.Fatal("applySandbox returned a nil result alongside the boot error — caller cannot inspect what was attempted")
	}
	if result.BackendName == "fallback" {
		t.Error("backend must NOT be swapped to fallback on a net-port rule rejection — that would silently boot with an incomplete allow-list")
	}
}
