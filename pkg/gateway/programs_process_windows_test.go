//go:build windows

package gateway

import "testing"

func pgAssertCheckProcessGone(t *testing.T, _ string) {
	t.Helper()
	// S-06's state, exact timeout message and bound are asserted by the caller.
	// Unix pgid probing is not Windows process-tree certification (H-17).
	t.Fatal("BLOCKED: Windows check-process tree termination probe is not implemented in this test harness — required by S-06/H-17; state/message timing alone is not process cleanup evidence")
}
