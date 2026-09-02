//go:build darwin

// White-box tests for the macOS platform backend's internal fd/map
// bookkeeping (watch_darwin.go) — findings 6 and 7 of the code review this
// file exists to regression-test. Both defects live entirely inside
// darwinWatcher's own state, unreachable from the platform-agnostic Watcher
// API that watch_test.go otherwise exercises, so this file talks to
// darwinWatcher directly instead.
//
// This file is darwin-only (unlike watch_test.go, which is built for both
// linux and darwin) because darwinWatcher itself only exists under this
// build tag.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// newTestDarwinWatcher builds a darwinWatcher the same way startPlatformWatch
// does, without going through the Watcher/goroutine machinery, so a test can
// call addTree/watchDir/watchFile/forgetSubtree directly and inspect the
// resulting maps synchronously.
func newTestDarwinWatcher(t *testing.T, root string) *darwinWatcher {
	t.Helper()
	kq, err := unix.Kqueue()
	if err != nil {
		t.Fatalf("kqueue: %v", err)
	}
	dw := &darwinWatcher{
		kq:        kq,
		root:      root,
		fdToEntry: make(map[int]watchedEntry),
		relToFd:   make(map[string]int),
		listing:   make(map[string]map[string]dirChild),
	}
	t.Cleanup(dw.closeAll)
	return dw
}

// ---------------------------------------------------------------------------
// Finding 6 — forgetSubtree("") (the collection root itself deleted, renamed
// away, or its volume unmounted) must forget EVERYTHING, not just an entry
// literally named "". prefix := rel + "/" degenerates to "/" when rel == "",
// and no collection-relative path starts with "/", so the unfixed code only
// ever matches the root's own literal "" key.
// ---------------------------------------------------------------------------

func TestDarwinWatcher_ForgetSubtreeOfRoot_ForgetsEverything(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "top.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "child", "nested.md"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	dw := newTestDarwinWatcher(t, root)
	if err := dw.addTree("", nil, nil); err != nil {
		t.Fatalf("addTree: %v", err)
	}

	// PRECONDITION: more than just the root's own watch is registered, so
	// "everything forgotten" cannot pass on a state that was already empty.
	dw.mu.Lock()
	before := len(dw.relToFd)
	dw.mu.Unlock()
	const wantAtLeast = 4 // root dir, "child" dir, "top.md", "child/nested.md"
	if before < wantAtLeast {
		t.Fatalf("precondition failed: only %d watches registered after addTree, want at least %d", before, wantAtLeast)
	}

	dw.forgetSubtree("") // the root-removed/unmounted case (watch_darwin.go ~367)

	dw.mu.Lock()
	relLeft, fdLeft, listingLeft := len(dw.relToFd), len(dw.fdToEntry), len(dw.listing)
	relToFd, fdToEntry := dw.relToFd, dw.fdToEntry
	dw.mu.Unlock()

	if relLeft != 0 || fdLeft != 0 || listingLeft != 0 {
		t.Fatalf("forgetSubtree(\"\") left entries behind (finding 6): relToFd=%v fdToEntry=%v listingCount=%d",
			relToFd, fdToEntry, listingLeft)
	}
}

// ---------------------------------------------------------------------------
// Finding 7 — watchFile re-registering the SAME rel must close the fd it is
// replacing, not just overwrite the map entry pointing at it. The unfixed
// code leaves the old fd open (leaked) and its entry in fdToEntry orphaned
// (unreachable via relToFd, so closeAll's own iteration over fdToEntry is
// the only thing that would ever have found it, and by then it is a second,
// harmless leftover key pointing at a still-open, otherwise-unreferenced
// fd).
// ---------------------------------------------------------------------------

func TestDarwinWatcher_WatchFile_ReRegistrationClosesStaleFD(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "reregistered.md")
	if err := os.WriteFile(abs, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	dw := newTestDarwinWatcher(t, root)
	if err := dw.watchFile("reregistered.md", abs); err != nil {
		t.Fatalf("first watchFile: %v", err)
	}

	dw.mu.Lock()
	firstFD, ok := dw.relToFd["reregistered.md"]
	dw.mu.Unlock()
	if !ok {
		t.Fatal("first watchFile did not register relToFd")
	}

	// Re-register the SAME rel without anything first removing the old
	// mapping — the exact "re-registered without the previous fd being
	// closed" shape finding 7 describes (reachable, per the code's own
	// comment, when watchDir's listDir call fails and files are later
	// re-registered via a subsequent directory-listing diff).
	if err := dw.watchFile("reregistered.md", abs); err != nil {
		t.Fatalf("second watchFile: %v", err)
	}

	dw.mu.Lock()
	secondFD := dw.relToFd["reregistered.md"]
	_, staleStillTracked := dw.fdToEntry[firstFD]
	dw.mu.Unlock()

	if secondFD == firstFD {
		t.Fatalf("re-registration reused the same fd number (%d) — this test cannot distinguish a leak from a no-op; the kernel should not have reissued a still-open fd", firstFD)
	}
	if staleStillTracked {
		t.Fatalf("stale fd %d from the first watchFile call is still tracked in fdToEntry after re-registration — it was never closed (finding 7)", firstFD)
	}

	// Prove the closure actually happened at the OS level, not just in this
	// package's own bookkeeping: closing an already-closed fd must fail.
	if err := unix.Close(firstFD); err == nil {
		t.Fatalf("fd %d closed successfully on this SECOND close call — watchFile never actually closed the stale fd itself, only forgot it in relToFd", firstFD)
	}
}
