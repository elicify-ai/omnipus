package programs

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// B0: innocent output must advance while open; at most seven undecided bytes.
// All reader/writer operations are owned, errors propagated, and fatal cleanup
// releases both pipe ends and the writer gate before joining both goroutines.
func testMaskingLivePipe(t *testing.T) {
	t.Helper()
	pr, pw := io.Pipe()
	w := NewMaskingWriter(pw, []string{redSentinel})
	var mu sync.Mutex
	var observed bytes.Buffer
	readDone, writeDone := make(chan struct{}), make(chan struct{})
	var readErr, writeErr error
	complete := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(complete) }) }
	firstWritten := make(chan error, 1)
	prefix := strings.Repeat("a", 64*1024) // long enough to reject whole-stream buffering
	go func() {
		defer close(readDone)
		buf := make([]byte, 1024)
		for {
			n, err := pr.Read(buf)
			mu.Lock()
			observed.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					readErr = err
				}
				return
			}
		}
	}()
	go func() {
		defer close(writeDone)
		defer pw.Close()
		_, err := w.Write([]byte(prefix + redSentinel[:10]))
		firstWritten <- err
		if err == nil {
			<-complete
			_, err = w.Write([]byte(redSentinel[10:]))
		}
		writeErr = errors.Join(err, w.Close())
	}()
	join := func(done <-chan struct{}, label string) {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("%s did not join after both pipe ends closed", label)
		}
	}
	t.Cleanup(func() {
		release()
		_ = pr.CloseWithError(io.ErrClosedPipe)
		_ = pw.CloseWithError(io.ErrClosedPipe)
		join(writeDone, "masking writer")
		join(readDone, "pipe reader")
	})
	snapshot := func() string { mu.Lock(); defer mu.Unlock(); return observed.String() }
	select {
	case err := <-firstWritten:
		require.NoError(t, err, "first Write must not wait for stream close")
	case <-time.After(5 * time.Second):
		t.Fatal("first Write blocked with an active pipe reader before Close")
	}
	require.Eventually(t, func() bool { return len(snapshot()) >= len(prefix)-7 }, 5*time.Second, 10*time.Millisecond,
		"while open, the long innocent prefix must flow, with no more than seven undecided bytes")
	mid := snapshot()
	require.True(t, strings.HasPrefix(prefix, mid) || strings.HasPrefix(mid, prefix), "innocent prefix remains byte-identical")
	requireNoLeak(t, mid, redSentinel)
	release()
	join(writeDone, "masking writer")
	join(readDone, "pipe reader")
	// Closed done channels synchronize the error fields with this goroutine.
	select {
	case <-writeDone:
		require.NoError(t, writeErr)
	default:
		t.Fatal("writer not joined")
	}
	select {
	case <-readDone:
		require.NoError(t, readErr)
	default:
		t.Fatal("reader not joined")
	}
	require.Equal(t, prefix+"[FILTERED]", snapshot(), "one maximal protected run across two writes")
}
