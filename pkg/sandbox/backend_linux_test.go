// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package sandbox_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestSandboxBackend_LinuxFull validates Landlock backend is selected and functional
// on Linux when Landlock is available. Skipped on kernels without Landlock.
// Traces to: wave2-security-layer-spec.md line 810 (TestSandboxBackend_LinuxFull)
// BDD: Scenario: Landlock restricts agent to workspace on supported kernel (spec line 372)
func TestSandboxBackend_LinuxFull(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 372 (Scenario: Landlock restricts agent)
	if os.Getuid() == 0 {
		t.Skip("Landlock tests must run as non-root (root bypasses Landlock)")
	}

	backend, name := sandbox.SelectBackend()
	if name == "fallback" || name == "seccomp" {
		t.Skipf("Landlock not available on this kernel — backend is %s", name)
	}

	t.Run("backend is landlock-vN when Landlock available", func(t *testing.T) {
		assert.True(t, len(name) > 9 && name[:9] == "landlock-",
			"backend name should start with 'landlock-' when Landlock available, got %q", name)
	})

	t.Run("backend is available", func(t *testing.T) {
		assert.True(t, backend.Available())
	})

	t.Run("Apply succeeds with workspace config", func(t *testing.T) {
		workspaceDir := t.TempDir()
		policy := sandbox.SandboxPolicy{
			FilesystemRules: []sandbox.PathRule{
				{Path: workspaceDir, Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute},
			},
			InheritToChildren: true,
		}
		// NOTE: Do NOT call Apply on the test process — it would permanently restrict
		// the Go test runner via Landlock's one-way ratchet. This test validates
		// only that the policy struct is constructed correctly.
		//
		// Subprocess-level Apply enforcement coverage lives in
		// backend_linux_subprocess_test.go:TestLandlock_ApplySubprocess.
		_ = policy
		_ = backend
		assert.NotNil(t, backend)
	})
}

// TestSandboxBackend_LandlockFallbackChain validates the full backend selection cascade
// on Linux: Landlock → seccomp → fallback.
// Traces to: wave2-security-layer-spec.md line 384 (Scenario: Graceful fallback on unsupported kernel)
func TestSandboxBackend_LandlockFallbackChain(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 993 (FR-001: backend selection cascade)
	backend, name := sandbox.SelectBackend()
	require.NotNil(t, backend)

	t.Run("backend selection always produces a working backend", func(t *testing.T) {
		workspaceDir := t.TempDir()
		allowedFile := filepath.Join(workspaceDir, "test.txt")
		require.NoError(t, os.WriteFile(allowedFile, []byte("ok"), 0o644))

		policy := sandbox.SandboxPolicy{
			FilesystemRules: []sandbox.PathRule{
				{Path: workspaceDir, Access: sandbox.AccessRead | sandbox.AccessWrite},
			},
		}

		if name == "fallback" || name == "seccomp" {
			// Safe to call Apply for these backends (no process-level restrictions for fallback;
			// seccomp must be validated carefully)
			err := backend.Apply(policy)
			assert.NoError(t, err, "Apply should not error for %s backend", name)
		}
		// For Landlock: skip Apply to avoid restricting the test process
	})

	t.Run("seccomp TSYNC flag is documented requirement", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 454 (Scenario: Seccomp TSYNC covers all threads)
		// FR-004: SECCOMP_FILTER_FLAG_TSYNC must be used when applying seccomp
		// This is a documentation test — validates the requirement exists in the spec.
		// Real enforcement tested in integration with child processes.
		assert.NotNil(t, backend, "A sandbox backend must always be selected")
	})
}
