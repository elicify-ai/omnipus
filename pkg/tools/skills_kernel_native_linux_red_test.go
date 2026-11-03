//go:build linux && !nogodmode

package tools

import (
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// Called only in the isolated inner test process: Landlock is irreversible.
func skillsInstallNativeBackend(t *testing.T, home string) {
	t.Helper()
	backend, available := sandbox.NewLinuxBackend()
	require.True(t, available, "BLOCKED: Landlock backend unavailable — r15 §6.2 native child proof")
	runtime.LockOSThread()
	require.NoError(t, backend.Apply(sandbox.DefaultPolicyForModel(sandbox.FilesystemModelOpen, home, nil, nil, nil, nil)), "real enforcing boot backend, not a policy-only test")
}
