// websocket_pump.go: Write pump and event forwarding to the client.

package gateway

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
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
	// here, the instant the writer dies — not just wc.close(). Before this,
	// the ONLY place that removed a chatID from h.sessions was ServeHTTP's
	// deferred cleanup, which runs after readLoop returns — and readLoop can
	// keep blocking on its own read deadline (up to wsPongWait, tens of
	// seconds) even though this connection's WRITE side is already dead.
	// Every token sent to a write-dead-but-still-bound connection in that
	// window pays sendRawFrameBytes' full backoff before dropping (bounded
	// now by the doneCh case added alongside this fix, but still non-zero
	// work) AND, more importantly, keeps the connection in every
	// resolveSessionConnsLocked() target list — including the set a live,
	// healthy viewer on the same session is waiting behind, since Update()
	// delivers to targets sequentially. Removing it from h.sessions here
	// drops it out of every future resolution immediately; ServeHTTP's own
	// deferred delete (still needed to clean up h.sessionIDs/h.taskChatIDs,
	// keyed by the same chatID) becomes a harmless, idempotent no-op repeat
	// when it runs later.
	defer func() {
		wc.close()
		if chatID != "" {
			h.mu.Lock()
			if h.sessions[chatID] == wc {
				delete(h.sessions, chatID)
			}
			h.mu.Unlock()
		}
	}()

	for {
		select {
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

// sendConnGenFrame marshals any generated frame type (from pkg/api/generated) and
// routes it to the connection with the same backpressure and replay-divert logic as
// sendConnGenFrame.  frameType is the string value of the frame's "type" field, used
// to determine whether the frame is critical (never dropped, blocks briefly).
func sendConnGenFrame(wc *wsConn, frameType string, frame any) {
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal generated frame failed", "type", frameType, "error", err)
		return
	}
	sendRawFrameBytes(wc, frameType, data)
}

// broadcastRaw fans one pre-marshaled frame out to every connected WS client
// (single-user model — every connection is the one account, so no per-account
// scoping). Best-effort: a connection whose send buffer is full drops the
// frame (logged with the caller-supplied message/attrs, counted on
// wc.droppedFrames) and must recover from the next reconnect snapshot.
// Shared by broadcastAskUserCard and broadcastToolApprovalRequired, which
// each keep their own frame construction and drop-log identity.
func (h *WSHandler) broadcastRaw(raw []byte, dropLogMsg string, dropLogAttrs ...any) {
	h.mu.Lock()
	conns := make([]*wsConn, 0, len(h.sessions))
	for _, wc := range h.sessions {
		conns = append(conns, wc)
	}
	h.mu.Unlock()
	for _, wc := range conns {
		select {
		case wc.sendCh <- raw:
		default:
			slog.Warn(dropLogMsg, dropLogAttrs...)
			wc.droppedFrames.Add(1)
		}
	}
}

// sendConnGenFrame marshals a frame and enqueues it on wc's send channel.
// For "done", "error", and approval frames, blocks up to 5 s rather than dropping,
// because losing these frames would leave the client in a permanently stuck state.
// For non-critical frames, retries with short delays (immediate, 10ms, 50ms) before dropping.
// After 20 cumulative dropped frames a "degraded" error frame is
// injected into the critical path to warn the client; the counter resets on success.
//
// During replay (wc.isReplayingLive == true), live frames arriving from the
// eventForwarder are diverted into wc.replayDivertCh so they do not interleave
// with replay frames that are being written directly to wc.sendCh. After replay
// finishes, handleAttachSession drains replayDivertCh into sendCh in order.
// This replaces the old wc.sendCh swap which caused a data race.
//
// droppedFramesWarnThreshold is the number of consecutively dropped non-critical
// frames after which a "connection degraded" error is sent to the browser.
const droppedFramesWarnThreshold = 20

// sendRawFrameBytes routes pre-marshaled frame bytes to the connection's send channel.
// It implements the replay-divert logic (W1-1), critical-frame blocking, and
// backpressure drop logic shared by sendConnGenFrame and wsStreamer.Update.
// frameType is used to determine criticality (done, error, exec_approval_*).
//
// Ordering guarantee (see docs/internal/investigation/bug-5-replay-order.md, code-reviewer
// Finding #2): the channel-selection decision (read isReplayingLive + pick targetCh)
// and the channel send are performed while holding wc.replayMu.RLock().  The drain in
// handleAttachSession holds wc.replayMu.Lock() for the entire drain+disarm sequence.
// This prevents the TOCTOU race where a writer snapshots isReplayingLive==true, is
// descheduled, the drain empties replayDivertCh and disarms the flag, and the writer
// then sends to the now-abandoned replayDivertCh.
//
// On the non-replay hot path (isReplayingLive==false) the RLock is never acquired,
// keeping the common case lock-free.
func sendRawFrameBytes(wc *wsConn, frameType string, data []byte) {
	// W1-1: if replay mode is active, divert live frames into the replay buffer
	// instead of wc.sendCh, so writePump never sees them while replay is running.
	// "error" and the exec_approval_* control frames are always sent to the
	// canonical sendCh regardless of replay state — they are rare, connection-
	// scoped signals that must reach the client immediately.
	isCritical := frameType == "done" || frameType == "error" ||
		frameType == "exec_approval_request" || frameType == "exec_approval_expired"

	// [ADR-082 review CR6] "done" is critical (must never be silently dropped
	// — see the isCritical branches below) but, UNLIKE error/exec_approval_*,
	// it must still respect replay ordering on a connection that is mid-
	// replay: a "done" marks a TURN ending, and a live turn finishing while a
	// re-attaching connection is still replaying its own history must not
	// jump the queue ahead of that connection's still-pending replay/catch-up
	// frames — the client would see an orphan "done" (no matching bubble) and
	// Stop would appear stuck. Diverting it like a token frame (still via the
	// isCritical, never-drop send semantics inside the divert branch below)
	// is what makes handleAttachSession's drain deliver
	// replay → catch-up → tail tokens → done in that exact order. The
	// replay's OWN synthetic "done" (streamReplay's frames_emitted summary)
	// is written directly into wc.sendCh by handleAttachSession's emitFn, not
	// through this function, so it is entirely unaffected by this change.
	bypassDivertWhileReplaying := isCritical && frameType != "done"

	// Fast path: not replaying (atomic check, no lock). This is the common case.
	if !wc.isReplayingLive.Load() || bypassDivertWhileReplaying {
		// Fall through to the send logic below with targetCh = sendCh.
	} else {
		// Slow path: replay is active. Hold RLock so the drain's Lock() cannot disarm
		// the flag until after we have completed the send into replayDivertCh.
		wc.replayMu.RLock()
		// Re-check under the lock: the drain may have disarmed the flag while we were
		// waiting for RLock.
		if wc.isReplayingLive.Load() && wc.replayDivertCh != nil {
			// Route to divert channel while holding the read-lock for the ENTIRE
			// send. Pass-2 reviewer caught: previous version RUnlock'd before the
			// send, letting the drain disarm + close the divert channel between
			// our RUnlock and the targetCh <- data write — orphaned-frame race.
			// Holding RLock through the send ensures the drain's exclusive Lock()
			// cannot fire until after our send completes.
			targetCh := wc.replayDivertCh
			defer wc.replayMu.RUnlock()
			//nolint:dupl // Mirrors the sendCh path below; differs by target channel + lock-holding context.
			switch {
			case isCritical:
				select {
				case targetCh <- data:
				case <-wc.doneCh:
					// ADR-082 review CR9/F4: the connection is already dead
					// (writePump has exited and closed doneCh) — no point
					// waiting out the 5s timeout only to close() a connection
					// that is already closing.
				case <-time.After(5 * time.Second):
					slog.Warn(
						"ws: send channel full after timeout for critical frame, closing connection",
						"type",
						frameType,
					)
					wc.close()
				}
			default:
				backoffs := [...]time.Duration{0, 10 * time.Millisecond, 50 * time.Millisecond}
				deadConn := false
				for _, wait := range backoffs {
					if wait == 0 {
						select {
						case targetCh <- data:
							wc.droppedFrames.Store(0)
							return
						case <-wc.doneCh:
							deadConn = true
						default:
						}
					} else {
						t := time.NewTimer(wait)
						select {
						case targetCh <- data:
							t.Stop()
							wc.droppedFrames.Store(0)
							return
						case <-wc.doneCh:
							t.Stop()
							deadConn = true
						case <-t.C:
						}
					}
					if deadConn {
						// ADR-082 review CR9/F4: stop retrying the moment the
						// connection is known dead instead of paying out the
						// full 0/10/50ms backoff schedule for every single
						// token — that cost is serial across every connection
						// Update() iterates, so a dead-but-still-bound viewer
						// otherwise slows delivery to every OTHER, live
						// viewer on the same session.
						break
					}
				}
				if deadConn {
					slog.Debug("ws: connection already closed, frame dropped without waiting out backoff", "type", frameType)
				} else {
					slog.Warn("ws: send channel full after backoff, frame dropped", "type", frameType)
				}
				wc.droppedTokens.Add(1)
				wc.droppedFrames.Add(1)
				if wc.droppedFrames.Load() >= int32(droppedFramesWarnThreshold) {
					wc.droppedFrames.Store(0)
					degraded, merr := json.Marshal(generated.ErrorFrame{
						Type:    string(generated.WsFrameTypeError),
						Message: "connection degraded: frames being dropped due to backpressure",
					})
					if merr != nil {
						slog.Error("ws: marshal degraded frame failed", "error", merr)
						return
					}
					select {
					case wc.sendCh <- degraded:
					case <-wc.doneCh:
					case <-time.After(5 * time.Second):
						slog.Warn("ws: could not deliver degraded warning frame, closing connection")
						wc.close()
					}
				}
			}
			return
		}
		wc.replayMu.RUnlock()
		// Flag was cleared before we got the lock — fall through to direct sendCh path.
	}

	targetCh := wc.sendCh

	//nolint:dupl // Mirrors the replayDivertCh path above; differs by target channel + lock-holding context.
	switch {
	case isCritical:
		// Critical frames must not be dropped. Block briefly; force-close on timeout.
		// Approval frames are critical: dropping them leaves the agent turn blocked for
		// the full approval timeout (90 s) and then results in a mysterious denial.
		select {
		case targetCh <- data:
		case <-wc.doneCh:
			// ADR-082 review CR9/F4: already dead — see the divert-path twin above.
		case <-time.After(5 * time.Second):
			slog.Warn("ws: send channel full after timeout for critical frame, closing connection", "type", frameType)
			wc.close()
		}
	default:
		// Try immediate send, then graduated retry delays (10 ms, 50 ms) before dropping.
		backoffs := [...]time.Duration{0, 10 * time.Millisecond, 50 * time.Millisecond}
		deadConn := false
		for _, wait := range backoffs {
			if wait == 0 {
				select {
				case targetCh <- data:
					wc.droppedFrames.Store(0)
					return
				case <-wc.doneCh:
					deadConn = true
				default:
				}
			} else {
				t := time.NewTimer(wait)
				select {
				case targetCh <- data:
					t.Stop()
					wc.droppedFrames.Store(0)
					return
				case <-wc.doneCh:
					t.Stop()
					deadConn = true
				case <-t.C:
					// Timer expired, try next delay.
				}
			}
			if deadConn {
				// ADR-082 review CR9/F4: a dead-but-still-bound connection
				// (writePump has exited, doneCh closed, but the ServeHTTP
				// teardown that unbinds it from h.sessions has not run yet —
				// readLoop can still be waiting out its own pong deadline,
				// up to wsPongWait) must not pay the FULL 0/10/50ms backoff
				// on every single token — that cost is paid serially, once
				// per connection, inside Update()'s per-target loop, so a
				// single dead viewer otherwise delays delivery to every
				// OTHER, live viewer bound to the same session.
				break
			}
		}

		if deadConn {
			slog.Debug("ws: connection already closed, frame dropped without waiting out backoff", "type", frameType)
		} else {
			// All attempts exhausted — drop the frame and record backpressure.
			slog.Warn("ws: send channel full after backoff, frame dropped", "type", frameType)
		}
		wc.droppedTokens.Add(1)
		wc.droppedFrames.Add(1)

		// After threshold drops, warn the client over the critical path so it knows
		// the connection is degraded. The degraded warning always goes to the canonical
		// wc.sendCh — never to replayDivertCh — so the user sees the overflow warning
		// immediately without waiting for replay to drain (W1-6).
		if wc.droppedFrames.Load() >= int32(droppedFramesWarnThreshold) {
			wc.droppedFrames.Store(0)
			degraded, merr := json.Marshal(generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "connection degraded: frames being dropped due to backpressure",
			})
			if merr != nil {
				slog.Error("ws: marshal degraded frame failed", "error", merr)
				return
			}
			select {
			case wc.sendCh <- degraded:
			case <-wc.doneCh:
			case <-time.After(5 * time.Second):
				slog.Warn("ws: could not deliver degraded warning frame, closing connection")
				wc.close()
			}
		}
	}
}
