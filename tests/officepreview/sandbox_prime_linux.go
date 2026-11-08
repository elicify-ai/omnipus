//go:build linux

package officepreview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// PrimeKernelSandbox boots a real Landlock backend for this process, the way
// the gateway boot does, so preview spawns run confined regardless of test
// order. Without it the first office-preview test in a process would run its
// converter unconfined and any confinement assertion would be vacuous. It
// fails the test when Landlock is unavailable — it never skips.
func PrimeKernelSandbox(t *testing.T) {
	t.Helper()
	if lb := sandbox.CurrentLinuxBackend(); lb != nil && lb.EnforcingChildren() {
		return
	}
	lb, ok := sandbox.NewLinuxBackend()
	require.True(t, ok,
		"BLOCKED: Landlock is unavailable on this kernel — required by this row; an unconfined converter proves nothing (spec §3.4 D1, FR-051)")
	// Apply registers the backend as the process-wide current one and marks it
	// enforcing; the preview hooks build each spawn's own policy from the
	// call's confinement, so this render never reaches a converter.
	base := filepath.Join(t.TempDir(), "prime")
	require.NoError(t, os.MkdirAll(base, 0o700))
	require.NoError(t, lb.Apply(sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{{Path: base, Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute}},
	}), "instrument: the priming Apply must succeed so the preview hooks find an enforcing backend")
	require.True(t, lb.EnforcingChildren(),
		"instrument: the primed backend must report enforcing children, or the hooks disclose an unconfined run")
}
