package whatsapp

// #1675: before the fix, stopNative cancelled the lifecycle context first and
// only then (on the beginNativeStop goroutine) called client.Disconnect. The
// library's read pump runs on that same context (connectNative passes c.runCtx
// to ConnectContext -> FrameSocket.Connect -> readPump), so the cancel made the
// read pump exit and run FrameSocket.Close, which reads fs.OnDisconnect under
// fs.lock, while our Disconnect -> NoiseSocket.Stop wrote fs.OnDisconnect
// without fs.lock. Run with -race.
//
// The schedule is aligned, not left to chance: the test holds reconnectMu
// (the first lock beginNativeStop takes), so a cancel-first stop parks between
// its cancel and Disconnect. The test waits until the frame socket is closed
// (IsConnected false), then releases the lock so a parked Disconnect lands on
// the read pump's close. The loop repeats the real start/stop so the race
// detector gets its chances.
//
// Second oracle: the library emits events.Disconnected only for a disconnect
// it did not expect, i.e. when the read pump's close ran before Disconnect
// marked the disconnect as expected. Our own stop must never log
// "WhatsApp disconnected, will attempt reconnection".

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

func TestRace1675_StopDisconnectLandsOnReadPumpClose(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "whatsapp.jsonl")
	previousLevel := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	require.NoError(t, logger.EnableFileLogging(logPath))
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(previousLevel) })

	const iterations = 25
	for i := 0; i < iterations; i++ {
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
				t.Fatal("instrument: the stop did not close the library frame socket")
			}
			runtime.Gosched()
		}
		f.ch.reconnectMu.Unlock()
		require.NoError(t, <-cancelled)
		_, finished := f.store.FinishStop("whatsapp", a.AttemptID, nil)
		require.True(t, finished)
	}

	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)
	log := string(raw)
	// Instrument: the log file really records this channel's lifecycle.
	require.Equal(t, iterations, strings.Count(log, "WhatsApp native channel connected"),
		"instrument: every start's connect is in the log file")
	require.Equal(t, 0, strings.Count(log, "WhatsApp disconnected, will attempt reconnection"),
		"our own stop surfaced as an unexpected library disconnect")
}
