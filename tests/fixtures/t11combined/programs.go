package t11combined

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TrustedProgramDir puts executable fixtures in the test checkout, not inside
// the agent-writable runtime temp folder (Q49). It never changes TMPDIR, TMP or
// TEMP. Callers still configure their separate writable home/workspace roots.
func TrustedProgramDir(t *testing.T) string {
	t.Helper()
	base, err := os.Getwd()
	if err != nil {
		t.Fatalf("SETUP: locate trusted fixture checkout: %v", err)
	}
	dir, err := os.MkdirTemp(base, ".t11-trusted-program-")
	if err != nil {
		t.Fatalf("SETUP: create trusted fixture outside runtime temp: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("SETUP: remove trusted fixture: %v", err)
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
