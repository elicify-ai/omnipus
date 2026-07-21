//go:build !linux && !cgo

// rest_plan_stop_immutable_other_test.go — non-Linux twin of
// blockNewFilesInDir (see rest_plan_stop_immutable_linux_test.go's doc
// comment for the full rationale and the two rejected prior approaches).
// This project's gateway test suite is exercised locally and on the
// ci-omnipus CI worker, both Linux; there is no verified immutable-flag
// equivalent wired up for other platforms here (darwin has chflags(2)'s
// UF_IMMUTABLE, but nothing in this codebase currently calls it), so the
// root case on a non-Linux platform skips outright rather than fabricate an
// unverified blocking mechanism that could silently produce a false pass —
// exactly the failure mode this whole helper exists to avoid.
package gateway

import (
	"os"
	"testing"
)

// blockNewFilesInDir (non-Linux) strips write permission on dir via chmod,
// which blocks new-file creation for an UNPRIVILEGED test process (the
// atomic-write path this defends against always calls
// os.CreateTemp(dir, ".tmp-*") before its rename, so blocking directory
// write is sufficient). As root, chmod is not a real block (superuser file
// permission bypass applies on non-Linux platforms too), so this skips
// rather than falsely reporting a block that isn't actually in effect.
func blockNewFilesInDir(t *testing.T, dir string) (restore func()) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("root on a non-Linux platform: no verified file-creation-block mechanism here; skipping rather than risking a false pass")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod 0o500 %s: %v", dir, err)
	}
	return func() { _ = os.Chmod(dir, 0o700) }
}
