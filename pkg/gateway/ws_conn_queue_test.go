// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainQueue plays writePump's part for a bare wsConn: take frames from
// sendCh until nothing more arrives (the queue's mover keeps refilling it).
func drainQueue(wc *wsConn) [][]byte {
	var out [][]byte
	for {
		select {
		case f := <-wc.sendCh:
			out = append(out, f)
		case <-time.After(100 * time.Millisecond):
			return out
		}
	}
}

// TestConnQueue_NeverDropsBeyondTheHotWindow_FIFO pins founder decision Q5 /
// BE-DESIGN.md §2.1: a frame that does not fit in the connection's send
// window is queued, never dropped, and everything is delivered in the order
// it was enqueued. Before #823 the fourth frame here would have been dropped
// after a 0/10/50ms backoff.
func TestConnQueue_NeverDropsBeyondTheHotWindow_FIFO(t *testing.T) {
	wc := &wsConn{sendCh: make(chan []byte, 2), doneCh: make(chan struct{})}
	start := time.Now()
	for i := 0; i < 100; i++ {
		require.True(t, wc.enqueue([]byte(fmt.Sprintf(`{"n":%d}`, i))), "frame %d must be accepted", i)
	}
	assert.Less(t, time.Since(start), 50*time.Millisecond, "enqueue must never block the producer")
	queued, _ := wc.queuedFrames()
	assert.Equal(t, 98, queued, "everything beyond the 2-slot window waits in the queue")

	got := drainQueue(wc)
	require.Len(t, got, 100, "no frame may be dropped")
	for i, f := range got {
		assert.Equal(t, fmt.Sprintf(`{"n":%d}`, i), string(f), "frames must arrive in enqueue order")
	}
	assert.Equal(t, int32(0), wc.closeCode.Load())
}

// TestConnQueue_TooFarBehind_ClosesWith4008 pins founder decision Q5: a tab
// that falls more than connQueueByteCap behind is disconnected with close
// code 4008 ("catch-up required") so it reconnects and catches up — it is
// never silently thinned out.
func TestConnQueue_TooFarBehind_ClosesWith4008(t *testing.T) {
	wc := &wsConn{sendCh: make(chan []byte, 1), doneCh: make(chan struct{})}
	chunk := []byte(`{"type":"token","content":"` + strings.Repeat("x", 1<<20) + `"}`)
	accepted := 0
	for i := 0; i < 20; i++ {
		if !wc.enqueue(chunk) {
			break
		}
		accepted++
	}
	assert.Less(t, accepted, 20, "the queue must refuse once the byte cap is exceeded")
	assert.GreaterOrEqual(t, accepted, connQueueByteCap/len(chunk), "everything under the cap is accepted")
	assert.Equal(t, int32(wsCloseCatchUpRequired), wc.closeCode.Load(), "a too-far-behind connection is closed with 4008")
	select {
	case <-wc.doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("the connection must be closed after overflowing")
	}
	assert.False(t, wc.enqueue([]byte(`{}`)), "a closed connection accepts nothing further")
}

// TestConnQueue_HoldMode_CatchUpFirstThenLiveInOrder pins BE-DESIGN.md §4.1
// A4/A7 (H10): frames published while an attach is being answered are held
// — errors included, no bypass — and follow the catch-up, in publish order.
func TestConnQueue_HoldMode_CatchUpFirstThenLiveInOrder(t *testing.T) {
	wc := &wsConn{sendCh: make(chan []byte, 64), doneCh: make(chan struct{})}
	wc.startHold()
	require.True(t, wc.enqueue([]byte(`live-1`)))
	require.True(t, wc.enqueue([]byte(`live-error`)))
	require.True(t, wc.direct([]byte(`catch-up-1`)))
	require.NoError(t, wc.directWait(context.Background(), []byte(`catch-up-2`)))
	wc.releaseHold()
	require.True(t, wc.enqueue([]byte(`live-2`)))

	drained := drainQueue(wc)
	order := make([]string, 0, len(drained))
	for _, f := range drained {
		order = append(order, string(f))
	}
	assert.Equal(t, []string{"catch-up-1", "catch-up-2", "live-1", "live-error", "live-2"}, order)
}

// TestConnQueue_DirectWait_PacedBySocket_AndStallTimesOut: an attach's bulk
// catch-up waits for the socket instead of overflowing the queue, and gives
// up (so the caller can close with 4008) when the socket stops moving.
func TestConnQueue_DirectWait_PacedBySocket_AndStallTimesOut(t *testing.T) {
	orig := attachReplayStallTimeout
	attachReplayStallTimeout = 100 * time.Millisecond
	t.Cleanup(func() { attachReplayStallTimeout = orig })

	wc := &wsConn{sendCh: make(chan []byte, 1), doneCh: make(chan struct{})}
	big := []byte(strings.Repeat("y", 1<<20))
	for i := 0; i < 5; i++ { // 1 in sendCh, 4 MiB in the queue: at the soft cap
		require.NoError(t, wc.directWait(context.Background(), big))
	}

	// Nobody reads: the next write waits, then reports a stall.
	err := wc.directWait(context.Background(), big)
	assert.True(t, errors.Is(err, errSendTimeout), "a socket that never drains must end the wait, got %v", err)

	// A reader that drains unblocks the wait.
	done := make(chan error, 1)
	go func() { done <- wc.directWait(context.Background(), big) }()
	time.Sleep(20 * time.Millisecond)
	drainQueue(wc)
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("directWait did not resume once the socket drained")
	}
	assert.Equal(t, int32(0), wc.closeCode.Load(), "pacing must never trip the 4008 cap")
}

// TestConnQueue_RealSocket_ClientNotReading_Gets4008 drives the whole path
// over a real WebSocket: a client that stops reading while a session keeps
// publishing is closed by the server with code 4008, and the close reason
// tells it to catch up. The producer side never blocks meanwhile.
func TestConnQueue_RealSocket_ClientNotReading_Gets4008(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverConn := make(chan *wsConn, 1)
	h := makeMinimalHandler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		wc := newWSConn(conn)
		go h.writePump(wc, "")
		serverConn <- wc
		// Keep the handler alive (reading) until the connection closes.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	client, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() { _ = client.Close() })
	wc := <-serverConn

	// The client reads nothing while ~20 MiB is published to it.
	chunk := []byte(`{"type":"token","content":"` + strings.Repeat("z", 256<<10) + `"}`)
	start := time.Now()
	for i := 0; i < 200 && wc.closeCode.Load() == 0; i++ {
		wc.enqueue(chunk)
	}
	assert.Less(t, time.Since(start), 2*time.Second, "publishing to a stalled client must never block")
	require.Equal(t, int32(wsCloseCatchUpRequired), wc.closeCode.Load(), "the stalled client must be closed with 4008")

	// Now the client reads: it sees whatever was already on the wire, then
	// the 4008 close.
	require.NoError(t, client.SetReadDeadline(time.Now().Add(10*time.Second)))
	for {
		_, _, err := client.ReadMessage()
		if err == nil {
			continue
		}
		var ce *websocket.CloseError
		require.True(t, errors.As(err, &ce), "expected a close frame, got %v", err)
		assert.Equal(t, wsCloseCatchUpRequired, ce.Code)
		assert.Equal(t, wsCloseCatchUpReason, ce.Text)
		return
	}
}
