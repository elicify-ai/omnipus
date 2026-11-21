package t11combined

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TrustedProgramDir puts executable fixtures in the test checkout's ignored
// build folder, not inside the agent-writable runtime temp folder (Q49). Crash
// residue stays ignored; normal cleanup still removes each unique directory.
// It never changes TMPDIR, TMP or TEMP. Callers still configure their separate
// writable home/workspace roots.
func TrustedProgramDir(t *testing.T) string {
	t.Helper()
	base, err := os.Getwd()
	if err != nil {
		t.Fatalf("SETUP: locate trusted fixture checkout: %v", err)
	}
	base = filepath.Join(base, "build") // Already ignored by the checked-in build/ rule.
	if mkdirErr := os.MkdirAll(base, 0o700); mkdirErr != nil {
		t.Fatalf("SETUP: create ignored trusted fixture parent: %v", mkdirErr)
	}
	dir, err := os.MkdirTemp(base, ".t11-trusted-program-")
	if err != nil {
		t.Fatalf("SETUP: create trusted fixture outside runtime temp: %v", err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			t.Errorf("SETUP: remove trusted fixture: %v", removeErr)
		}
	})
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("SETUP: resolve trusted fixture: %v", err)
	}
	temp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatalf("SETUP: resolve unchanged runtime temp: %v", err)
	}
	// Shared Unix /tmp is explicitly outside the writable union. A checkout
	// under any other runtime temp root is unsuitable: fail, never move the root.
	shared := runtime.GOOS != "windows" && (temp == "/tmp" || temp == "/private/tmp")
	if !shared && strings.EqualFold(filepath.VolumeName(temp), filepath.VolumeName(physical)) {
		rel, err := filepath.Rel(temp, physical)
		if err != nil {
			t.Fatalf("SETUP: compare fixture and runtime temp: %v", err)
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("SETUP: trusted fixture %q is inside runtime temp %q", physical, temp)
		}
	}
	return physical
}
