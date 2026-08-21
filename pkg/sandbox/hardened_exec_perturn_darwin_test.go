//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file proves spec unified-file-access-and-mounts FR-4.0/FR-4.2 against
// REAL children, following the pattern in seatbelt_deny_darwin_test.go: build
// a policy, run a real process under sandbox-exec, assert what the KERNEL did
// — never assertions against rendered profile text alone. Text assertions
// about a profile are necessary but not sufficient: they cannot tell you
// whether the seam that carries a per-turn policy to the spawn actually
// exists, only that renderSeatbeltProfile behaves correctly in isolation.

// installBootBackend installs a SeatbeltBackend with bootPolicy as the
// process-global (boot) profile and registers it as CurrentSeatbeltBackend,
// mirroring what the gateway does once at startup. Cleans up afterward so
// tests do not leak backend state into each other.
func installBootBackend(t *testing.T, bootPolicy SandboxPolicy) *SeatbeltBackend {
	t.Helper()
	backend := NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}
	require.NoError(t, backend.Apply(bootPolicy))
	currentSeatbeltBackend.Store(backend)
	t.Cleanup(func() { currentSeatbeltBackend.Store(nil) })
	return backend
}

func writeScript(path string) []string {
	return []string{"/bin/bash", "-c", "echo written > " + path}
}

// TestHardenedExec_PerTurnPolicy_WriteConfinedToWorkDir is the FR-4.0
// baseline: a child spawned with a per-turn KernelPolicy can write inside the
// granted work dir and cannot write outside it, executed through the actual
// production seam (Run -> applyPlatformHardening -> Limits.KernelPolicy),
// not by calling ApplyToCmd directly.
func TestHardenedExec_PerTurnPolicy_WriteConfinedToWorkDir(t *testing.T) {
	// Boot policy grants a directory the per-turn policy does NOT — if the
	// seam were broken and the child fell back to the boot profile, writes
	// under the per-turn work dir would still succeed by accident (both are
	// under t.TempDir() only coincidentally), so keep them disjoint and
	// unrelated on purpose.
	bootOnlyDir := t.TempDir()
	installBootBackend(t, SandboxPolicy{
		FilesystemRules: []PathRule{{Path: bootOnlyDir, Access: AccessRead | AccessWrite | AccessExecute}},
	})

	turnDir := t.TempDir()
	turnPolicy := SandboxPolicy{
		FilesystemRules: []PathRule{{Path: turnDir, Access: AccessRead | AccessWrite | AccessExecute}},
	}
	lim := Limits{WorkspaceDir: turnDir, KernelPolicy: &turnPolicy}

	inside := filepath.Join(turnDir, "ok.txt")
	res, err := Run(context.Background(), writeScript(inside), nil, lim)
	require.NoError(t, err, "spawn itself must not error")
	assert.Equal(t, 0, res.ExitCode, "write inside the per-turn work dir must succeed; stderr=%s", res.Stderr)
	assert.FileExists(t, inside)

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "leak.txt")
	res2, err := Run(context.Background(), writeScript(outside), nil, lim)
	require.NoError(t, err, "spawn itself must not error even though the child's write fails")
	assert.NotEqual(t, 0, res2.ExitCode, "write outside every grant must be denied by the kernel")
	assert.NoFileExists(t, outside)
}

// TestHardenedExec_PerTurnPolicy_OverridesBootProfile is THE seam-carrying
// test the task calls out explicitly: without it, every other test in this
// file could pass against a completely broken implementation that silently
// keeps wrapping every child in the boot-global profile.
//
// The boot profile grants bootDir. The per-turn policy grants a DIFFERENT
// directory and does NOT grant bootDir. If the per-turn policy actually
// governs the child (the correct behaviour), a write into bootDir must be
// DENIED even though the boot profile — which is still installed and active
// for any OTHER caller that does not supply a per-turn policy — would have
// allowed it. A write into bootDir succeeding here would mean the seam is
// decorative: Limits.KernelPolicy is accepted but never actually reaches the
// spawn.
func TestHardenedExec_PerTurnPolicy_OverridesBootProfile(t *testing.T) {
	bootDir := t.TempDir()
	installBootBackend(t, SandboxPolicy{
		FilesystemRules: []PathRule{{Path: bootDir, Access: AccessRead | AccessWrite | AccessExecute}},
	})

	// Control: confirm the boot profile really does grant bootDir, by
	// spawning WITHOUT a per-turn policy (Limits{} — the pre-existing
	// fallback path). Without this passing, the test below would prove
	// nothing: a denied write could just mean nobody can write anywhere.
	controlTarget := filepath.Join(bootDir, "control.txt")
	controlRes, err := Run(context.Background(), writeScript(controlTarget), nil, Limits{WorkspaceDir: bootDir})
	require.NoError(t, err)
	require.Equal(t, 0, controlRes.ExitCode, "control: the boot profile must itself permit writing bootDir; stderr=%s", controlRes.Stderr)
	require.FileExists(t, controlTarget)

	// Per-turn policy grants a DIFFERENT directory only.
	turnDir := t.TempDir()
	turnPolicy := SandboxPolicy{
		FilesystemRules: []PathRule{{Path: turnDir, Access: AccessRead | AccessWrite | AccessExecute}},
	}
	lim := Limits{WorkspaceDir: turnDir, KernelPolicy: &turnPolicy}

	// The critical assertion: writing into bootDir — permitted by the BOOT
	// profile, not by the per-turn one — must now be DENIED.
	leaked := filepath.Join(bootDir, "should-not-be-writable-under-turn-policy.txt")
	res, err := Run(context.Background(), writeScript(leaked), nil, lim)
	require.NoError(t, err)
	assert.NotEqual(t, 0, res.ExitCode,
		"a per-turn policy that does not grant bootDir must deny the write — "+
			"success here means the child silently fell back to the boot-global profile")
	assert.NoFileExists(t, leaked)

	// And the per-turn policy's OWN grant must still work in the same spawn.
	ownWrite := filepath.Join(turnDir, "own.txt")
	res2, err := Run(context.Background(), writeScript(ownWrite), nil, lim)
	require.NoError(t, err)
	assert.Equal(t, 0, res2.ExitCode, "the per-turn policy's own grant must work; stderr=%s", res2.Stderr)
	assert.FileExists(t, ownWrite)
}

// TestHardenedExec_TwoDifferentPerTurnPolicies_TwoGenuinelyDifferentChildren
// is the two-children variant explicitly required by the task: spawn TWO
// children under TWO different per-turn policies and prove each is confined
// to ITS OWN grant and denied the other's, in both directions. This is
// stronger than the single-child override test above because it rules out a
// bug where the seam works only for the "first" or only for the "second"
// KernelPolicy value passed since boot (e.g. a cache or backend field that
// silently accepts the first non-nil KernelPolicy and then ignores updates).
func TestHardenedExec_TwoDifferentPerTurnPolicies_TwoGenuinelyDifferentChildren(t *testing.T) {
	bootDir := t.TempDir()
	installBootBackend(t, SandboxPolicy{
		FilesystemRules: []PathRule{{Path: bootDir, Access: AccessRead | AccessWrite | AccessExecute}},
	})

	dirA := t.TempDir()
	policyA := SandboxPolicy{FilesystemRules: []PathRule{{Path: dirA, Access: AccessRead | AccessWrite | AccessExecute}}}
	limA := Limits{WorkspaceDir: dirA, KernelPolicy: &policyA}

	dirB := t.TempDir()
	policyB := SandboxPolicy{FilesystemRules: []PathRule{{Path: dirB, Access: AccessRead | AccessWrite | AccessExecute}}}
	limB := Limits{WorkspaceDir: dirB, KernelPolicy: &policyB}

	// Child A: can write its own dir, cannot write B's.
	okA := filepath.Join(dirA, "a-own.txt")
	resA1, err := Run(context.Background(), writeScript(okA), nil, limA)
	require.NoError(t, err)
	assert.Equal(t, 0, resA1.ExitCode, "child A must write its own granted dir; stderr=%s", resA1.Stderr)
	assert.FileExists(t, okA)

	leakA := filepath.Join(dirB, "a-into-b.txt")
	resA2, err := Run(context.Background(), writeScript(leakA), nil, limA)
	require.NoError(t, err)
	assert.NotEqual(t, 0, resA2.ExitCode, "child A must NOT be able to write dir B")
	assert.NoFileExists(t, leakA)

	// Child B: can write its own dir, cannot write A's.
	okB := filepath.Join(dirB, "b-own.txt")
	resB1, err := Run(context.Background(), writeScript(okB), nil, limB)
	require.NoError(t, err)
	assert.Equal(t, 0, resB1.ExitCode, "child B must write its own granted dir; stderr=%s", resB1.Stderr)
	assert.FileExists(t, okB)

	leakB := filepath.Join(dirA, "b-into-a.txt")
	resB2, err := Run(context.Background(), writeScript(leakB), nil, limB)
	require.NoError(t, err)
	assert.NotEqual(t, 0, resB2.ExitCode, "child B must NOT be able to write dir A")
	assert.NoFileExists(t, leakB)
}

// TestHardenedExec_BrokenPerTurnPolicy_AbortsSpawnRatherThanRunUnconfined is
// FR-4.2: a render/cache-fill failure must abort the spawn entirely — Run
// must return a non-nil error and the child must never have started, rather
// than falling through to an unconfined process.
func TestHardenedExec_BrokenPerTurnPolicy_AbortsSpawnRatherThanRunUnconfined(t *testing.T) {
	bootDir := t.TempDir()
	installBootBackend(t, SandboxPolicy{
		FilesystemRules: []PathRule{{Path: bootDir, Access: AccessRead | AccessWrite | AccessExecute}},
	})

	turnDir := t.TempDir()
	// Access == 0 is rejected by renderSeatbeltProfile ("has no access
	// flags") — a deliberately invalid per-turn policy. Non-empty
	// FilesystemRules means this goes through the render/cache path in
	// ApplyToCmd rather than the "empty policy -> reuse boot profile"
	// fallback, which is the branch that matters here: an accidental
	// fallback to the (perfectly valid) boot profile would make this test
	// pass for the wrong reason.
	broken := SandboxPolicy{
		FilesystemRules: []PathRule{{Path: turnDir, Access: 0}},
	}
	lim := Limits{WorkspaceDir: turnDir, KernelPolicy: &broken}

	marker := filepath.Join(turnDir, "must-not-exist.txt")
	res, err := Run(context.Background(), writeScript(marker), nil, lim)
	require.Error(t, err, "a broken per-turn policy must abort the spawn, not silently run an unconfined child")
	assert.Equal(t, Result{}, res, "no Result should be returned on an aborted spawn")
	assert.NoFileExists(t, marker, "the child must never have started — an unconfined run would have created this file")
}

// TestHardenedExec_NilKernelPolicy_FallsBackToBootProfile pins the documented
// fallback contract (Limits.KernelPolicy's doc comment): supplying no
// per-turn policy must reproduce the exact pre-FR-4.0 behaviour, not deny
// everything and not silently drop the boot profile's protections.
func TestHardenedExec_NilKernelPolicy_FallsBackToBootProfile(t *testing.T) {
	bootDir := t.TempDir()
	installBootBackend(t, SandboxPolicy{
		FilesystemRules: []PathRule{{Path: bootDir, Access: AccessRead | AccessWrite | AccessExecute}},
	})

	// No KernelPolicy set at all.
	lim := Limits{WorkspaceDir: bootDir}

	inside := filepath.Join(bootDir, "boot-ok.txt")
	res, err := Run(context.Background(), writeScript(inside), nil, lim)
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode, "with no per-turn policy the boot profile's own grant must still work; stderr=%s", res.Stderr)
	assert.FileExists(t, inside)

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "boot-deny.txt")
	res2, err := Run(context.Background(), writeScript(outside), nil, lim)
	require.NoError(t, err)
	assert.NotEqual(t, 0, res2.ExitCode, "with no per-turn policy the boot profile must still deny what it never granted")
	assert.NoFileExists(t, outside)
}
