//go:build !windows && !darwin

package tools

import "testing"

// installKernelTestBackend on Linux: no backend install is needed — the
// per-turn Landlock domain is applied at spawn time on the launching thread
// (StartLockedWithPolicy), which is the CRIT-1 call site this fixture guards.
func installKernelTestBackend(t *testing.T, _ string) {
	t.Helper()
}
