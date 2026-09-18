// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package environmentsetup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateCleanAbsPathRejectsTraversalAndRelative(t *testing.T) {
	cases := []struct {
		name, path string
		wantErr    bool
	}{
		{"absolute", "/tmp/omnipus-test", false},
		{"empty", "", true},
		{"relative", "tmp/ws", true},
		{"traversal", "/tmp/../etc", true},
		{"unclean", "/tmp//ws", true},
	}
	for _, tc := range cases {
		err := validateCleanAbsPath("test path", tc.path)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: path %q error = %v, wantErr %v", tc.name, tc.path, err, tc.wantErr)
		}
	}
}

func TestWorkspaceEnvRootToleratesDeepLayout(t *testing.T) {
	ws := t.TempDir()
	got, err := WorkspaceEnvRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(real, ".omnipus", "env"); got != want {
		t.Fatalf("env root = %q, want %q", got, want)
	}
}

// Oversize is detected and REJECTED, never silently truncated into a short
// read (spec: reject oversize input without silently truncating it). The
// previous test asserted the old truncation behavior — that oracle was wrong.
func TestReadFileLimitedRejectsOversizeControlFile(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Exactly the cap: fine.
	okBytes := make([]byte, 64<<10)
	if err := os.WriteFile(filepath.Join(dir, "exact"), okBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := root.Open("exact")
	if err != nil {
		t.Fatal(err)
	}
	got, err := readFileLimited(f)
	f.Close()
	if err != nil {
		t.Fatalf("cap-sized control file must read: %v", err)
	}
	if len(got) != 64<<10 {
		t.Fatalf("read %d bytes, want 65536", len(got))
	}

	// One byte over: rejected as oversize, not truncated.
	big := make([]byte, 64<<10+1)
	if err := os.WriteFile(filepath.Join(dir, "big"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err = root.Open("big")
	if err != nil {
		t.Fatal(err)
	}
	_, err = readFileLimited(f)
	f.Close()
	if err == nil {
		t.Fatal("oversize control file must be rejected, not truncated")
	}
}

func TestBinSubdirsForPlatforms(t *testing.T) {
	got := binSubdirsFor("windows")
	if len(got) != 2 || got[0] != "Scripts" || got[1] != "bin" {
		t.Fatalf("windows bins = %v, want [Scripts bin]", got)
	}
	for _, goos := range []string{"linux", "darwin"} {
		if got := binSubdirsFor(goos); len(got) != 1 || got[0] != "bin" {
			t.Fatalf("%s bins = %v, want [bin]", goos, got)
		}
	}
}

func TestBinDirsUnderListsOnlyRealDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	requireSymlink(t, outside, filepath.Join(dir, "Scripts"))
	// The symlink is only a real dir on Windows convention paths; force the
	// platform-independent logic by calling the helper over both names.
	got := binDirsUnder(dir)
	want := filepath.Join(dir, "bin")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("binDirsUnder = %v, want [%s]", got, want)
	}
}
