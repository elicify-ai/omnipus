// browser_ws_writedeadline_test.go — the browser-WS half of the write-deadline
// regression already covered for the chat WS in websocket_writedeadline_test.go.
//
// Root cause (2026-07-31): browser_ws.go's writePump had NO SetWriteDeadline on
// ANY of its writes, while websocket.go's equivalent had had them for some time
// — the same invariant applied to only one of the two sockets. gorilla/websocket
// requires a single writer goroutine, so once a client's TCP receive window
// fills, an unbounded WriteMessage blocks that goroutine forever: no further
// frames AND no further keepalive pings. The peer then hits its own read timeout
// and tears the connection down, surfacing as `close 1006 (abnormal closure)`.
//
// This socket is the more exposed of the two: it carries the high-volume JPEG
// screencast stream, so it is the one most likely to fill a window at all.
//
// Proven behaviourally, mirroring the chat-WS tests: a real server connection is
// driven through writePump while the client never reads after the handshake.
// Without the SetWriteDeadline this hangs until the outer timeout fires (RED);
// with it, writePump returns within wsWriteWait (GREEN).

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

// setupBrowserBackpressureWS mirrors setupBackpressureWS (websocket_writedeadline_test.go)
// for browserWSConn / BrowserWSHandler.writePump.
func setupBrowserBackpressureWS(t *testing.T) (wc *browserWSConn, wpDone chan struct{}) {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	var srvConn *browserWSConn
	connReady := make(chan struct{})
	wpDone = make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if tcpConn, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcpConn.SetWriteBuffer(4096) // best-effort
		}
		srvConn = &browserWSConn{conn: conn, sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
		close(connReady)

		// writePump touches no BrowserWSHandler field — a zero-value receiver
		// keeps this independent of full gateway setup.
		(&BrowserWSHandler{}).writePump(srvConn)
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
		_ = tcpConn.SetReadBuffer(4096) // best-effort
	}
	t.Cleanup(func() { _ = clientConn.Close() })
	// Deliberately no reader goroutine — this is the "client stopped draining"
	// scenario that wedged the pump in production.

	<-connReady
	return srvConn, wpDone
}

// TestBrowserWritePumpEnforcesWriteDeadline_TextMessage proves the browser WS's
// TextMessage write is bounded by wsWriteWait rather than blocking forever.
//
// BDD:
//
//	Given a browser-WS server connection whose client never reads again,
//	When writePump is fed more data than the socket buffers can absorb,
//	Then the blocked write returns a deadline error within wsWriteWait
//	  (+ slack) and writePump's goroutine exits, instead of stalling — which
//	  in production also silently stopped the keepalive ping behind it.
func TestBrowserWritePumpEnforcesWriteDeadline_TextMessage(t *testing.T) {
	wc, wpDone := setupBrowserBackpressureWS(t)

	payload := make([]byte, 256*1024)

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
			"browser writePump must return within wsWriteWait(%s)+slack once the client stops reading, took %s — "+
				"an unbounded write stalls the single writer goroutine, stops the keepalive ping, and the peer "+
				"tears the connection down as close 1006",
			wsWriteWait, elapsed)
	case <-time.After(25 * time.Second):
		t.Fatal("browser writePump did not return within 25s of a stalled client — the write deadline is not " +
			"being enforced (regression: missing SetWriteDeadline before wc.conn.WriteMessage in " +
			"browser_ws.go's writePump)")
	}
	<-feederDone
}

// TestBrowserWritePumpEnforcesWriteDeadline_Ping covers the keepalive ping
// specifically (the nil sentinel on sendCh). This is the call most directly
// implicated: the ping is what has to keep firing to hold the connection open,
// and it sits in the SAME in-order queue as the frames, so a stalled frame
// write starves it.
func TestBrowserWritePumpEnforcesWriteDeadline_Ping(t *testing.T) {
	wc, wpDone := setupBrowserBackpressureWS(t)

	payload := make([]byte, 256*1024)
	start := time.Now()
	feederDone := make(chan struct{})
	go func() {
		defer close(feederDone)
		for {
			// Interleave pings with bulk frames, exactly as production does.
			select {
			case wc.sendCh <- payload:
			case <-wpDone:
				return
			}
			select {
			case wc.sendCh <- nil: // ping sentinel
			case <-wpDone:
				return
			}
		}
	}()

	select {
	case <-wpDone:
		assert.LessOrEqualf(t, time.Since(start), 15*time.Second,
			"browser writePump must return within wsWriteWait(%s)+slack on the ping path", wsWriteWait)
	case <-time.After(25 * time.Second):
		t.Fatal("browser writePump did not return within 25s on the ping path — missing SetWriteDeadline " +
			"before the PingMessage write in browser_ws.go's writePump")
	}
	<-feederDone
}
