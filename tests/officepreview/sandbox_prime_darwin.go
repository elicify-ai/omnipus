//go:build darwin

package officepreview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// PrimeKernelSandbox installs a real Seatbelt backend for this process, the
// way the gateway boot does, so preview spawns run confined regardless of
// test order. Without it the first office-preview test in a process would run
// its converter unconfined and any confinement assertion would be vacuous.
// It fails the test when real Seatbelt is unavailable — it never skips.
// macOS confines only spawned children, so the test continues in this process.
func PrimeKernelSandbox(t *testing.T) bool {
	t.Helper()
	if backend := sandbox.CurrentSeatbeltBackend(); backend != nil && backend.Available() {
		return true
	}
	backend := sandbox.NewSeatbeltBackend()
	require.True(t, backend.Available(),
		"BLOCKED: real Seatbelt is unavailable (missing /usr/bin/sandbox-exec or OMNIPUS_SEATBELT_DISABLE set) — required by this row; an unconfined converter proves nothing (spec §3.4 D1, FR-051)")
	// Apply validates, renders and installs the process-wide pointer the
	// preview hooks read; each spawn carries its own policy, so this render
	// never reaches a converter.
	base := filepath.Join(t.TempDir(), "prime")
	require.NoError(t, os.MkdirAll(base, 0o700))
	require.NoError(t, backend.Apply(sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{{Path: base, Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute}},
	}), "instrument: the priming Apply must succeed so the preview hooks find an active backend")
	return true
}
