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
			saturateTCPSendPath(tcpConn)     // deterministic, platform-independent backpressure
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

// saturateTCPSendPath actively fills the kernel send/receive buffer pipeline
// between tcpConn and its (deliberately non-reading) peer by writing raw
// bytes directly on the underlying socket — bypassing gorilla/websocket
// entirely — until a short-deadlined Write blocks. This makes the very next
// write issued through writePump block almost immediately, so wsWriteWait's
// own 10s deadline (not an unbounded flood) is what determines how long the
// test takes, regardless of platform-specific default socket buffer sizes.
//
// Root cause this replaces (2026-08-06, `matrix (macos-latest, arm64)`, PR
// #597): TestWritePumpEnforcesWriteDeadline_Ping used to rely SOLELY on
// flooding many 2-byte PingMessage frames through writePump itself to
// organically fill an UNKNOWN-sized OS buffer — the SetWriteBuffer/
// SetReadBuffer(4096) hints above are best-effort and are not guaranteed to
// actually shrink the kernel's buffers on every platform. On macOS CI that
// flood never once blocked within the test's 45s outer bound, producing
// `--- FAIL: TestWritePumpEnforcesWriteDeadline_Ping (45.03s)` with the
// message "missing SetWriteDeadline before wc.conn.WriteMessage
// (websocket.PingMessage, nil) in writePump" — even though writePump's ping
// branch already has, and per `git log -L` has had since commit 7da86809
// (long before this failure), its own SetWriteDeadline immediately before
// that exact call; a scoped local run confirms
// TestWritePumpEnforcesWriteDeadline_Ping passes today at HEAD in ~15s. The
// neighboring TestWritePumpEnforcesWriteDeadline_TextMessage test did NOT
// fail on the same CI run: its 256 KiB payloads saturate any realistic
// buffer in a handful of writes regardless of platform, so it never
// depended on this throughput assumption. Pre-saturating the pipe here
// removes the same platform-variable throughput dependency without diluting
// what the ping test proves: the feeder it drives still enqueues PURE ping
// sentinels (see TestWritePumpEnforcesWriteDeadline_Ping below), so the
// write that ultimately blocks and times out is still, specifically,
// writePump's PingMessage branch. Mixing in large payloads instead (the
// pattern TestBrowserWritePumpEnforcesWriteDeadline_Ping already uses) was
// considered and rejected here: browser_ws.go's writePump has a single
// SetWriteDeadline call shared by both branches, so a large payload
// exercises the same call a ping would; websocket.go's writePump (this
// file) has TWO SEPARATE SetWriteDeadline calls, one per branch, so a test
// that always blocks on a large payload instead of a ping could pass even
// if a regression removed only the ping branch's own deadline call.
func saturateTCPSendPath(tcpConn *net.TCPConn) {
	_ = tcpConn.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
	chunk := make([]byte, 128*1024)
	for i := 0; i < 128; i++ { // up to 16 MiB — far beyond any realistic default socket buffer
		if _, err := tcpConn.Write(chunk); err != nil {
			break // pipe is now full; deadline fired as intended
		}
	}
	_ = tcpConn.SetWriteDeadline(time.Time{}) // clear before handing off to writePump/gorilla
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
// setupBackpressureWS pre-saturates the connection's send/receive pipeline
// (saturateTCPSendPath) before this test's feeder ever runs, so the FIRST
// ping write attempt blocks almost immediately — the elapsed time is
// governed by wsWriteWait's own deadline, not by how many 2-byte ping
// frames it takes to organically overflow a platform-specific, best-effort-
// shrunk OS buffer (the previous mechanism, which never once blocked within
// 45s on macOS CI — see saturateTCPSendPath's doc comment for the full
// root-cause account). The feeder below still enqueues PURE ping sentinels
// (no payload mixed in), so the write that ultimately blocks and times out
// is still, specifically, writePump's PingMessage branch.
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
			"writePump must return within wsWriteWait(%s)+slack once the pre-saturated pipe makes the "+
				"first ping write block, took %s", wsWriteWait, elapsed)
		assert.GreaterOrEqualf(t, elapsed, wsWriteWait-3*time.Second,
			"writePump returned after only %s — expected it to actually block until close to "+
				"wsWriteWait(%s) before the deadline fires; a near-instant return suggests the pipe "+
				"was never actually saturated before the feeder started", elapsed, wsWriteWait)
	case <-time.After(wsWriteWait + 30*time.Second):
		t.Fatal("writePump did not return within wsWriteWait+30s while flooded with ping frames against " +
			"a non-reading, pre-saturated client — the ping write deadline is not being enforced " +
			"(regression: missing SetWriteDeadline before wc.conn.WriteMessage(websocket.PingMessage, " +
			"nil) in writePump)")
	}
	<-feederDone
}
