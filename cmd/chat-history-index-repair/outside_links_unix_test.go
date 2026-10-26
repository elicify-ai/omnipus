//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// repairSetLinkTimes sets an entry's own modification time without following
// a symbolic link (lutimes semantics), like `cp -a` does for links.
func repairSetLinkTimes(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	ts := []unix.Timespec{unix.NsecToTimespec(mtime.UnixNano()), unix.NsecToTimespec(mtime.UnixNano())}
	require.NoError(t, unix.UtimesNanoAt(unix.AT_FDCWD, path, ts, unix.AT_SYMLINK_NOFOLLOW))
}

func repairMakeFIFO(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, unix.Mkfifo(path, 0o600))
}

// repairRunBounded runs the real command with a deadline: opening a FIFO with
// no writer blocks forever, so a hang must fail fast instead of stalling CI.
func repairRunBounded(t *testing.T, binary string, wantExit int, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	require.NoError(t, ctx.Err(), "command must not block on a FIFO; output=%s", b)
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		require.True(t, errors.As(err, &ee), "real command invocation: %v", err)
		exit = ee.ExitCode()
	}
	t.Logf("command phase=%s exit=%d output=%s", args[0], exit, b)
	require.Equal(t, wantExit, exit, "output=%s", b)
	return string(b)
}

// R8b / R2: a named pipe outside sessions/ is recorded without being opened
// (an open with no writer would block); inside sessions/ it is still refused.
func TestHistoryRepairCommand_OutsideSessionFIFORecordedNeverOpened(t *testing.T) {
	binary := repairBinary(t)
	source := repairCanonicalFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(source, "workspaces", "w1"), 0o700))
	repairMakeFIFO(t, filepath.Join(source, "workspaces", "w1", "pipe"))
	out := repairRunBounded(t, binary, 0, "dry-run", "--data-root", source)
	require.Contains(t, out, "refused=0")
	backup, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	repairCopyPreservingLinks(t, source, backup)
	manifest := filepath.Join(t.TempDir(), "backup-manifest.json")
	repairRunBounded(t, binary, 0, "manifest", "--data-root", source, "--backup-root", backup, "--output", manifest)
	_, byPath := repairManifestEntries(t, manifest)
	e, ok := byPath["workspaces/w1/pipe"]
	require.True(t, ok)
	require.Equal(t, "special", e["kind"])
	require.Equal(t, "named_pipe", e["special_type"])
	require.NotContains(t, e, "sha256")
	repairRunBounded(t, binary, 0, "verify", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest)
}

func TestHistoryRepairCommand_SessionsSubtreeFIFOStillRefused(t *testing.T) {
	binary := repairBinary(t)
	source, sid := repairMissingIndexFixture(t)
	repairMakeFIFO(t, filepath.Join(source, "sessions", sid, "pipe"))
	out := repairRunBounded(t, binary, 2, "dry-run", "--data-root", source)
	require.Contains(t, out, "class=unsafe_kind")
}
