// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — ADR-046 P1 ResolvePath unit tests (spec tests 2-7).

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// confinedPolicy builds a minimal fspolicy.FSPolicy rooted at workDir's
// realpath, with FSScopeConfined and no carve-outs — the direct-construction
// shape used by these resolver-level unit tests (as opposed to the
// integration tests, which go through the real fspolicy.EffectiveFSPolicy
// via the generic tools' own Execute path).
func confinedPolicy(t *testing.T, workDir string) fspolicy.FSPolicy {
	t.Helper()
	real, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("resolve workDir: %v", err)
	}
	return fspolicy.FSPolicy{WorkDir: real, Scope: fspolicy.FSScopeConfined}
}

func unrestrictedPolicy(t *testing.T, workDir string) fspolicy.FSPolicy {
	t.Helper()
	real, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("resolve workDir: %v", err)
	}
	return fspolicy.FSPolicy{WorkDir: real, Scope: fspolicy.FSScopeUnrestricted}
}

// TestResolvePath_RelativeRootsAtWorkingDir — spec test 2 (FR-004): a
// relative path resolves rooted at policy.WorkDir, and the returned handle
// actually reads the file that lives there.
func TestResolvePath_RelativeRootsAtWorkingDir(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	policy := confinedPolicy(t, workDir)

	handle, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, "a.txt")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	defer handle.Close()

	data, err := handle.ReadFile()
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", data, "hello")
	}
}

// TestResolvePath_IOThroughOsRoot_NoTOCTOU — spec test 3 (FR-006). A
// resolved handle's target symlink is swapped from an in-scope file to an
// out-of-scope secret AFTER ResolvePath has already resolved it (a
// deterministic simulation of the "goroutine swaps a symlink component
// between resolve and I/O" race the spec describes — a real concurrent race
// would be non-deterministic in CI, so this pins the same property with a
// sequential swap instead). The os.Root-backed handle must re-resolve and
// refuse the escape AT I/O TIME rather than trusting the earlier check —
// proving I/O happens through the root handle, not a pre-checked string.
func TestResolvePath_IOThroughOsRoot_NoTOCTOU(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink races are POSIX-specific")
	}
	workDir := t.TempDir()
	outside := t.TempDir()
	secretFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("top secret"), 0o600); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	insideTarget := filepath.Join(workDir, "innocuous")
	if err := os.WriteFile(insideTarget, []byte("innocuous"), 0o644); err != nil {
		t.Fatalf("seed inside target: %v", err)
	}

	link := filepath.Join(workDir, "toctou_link")
	if err := os.Symlink(insideTarget, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	policy := confinedPolicy(t, workDir)

	// Resolve while the link still points inside — this must succeed.
	handle, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, "toctou_link")
	if err != nil {
		t.Fatalf("initial ResolvePath (still inside) should succeed: %v", err)
	}
	defer handle.Close()

	// Swap the link to point OUTSIDE before the actual I/O runs.
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove link: %v", err)
	}
	if err := os.Symlink(secretFile, link); err != nil {
		t.Fatalf("re-symlink outside: %v", err)
	}

	// The os.Root-backed handle re-resolves "toctou_link" at I/O time — it
	// must refuse the now-escaping symlink rather than serving the secret
	// content it would have if it trusted the earlier check.
	data, err := handle.ReadFile()
	if err == nil {
		t.Fatalf("expected the swapped symlink to be refused, got content: %q", data)
	}
	if string(data) == "top secret" {
		t.Fatalf("TOCTOU BREACH: read the swapped-in secret file: %q", data)
	}
}

// TestResolvePath_AbsoluteGatedByScope — spec test 4 (FR-005/FR-016): an
// absolute path outside WorkDir is refused under Confined and permitted
// (minus carve-outs) under Unrestricted.
func TestResolvePath_AbsoluteGatedByScope(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "x.txt")
	if err := os.WriteFile(outsideFile, []byte("outside content"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	confined := confinedPolicy(t, workDir)
	_, err := ResolvePath(context.Background(), confined, "read_file", "", FSOpRead, outsideFile)
	if err == nil {
		t.Fatalf("expected confined scope to refuse an absolute outside path")
	}
	if !errors.Is(err, ErrOutsideScope) {
		t.Errorf("expected ErrOutsideScope, got: %v", err)
	}

	unrestricted := unrestrictedPolicy(t, workDir)
	handle, err := ResolvePath(context.Background(), unrestricted, "read_file", "", FSOpRead, outsideFile)
	if err != nil {
		t.Fatalf("expected unrestricted scope to permit an absolute outside path: %v", err)
	}
	defer handle.Close()
	data, err := handle.ReadFile()
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "outside content" {
		t.Errorf("content = %q, want %q", data, "outside content")
	}
}

// TestResolvePath_SymlinkAnchorsOnRealpath — spec test 5 (FR-006): a
// symlink INSIDE the confined WorkDir pointing OUTSIDE it is refused —
// confinement anchors on the realpath, not the lexical in-workdir path.
func TestResolvePath_SymlinkAnchorsOnRealpath(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	secretFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("top secret"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	link := filepath.Join(workDir, "escape_link")
	if err := os.Symlink(secretFile, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	policy := confinedPolicy(t, workDir)
	_, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, "escape_link")
	if err == nil {
		t.Fatalf("expected symlink escape to be refused")
	}
	if !errors.Is(err, ErrOutsideScope) {
		t.Errorf("expected ErrOutsideScope, got: %v", err)
	}
}

// TestResolvePath_NullByteRejected — spec test 6 (dataset row 11): an
// embedded NUL byte is rejected with ErrPathInvalid before any I/O.
func TestResolvePath_NullByteRejected(t *testing.T) {
	workDir := t.TempDir()
	policy := confinedPolicy(t, workDir)

	_, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, "work/x\x00/../master.key")
	if err == nil {
		t.Fatalf("expected embedded NUL byte to be rejected")
	}
	if !errors.Is(err, ErrPathInvalid) {
		t.Errorf("expected ErrPathInvalid, got: %v", err)
	}
}

// TestCarveOut_AnchoredOnOmnipusHome_NotWorkingDir — spec test 7 (FR-017,
// BLOCK #5): the carve-out matcher fires even under FSScopeUnrestricted, and
// is anchored on the boot-known $OMNIPUS_HOME — never derived from the
// (re-rootable) working directory — so a teammate on a shared workspace
// still cannot reach another agent's home or the master key.
func TestCarveOut_AnchoredOnOmnipusHome_NotWorkingDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	agentsDir := filepath.Join(home, "agents")
	agentA := filepath.Join(agentsDir, "agent-A")
	agentB := filepath.Join(agentsDir, "agent-B")
	if err := os.MkdirAll(agentA, 0o755); err != nil {
		t.Fatalf("mkdir agent-A: %v", err)
	}
	if err := os.MkdirAll(agentB, 0o755); err != nil {
		t.Fatalf("mkdir agent-B: %v", err)
	}
	soul := filepath.Join(agentB, "SOUL.md")
	if err := os.WriteFile(soul, []byte("agent B secret"), 0o600); err != nil {
		t.Fatalf("seed agent-B SOUL.md: %v", err)
	}
	masterKey := filepath.Join(home, "master.key")
	if err := os.WriteFile(masterKey, []byte("key-material"), 0o600); err != nil {
		t.Fatalf("seed master.key: %v", err)
	}

	// agent-A resolved as an Unrestricted (allow-equivalent) agent, sharing
	// the default workspace's re-rooted "work" concept is not needed to
	// prove the point here — even the agent's own confined home resolution
	// under Unrestricted must still hit the carve-out for agent-B/master.key.
	policy, err := fspolicy.EffectiveFSPolicy(
		context.Background(), agentA, "", false, /* restrict=false -> Unrestricted */
		config.OmnipusHomeDir(), "agent-A", "",
	)
	if err != nil {
		t.Fatalf("EffectiveFSPolicy: %v", err)
	}
	if policy.Scope != fspolicy.FSScopeUnrestricted {
		t.Fatalf("expected FSScopeUnrestricted, got %v", policy.Scope)
	}

	_, err = ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, soul)
	if err == nil {
		t.Fatalf("expected carve-out to refuse agent-B's home even under Unrestricted")
	}
	if !errors.Is(err, ErrCarveOut) {
		t.Errorf("expected ErrCarveOut for agent-B's SOUL.md, got: %v", err)
	}

	_, err = ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, masterKey)
	if err == nil {
		t.Fatalf("expected carve-out to refuse master.key even under Unrestricted")
	}
	if !errors.Is(err, ErrCarveOut) {
		t.Errorf("expected ErrCarveOut for master.key, got: %v", err)
	}
}
