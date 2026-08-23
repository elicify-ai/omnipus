// probe_timeout_test.go — the managed binary's `--version` probe gets a
// realistic budget (2026-08-13, macOS): a freshly-downloaded ~200MB Chrome
// bundle pays Gatekeeper's whole-bundle signature verification on its FIRST
// execution, which exceeded the 5s PATH-candidate timeout and made a healthy
// install report itself corrupt ("remove and retry"). Measured on a 4-core
// Intel MacBook Pro: first probe >5s, immediately-following probe <1s.

package browser

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagedProbeTimeout_IsFarLongerThanPathCandidateTimeout(t *testing.T) {
	require.Greater(t, managedChromiumProbeTimeout, chromiumProbeTimeout,
		"the single no-fallback managed binary must get a longer budget than one of several PATH candidates")
	require.GreaterOrEqual(t, managedChromiumProbeTimeout, 60*time.Second,
		"must comfortably cover macOS Gatekeeper verifying a ~200MB bundle on first exec")
}

// TestProbeChromiumBinary_HonoursSuppliedTimeout proves the budget is really
// the knob (not a constant read inside), using a script that sleeps past the
// short budget but finishes inside the long one — the shape of the macOS
// first-exec case.
func TestProbeChromiumBinary_HonoursSuppliedTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-in is POSIX-only")
	}
	dir := t.TempDir()
	slow := filepath.Join(dir, "slow-chrome")
	require.NoError(t, os.WriteFile(slow, []byte("#!/bin/sh\nsleep 1\necho 'Chrome 1.2.3'\n"), 0o755))

	ok, reason := probeChromiumBinaryWithTimeout(context.Background(), slow, 200*time.Millisecond)
	require.False(t, ok, "a probe shorter than the binary's startup must fail")
	require.Contains(t, reason, "timed out after 200ms",
		"the reason must report the ACTUAL budget used, not a hardcoded constant")

	ok, reason = probeChromiumBinaryWithTimeout(context.Background(), slow, 10*time.Second)
	require.True(t, ok, "the same binary must pass with a realistic budget; got: %s", reason)
	require.Empty(t, reason)
}

// TestProbeChromiumBinary_StillRejectsGenuinelyBrokenBinaries guards the
// thing the longer timeout must NOT weaken: a binary that runs and exits
// non-zero (the Ubuntu snap-stub case probeChromiumBinary exists to catch)
// is still rejected immediately, with its exit status in the reason.
func TestProbeChromiumBinary_StillRejectsGenuinelyBrokenBinaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-in is POSIX-only")
	}
	dir := t.TempDir()
	broken := filepath.Join(dir, "stub-chrome")
	require.NoError(t, os.WriteFile(broken, []byte("#!/bin/sh\nexit 3\n"), 0o755))

	start := time.Now()
	ok, reason := probeChromiumBinaryWithTimeout(context.Background(), broken, managedChromiumProbeTimeout)
	require.False(t, ok)
	require.Contains(t, reason, "binary present but broken")
	require.Less(t, time.Since(start), 10*time.Second,
		"a fast non-zero exit must be rejected immediately, never waited out")
}
