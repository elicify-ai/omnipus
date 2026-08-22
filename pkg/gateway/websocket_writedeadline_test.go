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
//
// Transport (2026-08-22, cross-platform CI red — see git history for the
// diagnostic session): the transport connecting the two ends is a
// synchronous, unbuffered net.Pipe(), NOT a real TCP loopback socket.
//
// This replaces an earlier real-TCP mechanism (setupBackpressureWS opening
// an httptest.Server over 127.0.0.1, shrinking SO_SNDBUF/SO_RCVBUF via
// SetWriteBuffer(4096)/SetReadBuffer(4096), then pre-flooding the pipe —
// saturateTCPSendPath — with up to 16 MiB before handing off to writePump).
// That mechanism had already been rewritten once, on 2026-08-06 (PR #597),
// to fix an identical symptom — TestWritePumpEnforcesWriteDeadline_Ping
// timing out at its outer bound on `matrix (macos-latest, arm64)` — and
// recurred with the byte-for-byte same signature
// (`--- FAIL: TestWritePumpEnforcesWriteDeadline_Ping (40.32s)`, i.e. the
// wsWriteWait+30s outer t.Fatal) on the SAME job.
//
// Root cause, isolated by direct experiment on this exact platform
// (darwin/amd64, reproduces the CI failure locally, 40.32s timeout,
// byte-identical): explicit small SO_SNDBUF/SO_RCVBUF hints do not produce
// a sustained zero-window condition on macOS's TCP stack.
//   - getsockopt after SetWriteBuffer(4096)/SetReadBuffer(4096) DOES report
//     the requested 4096-byte buffers — the hint is accepted at the socket
//     layer.
//   - But a burst write against that "shrunk" pipe still gets ~180 KiB
//     through before its own short deadline trips (far more than 4096
//     bytes), and — the actual killer — once that burst's deadline fires
//     and control returns to the caller, a single 2-byte write immediately
//     afterward succeeds in single-digit microseconds to milliseconds. The
//     "full" pipe drains itself almost instantly with nobody reading at the
//     application layer at all. A continuous background flood (rewriting
//     saturateTCPSendPath to feed constantly instead of bursting once)
//     reproduces the identical instant-drain-on-pause behavior. Even
//     shrinking SO_RCVBUF on the CLIENT side BEFORE the TCP handshake (via
//     a custom net.Dialer.Control, so the window-scale factor negotiated in
//     the SYN reflects the small buffer, not the OS default ~150–400 KiB)
//     does not produce a sustained block: macOS's receive-buffer
//     autotuning reports back a much larger effective window
//     (empirically ~320 KiB) regardless of the requested value. None of
//     this is something a test can out-flood its way past within a bounded
//     window — it is the kernel continuing to service the connection out
//     from under the deliberately-idle "reader".
//
// A synchronous net.Pipe() sidesteps the whole class of problem: it has no
// OS socket buffer to fill, shrink, drain, or autotune. A Write on one end
// blocks until a matching Read happens on the other, full stop. Since the
// client-side reader goroutine is deliberately never started (same "client
// stops reading" scenario as before), writePump's very next WriteMessage
// call blocks immediately and deterministically until wsWriteWait's
// SetWriteDeadline fires — verified locally to return in
// 2.000162445s against a 2s deadline, i.e. accurate to the sub-millisecond,
// with zero flakiness across repeated runs. wsPipeListener below is a
// minimal net.Listener that hands out one net.Pipe() half per Accept(), and
// the client's websocket.Dialer connects to it via NetDialContext instead
// of a real host:port — everything downstream (the real
// http.Handler/websocket.Upgrader handshake, the real *websocket.Conn, the
// real writePump under test) is unchanged; only the byte transport
// underneath the TCP-shaped abstraction is swapped for a deterministic one.

package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wsPipeAddr is the dummy net.Addr wsPipeListener reports — net.Pipe() ends
// have no real address, and nothing under test inspects Addr(), so any
// stable String() is sufficient.
type wsPipeAddr struct{}

func (wsPipeAddr) Network() string { return "pipe" }
func (wsPipeAddr) String() string  { return "pipe" }

// wsPipeListener is a net.Listener whose Accept() hands out one side of a
// fresh, synchronous, unbuffered net.Pipe() per "connection" instead of a
// real TCP socket — see the package doc comment above for why. Each
// connection is handed in over ch by the paired NetDialContext dialer in
// setupBackpressureWS; Accept blocks until one arrives or the listener is
// closed.
type wsPipeListener struct {
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newWSPipeListener() *wsPipeListener {
	return &wsPipeListener{ch: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *wsPipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, errors.New("wsPipeListener: closed")
	}
}

func (l *wsPipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *wsPipeListener) Addr() net.Addr { return wsPipeAddr{} }

// setupBackpressureWS stands up a real WS server connection (via a gorilla
// Upgrader served over a wsPipeListener — deliberately NOT going through
// WSHandler.ServeHTTP/auth, since writePump itself touches no WSHandler
// state, and deliberately NOT a real TCP loopback socket, per the package
// doc comment above) and a real WS client connection that completes the
// handshake and then never reads again. It returns the server-side wsConn
// (already handed to a running writePump goroutine) and a channel that is
// closed exactly when that writePump goroutine returns.
//
// No socket-buffer shrinking or pre-saturation is needed: the underlying
// net.Pipe() transport is synchronous and unbuffered, so the very first
// write writePump attempts after the client stops reading blocks
// immediately and deterministically.
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
		srvConn = &wsConn{conn: conn, sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
		close(connReady) // happens-before the receive below (Go memory model)

		// writePump touches no WSHandler field — a zero-value receiver is
		// sufficient and keeps this test independent of full gateway setup.
		(&WSHandler{}).writePump(srvConn)
		close(wpDone)
	})

	lst := newWSPipeListener()
	srv := &httptest.Server{
		Listener: lst,
		Config:   &http.Server{Handler: handler},
	}
	srv.Start()
	t.Cleanup(srv.Close)

	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			client, server := net.Pipe()
			select {
			case lst.ch <- server:
			case <-lst.closed:
				return nil, errors.New("wsPipeListener: closed before handshake")
			}
			return client, nil
		},
	}
	clientConn, resp, err := dialer.Dial("ws://pipe/", nil)
	require.NoError(t, err, "client dial must succeed")
	if resp != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() { _ = clientConn.Close() })
	// Deliberately no reader goroutine for clientConn from this point on —
	// this is the "client drains the socket slowly / stops draining"
	// scenario from the bug report. Because the transport is a synchronous,
	// unbuffered net.Pipe(), there is no OS buffer for that omission to
	// merely shrink: the very next write attempted from the server side
	// blocks immediately, for real, until a deadline fires.

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
