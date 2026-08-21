// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestSandboxBackend_Fallback validates the FallbackBackend works on all platforms
// (including non-Linux where Landlock/seccomp are unavailable).
// Traces to: wave2-security-layer-spec.md line 811 (TestSandboxBackend_Fallback)
// BDD: Scenario: Graceful fallback on unsupported kernel (spec line 384) +
// Scenario: Seccomp is no-op on non-Linux (spec line 429)
func TestSandboxBackend_Fallback(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 384 (Scenario: Graceful fallback)
	backend := sandbox.NewFallbackBackend()
	require.NotNil(t, backend)

	t.Run("fallback backend name is 'fallback'", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 992 (FR-033: FallbackSandboxBackend)
		assert.Equal(t, "fallback", backend.Name(),
			"FallbackBackend must identify as 'fallback'")
	})

	t.Run("fallback backend is always available", func(t *testing.T) {
		assert.True(t, backend.Available(),
			"FallbackBackend.Available must return true on any platform")
	})

	t.Run("Apply with filesystem rules does not error", func(t *testing.T) {
		workspaceDir := t.TempDir()
		policy := sandbox.SandboxPolicy{
			FilesystemRules: []sandbox.PathRule{
				{Path: workspaceDir, Access: sandbox.AccessRead | sandbox.AccessWrite},
			},
		}
		err := backend.Apply(policy)
		assert.NoError(t, err,
			"FallbackBackend.Apply must not return error")
	})

	t.Run("Apply with empty policy does not error", func(t *testing.T) {
		err := backend.Apply(sandbox.SandboxPolicy{})
		assert.NoError(t, err)
	})

	t.Run("CheckPath allows path inside workspace", func(t *testing.T) {
		workspaceDir := t.TempDir()
		backend2 := sandbox.NewFallbackBackend()
		_ = backend2.Apply(sandbox.SandboxPolicy{
			FilesystemRules: []sandbox.PathRule{
				{Path: workspaceDir, Access: sandbox.AccessRead | sandbox.AccessWrite},
			},
		})
		allowedFile := filepath.Join(workspaceDir, "file.txt")
		require.NoError(t, os.WriteFile(allowedFile, []byte("ok"), 0o644))
		err := backend2.CheckPath(allowedFile)
		assert.NoError(t, err, "path inside workspace should be allowed")
	})

	t.Run("CheckPath blocks path outside workspace", func(t *testing.T) {
		workspaceDir := t.TempDir()
		backend2 := sandbox.NewFallbackBackend()
		_ = backend2.Apply(sandbox.SandboxPolicy{
			FilesystemRules: []sandbox.PathRule{
				{Path: workspaceDir, Access: sandbox.AccessRead},
			},
		})

		// Try to access /etc/passwd (outside workspace)
		if _, err := os.Stat("/etc/passwd"); err == nil {
			err = backend2.CheckPath("/etc/passwd")
			assert.Error(t, err,
				"path outside workspace (/etc/passwd) should be blocked by FallbackBackend")
			assert.Contains(t, err.Error(), "outside allowed paths")
		}
	})
}

// TestSandboxBackend_SelectBackend validates that SelectBackend returns a non-nil,
// working backend for the current platform.
// Traces to: wave2-security-layer-spec.md line 992 (FR-001: detect kernel version and select backend)
func TestSandboxBackend_SelectBackend(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 992 (FR-001: backend selection)
	backend, name := sandbox.SelectBackend()
	require.NotNil(t, backend, "SelectBackend must never return nil")

	t.Run("selected backend has a non-empty name", func(t *testing.T) {
		assert.NotEmpty(t, name, "SelectBackend must return a non-empty name")
		assert.Equal(t, backend.Name(), name,
			"returned name should match backend.Name()")
	})

	t.Run("selected backend is available", func(t *testing.T) {
		assert.True(t, backend.Available(),
			"SelectBackend must always return an available backend")
	})

	t.Run("Apply with minimal policy succeeds", func(t *testing.T) {
		// Landlock's landlock_restrict_self is a per-process one-way ratchet —
		// applying it from a shared test process permanently restricts every
		// subsequent test in the same binary. Apply is therefore skipped for
		// Landlock backends in unit tests.
		//
		// Subprocess-level Apply coverage lives in
		// backend_linux_subprocess_test.go:TestLandlock_ApplySubprocess, which
		// forks the test binary, calls Apply inside the child, and verifies
		// that /etc/passwd is blocked before the child exits.
		if backend.Name() != "fallback" && backend.Name() != "seccomp" {
			t.Skipf("skipping Apply for %q backend: would irreversibly sandbox the test process", backend.Name())
		}
		workspaceDir := t.TempDir()
		policy := sandbox.SandboxPolicy{
			FilesystemRules: []sandbox.PathRule{
				{Path: workspaceDir, Access: sandbox.AccessRead | sandbox.AccessWrite},
			},
		}
		err := backend.Apply(policy)
		assert.NoError(t, err,
			"SelectBackend().Apply must not hard-fail — graceful degradation required")
	})
}

// TestLandlockDetector_ABIVersion validates the Landlock ABI version detection logic.
// Tests the LandlockBackend's Available() and ABIVersion() reporting.
// Traces to: wave2-security-layer-spec.md line 801 (TestLandlockDetector_ABIVersion)
// BDD: Scenario Outline: Landlock ABI version detection (spec line 395)
func TestLandlockDetector_ABIVersion(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 882 (Dataset: Landlock ABI Versions rows 1–4)
	// This test validates the detection logic on the current platform.
	// The FallbackBackend always reports no kernel sandboxing.

	t.Run("FallbackBackend always reports Name=fallback", func(t *testing.T) {
		fb := sandbox.NewFallbackBackend()
		assert.Equal(t, "fallback", fb.Name())
	})

	t.Run("SelectBackend backend name reflects kernel capabilities", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 993 (FR-001: Landlock ABI detection)
		backend, name := sandbox.SelectBackend()
		assert.NotEmpty(t, name)
		assert.NotNil(t, backend)
		// On Linux with Landlock, name will be "landlock-vN"
		// On Linux without Landlock, name will be "seccomp" or "fallback"
		// On macOS with sandbox-exec present, name will be "seatbelt"
		//   (ADR-052 Phase-3 AC-6; before that darwin always reported
		//   "fallback", which is why this list previously had no entry for it)
		// On any other platform, or when the preferred backend is unavailable,
		// name degrades to "fallback" (HC #4).
		validNames := map[string]bool{
			"fallback": true,
			"seatbelt": true,
		}
		// Landlock names are "landlock-v1", "landlock-v2", "landlock-v3"
		isLandlock := len(name) > 9 && name[:9] == "landlock-"
		isValid := validNames[name] || isLandlock
		assert.True(t, isValid,
			"backend name %q should be 'fallback', 'seatbelt', or 'landlock-vN'", name)
	})
}
