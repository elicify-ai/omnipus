// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 — the installer for FR-010's test-only seam: a one-shot hook into
// resolveScopedRoot's "existed at the Stat, gone at the open" race
// (grepScopeStatOpenHook / setGrepScopeStatOpenHook, grep_scope.go), used by
// the read-boundary tests that drive that race deterministically.
package tools

import (
	"os"
	"sync"
	"testing"
)

// installGrepScopeLostHook installs a one-shot hook (FR-010: invoked in
// resolveScopedRoot after container.Stat(subPath) succeeded and before the
// root is opened) that removes target once, synchronously — no sleep, no
// goroutine, no filesystem mock — and restores the previous hook with
// t.Cleanup.
func installGrepScopeLostHook(t *testing.T, target string) {
	t.Helper()
	var once sync.Once
	restore := setGrepScopeStatOpenHook(func(subPath string) {
		once.Do(func() {
			if err := os.RemoveAll(target); err != nil {
				t.Errorf("grepScopeStatOpenHook: remove %q (subPath %q): %v", target, subPath, err)
			}
		})
	})
	t.Cleanup(restore)
}

// installGrepScopeAncestorSwapHook installs a one-shot hook that, when it
// fires, removes dir and replaces it with a symlink to swapTo. The hook
// fires from absoluteGrepRoot after os.Stat(realAbs) succeeded and before
// os.OpenRoot(parent) — exactly the TOCTOU window an attacker who owns the
// mount can drive (Opus security-lead finding A1). The seam is nil in
// production; the cost in tests is one atomic load per absoluteGrepRoot
// call. Restored with t.Cleanup, like installGrepScopeLostHook.
func installGrepScopeAncestorSwapHook(t *testing.T, dir, swapTo string) {
	t.Helper()
	var once sync.Once
	restore := setGrepScopeStatOpenHook(func(subPath string) {
		once.Do(func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Errorf("grepScopeStatOpenHook: remove %q before swap: %v", dir, err)
				return
			}
			if err := os.Symlink(swapTo, dir); err != nil {
				t.Errorf("grepScopeStatOpenHook: symlink %q -> %q: %v", dir, swapTo, err)
			}
		})
	})
	t.Cleanup(restore)
}

// installGrepScopePostOpenSwapHook installs a one-shot hook that fires
// AFTER absoluteGrepRoot's os.OpenRoot has bound its file descriptor
// (Opus security-lead finding A2): removing dir and replacing it with a
// symlink now leaves the bound fd pointing at the original inode, while
// os.Stat(anchor) follows the symlink — the SameFile check the A2 fix
// adds detects the discrepancy and surfaces a lost root (FR-021,
// truncated root_lost). Nil in production; one atomic load per open.
func installGrepScopePostOpenSwapHook(t *testing.T, dir, swapTo string) {
	t.Helper()
	var once sync.Once
	restore := setGrepAbsolutePostOpenHook(func(anchor string) {
		once.Do(func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Errorf("grepAbsolutePostOpenHook: remove %q before swap: %v", dir, err)
				return
			}
			if err := os.Symlink(swapTo, dir); err != nil {
				t.Errorf("grepAbsolutePostOpenHook: symlink %q -> %q: %v", dir, swapTo, err)
			}
		})
	})
	t.Cleanup(restore)
}
