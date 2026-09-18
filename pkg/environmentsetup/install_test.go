// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package environmentsetup

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeExecutable creates dir/path containing a trivial executable — an
// arbitrary, unknown program standing in for whatever the agent's script
// installs. Tests here never reference a known package catalogue.
func writeExecutable(t *testing.T, dir, path string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\n"
	if runtime.GOOS == "windows" {
		body = "@echo ok\r\n"
	}
	if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// unlockForTest restores writability on a committed (read-only) generation so
// t.TempDir cleanup can remove it.
func unlockForTest(root string) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(path, 0o755)
			return nil
		}
		_ = os.Chmod(path, 0o644)
		return nil
	})
}

// commitSharedTool begins a shared install, lays down arbitrary tool files,
// commits it, and registers read-write restoration for TempDir cleanup.
func commitSharedTool(t *testing.T, dataRoot string, paths ...string) *Target {
	t.Helper()
	target, err := BeginInstall(dataRoot, "", ScopeShared)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlockForTest(target.Prefix()) })
	for _, path := range paths {
		writeExecutable(t, target.Prefix(), path)
	}
	if _, err := target.Commit(); err != nil {
		t.Fatal(err)
	}
	return target
}

func requireCommittedShared(t *testing.T, dataRoot string) *Target {
	t.Helper()
	target, err := BeginInstall(dataRoot, "", ScopeShared)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlockForTest(target.Prefix()) })
	return target
}

func TestBeginInstallWorkspaceCreatesReservedSubtree(t *testing.T) {
	ws := t.TempDir()
	target, err := BeginInstall(t.TempDir(), ws, ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Abort() })
	real, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(real, ".omnipus", "env")
	if target.Prefix() != want {
		t.Fatalf("prefix = %q, want %q", target.Prefix(), want)
	}
	for _, dir := range []string{target.Prefix(), target.Cache(), target.Tmp()} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Fatalf("expected created dir %s: %v", dir, err)
		}
	}
	if got := filepath.Base(target.Cache()); got != "cache" {
		t.Fatalf("cache = %q, want cache subdir", got)
	}
}

func TestBeginInstallWorkspaceResolvesSymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "ws-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	target, err := BeginInstall(t.TempDir(), link, ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Abort() })
	if !filepath.IsAbs(target.Prefix()) {
		t.Fatalf("prefix not absolute: %q", target.Prefix())
	}
	if _, err := os.Stat(target.Prefix()); err != nil {
		t.Fatalf("prefix must exist behind symlinked root: %v", err)
	}
}

func TestBeginInstallWorkspaceRequiresExistingRoot(t *testing.T) {
	if _, err := BeginInstall(t.TempDir(), filepath.Join(t.TempDir(), "absent"), ScopeWorkspace); err == nil {
		t.Fatal("expected error for absent workspace root")
	}
}

func TestBeginInstallWorkspaceRejectsNonAbsoluteRoot(t *testing.T) {
	if _, err := BeginInstall(t.TempDir(), "relative/ws", ScopeWorkspace); err == nil {
		t.Fatal("expected error for relative workspace root")
	}
}

func TestTargetGrantConfinesToPrefix(t *testing.T) {
	for _, scope := range []Scope{ScopeWorkspace, ScopeShared} {
		ws := ""
		if scope == ScopeWorkspace {
			ws = t.TempDir()
		}
		target, err := BeginInstall(t.TempDir(), ws, scope)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = target.Abort() })
		grant := target.Grant()
		if len(grant.Writable) != 1 || grant.Writable[0] != target.Prefix() {
			t.Fatalf("scope %s: grant writable = %v, want exactly [prefix]", scope, grant.Writable)
		}
	}
}

func TestSharedCommitPublishesAdditively(t *testing.T) {
	dataRoot := t.TempDir()
	// Install A: an arbitrary unknown tool.
	a := commitSharedTool(t, dataRoot, "bin/tool-a")
	// Install B: a DIFFERENT arbitrary tool, must not hide A.
	b := commitSharedTool(t, dataRoot, "bin/tool-b")
	valid, broken, err := SharedPublished(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 0 {
		t.Fatalf("unexpected broken: %v", broken)
	}
	if len(valid) != 2 {
		t.Fatalf("published %d generations, want 2 (additive)", len(valid))
	}
	if valid[0].ID != b.GenerationID() || valid[1].ID != a.GenerationID() {
		t.Fatalf("PATH order wrong: newest %q then %q", valid[0].ID, valid[1].ID)
	}
	for _, gen := range valid {
		if gen.Digest == "" {
			t.Fatalf("generation %s published without digest", gen.ID)
		}
		if _, err := os.Stat(gen.Dir); err != nil {
			t.Fatalf("generation dir %s unreachable: %v", gen.Dir, err)
		}
	}
}

func TestSharedAbortPreservesPublishedGenerations(t *testing.T) {
	dataRoot := t.TempDir()
	a := commitSharedTool(t, dataRoot, "bin/tool-a")
	// B fails: abort must remove ONLY its own unpublished dir.
	b, err := BeginInstall(dataRoot, "", ScopeShared)
	if err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, b.Prefix(), "bin/tool-b-broken")
	if err := b.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.Prefix()); !os.IsNotExist(err) {
		t.Fatalf("aborted generation dir still present: %v", err)
	}
	valid, broken, err := SharedPublished(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(valid) != 1 || valid[0].ID != a.GenerationID() || len(broken) != 0 {
		t.Fatalf("after failed B: valid=%d broken=%d — A must remain published", len(valid), len(broken))
	}
	// A's files remain reachable at their exact published paths.
	if _, err := os.Stat(filepath.Join(a.Prefix(), "bin", "tool-a")); err != nil {
		t.Fatalf("A's files must stay reachable: %v", err)
	}
}

func TestSharedPrefixPathStableAcrossPublication(t *testing.T) {
	dataRoot := t.TempDir()
	target, err := BeginInstall(dataRoot, "", ScopeShared)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlockForTest(target.Prefix()) })
	prefixBefore := target.Prefix()
	// A script embeds the absolute prefix path (venv-shebang analogue).
	script := filepath.Join(prefixBefore, "bin", "embedded")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!"+prefixBefore+"/bin/interp\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Commit(); err != nil {
		t.Fatal(err)
	}
	if target.Prefix() != prefixBefore {
		t.Fatalf("publication moved the prefix: %q -> %q", prefixBefore, target.Prefix())
	}
	got, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "#!"+prefixBefore+"/bin/interp\n" {
		t.Fatalf("embedded absolute path broken by publication: %q", got)
	}
}

func TestSharedCommitWrongScopeAndDoubleUse(t *testing.T) {
	// Workspace scope cannot Commit.
	ws := t.TempDir()
	w, err := BeginInstall(t.TempDir(), ws, ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Abort() })
	if _, err := w.Commit(); err == nil {
		t.Fatal("workspace-scope Commit must be rejected")
	}
	// Shared target spent after Commit.
	s := commitSharedTool(t, t.TempDir(), "bin/tool")
	if _, err := s.Commit(); err == nil {
		t.Fatal("second Commit must fail")
	}
	if _, err := s.Commit(); err == nil {
		t.Fatal("second Commit must fail")
	}
	if err := s.Abort(); err == nil {
		t.Fatal("Abort after Commit must refuse to delete a published tree")
	}
	if _, err := os.Stat(s.Prefix()); err != nil {
		t.Fatalf("published tree must survive refused Abort: %v", err)
	}
}

func TestSharedCommitRequiresNonEmptyTree(t *testing.T) {
	target := requireCommittedShared(t, t.TempDir())
	_, err := target.Commit()
	if err == nil {
		t.Fatal("committing an empty generation must fail")
	}
	if !strings.Contains(err.Error(), "produced no files") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeEnvPathsViews(t *testing.T) {
	dataRoot := t.TempDir()
	ws := t.TempDir()

	// Nothing installed yet: empty view, no error.
	env, err := RuntimeEnvPaths(dataRoot, ws)
	if err != nil {
		t.Fatal(err)
	}
	if env.WorkspacePrefix != "" || len(env.Shared) != 0 || len(env.ReadExec) != 0 {
		t.Fatalf("expected empty view before any install: %+v", env)
	}

	// Workspace install makes the workspace half appear.
	w, err := BeginInstall(dataRoot, ws, ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Abort() })
	env, err = RuntimeEnvPaths(dataRoot, ws)
	if err != nil {
		t.Fatal(err)
	}
	if env.WorkspacePrefix == "" || env.WorkspaceCache == "" || env.WorkspaceTmp == "" {
		t.Fatalf("workspace view missing after install: %+v", env)
	}
	if len(env.Writable) != 2 {
		t.Fatalf("writable = %v, want [cache tmp]", env.Writable)
	}
	if len(env.Shared) != 0 {
		t.Fatalf("no shared generations expected yet: %+v", env.Shared)
	}

	// Two shared installs appear additively, newest first, with bin dirs.
	first := commitSharedTool(t, dataRoot, "bin/older-tool")
	second := commitSharedTool(t, dataRoot, "bin/newer-tool")
	env, err = RuntimeEnvPaths(dataRoot, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Shared) != 2 || env.Shared[0].ID != second.GenerationID() {
		t.Fatalf("shared order wrong: %+v", env.Shared)
	}
	wantNewest := filepath.Join(second.Prefix(), "bin")
	wantOlder := filepath.Join(first.Prefix(), "bin")
	if len(env.BinDirs) != 2 || env.BinDirs[0] != wantNewest || env.BinDirs[1] != wantOlder {
		t.Fatalf("bin dirs = %v, want [%s %s]", env.BinDirs, wantNewest, wantOlder)
	}
	// Shared dirs are read/execute for runtime; never in Writable.
	for _, w := range env.Writable {
		if strings.HasPrefix(w, dataRoot) {
			t.Fatalf("shared store leaked into writable: %s", w)
		}
	}
}

func TestRuntimeEnvPathsReportsBrokenGeneration(t *testing.T) {
	dataRoot := t.TempDir()
	good := commitSharedTool(t, dataRoot, "bin/good")
	// Corrupt the marker of the published generation.
	if err := os.Chmod(filepath.Join(good.Prefix(), markerFileName), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good.Prefix(), markerFileName), []byte("{not json"), 0o444); err != nil {
		t.Fatal(err)
	}
	env, err := RuntimeEnvPaths(dataRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Shared) != 0 {
		t.Fatalf("corrupt generation must be excluded: %+v", env.Shared)
	}
	if len(env.Broken) != 1 || env.Broken[0] != good.GenerationID() {
		t.Fatalf("broken = %v, want [%s]", env.Broken, good.GenerationID())
	}
}

func TestSharedLockSerializesConcurrentCommits(t *testing.T) {
	dataRoot := t.TempDir()
	const n = 4
	targets := make([]*Target, n)
	for i := range targets {
		target, err := BeginInstall(dataRoot, "", ScopeShared)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { unlockForTest(target.Prefix()) })
		writeExecutable(t, target.Prefix(), "bin/concurrent")
		targets[i] = target
	}
	done := make(chan error, n)
	for _, target := range targets {
		go func(tg *Target) {
			_, err := tg.Commit()
			done <- err
		}(target)
	}
	for range targets {
		if err := <-done; err != nil {
			t.Fatalf("concurrent commit: %v", err)
		}
	}
	valid, broken, err := SharedPublished(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(valid) != n || len(broken) != 0 {
		t.Fatalf("valid=%d broken=%d, want all %d published", len(valid), len(broken), n)
	}
}

func TestPublishedMarkerIsReadOnlyAndRecordsDigest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced on Windows")
	}
	target := commitSharedTool(t, t.TempDir(), "bin/tool")
	marker := filepath.Join(target.Prefix(), markerFileName)
	info, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o444 {
		t.Fatalf("marker mode = %o, want 444", info.Mode().Perm())
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"digest"`) {
		t.Fatalf("marker missing digest: %s", data)
	}
}

func TestGenerationIDsAreUniqueAndValid(t *testing.T) {
	dataRoot := t.TempDir()
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		target, err := BeginInstall(dataRoot, "", ScopeShared)
		if err != nil {
			t.Fatal(err)
		}
		if seen[target.GenerationID()] {
			t.Fatalf("duplicate generation id %s", target.GenerationID())
		}
		seen[target.GenerationID()] = true
		if !validGenerationID(target.GenerationID()) {
			t.Fatalf("invalid id %q", target.GenerationID())
		}
		if err := target.Abort(); err != nil {
			t.Fatal(err)
		}
	}
}

// BeginInstall (shared) must leave the pre-allocated destination REALLY at
// <store>/<genID> the moment it returns — Prefix/Cache/Tmp already exist — and
// must not leak a stray <dataRoot>/<genID> beside the store. This is the
// positive counterpart to the ancestor-symlink refusals: confined creation
// anchored at the data root is data-root-relative, so the store component is
// part of every created path (coordinator checkpoint CRIT2).
func TestBeginInstallSharedPreallocatesInsideStore(t *testing.T) {
	dataRoot := t.TempDir()
	target, err := BeginInstall(dataRoot, "", ScopeShared)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Abort() })

	for _, dir := range []string{target.Prefix(), target.Cache(), target.Tmp()} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("pre-allocated destination missing on return: %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("pre-allocated destination is not a directory: %s", dir)
		}
	}
	// Nothing may be created directly under the data root — the only new
	// subtree is the confined store, and it holds exactly this generation.
	entries, err := os.ReadDir(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), generationPrefix) {
			t.Fatalf("stray generation dir beside the store: %s", e.Name())
		}
	}
	store := filepath.Join(dataRoot, filepath.FromSlash(storeRelative))
	inside, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(inside) != 1 || inside[0].Name() != target.GenerationID() {
		t.Fatalf("store must hold exactly the new generation: %v", inside)
	}
}
