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
