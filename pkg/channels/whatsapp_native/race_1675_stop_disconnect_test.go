package whatsapp

// #1675 RED: stopNative cancels the lifecycle context first and only then
// (on the beginNativeStop goroutine) calls client.Disconnect. The library's
// read pump runs on that same context (connectNative passes c.runCtx to
// ConnectContext -> FrameSocket.Connect -> readPump), so the cancel makes the
// read pump exit and run FrameSocket.Close, which reads fs.OnDisconnect under
// fs.lock, while our Disconnect -> NoiseSocket.Stop writes fs.OnDisconnect
// without fs.lock. Run with -race.
//
// The schedule is aligned, not left to chance: the test holds reconnectMu
// (the first lock beginNativeStop takes), so the production Cancel runs its
// context cancel and then parks before Disconnect. The test waits until the
// read pump's Close has swapped the connection out (IsConnected false), then
// releases the lock so Disconnect lands on the read pump's close. The race
// detector reports the unsynchronised pair if our Stop wins the library's
// destroyed flag; the loop repeats the real start/stop to give it chances.

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRace1675_StopDisconnectLandsOnReadPumpClose(t *testing.T) {
	for i := 0; i < 25; i++ {
		f, captured := finalNativeStartedResource(t)
		a, exists := f.store.Latest("whatsapp")
		require.True(t, exists)
		selected, ok := f.store.BeginStop(a)
		require.True(t, ok)

		f.ch.reconnectMu.Lock()
		cancelled := make(chan error, 1)
		go func() { cancelled <- selected.Cancel(context.Background()) }()
		deadline := time.Now().Add(5 * time.Second)
		for captured.IsConnected() {
			if time.Now().After(deadline) {
				f.ch.reconnectMu.Unlock()
				t.Fatal("instrument: the lifecycle cancel did not end the library read pump")
			}
			runtime.Gosched()
		}
		f.ch.reconnectMu.Unlock()
		require.NoError(t, <-cancelled)
		_, finished := f.store.FinishStop("whatsapp", a.AttemptID, nil)
		require.True(t, finished)
	}
}
