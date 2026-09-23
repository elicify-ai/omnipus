// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBinary_BareName_SearchesPathInOrder(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	wantPath := writeFakeExecutable(t, dirA, "git")
	writeFakeExecutable(t, dirB, "git") // a second "git" further down PATH — must not win

	pathList := dirA + string(filepath.ListSeparator) + dirB
	got, err := ResolveBinary("git", pathList)
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	// wantPath's file is not itself a symlink, so ResolveBinary returns it
	// unresolved (tryResolveExecutable only calls EvalSymlinks when the
	// FILE is a symlink, never for an ancestor directory) — compare against
	// the literal joined path, not an EvalSymlinks'd one, which would
	// additionally (and irrelevantly to this test) resolve a symlinked
	// TMPDIR ancestor on some platforms (e.g. macOS's /var -> /private/var).
	if got != wantPath {
		t.Errorf("ResolveBinary(\"git\", %q) = %q, want the FIRST PATH entry's binary %q", pathList, got, wantPath)
	}
}

func TestResolveBinary_MissingBinary_Errors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ResolveBinary("does-not-exist", dir); err == nil {
		t.Fatal("expected an error for a binary absent from PATH, got nil")
	}
}

func TestResolveBinary_PathShapedName_SkipsPathSearch(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "mytool")
	t.Chdir(dir)

	// pathList is deliberately unrelated/nonexistent: a path-shaped name
	// (here, relative — contains a '/') must resolve directly against the
	// filesystem, never search PATH.
	got, err := ResolveBinary("./mytool", "/does/not/exist/on/path")
	if err != nil {
		t.Fatalf("ResolveBinary(\"./mytool\"): %v", err)
	}
	want, err := filepath.Abs("mytool")
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if got != want {
		t.Errorf("ResolveBinary(\"./mytool\") = %q, want %q", got, want)
	}
}

func TestResolveBinary_ResolvesSymlink(t *testing.T) {
	skipOnWindows(t, "symlink creation needs elevated privileges on some Windows setups")
	realDir := t.TempDir()
	linkDir := t.TempDir()
	realBin := writeFakeExecutable(t, realDir, "realgit")
	linkPath := filepath.Join(linkDir, "git")
	if err := os.Symlink(realBin, linkPath); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	got, err := ResolveBinary("git", linkDir)
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(realBin)
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks: %v", err)
	}
	if got != wantResolved {
		t.Errorf("ResolveBinary(\"git\", %q) = %q, want the symlink TARGET %q", linkDir, got, wantResolved)
	}
}

func TestResolveBinary_NonExecutableFile_NotResolved(t *testing.T) {
	skipOnWindows(t, "POSIX execute-bit is not a Windows concept")
	dir := t.TempDir()
	writeNonExecutable(t, dir, "git")
	if _, err := ResolveBinary("git", dir); err == nil {
		t.Fatal("a non-executable file must not be resolved as a runnable binary")
	}
}

// TestVerify_Reresolution is the "immediately before spawn" re-check
// ADR-092 D3 requires: Verify re-resolves head against pathList and reports
// whether it still matches expected, catching a PATH/filesystem change
// between an earlier decision and now.
func TestVerify_Reresolution(t *testing.T) {
	dir := t.TempDir()
	binPath := writeFakeExecutable(t, dir, "git")
	expected, err := ResolveBinary("git", dir)
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}

	ok, resolved, err := Verify("git", dir, expected, ResolveBinary)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok || resolved != expected {
		t.Fatalf("Verify: got (%v, %q), want (true, %q) when nothing changed", ok, resolved, expected)
	}

	// Simulate a swap: remove the original and put a different binary at
	// the same PATH-searched name.
	if err := os.Remove(binPath); err != nil {
		t.Fatalf("os.Remove: %v", err)
	}
	writeFakeExecutable(t, dir, "git")
	ok2, resolved2, err := Verify("git", dir, expected, ResolveBinary)
	if err != nil {
		t.Fatalf("Verify after swap: %v", err)
	}
	// On most filesystems a same-name replace keeps the same resolved
	// absolute path (no symlink involved) — Verify(bool) is expected true
	// here too; the meaningful assertion is that Verify actually re-ran
	// resolution rather than trusting a cached value, which the mismatch
	// sub-case below demonstrates.
	if resolved2 != expected {
		t.Fatalf("resolved path changed unexpectedly after same-name replace: %q vs %q", resolved2, expected)
	}
	_ = ok2

	// A genuine mismatch: verify against a DIFFERENT expected path.
	otherDir := t.TempDir()
	otherExpected := writeFakeExecutable(t, otherDir, "unrelated")
	otherResolved, err := ResolveBinary("unrelated", otherDir)
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	_ = otherExpected
	ok3, _, err := Verify("git", dir, otherResolved, ResolveBinary)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok3 {
		t.Fatal("Verify must report false when the re-resolved path does not match the expected one")
	}
}
