package whatsapp

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// The logger's external clock callback is a passive stage acknowledgement:
// reconnectWithBackoff logs immediately AFTER capturing its concrete client
// and BEFORE calling Connect. No native method/return/lock is replaced. Holding
// this callback leaves the logger's shared read lock available to Stop's logs.
// This precise window is necessary: holding the network handshake instead
// would hold whatsmeow's socket lock, so Disconnect would not yet reach its
// bounded native join. The clock returns its original value after release.
func fix1HoldCapturedReconnect(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	original := zerolog.TimestampFunc
	held, resume := make(chan struct{}), make(chan struct{})
	var claimed atomic.Bool
	var once sync.Once
	release := func() { once.Do(func() { close(resume) }) }
	zerolog.TimestampFunc = func() time.Time {
		var pcs [32]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		for {
			frame, more := frames.Next()
			if strings.HasSuffix(frame.Function, "(*WhatsAppNativeChannel).reconnectWithBackoff") && claimed.CompareAndSwap(false, true) {
				close(held)
				<-resume
				break
			}
			if !more {
				break
			}
		}
		return original()
	}
	t.Cleanup(func() { release(); zerolog.TimestampFunc = original })
	return held, release
}

// T6 / founder Q9=A / DESIGN §3: a native reconnect already captured a real
// client. Stop times out on this tracked work; when the external successful
// connection is released, joining the goroutine is NOT final client disposal.
// The remote fixture completes real WebSocket/Noise handshakes (not nil client,
// QR-only, closed-port failure, or a fake Connect that simply returns nil).
func TestNativeStop_SuccessfulLateReconnectDisposesItsCapturedClient(t *testing.T) {
	server := fix1NewNoiseServer(t)
	f := newNativeFixture(t, t.TempDir())
	var captured *whatsmeow.Client
	previous := nativeClientHook
	nativeClientHook = func(c *whatsmeow.Client) {
		captured = c
		c.EnableAutoReconnect = false // exercise Omnipus's tracked native reconnect, not a second library retry loop
		c.MessengerConfig = &whatsmeow.MessengerConfig{BaseURL: server.server.URL, WebsocketURL: "ws" + strings.TrimPrefix(server.server.URL, "http")}
	}
	t.Cleanup(func() { nativeClientHook = previous })
	a := f.admitReady(t, channels.PairingAdmissionConnect, ownerA)
	require.NoError(t, f.ch.Start(channels.WithPairingStart(context.Background(), a, false)))
	require.NotNil(t, captured)
	require.True(t, captured.IsConnected(), "instrument: the initial real native client successfully connected")
	var remote interface{ CloseNow() error }
	select {
	case ws := <-server.connected:
		remote = ws
	case err := <-server.errors:
		t.Fatalf("HARNESS: real initial Noise handshake failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: initial authenticated connection was not acknowledged")
	}
	held, release := fix1HoldCapturedReconnect(t)
	joined := make(chan struct{})
	qrContended, qrAllowed := make(chan struct{}, 1), make(chan struct{})
	var qrOnce, unlockOnce sync.Once
	allowQR := func() { qrOnce.Do(func() { close(qrAllowed) }) }
	f.store.SetOrderContendedHookForTest(func(id string) {
		var stack [32768]byte
		n := runtime.Stack(stack[:], false)
		if id == "whatsapp" && strings.Contains(string(stack[:n]), "(*PairingAttempts).Complete") {
			qrContended <- struct{}{}
			<-qrAllowed // actual competing QR completion is not yet admitted
		}
	})
	order := f.store.LockChannel("whatsapp")
	unlock := func() { unlockOnce.Do(order.Unlock) }
	t.Cleanup(func() {
		unlock()
		allowQR()
		release()
		f.ch.wg.Wait()
		captured.Disconnect()
		f.store.SetOrderContendedHookForTest(nil)
	})
	require.NoError(t, remote.CloseNow()) // actual remote disconnect reaches the real registered event handler
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: real reconnect did not acknowledge its captured-client stage")
	}
	select {
	case <-qrContended:
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: competing QR completion did not acknowledge its actual held ordering boundary")
	}
	unlock() // QR completion is still held before acquiring; Stop wins this legal ordering.
	active, exists := f.store.Attempt("whatsapp", a)
	require.True(t, exists)
	require.Equal(t, channels.PairingPhaseActive, active.Phase)
	selected, ok := f.store.BeginStop(active)
	require.True(t, ok)
	allowQR() // it can now observe the already-admitted Stop, never race ownership selection
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	err := selected.Cancel(ctx)
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded, "actual tracked reconnect is held beyond this Stop's deadline")
	_, finished := f.store.FinishStop("whatsapp", a, err)
	require.False(t, finished)
	_, err = f.store.Admit("whatsapp", channels.PairingAdmissionConnect, &channels.PairingOwner{SessionID: "chat-b", RequestID: "before-native-join"})
	require.ErrorIs(t, err, channels.ErrPairingConflict, "no admission while the captured work remains held")
	release()
	go func() { f.ch.wg.Wait(); close(joined) }()
	select {
	case <-joined:
	case err := <-server.errors:
		t.Fatalf("HARNESS: late real Noise handshake failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: released native reconnect did not join")
	}
	if captured.IsConnected() {
		// Join the remote receipt as well: the client's Connect may return just
		// before the server consumes its authenticated client-finish message.
		select {
		case <-server.connected:
			require.Equal(t, int32(2), server.handshakes.Load(), "instrument: the leaked late connection really completed a second authenticated handshake")
		case err := <-server.errors:
			t.Fatalf("HARNESS: late authenticated connection failed: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("HARNESS: live late client lacked its real remote handshake receipt")
		}
	}
	t.Logf("successful real Noise handshakes including any late connect: %d", server.handshakes.Load())
	assert.False(t, captured.IsConnected(), "joining late native work must also disconnect/dispose the exact captured real client")
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	err = selected.Cancel(ctx) // same exact handle; never resolve a newer resource by name
	cancel()
	require.NoError(t, err)
	f.store.FinishStop("whatsapp", a, nil)
	terminal, exists := f.store.Attempt("whatsapp", a)
	require.True(t, exists)
	assert.Equal(t, channels.PairingOutcomeStopped, terminal.Outcome)
	assert.False(t, captured.IsConnected(), "stopped/admission is forbidden while the old client remains connected")
}
