//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// applySandbox's macOS branch had NO test coverage at all, which is how it
// shipped with two defects at once: it originally returned before ever calling
// Apply (so every child ran unconfined while the log named "seatbelt"), and it
// then applied a blocking profile under mode=permissive (so the one mode that
// promises not to block, blocked).
//
// Both are regressions that would boot green. These tests exist so they cannot
// return silently.

func darwinSandboxCfg(t *testing.T, mode string) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Sandbox.Mode = config.SandboxMode(mode)
	return cfg
}

// TestApplySandbox_Darwin_PermissiveDoesNotInstallProfile is the regression
// test for the worst failure mode on this platform.
//
// Seatbelt has no audit-only mode. Installing the profile under `permissive`
// gives an operator running the documented "see what would break first" step
// full hard enforcement — with no banner, no audit_only flag and nothing in the
// status surface to reveal it.
func TestApplySandbox_Darwin_PermissiveDoesNotInstallProfile(t *testing.T) {
	backend := sandbox.NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}

	tmp, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("temp stderr: %v", err)
	}
	defer func() { _ = tmp.Close() }()

	result, err := applySandbox(SandboxApplyOptions{
		Cfg:      darwinSandboxCfg(t, "permissive"),
		HomePath: t.TempDir(),
		Backend:  backend,
		GetEnv:   func(string) string { return "" },
		Stderr:   tmp,
	})
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}

	if backend.PolicyApplied() {
		t.Error("permissive must NOT install a kernel profile: Seatbelt cannot audit without enforcing")
	}
	if !result.ApplyState.AuditOnly {
		t.Error("ApplyState.AuditOnly must be true so the status surface does not claim enforcement")
	}
	if result.NagReason != "permissive" {
		t.Errorf("NagReason = %q, want \"permissive\" so the banner actually fires", result.NagReason)
	}
	notes := strings.Join(result.ApplyState.ExtraNotes, " ")
	if !strings.Contains(strings.ToLower(notes), "audit-only") {
		t.Errorf("operator-facing notes must explain the degradation, got %q", notes)
	}
}

// TestApplySandbox_Darwin_EnforceInstallsProfile is the positive control. Without
// it, the permissive test above would also pass if applySandbox never installed
// a profile in ANY mode — which is precisely the earlier bug.
func TestApplySandbox_Darwin_EnforceInstallsProfile(t *testing.T) {
	backend := sandbox.NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}

	tmp, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("temp stderr: %v", err)
	}
	defer func() { _ = tmp.Close() }()

	result, err := applySandbox(SandboxApplyOptions{
		Cfg:      darwinSandboxCfg(t, "enforce"),
		HomePath: t.TempDir(),
		Backend:  backend,
		GetEnv:   func(string) string { return "" },
		Stderr:   tmp,
	})
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}

	if !backend.PolicyApplied() {
		t.Fatal("enforce must install a kernel profile; Apply was never called")
	}
	notes := strings.Join(result.ApplyState.ExtraNotes, " ")
	if strings.Contains(notes, "does not support Landlock") {
		t.Errorf("macOS must not report the Linux degradation note, got %q", notes)
	}
	if len(result.Policy.FilesystemRules) == 0 {
		t.Error("a policy must have been computed and retained for the status endpoint")
	}
}

// TestApplySandbox_Darwin_SeededExecPathsReachThePolicy closes the config →
// policy seam. Every other exec-path test hand-builds the list; nothing
// asserted that applySandbox actually passes cfg.Sandbox.AllowedExecPaths to
// DefaultPolicy, so dropping that argument would leave all of them green.
func TestApplySandbox_Darwin_SeededExecPathsReachThePolicy(t *testing.T) {
	backend := sandbox.NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}

	// NOT t.TempDir(): on macOS that lives inside $TMPDIR, which DefaultPolicy
	// grants RWX, so the overlap guard would correctly drop the exec grant and
	// this test would fail for a reason unrelated to the seam it covers. The
	// policy never stats the path, so a synthetic one is fine — and the
	// interaction itself is pinned by the sibling test below.
	const toolDir = "/omnipus-test-toolchain"
	cfg := darwinSandboxCfg(t, "enforce")
	cfg.Sandbox.AllowedExecPaths = []string{toolDir}
	// allowed_exec_paths is a CONFINED-model mechanism, so this test pins it
	// explicitly rather than inheriting the seeded default. ADR-062 made "open"
	// the default, under which execution is unrestricted and the list is inert
	// BY DESIGN — leaving this on the default turned a correct behaviour change
	// into a red test that says nothing. The open-model half is asserted by the
	// sibling test below, so both behaviours stay covered.
	cfg.Sandbox.FilesystemModel = string(config.FilesystemModelConfined)

	tmp, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("temp stderr: %v", err)
	}
	defer func() { _ = tmp.Close() }()

	result, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  backend,
		GetEnv:   func(string) string { return "" },
		Stderr:   tmp,
	})
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}

	var found bool
	for _, r := range result.Policy.FilesystemRules {
		if r.Path == toolDir {
			found = true
			if r.Access&sandbox.AccessExecute == 0 {
				t.Error("configured exec path must carry the execute bit")
			}
			if r.Access&sandbox.AccessWrite != 0 {
				t.Error("configured exec path must never be writable")
			}
		}
	}
	if !found {
		t.Errorf("cfg.Sandbox.AllowedExecPaths never reached the policy; rules=%v", result.Policy.FilesystemRules)
	}
}

// TestApplySandbox_Darwin_OpenModelIgnoresExecPaths is the other half of the
// test above, and it pins a deliberate behaviour change rather than a bug.
//
// Under the open model execution is unrestricted, so allowed_exec_paths has
// nothing left to grant. The config key stays VALID and simply stops
// contributing: an operator who carefully listed their toolchain directories is
// not punished with an error for having done so, and does not need to unpick
// their config to upgrade. It just stops being load-bearing.
func TestApplySandbox_Darwin_OpenModelIgnoresExecPaths(t *testing.T) {
	backend := sandbox.NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}
	const toolDir = "/omnipus-test-toolchain"
	cfg := darwinSandboxCfg(t, "enforce")
	cfg.Sandbox.AllowedExecPaths = []string{toolDir}
	cfg.Sandbox.FilesystemModel = string(config.FilesystemModelOpen)

	tmp, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("temp stderr: %v", err)
	}
	defer func() { _ = tmp.Close() }()

	result, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  backend,
		GetEnv:   func(string) string { return "" },
		Stderr:   tmp,
	})
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}

	for _, r := range result.Policy.FilesystemRules {
		if r.Path == toolDir {
			t.Errorf("open model emitted an exec-path rule for %q; execution is already "+
				"unrestricted, so the rule is redundant and its presence means the model was not applied", toolDir)
		}
	}
	if !result.Policy.ExecOpen {
		t.Error("open model must report ExecOpen; without it agents still cannot run installed toolchains, " +
			"which is the entire problem ADR-062 exists to fix")
	}
}

// TestApplySandbox_Darwin_ExecPathInsideTempDirIsDropped pins a real
// operational gotcha discovered while writing these tests: on macOS the
// per-user $TMPDIR is granted RWX, so an operator who points
// allowed_exec_paths at a directory under it gets NO exec grant.
//
// That is the overlap guard working as designed — a writable AND executable
// directory is exactly what it exists to prevent — but the behaviour is
// surprising enough that it deserves to be asserted rather than rediscovered.
func TestApplySandbox_Darwin_ExecPathInsideTempDirIsDropped(t *testing.T) {
	backend := sandbox.NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}

	insideTemp := t.TempDir() // lives under $TMPDIR on macOS
	cfg := darwinSandboxCfg(t, "enforce")
	cfg.Sandbox.AllowedExecPaths = []string{insideTemp}

	tmp, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("temp stderr: %v", err)
	}
	defer func() { _ = tmp.Close() }()

	result, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  backend,
		GetEnv:   func(string) string { return "" },
		Stderr:   tmp,
	})
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}

	for _, r := range result.Policy.FilesystemRules {
		if r.Path == insideTemp && r.Access&sandbox.AccessExecute != 0 && r.Access&sandbox.AccessWrite != 0 {
			t.Fatalf("a directory under $TMPDIR became writable AND executable: %+v", r)
		}
	}
}
