// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package environmentsetup

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// requireSymlink creates oldname→newname, skipping the test when the platform
// forbids symlinks.
func requireSymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
}

func skipWindowsPermissionBits(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced on Windows")
	}
}

// BeginInstall must create the reserved workspace subtree through a
// root-confined handle (ES-FR-03): a pre-existing symlink anywhere below the
// authorized workspace root must be refused, never followed — the setup child's
// sandbox is applied only AFTER these parent-side operations.

func TestBeginInstallRefusesSymlinkedOmnipusDir(t *testing.T) {
	outside := t.TempDir()
	ws := t.TempDir()
	// The outside sentinel directory must EXIST so the symlink resolves —
	// a dangling link fails mkdir with ENOENT for the wrong reason.
	if err := os.MkdirAll(filepath.Join(outside, "omnipus"), 0o755); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, filepath.Join(outside, "omnipus"), filepath.Join(ws, ".omnipus"))

	target, err := BeginInstall(t.TempDir(), ws, ScopeWorkspace)
	if err == nil {
		_ = target.Abort()
		t.Fatal("pre-existing .omnipus symlink must be refused, not followed")
	}
	if _, err := os.Stat(filepath.Join(outside, "omnipus", "env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("setup escaped the workspace: outside env created: %v", err)
	}
}

func TestBeginInstallRefusesSymlinkedEnvDir(t *testing.T) {
	outside := t.TempDir()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".omnipus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "env"), 0o755); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, filepath.Join(outside, "env"), filepath.Join(ws, ".omnipus", "env"))

	target, err := BeginInstall(t.TempDir(), ws, ScopeWorkspace)
	if err == nil {
		_ = target.Abort()
		t.Fatal("pre-existing env symlink must be refused, not followed")
	}
	entries, err := os.ReadDir(filepath.Join(outside, "env"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("setup escaped the workspace: outside env populated: %v", entries)
	}
}

func TestBeginInstallRefusesSymlinkedCacheDir(t *testing.T) {
	outside := t.TempDir()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".omnipus", "env"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, filepath.Join(outside, "cache"), filepath.Join(ws, ".omnipus", "env", "cache"))

	target, err := BeginInstall(t.TempDir(), ws, ScopeWorkspace)
	if err == nil {
		_ = target.Abort()
		t.Fatal("pre-existing cache symlink must be refused, not followed")
	}
	entries, err := os.ReadDir(filepath.Join(outside, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("setup escaped the workspace: outside cache populated: %v", entries)
	}
}

// RuntimeEnvPaths decides read-side grants; an env subtree that is really a
// symlink out of the workspace must never produce grants pointing outside.

func TestRuntimeEnvPathsRefusesWorkspaceEnvOutsideRoot(t *testing.T) {
	outside := t.TempDir()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "env", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".omnipus"), 0o755); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, filepath.Join(outside, "env"), filepath.Join(ws, ".omnipus", "env"))

	env, err := RuntimeEnvPaths(t.TempDir(), ws)
	if err == nil {
		t.Fatalf("env symlink escaping the workspace must not yield grants: %+v", env)
	}
}

// Shared-store metadata and published markers are read side inputs too; a
// symlink must never smuggle in content from outside the store.

func TestReadPublishedSetRefusesSetSymlinkOutside(t *testing.T) {
	store := filepath.Join(t.TempDir(), storeRelative)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Valid, well-formed set content at an outside path — if the reader
	// follows the link, the smuggled set would be accepted.
	outsideSet := filepath.Join(t.TempDir(), "published.json")
	if err := os.WriteFile(outsideSet, []byte(`{"version":1,"generations":["gen-20260101T000000-ab12cd34"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, outsideSet, filepath.Join(store, publishedFileName))

	set, ok, err := readPublishedSet(store)
	if err == nil {
		t.Fatalf("set symlink out of the store must be refused; got set=%+v ok=%v", set, ok)
	}
	got, _ := os.ReadFile(outsideSet)
	if string(got) != `{"version":1,"generations":["gen-20260101T000000-ab12cd34"]}` {
		t.Fatalf("outside set content was modified: %s", got)
	}
}

func TestSharedPublishedRefusesMarkerSymlinkOutside(t *testing.T) {
	dataRoot := t.TempDir()
	a := commitSharedTool(t, dataRoot, "bin/tool-a")
	marker := filepath.Join(a.Prefix(), markerFileName)
	if err := os.Chmod(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	// A fully valid marker for THIS generation, hosted outside the store.
	outside := filepath.Join(t.TempDir(), "marker.json")
	body := `{"storage_version":1,"generation":"` + a.GenerationID() + `","digest":"deadbeef","published_at":"2026-09-18T00:00:00Z","platform":"test/test"}`
	if err := os.WriteFile(outside, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, outside, marker)

	valid, broken, err := SharedPublished(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(valid) != 0 {
		t.Fatalf("generation with outside marker symlink must not be granted: %+v", valid)
	}
	if len(broken) != 1 || broken[0] != a.GenerationID() {
		t.Fatalf("broken = %v, want [%s]", broken, a.GenerationID())
	}
}

// ES-FR-03 / BDD-05: one installer at a time per workspace prefix. The second
// concurrent BeginInstall gets a clear busy refusal (never an unbounded block),
// and Abort — called by the tool on every terminal outcome — releases the
// target for the next begin.

func TestWorkspaceInstallLockSerializesAndAbortReleases(t *testing.T) {
	dataRoot := t.TempDir()
	ws := t.TempDir()
	first, err := BeginInstall(dataRoot, ws, ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BeginInstall(dataRoot, ws, ScopeWorkspace)
	if err == nil {
		_ = second.Abort()
		t.Fatal("concurrent install into the same workspace must be refused as busy")
	}
	if !errors.Is(err, errTargetBusy) {
		t.Fatalf("busy refusal must be recognizable: %v", err)
	}
	if err = first.Abort(); err != nil {
		t.Fatalf("Abort must release the target lock: %v", err)
	}
	// Retry after release must succeed.
	third, err := BeginInstall(dataRoot, ws, ScopeWorkspace)
	if err != nil {
		t.Fatalf("begin after Abort must succeed: %v", err)
	}
	if err = third.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceInstallLockReleasedOnStartupFailure(t *testing.T) {
	dataRoot := t.TempDir()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".omnipus", "env"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file where the cache dir must be created: BeginInstall fails after
	// taking the lock, and that failure must release the lock again.
	if err := os.WriteFile(filepath.Join(ws, ".omnipus", "env", "cache"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginInstall(dataRoot, ws, ScopeWorkspace); err == nil {
		t.Fatal("expected failure: cache path is occupied by a file")
	}
	if err := os.Remove(filepath.Join(ws, ".omnipus", "env", "cache")); err != nil {
		t.Fatal(err)
	}
	again, err := BeginInstall(dataRoot, ws, ScopeWorkspace)
	if err != nil {
		t.Fatalf("lock from the failed begin was not released: %v", err)
	}
	if err := again.Abort(); err != nil {
		t.Fatal(err)
	}
}

// Shared generations are uniquely allocated, so concurrent shared begins are
// legitimate and must NOT be blocked by the workspace lifetime lock.
func TestSharedBeginInstallAllowsConcurrentTargets(t *testing.T) {
	dataRoot := t.TempDir()
	a, err := BeginInstall(dataRoot, "", ScopeShared)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlockForTest(a.Prefix()) })
	b, err := BeginInstall(dataRoot, "", ScopeShared)
	if err != nil {
		t.Fatalf("second concurrent shared begin must be allowed: %v", err)
	}
	t.Cleanup(func() { unlockForTest(b.Prefix()) })
	_ = a.Abort()
	_ = b.Abort()
}

// A Commit that fails after the tree was locked read-only must still be
// cleanable: Abort restores owner-writable bits inside its own unpublished
// destination (never following symlinks) and removes it, leaving older
// published generations untouched.
func TestSharedAbortRemovesReadOnlyTreeAfterFailedCommit(t *testing.T) {
	skipWindowsPermissionBits(t)
	dataRoot := t.TempDir()
	a := commitSharedTool(t, dataRoot, "bin/tool-a")
	b := requireCommittedShared(t, dataRoot)
	writeExecutable(t, b.Prefix(), "bin/tool-b")

	// Induce a publish failure after lockDownReadOnly: make the store
	// unwritable so the set-file write fails while b's tree is already
	// read-only.
	store := SharedStoreDir(dataRoot)
	if err := os.Chmod(store, 0o555); err != nil {
		t.Fatal(err)
	}
	_, commitErr := b.Commit()
	if err := os.Chmod(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if commitErr == nil {
		t.Fatal("expected induced publish failure")
	}
	if err := b.Abort(); err != nil {
		t.Fatalf("Abort must remove its read-only unpublished tree: %v", err)
	}
	if _, err := os.Stat(b.Prefix()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unpublished read-only tree not removed: %v", err)
	}
	valid, broken, err := SharedPublished(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(valid) != 1 || valid[0].ID != a.GenerationID() || len(broken) != 0 {
		t.Fatalf("older published generation must survive: valid=%+v broken=%v", valid, broken)
	}
}

// The published set is a bounded control file; a set padded past the cap must
// be rejected as oversize — never silently truncated into a "valid" set
// (spec: reject oversize input without silently truncating it).
func TestReadPublishedSetRejectsOversizeSet(t *testing.T) {
	store := filepath.Join(t.TempDir(), storeRelative)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Valid set followed by space padding: truncation at the cap would still
	// parse as a well-formed set (trailing whitespace is valid JSON), hiding
	// the oversize state.
	body := `{"version":1,"generations":["gen-20260101T000000-ab12cd34"]}` +
		strings.Repeat(" ", 200<<10)
	if err := os.WriteFile(filepath.Join(store, publishedFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set, ok, err := readPublishedSet(store)
	if err == nil {
		t.Fatalf("oversize set must be rejected, not truncated; got set=%+v ok=%v", set, ok)
	}
}

// The shared store's ANCESTORS below the data root are descendants of an
// authorized root too: a planted <dataRoot>/toolchains symlink must be
// refused at BeginInstall, never followed to create the store outside the
// application data root (CRIT2).
func TestBeginInstallSharedRefusesAncestorSymlinkOutside(t *testing.T) {
	outside := t.TempDir()
	dataRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "toolchains", "environment", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, filepath.Join(outside, "toolchains"), filepath.Join(dataRoot, "toolchains"))

	target, err := BeginInstall(dataRoot, "", ScopeShared)
	if err == nil {
		unlockForTest(target.Prefix())
		_ = target.Abort()
		t.Fatal("ancestor symlink below the data root must be refused, not followed")
	}
	entries, err := os.ReadDir(filepath.Join(outside, "toolchains", "environment", "shared"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("store escaped the data root: outside shared store populated: %v", entries)
	}
}

// Readers must refuse an escaped store as well: no ReadExec/BinDirs grant may
// point outside the authorized data root through a planted ancestor symlink.
func TestSharedPublishedRefusesStoreAncestorSymlinkOutside(t *testing.T) {
	outside := t.TempDir()
	dataRoot := t.TempDir()
	store := filepath.Join(outside, "toolchains", "environment", "shared")
	if err := os.MkdirAll(filepath.Join(store, "gen-20260101T000000-ab12cd34", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, publishedFileName), []byte(`{"version":1,"generations":["gen-20260101T000000-ab12cd34"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	markerBody := `{"storage_version":1,"generation":"gen-20260101T000000-ab12cd34","digest":"deadbeef","published_at":"2026-09-18T00:00:00Z","platform":"test/test"}`
	if err := os.WriteFile(filepath.Join(store, "gen-20260101T000000-ab12cd34", markerFileName), []byte(markerBody), 0o444); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, filepath.Join(outside, "toolchains"), filepath.Join(dataRoot, "toolchains"))

	valid, broken, err := SharedPublished(dataRoot)
	if err == nil {
		t.Fatalf("store reached through an escaped ancestor must be refused, not granted: valid=%+v broken=%v", valid, broken)
	}
}
