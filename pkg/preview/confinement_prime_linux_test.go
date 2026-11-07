//go:build linux

package preview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// primeKernelSandbox boots a real Landlock backend for this process, the way
// the gateway boot does, so hooksFor spawns the launcher from a restricted
// thread. Nothing else in this package primes it: without this call every
// process-edge test here runs its converter unconfined
// ("the Linux kernel sandbox is not enforcing") and any confinement assertion
// would be vacuous. The rows that must see real confinement call this first;
// it fails the test when Landlock is unavailable — it never skips.
func primeKernelSandbox(t *testing.T) {
	t.Helper()
	if lb := sandbox.CurrentLinuxBackend(); lb != nil && lb.EnforcingChildren() {
		return
	}
	lb, ok := sandbox.NewLinuxBackend()
	require.True(t, ok,
		"BLOCKED: Landlock is unavailable on this kernel — required by this row; an unconfined converter proves nothing (spec §3.4 D1, FR-051)")
	// Apply registers the backend as the process-wide current one and marks
	// it enforcing; hooksFor builds each spawn's own policy from the call's
	// confinement, so this render never reaches a converter.
	base := filepath.Join(t.TempDir(), "prime")
	require.NoError(t, os.MkdirAll(base, 0o700))
	require.NoError(t, lb.Apply(sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{{Path: base, Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute}},
	}), "instrument: the priming Apply must succeed so hooksFor finds an enforcing backend")
	require.True(t, lb.EnforcingChildren(),
		"instrument: the primed backend must report enforcing children, or hooksFor discloses an unconfined run")
}
