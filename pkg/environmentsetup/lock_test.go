// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package environmentsetup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The Windows build has no cross-process advisory lock; its install lock is
// the in-process helper in lock_inprocess.go. These regressions pin the
// helper's contract natively on every platform: same-target busy refusal,
// independent targets never block each other, and release/reacquire works.
// Only the Windows file-open wrapper is cross-compiled, not runtime-tested.

// acquireBounded runs acquireInprocessLock in a goroutine and fails the test
// if it has not returned within 2s. The map mutex is only ever held for a map
// lookup plus a non-blocking TryLock, so any visible block is the reported
// deadlock bug — there is no legitimate waiting state.
func acquireBounded(t *testing.T, key string) (*sync.Mutex, error) {
	t.Helper()
	type result struct {
		mu  *sync.Mutex
		err error
	}
	done := make(chan result, 1)
	go func() {
		mu, err := acquireInprocessLock(key)
		done <- result{mu, err}
	}()
	select {
	case r := <-done:
		return r.mu, r.err
	case <-time.After(2 * time.Second):
		err := fmt.Errorf("acquireInprocessLock(%q) blocked 2s — global map mutex never released (cross-target deadlock)", key)
		t.Fatal(err)
		return nil, err
	}
}

// A second holder of the SAME target is refused busy, never blocked.
func TestInprocessInstallLockSameTargetBusyRefused(t *testing.T) {
	first, err := acquireBounded(t, "test:same-target")
	if err != nil {
		t.Fatal(err)
	}
	_, err = acquireBounded(t, "test:same-target")
	if !errors.Is(err, errTargetBusy) {
		t.Fatalf("same-target second acquire = %v, want errTargetBusy", err)
	}
	first.Unlock()
}

// After release the same target can be taken again.
func TestInprocessInstallLockReleaseReacquire(t *testing.T) {
	first, err := acquireBounded(t, "test:release-reacquire")
	if err != nil {
		t.Fatal(err)
	}
	first.Unlock()
	second, err := acquireBounded(t, "test:release-reacquire")
	if err != nil {
		t.Fatalf("reacquire after release must succeed: %v", err)
	}
	second.Unlock()
}

// An acquisition for one target must never stall an acquisition for an
// INDEPENDENT target: the map mutex is never held across anything blocking.
func TestInprocessInstallLockDifferentTargetsIndependent(t *testing.T) {
	first, err := acquireBounded(t, "test:independent-a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := acquireBounded(t, "test:independent-b")
	if err != nil {
		t.Fatalf("independent-target acquire must succeed: %v", err)
	}
	other.Unlock()
	first.Unlock()
}

// The lifetime lock must live in reserved workspace metadata OUTSIDE the
// installer-writable prefix (the installer legitimately owns the prefix and a
// generic script may clear and recreate it), and the grant must remain
// exactly the prefix.
func TestWorkspaceLockLivesOutsideInstallerPrefix(t *testing.T) {
	ws := t.TempDir()
	target, err := BeginInstall(t.TempDir(), ws, ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Abort() })

	outside := filepath.Join(ws, ".omnipus", ".install.lock")
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("lifetime lock must exist at %s, outside the prefix: %v", outside, err)
	}
	inside := filepath.Join(target.Prefix(), ".install.lock")
	if _, err := os.Stat(inside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no lock file may remain INSIDE the installer-writable prefix: %v", err)
	}
	if g := target.Grant(); len(g.Writable) != 1 || g.Writable[0] != target.Prefix() {
		t.Fatalf("grant = %+v, want writable exactly [prefix] — the lock must stay outside", g)
	}
}

// ES-FR-03/BDD-05 after the installer wipes its own prefix: a generic script
// that clears and recreates the installation prefix must not unlink the
// lifetime lock and open a second BeginInstall while the first still runs.
// (Unix: flock is inode-based, so a replaced lock file loses the lock.)
func TestWorkspaceLockSurvivesPrefixRecreation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows lock is path-keyed and in-process; this regression targets Unix flock's inode semantics")
	}
	dataRoot := t.TempDir()
	ws := t.TempDir()
	first, err := BeginInstall(dataRoot, ws, ScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}

	// The installer legitimately owns its prefix: clear and recreate exactly
	// as a generic "clean install" script would. The reserved metadata (and
	// with it the lock file) is outside this grant.
	if err = os.RemoveAll(first.Prefix()); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{first.Prefix(), first.Cache(), first.Tmp()} {
		if err = os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	second, err := BeginInstall(dataRoot, ws, ScopeWorkspace)
	if err == nil {
		_ = second.Abort()
		t.Fatal("begin after the installer wiped its own prefix must still be refused as busy")
	}
	if !errors.Is(err, errTargetBusy) {
		t.Fatalf("busy refusal must be recognizable: %v", err)
	}

	// Abort still releases the lock for the next installer.
	if err = first.Abort(); err != nil {
		t.Fatalf("Abort must release the target lock: %v", err)
	}
	third, err := BeginInstall(dataRoot, ws, ScopeWorkspace)
	if err != nil {
		t.Fatalf("begin after Abort must succeed: %v", err)
	}
	if err := third.Abort(); err != nil {
		t.Fatal(err)
	}
}

// A shared begin that fails AFTER the generation directory exists must clean
// up its own unpublished directory, and a cleanup failure must be reported,
// never silently discarded. The seam fires on the tmp step, after the
// generation and cache dirs were created through the confined root.
func TestSharedBeginFailureAfterGenerationCleansUp(t *testing.T) {
	dataRoot := t.TempDir()
	testFailGenerationStep = func(rel string) error {
		if strings.HasSuffix(rel, "/"+tmpSubdirName) {
			return errors.New("induced tmp creation failure")
		}
		return nil
	}
	t.Cleanup(func() { testFailGenerationStep = nil })

	_, err := BeginInstall(dataRoot, "", ScopeShared)
	if err == nil {
		t.Fatal("expected the induced generation-tmp failure")
	}
	if strings.Contains(err.Error(), "remove unpublished") {
		t.Fatalf("clean removal must not add a cleanup error: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dataRoot, filepath.FromSlash(storeRelative)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed begin must remove its own unpublished generation dir; store still holds %v", entries)
	}
}

// A failed cleanup must be REPORTED, never silently discarded: when the
// removal of the unpublished generation cannot complete, the returned error
// carries both the original cause and the removal failure (errors.Join).
func TestCleanupUnpublishedGenerationReportsRemovalFailure(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	gen := filepath.Join(store, "gen-20260101T000000-ab12cd34")
	if err := os.MkdirAll(filepath.Join(gen, tmpSubdirName), 0o755); err != nil {
		t.Fatal(err)
	}
	// An unwritable parent makes the final unlink of the generation dir
	// impossible, forcing the removal failure deterministically.
	if err := os.Chmod(store, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store, 0o755) })

	cause := errors.New("induced tmp creation failure")
	err := cleanupUnpublishedGeneration(gen, cause)
	if err == nil {
		t.Fatal("expected the joined cleanup failure")
	}
	if !errors.Is(err, cause) {
		t.Fatalf("original cause must survive the join: %v", err)
	}
	if !strings.Contains(err.Error(), "remove unpublished generation") {
		t.Fatalf("cleanup failure must be reported, not discarded: %v", err)
	}
	if _, statErr := os.Stat(gen); statErr != nil {
		t.Fatalf("removal failed yet the dir is gone — injection invalid: %v", statErr)
	}
}
