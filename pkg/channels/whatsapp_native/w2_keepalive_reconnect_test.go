//go:build !mipsle && !netbsd && !(freebsd && arm)

package whatsapp

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
)

// Requirement R3 (team-lead, after founder decision W2 A / issue #1687):
// whatsmeow's keepalive reconnect is gated on EnableAutoReconnect
// (keepalive.go::keepAliveLoop). With that flag off, a silent keepalive failure
// only dispatches events.KeepAliveTimeout, which this channel ignores, and the
// library never emits Disconnected on that path. Our reconnectWithBackoff must
// own the case. The threshold is the library constant whatsmeow.KeepAliveMaxFailTime,
// not a copied duration.
//
// Clock: the tests pass LastSuccess themselves. keepAliveLoop compares
// time.Since(lastSuccess) > KeepAliveMaxFailTime and puts that timestamp on the
// event. A LastSuccess already past the constant makes the comparison true
// immediately, so the tests do not sleep for three minutes. The package's log
// clock seam only parks a log line; it does not move this comparison.
//
// Delivery is the real eventHandler Start registered (whatsapp_native.go::Start,
// client.AddEventHandler). The library's dispatchEvent calls that same function.

// w2KeepAliveTimeout builds the event keepAliveLoop would dispatch.
// ago is how long before now the last successful keepalive was.
func w2KeepAliveTimeout(ago time.Duration, errorCount int) *events.KeepAliveTimeout {
	return &events.KeepAliveTimeout{ErrorCount: errorCount, LastSuccess: time.Now().Add(-ago)}
}

// w2WaitOurReconnectHeld waits until reconnectWithBackoff is parked at its own
// log line (before it dials). Failing this wait is the missing-takeover failure:
// the channel did not enter its reconnect.
func w2WaitOurReconnectHeld(t *testing.T, held <-chan struct{}) {
	t.Helper()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("KeepAliveTimeout with LastSuccess older than whatsmeow.KeepAliveMaxFailTime did not enter reconnectWithBackoff")
	}
}

// TestKeepAlive_PastMaxFailTimeDisconnectsAndDialsOnceThroughOurReconnect is R3's
// positive case: one KeepAliveTimeout whose LastSuccess is already past
// whatsmeow.KeepAliveMaxFailTime makes the channel disconnect and dial exactly
// once, through reconnectWithBackoff. Anything that dials while that reconnect
// is held is not ours.
func TestKeepAlive_PastMaxFailTimeDisconnectsAndDialsOnceThroughOurReconnect(t *testing.T) {
	f, server, _, _ := w2StartLinkedAgainstFakeServer(t)
	require.Equal(t, int32(1), server.handshakes.Load(), "instrument: exactly the initial dial so far")
	held, release := fix1HoldCapturedReconnect(t)
	f.ch.eventHandler(w2KeepAliveTimeout(whatsmeow.KeepAliveMaxFailTime+time.Second, 1))
	w2WaitOurReconnectHeld(t, held)
	select {
	case <-server.connected:
		t.Fatal("a handshake completed while reconnectWithBackoff was held; that dial is not ours")
	case <-time.After(5 * time.Second):
	}
	require.Equal(t, int32(1), server.handshakes.Load(),
		"R3: no handshake until our reconnect is released — the library must not dial for a keepalive failure")
	release()
	select {
	case <-server.connected:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnectWithBackoff did not dial after a KeepAliveTimeout past whatsmeow.KeepAliveMaxFailTime")
	}
	select {
	case <-server.connected:
		t.Fatalf("a further reconnect dial completed; exactly one is required (handshakes=%d)", server.handshakes.Load())
	case <-time.After(5 * time.Second):
	}
	require.Equal(t, int32(2), server.handshakes.Load(),
		"R3: a keepalive failure past whatsmeow.KeepAliveMaxFailTime produces exactly one reconnect dial, and it is reconnectWithBackoff's")
}

// TestKeepAlive_BelowMaxFailTimeThenRestoredDoesNotDial is R3's boundary: timeouts
// whose LastSuccess is still inside whatsmeow.KeepAliveMaxFailTime, followed by
// KeepAliveRestored, produce no reconnect dial. The library's own comparison is
// strict greater-than, so one second inside the constant is below the threshold.
func TestKeepAlive_BelowMaxFailTimeThenRestoredDoesNotDial(t *testing.T) {
	f, server, _, _ := w2StartLinkedAgainstFakeServer(t)
	require.Equal(t, int32(1), server.handshakes.Load(), "instrument: exactly the initial dial so far")
	below := whatsmeow.KeepAliveMaxFailTime - time.Second
	f.ch.eventHandler(w2KeepAliveTimeout(below, 1))
	f.ch.eventHandler(w2KeepAliveTimeout(below, 2))
	f.ch.eventHandler(&events.KeepAliveRestored{})
	select {
	case <-server.connected:
		t.Fatal("a KeepAliveTimeout inside whatsmeow.KeepAliveMaxFailTime, then KeepAliveRestored, must not dial")
	case <-time.After(5 * time.Second):
	}
	require.Equal(t, int32(1), server.handshakes.Load(),
		"R3: KeepAliveRestored before whatsmeow.KeepAliveMaxFailTime causes no reconnect dial")
	require.False(t, f.ch.reconnecting, "R3: a below-threshold keepalive failure must not leave a reconnect running")
}

// TestKeepAlive_StopWhileReconnectPendingDoesNotDial is R3's stop rule, and the
// deterministic form of the earlier T3 gap for this path: Stop while the
// keepalive reconnect is pending (held before its dial) returns with no further
// handshake. The refusal is the #1681 pair: nativeStopMark is stored before
// Stop waits, and connectNative dials only when that mark does not name its
// context. The hold is released only after the mark is visible, so the dial
// decision sees the stop.
func TestKeepAlive_StopWhileReconnectPendingDoesNotDial(t *testing.T) {
	f, server, _, _ := w2StartLinkedAgainstFakeServer(t)
	require.Equal(t, int32(1), server.handshakes.Load(), "instrument: exactly the initial dial so far")
	held, release := fix1HoldCapturedReconnect(t)
	f.ch.eventHandler(w2KeepAliveTimeout(whatsmeow.KeepAliveMaxFailTime+time.Second, 1))
	w2WaitOurReconnectHeld(t, held)
	stopped := make(chan error, 1)
	go func() { stopped <- f.ch.Stop(context.Background()) }()
	deadline := time.After(5 * time.Second)
	for f.ch.nativeStopMark.Load() == nil {
		select {
		case <-deadline:
			t.Fatal("HARNESS: Stop did not record nativeStopMark while the keepalive reconnect was held")
		default:
			runtime.Gosched()
		}
	}
	release()
	select {
	case err := <-stopped:
		require.NoError(t, err, "Stop joins the pending keepalive reconnect")
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: Stop did not return after the pending keepalive reconnect was released")
	}
	select {
	case <-server.connected:
		t.Fatalf("a dial completed after Stop returned (handshakes=%d)", server.handshakes.Load())
	case <-time.After(5 * time.Second):
	}
	require.Equal(t, int32(1), server.handshakes.Load(),
		"R3: Stop while the keepalive reconnect is pending produces no dial")
}
