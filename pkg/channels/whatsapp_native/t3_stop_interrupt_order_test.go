//go:build (linux || darwin || windows) && !mipsle

package whatsapp

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// TestStop_InterruptionNoticeIsPublishedBeforeStopReturns pins the order a
// disable/reload Stop of a running pairing must keep (train 3, CI race
// TestStopHandle_EndsOnlyItsOwnLoop): the "interrupted, press Retry" notice is
// handed to the observer BEFORE the stop is reported finished. If the stop's
// completion (resource.done) were released first, the caller of Stop could
// move on — admit a Retry, restart the connector — before the notice arrives,
// and the chat's last word on the attempt would stay "Stopping this pairing
// has not finished yet", although the stop did finish.
//
// Deterministic: the observer checks, at the moment the interrupted notice
// arrives, whether the stop's completion signal is already closed.
func TestStop_InterruptionNoticeIsPublishedBeforeStopReturns(t *testing.T) {
	f := newNativeFixture(t, t.TempDir())
	var (
		mu              sync.Mutex
		notices         int
		doneAtNotice    bool
		lifecycleAbsent bool
	)
	f.ch.SetPairingAttemptObserver(func(attemptID string, status channels.PairingStatus, qr, message string) {
		f.mu.Lock()
		f.frames = append(f.frames, nativeFrame{attemptID, status, qr, message})
		f.mu.Unlock()
		if message != channels.PairingInterruptedMessage {
			return
		}
		f.ch.mu.Lock()
		resource := f.ch.lifecycle
		f.ch.mu.Unlock()
		mu.Lock()
		defer mu.Unlock()
		notices++
		if resource == nil {
			lifecycleAbsent = true
			return
		}
		select {
		case <-resource.done:
			doneAtNotice = true
		default:
		}
	})

	b := f.admitReady(t, channels.PairingAdmissionConnect, ownerA)
	_, err := f.ch.beginAttempt(b)
	require.NoError(t, err)

	require.NoError(t, f.ch.Stop(context.Background()), "a disable or reload stops the channel")

	got, ok := f.store.Attempt("whatsapp", b)
	require.True(t, ok)
	require.Equal(t, channels.PairingOutcomeInterrupted, got.Outcome)

	mu.Lock()
	require.Equal(t, 1, notices, "exactly one interrupted notice, already delivered when Stop returned")
	require.False(t, lifecycleAbsent, "instrument: the stopped lifecycle is still captured at the notice")
	require.False(t, doneAtNotice, "the stop was reported finished before its interrupted notice was published")
	mu.Unlock()

	frames := f.recorded()
	require.Equal(t, []nativeFrame{
		{b, channels.PairingStatusError, "", channels.PairingStopPendingMessage},
		{b, channels.PairingStatusError, "", channels.PairingInterruptedMessage},
	}, frames, "the stopping notice, then the interrupted notice, both before Stop returns")
}
