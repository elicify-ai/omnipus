// websocket_pump_test.go: tests for write pump and event forwarding to the client.

package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The pre-#823 M4-2 tests (droppedFrames counter, 0/10/50ms backoff,
// "connection degraded" warning after 20 drops) were deleted with the
// mechanism itself: founder decision Q5 replaced drop-and-warn with a
// never-dropping per-connection queue that closes a too-far-behind tab with
// 4008 so it catches up. The replacement guarantees are pinned in
// ws_conn_queue_test.go.

// TestWritePumpEnforcesWriteDeadline_TextMessage proves that writePump's
// TextMessage write call (wc.conn.WriteMessage(websocket.TextMessage, msg))
// is bounded by wsWriteWait rather than blocking forever when the client
// stops reading.
//
// BDD:
//
//	Given a WS server connection whose client never reads again,
//	When writePump attempts its first write over the (unbuffered, nobody-
//	  reading) net.Pipe() transport,
//	Then the blocked WriteMessage call returns a deadline error within
//	  wsWriteWait (+ scheduling slack), and writePump's goroutine exits —
//	  it does not hang indefinitely.
//
// Traces to: pkg/gateway/websocket.go writePump (TextMessage branch).
func TestWritePumpEnforcesWriteDeadline_TextMessage(t *testing.T) {
	wc, wpDone := setupBackpressureWS(t)

	payload := make([]byte, 256*1024) // 256 KiB text frame, reused every send

	start := time.Now()
	feederDone := make(chan struct{})
	go func() {
		defer close(feederDone)
		for {
			select {
			case wc.sendCh <- payload:
			case <-wpDone:
				return
			}
		}
	}()

	select {
	case <-wpDone:
		elapsed := time.Since(start)
		assert.LessOrEqualf(t, elapsed, 15*time.Second,
			"writePump must return within wsWriteWait(%s)+slack once the client stops reading, took %s — "+
				"a write deadline that isn't firing means the single writer goroutine can stall forever",
			wsWriteWait, elapsed)
		assert.GreaterOrEqualf(t, elapsed, 5*time.Second,
			"writePump returned after only %s — expected it to actually block until close to "+
				"wsWriteWait(%s) before the deadline fires; a near-instant return suggests the test "+
				"isn't exercising real backpressure (the pipe write never actually blocked) rather than confirming the fix",
			elapsed, wsWriteWait)
	case <-time.After(25 * time.Second):
		t.Fatal("writePump did not return within 25s of a stalled client on the TextMessage path — " +
			"the write deadline is not being enforced (regression: missing SetWriteDeadline before " +
			"wc.conn.WriteMessage(websocket.TextMessage, msg) in writePump)")
	}
	<-feederDone
}

// TestWritePumpEnforcesWriteDeadline_Ping proves that writePump's
// PingMessage write call (wc.conn.WriteMessage(websocket.PingMessage, nil),
// triggered by the nil sentinel on wc.sendCh) is bounded by wsWriteWait
// rather than blocking forever when the client stops reading.
//
// This is the call site most directly implicated in the production bug:
// the keepalive ping is what has to keep firing every wsPingPeriod to beat
// the reverse proxy's idle timeout, and it is exactly the frame that got
// silently starved when an earlier write on the same single writer
// goroutine blocked forever.
//
// setupBackpressureWS's net.Pipe() transport has no OS buffer to overflow,
// so the FIRST ping write attempt blocks immediately — the elapsed time is
// governed purely by wsWriteWait's own deadline, not by how many 2-byte
// ping frames it takes to organically overflow a platform-specific,
// best-effort-shrunk OS buffer (the previous, flakier mechanism — see the
// package doc comment above for the full root-cause account). The feeder
// below still enqueues PURE ping sentinels (no payload mixed in), so the
// write that ultimately blocks and times out is still, specifically,
// writePump's PingMessage branch.
//
// Traces to: pkg/gateway/websocket.go writePump (PingMessage branch).
func TestWritePumpEnforcesWriteDeadline_Ping(t *testing.T) {
	wc, wpDone := setupBackpressureWS(t)

	start := time.Now()
	feederDone := make(chan struct{})
	go func() {
		defer close(feederDone)
		for {
			select {
			case wc.sendCh <- wsPingMsg: // nil sentinel -> PingMessage write in writePump
			case <-wpDone:
				return
			}
		}
	}()

	select {
	case <-wpDone:
		elapsed := time.Since(start)
		assert.LessOrEqualf(t, elapsed, wsWriteWait+15*time.Second,
			"writePump must return within wsWriteWait(%s)+slack once the unbuffered pipe makes the "+
				"first ping write block, took %s", wsWriteWait, elapsed)
		assert.GreaterOrEqualf(t, elapsed, wsWriteWait-3*time.Second,
			"writePump returned after only %s — expected it to actually block until close to "+
				"wsWriteWait(%s) before the deadline fires; a near-instant return suggests the pipe "+
				"write never actually blocked before the feeder started", elapsed, wsWriteWait)
	case <-time.After(wsWriteWait + 30*time.Second):
		t.Fatal("writePump did not return within wsWriteWait+30s while flooded with ping frames against " +
			"a non-reading client over an unbuffered pipe — the ping write deadline is not being enforced " +
			"(regression: missing SetWriteDeadline before wc.conn.WriteMessage(websocket.PingMessage, " +
			"nil) in writePump)")
	}
	<-feederDone
}
