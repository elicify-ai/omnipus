//go:build linux

package preview

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// primeKernelSandbox runs the entire confinement-dependent test in a fresh
// process. Callers return when it returns false: the child, not the unprimed
// parent, must execute the assertions. Landlock is irreversible, so neither
// Apply nor its process-wide backend state may escape into later tests.
func primeKernelSandbox(t *testing.T) bool {
	t.Helper()
	const marker = "OMNIPUS_TEST_PREVIEW_LANDLOCK_CHILD"
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
		// These are the two resources the contaminated CI process lost.
		require.NoError(t, os.WriteFile(filepath.Join(t.TempDir(), "parent-writable"), []byte("parent"), 0600))
		null, openErr := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		require.NoError(t, openErr, "instrument: the parent retains /dev/null access")
		require.NoError(t, null.Close())
		return false
	}

	lb, ok := sandbox.NewLinuxBackend()
	require.True(t, ok,
		"BLOCKED: Landlock is unavailable on this kernel — required by this row; an unconfined converter proves nothing (spec §3.4 D1, FR-051)")
	// The isolated gateway harness must orchestrate temporary files and child
	// processes. This broad bootstrap policy is NOT a converter policy:
	// hooksFor builds and applies each converter's restrictive policy at its
	// locked-thread spawn. The real attack and in-scratch control assertions
	// still have to succeed; disabling converter confinement fails them.
	require.NoError(t, lb.Apply(sandbox.SandboxPolicy{
		FilesystemRules:  []sandbox.PathRule{{Path: "/", Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute}},
		NetworkUnhandled: true,
	}), "instrument: real enforcing Apply succeeds only in the isolated gateway")
	require.True(t, lb.EnforcingChildren(),
		"instrument: the primed backend must enforce the converter policy, or the test is vacuous")
	return true
}

func TestPreviewKernelSandbox_IsolatedChildDoesNotRestrictParent(t *testing.T) {
	if !primeKernelSandbox(t) {
		return
	}
	require.True(t, sandbox.CurrentLinuxBackend().EnforcingChildren(),
		"instrument: the child executes the test with a real enforcing backend")
}
