//go:build linux

package officepreview

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// PrimeKernelSandbox runs the entire confinement-dependent test in a fresh
// process. Callers must return when it returns false: only the child executes
// the assertions with a real enforcing backend. Irreversible Landlock policy
// and process-wide backend state must never contaminate the parent test runner.
func PrimeKernelSandbox(t *testing.T) bool {
	t.Helper()
	const marker = "OMNIPUS_TEST_OFFICEPREVIEW_LANDLOCK_CHILD"
	if os.Getenv(marker) != t.Name() {
		self, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(self, "-test.v", "-test.count=1", "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
		cmd.Env = append(os.Environ(), marker+"="+t.Name())
		output, runErr := cmd.CombinedOutput()
		t.Logf("isolated Landlock test process:\n%s", output)
		require.NoError(t, runErr, "the isolated process must pass the unchanged confinement assertions")
		require.Contains(t, string(output), "--- PASS: "+t.Name()+" (",
			"instrument: the exact child test must have run, not an empty test selection")
		require.Nil(t, sandbox.CurrentLinuxBackend(), "instrument: only the child installs a backend")
		require.NoError(t, os.WriteFile(filepath.Join(t.TempDir(), "parent-writable"), []byte("parent"), 0600))
		null, openErr := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		require.NoError(t, openErr, "instrument: the parent retains /dev/null access")
		require.NoError(t, null.Close())
		return false
	}

	lb, ok := sandbox.NewLinuxBackend()
	require.True(t, ok,
		"BLOCKED: Landlock is unavailable on this kernel — required by this row; an unconfined converter proves nothing (spec §3.4 D1, FR-051)")
	// Only the isolated gateway harness gets this broad bootstrap policy so it
	// can prepare fixtures and orchestrate children. The real preview hooks
	// still apply each converter's restrictive policy at its locked-thread
	// spawn; the unchanged confinement and output assertions must still pass.
	require.NoError(t, lb.Apply(sandbox.SandboxPolicy{
		FilesystemRules:  []sandbox.PathRule{{Path: "/", Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute}},
		NetworkUnhandled: true,
	}), "instrument: real enforcing Apply succeeds only in the isolated gateway")
	require.True(t, lb.EnforcingChildren(),
		"instrument: the primed backend must enforce the converter policy, or the test is vacuous")
	return true
}
