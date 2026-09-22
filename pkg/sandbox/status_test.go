// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDescribeBackend_Nil verifies that a nil backend produces a "none" status
// with Available=false and KernelLevel=false.
func TestDescribeBackend_Nil(t *testing.T) {
	status := DescribeBackend(nil)

	assert.Equal(t, "none", status.Backend)
	assert.False(t, status.Available)
	assert.False(t, status.KernelLevel)
	assert.False(t, status.PolicyApplied)
	assert.Equal(t, 0, status.ABIVersion)
	assert.Empty(t, status.BlockedSyscalls)
	assert.False(t, status.SeccompEnabled)
	assert.Empty(t, status.LandlockFeatures)
	assert.Empty(t, status.Notes)
}

// TestDescribeBackend_Fallback verifies that a FallbackBackend reports the
// correct status: backend="fallback", available=true, kernel_level=false, and
// no seccomp or Landlock ABI details.
func TestDescribeBackend_Fallback(t *testing.T) {
	fb := NewFallbackBackend()
	status := DescribeBackend(fb)

	assert.Equal(t, "fallback", status.Backend)
	assert.True(t, status.Available)
	assert.False(t, status.KernelLevel, "FallbackBackend must not report kernel_level=true")
	assert.Equal(t, 0, status.ABIVersion, "fallback must not report an ABI version")
	assert.Empty(t, status.BlockedSyscalls, "fallback must not report blocked syscalls")
	assert.False(t, status.SeccompEnabled, "fallback must not report seccomp as enabled")
	assert.Empty(t, status.LandlockFeatures, "fallback must not report Landlock features")
}

// mockABIBackend is a SandboxBackend that satisfies the abiReporter interface,
// simulating a LinuxBackend for platforms where the real LinuxBackend is
// unavailable (e.g., non-Linux CI or Linux < 5.13).
//
// applied controls whether it also satisfies policyApplyReporter: when true,
// PolicyApplied() returns true and DescribeBackend treats the backend as
// enforcing. When false, the status reports capability without enforcement.
type mockABIBackend struct {
	abiVersion int
	applied    bool
}

func (m *mockABIBackend) Name() string                                  { return "landlock-v3" }
func (m *mockABIBackend) Available() bool                               { return true }
func (m *mockABIBackend) Apply(_ SandboxPolicy) error                   { return nil }
func (m *mockABIBackend) ApplyToCmd(_ *exec.Cmd, _ SandboxPolicy) error { return nil }
func (m *mockABIBackend) ABIVersion() int                               { return m.abiVersion }
func (m *mockABIBackend) PolicyApplied() bool                           { return m.applied }

// TestDescribeBackend_LinuxReportsABI_Enforcing verifies that a backend
// implementing BOTH abiReporter and policyApplyReporter (with applied=true)
// is correctly reported as kernel_level=true AND policy_applied=true, with
// no status notes.
func TestDescribeBackend_LinuxReportsABI_Enforcing(t *testing.T) {
	mock := &mockABIBackend{abiVersion: 3, applied: true}
	status := DescribeBackend(mock)

	assert.Equal(t, "landlock-v3", status.Backend)
	assert.True(t, status.Available)
	assert.True(t, status.KernelLevel, "mock ABI backend must be reported as kernel_level=true")
	assert.True(t, status.PolicyApplied, "applied=true must produce policy_applied=true")
	assert.Equal(t, 3, status.ABIVersion)
	assert.True(t, status.SeccompEnabled, "seccomp must be reported as enabled when policy is applied")
	assert.Empty(t, status.Notes, "enforcing backend must have no status notes")

	// ABI v3 includes REFER (ABI v2) and TRUNCATE (ABI v3) but NOT IOCTL_DEV
	// (ABI v5 / kernel 6.10) or the net TCP rights (ABI v4 / kernel 6.7).
	require.NotEmpty(t, status.LandlockFeatures, "ABI v3 must have Landlock features")
	assert.Contains(t, status.LandlockFeatures, "REFER", "ABI v3 must include REFER (ABI v2)")
	assert.Contains(t, status.LandlockFeatures, "TRUNCATE", "ABI v3 must include TRUNCATE (ABI v3)")
	assert.NotContains(t, status.LandlockFeatures, "IOCTL_DEV", "ABI v3 must NOT include IOCTL_DEV (ABI v5)")
	assert.NotContains(t, status.LandlockFeatures, "NET_BIND_TCP", "ABI v3 must NOT include NET_BIND_TCP (ABI v4)")
	assert.Contains(t, status.LandlockFeatures, "EXECUTE", "ABI v3 must include EXECUTE")

	// Blocked syscalls must list the canonical set from BuildSeccompProgram.
	require.NotEmpty(t, status.BlockedSyscalls, "must report blocked syscalls for kernel backends")
	assert.Contains(t, status.BlockedSyscalls, "ptrace", "ptrace must be in the blocked syscall list")
	assert.Contains(t, status.BlockedSyscalls, "bpf", "bpf must be in the blocked syscall list")
	assert.Contains(t, status.BlockedSyscalls, "mount", "mount must be in the blocked syscall list")
}

// TestDescribeBackend_CapableButNotEnforcing verifies that a kernel-capable
// backend whose Apply() has NOT been called is reported as kernel_level=true
// but policy_applied=false, with seccomp reported as NOT enabled (because
// it isn't installed either) and a status note explaining the gap.
//
// This prevents the "UI claims seccomp is enabled while the process has no
// filter installed" silent-failure mode that would otherwise mislead operators.
func TestDescribeBackend_CapableButNotEnforcing(t *testing.T) {
	mock := &mockABIBackend{abiVersion: 3, applied: false}
	status := DescribeBackend(mock)

	assert.True(t, status.KernelLevel, "capability is present")
	assert.False(t, status.PolicyApplied, "Apply() never ran, so not enforcing")
	assert.False(t, status.SeccompEnabled,
		"seccomp must be reported as NOT enabled when policy is not applied")
	require.NotEmpty(t, status.Notes, "must surface a note explaining the capability/enforcement gap")
	assert.Contains(t, status.Notes[0], "not currently restricted",
		"note must tell the operator that children are not restricted")
}

// TestDescribeBackend_ABI1Features verifies that ABI version 1 only reports
// base features (no REFER, no TRUNCATE, no IOCTL_DEV).
func TestDescribeBackend_ABI1Features(t *testing.T) {
	mock := &mockABIBackend{abiVersion: 1, applied: true}
	status := DescribeBackend(mock)

	assert.Equal(t, 1, status.ABIVersion)
	assert.NotContains(t, status.LandlockFeatures, "REFER", "ABI v1 must not include REFER (added in ABI v2 / kernel 5.19)")
	assert.NotContains(t, status.LandlockFeatures, "TRUNCATE", "ABI v1 must not include TRUNCATE (added in ABI v3 / kernel 6.2)")
	assert.NotContains(t, status.LandlockFeatures, "IOCTL_DEV", "ABI v1 must not include IOCTL_DEV (added in ABI v5 / kernel 6.10)")
	assert.NotContains(t, status.LandlockFeatures, "NET_BIND_TCP", "ABI v1 must not include NET_BIND_TCP (added in ABI v4 / kernel 6.7)")
	assert.Contains(t, status.LandlockFeatures, "EXECUTE", "ABI v1 must include base EXECUTE feature")
}

// TestDescribeBackend_ABI2Features verifies that ABI version 2 includes
// REFER (kernel 5.19) but NOT TRUNCATE (kernel 6.2) or IOCTL_DEV (kernel
// 6.10).
func TestDescribeBackend_ABI2Features(t *testing.T) {
	mock := &mockABIBackend{abiVersion: 2, applied: true}
	status := DescribeBackend(mock)

	assert.Equal(t, 2, status.ABIVersion)
	assert.Contains(t, status.LandlockFeatures, "REFER", "ABI v2 must include REFER")
	assert.NotContains(t, status.LandlockFeatures, "TRUNCATE", "ABI v2 must NOT include TRUNCATE (added in ABI v3 / kernel 6.2)")
	assert.NotContains(t, status.LandlockFeatures, "IOCTL_DEV", "ABI v2 must not include IOCTL_DEV (added in ABI v5)")
	assert.NotContains(t, status.LandlockFeatures, "NET_BIND_TCP", "ABI v2 must not include NET_BIND_TCP (added in ABI v4)")
}

// TestDescribeBackendWithState_DisabledByCliFlag verifies that when the
// operator explicitly disabled the sandbox via --sandbox=off, the status
// note describes an operator choice rather than the scary "Apply() has
// not been called" warning. Bug 2 from public-IP test findings.
func TestDescribeBackendWithState_DisabledByCliFlag(t *testing.T) {
	mock := &mockABIBackend{abiVersion: 3, applied: false}
	status := DescribeBackendWithState(mock, ApplyState{
		Mode:       ModeOff,
		DisabledBy: "cli_flag",
	})

	assert.True(t, status.KernelLevel,
		"backend is still kernel-capable even when disabled")
	assert.False(t, status.PolicyApplied, "off mode must NOT report PolicyApplied=true")
	require.Len(t, status.Notes, 1, "expect exactly one note for this branch")
	note := status.Notes[0]
	assert.Contains(t, note, "--sandbox CLI flag",
		"note must reference the CLI flag as the reason")
	assert.Contains(t, note, "operator choice",
		"note must frame the state as a deliberate operator choice")
	assert.NotContains(t, note, "Apply() has not been called",
		"must not use the gap-warning wording when operator disabled explicitly")
}

// TestDescribeBackendWithState_DisabledByConfig verifies the equivalent
// informational note when the operator set gateway.sandbox.mode=off in
// config.json. Bug 2 from public-IP test findings.
func TestDescribeBackendWithState_DisabledByConfig(t *testing.T) {
	mock := &mockABIBackend{abiVersion: 3, applied: false}
	status := DescribeBackendWithState(mock, ApplyState{
		Mode:       ModeOff,
		DisabledBy: "config",
	})

	assert.False(t, status.PolicyApplied)
	require.Len(t, status.Notes, 1)
	note := status.Notes[0]
	assert.Contains(t, note, "gateway.sandbox.mode=off",
		"note must name the config key")
	assert.Contains(t, note, "operator choice")
	assert.NotContains(t, note, "Apply() has not been called")
}

// TestDescribeBackendWithState_UnexpectedGapKeepsWarning verifies that when
// the kernel is capable, the backend reports not-applied, and there is NO
// DisabledBy marker, the original "Apply() has not been called" warning
// still fires — that's a real gap operators need to see.
func TestDescribeBackendWithState_UnexpectedGapKeepsWarning(t *testing.T) {
	mock := &mockABIBackend{abiVersion: 3, applied: false}
	status := DescribeBackendWithState(mock, ApplyState{
		Mode:       "", // no explicit mode
		DisabledBy: "",
	})

	assert.False(t, status.PolicyApplied)
	require.Len(t, status.Notes, 1)
	assert.Contains(t, status.Notes[0], "Apply() has not been called",
		"unexpected-disabled case (no DisabledBy) must still surface the original gap warning")
}
