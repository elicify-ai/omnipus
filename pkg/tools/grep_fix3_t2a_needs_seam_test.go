// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 fix-round-3 RED pack — T2's first scenario (A3/F3 + N2, Opus
// security-lead review): needs a NEW production test seam that does not
// exist on 5a87aa6bd. Isolated behind this build tag so the default build
// (`-tags goolm,stdjson`, no `fix3seam`) stays green; this file does NOT
// compile until the seam below is added to grep_scope.go. The backend
// developer adds the seam and removes this tag (or folds this test into a
// normal *_test.go file once the seam exists).
//
// # Needed production seam: grepGatePreReopenHook (N2 — the reopen gap in
// the A3 fix)
//
// Finding N2: grepGateFS.Open (grep_scope.go) calls
// refuseNonRegularViaOpen(g.abs(name), name) to verify the entry's kind —
// which itself performs a real os.OpenFile + Stat + Close on the entry —
// and then, on success, calls g.fsys.Open(name) AGAIN: a SEPARATE, fresh
// open through the guarded fsys. The file the kind check just verified is
// discarded; the file the caller actually reads is a brand-new open that
// never inherited the verified kind. A swap landing in the gap between the
// two opens (regular -> FIFO) faces the guarded fsys's OWN Open, which has
// no O_NONBLOCK protection of its own — reintroducing the exact hang A3 was
// written to remove, just moved one open later.
//
// Needed signature (same pattern as the three existing seams in
// grep_scope.go — atomic.Pointer, nil in production, one-shot t.Cleanup
// restore in tests):
//
//	var grepGatePreReopenHook atomic.Pointer[func(name string)]
//	func setGrepGatePreReopenHook(fn func(name string)) (restore func())
//	func runGrepGatePreReopenHook(name string)
//
// Call runGrepGatePreReopenHook(name) in grepGateFS.Open, immediately after
// refuseNonRegularViaOpen returns nil and immediately before
// g.fsys.Open(name).
//
//go:build fix3seam && unix

package tools

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// installFix3PreReopenHook fires once, right before grepGateFS.Open's
// second (guarded-fsys) open — swapping target (a regular file, verified
// regular by the kind check moments earlier) for a FIFO.
func installFix3PreReopenHook(t *testing.T, target string) {
	t.Helper()
	var once sync.Once
	restore := setGrepGatePreReopenHook(func(name string) {
		once.Do(func() {
			if err := os.Remove(target); err != nil {
				t.Errorf("grepGatePreReopenHook: remove %q: %v", target, err)
				return
			}
			if err := syscall.Mkfifo(target, 0o600); err != nil {
				t.Errorf("grepGatePreReopenHook: mkfifo %q: %v", target, err)
			}
		})
	})
	t.Cleanup(restore)
}

// TestFix3RedT2a_GrepGateFS_Open_NoReopen_FIFOSwapAfterKindCheck is T2's
// first scenario (A3/F3 + N2): an entry that IS regular when
// refuseNonRegularViaOpen verifies it, but becomes a FIFO in the gap before
// grepGateFS.Open's own SEPARATE g.fsys.Open(name) reopen. The verified
// file must be the one returned — reopening must not happen at all.
func TestFix3RedT2a_GrepGateFS_Open_NoReopen_FIFOSwapAfterKindCheck(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "entry.txt")
	if err := os.WriteFile(target, []byte("needle\n"), 0o600); err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	installFix3PreReopenHook(t, target)

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
			t.Fatal("N2: gate Open accepted an entry that became a FIFO between the kind check and the reopen — the verified file was discarded and a fresh, unverified open was used instead")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("N2: gate Open hung reopening a swapped-in FIFO — the kind check's own verified, already-open file was discarded in favor of an unverified reopen")
	}
}
