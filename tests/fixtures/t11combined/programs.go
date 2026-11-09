package t11combined

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TrustedProgramDir separates a trusted fixture from the agent-writable runtime
// temp folder (founder Q49 A). Only the empty child is os.TempDir; programs and
// their markers stay outside it. It does not rely on testing's TempDir caching.
// Environment changes are restored by testing; callers must not be parallel.
func TrustedProgramDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runtimeTemp := filepath.Join(dir, "agent-runtime-temp")
	if err := os.Mkdir(runtimeTemp, 0o700); err != nil {
		t.Fatalf("SETUP: separate runtime temp: %v", err)
	}
	t.Setenv("TMPDIR", runtimeTemp)
	if runtime.GOOS == "windows" {
		t.Setenv("TMP", runtimeTemp)
		t.Setenv("TEMP", runtimeTemp)
	}
	if filepath.Clean(os.TempDir()) != runtimeTemp {
		t.Fatalf("SETUP: runtime temp did not select the separate fixture folder")
	}
	return dir
}
