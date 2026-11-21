//go:build darwin || linux

package preview

import "testing"

func q62Prime(t *testing.T) bool {
	t.Helper()
	return primeKernelSandbox(t)
}
