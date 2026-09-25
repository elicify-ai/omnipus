// websocket_pump.go: Write pump and event forwarding to the client.

package gateway

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
)

// writePump is the single goroutine that writes all frames to the WebSocket connection.
// gorilla/websocket requires all writes to happen from the same goroutine.
// A nil message on sendCh is the sentinel for a ping frame.
//
// chatID (ADR-082 review CR9/F4) is this connection's own key in h.sessions —
// passed in (rather than resolved from wc) purely so the deferred cleanup
// below can promptly remove wc from h.sessions the instant this goroutine
// exits, without waiting for readLoop to notice.
func (h *WSHandler) writePump(wc *wsConn, chatID string) {
	// 2026-07-31 review finding (mirrors the same fix in browser_ws.go's
	// writePump): returning here on a write-side stall used to leave the
	// connection write-dead but read-alive — nothing else in this function
	// called wc.close(), so pingPump/sendRawFrameBytes kept selecting on a
	// doneCh that was never closed, and readLoop's own read deadline kept
	// getting refreshed by whatever the client was still sending (including
	// the client's own app-level ping). The SetWriteDeadline calls below only
	// bound how long ONE write blocks; without this, the connection was still
	// only actually reaped by the client's independent missed-ping self-heal
	// (ws.ts), not by anything server-side. wc.close() is sync.Once-guarded,
	// so signalling here the moment the writer dies is safe to call alongside
	// whatever else already calls it.
	//
	// [ADR-082 review CR9/F4] Also unbind wc from h.sessions[chatID] right
	// here, the instant the writer dies — not just wc.close(). readLoop can
	// keep blocking on its own read deadline (up to wsPongWait) even though
	// this connection's WRITE side is already dead; ServeHTTP's own deferred
	// delete (still needed for h.sessionIDs/h.taskChatIDs) becomes a
	// harmless, idempotent repeat when it runs later.
	//
	// #823: likewise unbind it from its session hub, so the hub stops
	// queueing frames for a connection whose writer is gone.
	defer func() {
		wc.close()
		h.mu.Lock()
		if chatID != "" && h.sessions[chatID] == wc {
			delete(h.sessions, chatID)
		}
		h.unbindConnHubLocked(wc)
		h.mu.Unlock()
	}()

	for {
		// #823: a pending 4008 close (the connection fell too far behind)
		// goes out before anything else still in the send window.
		select {
		case <-wc.closeReq:
			wc.writeCatchUpClose()
			return
		default:
		}
		select {
		case <-wc.closeReq:
			wc.writeCatchUpClose()
			return
		case msg, ok := <-wc.sendCh:
			if !ok {
				return
			}
			if msg == nil {
				// nil sentinel: send a WebSocket ping frame.
				//
				// SetWriteDeadline before every write (including this
				// keepalive ping) so a slow/back-pressured client can't
				// stall this single writer goroutine indefinitely — without
				// it, a blocked write here would silently starve the
				// keepalive ping, and the reverse proxy would eventually
				// reset the TCP connection with no close frame (browser
				// sees code 1006) instead of the deadline firing and
				// tearing the connection down cleanly within wsWriteWait.
				if err := wc.conn.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
					slog.Debug("ws: SetWriteDeadline failed for ping", "error", err)
					return
				}
				if err := wc.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					slog.Debug("ws: ping write error", "error", err)
					return
				}
				continue
			}
			if err := wc.conn.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
				slog.Debug("ws: SetWriteDeadline failed", "error", err)
				return
			}
			if err := wc.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				slog.Debug("ws: write error", "error", err)
				return
			}
		case <-wc.doneCh:
			return
		}
	}
}

// wsPingMsg is a nil sentinel enqueued by pingPump to signal writePump to send a WebSocket ping.
// Using a sentinel through sendCh ensures all writes go through the single writer goroutine,
// satisfying gorilla/websocket's single-writer requirement (fix for gorilla write race).
// Important: do not pass nil []byte through sendCh for any other purpose — nil is reserved as the ping sentinel.
var wsPingMsg []byte

// pingPump enqueues a nil sentinel onto sendCh every 30 s for keep-alive pings.
// All writes go through writePump, satisfying gorilla's single-writer requirement.
func (h *WSHandler) pingPump(wc *wsConn) {
	ticker := time.NewTicker(wsPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			select {
			case wc.sendCh <- wsPingMsg: // nil sentinel triggers a ping in writePump
			case <-wc.doneCh:
				return
			}
		case <-wc.doneCh:
			return
		}
	}
}

// sendConnGenFrame marshals a generated frame and appends it to wc's ordered
// outbound queue (#823 ws_conn_queue.go). frameType is the frame's "type"
// value, used for logging only.
func sendConnGenFrame(wc *wsConn, frameType string, frame any) {
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal generated frame failed", "type", frameType, "error", err)
		return
	}
	sendRawFrameBytes(wc, frameType, data)
}

// sendRawFrameBytes appends pre-marshaled frame bytes to wc's ordered
// outbound queue. It never blocks and never drops: a connection that has
// fallen too far behind is closed with 4008 and reconnects to catch up
// (founder decision Q5), which replaces the pre-#823 backoff-and-drop path,
// its "connection degraded" warning and the replay divert channel. While
// the connection's attach is being answered the frame is held and delivered
// right after catch_up_complete (hold mode).
func sendRawFrameBytes(wc *wsConn, frameType string, data []byte) {
	if wc == nil {
		return
	}
	if !wc.enqueue(data) {
		slog.Debug("ws: frame not queued — connection closed or closing", "type", frameType)
	}
}

// broadcastRaw fans one pre-marshaled, unsequenced frame out to every
// connected WS client (single-user model — every connection is the one
// account). Each connection gets it through its own ordered queue, so
// nothing is dropped for a slow tab; fanoutCount is the number of
// connections, dropCount the number that were already closed or closing
// (they reconnect and re-read state). A non-empty dropLogMsg logs each such
// connection.
func (h *WSHandler) broadcastRaw(raw []byte, dropLogMsg string, dropLogAttrs ...any) (fanoutCount, dropCount int) {
	h.mu.Lock()
	conns := make([]*wsConn, 0, len(h.sessions))
	for _, wc := range h.sessions {
		conns = append(conns, wc)
	}
	h.mu.Unlock()
	for _, wc := range conns {
		if !wc.enqueue(raw) {
			if dropLogMsg != "" {
				slog.Warn(dropLogMsg, dropLogAttrs...)
			}
			dropCount++
		}
	}
	return len(conns), dropCount
}
