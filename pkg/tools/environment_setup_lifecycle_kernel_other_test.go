//go:build !windows && !darwin && !linux

package tools

import "testing"

// installKernelTestBackend on platforms with no kernel sandbox backend:
// skip truthfully — the fixture asserts real kernel denials that nothing
// on such a platform can enforce.
func installKernelTestBackend(t *testing.T, _ string) {
	t.Helper()
	t.Skip("no kernel sandbox backend on this platform")
}
