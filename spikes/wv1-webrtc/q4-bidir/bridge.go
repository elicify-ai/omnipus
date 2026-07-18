package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// inputBridge is the WS server the Node encoder orchestrator (run.js)
// connects to at startup so the Go relay can send it input-dispatch
// commands. Extensions.loadUnpacked demands pipe-mode CDP transport, which
// only run.js holds (see wv1-spike-results.md Q2) -- Go cannot attach to
// the encoder Chrome's CDP session directly. So: Go owns the WebRTC "input"
// data channel from the viewer, and forwards each event over this
// localhost-only WS hop to run.js, which translates it to
// Input.dispatchMouseEvent / Input.dispatchKeyEvent (or Runtime.evaluate
// for verification) on the captured tab's CDP session and replies.
//
// Spike-only scaffolding: in the real build Go owns the pipe directly via
// chromedp and dispatches CDP commands itself, no WS hop, no Node process.
type inputBridge struct {
	upgrader websocket.Upgrader

	mu      sync.Mutex
	conn    *websocket.Conn
	writeMu sync.Mutex
	pending map[int64]chan bridgeResp

	cmdSeq    atomic.Int64
	connected atomic.Bool
}

type bridgeCmd struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type bridgeResp struct {
	ID     int64           `json:"id"`
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

var errBridgeNotConnected = fmt.Errorf("inputbridge: node encoder not connected")
var errBridgeTimeout = fmt.Errorf("inputbridge: command timed out")

func newInputBridge() *inputBridge {
	return &inputBridge{
		upgrader: websocket.Upgrader{
			// The Node client (Node 22 built-in WebSocket) sends no Origin
			// header worth checking; the real access control is the
			// localhost-only remote-address guard in handler().
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		pending: make(map[int64]chan bridgeResp),
	}
}

// isLocalhostRequest guards both /inputbridge and /debug/* -- port 8080 is
// exposed to the public Fly proxy for /view, so anything CDP-adjacent must
// refuse non-loopback callers even in spike code.
func isLocalhostRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (b *inputBridge) handler(w http.ResponseWriter, r *http.Request) {
	if !isLocalhostRequest(r) {
		serverLog.Add("[inputbridge] rejecting non-localhost connection from %s", r.RemoteAddr)
		http.Error(w, "forbidden: inputbridge is localhost-only", http.StatusForbidden)
		return
	}

	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		serverLog.Add("[inputbridge] upgrade failed: %v", err)
		return
	}
	serverLog.Add("[inputbridge] node encoder bridge connected from %s", r.RemoteAddr)

	b.mu.Lock()
	old := b.conn
	b.conn = conn
	b.connected.Store(true)
	b.mu.Unlock()
	if old != nil {
		// A previous encoder connection is stale (e.g. reconnect); drop it.
		_ = old.Close()
	}

	defer func() {
		b.mu.Lock()
		if b.conn == conn {
			b.conn = nil
			b.connected.Store(false)
		}
		b.mu.Unlock()
		_ = conn.Close()
		serverLog.Add("[inputbridge] node encoder bridge disconnected")
	}()

	for {
		_, data, rerr := conn.ReadMessage()
		if rerr != nil {
			return
		}
		var resp bridgeResp
		if uerr := json.Unmarshal(data, &resp); uerr != nil {
			serverLog.Add("[inputbridge] bad response JSON: %v", uerr)
			continue
		}
		b.mu.Lock()
		ch, ok := b.pending[resp.ID]
		if ok {
			delete(b.pending, resp.ID)
		}
		b.mu.Unlock()
		if ok {
			ch <- resp
		} else {
			serverLog.Add("[inputbridge] response for unknown/expired id=%d", resp.ID)
		}
	}
}

// call sends a command to the connected Node bridge and blocks for its
// reply (matched by id) up to timeout. Safe for concurrent use -- each
// caller gets its own response channel.
func (b *inputBridge) call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	b.mu.Lock()
	conn := b.conn
	b.mu.Unlock()
	if conn == nil {
		return nil, errBridgeNotConnected
	}

	id := b.cmdSeq.Add(1)
	cmd := bridgeCmd{ID: id, Method: method, Params: params}
	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal bridge command: %w", err)
	}

	ch := make(chan bridgeResp, 1)
	b.mu.Lock()
	b.pending[id] = ch
	b.mu.Unlock()

	b.writeMu.Lock()
	werr := conn.WriteMessage(websocket.TextMessage, data)
	b.writeMu.Unlock()
	if werr != nil {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, fmt.Errorf("write bridge command: %w", werr)
	}

	select {
	case resp := <-ch:
		if !resp.OK {
			return nil, fmt.Errorf("bridge error: %s", resp.Error)
		}
		return resp.Result, nil
	case <-time.After(timeout):
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, errBridgeTimeout
	}
}

func (b *inputBridge) isConnected() bool {
	return b.connected.Load()
}

// evaluate runs a Runtime.evaluate expression on the captured tab (via
// run.js's stored metronomeSession) and returns the JSON-decoded value.
// Used both by /debug/evaluate (verification tooling) and could be reused
// by any future bridge consumer.
func (b *inputBridge) evaluate(expression string, timeout time.Duration) (json.RawMessage, error) {
	return b.call("evaluate", map[string]string{"expression": expression}, timeout)
}
