//go:build darwin

package preview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// primeKernelSandbox installs a real Seatbelt backend for this process, the
// way the gateway boot does, so hooksFor wraps the launcher in a real
// profile. Nothing else in this package primes it: without this call every
// process-edge test here runs its converter unconfined
// ("the macOS sandbox is not active") and any confinement assertion would be
// vacuous. The rows that must see real confinement call this first; it fails
// the test when real Seatbelt is unavailable — it never skips.
func primeKernelSandbox(t *testing.T) bool {
	t.Helper()
	if backend := sandbox.CurrentSeatbeltBackend(); backend != nil && backend.Available() {
		return true
	}
	backend := sandbox.NewSeatbeltBackend()
	require.True(t, backend.Available(),
		"BLOCKED: real Seatbelt is unavailable (missing /usr/bin/sandbox-exec or OMNIPUS_SEATBELT_DISABLE set) — required by this row; an unconfined converter proves nothing (spec §3.4 D1, FR-051)")
	// Apply validates, renders and installs the process-wide pointer that
	// hooksFor reads; hooksFor passes each spawn its own policy through
	// ApplyToCmd, so this render never reaches a converter.
	base := filepath.Join(t.TempDir(), "prime")
	require.NoError(t, os.MkdirAll(base, 0o700))
	require.NoError(t, backend.Apply(sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{{Path: base, Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute}},
	}), "instrument: the priming Apply must succeed so hooksFor finds an active backend")
	return true
}
