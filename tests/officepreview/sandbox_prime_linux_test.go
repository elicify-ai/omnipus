//go:build linux

package officepreview

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// The follow-up to T1 requires this external primer to isolate the entire test,
// just like the internal preview primer. Its parent-side checks must retain
// temporary-file and /dev/null access and require this exact child test's PASS.
func TestPrimeKernelSandbox_IsolatedChildDoesNotRestrictParent(t *testing.T) {
	if !PrimeKernelSandbox(t) {
		return
	}
	require.True(t, sandbox.CurrentLinuxBackend().EnforcingChildren(),
		"instrument: the child executes the test with a real enforcing backend")
}
