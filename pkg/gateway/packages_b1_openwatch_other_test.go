//go:build !linux

// packages_b1_openwatch_other_test.go: non-Linux counterpart of the B1 drain
// shim. There is no inotify here; the B1 test gates its planted-open proof on
// GOOS and logs instead (the Linux CI leg owns the open proofs).
package gateway

import "testing"

func b1OpenWatchDrain(*testing.T, *openWatch) int { return 0 }
