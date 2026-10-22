package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The guard must be able to see the failure it guards (it could otherwise stay
// green forever): drop a file where the leak lands, expect it listed, remove it.
func TestLeakGuardSeesAFileLeftUnderTheEntitiesDir(t *testing.T) {
	// Pre-existing leaks are a FAILURE, never a skip: name them here, and
	// the TestMain guard reports them independently of this run's outcome.
	if leaked := leakedPackageDirFiles(); len(leaked) != 0 {
		t.Fatalf("the package directory already holds leaked files the guard sees — the TestMain guard must fail this run: %v", leaked)
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

// The same guard must see a migration sentinel left in the package
// directory itself, not only files under tasks/ or entities/.
func TestLeakGuardSeesASentinelLeftAtThePackageRoot(t *testing.T) {
	// Pre-existing leaks are a FAILURE, never a skip: name them here, and
	// the TestMain guard reports them independently of this run's outcome.
	// Fail before writing the probe: its cleanup would remove a sentinel
	// that was already there, so the files stay left in place for TestMain.
	if leaked := leakedPackageDirFiles(); len(leaked) != 0 {
		t.Fatalf("precondition: the package directory already holds leaked files the guard sees — left in place; the TestMain guard must fail this run: %s", strings.Join(leaked, ", "))
	}
	const name = ".milestones_migrated"
	if err := os.WriteFile(name, []byte("probe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(name) })
	found := false
	for _, p := range leakedPackageDirFiles() {
		if p == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("the guard did not list %s", name)
	}
}
