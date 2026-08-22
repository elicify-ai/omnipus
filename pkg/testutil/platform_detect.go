package testutil

import (
	"runtime"
	"testing"
)

// SkipOnDarwin skips the test on macOS.
func SkipOnDarwin(tb testing.TB) {
	tb.Helper()
	if runtime.GOOS == "darwin" {
		tb.Skip("skipping: test is not applicable on macOS/Darwin")
	}
}
