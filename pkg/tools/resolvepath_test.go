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
	"strings"
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
	realDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("resolve workDir: %v", err)
	}
	return fspolicy.FSPolicy{WorkDir: realDir, Scope: fspolicy.FSScopeConfined}
}

func unrestrictedPolicy(t *testing.T, workDir string) fspolicy.FSPolicy {
	t.Helper()
	realDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("resolve workDir: %v", err)
	}
	return fspolicy.FSPolicy{WorkDir: realDir, Scope: fspolicy.FSScopeUnrestricted}
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
	if err = os.Remove(link); err != nil {
		t.Fatalf("remove link: %v", err)
	}
	if err = os.Symlink(secretFile, link); err != nil {
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

// TestResolvePath_AbsoluteGatedByScope — spec test 4 (FR-005/FR-016), UPDATED
// for ADR-063 / spec unified-file-access-and-mounts FR-2.2/FR-2.5: an
// absolute FSOpRead path outside WorkDir is now permitted (minus carve-outs)
// under BOTH Confined and Unrestricted — reads are open regardless of Scope.
// The name is kept (it is the well-known "spec test 4" for FR-005/FR-016;
// the underlying anchoring-on-realpath property those FRs describe is still
// exercised, just no longer split by Scope for reads) but the body now
// documents the FR-2.2 change directly: this used to be THE test proving
// Confined denies an absolute outside read. It no longer does, by design.
// The write side of the split (still Scope-independent, but confined to
// WorkDir/a mount either way) is exercised separately by
// TestResolvePath_FSOpWrite_ConfinedToWorkDirOrMount_RegardlessOfScope below.
func TestResolvePath_AbsoluteGatedByScope(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "x.txt")
	if err := os.WriteFile(outsideFile, []byte("outside content"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	confined := confinedPolicy(t, workDir)
	handleConfined, err := ResolvePath(context.Background(), confined, "read_file", "", FSOpRead, outsideFile)
	if err != nil {
		t.Fatalf("FR-2.2: expected Confined scope to permit an absolute outside READ, got: %v", err)
	}
	defer handleConfined.Close()
	dataConfined, err := handleConfined.ReadFile()
	if err != nil {
		t.Fatalf("ReadFile (confined): %v", err)
	}
	if string(dataConfined) != "outside content" {
		t.Errorf("content = %q, want %q", dataConfined, "outside content")
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

// TestResolvePath_FSOpWrite_ConfinedToWorkDirOrMount_RegardlessOfScope —
// ADR-063 / spec unified-file-access-and-mounts FR-2.2/FR-2.5: unlike reads,
// an FSOpWrite to the SAME absolute outside path is refused under BOTH
// Confined and Unrestricted scope — writes are confined to WorkDir or a
// mount (policy.AllowedRoots) regardless of Scope, which is the new,
// narrower thing "restrict to workspace" governs (FR-2.5).
func TestResolvePath_FSOpWrite_ConfinedToWorkDirOrMount_RegardlessOfScope(t *testing.T) {
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "x.txt")

	for _, tc := range []struct {
		name   string
		policy func(t *testing.T, workDir string) fspolicy.FSPolicy
	}{
		{"confined", confinedPolicy},
		{"unrestricted", unrestrictedPolicy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			_, err := ResolvePath(context.Background(), tc.policy(t, workDir), "write_file", "", FSOpWrite, outsideFile)
			if err == nil {
				t.Fatalf("expected FSOpWrite outside WorkDir with no mount to be refused under %s scope", tc.name)
			}
			if !errors.Is(err, ErrOutsideScope) {
				t.Errorf("expected ErrOutsideScope, got: %v", err)
			}
		})
	}
}

// TestResolvePath_SymlinkAnchorsOnRealpath_Write — spec test 5 (FR-006),
// NARROWED to FSOpWrite by ADR-063 / spec unified-file-access-and-mounts
// FR-2.2: a symlink INSIDE the confined WorkDir pointing OUTSIDE it is
// refused for a WRITE — confinement still anchors on the realpath, not the
// lexical in-workdir path, for the op that still confines at all. The
// original version of this test asserted the same for FSOpRead; that
// assertion is now FALSE by design (see
// TestResolvePath_SymlinkAnchorsOnRealpath_Read_NowOpen immediately below) —
// reads are open regardless of Scope or of a symlink's target, so realpath
// anchoring for reads now matters only for the carve-out check (FR-017),
// covered by TestCarveOut_AnchoredOnOmnipusHome_NotWorkingDir.
func TestResolvePath_SymlinkAnchorsOnRealpath_Write(t *testing.T) {
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
	_, err := ResolvePath(context.Background(), policy, "write_file", "", FSOpWrite, "escape_link")
	if err == nil {
		t.Fatalf("expected a write through a symlink escape to be refused")
	}
	if !errors.Is(err, ErrOutsideScope) {
		t.Errorf("expected ErrOutsideScope, got: %v", err)
	}
}

// TestResolvePath_SymlinkAnchorsOnRealpath_Read_NowOpen is the read half of
// the same fixture, pinned to the FR-2.2 headline change directly: a read
// through the identical symlink escape now SUCCEEDS, because FSOpRead is
// open anywhere outside the secret set, regardless of Scope. This is the
// exact BDD S-2 / dataset-row-1 shape ("read succeeds while write on the
// same underlying target is denied") applied to a symlink rather than a
// plain absolute path.
func TestResolvePath_SymlinkAnchorsOnRealpath_Read_NowOpen(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	secretFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("top secret"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	link := filepath.Join(workDir, "escape_link_read")
	if err := os.Symlink(secretFile, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	policy := confinedPolicy(t, workDir)
	handle, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, "escape_link_read")
	if err != nil {
		t.Fatalf("FR-2.2: expected a read through a symlink escape to succeed, got: %v", err)
	}
	defer handle.Close()
	data, err := handle.ReadFile()
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "top secret" {
		t.Errorf("content = %q, want %q", data, "top secret")
	}
}

// TestResolvePath_SymlinkedWorkspaceRoot_Confined — regression test for a
// HIGH-severity finding surfaced by the macOS CI matrix job (PR #597,
// `matrix (macos-latest, arm64)`): ResolvePath compared a symlink-resolved
// candidate path (realAbs) against an UNRESOLVED policy.WorkDir, so on
// macOS — where t.TempDir() lives under /var, itself a symlink to
// /private/var — every legitimate in-workspace path was falsely rejected as
// "escaping" the working directory (20+ failures: TestFilesystemTool_*,
// TestEditTool_*, TestReadFile_*, TestSendFileTool_*).
//
// This pod is Linux, so it cannot exercise /var's real symlink. Instead it
// manufactures the identical SHAPE locally: a real directory behind an
// explicit symlink, with policy.WorkDir set to the symlink path and
// deliberately NOT pre-resolved via filepath.EvalSymlinks — unlike this
// file's own confinedPolicy/unrestrictedPolicy helpers (which is exactly why
// this is a fresh fspolicy.FSPolicy{} literal here, not those helpers: using
// them would silently resolve the bug away before ResolvePath ever saw it).
// This reproduces the defect precisely: fspolicy.EffectiveFSPolicy always
// resolves WorkDir in production, but fspolicy.FSPolicy.Validate's own doc
// comment (policy.go ~line 96-99) acknowledges "the direct-construction
// shape several resolver-level unit tests use" as a sanctioned pattern
// ResolvePath must not silently assume away.
func TestResolvePath_SymlinkedWorkspaceRoot_Confined(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are POSIX-specific here")
	}

	realTarget := t.TempDir()
	if err := os.WriteFile(filepath.Join(realTarget, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	parent := t.TempDir()
	symlinkedWorkDir := filepath.Join(parent, "workdir-link")
	if err := os.Symlink(realTarget, symlinkedWorkDir); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	// Deliberately UNRESOLVED WorkDir (contrast with confinedPolicy above).
	policy := fspolicy.FSPolicy{WorkDir: symlinkedWorkDir, Scope: fspolicy.FSScopeConfined}

	// A relative path must resolve and read successfully through the
	// symlinked root.
	handle, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, "a.txt")
	if err != nil {
		t.Fatalf("ResolvePath (relative, symlinked WorkDir) should succeed, got: %v", err)
	}
	data, err := handle.ReadFile()
	handle.Close()
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", data, "hello")
	}

	// An ABSOLUTE path spelled through the UNRESOLVED symlink prefix must
	// also succeed — this is exactly how the failing macOS tests built their
	// "path" argument: filepath.Join(tmpDir, "a.txt") using tmpDir's raw,
	// unresolved spelling (the /var, not /private/var, form).
	absViaSymlink := filepath.Join(symlinkedWorkDir, "a.txt")
	handle2, err := ResolvePath(context.Background(), policy, "read_file", "call-2", FSOpRead, absViaSymlink)
	if err != nil {
		t.Fatalf("ResolvePath (absolute, unresolved-symlink spelling) should succeed, got: %v", err)
	}
	defer handle2.Close()
	data2, err := handle2.ReadFile()
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data2) != "hello" {
		t.Errorf("content = %q, want %q", data2, "hello")
	}

	// Operating on the WORKSPACE ROOT ITSELF (spelled through the symlink,
	// matching TestFilesystemTool_ListDir_Success's "path": tmpDir shape)
	// must also succeed. This is the edge case a naive "just resolve the
	// ancestor and keep the last component literal" fix gets wrong: with no
	// child component to preserve, that approach walks one directory too far
	// up and rejects the root itself as "../workdir-link escapes".
	handle3, err := ResolvePath(context.Background(), policy, "list_directory", "call-3", FSOpList, symlinkedWorkDir)
	if err != nil {
		t.Fatalf("ResolvePath (workspace root itself, unresolved-symlink spelling) should succeed, got: %v", err)
	}
	defer handle3.Close()
	entries, err := handle3.ReadDir()
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.txt" {
		t.Errorf("unexpected directory listing: %+v", entries)
	}
}

// TestResolvePath_SymlinkedWorkspaceRoot_GenuineEscapeStillRefused is the
// negative control for the fix proven above (Binding Rule 4: a fix needs a
// positive assertion AND a control proving it didn't just start accepting
// everything). Normalizing policy.WorkDir's own symlink must not, in the
// process, start ACCEPTING a WRITE that genuinely escapes the workspace —
// neither via an absolute path to an unrelated real location, nor via a
// lexical ".." escape expressed relative to the symlinked root. UPDATED for
// ADR-063 / spec unified-file-access-and-mounts FR-2.2: this used FSOpRead
// originally; reads are now open outside WorkDir by design (see
// TestResolvePath_SymlinkedWorkspaceRoot_Read_NowOpen below for that half),
// so the negative control that still means something here — "the symlink
// normalization fix didn't quietly widen confinement" — is FSOpWrite, which
// remains confined regardless of Scope.
func TestResolvePath_SymlinkedWorkspaceRoot_GenuineEscapeStillRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are POSIX-specific here")
	}

	realTarget := t.TempDir()
	parent := t.TempDir()
	symlinkedWorkDir := filepath.Join(parent, "workdir-link")
	if err := os.Symlink(realTarget, symlinkedWorkDir); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	outside := t.TempDir()
	secretFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("top secret"), 0o600); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	policy := fspolicy.FSPolicy{WorkDir: symlinkedWorkDir, Scope: fspolicy.FSScopeConfined}

	// An absolute path to a real file entirely outside the (symlinked)
	// workspace must still be refused for a write.
	_, err := ResolvePath(context.Background(), policy, "write_file", "call-1", FSOpWrite, secretFile)
	if err == nil {
		t.Fatal("expected ResolvePath to refuse a write outside the symlinked workspace, got success")
	}
	if !errors.Is(err, ErrOutsideScope) {
		t.Errorf("expected ErrOutsideScope, got: %v", err)
	}

	// A leading ".." escape expressed relative to the symlinked WorkDir must
	// still be refused too, for a write.
	_, err = ResolvePath(context.Background(), policy, "write_file", "call-2", FSOpWrite, "../secret.txt")
	if err == nil {
		t.Fatal("expected a leading .. escape to be refused even with a symlinked WorkDir")
	}
	if !errors.Is(err, ErrOutsideScope) {
		t.Errorf("expected ErrOutsideScope, got: %v", err)
	}
}

// TestResolvePath_SymlinkedWorkspaceRoot_Read_NowOpen is the FR-2.2 read
// counterpart to the write-only negative control above: the identical
// absolute-path and ".." escapes now succeed for a read, through a
// symlinked WorkDir just like through a plain one.
func TestResolvePath_SymlinkedWorkspaceRoot_Read_NowOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are POSIX-specific here")
	}

	realTarget := t.TempDir()
	parent := t.TempDir()
	symlinkedWorkDir := filepath.Join(parent, "workdir-link")
	if err := os.Symlink(realTarget, symlinkedWorkDir); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	outside := t.TempDir()
	secretFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("top secret"), 0o600); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	policy := fspolicy.FSPolicy{WorkDir: symlinkedWorkDir, Scope: fspolicy.FSScopeConfined}

	handle, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, secretFile)
	if err != nil {
		t.Fatalf("FR-2.2: expected a read outside the symlinked workspace to succeed, got: %v", err)
	}
	handle.Close()

	handle2, err := ResolvePath(context.Background(), policy, "read_file", "call-2", FSOpRead, "../secret.txt")
	if err != nil {
		t.Fatalf("FR-2.2: expected a '..' escape read to succeed even with a symlinked WorkDir, got: %v", err)
	}
	handle2.Close()
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

// TestResolvePath_ZeroValuePolicyRefused is the BLOCK #1 regression (ADR-046
// P1 review): ResolvePath now calls policy.Validate() as its FIRST step, so
// a hand-built zero-value fspolicy.FSPolicy{} (WorkDir=="", Scope=="") is
// refused with a wrapped ErrPathInvalid rather than reaching any resolution
// logic at all (which, pre-fix, could fall through to unpredictable
// behavior depending on what filepath.Join/EvalSymlinks did with an empty
// WorkDir).
func TestResolvePath_ZeroValuePolicyRefused(t *testing.T) {
	_, err := ResolvePath(context.Background(), fspolicy.FSPolicy{}, "read_file", "", FSOpRead, "x")
	if err == nil {
		t.Fatalf("expected a zero-value FSPolicy{} to be refused")
	}
	if !errors.Is(err, ErrPathInvalid) {
		t.Errorf("expected ErrPathInvalid, got: %v", err)
	}
}

// TestResolvePath_UnrestrictedScope_CarveOutHoldsUnderRace is the HIGH #3
// regression (ADR-046 P1 review): under FSScopeUnrestricted, ResolvePath
// returns a host-mode (root==nil) PathHandle whose I/O methods do raw os.*
// calls against h.abs — a path resolved ONCE, at ResolvePath time. Before
// this fix, nothing re-verified FR-017's carve-out protection between that
// resolve and the actual I/O call, so an attacker who could swap the
// resolved target into a symlink pointing at a carve-out (master.key here)
// in that window would have the read served straight through. This pins
// PathHandle.ReadFile's recheckUnrestrictedCarveOut re-check: it must catch
// the swapped-in carve-out and refuse with ErrCarveOut, even though the
// FIRST resolution (before the swap) legitimately succeeded.
func TestResolvePath_UnrestrictedScope_CarveOutHoldsUnderRace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink races are POSIX-specific")
	}
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	agentHome := filepath.Join(home, "agents", "self")
	if err := os.MkdirAll(agentHome, 0o755); err != nil {
		t.Fatalf("mkdir agentHome: %v", err)
	}
	masterKey := filepath.Join(home, "master.key")
	if err := os.WriteFile(masterKey, []byte("key-material"), 0o600); err != nil {
		t.Fatalf("seed master.key: %v", err)
	}

	// A decoy file OUTSIDE any carve-out root and outside WorkDir — the
	// host-mode branch is reached because it is out-of-scope but the
	// effective scope is Unrestricted.
	decoyDir := t.TempDir()
	decoyPath := filepath.Join(decoyDir, "decoy.txt")
	if err := os.WriteFile(decoyPath, []byte("innocuous"), 0o644); err != nil {
		t.Fatalf("seed decoy: %v", err)
	}

	policy, err := fspolicy.EffectiveFSPolicy(
		context.Background(), agentHome, "", false, /* restrict=false -> Unrestricted */
		config.OmnipusHomeDir(), "self", "",
	)
	if err != nil {
		t.Fatalf("EffectiveFSPolicy: %v", err)
	}
	if policy.Scope != fspolicy.FSScopeUnrestricted {
		t.Fatalf("expected FSScopeUnrestricted, got %v", policy.Scope)
	}

	handle, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, decoyPath)
	if err != nil {
		t.Fatalf("initial ResolvePath (decoy, not yet swapped) should succeed: %v", err)
	}
	defer handle.Close()

	// TOCTOU: swap the already-resolved host-mode target itself into a
	// symlink pointing at master.key, AFTER ResolvePath has already
	// resolved and returned the handle.
	if err = os.Remove(decoyPath); err != nil {
		t.Fatalf("remove decoy: %v", err)
	}
	if err = os.Symlink(masterKey, decoyPath); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	data, err := handle.ReadFile()
	if err == nil {
		t.Fatalf("expected the swapped-in carve-out to be refused at I/O time, got content: %q", data)
	}
	if !errors.Is(err, ErrCarveOut) {
		t.Errorf("expected ErrCarveOut from the I/O-time re-check, got: %v", err)
	}
	if string(data) == "key-material" {
		t.Fatalf("TOCTOU BREACH: read master.key content through the swapped decoy path: %q", data)
	}
}

// TestResolvePath_UnicodePathResolvesCorrectly — item #10 (ADR-046 P1
// review): a relative path containing multi-script Unicode components lands
// under WorkDir exactly like an ASCII one, with no lossy normalization.
func TestResolvePath_UnicodePathResolvesCorrectly(t *testing.T) {
	workDir := t.TempDir()
	rel := filepath.Join("工作", "データ", "файл.txt")
	full := filepath.Join(workDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir unicode dir: %v", err)
	}
	if err := os.WriteFile(full, []byte("unicode ok"), 0o644); err != nil {
		t.Fatalf("seed unicode file: %v", err)
	}

	policy := confinedPolicy(t, workDir)
	handle, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, rel)
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	defer handle.Close()

	data, err := handle.ReadFile()
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "unicode ok" {
		t.Errorf("content = %q, want %q", data, "unicode ok")
	}
}

// TestResolvePath_EmptyPathDefaultsToWorkDir — item #10 (ADR-046 P1
// review): an empty rawPath resolves to WorkDir itself (list_directory's
// documented "." default), not an error.
func TestResolvePath_EmptyPathDefaultsToWorkDir(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}

	policy := confinedPolicy(t, workDir)
	handle, err := ResolvePath(context.Background(), policy, "list_dir", "", FSOpList, "")
	if err != nil {
		t.Fatalf("ResolvePath with empty rawPath: %v", err)
	}
	defer handle.Close()

	entries, err := handle.ReadDir()
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == "marker.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf(
			"expected marker.txt in the WorkDir listing when rawPath defaults to WorkDir, got entries: %v",
			entries,
		)
	}
}

// TestResolvePath_LeadingDotDotEscape_Write — item #10 (ADR-046 P1 review),
// NARROWED to FSOpWrite by ADR-063 / spec unified-file-access-and-mounts
// FR-2.2: a rawPath that starts with ".." (escaping WorkDir from the very
// first component) is still refused under Confined scope for a WRITE. The
// original FSOpRead version of this assertion is now false by design — see
// TestResolvePath_LeadingDotDotEscape_Read_NowOpen.
func TestResolvePath_LeadingDotDotEscape_Write(t *testing.T) {
	workDir := t.TempDir()
	policy := confinedPolicy(t, workDir)

	_, err := ResolvePath(context.Background(), policy, "write_file", "", FSOpWrite, filepath.Join("..", "outside.txt"))
	if err == nil {
		t.Fatalf("expected a leading '..' escape to be refused for a write")
	}
	if !errors.Is(err, ErrOutsideScope) {
		t.Errorf("expected ErrOutsideScope, got: %v", err)
	}
}

// TestResolvePath_LeadingDotDotEscape_Read_NowOpen is the FR-2.2 read
// counterpart: the identical ".." escape now succeeds for a read, since
// reads are open outside WorkDir regardless of how the outside path was
// spelled (absolute, symlink, or a lexical ".." escape) as long as it is not
// in the secret set.
func TestResolvePath_LeadingDotDotEscape_Read_NowOpen(t *testing.T) {
	outer := t.TempDir()
	workDir := filepath.Join(outer, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}
	// The ".." escape from workDir lands in workDir's own parent (outer,
	// fully test-owned) — seed a real file there so a successful read has
	// real content to check.
	outsideFile := filepath.Join(outer, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside content"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	policy := confinedPolicy(t, workDir)

	handle, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, filepath.Join("..", "outside.txt"))
	if err != nil {
		t.Fatalf("FR-2.2: expected a leading '..' escape to succeed for a read, got: %v", err)
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

// TestResolvePath_MidStringDotDotReentry_Write — item #10 (ADR-046 P1
// review), NARROWED to FSOpWrite: a rawPath that dips into a subdirectory
// before ".."-ing back out past WorkDir (rather than starting with ".."
// immediately) is refused too — a lexically distinct shape from
// TestResolvePath_LeadingDotDotEscape_Write and from the symlink-based
// escape tests above (no symlink involved here at all, pure ".." reentry).
func TestResolvePath_MidStringDotDotReentry_Write(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	policy := confinedPolicy(t, workDir)

	_, err := ResolvePath(context.Background(), policy, "write_file", "", FSOpWrite,
		filepath.Join("sub", "..", "..", "outside.txt"))
	if err == nil {
		t.Fatalf("expected a mid-string '..' reentry escape to be refused for a write")
	}
	if !errors.Is(err, ErrOutsideScope) {
		t.Errorf("expected ErrOutsideScope, got: %v", err)
	}
}

// TestResolvePath_MidStringDotDotReentry_Read_NowOpen is the FR-2.2 read
// counterpart to the mid-string reentry shape above.
func TestResolvePath_MidStringDotDotReentry_Read_NowOpen(t *testing.T) {
	outer := t.TempDir()
	workDir := filepath.Join(outer, "work")
	if err := os.MkdirAll(filepath.Join(workDir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	outsideFile := filepath.Join(outer, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside content"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	policy := confinedPolicy(t, workDir)

	handle, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead,
		filepath.Join("sub", "..", "..", "outside.txt"))
	if err != nil {
		t.Fatalf("FR-2.2: expected a mid-string '..' reentry to succeed for a read, got: %v", err)
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

// TestGenericTools_PassDocumentedFSOp is item #12 (ADR-046 P1 review): pins
// each known production ResolvePath/ResolvePathAllowingPatterns call site's
// FSOp argument to its documented value. FSOp is a P2 seam ResolvePath does
// not yet consult for any decision (see FSOp's doc comment) and is not
// stored on the returned handle for post-hoc inspection, so a runtime spy
// can't observe it — this is a source-text assertion instead, reusing the
// runtime.Caller(0)-relative-directory technique resolvepath_lint_test.go's
// AST walk already established for this package. A future refactor that
// silently changes read_file's FSOp from FSOpRead to something else (or
// merges two call sites incorrectly) fails this test immediately rather
// than only surfacing once the P2 ask-flow starts keying off op for real.
func TestGenericTools_PassDocumentedFSOp(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	toolsDir := filepath.Dir(thisFile)
	browserDir := filepath.Join(toolsDir, "browser")

	cases := []struct {
		dir     string
		file    string
		tool    string
		snippet string
	}{
		{
			toolsDir, "filesystem.go", "read_file",
			`ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpRead, path, t.patterns)`,
		},
		{
			toolsDir, "filesystem.go", "write_file",
			`ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpWrite, path, t.patterns)`,
		},
		{
			toolsDir, "filesystem.go", "list_directory",
			`ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpList, path, t.patterns)`,
		},
		{
			toolsDir, "edit.go", "edit_file / append_file",
			`ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpWrite, path, t.patterns)`,
		},
		{
			// FR-2.3a (ADR-063 / spec unified-file-access-and-mounts):
			// send_file uses FSOpSend, not FSOpRead, purely for audit —
			// distinguishing a disclosure to a chat channel from an
			// ordinary read. It carries no additional path restriction.
			toolsDir, "send_file.go", "send_file",
			`ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpSend, path, t.allowPaths)`,
		},
		{
			toolsDir, "web_serve.go", "web_serve (static + dev)",
			`ResolvePath(ctx, policy, ToolNameWebServe, "", FSOpServe, rawPath)`,
		},
		{
			toolsDir, "shell.go", "bash (cwd resolution)",
			`ResolvePath(ctx, policyForCWD, "bash", "", FSOpExec, rawCWD)`,
		},
		{
			browserDir, "tools.go", "browser_screenshot",
			`tools.ResolvePath(ctx, policy, "browser_screenshot", "", tools.FSOpWrite, filename)`,
		},
		// "workspace_read (REST handler)" (pkg/gateway/rest_workspace.go
		// HandleWorkspace, FR-2.3c) is deliberately NOT pinned here anymore.
		// HandleWorkspace was deleted in commit 7cbe692b ("refactor: remove
		// 40 unreachable functions") as confirmed dead code — it was built
		// but never registered on any mux (issue #470) and was superseded
		// by ADR-044's `/preview/` mechanism. There is now no ResolvePath
		// call site anywhere in pkg/gateway for this test to pin: grep
		// confirms zero non-test callers of tools.ResolvePath /
		// tools.ResolvePathAllowingPatterns under pkg/gateway. Re-add a case
		// here if a gateway-side ResolvePath call site is introduced again.
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(tc.dir, tc.file))
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			if !strings.Contains(string(data), tc.snippet) {
				t.Errorf("%s: expected call site for %q not found (FSOp drifted from its documented value):\n  %s",
					tc.file, tc.tool, tc.snippet)
			}
		})
	}
}

// ============================================================================
// FR-2 (ADR-063 / spec unified-file-access-and-mounts) — operation-aware
// decision coverage. The table below is the "every op x location" matrix the
// task explicitly asks for: inside the work dir, outside it (non-carve-out),
// inside a mount (policy.AllowedRoots), and the secret set (a dedicated
// sub-test below, since it needs a real $OMNIPUS_HOME-shaped tree rather
// than a bare two-directory fixture).
// ============================================================================

// fr2Tree is the shared fixture: a work dir, an unrelated outside dir, and a
// mount root — a mount is modeled exactly as FR-6.1 describes it at the app
// layer, a write grant on a real local folder via policy.AllowedRoots,
// independent of the FR-5.4 symlink materialization mounts will also get.
type fr2Tree struct {
	workDir   string
	outside   string
	mountRoot string
}

func newFR2Tree(t *testing.T) fr2Tree {
	t.Helper()
	return fr2Tree{
		workDir:   t.TempDir(),
		outside:   t.TempDir(),
		mountRoot: t.TempDir(),
	}
}

// fr2Policy builds an FSPolicy for tr under the given scope, with
// tr.mountRoot granted via AllowedRoots.
func fr2Policy(t *testing.T, tr fr2Tree, scope fspolicy.FSScope) fspolicy.FSPolicy {
	t.Helper()
	realWork, err := filepath.EvalSymlinks(tr.workDir)
	if err != nil {
		t.Fatalf("resolve workDir: %v", err)
	}
	realMount, err := filepath.EvalSymlinks(tr.mountRoot)
	if err != nil {
		t.Fatalf("resolve mountRoot: %v", err)
	}
	return fspolicy.FSPolicy{WorkDir: realWork, Scope: scope, AllowedRoots: []string{realMount}}
}

// TestFR2_OpByLocationMatrix is the headline table-driven proof of FR-2.2:
// FSOpRead/List/Send are allowed inside the work dir, outside it, AND inside
// a mount (open, minus the secret set — a mount is a strict subset of what
// reads already permit); FSOpWrite/Serve are allowed inside the work dir and
// inside a mount, but refused outside both; FSOpExec is unchanged from the
// pre-FR-2 Scope-only dispatch (exercised separately below, since it varies
// by Scope, not by op-vs-location the way the other five do).
func TestFR2_OpByLocationMatrix(t *testing.T) {
	type location struct {
		name   string
		target func(tr fr2Tree) string
	}
	locations := []location{
		{"inside work dir", func(tr fr2Tree) string { return filepath.Join(tr.workDir, "f.txt") }},
		{"outside work dir (non-carve-out)", func(tr fr2Tree) string { return filepath.Join(tr.outside, "f.txt") }},
		{"inside a mount", func(tr fr2Tree) string { return filepath.Join(tr.mountRoot, "f.txt") }},
	}

	readLikeOps := []FSOp{FSOpRead, FSOpList, FSOpSend}
	writeLikeOps := []FSOp{FSOpWrite, FSOpServe}

	for _, scope := range []fspolicy.FSScope{fspolicy.FSScopeConfined, fspolicy.FSScopeUnrestricted} {
		for _, loc := range locations {
			for _, op := range readLikeOps {
				t.Run(string(scope)+"/"+string(op)+"/"+loc.name, func(t *testing.T) {
					tr := newFR2Tree(t)
					target := loc.target(tr)
					if err := os.WriteFile(target, []byte("content:"+string(op)), 0o644); err != nil {
						t.Fatalf("seed target: %v", err)
					}
					policy := fr2Policy(t, tr, scope)
					handle, err := ResolvePath(context.Background(), policy, "test", "", op, target)
					if err != nil {
						t.Fatalf("FR-2.2: expected %s to be allowed at %s under %s scope, got: %v", op, loc.name, scope, err)
					}
					defer handle.Close()
					data, err := handle.ReadFile()
					if err != nil {
						t.Fatalf("ReadFile: %v", err)
					}
					if string(data) != "content:"+string(op) {
						t.Errorf("content = %q, want %q", data, "content:"+string(op))
					}
				})
			}

			for _, op := range writeLikeOps {
				t.Run(string(scope)+"/"+string(op)+"/"+loc.name, func(t *testing.T) {
					tr := newFR2Tree(t)
					target := loc.target(tr)
					policy := fr2Policy(t, tr, scope)
					handle, err := ResolvePath(context.Background(), policy, "test", "", op, target)

					if loc.name == "outside work dir (non-carve-out)" {
						if err == nil {
							handle.Close()
							t.Fatalf("FR-2.2: expected %s outside WorkDir and outside any mount to be refused under %s scope", op, scope)
						}
						if !errors.Is(err, ErrOutsideScope) {
							t.Errorf("expected ErrOutsideScope, got: %v", err)
						}
						return
					}

					// inside WorkDir or inside the mount: must succeed, and
					// the write must actually persist to disk.
					if err != nil {
						t.Fatalf("FR-2.2: expected %s to be allowed at %s under %s scope, got: %v", op, loc.name, scope, err)
					}
					defer handle.Close()
					if writeErr := handle.WriteFile([]byte("written:" + string(op))); writeErr != nil {
						t.Fatalf("WriteFile: %v", writeErr)
					}
					data, err := os.ReadFile(target)
					if err != nil || string(data) != "written:"+string(op) {
						t.Fatalf("write did not persist: data=%q err=%v", data, err)
					}
				})
			}
		}
	}
}

// TestFR2_Exec_UnchangedScopeDispatch is FSOpExec's own coverage — per the
// task brief and FR-2.2's table, exec is deliberately left on the pre-FR-2
// Scope-only dispatch (the ADR-062 kernel model governs it, not this app
// layer's mount awareness), so — unlike Read/Write/Serve — its outcome still
// varies by Scope alone and does NOT consult policy.AllowedRoots.
func TestFR2_Exec_UnchangedScopeDispatch(t *testing.T) {
	tr := newFR2Tree(t)
	outsideTarget := filepath.Join(tr.outside, "cwd")
	if err := os.MkdirAll(outsideTarget, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mountTarget := filepath.Join(tr.mountRoot, "cwd")
	if err := os.MkdirAll(mountTarget, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	confined := fr2Policy(t, tr, fspolicy.FSScopeConfined)
	if _, err := ResolvePath(context.Background(), confined, "bash", "", FSOpExec, outsideTarget); !errors.Is(err, ErrOutsideScope) {
		t.Errorf("confined exec outside WorkDir: err = %v, want ErrOutsideScope", err)
	}
	// Unchanged from today: exec does NOT consult AllowedRoots, so even a
	// mount does not help it under Confined scope.
	if _, err := ResolvePath(context.Background(), confined, "bash", "", FSOpExec, mountTarget); !errors.Is(err, ErrOutsideScope) {
		t.Errorf("confined exec inside a mount: err = %v, want ErrOutsideScope (exec ignores AllowedRoots, unchanged)", err)
	}

	unrestricted := fr2Policy(t, tr, fspolicy.FSScopeUnrestricted)
	if handle, err := ResolvePath(context.Background(), unrestricted, "bash", "", FSOpExec, outsideTarget); err != nil {
		t.Errorf("unrestricted exec outside WorkDir: unexpected error: %v", err)
	} else {
		handle.Close()
	}
}

// TestFR2_MountWrite_AncestorSymlinkSwap_TOCTOU is the mount-write analogue of
// TestResolvePath_IOThroughOsRoot_NoTOCTOU (spec test 3, FR-006), for the
// adversarial-review finding that a mount write's I/O-time re-check
// (recheckUnrestrictedCarveOut, before this fix) verified only the secret
// set, never containment in policy.AllowedRoots — so a host-fs (root==nil)
// handle carrying a bare resolved-abs string could be walked out from under
// itself.
//
// The write resolves successfully to a target inside a granted mount; then,
// BEFORE the actual I/O runs, an ANCESTOR directory under the mount — fully
// owned by whoever the mount points at, which can be the SAME turn doing the
// write, since it is their own repository — is swapped from a real directory
// to a symlink pointing entirely outside every granted root (neither WorkDir
// nor the mount). The write must be refused at I/O time, not silently
// redirected to wherever the swapped symlink now resolves.
func TestFR2_MountWrite_AncestorSymlinkSwap_TOCTOU(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink races are POSIX-specific")
	}
	tr := newFR2Tree(t)
	policy := fr2Policy(t, tr, fspolicy.FSScopeConfined)

	// The ancestor directory under the mount that gets swapped — modeling the
	// finding's "002/sub" shape: a subdirectory the mount owner (here, the
	// same turn) already created inside their own mounted repo.
	subDir := filepath.Join(tr.mountRoot, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("seed sub: %v", err)
	}
	target := filepath.Join(subDir, "victim.txt")

	handle, err := ResolvePath(context.Background(), policy, "write_file", "", FSOpWrite, target)
	if err != nil {
		t.Fatalf("initial resolve (still inside the mount) should succeed: %v", err)
	}
	defer handle.Close()

	// escapeDir is outside BOTH WorkDir and the mount — the destination the
	// swap tries to redirect the write to.
	escapeDir := t.TempDir()

	// Swap "sub" from a real directory to a symlink pointing at escapeDir.
	// This needs no privilege beyond what a normal write-capable turn already
	// has over its own mounted directory.
	if err := os.RemoveAll(subDir); err != nil {
		t.Fatalf("remove sub: %v", err)
	}
	if err := os.Symlink(escapeDir, subDir); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	writeErr := handle.WriteFile([]byte("BREACH: wrote outside every granted root via ancestor symlink swap"))

	if _, statErr := os.Stat(filepath.Join(escapeDir, "victim.txt")); statErr == nil {
		t.Fatalf("BREACH: wrote outside every granted root via ancestor symlink swap (WriteFile err=%v)", writeErr)
	}
	if writeErr == nil {
		t.Fatalf("expected the ancestor-swap write to be refused, but WriteFile reported success")
	}
}

// TestFR2_SecretSet_DeniedForEveryOp is dataset rows 3/4 generalized across
// every op: the secret set (master.key here) is refused for every FSOp, not
// just reads — fspolicy.IsCarveOut runs unconditionally before ResolvePath
// ever consults op.
func TestFR2_SecretSet_DeniedForEveryOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	agentHome := filepath.Join(home, "agents", "self")
	if err := os.MkdirAll(agentHome, 0o755); err != nil {
		t.Fatalf("mkdir agentHome: %v", err)
	}
	masterKey := filepath.Join(home, "master.key")
	if err := os.WriteFile(masterKey, []byte("key-material"), 0o600); err != nil {
		t.Fatalf("seed master.key: %v", err)
	}

	for _, restrict := range []bool{true, false} {
		policy, err := fspolicy.EffectiveFSPolicy(context.Background(), agentHome, "", restrict, config.OmnipusHomeDir(), "self", "")
		if err != nil {
			t.Fatalf("EffectiveFSPolicy: %v", err)
		}
		for _, op := range []FSOp{FSOpRead, FSOpList, FSOpSend, FSOpWrite, FSOpServe} {
			t.Run(string(policy.Scope)+"/"+string(op), func(t *testing.T) {
				_, err := ResolvePath(context.Background(), policy, "test", "", op, masterKey)
				if !errors.Is(err, ErrCarveOut) {
					t.Errorf("op %s: err = %v, want ErrCarveOut", op, err)
				}
			})
		}
	}
}

// TestFR2_ZeroValueFSOp_Rejected is FR-2.4's direct test: an empty FSOp must
// fail loudly with ErrPathInvalid, whether the target resolves inside or
// outside WorkDir — never silently take a default branch.
func TestFR2_ZeroValueFSOp_Rejected(t *testing.T) {
	tr := newFR2Tree(t)
	policy := fr2Policy(t, tr, fspolicy.FSScopeConfined)

	insideTarget := filepath.Join(tr.workDir, "f.txt")
	if err := os.WriteFile(insideTarget, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Run("inside WorkDir", func(t *testing.T) {
		_, err := ResolvePath(context.Background(), policy, "test", "", FSOp(""), insideTarget)
		if !errors.Is(err, ErrPathInvalid) {
			t.Errorf("err = %v, want ErrPathInvalid", err)
		}
	})

	t.Run("outside WorkDir", func(t *testing.T) {
		_, err := ResolvePath(context.Background(), policy, "test", "", FSOp(""), filepath.Join(tr.outside, "f.txt"))
		if !errors.Is(err, ErrPathInvalid) {
			t.Errorf("err = %v, want ErrPathInvalid", err)
		}
	})

	t.Run("garbage FSOp value", func(t *testing.T) {
		_, err := ResolvePath(context.Background(), policy, "test", "", FSOp("delete"), insideTarget)
		if !errors.Is(err, ErrPathInvalid) {
			t.Errorf("err = %v, want ErrPathInvalid", err)
		}
	})
}

// TestFR2_HeadlinePair_ReadSucceedsWriteDenied_SamePath is spec §4 row 1/2's
// exact pairing, asserted directly against the SAME resolved path in one
// test: "read_file on a path outside the work dir SUCCEEDS while write_file
// on the SAME path is DENIED." This is the single assertion the whole of
// FR-2 exists to make true.
func TestFR2_HeadlinePair_ReadSucceedsWriteDenied_SamePath(t *testing.T) {
	tr := newFR2Tree(t)
	target := filepath.Join(tr.outside, "shared.txt")
	if err := os.WriteFile(target, []byte("pre-existing content"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, scope := range []fspolicy.FSScope{fspolicy.FSScopeConfined, fspolicy.FSScopeUnrestricted} {
		t.Run(string(scope), func(t *testing.T) {
			policy := fr2Policy(t, tr, scope)

			readHandle, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, target)
			if err != nil {
				t.Fatalf("read_file on %q must succeed, got: %v", target, err)
			}
			data, err := readHandle.ReadFile()
			readHandle.Close()
			if err != nil || string(data) != "pre-existing content" {
				t.Fatalf("ReadFile: data=%q err=%v", data, err)
			}

			_, err = ResolvePath(context.Background(), policy, "write_file", "", FSOpWrite, target)
			if err == nil {
				t.Fatalf("write_file on the SAME path %q must be denied", target)
			}
			if !errors.Is(err, ErrOutsideScope) {
				t.Errorf("expected ErrOutsideScope, got: %v", err)
			}

			// The file must be untouched by the denied write attempt.
			after, err := os.ReadFile(target)
			if err != nil || string(after) != "pre-existing content" {
				t.Fatalf("target must be unmodified after the denied write: data=%q err=%v", after, err)
			}
		})
	}
}
