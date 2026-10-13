//go:build linux

package tools

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

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

// landlockCreateRulesetVersionFlag is LANDLOCK_CREATE_RULESET_VERSION from
// <linux/landlock.h> (1 << 0), declared here on purpose: the oracle below must
// share no constant, helper or code path with pkg/sandbox's detection.
const landlockCreateRulesetVersionFlag = 1

// hostKernelSandboxOracle answers "does this host really have a kernel
// sandbox?" without touching pkg/sandbox: the raw landlock_create_ruleset(2)
// version query. A positive ABI number means Landlock is usable here; any
// errno (ENOSYS, EOPNOTSUPP, a seccomp-blocked container) means it is not.
func hostKernelSandboxOracle() (bool, string) {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, landlockCreateRulesetVersionFlag)
	if errno != 0 {
		return false, fmt.Sprintf("landlock_create_ruleset(VERSION): %v", errno)
	}
	return true, fmt.Sprintf("landlock_create_ruleset(VERSION) = ABI %d", int(abi))
}
