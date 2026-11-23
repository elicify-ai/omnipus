//go:build linux

package preview

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// t131ConverterFileOpenCount watches the real executable's inode before any
// call, without a production hook, executable replacement or sampling goroutine.
// An exec opens that file; the direct-exec control in the cached-link row proves
// this instrument sees it without a fingerprint read. Verification reads also
// count: OF-4 forbids opening anything inside the refused root, so zero opens is
// deliberately stronger than zero starts. Events may coalesce; only a positive
// delta and then no delta are asserted, never an exact process count.
//
// Renaming an ancestor preserves this watch. The caller proves the executable
// inode survived its root move; a lost watch or queue overflow fails loudly.
func t131ConverterFileOpenCount(t *testing.T, executable string) func() int {
	t.Helper()
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	require.NoError(t, err, "R3-F1 instrument: create the real converter file watch")
	t.Cleanup(func() { _ = unix.Close(fd) })
	wd, err := unix.InotifyAddWatch(fd, executable, unix.IN_OPEN|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF)
	require.NoError(t, err, "R3-F1 instrument: arm the watch before preview 1")
	count := 0
	return func() int {
		t.Helper()
		var buffer [4096]byte
		for {
			n, readErr := unix.Read(fd, buffer[:])
			if errors.Is(readErr, unix.EINTR) {
				continue
			}
			if errors.Is(readErr, unix.EAGAIN) {
				return count
			}
			require.NoError(t, readErr, "R3-F1 instrument: cannot certify no opens after a read failure")
			require.Positive(t, n, "R3-F1 instrument: unexpected watch EOF")
			for offset := 0; offset < n; {
				var event unix.InotifyEvent
				header, decodeErr := binary.Decode(buffer[offset:n], binary.NativeEndian, &event)
				require.NoError(t, decodeErr, "R3-F1 instrument: incomplete kernel event")
				require.Zero(t, event.Mask&(unix.IN_Q_OVERFLOW|unix.IN_IGNORED|unix.IN_UNMOUNT|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF),
					"R3-F1 instrument: lost watch or events; no-start proof is invalid")
				require.Equal(t, wd, int(event.Wd), "R3-F1 instrument: event belongs to the watched converter")
				offset += header + int(event.Len)
				require.LessOrEqual(t, offset, n, "R3-F1 instrument: incomplete event name")
				if event.Mask&unix.IN_OPEN != 0 {
					count++
				}
			}
		}
	}
}
