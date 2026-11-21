//go:build linux

// packages_t5_openwatch_linux_test.go: the FR-013 open-watch instrument on
// Linux (inotify IN_OPEN). The non-Linux stub is packages_t5_openwatch_other_test.go.
package gateway

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type openWatch struct{ fd int }

// newOpenWatch watches each path for opens. Events before the call are discarded.
func newOpenWatch(t *testing.T, paths []string) *openWatch {
	t.Helper()
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	require.NoError(t, err)
	t.Cleanup(func() { _ = unix.Close(fd) })
	for _, path := range paths {
		_, err = unix.InotifyAddWatch(fd, path, unix.IN_OPEN)
		require.NoError(t, err)
	}
	w := &openWatch{fd: fd}
	_ = w.drain(t)
	return w
}

func (w *openWatch) requireNoOpens(t *testing.T, msg string) {
	t.Helper()
	require.Zero(t, w.drain(t), msg)
}

// requireProbeSeen expects exactly one open since the last drain: the caller's own probe.
func (w *openWatch) requireProbeSeen(t *testing.T, msg string) {
	t.Helper()
	require.Equal(t, 1, w.drain(t), msg)
}

func (w *openWatch) drain(t *testing.T) int {
	t.Helper()
	buf := make([]byte, 4096)
	opens := 0
	for {
		n, err := unix.Read(w.fd, buf)
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return opens
		}
		require.NoError(t, err)
		off := 0
		for off < n {
			if n-off < unix.SizeofInotifyEvent {
				t.Fatalf("short inotify event")
			}
			mask := binary.LittleEndian.Uint32(buf[off+4 : off+8])
			nameLen := binary.LittleEndian.Uint32(buf[off+12 : off+16])
			off += unix.SizeofInotifyEvent + int(nameLen)
			if mask&unix.IN_OPEN != 0 {
				opens++
			}
		}
	}
}
