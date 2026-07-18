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
	if err := os.Remove(decoyPath); err != nil {
		t.Fatalf("remove decoy: %v", err)
	}
	if err := os.Symlink(masterKey, decoyPath); err != nil {
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
		t.Errorf("expected marker.txt in the WorkDir listing when rawPath defaults to WorkDir, got entries: %v", entries)
	}
}

// TestResolvePath_LeadingDotDotEscape — item #10 (ADR-046 P1 review): a
// rawPath that starts with ".." (escaping WorkDir from the very first
// component) is refused under Confined scope.
func TestResolvePath_LeadingDotDotEscape(t *testing.T) {
	workDir := t.TempDir()
	policy := confinedPolicy(t, workDir)

	_, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, filepath.Join("..", "outside.txt"))
	if err == nil {
		t.Fatalf("expected a leading '..' escape to be refused")
	}
	if !errors.Is(err, ErrOutsideScope) {
		t.Errorf("expected ErrOutsideScope, got: %v", err)
	}
}

// TestResolvePath_MidStringDotDotReentry — item #10 (ADR-046 P1 review): a
// rawPath that dips into a subdirectory before ".."-ing back out past
// WorkDir (rather than starting with ".." immediately) is refused too — a
// lexically distinct shape from TestResolvePath_LeadingDotDotEscape and from
// the symlink-based escape tests above (no symlink involved here at all,
// pure ".." reentry).
func TestResolvePath_MidStringDotDotReentry(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	policy := confinedPolicy(t, workDir)

	_, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead,
		filepath.Join("sub", "..", "..", "outside.txt"))
	if err == nil {
		t.Fatalf("expected a mid-string '..' reentry escape to be refused")
	}
	if !errors.Is(err, ErrOutsideScope) {
		t.Errorf("expected ErrOutsideScope, got: %v", err)
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
	gatewayDir := filepath.Join(filepath.Dir(toolsDir), "gateway")

	cases := []struct {
		dir     string
		file    string
		tool    string
		snippet string
	}{
		{toolsDir, "filesystem.go", "read_file",
			`ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpRead, path, t.patterns)`},
		{toolsDir, "filesystem.go", "write_file",
			`ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpWrite, path, t.patterns)`},
		{toolsDir, "filesystem.go", "list_directory",
			`ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpList, path, t.patterns)`},
		{toolsDir, "edit.go", "edit_file / append_file",
			`ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpWrite, path, t.patterns)`},
		{toolsDir, "send_file.go", "send_file",
			`ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpRead, path, t.allowPaths)`},
		{toolsDir, "web_serve.go", "web_serve (static + dev)",
			`ResolvePath(ctx, policy, ToolNameWebServe, "", FSOpServe, rawPath)`},
		{toolsDir, "shell.go", "bash (cwd resolution)",
			`ResolvePath(ctx, policyForCWD, "bash", "", FSOpExec, rawCWD)`},
		{browserDir, "tools.go", "browser_screenshot",
			`tools.ResolvePath(ctx, policy, "browser_screenshot", "", tools.FSOpWrite, filename)`},
		{gatewayDir, "rest_workspace.go", "workspace_read (REST handler)",
			`tools.ResolvePath(r.Context(), policy, "workspace_read", "", tools.FSOpRead, filePath)`},
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
