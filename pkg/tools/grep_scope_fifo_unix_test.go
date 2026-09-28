// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 RED pack — A3 from the Opus security-lead review (unix build tag).
//
// refuseNonRegular + grepGateFS.Open / regularOnlyFS.Open do Stat-then-Open
// as two steps; a file renamed to a FIFO in between makes the blocking
// Open hang forever (FIFO blocks until a writer connects). The fix is to
// open with O_NONBLOCK and check the kind on the opened file (the same
// shape PathHandle.OpenRegularNonBlocking uses) — the kind check refuses
// non-regular before the actual read, so a FIFO swap is caught without
// ever blocking.
//
// ORACLE (A3 / D13): an entry that is regular at Stat time but a FIFO at
// Open time (a test seam swaps regular→FIFO between Stat and Open in the
// OLD code; in the NEW code the open itself is non-blocking and the
// check happens on the opened file) must be refused within a short
// deadline and never hang. pre-fix: the gate Open() blocks forever
// waiting for a writer; the test fails via its 2 s watchdog. GREEN:
// refused immediately as a permission error.
//go:build unix

package tools

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// grepGateFIFOSwapHook, setGrepGateFIFOSwapHook and runGrepGateFIFOSwapHook
// are defined in grep_scope.go (production file) so refuseNonRegular and
// refuseNonRegularViaOpen can call them from the production build.
// installGrepGateFIFOSwapHook installs a one-shot hook that, when it
// fires, removes target and creates a FIFO at the same path (replacing
// the regular file with a FIFO between Stat and Open in the OLD code;
// the test asserts the kind check refuses the FIFO without blocking).
func installGrepGateFIFOSwapHook(t *testing.T, target string) {
	t.Helper()
	var called atomic.Bool
	restore := setGrepGateFIFOSwapHook(func(name string) {
		if !called.CompareAndSwap(false, true) {
			return
		}
		if err := os.Remove(target); err != nil {
			t.Errorf("grepGateFIFOSwapHook: remove %q: %v", target, err)
			return
		}
		if err := syscall.Mkfifo(target, 0o600); err != nil {
			t.Errorf("grepGateFIFOSwapHook: mkfifo %q: %v", target, err)
		}
	})
	t.Cleanup(restore)
}

// TestGrepGateFS_Open_FIFOSwapNeverHangs is the A3 RED/GREEN test:
// an entry that is regular at Stat time but a FIFO at Open time must
// be refused within a short deadline and never hang.
func TestGrepGateFS_Open_FIFOSwapNeverHangs(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "entry.txt")
	if err := os.WriteFile(target, []byte("needle\n"), 0o600); err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	installGrepGateFIFOSwapHook(t, target)

	fsys := os.DirFS(dir)
	policy := fspolicy.FSPolicy{}
	g := grepGateFS{
		fsys:   carveOutFS{fsys: fsys, root: dir, policy: policy},
		raw:    fsys,
		root:   dir,
		policy: policy,
	}

	done := make(chan error, 1)
	go func() { _, err := g.Open("entry.txt"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("A3: gate Open accepted a FIFO — should refuse non-regular entries")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("A3: gate Open hung on the FIFO (no non-blocking open + kind check) — D13 violated")
	}
}