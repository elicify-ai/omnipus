//go:build linux

package tools

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// installKernelTestBackend installs the enforcing Landlock boot backend the
// per-turn spawn path requires: restrictCurrentThreadIfNeeded is a no-op
// until a LinuxBackend has completed an enforcing Apply, so without it the
// fixture's child spawns unconfined and the negative probes read "ok". The
// backend installs inside the re-exec'd inner process, so the kernel state
// dies with it instead of confining other tests' non-god-mode children.
func installKernelTestBackend(t *testing.T, home string) {
	t.Helper()
	backend, ok := sandbox.NewLinuxBackend()
	if !ok {
		t.Skip("Landlock unavailable on this host")
	}
	require.NoError(t, backend.Apply(
		sandbox.DefaultPolicyForModel(sandbox.FilesystemModelOpen, home, nil, nil, nil, nil)))
}
