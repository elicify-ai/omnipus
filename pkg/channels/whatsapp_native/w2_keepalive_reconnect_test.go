//go:build !mipsle && !netbsd && !(freebsd && arm)

package whatsapp

import (
	"context"
	"net"
	"net/http"
	"net/url"
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

// w2ReconnectingUnderLock reads the reconnecting flag under reconnectMu, the
// lock production writes it under (whatsapp_native.go::reconnectWithBackoff's
// deferred reset, and handleKeepAliveTimeout's claim). An unlocked test read
// has no happens-before edge to that write and is a data race under -race.
func w2ReconnectingUnderLock(ch *WhatsAppNativeChannel) bool {
	ch.reconnectMu.Lock()
	defer ch.reconnectMu.Unlock()
	return ch.reconnecting
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
	require.False(t, w2ReconnectingUnderLock(f.ch), "R3: a below-threshold keepalive failure must not leave a reconnect running")
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

// TestKeepAlive_RestoredAfterThresholdStillDialsOnceAndEndsConnected is R3 (d),
// chief 2026-11-07 16:00Z: the library reports KeepAliveRestored after the
// failure already passed whatsmeow.KeepAliveMaxFailTime. One restored event
// arrives while our reconnect is still pending, and another after that dial
// has completed. The channel still dials exactly once (that pending reconnect),
// never a second time, and ends connected rather than stuck.
func TestKeepAlive_RestoredAfterThresholdStillDialsOnceAndEndsConnected(t *testing.T) {
	f, server, client, _ := w2StartLinkedAgainstFakeServer(t)
	require.Equal(t, int32(1), server.handshakes.Load(), "instrument: exactly the initial dial so far")
	held, release := fix1HoldCapturedReconnect(t)
	f.ch.eventHandler(w2KeepAliveTimeout(whatsmeow.KeepAliveMaxFailTime+time.Second, 1))
	w2WaitOurReconnectHeld(t, held)
	f.ch.eventHandler(&events.KeepAliveRestored{})
	select {
	case <-server.connected:
		t.Fatal("KeepAliveRestored while the keepalive reconnect was pending completed a handshake; that dial is not the one still held")
	default:
	}
	require.Equal(t, int32(1), server.handshakes.Load(),
		"R3 (d): KeepAliveRestored while our reconnect is pending must not dial on its own")
	release()
	select {
	case <-server.connected:
	case <-time.After(5 * time.Second):
		t.Fatal("the pending reconnectWithBackoff did not dial after KeepAliveRestored arrived while it was held")
	}
	require.True(t, client.IsConnected(), "R3 (d): the one reconnect dial leaves the client connected")
	f.ch.eventHandler(&events.KeepAliveRestored{})
	select {
	case <-server.connected:
		t.Fatalf("KeepAliveRestored after the reconnect completed dialled again (handshakes=%d)", server.handshakes.Load())
	case <-time.After(5 * time.Second):
	}
	require.Equal(t, int32(2), server.handshakes.Load(),
		"R3 (d): a KeepAliveRestored after the threshold still produces exactly one reconnect dial, and it is the one that was already pending")
	require.True(t, client.IsConnected(), "R3 (d): the channel ends connected")
	require.True(t, f.ch.IsRunning(), "R3 (d): the channel is not left stopped")
	require.False(t, w2ReconnectingUnderLock(f.ch), "R3 (d): the channel is not left stuck in a reconnect")
}

// TestKeepAlive_RepeatedTimeoutsDuringAndAfterReconnectDialExactlyOnce is R3's
// one-reconnect rule under repetition (issue #1687: "exactly one reconnect
// (ours)"). Several KeepAliveTimeout events past whatsmeow.KeepAliveMaxFailTime
// in a row — as keepAliveLoop emits them, one per failed ping — must cause
// exactly ONE dial even though later events arrive with newer LastSuccess
// values while the first reconnect is still pending, and stale events (their
// LastSuccess not newer than the one that triggered) arrive after that
// reconnect completed. The dial while ours is held, and the dial after the
// release, are attributed by ordering as in the other tests; anything else
// dialling is the defect.
func TestKeepAlive_RepeatedTimeoutsDuringAndAfterReconnectDialExactlyOnce(t *testing.T) {
	f, server, client, _ := w2StartLinkedAgainstFakeServer(t)
	require.Equal(t, int32(1), server.handshakes.Load(), "instrument: exactly the initial dial so far")
	held, release := fix1HoldCapturedReconnect(t)
	first := w2KeepAliveTimeout(whatsmeow.KeepAliveMaxFailTime+time.Second, 1)
	f.ch.eventHandler(first)
	w2WaitOurReconnectHeld(t, held)
	// More dead-connection timeouts while the one reconnect is still pending:
	// keepAliveLoop would keep emitting them, each newer than the last. Record
	// the newest timestamp observed: the equality boundary below is against the
	// latest one, not against the first.
	var pendingLatest time.Time
	for n := 2; n <= 4; n++ {
		ev := w2KeepAliveTimeout(whatsmeow.KeepAliveMaxFailTime+time.Second, n)
		pendingLatest = ev.LastSuccess
		f.ch.eventHandler(ev)
	}
	// The same latest timestamp again while the reconnect is still pending: one
	// failed keepalive episode repeats its LastSuccess (keepalive.go keeps
	// lastSuccess until a ping succeeds), so the pending reconnect must absorb
	// it without dialling or restarting.
	f.ch.eventHandler(&events.KeepAliveTimeout{ErrorCount: 7, LastSuccess: pendingLatest})
	select {
	case <-server.connected:
		t.Fatalf("a handshake completed while the one reconnect was held and later timeouts were being dropped (handshakes=%d)", server.handshakes.Load())
	case <-time.After(5 * time.Second):
	}
	require.Equal(t, int32(1), server.handshakes.Load(),
		"R3: repeated past-threshold timeouts during a pending reconnect must not dial; the one reconnect is still held")
	release()
	select {
	case <-server.connected:
	case <-time.After(5 * time.Second):
		t.Fatal("the single reconnectWithBackoff did not dial after the repeated timeouts")
	}
	// The reconnect's bookkeeping (reconnecting reset) happens after its dial
	// receipt; wait for it under the lock so the stale events below can only be
	// dropped by the late-timeout guard, never by a still-claimed reconnect.
	deadline := time.After(5 * time.Second)
	for w2ReconnectingUnderLock(f.ch) {
		select {
		case <-deadline:
			t.Fatal("HARNESS: the reconnect never released the reconnecting claim after its dial")
		default:
			runtime.Gosched()
		}
	}
	// Stale events from the dead connection this reconnect replaced: their
	// LastSuccess is not newer than the one that triggered. Equal tests the
	// guard's boundary; earlier tests a genuinely old event.
	for _, stale := range []*events.KeepAliveTimeout{
		{ErrorCount: 5, LastSuccess: first.LastSuccess},
		{ErrorCount: 6, LastSuccess: first.LastSuccess.Add(-2 * time.Second)},
	} {
		f.ch.eventHandler(stale)
	}
	// Equality with the LATEST observed timestamp after the claim cleared: the
	// same episode's timestamp must not start a second reconnect now that
	// nothing is pending. Correct code drops it as not newer; a mutant that
	// lets the equal boundary through dials again right here.
	f.ch.eventHandler(&events.KeepAliveTimeout{ErrorCount: 8, LastSuccess: pendingLatest})
	select {
	case <-server.connected:
		t.Fatalf("a stale past-threshold timeout after the completed reconnect dialled again (handshakes=%d)", server.handshakes.Load())
	case <-time.After(5 * time.Second):
	}
	require.Equal(t, int32(2), server.handshakes.Load(),
		"R3: repeated timeouts during and after one keepalive reconnect still produce exactly one reconnect dial, and it is ours")
	require.False(t, w2ReconnectingUnderLock(f.ch), "R3: no reconnect is left running after the repeated timeouts")
	require.True(t, client.IsConnected(), "R3: the channel ends connected after the repeated timeouts")
}

// TestReconnect_FailureIsVisibleInTheChannelStatusAndRecoveryClearsIt pins the
// no-silent-swallow rule for our managed reconnect (issue #1687: our loop is
// the single reconnect owner): when a reconnect dial fails for an unexpected
// (non-cancellation) reason, the failure must be visible through the channel's
// EXISTING status surface — the BaseChannel running flag (pkg/channels/base.go
// IsRunning/SetRunning) — which is what this channel already uses to report
// connection state: publishNativeRunning sets it on a successful connect and
// disposeNative clears it on stop, and manager.GetStatus reports it per
// channel. Today the error arm of reconnectWithBackoff only logs, so a broken
// connection keeps reporting running: the RED this test must show. A later
// successful reconnect clears it again.
//
// The unexpected failure is deterministic without touching production code:
// the captured client's proxy points at a closed local port (the same shape as
// the offline fixture), so every reconnect dial is refused while the live
// socket is dropped remotely; SetProxy(nil) — the library's own reset path —
// restores direct dialling for the recovery leg.
func TestReconnect_FailureIsVisibleInTheChannelStatusAndRecoveryClearsIt(t *testing.T) {
	f, server, captured, remote := w2StartLinkedAgainstFakeServer(t)
	require.Equal(t, int32(1), server.handshakes.Load(), "instrument: exactly the initial dial so far")
	require.True(t, f.ch.IsRunning(), "instrument: the connected channel reports running through its existing status surface")
	brokenListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	brokenAddr := brokenListener.Addr().String()
	require.NoError(t, brokenListener.Close())
	brokenProxy, err := url.Parse("http://" + brokenAddr)
	require.NoError(t, err)
	captured.SetProxy(http.ProxyURL(brokenProxy))
	held, release := fix1HoldCapturedReconnect(t)
	require.NoError(t, remote.CloseNow(), "instrument: the remote drop reached the socket")
	w2WaitOurReconnectHeld(t, held)
	release()
	// A refused dial returns quickly. Give the failure a bounded moment to land
	// while the broken endpoint stays broken, then require it visible.
	failureVisible := make(chan struct{})
	go func() {
		for {
			if !f.ch.IsRunning() {
				close(failureVisible)
				return
			}
			runtime.Gosched()
		}
	}()
	select {
	case <-failureVisible:
	case <-time.After(2 * time.Second):
	}
	require.False(t, f.ch.IsRunning(),
		"an unexpected reconnect failure must be visible through the channel's existing status surface (IsRunning — the flag publishNativeRunning sets and manager.GetStatus reports), not swallowed into the log")
	// Recovery: restore direct dialling; the loop's next attempt succeeds and
	// the reported state clears again. The bound covers reconnectWithBackoff's
	// 5s initial backoff plus one localhost handshake.
	captured.SetProxy(nil)
	select {
	case <-server.connected:
	case <-time.After(10 * time.Second):
		t.Fatal("the managed reconnect did not recover after the endpoint returned")
	}
	require.True(t, f.ch.IsRunning(), "a later successful reconnect clears the reported failure")
	select {
	case <-server.connected:
		t.Fatalf("a further reconnect dial completed after recovery (handshakes=%d)", server.handshakes.Load())
	case <-time.After(5 * time.Second):
	}
	require.Equal(t, int32(2), server.handshakes.Load(),
		"exactly one reconnect dial on recovery")
}
