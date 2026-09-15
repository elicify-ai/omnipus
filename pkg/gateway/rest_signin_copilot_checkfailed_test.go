// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestSignInStatus_CopilotCheckThatCannotRun covers a Check sign-in whose CLI
// run never produced an answer about the login at all: here the binary exists
// but cannot be started.
//
// The wire enum has no state for "the check itself failed" (SignInStatus.yaml:
// not_signed_in | pending | signed_in | expired, no reason field), so the
// answer stays not_signed_in — adding a reason is a contract change, recorded
// for the orchestrator. Two things are fixed without touching the wire:
//
//   - the failure is never CLASSIFIED. It used to be: Go's launch error names
//     the binary's path, and a path containing "401" (a folder such as
//     tools-401) was read as an expired session.
//   - the server log says the check could not run, with the reason, instead of
//     the generic "unrecognised message" warning.
func TestSignInStatus_CopilotCheckThatCannotRun(t *testing.T) {
	if !hasBash() {
		t.Skip("POSIX exec semantics; see #113")
	}
	api, _ := newAuthMethodOnboardingAPI(t)

	// A realistic install folder whose name happens to contain 401.
	dir := filepath.Join(neutralFakeCLIDir(t), "tools-401")
	require.NoError(t, os.Mkdir(dir, 0o700))
	// Executable, found by exec.LookPath, but its interpreter does not exist:
	// the kernel refuses to start it, so there is no exit code and no stderr.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "copilot"),
		[]byte("#!/nonexistent/omnipus-test-interpreter\n"), 0o755))
	t.Setenv("PATH", dir)
	logs := captureSlog(t)

	assert.Equal(t, gen.SignInStatusStateNotSignedIn, copilotStatusState(t, api),
		"a check that could not start says nothing about the login and must never read as expired")
	assert.Contains(t, logs.String(), "could not run",
		"the server log must say the check could not run; logs=%s", logs.String())
	assert.NotContains(t, logs.String(), unrecognisedCopilotMessageLog,
		"a launch failure is not a CLI message and must not be classified; logs=%s", logs.String())
}
