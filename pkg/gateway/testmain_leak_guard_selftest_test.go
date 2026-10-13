package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

// The guard must be able to see the failure it guards (it could otherwise stay
// green forever): drop a file where the leak lands, expect it listed, remove it.
func TestLeakGuardSeesAFileLeftUnderTheEntitiesDir(t *testing.T) {
	if len(leakedPackageDirFiles()) != 0 {
		t.Skip("the package directory already holds leaked files; the TestMain guard will report them")
	}
	dir := filepath.Join("entities", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "guard-probe.lock")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll("entities") })
	found := false
	for _, p := range leakedPackageDirFiles() {
		if p == probe {
			found = true
		}
	}
	if !found {
		t.Fatalf("the guard did not list %s", probe)
	}
}
