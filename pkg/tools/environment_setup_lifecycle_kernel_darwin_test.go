//go:build darwin

package tools

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// installKernelTestBackend installs the process-global Seatbelt boot backend
// for the confinement fixture's INNER process, with the real boot policy
// shape (open model, this fixture's home). Apply stores the backend
// process-globally — which is exactly why the fixture re-execs: the pollution
// dies with the inner process instead of confining other pkg/tools tests'
// non-god-mode children.
func installKernelTestBackend(t *testing.T, home string) {
	t.Helper()
	backend := sandbox.NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}
	require.NoError(t, backend.Apply(
		sandbox.DefaultPolicyForModel(sandbox.FilesystemModelOpen, home, nil, nil, nil, nil)))
}
