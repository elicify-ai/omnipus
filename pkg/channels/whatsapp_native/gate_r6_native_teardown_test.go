//go:build !mipsle && !netbsd && !(freebsd && arm)

package whatsapp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// runLoop starts the channel's REAL QR forwarding loop (runQRLoop) for
// attemptID on a platform QR channel the test feeds, with a cancellable
// runtime, exactly as startPairing does after the platform calls.
func (f *nativeFixture) runLoop(t *testing.T, attemptID string) chan whatsmeow.QRChannelItem {
	t.Helper()
	qr := make(chan whatsmeow.QRChannelItem, 8)
	f.ch.mu.Lock()
	if f.ch.runCtx == nil || f.ch.runCtx.Err() != nil {
		f.ch.runCtx, f.ch.runCancel = context.WithCancel(context.Background())
	}
	f.ch.mu.Unlock()
	f.ch.wg.Add(1)
	go f.ch.runQRLoop(attemptID, qr, false)
	return qr
}

func (f *nativeFixture) waitFrames(t *testing.T, n int) []nativeFrame {
	t.Helper()
	require.Eventually(t, func() bool { return len(f.recorded()) >= n }, 5*time.Second, 5*time.Millisecond)
	return f.recorded()
}

// TestQRLoop_TheRealLoopStampsLateEventsWithItsOwnAttempt is gate r5 CHECK
// R5-F1 (M8) through the REAL forwarding loop: attempt A's loop still holds
// platform items after A ended and the same chat's Retry B became the
// channel's current attempt. A's late code is forwarded as A's, and A's late
// terminal item cannot end B.
func TestQRLoop_TheRealLoopStampsLateEventsWithItsOwnAttempt(t *testing.T) {
	f := newNativeFixture(t, t.TempDir())
	a := f.admitReady(t, channels.PairingAdmissionConnect, ownerA)
	_, err := f.ch.beginAttempt(a)
	require.NoError(t, err)
	qrA := f.runLoop(t, a)
	qrA <- whatsmeow.QRChannelItem{Event: "code", Code: "QR-A"}
	qrA <- whatsmeow.QRChannelItem{Event: "timeout"}
	f.waitFrames(t, 2)

	b := f.admitReady(t, channels.PairingAdmissionRetry, ownerA)
	_, err = f.ch.beginAttempt(b)
	require.NoError(t, err)
	qrA <- whatsmeow.QRChannelItem{Event: "code", Code: "QR-A-LATE"}
	qrA <- whatsmeow.QRChannelItem{Event: "timeout"}
	qrA <- whatsmeow.QRChannelItem{Event: "code", Code: "QR-A-MARK"} // drain marker
	frames := f.waitFrames(t, 4)

	require.Equal(t, []nativeFrame{
		{a, channels.PairingStatusCode, "QR-A", ""},
		{a, channels.PairingStatusTimeout, "", "the QR code expired before it was scanned"},
		{a, channels.PairingStatusCode, "QR-A-LATE", ""},
		{a, channels.PairingStatusCode, "QR-A-MARK", ""},
	}, frames, "every item of A's loop is A's; A's late timeout ends nothing")
	require.True(t, f.store.IsCurrent("whatsapp", b), "B keeps running")
}

// TestStopHandle_ATimedOutTeardownIsNotStoppedUntilItsWorkJoined is gate r5
// CR-T2-R5-02 / T2-R5-S1 / R5-A02 / T2-SF-14 residual and pr-test R4-T2 (M6,
// M7), on the real native handle: the attempt's QR loop is held inside its
// callback past the Stop's deadline. The first teardown reports failure; a
// retry of the SAME handle while the work is still held also fails — never a
// fabricated success — and no successor is admitted. Once the work is
// released, the retry joins it and succeeds, and only then is it stopped.
func TestStopHandle_ATimedOutTeardownIsNotStoppedUntilItsWorkJoined(t *testing.T) {
	f := newNativeFixture(t, t.TempDir())
	held := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWork := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseWork) // runs before the fixture's Stop, so a failed assertion cannot hang it
	f.ch.SetPairingAttemptObserver(func(attemptID string, status channels.PairingStatus, qr, message string) {
		f.mu.Lock()
		f.frames = append(f.frames, nativeFrame{attemptID, status, qr, message})
		f.mu.Unlock()
		if qr == "QR-HOLD" {
			close(held)
			<-release // the platform callback is stuck
		}
	})
	a := f.admitReady(t, channels.PairingAdmissionConnect, ownerA)
	_, err := f.ch.beginAttempt(a)
	require.NoError(t, err)
	qr := f.runLoop(t, a)
	qr <- whatsmeow.QRChannelItem{Event: "code", Code: "QR-HOLD"}
	<-held

	stop, ok := f.store.BeginStop(f.store.OwnedBy("chat-a")[0])
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	err = stop.Cancel(ctx)
	cancel()
	require.Error(t, err, "the first teardown did not finish")
	_, finished := f.store.FinishStop("whatsapp", a, err)
	require.False(t, finished)

	retry, ok := f.store.BeginStop(f.store.OwnedBy("chat-a")[0])
	require.True(t, ok, "the stuck attempt is selected again")
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	err = retry.Cancel(ctx)
	cancel()
	require.Error(t, err, "a retry while the work is still held must not report success")
	_, finished = f.store.FinishStop("whatsapp", a, err)
	require.False(t, finished)
	phase, _ := f.store.Phase("whatsapp", a)
	require.Equal(t, channels.PairingPhaseStopping, phase, "still not stopped")
	_, err = f.store.Admit("whatsapp", channels.PairingAdmissionConnect, &channels.PairingOwner{SessionID: "chat-b", RequestID: "req-b"})
	require.ErrorIs(t, err, channels.ErrPairingConflict, "no successor over the undisposed client")

	releaseWork()
	again, ok := f.store.BeginStop(f.store.OwnedBy("chat-a")[0])
	require.True(t, ok)
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	err = again.Cancel(ctx)
	cancel()
	require.NoError(t, err, "the retry joined the released work")
	done, finished := f.store.FinishStop("whatsapp", a, nil)
	require.True(t, finished)
	require.Equal(t, channels.PairingOutcomeStopped, done.Outcome)
}

// TestStopHandle_CancelsAndJoinsARunningLoop is the successful-cancel control
// (pr-test R4-T2): a running QR loop's own Stop handle cancels it and returns
// only after the loop exited; the channel is no longer running.
func TestStopHandle_CancelsAndJoinsARunningLoop(t *testing.T) {
	f := newNativeFixture(t, t.TempDir())
	a := f.admitReady(t, channels.PairingAdmissionConnect, ownerA)
	_, err := f.ch.beginAttempt(a)
	require.NoError(t, err)
	exited := make(chan struct{})
	f.ch.mu.Lock()
	f.ch.runCtx, f.ch.runCancel = context.WithCancel(context.Background())
	f.ch.mu.Unlock()
	qr := make(chan whatsmeow.QRChannelItem)
	f.ch.wg.Add(1)
	go func() {
		f.ch.runQRLoop(a, qr, false)
		close(exited)
	}()
	stop, ok := f.store.BeginStop(f.store.OwnedBy("chat-a")[0])
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, stop.Cancel(ctx))
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("the handle did not end the running loop")
	}
	require.False(t, f.ch.IsRunning())
}
