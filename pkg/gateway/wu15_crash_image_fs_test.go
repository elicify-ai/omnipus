//go:build linux

package gateway

// Filesystem-edge contracts for the WU15 crash-image fixture (CHECK 1
// finding F3, 2026-11-08): the walk's vanished-entry tolerance is LIMITED to
// the known ".tmp-*" atomic-write artifacts, and a DURABLE record that
// vanishes mid-walk fails the snapshot loudly instead of being silently
// dropped — before the fix, a record renamed mid-walk (a rotated transcript:
// current.jsonl → <date>.jsonl while only the old name was listed) was
// skipped, and the restored home had lost it at every name.
//
// The inotify watch synchronizes each vanish with the walk opening its first
// sorted file (the 64 MB "!hold" entry, which the walk reads with
// os.ReadFile): IN_OPEN fires mid-walk, the goroutine performs the rename or
// removal while the walk is still running, and the walk then reaches the
// changed entry with a stale listing. No production source or helper is
// replaced.
import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestWU15CrashImage_PreservesStableRecord(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "current.jsonl")
	record := []byte("{\"durable\":true}\n")
	require.NoError(t, os.WriteFile(path, record, 0o600))
	restore := wu15DurableCrashImage(t, home)
	restore()
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, record, got)
}

// TestWU15CrashImage_VanishedDurableFileFailsLoudly: a durable record that
// vanishes mid-walk must ERROR the snapshot (os.ErrNotExist), never be
// skipped. On the pre-fix blanket tolerance this walk returned nil and the
// record was silently lost across its own rename.
func TestWU15CrashImage_VanishedDurableFileFailsLoudly(t *testing.T) {
	home := t.TempDir()
	first := filepath.Join(home, "!hold")
	require.NoError(t, os.WriteFile(first, bytes.Repeat([]byte("h"), 64<<20), 0o600))
	old := filepath.Join(home, "current.jsonl")
	rolled := filepath.Join(home, "2099-01-01.jsonl")
	record := []byte("{\"durable\":true}\n")
	require.NoError(t, os.WriteFile(old, record, 0o600))

	// The rename fires mid-walk; its result is asserted in the instrument
	// cleanup below.
	syncRenameDuringWalk(t, first, old, rolled)

	_, _, err := wu15snapshotDurableHome(home)
	require.Error(t, err, "a vanished durable file must fail the snapshot loudly; skipping it is how a rotated record is silently lost")
	require.ErrorIs(t, err, os.ErrNotExist)
	// And prove the fixture WOULD have been losing the record: the renamed
	// file holds the bytes, but its new name was never in the walk's listing.
	got, readErr := os.ReadFile(rolled)
	require.NoError(t, readErr)
	require.Equal(t, record, got)
}

// TestWU15CrashImage_ToleratesVanishingAtomicTemp: the tolerance still
// covers exactly the population it exists for — a fileutil ".tmp-*" atomic
// write temp enumerated and renamed away mid-walk produces no error.
func TestWU15CrashImage_ToleratesVanishingAtomicTemp(t *testing.T) {
	home := t.TempDir()
	first := filepath.Join(home, "!hold")
	require.NoError(t, os.WriteFile(first, bytes.Repeat([]byte("h"), 64<<20), 0o600))
	temp := filepath.Join(home, ".tmp-ab12cd")
	require.NoError(t, os.WriteFile(temp, []byte("partial"), 0o600))

	syncRemoveDuringWalk(t, first, temp)

	files, _, err := wu15snapshotDurableHome(home)
	require.NoError(t, err, "a vanished .tmp-* atomic-write temp is the one tolerated transient")
	require.NotContains(t, files, ".tmp-ab12cd")
}

// syncRenameDuringWalk arms an IN_OPEN watch on first (the walk's first
// sorted file, opened by os.ReadFile mid-walk) and, on that event, renames
// from→to. The returned channel yields the rename's result.
func syncRenameDuringWalk(t *testing.T, first, from, to string) <-chan error {
	t.Helper()
	return syncDuringWalk(t, first, func() error { return os.Rename(from, to) })
}

// syncRemoveDuringWalk arms the same watch and removes path mid-walk.
func syncRemoveDuringWalk(t *testing.T, first, path string) <-chan error {
	t.Helper()
	return syncDuringWalk(t, first, func() error { return os.Remove(path) })
}

func syncDuringWalk(t *testing.T, first string, act func() error) <-chan error {
	t.Helper()
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	require.NoError(t, err)
	t.Cleanup(func() { _ = unix.Close(fd) })
	_, err = unix.InotifyAddWatch(fd, first, unix.IN_OPEN)
	require.NoError(t, err)
	fired := make(chan error, 1)
	go func() {
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		buf := make([]byte, 4096)
		deadline := time.Now().Add(5 * time.Second)
		for {
			// The Go runtime's async-preemption signals interrupt Poll with
			// EINTR, and a ready event can already have been consumed by
			// another reader (EAGAIN) — both are retried, the deadline still
			// bounds the whole wait, and the inotify watch keeps the event
			// queued until it is actually read.
			_, pollErr := unix.Poll(poll, 1000)
			if pollErr == unix.EINTR {
				continue
			}
			if pollErr != nil {
				fired <- pollErr
				return
			}
			_, readErr := unix.Read(fd, buf)
			if readErr == unix.EAGAIN {
				if time.Now().After(deadline) {
					fired <- os.ErrDeadlineExceeded
					return
				}
				continue
			}
			if readErr != nil {
				fired <- readErr
				return
			}
			fired <- act()
			return
		}
	}()
	t.Cleanup(func() {
		select {
		case err := <-fired:
			require.NoError(t, err, "instrument: the mid-walk action must succeed")
		case <-time.After(4 * time.Second):
			t.Error("instrument: mid-walk action never fired")
		}
	})
	return fired
}
