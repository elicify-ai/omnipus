// websocket_writedeadline_test.go — regression tests for the WS
// write-deadline fix in writePump (and sendGenWSFrame).
//
// Root cause (fixed in pkg/gateway/websocket.go): writePump wrote every
// frame — including the 30s keepalive ping — with NO write deadline. The
// chat WS is a long-lived singleton with a single writer goroutine
// (gorilla/websocket's single-writer requirement). When a client drains the
// socket slowly (e.g. the browser main thread is busy) TCP back-pressures
// and wc.conn.WriteMessage(...) can block that single writer goroutine
// indefinitely. Because writePump processes wc.sendCh strictly in order, a
// stuck write also starves every frame queued behind it — including the
// next keepalive ping — so after Fly's ~60s proxy idle timeout the TCP
// connection is reset with no close frame, and the browser reports close
// code 1006.
//
// Fix: every wc.conn.WriteMessage call in writePump (the PingMessage write
// AND the TextMessage write), plus the direct write in sendGenWSFrame, is
// now preceded by wc.conn.SetWriteDeadline(time.Now().Add(wsWriteWait)).
// A stalled write now fails within wsWriteWait instead of hanging forever,
// writePump's error path returns, and the client can reconnect.
//
// These tests prove the fix BEHAVIORALLY: a real gorilla/websocket server
// connection is driven directly through writePump while the client
// deliberately never reads again after the handshake (reproducing "client
// drains the socket slowly / stops draining" backpressure). Removing the
// SetWriteDeadline calls under test makes these tests hang until their own
// bounded outer timeout fires and t.Fatal — i.e. RED without the fix,
// GREEN with it.

package gateway

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupBackpressureWS stands up a real WS server connection (via a
// dedicated httptest.Server + gorilla Upgrader — deliberately NOT going
// through WSHandler.ServeHTTP/auth, since writePump itself touches no
// WSHandler state) and a real WS client connection that completes the
// handshake and then never reads again. It returns the server-side wsConn
// (already handed to a running writePump goroutine) and a channel that is
// closed exactly when that writePump goroutine returns.
//
// Best-effort OS socket buffer shrinking (SetWriteBuffer/SetReadBuffer) is
// applied on both ends so backpressure can be induced with a small, bounded
// amount of data rather than relying on default (potentially multi-MB,
// auto-tuned) kernel buffers; if the platform ignores the hint the tests
// still converge, just with more iterations.
func setupBackpressureWS(t *testing.T) (wc *wsConn, wpDone chan struct{}) {
	t.Helper()

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	var srvConn *wsConn
	connReady := make(chan struct{})
	wpDone = make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if tcpConn, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcpConn.SetWriteBuffer(4096) // best-effort; ignore failure
		}
		srvConn = &wsConn{conn: conn, sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
		close(connReady) // happens-before the receive below (Go memory model)

		// writePump touches no WSHandler field — a zero-value receiver is
		// sufficient and keeps this test independent of full gateway setup.
		(&WSHandler{}).writePump(srvConn)
		close(wpDone)
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	clientConn, resp, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err, "client dial must succeed")
	if resp != nil {
		_ = resp.Body.Close()
	}
	if tcpConn, ok := clientConn.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcpConn.SetReadBuffer(4096) // best-effort; ignore failure
	}
	t.Cleanup(func() { _ = clientConn.Close() })
	// Deliberately no reader goroutine for clientConn from this point on —
	// this is the "client drains the socket slowly / stops draining"
	// scenario from the bug report.

	<-connReady
	return srvConn, wpDone
}

// TestWritePumpEnforcesWriteDeadline_TextMessage proves that writePump's
// TextMessage write call (wc.conn.WriteMessage(websocket.TextMessage, msg))
// is bounded by wsWriteWait rather than blocking forever when the client
// stops reading.
//
// BDD:
//
//	Given a WS server connection whose client never reads again,
//	When writePump is fed more data than the (shrunk) socket buffers can
//	  absorb,
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
				"isn't exercising real backpressure (buffers never filled) rather than confirming the fix",
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
// Because a single ping frame carries no application payload (~2 bytes on
// the wire), the feeder floods many ping sentinels to actually exhaust the
// (shrunk) socket buffers — this test allows a longer, but still bounded,
// outer timeout to accommodate that.
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
		assert.LessOrEqualf(t, elapsed, 25*time.Second,
			"writePump must return within wsWriteWait(%s)+slack even when saturated purely by tiny "+
				"keepalive ping frames, took %s", wsWriteWait, elapsed)
		assert.GreaterOrEqualf(t, elapsed, 3*time.Second,
			"writePump returned after only %s — expected it to actually block until close to "+
				"wsWriteWait(%s) before the deadline fires; a near-instant return suggests the buffers "+
				"were never actually saturated", elapsed, wsWriteWait)
	case <-time.After(45 * time.Second):
		t.Fatal("writePump did not return within 45s while flooded with ping frames against a " +
			"non-reading client — the ping write deadline is not being enforced (regression: missing " +
			"SetWriteDeadline before wc.conn.WriteMessage(websocket.PingMessage, nil) in writePump)")
	}
	<-feederDone
}
