// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_conn_queue.go: the per-connection ordered outbound queue (#823
// BE-DESIGN.md §2.1, founder decision Q5).
//
// Every frame for a browser connection — sequenced session frames from the
// hub and unsequenced connection frames alike — is appended here, and
// writePump is the only reader. The queue:
//
//   - never blocks the caller: the hub appends while holding its own lock,
//     so a slow browser can never stall a turn, another tab or another
//     session;
//   - never drops a frame: there is no backoff-and-drop path any more;
//   - closes a connection that falls too far behind (more than
//     connQueueByteCap bytes waiting) with WebSocket close code 4008
//     "catch-up required". Nothing is lost: the session journal still holds
//     every numbered frame, and the tab reconnects and catches up from its
//     cursor (incremental) or gets a snapshot.
//
// Shape: sendCh (a small buffered channel writePump selects on) is the hot
// window; q holds whatever does not fit. Invariant, held under qmu: while q
// is non-empty every new frame goes to q's tail (FIFO), and a single mover
// goroutine — started on demand, gone when q is empty — hands q's head to
// sendCh as soon as it has room, so the queue drains at exactly the speed
// the socket reads, whoever the reader is.
//
// Hold mode (BE-DESIGN.md §4.1 A4/A7): while an attach is being answered,
// live frames published to the connection go to held instead; the attach
// goroutine writes its catch-up with direct/directWait (which bypass held),
// then releaseHold moves held into the queue — so live frames always arrive
// strictly after catch_up_complete, in seq order, with no divert channel
// and no drop.
package gateway

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// connQueueByteCap is how far behind a connection may fall (bytes queued
	// beyond sendCh, or held during an attach) before it is closed with 4008.
	connQueueByteCap = 8 << 20
	// wsCloseCatchUpRequired is the close code a too-far-behind connection
	// gets. The SPA reconnects immediately and catches up (BE-DESIGN.md §6.7).
	wsCloseCatchUpRequired = 4008
	wsCloseCatchUpReason   = "catch-up required"
	// attachReplaySoftCap bounds how much an attach's own catch-up writes may
	// queue before the attach goroutine waits for writePump to drain: a long
	// transcript replay must be paced by the socket, not trip the 4008 cap.
	attachReplaySoftCap = 4 << 20
)

// attachReplayStallTimeout is how long an attach waits for the socket to
// make progress before treating the connection as too slow. A var only so a
// test can exercise the stall path without a real 5-second wait; never
// mutate it outside a test.
var attachReplayStallTimeout = 5 * time.Second

// errConnClosed is returned by directWait when the connection is gone or was
// closed for falling too far behind.
var errConnClosed = errors.New("ws: connection closed")

// enqueue appends frame to the connection's outbound queue — to held while
// an attach is being answered. It never blocks and never drops; it returns
// false when the connection is already dead or was just closed for falling
// too far behind (the caller — the hub — then unbinds it).
func (c *wsConn) enqueue(frame []byte) bool {
	select {
	case <-c.doneCh:
		return false
	default:
	}
	c.qmu.Lock()
	if c.qClosed {
		c.qmu.Unlock()
		return false
	}
	if c.holding {
		c.held = append(c.held, frame)
		c.heldBytes += len(frame)
		if c.heldBytes > connQueueByteCap {
			c.overflowLocked()
			c.qmu.Unlock()
			c.closeCatchUp()
			return false
		}
		c.qmu.Unlock()
		return true
	}
	ok := c.pushLocked(frame)
	c.qmu.Unlock()
	if !ok {
		c.closeCatchUp()
	}
	return ok
}

// direct appends frame to the queue, bypassing hold mode — for the attach
// goroutine's own small catch-up control frames (session_state,
// session_snapshot, catch_up_complete). Never blocks.
func (c *wsConn) direct(frame []byte) bool {
	c.qmu.Lock()
	if c.qClosed {
		c.qmu.Unlock()
		return false
	}
	ok := c.pushLocked(frame)
	c.qmu.Unlock()
	if !ok {
		c.closeCatchUp()
	}
	return ok
}

// directWait is direct with flow control, for the bulk of an attach's
// catch-up (journal tail, transcript replay, projection): when more than
// attachReplaySoftCap bytes are already waiting it waits for writePump to
// make room, so a long history is paced by the socket instead of tripping
// the 4008 cap. It gives up with errSendTimeout when the socket makes no
// progress for attachReplayStallTimeout.
func (c *wsConn) directWait(ctx context.Context, frame []byte) error {
	for {
		c.qmu.Lock()
		if c.qClosed {
			c.qmu.Unlock()
			return errConnClosed
		}
		if len(c.q) == 0 || c.qBytes+len(frame) <= attachReplaySoftCap {
			ok := c.pushLocked(frame)
			c.qmu.Unlock()
			if !ok {
				c.closeCatchUp()
				return errConnClosed
			}
			return nil
		}
		if c.qDrained == nil {
			c.qDrained = make(chan struct{}, 1)
		}
		drained := c.qDrained
		c.qmu.Unlock()
		timer := time.NewTimer(attachReplayStallTimeout)
		select {
		case <-drained:
			timer.Stop()
		case <-c.doneCh:
			timer.Stop()
			return errConnClosed
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			return errSendTimeout
		}
	}
}

// pushLocked appends frame behind everything already queued. Caller holds
// qmu and has checked qClosed. Returns false when the queue just exceeded
// connQueueByteCap (the queue is then closed; the caller must call
// closeCatchUp after releasing qmu).
func (c *wsConn) pushLocked(frame []byte) bool {
	if len(c.q) == 0 {
		select {
		case c.sendCh <- frame:
			return true
		default:
		}
	}
	c.q = append(c.q, frame)
	c.qBytes += len(frame)
	if c.qBytes > connQueueByteCap {
		c.overflowLocked()
		return false
	}
	if !c.moverActive {
		c.moverActive = true
		go c.moveQueue()
	}
	return true
}

// moveQueue hands queued frames to sendCh in order, blocking on sendCh (never
// on a lock anyone else needs) until the queue is empty or the connection
// is gone. At most one runs per connection (moverActive, under qmu).
func (c *wsConn) moveQueue() {
	for {
		c.qmu.Lock()
		if len(c.q) == 0 || c.qClosed {
			c.moverActive = false
			c.qmu.Unlock()
			return
		}
		frame := c.q[0]
		c.qmu.Unlock()
		select {
		case c.sendCh <- frame:
		case <-c.doneCh:
			c.qmu.Lock()
			c.moverActive = false
			c.qmu.Unlock()
			return
		}
		c.qmu.Lock()
		// q[0] is still frame: only this goroutine pops, and overflow (which
		// clears q) is checked at the top of the loop.
		if len(c.q) > 0 {
			c.qBytes -= len(c.q[0])
			c.q[0] = nil
			c.q = c.q[1:]
		}
		if c.qDrained != nil {
			select {
			case c.qDrained <- struct{}{}:
			default:
			}
		}
		c.qmu.Unlock()
	}
}

// overflowLocked closes the queue for good. Caller holds qmu.
func (c *wsConn) overflowLocked() {
	c.qClosed = true
	c.q, c.qBytes = nil, 0
	c.held, c.heldBytes = nil, 0
	c.holding = false
}

// startHold switches the connection into hold mode (BE-DESIGN.md §4.1 A4).
// Called by sessionHub.bind under the hub's lock, so the switch is atomic
// with the bind: every frame the hub publishes after the bind is held.
func (c *wsConn) startHold() {
	c.qmu.Lock()
	if !c.qClosed {
		c.holding = true
	}
	c.qmu.Unlock()
}

// releaseHold ends hold mode (§4.1 A7): the held live frames are appended to
// the queue in the order they were published, behind the catch-up the
// attach goroutine already queued. The flag flips under qmu with held
// drained in the same critical section, so a frame published concurrently
// can never overtake a held one.
func (c *wsConn) releaseHold() {
	c.qmu.Lock()
	held := c.held
	c.held, c.heldBytes, c.holding = nil, 0, false
	ok := true
	for _, f := range held {
		if !c.pushLocked(f) {
			ok = false
			break
		}
	}
	c.qmu.Unlock()
	if !ok {
		c.closeCatchUp()
	}
}

// queuedFrames reports how many frames are waiting beyond sendCh plus how
// many are held (tests and diagnostics).
func (c *wsConn) queuedFrames() (queued, held int) {
	c.qmu.Lock()
	defer c.qmu.Unlock()
	return len(c.q), len(c.held)
}

// closeCatchUp closes a connection that fell too far behind with close code
// 4008 "catch-up required" (founder decision Q5: disconnect and catch up,
// never silently drop). The close frame is written by writePump itself —
// the connection's only writer — the moment its current write finishes, and
// ahead of anything still in the send window (closeReq has priority there).
// A connection without a writePump (a bare test fixture) is just closed.
// Callers may hold no lock that writePump needs; this never blocks.
func (c *wsConn) closeCatchUp() {
	c.catchUpCloseOnce.Do(func() {
		c.closeCode.Store(wsCloseCatchUpRequired)
		slog.Warn("ws: connection too far behind — closing with 4008 so it reconnects and catches up",
			"event", "ws_close_catch_up_required", "user_id", c.userID)
		if c.closeReq != nil {
			select {
			case c.closeReq <- struct{}{}:
			default:
			}
			return
		}
		c.close()
	})
}

// writeCatchUpClose is writePump's side of closeCatchUp: send the 4008 close
// frame (deadline-bounded like every write) and shorten the read deadline so
// the read loop ends promptly even if the client never answers the close.
func (c *wsConn) writeCatchUpClose() {
	if c.conn == nil {
		return
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
		slog.Debug("ws: SetWriteDeadline failed for 4008 close", "error", err)
		return
	}
	if err := c.conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(wsCloseCatchUpRequired, wsCloseCatchUpReason)); err != nil {
		slog.Debug("ws: 4008 close frame write failed", "error", err)
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
}
