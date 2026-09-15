// websocket_pump.go: Write pump and event forwarding to the client.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// GetStreamer implements bus.StreamDelegate.
//
// ADR-082 D2/FR-003: returns a streamer for EVERY webchat turn that carries a
// session id — including when ZERO connections are currently bound to that
// session (e.g. the only viewer disconnected mid-turn, or a keeper follow-up
// has no viewer at all). This is what keeps the webchat path on ChatStream
// for every LLM round of a turn instead of silently degrading to a
// non-streaming Chat call under the provider's DefaultRequestTimeout the
// moment the originating connection closes (ADR-082 §2 evidence E4).
// sessionID is provided by the caller (agent loop) so the streamer can record
// to the correct transcript without a map reverse-lookup; chatID is kept only
// as an advisory "origin" label (shadow-stream ownership key, markStreamed) —
// it is NOT used to resolve delivery targets any more (see wsStreamer.Update).
func (h *WSHandler) GetStreamer(_ context.Context, channel, chatID, sessionID string) (bus.Streamer, bool) {
	if channel != "webchat" {
		return nil, false
	}
	sid := sessionID
	if sid == "" {
		// Backward-compat fallback for a caller that hasn't yet threaded a
		// session id through explicitly — resolve from this chatID's own
		// current binding.
		h.mu.Lock()
		sid = h.sessionIDs[chatID]
		h.mu.Unlock()
	}
	if sid == "" {
		return nil, false
	}

	// Resolve the agent store for transcript recording.
	agentStore := h.resolveSessionStore(sid)

	// Resolve the active agent for this session so the transcript entry
	// can be tagged with the correct agent ID (FR-002). Key by sessionID.
	//
	// Prefer the handoff override, then fall back to the session's
	// ActiveAgentID from metadata. Without the fallback, assistant entries
	// for un-handed-off sessions get written with AgentID="", which means
	// HydrateAgentHistoryFromTranscript attributes them to "main" instead
	// of the real owning agent — so the next turn's LLM call has only
	// tool_calls/tool_results in its history, no connecting reasoning text,
	// and the agent re-starts the task from scratch.
	activeAgentID := ""
	if aid, ok := h.agentLoop.GetSessionActiveAgent(sid); ok && aid != "" {
		activeAgentID = aid
	} else if agentStore != nil {
		if meta, err := agentStore.GetMeta(sid); err == nil && meta != nil {
			if meta.ActiveAgentID != "" {
				activeAgentID = meta.ActiveAgentID
			} else if meta.AgentID != "" {
				activeAgentID = meta.AgentID
			}
		}
	}

	streamer := &wsStreamer{
		chatID:     chatID,
		sessionID:  sid,
		agentStore: agentStore,
		agentID:    activeAgentID,
		channel:    h.webchatCh,
		// h (not just channel) so connection resolution works even when a
		// test harness never wired webchatCh — see wsHandler()'s doc
		// comment. Always safe: GetStreamer is a *WSHandler method, so h
		// (the receiver) is never nil here.
		h: h,
	}

	// ADR-082 D3/D4: register this round's streamer as sid's in-flight
	// streamer — read by handleAttachSession's catch-up snapshot and by
	// session_state's active_turn announcement. See liveStreamers' own doc
	// comment for the per-round overwrite semantics.
	h.mu.Lock()
	if h.liveStreamers == nil {
		h.liveStreamers = make(map[string]*wsStreamer)
	}
	h.liveStreamers[sid] = streamer
	h.mu.Unlock()

	return streamer, true
}

// resolveSessionConnsLocked returns every live *wsConn currently bound to
// sessionID — resolved via h.sessionIDs (chatID → sessionID) and h.sessions
// (chatID → connection) — plus originChatID's own connection (if it has one
// live in h.sessions) even when its session mapping is empty or has not
// caught up yet. Caller must already hold h.mu. Pass "" for originChatID
// when there is no meaningful origin connection to fall back to (every
// wsStreamer.Update/Finalize call site: streaming delivery is purely
// session-scoped per ADR-082 D2, with no origin-chatID special case).
//
// ADR-082 D2: this is the SINGLE per-frame resolution point for webchat
// delivery, replacing both the old single-target s.conn direct-send and the
// separate fanOutToSessionPeers pass. A connection that (re)binds to a
// session between two frames sees every frame sent after its bind; a
// connection that unbinds/closes stops receiving frames from the very next
// call. No special-casing of "the originating connection" versus a
// later-attached peer — both are just entries in h.sessionIDs pointing at
// sessionID.
//
// [ADR-082 review CR7] Unifies what used to be TWO independent resolvers:
// this function (session-only) and webchatChannel.collectSessionConnsLocked
// (origin-chatID-first, then session). webchatChannel.Send and SendMedia
// need the origin-chatID fallback — a message/media send can legitimately
// fire before msg.SessionID's h.sessionIDs mapping exists yet (e.g. the very
// first outbound message of a brand-new session) — so having two near-
// identical implementations risked exactly the kind of drift CR7 found:
// SendMedia's copy never gained the "zero connections is not a failure"
// semantics Send's copy got under ADR-082 D6. One implementation now serves
// both call shapes.
func (h *WSHandler) resolveSessionConnsLocked(originChatID, sessionID string) []*wsConn {
	var conns []*wsConn
	var seen map[*wsConn]struct{}
	if originChatID != "" {
		if conn, ok := h.sessions[originChatID]; ok {
			conns = append(conns, conn)
			seen = map[*wsConn]struct{}{conn: {}}
		}
	}
	if sessionID == "" {
		return conns
	}
	for chatID, sid := range h.sessionIDs {
		if sid != sessionID {
			continue
		}
		conn, ok := h.sessions[chatID]
		if !ok {
			continue
		}
		if _, dup := seen[conn]; dup {
			continue
		}
		conns = append(conns, conn)
		if seen == nil {
			seen = make(map[*wsConn]struct{}, 2)
		}
		seen[conn] = struct{}{}
	}
	return conns
}

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

// orphanWatchdogTimeout is the duration the forwarder waits after a parent turn ends
// before synthesizing a subagent_end{status:"interrupted"} for any still-open span.
// Configurable so tests can override to a short value (e.g., 200ms) without sleeping.
//
// Bumped 2026-05-11 from 5s → 60s. The old value killed legitimate subagents:
// a sub-turn that runs 3 shell calls back-to-back through a real LLM regularly
// takes 6–12s of wall-clock (1–4s per turn iteration × N tool calls), and Mia's
// root turn ends within ~2s of dispatching `spawn`. With a 5s watchdog the
// subagent was synthesizing `status:"interrupted"` after the second shell call
// even though the agent loop was still executing — closes the cascade of
// suite-load flakes in subagent.spec.ts (a)–(e) and handoff.spec.ts (b).
//
// 60s is a conservative upper bound for a single sub-turn; the parent-loop
// `subturn.default_timeout_minutes` config knob already enforces a hard
// runtime cap higher up the stack for legitimately stuck sub-turns.
var orphanWatchdogTimeout = 60 * time.Second

// orphanWatchdogMaxRechecks bounds how many times startOrphanWatchdog will
// reschedule after agent.AgentLoop.IsSubTurnActiveForSpawnCall reports "still
// active" before giving up and force-emitting the synthetic interrupted
// terminal frame regardless of what the liveness check reports (fail-closed).
//
// The re-check-and-reschedule loop is correctly bounded for the NORMAL case
// by the pre-existing sub-turn context timeout (pkg/agent/subturn.go's
// defaultSubTurnTimeout, 5 minutes by default, or subturn.default_timeout_minutes
// when configured) — that timeout cancels the child's context, runTurn
// returns, and IsSubTurnActiveForSpawnCall eventually reports false once
// spawnSubTurn's cleanup defer finishes persisting the real terminal status
// (see turnState.subTurnRecordPersisted's doc comment, pkg/agent/turn.go).
// But a genuinely wedged/deadlocked turn — a goroutine that neither returns
// nor panics, e.g. blocked on a tool call that does not honor context
// cancellation — has no ceiling of its own: IsSubTurnActiveForSpawnCall would
// report "active" forever (isFinished never flips), and without this bound
// the watchdog would reschedule indefinitely, logging only at slog.Debug
// (invisible at typical production log levels) and never emitting a terminal
// frame for that span.
//
// Default 15 reschedules x orphanWatchdogTimeout's default 60s = 15 minutes,
// comfortably (~3x) above pkg/agent/subturn.go's defaultSubTurnTimeout (5
// minutes) — a legitimately still-running sub-turn should never come close to
// exhausting this many reschedules; long before it would, its own context
// timeout has fired and IsSubTurnActiveForSpawnCall is already reporting
// false. Configurable so tests can override to a small value without
// sleeping for the real 15 minutes.
var orphanWatchdogMaxRechecks = 15

// openSpanEntry tracks an in-flight subagent span in the event forwarder.
type openSpanEntry struct {
	spanID          string
	parentCallID    string
	agentID         string
	sessionID       string        // session_id at spawn time; carried on synthesized frames
	parentTurnEnded bool          // set to true when EventKindTurnEnd fires for the parent turn
	closeCh         chan struct{} // closed when EventKindSubTurnEnd arrives (cancels watchdog)
}

// ADR-057 FR-089 — W5 audit classification artefact (U11's half).
//
// generated.SESSION_SCOPED_FRAME_TYPES has 19 members. 13 were classified by
// the spec itself (adr-057-session-unification-spec.md, BDD-16/BDD-98/BDD-99):
// class (a) both-ids — token, done, tool_call_start, tool_call_result,
// tool_approval_required, media; class (b) producing_session_id-absent —
// replay_message, session_started, session_close_ack, subagent_start,
// subagent_end; class (c) documented pre-existing gap — rate_limit,
// replay_done. The remaining 6 were left "class not yet assigned by the W5
// audit" on their generated types pending this classification, verified
// 2026-08 against this tree:
//
//   - agent_switched → class (a). Built at this file's ToolExecEnd case
//     (below, evtSID := p.SessionID from agent.ToolExecEndPayload) immediately
//     after a successful switch_agent tool_call_result (ADR-071 D4 merged
//     hand_off/return_to_default into this one tool) — the IDENTICAL payload
//     and session-id source as tool_call_result, which is already verified
//     class (a). A delegated child can invoke switch_agent on its own session
//     exactly as a root turn can, so evtSID is the child's own producing
//     session whenever that happens, distinct from the routing key. Stamped
//     alongside tool_call_result above.
//
//   - task_status_changed → class (b). Its only non-test construction site is
//     `TaskStatusChangedFrame{..., SessionId: p.SessionID, ...}` (this file's
//     EventKindTaskStatusChanged case), fed solely by
//     agent.TaskStatusChangedPayload, whose ONLY constructor is
//     pkg/agent/task_executor.go:1821-1830 (`sessionID := t.SessionID; ...
//     EmitTaskStatusChanged(TaskStatusChangedPayload{SessionID: sessionID,
//     ...})`) — the TaskExecutor (a system-level component) reporting a
//     scheduled task's OWN session lifecycle, not narration produced by a
//     delegated child turn. No stamping added; producing_session_id stays
//     absent.
//
//   - cancel_stage → class (b). Sole constructor is sendCancelStageFrame
//     (this file), called only with the id RequestCancel's CancelScope.SessionID
//     resolved to (pkg/agent/cancel.go:404-409,
//     `hooks.SendStageFrame(sessionID, "graceful")`) — the cancel machinery's
//     own target id, narrating the Stop's progress across the whole subtree it
//     cascades to (FR-032/W10c below), never a specific descendant's own
//     output. No stamping added.
//
//   - goal_status → class (b). Its only non-test construction site is this
//     file's EventKindGoalStatusChanged case (`SessionId: p.SessionID` from
//     agent.GoalStatusChangedPayload), whose constructors —
//     pkg/agent/goal_loop.go:502-514, several sites in
//     pkg/agent/goal_triggers.go, and pkg/agent/session_messaging_wire.go:602-606
//     /:626-630 — all pass the session that OWNS the /goal loop config being
//     reported, i.e. the session reporting on itself. No call site was found
//     where this payload's SessionID is a delegated child distinct from a
//     parent's routing id. No stamping added.
//
//   - loop_status → class (b). Its only non-test construction site is this
//     file's EventKindLoopStatusChanged case (`SessionId: p.SessionID` from
//     agent.LoopStatusChangedPayload), whose sole constructor
//     (pkg/agent/loop_command.go:301-309, `emitLoopStatusFrame(sessionID,
//     ...)`) reports on the session whose OWN /loop state changed — the
//     identical "reporting on itself" shape as goal_status. No stamping added.
//
//   - system_overload → COULD NOT DETERMINE; not guessed (spec line ~1309
//     forbids it). Verified: `rg -rl 'SystemOverloadFrame|system_overload'
//     pkg/` matches only the generated type
//     (pkg/api/generated/asyncapi_types.gen.go), its fixtures
//     (pkg/api/generated/fixtures.go), and the inbound-schema copy
//     (pkg/gateway/inboundschemas/SystemOverloadFrame.yaml) — ZERO
//     non-generated, non-fixture Go call site constructs or sends this frame
//     anywhere in pkg/gateway or pkg/agent. The type is fully specified on the
//     wire and consumed by the SPA (src/store/chat.ts, src/lib/ws.ts) but is
//     never produced by the backend, so there is no real emission site to
//     classify BY EVIDENCE. Do not assume a class for this type until a
//     producer exists and is audited.
//
// TokenFrame/DoneFrame (wsStreamer.Update/Finalize, below) need no stamping
// change: their shared "shadow stream" gate (isShadowStream forced true
// whenever parentSpawnCallID != "", in both Update and Finalize) means these
// two frames are constructed ONLY when parentSpawnCallID == "" — i.e. only
// for a turn that IS the routing session by definition (turn.go:406,
// session.RoutingSessionID's own contract: "for a root turn, RoutingSessionID
// MUST equal that turn's own SessionID"). SessionId already equals the
// routing key in every case either frame is actually emitted, so
// ProducingSessionId is correctly always absent.
//
// eventForwarder listens on the agent EventBus and forwards tool_call_start/result
// frames to the browser so tool call UIs render in real time.
// It also matches events from an attached task session (via taskChatIDs).
// Extended (FR-H-004, FR-H-005): emits subagent_start / subagent_end frames and
// propagates parent_call_id on tool_call_* frames fired inside sub-turns.
// Orphan watchdog (FR-H-004, Scenario 7): when the parent turn ends before all spans
// are closed, a timer fires after orphanWatchdogTimeout and synthesizes
// subagent_end{status:"interrupted"} for each still-open span.
func (h *WSHandler) eventForwarder(wc *wsConn, chatID string, sub agent.EventSubscription, done chan<- struct{}) {
	defer close(done)

	// matchesChatID returns true if evtChatID belongs to this connection's chat or
	// to a task session the connection has attached to via handleAttachSession.
	matchesChatID := func(evtChatID string) bool {
		if evtChatID == chatID {
			return true
		}
		h.mu.Lock()
		tid := h.taskChatIDs[chatID]
		h.mu.Unlock()
		// Note: using exclusive lock for a read-only lookup. Acceptable for now;
		// migrate h.mu to sync.RWMutex if contention becomes measurable.
		return tid != "" && evtChatID == tid
	}

	// matchesEvent extends matchesChatID with a session-based fallback so a
	// live event reaches a connection that reattached to the same PERSISTED
	// session after a reload, even though the event's own ChatID still names
	// a now-stale, pre-reload connection.
	//
	// Root cause this closes (reload/replay "never self-updates" bug, live
	// UAT re-verification 2026-07): ServeHTTP mints a brand-new chatID
	// ("webchat:" + uuid.New()) for EVERY WebSocket connection, including a
	// browser reload — there is no client-supplied continuity. A turn's own
	// ChatID (turnState.chatID, threaded onto every event payload below) is
	// stamped ONCE at turn-dispatch time from whichever connection sent the
	// message, and never changes even if that connection later closes. A
	// background delegate (Critical:true, 7dd9e7a5) can legitimately keep
	// running long after its ORIGINATING connection is gone — matchesChatID
	// alone can then NEVER match its live completion event against a NEW
	// connection that reattached via attach_session, because rule 1 compares
	// against the stale chatID, and the taskChatIDs alias (rule 2) maps this
	// connection's chatID to the session_id, not to that stale chatID. The
	// browser was stuck showing whatever replay served at attach time until
	// a SECOND reload happened to catch the by-then-corrected transcript.
	//
	// evtSessionID/currentSessionID compares by the durable session_id
	// instead: h.sessionIDs[chatID] is the session THIS connection currently
	// has open (set by handleAttachSession/handleChatMessage), and
	// evtSessionID is threaded end-to-end on every relevant event payload
	// (SubTurnSpawnPayload, SubTurnEndPayload, ToolExec*Payload,
	// TurnEndPayload). session_id survives a reload; chatID does not.
	matchesEvent := func(evtChatID, evtSessionID string) bool {
		if matchesChatID(evtChatID) {
			return true
		}
		if evtSessionID == "" {
			return false
		}
		h.mu.Lock()
		currentSessionID := h.sessionIDs[chatID]
		h.mu.Unlock()
		return currentSessionID != "" && evtSessionID == currentSessionID
	}

	// sessionIDForChat looks up the active session_id for a given chatID so every
	// event frame can carry it, enabling per-session routing in the SPA.
	sessionIDForChat := func(evtChatID string) string {
		h.mu.Lock()
		sid := h.sessionIDs[evtChatID]
		if sid == "" {
			// Also check the task alias.
			if tid := h.taskChatIDs[evtChatID]; tid != "" {
				sid = h.sessionIDs[tid]
			}
		}
		h.mu.Unlock()
		return sid
	}

	// openSpans tracks in-flight subagent spans keyed by parentCallID.
	// Accessed only from the single eventForwarder goroutine — no mutex needed.
	openSpans := make(map[string]*openSpanEntry)

	// rootTurnEnded latches whether the root turn for this connection has
	// already ended, and with what watchdog reason (#605). The root TurnEnd
	// and a delegate's SubTurnSpawn are emitted from DIFFERENT goroutines
	// (the parent turn's vs the detached async-delegate's), so a spawn event
	// can legally reach this forwarder AFTER the root turn_end. The
	// EventKindTurnEnd case below only arms spans already registered in
	// openSpans — without this latch, such a late-registered span would
	// never be armed and would stay invisible to the orphan watchdog
	// forever. EventKindSubTurnSpawn consults the latch to arm late
	// registrations immediately; a NEW root turn's TurnStart resets it so
	// spans of a live root turn are not spuriously armed.
	// Single-goroutine state like openSpans — no mutex needed.
	rootTurnEnded := false
	rootTurnEndReason := ""

	// closeSpan marks a span as resolved and signals its watchdog to stop.
	closeSpan := func(parentCallID string) {
		if entry, ok := openSpans[parentCallID]; ok {
			select {
			case <-entry.closeCh: // already closed
			default:
				close(entry.closeCh)
			}
			delete(openSpans, parentCallID)
		}
	}

	// orphanFires carries a watchdog goroutine's "this span looks orphaned"
	// verdict to THIS goroutine, which alone decides whether to synthesize the
	// interrupted end.
	//
	// Root cause this closes (a delegation that completed normally reported as
	// interrupted, before or just after its real success frame): the watchdog
	// used to send the synthetic frame itself the moment
	// agent.AgentLoop.IsSubTurnActiveForSpawnCall reported "not active". A
	// sub-turn stops counting as active only after its EventKindSubTurnEnd is
	// queued on sub.C (markSubTurnSpanOpen, pkg/agent/steering.go) — but this
	// goroutine may not have consumed that event yet, so the watchdog's frame
	// could still win. Deciding here, only once every event that was already
	// queued when the verdict arrived has been handled, means a real end always
	// closes its span first; a span still open after that is genuinely
	// orphaned (or its end event was dropped, which EventBus counts and logs).
	type orphanFire struct {
		entry  *openSpanEntry
		reason string
		// forced: the reschedule ceiling was exceeded (already logged at Error
		// level by the watchdog).
		forced bool
		// eventsAhead: events that were queued on sub.C when the verdict
		// arrived and have not been handled yet.
		eventsAhead int
	}
	orphanFires := make(chan orphanFire)
	// forwarderExited releases a watchdog blocked handing over its verdict
	// once this goroutine has stopped reading orphanFires.
	forwarderExited := make(chan struct{})
	defer close(forwarderExited)
	var pendingOrphanFires []orphanFire

	// startOrphanWatchdog launches a goroutine that fires after orphanWatchdogTimeout
	// if the span is not closed first. On timeout it hands its verdict to this
	// goroutine (orphanFires), which synthesizes subagent_end and logs.
	// W1-9: the goroutine also exits cleanly when wc.doneCh is closed (connection torn down).
	startOrphanWatchdog := func(entry *openSpanEntry, reason string) {
		// Snapshot BOTH test-shrinkable knobs ONCE, synchronously, before
		// spawning the goroutine below — never re-read the package-level vars
		// from inside it. This goroutine loops (reschedule on "still active")
		// for however long a genuine delegate keeps running, re-arming
		// time.After(orphanWatchdogTimeout) and re-checking the reschedule
		// count against orphanWatchdogMaxRechecks on every iteration —
		// potentially for the lifetime of a long test. A test that shrinks
		// these vars via
		// SetOrphanWatchdogTimeoutForTest/SetOrphanWatchdogMaxRechecksForTest
		// and restores them (defer/t.Cleanup) the moment its OWN foreground
		// assertions pass has no happens-before edge to this still-running
		// goroutine's later reads — a genuine data race (WARNING: DATA RACE,
		// websocket.go:3229 vs export_test.go:29, caught under
		// `go test -race`, TestOrphanWatchdog_GenuinelyActiveDelegate_
		// NeverSynthesizesInterrupted), not a flake. Capturing both up front
		// removes every later read of the package vars from this goroutine;
		// production behavior is unchanged since neither var is ever mutated
		// outside tests.
		watchdogTimeout := orphanWatchdogTimeout
		maxRechecks := orphanWatchdogMaxRechecks
		go func() {
			rechecks := 0
			for {
				select {
				case <-entry.closeCh:
					// Span resolved normally — nothing to do.
					return
				case <-wc.doneCh:
					// Connection closed while waiting — exit cleanly without emitting.
					return
				case <-time.After(watchdogTimeout):
					// Span is still open after timeout. Before declaring it
					// orphaned, confirm the real sub-turn genuinely isn't
					// still running.
					//
					// Root cause this closes (transient false "interrupted"
					// status flicker, live UAT re-verification 2026-07): this
					// watchdog arms the instant the PARENT turn ends
					// (EventKindTurnEnd, IsRoot), which — for a background
					// delegate — routinely happens within a second or two of
					// dispatch. Before 7dd9e7a5 ("background delegate's
					// final answer lost when parent finishes first"), a
					// delegate needing more than one LLM turn silently exited
					// its own loop early the instant its parent ended, so
					// the real EventKindSubTurnEnd almost always arrived
					// (closing this span via closeCh) well inside
					// orphanWatchdogTimeout. Critical:true now lets it run
					// for its full, genuine duration — so a normal,
					// still-working delegation can legitimately still be
					// open when this timer fires, and synthesizing
					// status:"interrupted" here fabricated a false terminal
					// state for a turn that was, in truth, still generating
					// (self-correcting only once the real EventKindSubTurnEnd
					// arrived later and overwrote it). Re-checking real
					// liveness via agent.AgentLoop.IsSubTurnActiveForSpawnCall
					// and rescheduling instead of firing turns this
					// heuristic, timeout-only guess into a confirm-or-wait
					// check — a genuinely orphaned span (the real check
					// below returns false) is still reported exactly as
					// before.
					stillActive := h.agentLoop != nil && h.agentLoop.IsSubTurnActiveForSpawnCall(entry.parentCallID)
					forceCeiling := false
					if stillActive {
						rechecks++
						if rechecks > maxRechecks {
							// Ceiling exceeded: a genuinely wedged/deadlocked
							// turn — a goroutine that neither returns nor
							// panics, e.g. blocked on a tool call not
							// honoring context cancellation — would
							// otherwise keep IsSubTurnActiveForSpawnCall
							// reporting "active" forever, and this loop
							// would reschedule indefinitely, never emitting
							// a terminal frame for the span. Fail closed:
							// force the synthetic interrupted frame below
							// regardless of what the liveness check
							// reports, matching this codebase's established
							// fail-closed posture elsewhere in delegation
							// gating.
							forceCeiling = true
							slog.Error("ws: subagent span still reports active past the watchdog's reschedule "+
								"ceiling — force-emitting interrupted (fail-closed)",
								"event", "span_orphan_ceiling_exceeded",
								"span_id", entry.spanID,
								"parent_call_id", entry.parentCallID,
								"reason", reason,
								"rechecks", rechecks,
								"max_rechecks", maxRechecks,
							)
						} else {
							// Escalate Debug -> Warn once the loop has
							// re-checked more than once or twice, so a
							// genuinely stuck span (heading toward the
							// ceiling above) is discoverable by an operator
							// without changing production log levels —
							// Debug alone is invisible at typical
							// production log levels.
							logFn := slog.Debug
							if rechecks > 2 {
								logFn = slog.Warn
							}
							logFn("ws: subagent span still genuinely active past watchdog timeout — rescheduling",
								"event", "span_orphan_recheck_still_alive",
								"span_id", entry.spanID,
								"parent_call_id", entry.parentCallID,
								"reason", reason,
								"rechecks", rechecks,
								"max_rechecks", maxRechecks,
							)
							continue
						}
					}
					// Span is still open after timeout AND either the real
					// sub-turn is confirmed no longer active, or the
					// reschedule ceiling was exceeded (forceCeiling, already
					// logged at Error level above). Hand the verdict to the
					// forwarder goroutine, which synthesizes the interrupted
					// end only if the span is STILL open once it has handled
					// every event already queued — see orphanFires.
					select {
					case orphanFires <- orphanFire{entry: entry, reason: reason, forced: forceCeiling}:
					case <-entry.closeCh:
					case <-wc.doneCh:
					case <-forwarderExited:
					}
					return
				}
			}
		}()
	}

	// synthesizeOrphanEnd emits the synthetic interrupted subagent_end for a
	// span a watchdog found orphaned — unless the span's real end closed it
	// while the verdict waited (see orphanFires). Runs only on this goroutine.
	synthesizeOrphanEnd := func(fire orphanFire) {
		entry := fire.entry
		if openSpans[entry.parentCallID] != entry {
			// Closed by its real EventKindSubTurnEnd (or replaced by a newer
			// span under the same call ID) while the verdict waited.
			return
		}
		reason := fire.reason
		switch {
		case fire.forced:
			// Already logged by the watchdog; avoid a second, redundant log line.
		case reason == "unknown":
			slog.Error("ws: subagent span orphaned with unknown reason — synthesizing interrupted end",
				"event", "span_orphan_interrupted",
				"span_id", entry.spanID,
				"parent_call_id", entry.parentCallID,
				"reason", reason,
			)
		default:
			slog.Warn("ws: subagent span orphaned — synthesizing interrupted end",
				"event", "span_orphan_interrupted",
				"span_id", entry.spanID,
				"parent_call_id", entry.parentCallID,
				"reason", reason,
			)
		}
		// Use generated.SubagentEndFrame (contract-first migration).
		endFrame := generated.SubagentEndFrame{
			Type:      string(generated.WsFrameTypeSubagentEnd),
			SessionId: entry.sessionID,
			SpanId:    entry.spanID,
			Status:    "interrupted",
			Message:   &reason,
		}
		if entry.agentID != "" {
			agentID := entry.agentID
			endFrame.AgentId = &agentID
		}
		if entry.parentCallID != "" {
			pc := entry.parentCallID
			endFrame.ParentCallId = &pc
		}
		sendConnGenFrame(wc, string(generated.WsFrameTypeSubagentEnd), endFrame)
		// The span is resolved: a later real end still sends its own frame
		// (the EventKindSubTurnEnd case never needs the entry), and a
		// resolved span is never re-armed or synthesized twice.
		closeSpan(entry.parentCallID)
	}

	for {
		// Settle every watchdog verdict whose already-queued events have all
		// been handled (see orphanFires).
		if len(pendingOrphanFires) > 0 {
			waiting := pendingOrphanFires[:0]
			for _, fire := range pendingOrphanFires {
				if fire.eventsAhead > 0 {
					waiting = append(waiting, fire)
					continue
				}
				synthesizeOrphanEnd(fire)
			}
			pendingOrphanFires = waiting
		}

		var evt agent.Event
		select {
		case received, ok := <-sub.C:
			if !ok {
				return
			}
			evt = received
			for i := range pendingOrphanFires {
				if pendingOrphanFires[i].eventsAhead > 0 {
					pendingOrphanFires[i].eventsAhead--
				}
			}
		case fire := <-orphanFires:
			fire.eventsAhead = len(sub.C)
			pendingOrphanFires = append(pendingOrphanFires, fire)
			continue
		}

		switch evt.Kind {
		case agent.EventKindTurnStart:
			// #605: a NEW root turn began on this chat — reset the
			// root-turn-ended latch so spans it spawns are registered
			// unarmed (their root is alive; the TurnEnd case will arm them).
			// Only a ROOT turn's start may reset: a child's own turn-start
			// arrives between its SubTurnSpawn and the next root turn, and
			// resetting on it would reopen the arming hole for a sibling
			// delegate's later-arriving spawn event.
			p, ok := evt.Payload.(agent.TurnStartPayload)
			if !ok || !p.IsRoot || !matchesEvent(p.ChatID, "") {
				continue
			}
			rootTurnEnded = false
			rootTurnEndReason = ""

		case agent.EventKindSubTurnSpawn:
			// FR-H-004: emit subagent_start when a sub-turn is spawned.
			p, ok := evt.Payload.(agent.SubTurnSpawnPayload)
			if !ok || !matchesEvent(p.ChatID, p.SessionID) {
				continue
			}
			slog.Debug("ws: subagent_start",
				"span_id", p.SpanID,
				"parent_call_id", p.ParentSpawnCallID,
				"agent_id", p.AgentID,
			)
			// Prefer SessionID from payload; fall back to map lookup for legacy events.
			spawnSID := p.SessionID
			if spawnSID == "" {
				spawnSID = sessionIDForChat(p.ChatID)
			}
			// Use generated.SubagentStartFrame (contract-first migration).
			spawnFrame := generated.SubagentStartFrame{
				Type:         string(generated.WsFrameTypeSubagentStart),
				SessionId:    spawnSID,
				SpanId:       p.SpanID,
				ParentCallId: string(p.ParentSpawnCallID),
				TaskLabel:    p.TaskLabel,
			}
			if p.AgentID != "" {
				aid := p.AgentID
				spawnFrame.AgentId = &aid
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeSubagentStart), spawnFrame)
			// Register the span in openSpans for orphan watchdog tracking.
			entry := &openSpanEntry{
				spanID:       p.SpanID,
				parentCallID: string(p.ParentSpawnCallID),
				agentID:      p.AgentID,
				sessionID:    spawnSID,
				closeCh:      make(chan struct{}),
			}
			openSpans[string(p.ParentSpawnCallID)] = entry
			// #605: if the root turn already ended, the EventKindTurnEnd case
			// has already run its arming loop and will never see this entry —
			// arm it now, or the span stays invisible to the orphan watchdog
			// forever (no reschedule ceiling, no forced interrupted frame).
			if rootTurnEnded {
				entry.parentTurnEnded = true
				startOrphanWatchdog(entry, rootTurnEndReason)
			}

		case agent.EventKindSubTurnEnd:
			// FR-H-004: emit subagent_end when a sub-turn finishes.
			p, ok := evt.Payload.(agent.SubTurnEndPayload)
			if !ok || !matchesEvent(p.ChatID, p.SessionID) {
				continue
			}
			slog.Debug("ws: subagent_end",
				"span_id", p.SpanID,
				"parent_call_id", p.ParentSpawnCallID,
				"agent_id", p.AgentID,
			)
			// Prefer SessionID from payload; fall back to map lookup for legacy events.
			endSID := p.SessionID
			if endSID == "" {
				endSID = sessionIDForChat(p.ChatID)
			}
			// Use generated.SubagentEndFrame (contract-first migration).
			endFrameEnd := generated.SubagentEndFrame{
				Type:      string(generated.WsFrameTypeSubagentEnd),
				SessionId: endSID,
				SpanId:    p.SpanID,
				Status:    string(p.Status),
			}
			if p.DurationMS != 0 {
				dm := int(p.DurationMS)
				endFrameEnd.DurationMs = &dm
			}
			if p.AgentID != "" {
				aid := p.AgentID
				endFrameEnd.AgentId = &aid
			}
			if p.ParentSpawnCallID != "" {
				pc := string(p.ParentSpawnCallID)
				endFrameEnd.ParentCallId = &pc
			}
			// FIX 4 (7-reviewer-gate follow-up): surface SubTurnEndPayload.Reason
			// (populated by spawnSubTurn's cleanup defer, pkg/agent/subturn.go,
			// only when Status == "interrupted") as the wire contract's
			// SubagentEndFrame.reason. The frontend (SubagentBlock.tsx) already
			// renders this — it just never received a value before this fix.
			if p.Reason != "" {
				reason := p.Reason
				endFrameEnd.Reason = &reason
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeSubagentEnd), endFrameEnd)
			// Signal the watchdog that the span closed normally.
			closeSpan(string(p.ParentSpawnCallID))

		case agent.EventKindTurnEnd:
			// W1-2: only arm the orphan watchdog when the root turn for this
			// connection ends (IsRoot == true) and the event belongs to our chat
			// (ChatID matches). Sub-turn ends from sibling sub-turns would otherwise
			// spuriously interrupt still-running spans on this connection.
			p, ok := evt.Payload.(agent.TurnEndPayload)
			if !ok || !p.IsRoot || !matchesEvent(p.ChatID, p.SessionID) {
				continue
			}
			// Determine watchdog reason from the terminal status of the parent turn.
			var watchdogReason string
			switch p.Status {
			case agent.TurnEndStatusAborted:
				watchdogReason = "parent_cancelled" //nolint:misspell // wire value, frontend TS union
			case agent.TurnEndStatusError:
				watchdogReason = "parent_timeout"
			case agent.TurnEndStatusCompleted:
				watchdogReason = "parent_done_early"
			case agent.TurnEndStatusParked:
				// Behavior-preserving: this previously fell through the
				// `default` branch below to "unknown" (Parked was not a
				// distinct case). Kept identical here rather than guessing
				// a more specific wire value without frontend confirmation
				// of what consumes it.
				watchdogReason = "unknown"
			default:
				watchdogReason = "unknown"
			}
			// #605: latch the root-turn-ended state for spans whose
			// SubTurnSpawn arrives after this event (see rootTurnEnded decl).
			rootTurnEnded = true
			rootTurnEndReason = watchdogReason
			for _, entry := range openSpans {
				if !entry.parentTurnEnded {
					entry.parentTurnEnded = true
					startOrphanWatchdog(entry, watchdogReason)
				}
			}

		case agent.EventKindToolExecStart:
			p, ok := evt.Payload.(agent.ToolExecStartPayload)
			if !ok || !matchesEvent(p.ChatID, p.SessionID) {
				continue
			}
			// Prefer SessionID from payload; fall back to map lookup for legacy events.
			startSID := p.SessionID
			if startSID == "" {
				startSID = sessionIDForChat(p.ChatID)
			}
			// FR-H-005: propagate parent_call_id when the tool fires inside a sub-turn.
			// FR-I-008: propagate agent_id so live frames match replay frame parity.
			// Nil-safety: params MUST be object (never null) — SPA calls Object.keys(params).
			startArgs := p.Arguments
			if startArgs == nil {
				startArgs = map[string]any{}
			}
			// Use generated.ToolCallStartFrame (contract-first migration).
			startF := generated.ToolCallStartFrame{
				Type:      string(generated.WsFrameTypeToolCallStart),
				SessionId: startSID,
				CallId:    string(p.ToolCallID),
				Tool:      p.Tool,
				Params:    startArgs,
			}
			if p.AgentID != "" {
				aid := p.AgentID
				startF.AgentId = &aid
			}
			if p.ParentSpawnCallID != "" {
				pc := string(p.ParentSpawnCallID)
				startF.ParentCallId = &pc
			}
			// ADR-057 FR-012/FR-013 (W5b): tool_call_start is class (a) — a
			// genuinely child-turn-produced frame (BDD-16; generated.
			// ToolCallStartFrame's own doc comment). startSID above already
			// carries the routing key per ToolExecStartPayload.SessionID's
			// contract (events.go, U3/U9); ProducingSessionID is the emitting
			// turn's own real session, left zero-valued by the emitter when it
			// equals the routing key. Stamp the wire's optional
			// producing_session_id only when it is non-empty AND differs from
			// what was actually placed in SessionId — never "≥ 1" but the
			// FR-013 "present iff it differs" rule, checked against startSID
			// rather than raw p.SessionID so the sessionIDForChat fallback
			// above can never manufacture a false "differs".
			if producingSID := string(p.ProducingSessionID); producingSID != "" && producingSID != startSID {
				startF.ProducingSessionId = &producingSID
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeToolCallStart), startF)
		case agent.EventKindToolExecEnd:
			p, ok := evt.Payload.(agent.ToolExecEndPayload)
			if !ok || !matchesEvent(p.ChatID, p.SessionID) {
				continue
			}
			status := "success"
			if p.IsError {
				status = "error"
			}
			// Prefer SessionID from payload; fall back to map lookup for legacy events.
			evtSID := p.SessionID
			if evtSID == "" {
				evtSID = sessionIDForChat(p.ChatID)
			}
			// FR-H-005: propagate parent_call_id when the tool fires inside a sub-turn.
			// FR-I-008: propagate agent_id so live frames match replay frame parity.
			// Use generated.ToolCallResultFrame (contract-first migration).
			//
			// Apply the lazy-fetch offload policy: when the string result exceeds
			// InlineToolResultMaxBytes (50 KiB), persist it to disk and substitute a
			// generated.ToolResultRef sentinel so the WS frame stays small.
			var liveResult any = p.Result
			// Structured tool failure (UAT fix, extended by ADR-059 W5): some
			// tools emit a typed JSON object as their result rather than prose
			// — a denied delegation (DelegationFailure) or a write_file
			// precondition refusal (FileExistsRefusal). Parse it into a real
			// object so the SPA receives the typed shape it can match on, and
			// lift the human-readable reason into the frame's error field so
			// renderers that show only `error` still show a sentence rather
			// than a JSON blob.
			var structuredErr string
			if status == "error" {
				if obj, reason, isStructured := parseStructuredToolFailure(p.Result); isStructured {
					liveResult = obj
					structuredErr = reason
				}
			}
			if liveResult == any(p.Result) && len(p.Result) > InlineToolResultMaxBytes {
				// JSON-encode the string to get the exact wire size.
				if encoded, merr := json.Marshal(p.Result); merr == nil {
					if sentinel, offloaded := maybeOffloadResult(h.toolStore, evtSID, encoded); offloaded {
						liveResult = sentinel
					}
				}
			}
			resultF := generated.ToolCallResultFrame{
				Type:      string(generated.WsFrameTypeToolCallResult),
				SessionId: evtSID,
				CallId:    string(p.ToolCallID),
				Tool:      p.Tool,
				Result:    liveResult,
				Status:    status,
			}
			if p.Duration != 0 {
				dm := int(p.Duration.Milliseconds())
				resultF.DurationMs = &dm
			}
			if p.AgentID != "" {
				aid := p.AgentID
				resultF.AgentId = &aid
			}
			if p.ParentSpawnCallID != "" {
				pc := string(p.ParentSpawnCallID)
				resultF.ParentCallId = &pc
			}
			// Live/replay error parity (fixes the inverted-parity gap left by
			// RC-5c in pkg/gateway/replay.go's buildResult): that replay
			// reconstruction sets ToolCallResultFrame.Error from tc.Error for
			// EVERY persisted failure (session.ToolCall.Error, populated in
			// pkg/agent/loop.go's runTurn whenever toolResult.IsError and no
			// richer Result was already attached — see tcRecord.Error's own
			// RC-5 comment there), not just delegation denials. Before this,
			// the live path here populated .Error ONLY via the
			// parseStructuredToolFailure special case above, so a failed bash/
			// write_file/etc. call showed NO error live but DID show one
			// after a page reload — the exact opposite of parity. p.Result is
			// ToolExecEndPayload.Result, which loop.go sets to the very same
			// contentForLLM string tcRecord.Error is derived from (pre the
			// persisted side's truncation) — so it is the same string the
			// transcript records.
			//
			// It MUST be truncated to the same bound the persisted side uses,
			// for two independent reasons:
			//
			//  1. SIZE. resultF.Result is subject to the
			//     InlineToolResultMaxBytes offload a few lines above: a result
			//     over 50 KiB is written to disk and replaced with a small
			//     ToolResultRef sentinel, because a multi-megabyte frame can
			//     OOM a constrained client (see maybeOffloadResult). Assigning
			//     the raw p.Result to Error would put the entire string back
			//     into the very same frame, defeating that guard — and the
			//     error path is where large payloads are MOST likely (stderr
			//     dumps, stack traces, build logs).
			//
			//  2. PARITY, which is the whole point of this branch. The
			//     persisted side caps at maxFailClosedOutputChars, so an
			//     untruncated live value means a long error renders one way
			//     live and a different way after a reload — the same class of
			//     divergence this change set out to remove.
			switch {
			case structuredErr != "":
				// Truncated for the same two reasons the comment above states
				// as MUST, and which the branch below already honours: frame
				// size, and parity with the persisted side's own 2000-rune
				// cap. This branch was the one place that skipped it.
				se := truncateRunesForFrame(structuredErr, maxLiveErrorChars)
				resultF.Error = &se
			case status == "error" && p.Result != "" && liveResult != any(p.Result):
				// Only when Result no longer carries the text itself.
				//
				// `liveResult != any(p.Result)` is true exactly when Result was
				// REPLACED above — either offloaded to disk as a ToolResultRef
				// sentinel (over InlineToolResultMaxBytes) or parsed into a
				// structured object. In those cases the frame would otherwise
				// reach the client with no readable reason at all, so Error is
				// the only thing carrying it.
				//
				// When Result IS still the plain string, setting Error would
				// ship the identical text twice in one frame. That is pure
				// duplication: the SPA already has the reason in Result.
				//
				// Replay is unaffected and still sets Error unconditionally
				// from the persisted record — it must, because on the persisted
				// side Result is nil for ordinary tool failures (only media and
				// synchronous delegate calls populate it), so Error is the ONLY
				// carrier there. That asymmetry is deliberate: each path sets
				// Error precisely when its own Result cannot carry the reason.
				liveErr := truncateRunesForFrame(p.Result, maxLiveErrorChars)
				resultF.Error = &liveErr
			}
			// ADR-057 FR-012/FR-013 (W5b): tool_call_result is class (a) —
			// genuinely child-turn-produced (BDD-16; generated.
			// ToolCallResultFrame's own doc comment). See the tool_call_start
			// stamping above for the identical "present iff it differs from
			// what's actually on the wire" contract.
			var producingSIDForResult string
			if producingSID := string(p.ProducingSessionID); producingSID != "" && producingSID != evtSID {
				resultF.ProducingSessionId = &producingSID
				producingSIDForResult = producingSID
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeToolCallResult), resultF)
			// When switch_agent succeeds, notify the frontend to switch agents.
			// Use evtSID (the session ID from the payload) to key the lookup, not chatID.
			//
			// ADR-057 FR-089 (W5 audit): agent_switched is class (a), not
			// "class not yet assigned" (generated.AgentSwitchedFrame's doc
			// comment pre-audit) — it is derived from THIS SAME
			// ToolExecEndPayload, at the exact call site whose tool_call_result
			// sibling is already verified class (a): a delegated child can
			// invoke switch_agent on its OWN session exactly as a root turn
			// can, so evtSID here is the CHILD's own producing session
			// whenever switch_agent ran inside a sub-turn, distinct from the
			// routing key placed in SessionId below. Reuses
			// producingSIDForResult computed above rather than re-deriving it,
			// since both frames answer the identical "does this ToolExecEnd's
			// producer differ from its routing key" question.
			//
			// ADR-071 §5.2.1/§5.2.2: this used to be TWO exact-string
			// branches (p.Tool == "hand_off" and p.Tool == "return_to_default"),
			// one per retired tool. D4 merged both into one tool name with no
			// arguments in ToolExecEndPayload to distinguish which branch ran
			// (agent.ToolExecEndPayload carries no tool-arguments field).
			//
			// The semantic is NOT re-derived from the resulting agent id
			// (§5.2.2 decision A's original approach: comparing the
			// session's post-switch active agent against the registry's
			// default agent id) — that comparison misreports an explicit
			// switch_agent(target:"<id>") that happens to name the CURRENT
			// default agent as a return-to-default, since the resulting
			// AgentID is identical in both cases. Instead this reads the
			// tool's own toDefault intent back via GetLastSwitchToDefault,
			// populated synchronously by onHandoffFrontend (pkg/agent/loop.go)
			// from tools.HandoffEvent.ToDefault before this ToolExecEnd event
			// is even emitted, keyed the same way GetSessionActiveAgent is.
			if p.Tool == "switch_agent" && status == "success" {
				defaultAgent := h.agentLoop.GetRegistry().GetDefaultAgent()
				var defaultName string
				if defaultAgent != nil {
					defaultName = defaultAgent.Name
				}
				activeAgent, activeOk := h.agentLoop.GetSessionActiveAgent(evtSID)
				toDefault, sawToDefault := h.agentLoop.GetLastSwitchToDefault(evtSID)
				if !activeOk {
					// After a SUCCESSFUL switch this is an invariant
					// violation, not a normal path (§5.2.2) — WARN rather
					// than silently emitting nothing, so this is
					// distinguishable in logs from the exact regression
					// this section exists to prevent. Still emit a frame
					// below (defaulting to the "returned to default" shape)
					// rather than dropping it — the sibling
					// return_to_default branch never had this guard and
					// always emitted.
					slog.Warn("websocket: switch_agent succeeded but no active agent found for session",
						"session_id", evtSID)
				}
				if !sawToDefault {
					// Should not happen on the success path — onHandoffFrontend
					// stores this before Execute returns, strictly before this
					// event fires. Fall back to the old id-comparison so a
					// frame still emits (best-effort) rather than silently
					// dropping, and make the anomaly visible.
					slog.Warn("websocket: switch_agent succeeded but no toDefault record found for session; falling back to id comparison",
						"session_id", evtSID)
					toDefault = !activeOk || activeAgent == "" || (defaultAgent != nil && activeAgent == defaultAgent.ID)
				}
				switchF := generated.AgentSwitchedFrame{
					Type:      string(generated.WsFrameTypeAgentSwitched),
					SessionId: evtSID,
				}
				if activeOk && activeAgent != "" && !toDefault {
					// Named-target switch.
					agentName, _ := h.agentLoop.GetRegistry().GetAgentName(activeAgent)
					switchF.AgentId = &activeAgent
					if agentName != "" {
						switchF.Message = &agentName
					}
				} else {
					// Returned to default (or the active-agent lookup was
					// unavailable — best-effort default shape per the WARN
					// above). AgentId omitted (nil ptr) = return to default
					// agent.
					if defaultName != "" {
						switchF.Message = &defaultName
					}
				}
				if producingSIDForResult != "" {
					pid := producingSIDForResult
					switchF.ProducingSessionId = &pid
				}
				sendConnGenFrame(wc, string(generated.WsFrameTypeAgentSwitched), switchF)
			}
		case agent.EventKindRateLimit:
			// SEC-26: forward rate-limit denials to the browser so the chat UI
			// can display an inline indicator. Global-scope events (daily cost
			// cap) are broadcast to every connection since they are not tied
			// to a specific chatID.
			p, ok := evt.Payload.(agent.RateLimitPayload)
			if !ok {
				continue
			}
			// Prefer the routing session stamped on the payload so a
			// second tab / reload attached to the same session still
			// sees the denial. ChatID alone is a dead webchat: uuid
			// after ServeHTTP mints a new connection.
			rateSID := p.SessionID
			if rateSID == "" {
				rateSID = sessionIDForChat(p.ChatID)
			}
			if p.Scope != "global" && !matchesEvent(p.ChatID, rateSID) {
				continue
			}
			// Use generated.RateLimitFrame (contract-first migration).
			rateF := generated.RateLimitFrame{
				Type:              string(generated.WsFrameTypeRateLimit),
				SessionId:         rateSID,
				Scope:             p.Scope,
				Resource:          p.Resource,
				PolicyRule:        p.PolicyRule,
				RetryAfterSeconds: p.RetryAfterSeconds,
			}
			if p.AgentID != "" {
				aid := p.AgentID
				rateF.AgentId = &aid
			}
			if p.Tool != "" {
				tool := p.Tool
				rateF.Tool = &tool
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeRateLimit), rateF)
		case agent.EventKindError:
			// ADR-051 §RD6: forward translated provider/LLM errors to the
			// browser so the chat UI can render the typed ErrorFrame inline
			// (Code/Retryable/Detail) instead of the raw provider text.
			//
			// NO CODE IS SUPPRESSED HERE — and `rate_limited` least of all.
			// This arm used to end with an unconditional
			// `if code == agent.CodeRateLimited { continue }`, justified as
			// "the dedicated RateLimitFrame above is authoritative for that
			// class". That justification was false, and it cost a user their
			// only signal: an upstream HTTP 429 produced a turn that opened,
			// said nothing, and closed reporting success.
			//
			// The two mechanisms share a code NAME but not a producer:
			//
			//   - EventKindRateLimit (the arm above) has EXACTLY ONE producer,
			//     AgentLoop.recordRateLimitDenial (pkg/agent/loop.go), called
			//     from two sites both guarded on Omnipus's OWN internal SEC-26
			//     limiter being configured (cfg.Sandbox.RateLimits.MaxAgent{
			//     LLMCallsPerHour,ToolCallsPerMinute} > 0). It means "Omnipus
			//     denied this".
			//   - An UPSTREAM refusal never reaches that function at all. It
			//     travels runTurn's LLM-error block, which emits EventKindError
			//     with Code: "rate_limited". It means "the provider denied
			//     this" — a different fact with a different remedy (wait /
			//     retry / switch model, not raise your own cap).
			//
			// So the suppression could never de-duplicate anything: it only
			// ever deleted the provider case, with nothing replacing it.
			//
			// Nor is a dual-emit lurking behind it. recordRateLimitDenial emits
			// ONE event, and its doc comment records that the prior
			// "EventKindError + RateLimitPayload + EventKindRateLimit"
			// dual-emit was deliberately removed as bus pollution — that
			// removal was correct and stays. Even reinstated in its old shape
			// it could not reach this frame: it carried a RateLimitPayload,
			// which the ErrorPayload type assertion immediately below already
			// rejects. Dedup belongs at the producer (one event per denial),
			// not here — pinned end-to-end by
			// TestEventForwarder_InternalRateLimitDenial_EmitsExactlyOneFrame.
			p, ok := evt.Payload.(agent.ErrorPayload)
			if !ok {
				continue
			}
			// Prefer the routing session stamped on the payload (survives
			// reload: the originating chatID is a dead webchat: uuid).
			// Fall back to the live chatID→session map for older emitters
			// that only set ChatID.
			errSID := p.SessionID
			if errSID == "" {
				errSID = sessionIDForChat(p.ChatID)
			}
			if !matchesEvent(p.ChatID, errSID) {
				continue
			}
			// FIX 2: prefer the already-computed p.Code/p.Message over a
			// fresh TranslateLLMError call. Every ErrorPayload construction
			// site now populates Code (pkg/agent's FIX 3) alongside a
			// Message that is EITHER the classifier's own generic copy OR —
			// for trusted internal stages (hook aborts, model-switch
			// failures, session save/restore, synthetic-error-floor,
			// external-CLI sanitized text) — caller-curated text that must
			// reach the wire verbatim. This mirrors appendErrorTranscript's
			// write-choke-point behavior (pkg/agent/turn.go): re-running
			// TranslateLLMError against already-curated text here would
			// re-classify it against the generic message catalog and
			// silently replace the curated copy with boilerplate whenever
			// the text happened to contain a pinned substring (e.g. a hook
			// abort reason mentioning "safety") — exactly the live-vs-replay
			// divergence this closes. Only fall back to a fresh translation
			// when a call site left Code empty (defensive — after FIX 3
			// every production site sets it).
			translated := agent.TranslateLLMError(p.ProviderError, p.Message)
			code := translated.Code
			message := translated.Message
			retryable := translated.Retryable
			detail := translated.Detail
			if p.Code != "" {
				code = agent.LLMErrorCode(p.Code)
				message = p.Message
				retryable = agent.IsRetryableCode(code)
				// FIX 2 (re-review): detail must follow the same
				// curated-preferred rule as code/message/retryable above,
				// not silently stay pinned to the fresh-classification
				// value computed a few lines up. Recomputing from
				// (p.ProviderError, message) — message is already the
				// curated p.Message reassigned just above — is a no-op
				// TODAY (every curated site passes ProviderError: nil, so
				// agent.BuildDetail(nil, msg) echoes msg exactly like
				// translated.Detail already does), but stops being one the
				// day a curated site pairs a curated Code+Message with a
				// non-nil ProviderError: buildDetail favors pe.Status/
				// pe.Body over the message argument once pe != nil, so
				// leaving this pinned to `translated.Detail` would render a
				// diagnostic string that was never validated against the
				// curated Code/Message this frame actually carries.
				detail = agent.BuildDetail(p.ProviderError, message)
			}
			errF := generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				SessionId: &errSID,
				Message:   message,
			}
			errF.Payload = &generated.ErrorPayload{
				LlmError: generated.LLMError{
					Code:      string(code),
					Message:   message,
					Retryable: retryable,
					Detail:    &detail,
				},
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeError), errF)
		case agent.EventKindWhatsAppPairing:
			// #283: WhatsApp linked-device pairing (QR + status). Not tied to a
			// chatID. Delivered only to connections that subscribed to this
			// channel's pairing UI (Option B), so the QR pairing secret isn't
			// broadcast to every connected tab.
			p, ok := evt.Payload.(agent.WhatsAppPairingPayload)
			if !ok {
				continue
			}
			pairF := generated.WhatsAppPairingFrame{
				Type:      string(generated.WsFrameTypeWhatsappPairing),
				ChannelId: p.ChannelID,
				Status:    string(p.Status),
			}
			if p.QR != "" {
				qr := p.QR
				pairF.Qr = &qr
			}
			if p.Message != "" {
				msg := p.Message
				pairF.Message = &msg
			}
			// #368: maintain the per-channel QR cache so late subscribers (e.g.
			// a tab that opens the pairing UI after the first QR fires) receive
			// the last-seen code immediately on subscribe rather than waiting for
			// the next QR rotation.  Only "code" (QR available) is cached;
			// terminal states are evicted so stale QRs are not re-emitted.
			switch p.Status {
			case channels.PairingStatusCode:
				if frameBytes, merr := json.Marshal(pairF); merr == nil {
					h.lastPairingState.Store(p.ChannelID, frameBytes)
				} else {
					slog.Error("ws: failed to marshal whatsapp_pairing frame for cache",
						"channel_id", p.ChannelID, "error", merr)
				}
			case channels.PairingStatusLinked, channels.PairingStatusTimeout, channels.PairingStatusError,
				channels.PairingStatusWaiting:
				// PairingStatusWaiting and any other status that is not
				// "code" must not leave a stale QR in the cache — evict so a
				// late subscriber is not shown an outdated code.
				h.lastPairingState.Delete(p.ChannelID)
			default:
				// Any future status not yet in this switch: same fail-safe
				// eviction as above, so an unrecognized status never leaves
				// a stale QR behind.
				h.lastPairingState.Delete(p.ChannelID)
			}
			if !wc.wantsPairing(p.ChannelID) {
				continue
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeWhatsappPairing), pairF)
		case agent.EventKindNotification:
			// #264: a user-facing notification (e.g. a scheduled run failed).
			// Delivered ONLY to the recipient user's connections (filtered by
			// wc.userID) so it never leaks to other tabs/sessions. The
			// NotificationAdminBroadcast sentinel fans out to every connected
			// client unconditionally when no specific recipient could be
			// resolved — under the single-user model, "broadcast to admins" and
			// "broadcast to the one account's connections" are the same thing.
			p, ok := evt.Payload.(agent.NotificationPayload)
			if !ok {
				continue
			}
			if p.Recipient != agent.NotificationAdminBroadcast && wc.userID != p.Recipient {
				continue
			}
			notifF := generated.NotificationFrame{
				Type:             string(generated.WsFrameTypeNotification),
				Id:               p.ID,
				NotificationType: p.NotificationType,
				Title:            p.Title,
				Severity:         p.Severity,
				Read:             p.Read,
				CreatedAtMs:      p.CreatedAtMs,
			}
			if p.Body != "" {
				body := p.Body
				notifF.Body = &body
			}
			if p.ScheduleID != "" {
				sid := p.ScheduleID
				notifF.ScheduleId = &sid
			}
			if p.SessionID != "" {
				ses := p.SessionID
				notifF.SessionId = &ses
			}
			if p.AgentID != "" {
				aid := p.AgentID
				notifF.AgentId = &aid
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeNotification), notifF)
		case agent.EventKindTaskStatusChanged:
			// A workflow task's status changed (queued→running→completed/failed).
			// Not tied to a specific chatID — broadcast to every connection so
			// anyone viewing the tasks board sees live updates. The SPA
			// invalidates its tasks TanStack Query cache on receipt.
			p, ok := evt.Payload.(agent.TaskStatusChangedPayload)
			if !ok {
				continue
			}
			taskF := generated.TaskStatusChangedFrame{
				Type:      string(generated.WsFrameTypeTaskStatusChanged),
				SessionId: p.SessionID,
				TaskId:    p.TaskID,
				Status:    p.Status,
			}
			if p.AgentID != "" {
				aid := p.AgentID
				taskF.AgentId = &aid
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeTaskStatusChanged), taskF)
		case agent.EventKindPlanStatusChanged:
			// ADR-049 D4/D7: a Plan's state/phase/progress/paused_reason changed.
			// Not tied to a specific chatID (a Plan is workspace-scoped, not
			// session-scoped) — broadcast to every connection, mirroring
			// EventKindTaskStatusChanged above. The SPA invalidates its plans
			// query cache / updates the plan card on receipt.
			p, ok := evt.Payload.(agent.PlanStatusChangedPayload)
			if !ok {
				continue
			}
			planF := generated.PlanStatusFrame{
				Type:      string(generated.WsFrameTypePlanStatus),
				PlanId:    p.PlanID,
				State:     p.State,
				PlanPhase: p.PlanPhase,
				Progress:  p.Progress,
			}
			if p.PausedReason != "" {
				pr := p.PausedReason
				planF.PausedReason = &pr
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypePlanStatus), planF)
		case agent.EventKindGoalStatusChanged:
			// ADR-049 D6/D7: a session's `/goal` loop status changed (set,
			// round advance, met, bound reached, cleared). Broadcast to every
			// connection, mirroring EventKindPlanStatusChanged — the SPA
			// matches session_id client-side to the currently open session.
			p, ok := evt.Payload.(agent.GoalStatusChangedPayload)
			if !ok {
				continue
			}
			goalF := generated.GoalStatusFrame{
				Type:         string(generated.WsFrameTypeGoalStatus),
				SessionId:    p.SessionID,
				Condition:    p.Condition,
				Round:        p.Round,
				MaxRounds:    p.MaxRounds,
				LatestReason: p.LatestReason,
				ActiveLoops:  p.ActiveLoops,
				Cap:          p.Cap,
				State:        p.State,
			}
			// ADR-053 R§8.11 / UAT S3 fix: goal_id disambiguates which goal
			// generation this frame updates so the SPA's GoalPillTray can key
			// one pill per goal-id instead of collapsing every goal a session
			// ever carried into the `_default` bucket. Optional on the wire —
			// omitted for a legacy pre-upgrade goal that never had one minted.
			if p.GoalID != "" {
				gid := p.GoalID
				goalF.GoalId = &gid
			}
			// ADR-074 D5.2 / FR-011: the compiled criteria breakdown rides the
			// `queued` (pending-confirm) emission so the SPA's echo card can
			// itemize exactly what will run (commands verbatim). Optional on
			// the wire — absent (nil) on every other emission.
			setGoalStatusCriteria(&goalF, p.Criteria)
			// ADR-080 D-STATEMENT/D-DOD: the restated goal statement and the
			// Definition-of-Done breakdown ride the SAME `queued` emission as
			// Criteria above — both optional on the wire, absent on every
			// other emission (goal_loop.go's emitGoalStatusFrameWithCriteriaAndDoD
			// only ever populates them on the pending-confirm push).
			if p.Definition != "" {
				def := p.Definition
				goalF.Definition = &def
			}
			setGoalStatusDoD(&goalF, p.DoD)
			sendConnGenFrame(wc, string(generated.WsFrameTypeGoalStatus), goalF)
		case agent.EventKindGoalOutcome:
			// A goal ENDED (founder decision 2026-09-14): the lasting outcome
			// line. pkg/agent emits this right after saving the matching
			// `system_subtype: goal_outcome` transcript entry, with that
			// entry's id as message_id, so this live frame, the replayed one
			// (replay.go) and a cold REST load converge on one thread line.
			// Broadcast like goal_status above; the SPA routes it by
			// session_id (SESSION_SCOPED_FRAME_TYPES).
			p, ok := evt.Payload.(agent.GoalOutcomePayload)
			if !ok {
				continue
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeGoalOutcome),
				goalOutcomeFrame(p.SessionID, p.MessageID, p.Outcome))
		case agent.EventKindJudgeVerdict:
			// pkg/agent emits this right after saving the matching
			// `judge_verdict` transcript entry (task_executor.go's
			// writeJudgeVerdictTranscript, goal_loop.go's
			// writeGoalVerdictTranscript). Broadcast to every connection like
			// goal_outcome above — toJudgeVerdictFrame (replay.go) is the ONE
			// conversion both this live push and replay use, so the two
			// frames for one round can never differ. p.SessionID is "" for a
			// scope=plan verdict (never emitted today) — toJudgeVerdictFrame
			// leaves session_id absent on the wire in that case, and the SPA
			// keeps today's GLOBAL panel-only routing for it.
			p, ok := evt.Payload.(agent.JudgeVerdictPayload)
			if !ok {
				continue
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeJudgeVerdict),
				toJudgeVerdictFrame(p.SessionID, p.Verdict))
		case agent.EventKindLoopStatusChanged:
			// ADR-049 D6/D7: a session's `/loop` status changed (set, run
			// fired, run-cap reached, stop). Broadcast to every connection,
			// mirroring EventKindGoalStatusChanged above.
			p, ok := evt.Payload.(agent.LoopStatusChangedPayload)
			if !ok {
				continue
			}
			loopF := generated.LoopStatusFrame{
				Type:      string(generated.WsFrameTypeLoopStatus),
				SessionId: p.SessionID,
				Mode:      p.Mode,
				Run:       p.Run,
				MaxRuns:   p.MaxRuns,
				State:     p.State,
			}
			if p.NextDelay != nil {
				nd := int64(*p.NextDelay)
				loopF.NextDelay = &nd
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeLoopStatus), loopF)
		case agent.EventKindTaskRunStatus:
			// A per-execution run opened or closed (ADR-050). Broadcast so the
			// calendar's per-occurrence chip updates live without a full refetch.
			// occurrence_ms is nil for an ad-hoc/once/manual run.
			p, ok := evt.Payload.(agent.TaskRunStatusPayload)
			if !ok {
				continue
			}
			runF := generated.TaskRunStatusFrame{
				Type:   string(generated.WsFrameTypeTaskRunStatus),
				TaskId: p.TaskID,
				RunId:  p.RunID,
				Status: p.Status,
			}
			if p.OccurrenceMs != nil {
				ms := *p.OccurrenceMs
				runF.OccurrenceMs = &ms
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeTaskRunStatus), runF)

		case agent.EventKindToolResultProjection:
			// ADR-066 D5 / FR-022 (T066-12): a tool result this session already
			// received was emptied in place in the model's window. Push the
			// typed tool_result_projection frame so the SPA re-renders the
			// matching tool call (the mark only under Verbose chat); on reload
			// the same state arrives as ToolCall.content_state on the
			// transcript. Session-scoped: same matchesEvent / session-id
			// contract as tool_call_result (ToolExecEndPayload).
			p, ok := evt.Payload.(agent.ToolResultProjectionPayload)
			if !ok || !matchesEvent(p.ChatID, p.SessionID) {
				continue
			}
			projSID := p.SessionID
			if projSID == "" {
				projSID = sessionIDForChat(p.ChatID)
			}
			projF := generated.ToolResultProjectionFrame{
				Type:         string(generated.WsFrameTypeToolResultProjection),
				SessionId:    projSID,
				ToolCallId:   string(p.ToolCallID),
				ArchiveLine:  p.ArchiveLine,
				ContentState: p.ContentState,
			}
			if p.Mark != "" {
				mark := p.Mark
				projF.Mark = &mark
			}
			if producingSID := string(p.ProducingSessionID); producingSID != "" && producingSID != projSID {
				projF.ProducingSessionId = &producingSID
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeToolResultProjection), projF)
		case agent.EventKindLLMRequest, agent.EventKindLLMDelta, agent.EventKindLLMResponse,
			agent.EventKindLLMRetry, agent.EventKindContextCompress,
			agent.EventKindToolExecSkipped, agent.EventKindSteeringInjected, agent.EventKindFollowUpQueued,
			agent.EventKindInterruptReceived, agent.EventKindSubTurnResultDelivered, agent.EventKindSubTurnOrphan,
			agent.EventKindTurnTimeout, agent.EventKindEmptyResponseRetry, agent.EventKindCompactionRetry,
			agent.EventKindBackgroundProcessKill:
			// Not part of the live WS wire protocol — this forwarder only
			// translates the kinds handled above into browser frames.
			// Behavior-preserving: previously these fell through the switch
			// unmatched (no default case existed), which is a silent no-op
			// identical to this explicit, empty case.
		}
	}
}

// wsTranscriptWriteFailures counts how many times wsStreamer.Finalize's
// streamed-assistant-message audit write via AppendTranscriptStrict failed
// against a session id that does not resolve to a real, store-backed session
// (ADR-057 FR-001/FR-002/W3, spec BDD-03 row `pkg/gateway/websocket.go:4256`
// "streamed assistant"). Mirrors pkg/agent/turn.go's transcriptWriteFailures
// and pkg/tools/handoff.go's handoffTranscriptWriteFailures — each unit that
// owns a converted call site gets its own package-local counter rather than
// sharing one across package boundaries. The write itself stays best-effort
// by design (a failed streamed-transcript record must never fail the turn
// that already streamed successfully to the client); this counter is the
// only durable, operator-visible signal that it happened. Exposed via
// WSTranscriptWriteFailures() for tests and operator tooling.
var wsTranscriptWriteFailures atomic.Uint64

// WSTranscriptWriteFailures returns the current value of the
// omnipus_ws_transcript_write_failures_total counter (ADR-057 FR-002).
func WSTranscriptWriteFailures() uint64 {
	return wsTranscriptWriteFailures.Load()
}

// wsHandler returns the *WSHandler this streamer resolves live connections
// through (ADR-082 D2), or nil for a bare test fixture with neither wired.
// Prefers s.h — set DIRECTLY by GetStreamer to its own receiver — over
// s.channel.wsHandler. This distinction matters: s.channel (*webchatChannel)
// is wired onto WSHandler.webchatCh only by the gateway's production boot
// sequence (gateway.go, AFTER newWSHandler returns), so a test harness that
// constructs a *WSHandler directly (very common — most of this package's
// tests never call the production boot path) leaves it nil even though the
// handler itself is perfectly real and live. Deriving connection resolution
// SOLELY from s.channel.wsHandler would silently degrade every such
// GetStreamer-driven streaming test to the connection-less bare-fixture path
// — no tokens delivered anywhere, a real regression this field prevents. s.h
// is always set by GetStreamer regardless of whether webchatCh happens to be
// wired; s.channel.wsHandler remains the fallback for a caller that
// constructs a wsStreamer literal directly (as many tests in this package
// do) with only `channel` set.
func (s *wsStreamer) wsHandler() *WSHandler {
	if s.h != nil {
		return s.h
	}
	if s.channel != nil {
		return s.channel.wsHandler
	}
	return nil
}

// streamOwnerClaim is the value stored in WSHandler.streamOwners: which turn
// holds a chatID's live-stream slot, and when it claimed it. claimedAt backs
// claimStreamOwnership's stale-claim force-reclaim safety net.
type streamOwnerClaim struct {
	turnID    string
	claimedAt time.Time
}

// streamOwnershipStaleAfter bounds how long an unreleased live-stream
// ownership claim is honored before a new claimant on the same chatID may
// force-reclaim it. Deliberately generous — every real release path
// (Finalize, Cancel, the abandoned-turn early return) frees the claim
// immediately, well within this window — this exists purely as a backstop
// against a future bug in this family leaving a claim permanently
// unreleased, so such a leak degrades to "briefly wrong attribution" rather
// than "permanently mute chat" (see WSHandler.streamOwners' doc comment).
const streamOwnershipStaleAfter = 10 * time.Minute

// claimStreamOwnership attempts to claim (or re-confirm) sessionID's live
// TokenFrame-delivery slot in owners for turnID. See WSHandler.streamOwners'
// doc comment for the full rationale, including why this is keyed by
// sessionID rather than the originating chatID (ADR-082 review F6). Returns
// true when turnID owns the slot — either because it just claimed an empty
// slot, because it already owned it (a single turn typically opens several
// sequential wsStreamer instances across its own tool-calling iterations —
// see turnState.lastStreamer/finalizeStreamer in pkg/agent/turn.go — and
// each must see itself as "still the owner", not a foreign claimant), or
// because the existing claim is older than streamOwnershipStaleAfter and was
// force-reclaimed. Returns false only when a DIFFERENT, still-fresh turnID
// already owns the slot. The generic `owners *sync.Map` / string-key
// signature (unchanged by the sessionID rename) is also exercised directly
// by unit tests with arbitrary string keys — see
// TestClaimStreamOwnership_StaleClaimIsForceReclaimed and its sibling.
func claimStreamOwnership(owners *sync.Map, sessionID, turnID string) bool {
	now := time.Now()
	newClaim := streamOwnerClaim{turnID: turnID, claimedAt: now}
	actual, loaded := owners.LoadOrStore(sessionID, newClaim)
	for {
		if !loaded {
			return true
		}
		claim, ok := actual.(streamOwnerClaim)
		if !ok || claim.turnID == turnID {
			return true
		}
		if now.Sub(claim.claimedAt) < streamOwnershipStaleAfter {
			return false
		}
		// The existing claim is stale — force-reclaim it. CompareAndSwap only
		// succeeds if the entry is still exactly what we last observed, so a
		// concurrent claimant racing us here safely retries instead of both
		// believing they own the slot.
		if owners.CompareAndSwap(sessionID, actual, newClaim) {
			return true
		}
		actual, loaded = owners.Load(sessionID)
	}
}

// releaseStreamOwnershipClaim releases turnID's live-stream ownership claim
// for sessionID in owners, if it currently holds it. A no-op when turnID
// never held the claim (e.g. a shadow stream, or a turn that never called
// Update()) or when it has already been released or force-reclaimed by a
// stale-claim takeover. Load-then-CompareAndDelete rather than a bare delete
// so a concurrent stale-claim reclaim racing this release can never clobber
// a different, newer claimant's entry.
func releaseStreamOwnershipClaim(owners *sync.Map, sessionID, turnID string) {
	if turnID == "" {
		return
	}
	actual, ok := owners.Load(sessionID)
	if !ok {
		return
	}
	claim, ok := actual.(streamOwnerClaim)
	if !ok || claim.turnID != turnID {
		return
	}
	owners.CompareAndDelete(sessionID, actual)
}

// SetProducedModel stamps the model string that produced this streamed
// response. Called by the agent loop before Finalize so the assistant
// transcript entry carries the per-turn Model field (FR-013). Empty model
// is treated as "not recorded" by the UI; the UI omits the model span
// entirely per FR-014 (no placeholder).
func (s *wsStreamer) SetProducedModel(model string) {
	s.statsMu.Lock()
	s.producedModel = strings.TrimSpace(model)
	s.statsMu.Unlock()
}

// SetProducerAgentID overrides the streamer's attributed agent with the TRUE
// per-turn producer (ts.agent.ID). Called by the agent loop (via the inline
// SetProducerAgentID interface, mirroring the SetProducedModel/
// SuppressTranscriptWrite pattern) immediately after obtaining the streamer
// for an LLM streaming call, before any Update/Finalize can observe it.
//
// FIX 5a: without this, both the live TokenFrame.AgentId (Update) and the
// persisted transcript entry (Finalize) fall back to the "active session
// agent" guess computed at GetStreamer time — correct for an ordinary turn,
// but wrong for a background/delegated sub-turn, where ADR-032 guarantees
// the delegate runs as its own identity, never the parent's. A no-op on an
// empty agentID so a caller that genuinely has no resolved agent (should not
// happen in practice) cannot blank out the GetStreamer-time guess.
func (s *wsStreamer) SetProducerAgentID(agentID string) {
	if agentID == "" {
		return
	}
	s.statsMu.Lock()
	s.agentID = agentID
	s.statsMu.Unlock()
}

// SetTurnID stamps the turn ID that will be attributed to the transcript
// entry Finalize writes. Called by the agent loop (via the inline SetTurnID
// interface, mirroring SetProducerAgentID exactly) immediately after
// obtaining the streamer for an LLM streaming call, before any Update/
// Finalize can observe it.
//
// FIX 5c/1: without this, the assistant entry Finalize writes carries no
// TurnID, so a mid-stream cancel's turn_canceled entry (which DOES carry
// TurnID — see pkg/agent/cancel.go) can never be correlated with the
// assistant message it interrupted on replay, and
// MarkLastEntryTruncated's own turn-scoped matching can never find the
// entry to flag. A no-op on an empty turnID so a caller with no resolved
// turn ID (should not happen in practice) cannot blank out an
// already-stamped value.
func (s *wsStreamer) SetTurnID(turnID string) {
	if turnID == "" {
		return
	}
	s.statsMu.Lock()
	s.turnID = turnID
	s.statsMu.Unlock()
}

// SetParentSpawnCallID stamps the delegation-nesting correlation that will be
// attributed to the transcript entry Finalize writes. Called by the agent
// loop (via the inline SetParentSpawnCallID interface, mirroring SetTurnID
// exactly) immediately after obtaining the streamer for an LLM streaming
// call, before any Update/Finalize can observe it.
//
// Unlike SetTurnID/SetProducerAgentID, an EMPTY parentSpawnCallID is a valid,
// common value — it means "this is a root turn, not a delegation child" —
// so, unlike those two setters, this one does NOT no-op on empty; it always
// stamps whatever the caller passes (including clearing back to "" for a
// caller that resolves no parent span, which should never blank out a
// previously-stamped value in practice since each wsStreamer instance is
// single-use for one turn, but matches the field's own zero-value semantics
// rather than silently refusing a legitimate "no parent" write).
func (s *wsStreamer) SetParentSpawnCallID(parentSpawnCallID string) {
	s.statsMu.Lock()
	s.parentSpawnCallID = parentSpawnCallID
	s.statsMu.Unlock()
}

// SuppressTranscriptWrite marks this streamer so its Finalize skips the
// transcript-append block. The agent loop calls this (via the inline
// SuppressTranscriptWrite interface) after it has already persisted the round's
// narration through appendIntermediateAssistantTranscript (#416 gate fix).
func (s *wsStreamer) SuppressTranscriptWrite() {
	s.statsMu.Lock()
	s.transcriptPersisted = true
	s.statsMu.Unlock()
}

// StreamedContentLen reports how many bytes of streamed content this streamer
// has already emitted to the client for the current attempt. The agent loop's
// inline-retry guard uses this to avoid re-streaming a full response onto a
// partially-streamed bubble after a mid-stream transport drop (which would
// visibly duplicate text in the SPA, since the dropped attempt sent no `done`
// frame). ADR-082: accumulated moved from statsMu to the WSHandler's own mu
// (see the field's doc comment) — guarded here the same way, so the read
// stays race-free across goroutines.
func (s *wsStreamer) StreamedContentLen() int {
	if h := s.wsHandler(); h != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		return s.accumulated.Len()
	}
	return s.accumulated.Len()
}

// SetTurnStats is called by the agent loop's finalizeStreamer just before
// Finalize. Implements the streamerStatsSetter interface from pkg/agent.
func (s *wsStreamer) SetTurnStats(tokens int64, costUSD float64, duration time.Duration) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.statsTokens = tokens
	s.statsCostUSD = costUSD
	s.statsDuration = duration
}

// SetTurnIOStats receives the provider's input/output and cache token split
// from the agent loop's finalizeStreamer (streamerIOStatsSetter).
//
// SetTurnStats above carries only a collapsed total, which is why a streamed
// turn used to persist an entry with no split at all — leaving tokens_in at 0
// for every webchat session.
func (s *wsStreamer) SetTurnIOStats(promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens int) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.statsPromptTokens = promptTokens
	s.statsCompletionTokens = completionTokens
	s.statsCacheRead = cacheReadTokens
	s.statsCacheWrite = cacheWriteTokens
}

// SetTurnFailed is called by the agent loop's finalizeStreamer when the turn
// ended via the engine's error/limit fallback rather than a real model response.
// Conditions that set the flag: (1) LLM returned empty after retries and the
// engine substituted its defaultResponse sentinel; (2) tool-iteration limit
// reached; (3) generic empty-content exhaustion resolved to the defaultResponse
// sentinel (excludes caller-supplied success strings like the heartbeat path).
// Implements the streamerFailedSetter interface from pkg/agent. The flag is
// emitted in the done frame as DoneStats.TurnFailed so CLI/automation clients
// can exit non-zero.
func (s *wsStreamer) SetTurnFailed(failed bool) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.statsTurnFailed = failed
}

// SetTruncation stamps ADR-087 D2's truncation reason onto this streamer so
// Finalize can persist Truncated/TruncationReason on the transcript entry it
// writes — the D4a zero-content case and the D4b "annotate the accumulated
// answer" case both route through here. Implements the
// streamerTruncationSetter interface from pkg/agent, called by
// finalizeStreamer's probe (pkg/agent/turn.go) immediately BEFORE Finalize,
// mirroring SetContinuationContent's calling convention exactly. A no-op on
// an empty reason so a caller with nothing to stamp cannot blank out an
// already-set value.
func (s *wsStreamer) SetTruncation(reason string) {
	if reason == "" {
		return
	}
	s.statsMu.Lock()
	s.truncationReason = reason
	s.statsMu.Unlock()
}

// SetContinuationContent stamps the FULL accumulated answer across an
// ADR-087 D6 auto-continuation (turnState.continuationAccum) onto this
// streamer, so Finalize persists the complete prefix+suffix text instead of
// just the text this particular streamer itself accumulated — which, for a
// continuation's own per-call streamer, is only the suffix (ADR-087 §2.8:
// WSHandler.GetStreamer constructs a NEW wsStreamer on every provider call;
// only the last one is finalized, and it never saw the earlier call's text).
//
// Called by the agent loop (via the inline streamerContinuationSetter
// interface probed by turn.go's finalizeStreamer) immediately before
// Finalize, only when the turn had at least one continuation. A no-op on an
// empty string so a caller with nothing to stamp cannot blank out an
// already-set value; a turn with no continuation simply never calls this,
// leaving hasContinuation false and Finalize's existing
// accumulated/finalContent behavior byte-identical to before this method
// existed.
func (s *wsStreamer) SetContinuationContent(full string) {
	if full == "" {
		return
	}
	s.statsMu.Lock()
	s.continuationContent = full
	s.hasContinuation = true
	s.statsMu.Unlock()
}

func (s *wsStreamer) Update(_ context.Context, content string) error {
	s.statsMu.Lock()
	producerAgentID := s.agentID
	// Live-stream ownership gate (see WSHandler.streamOwners' doc comment):
	// resolved once, lazily, on this streamer's first Update() call, then
	// reused. A streamer with no turnID (legacy/best-effort caller) or no
	// channel/wsHandler wired (e.g. a bare unit-test fixture) always streams
	// live — the gate degrades to the pre-fix always-live behavior rather
	// than silently withholding content it has no way to attribute.
	//
	// Finding B (A-I4 round 4): a delegated CHILD sub-turn (s.parentSpawnCallID
	// != "", stamped by stampStreamerParentSpawnCallID before any Update/
	// Finalize call can observe it) must NEVER become the live-stream owner
	// for its shared chatID, full stop — never via claimStreamOwnership's
	// normal empty-slot-wins/stale-reclaim paths either. The pre-fix
	// claimStreamOwnership-only check let a background/async child win a
	// vacated ownership claim and start streaming its own raw, hidden-by-
	// design narration live: turnState.finalizeStreamer's B4 abandoned-turn
	// path (pkg/agent/turn.go) calls ReleaseStreamOwnership() on the PARENT's
	// claim the instant the parent is canceled (e.g. user clicks Stop) —
	// but a background delegate is intentionally allowed to keep running
	// past its parent's cancellation (see the Critical:true / "background
	// delegate's final answer lost when parent finishes first" fix), so the
	// child's NEXT streaming round (a fresh wsStreamer instance, its own
	// lazy shadowResolved=false) then finds the slot empty and legitimately
	// "wins" it under the old rule — even though a child's own token stream
	// is supposed to stay hidden unconditionally, exactly like the
	// already-correct sync/await case.
	//
	// [FIX-5, Defect 5, 2026-08-03] This gate's justification is
	// SELF-CONTAINED and does not rest on replay.go: a delegated child's
	// narration must never reach the user as its own top-level chat bubble,
	// live-streamed or replayed. This function enforces the LIVE half of
	// that invariant (withholding the live TokenFrame for a shadow stream);
	// the DURABLE half no longer needs an analogous read-side filter at all
	// (ADR-057 FR-034/FR-038 gave every delegated child its own store-backed
	// session, so its narration never lands in the parent's transcript for
	// replay.go to have to withhold in the first place — see
	// session.TranscriptEntry.ParentSpawnCallID's doc comment,
	// pkg/session/daypartition.go, for that mechanism's retirement). Do NOT
	// read the historical replay.go comparison as this gate's justification
	// and remove this gate on discovering replay.go no longer filters
	// anything — this Update() gate is a DIFFERENT, still-live mechanism
	// (the live-stream ownership claim, not a transcript read filter) with
	// its own reason to exist, stated above. Live-verified: reproduced the
	// leak (a second, delegate-authored top-level bubble with raw narration)
	// via a background delegation canceled mid-flight, confirmed the fix
	// removes it. A root/non-delegated turn is unaffected — this branch only
	// ever narrows behavior for parentSpawnCallID != "".
	if !s.shadowResolved {
		if s.parentSpawnCallID != "" {
			s.isShadowStream = true
		} else if s.turnID != "" && s.sessionID != "" && s.channel != nil && s.channel.wsHandler != nil {
			// ADR-082 review F6: keyed by sessionID, not s.chatID — delivery
			// itself is resolved purely by session (resolveSessionConnsLocked),
			// so ownership must be too, or two turns with different ORIGIN
			// chatIDs (e.g. a keeper/background turn's internal chatID and the
			// user's live webchat chatID) that both deliver into the SAME
			// session's bound connections would never contend for the slot and
			// could interleave their live tokens into the same viewers.
			s.isShadowStream = !claimStreamOwnership(&s.channel.wsHandler.streamOwners, s.sessionID, s.turnID)
		}
		s.shadowResolved = true
	}
	shadow := s.isShadowStream
	s.statsMu.Unlock()

	// ADR-082 D2/D3: accumulate this delta and resolve the CURRENT set of
	// connections bound to s.sessionID in ONE critical section, guarded by
	// the WSHandler's own mu — the SAME lock handleAttachSession's catch-up
	// bind+snapshot uses (WSHandler.snapshotLiveStreamerLocked). That shared
	// lock is what gives "no duplicate, no gap" catch-up ordering: a
	// connection binding concurrently with this Update either (a) completes
	// its bind before this critical section — excluded from targets here,
	// its own catch-up snapshot (taken atomically with its bind) captures
	// everything accumulated up to and NOT including this delta, so the
	// live divert buffer picks up this delta right after — or (b) completes
	// its bind after — included in targets here (delivered this delta live,
	// diverted into its replayDivertCh since it's still mid-replay), and its
	// catch-up snapshot (taken after its bind, hence after this critical
	// section too) already includes this delta, so the divert-buffered copy
	// of THIS SAME delta must never also be double-counted... it isn't,
	// because a connection only starts diverting into replayDivertCh once
	// wc.isReplayingLive flips true, which happens strictly AFTER its bind —
	// so case (b) never actually produces a divert-buffered copy of a delta
	// that predates the bind. Accumulate happens BEFORE the shadow check
	// only conceptually (both branches below run unconditionally under this
	// same lock) — a shadow (non-owning) stream's content must still be
	// fully captured for its own Finalize/transcript write even though it
	// withholds live frames (see the shadow gate above).
	//
	// A bare test fixture with no channel/wsHandler wired (wsHandler()
	// returns nil) accumulates lock-free — no binding exists to resolve.
	var targets []*wsConn
	if h := s.wsHandler(); h != nil {
		h.mu.Lock()
		s.accumulated.WriteString(content)
		if s.sessionID != "" {
			targets = h.resolveSessionConnsLocked("", s.sessionID)
		}
		h.mu.Unlock()
	} else {
		s.accumulated.WriteString(content)
	}

	if shadow {
		// A DIFFERENT, still-live turn already owns live TokenFrame delivery
		// to this chatID. This stream's content is fully captured in
		// s.accumulated above and will be persisted correctly by Finalize —
		// withholding the live frame here is what prevents two concurrent
		// delegate streams from interleaving their deltas into one garbled
		// message, live or on a client that caches/replays the live view.
		return nil
	}

	if len(targets) == 0 {
		// ADR-082 FR-006: zero bound connections costs no backoff wait — the
		// producer (the LLM streaming callback) is never slowed by an absent
		// viewer. Return immediately without touching any channel.
		return nil
	}

	frame := generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   content,
		SessionId: s.sessionID,
	}
	// FIX 5a: attribute the frame to the turn's TRUE producer (stamped via
	// SetProducerAgentID) rather than leaving the client to guess based on
	// whichever agent it happens to be actively chatting with — wrong for
	// background/delegated sub-turns.
	if producerAgentID != "" {
		frame.AgentId = &producerAgentID
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("ws: marshal token frame: %w", err)
	}
	// ADR-082 D2/FR-004/FR-005: deliver to EVERY connection currently bound
	// to this session, resolved above. Route each through sendRawFrameBytes
	// so token frames respect the replay-divert logic (a client reconnecting
	// mid-turn buffers live frames in its own replayDivertCh until its
	// attach_session replay finishes) and the existing backoff/backpressure
	// protocol. A drop or backpressure on one connection is that
	// connection's own problem — see DoneStats.TokensDropped, computed per
	// connection in Finalize — and never prevents delivery to any other
	// connection in this loop, and never causes this whole call to return an
	// error: Update returns nil unless the frame itself could not be
	// marshalled (checked above).
	for _, conn := range targets {
		before := conn.droppedTokens.Load()
		sendRawFrameBytes(conn, string(generated.WsFrameTypeToken), data)
		if conn.droppedTokens.Load() > before {
			slog.Warn("ws: token backpressure", "session_id", s.sessionID, "chat_id", s.chatID, "agent_id", producerAgentID)
		}
	}
	return nil
}

func (s *wsStreamer) Finalize(_ context.Context, finalContent string) error {
	// ADR-082 D2/FR-014: TokensDropped is no longer computed here as a single
	// turn-level value — a drop is a property of ONE connection's send
	// buffer, not the turn. Each connection gets its own generated.DoneStats
	// (sharing the turn-level fields below) built in the per-connection send
	// loop further down.
	// Include turn-level token/cost/duration if the agent loop pushed them via
	// SetTurnStats before this call (issue #12). Zero values are still emitted
	// so the client can reset the session counters for turns with no LLM usage.
	s.statsMu.Lock()
	tokensF := float64(s.statsTokens)
	costF := s.statsCostUSD
	// Read the split under the same lock as the total, so the entry cannot
	// carry a total from one turn and a split from another.
	promptTokensF := s.statsPromptTokens
	completionTokensF := s.statsCompletionTokens
	cacheReadF := s.statsCacheRead
	cacheWriteF := s.statsCacheWrite
	durF := float64(s.statsDuration.Milliseconds())
	transcriptAlreadyPersisted := s.transcriptPersisted
	producedModel := s.producedModel
	turnFailed := s.statsTurnFailed
	continuationContent := s.continuationContent
	hasContinuation := s.hasContinuation
	// ADR-087 D2/D4a/D4b, WP C: read under statsMu, same pattern as
	// continuationContent — SetTruncation (called by the agent loop's
	// finalizeStreamer immediately before Finalize) and Finalize (turn end)
	// may run on different goroutines in principle even though in practice
	// they are sequenced back-to-back by finalizeStreamer itself.
	truncationReason := s.truncationReason
	// FIX 5a/5c: read under statsMu — SetProducerAgentID/SetTurnID (called by
	// the agent loop at streaming-call start) may run on a different
	// goroutine than Finalize (called at turn end).
	producerAgentID := s.agentID
	turnID := s.turnID
	parentSpawnCallID := s.parentSpawnCallID
	// A-I4 round 4 / Finding A: resolve the live-stream shadow gate HERE too,
	// not just in Update(). Every turn — root OR a delegated child sub-turn —
	// runs through the exact same pkg/agent/loop.go runTurn/finalizeStreamer
	// path (spawnSubTurn calls al.runTurn(childCtx, childTS) directly, see
	// subturn.go), so a child's own Finalize fires — sending an UNCONDITIONAL
	// "done" WS frame to the CHATID IT SHARES WITH ITS PARENT — the instant
	// the child's own sub-turn completes, even while the parent's own turn is
	// still actively streaming (the common case for a synchronous/"await"
	// delegate call, which blocks the parent mid-turn while the child runs).
	// DoneFrame carries no turn/parent discriminator, so the client's `done`
	// handler (src/store/chat.ts) has no way to tell "a nested child
	// finished" from "the outer turn finished" — it unconditionally closes
	// whichever bubble is current, so the parent's still-open bubble is
	// finalized prematurely and every following narration segment opens a
	// brand-new bubble. Live-verified via a real 3-round synchronous
	// delegation: a `done` frame lands right after each child's
	// subagent_start (duration_ms matching that child's own subagent_end),
	// well before the parent's real, final `done` — producing N+1 bubbles for
	// N sequential delegate calls instead of one continuous bubble matching
	// the child's own hidden-narration invariant (this same Update() shadow
	// gate already treats a child's own text as never visible — Finalize
	// simply never enforced that for the "done" signal it also sends).
	//
	// [FIX-5, Defect 5, 2026-08-03] The invariant this paragraph enforces —
	// "a delegated child's own narration/signals are never visible as their
	// own top-level chat event" — no longer needs replay.go as a supporting
	// citation: that read-side skip was DELETED (ADR-057 FR-034/FR-038; see
	// session.TranscriptEntry.ParentSpawnCallID's doc comment,
	// pkg/session/daypartition.go, for why — a delegated child now owns its
	// own store-backed session, so there is nothing left in the PARENT's
	// transcript for a read boundary to withhold). This Finalize() gate and
	// Update()'s shadow-stream gate are BOTH still live, LIVE-side
	// mechanisms (withholding the "done" frame / the live TokenFrame,
	// respectively) — each independently justified by the invariant stated
	// above, not by the retired replay.go mechanism.
	//
	// Mirrors Update()'s lazy resolution exactly, including the same "a
	// delegated child NEVER owns the live slot" rule Update() gained for
	// Finding B below — a streamer whose Update() was never called (e.g. an
	// immediate tool-only round with no narration text) would otherwise
	// reach Finalize with shadowResolved still false and default to
	// isShadowStream=false (treated as live), which is wrong for a child
	// sub-turn that happened to stream zero tokens of its own.
	if !s.shadowResolved {
		if parentSpawnCallID != "" {
			s.isShadowStream = true
		} else if turnID != "" && s.sessionID != "" && s.channel != nil && s.channel.wsHandler != nil {
			// ADR-082 review F6: keyed by sessionID — see Update's identical gate.
			s.isShadowStream = !claimStreamOwnership(&s.channel.wsHandler.streamOwners, s.sessionID, turnID)
		}
		s.shadowResolved = true
	}
	shadow := s.isShadowStream
	s.statsMu.Unlock()

	// Release this turn's live-stream ownership claim (if held) so a
	// different, still-running turn on the same chatID can become the live
	// owner (see WSHandler.streamOwners' doc comment). Finalize is the
	// normal, once-per-turn release point via turnState's deferred
	// finalizeStreamer (pkg/agent/turn.go) — safe/no-op when this stream was
	// never the owner (a shadow stream) or never claimed at all (e.g. an
	// immediate tool-only round with no narration text, so Update() was
	// never called). See ReleaseStreamOwnership for the other release
	// points (Cancel, and finalizeStreamer's B4 abandoned-turn path, which
	// deliberately skips the rest of this method).
	// ReleaseStreamOwnership also unregisters this streamer from
	// h.liveStreamers[s.sessionID] when it is still the currently-registered
	// one (ADR-082 review CR4/F2) — see that method's doc comment. Finalize
	// used to do this deletion itself, inline, right here; it is now shared
	// with every other release path (Cancel, and finalizeStreamer's B4
	// abandoned-turn early return, pkg/agent/turn.go) so an abandoned turn
	// cannot leave a phantom liveStreamers entry (and therefore a phantom
	// catch-up token with no done frame ever following it) for a session
	// that no longer has any turn actually in flight.
	s.ReleaseStreamOwnership()

	// ADR-082 D2/D3: resolve the CURRENT set of connections bound to this
	// session, under the SAME h.mu critical section Update uses, for the
	// same "no duplicate, no gap" ordering reason (see Update's doc comment).
	var targets []*wsConn
	if h := s.wsHandler(); h != nil && s.sessionID != "" {
		h.mu.Lock()
		targets = h.resolveSessionConnsLocked("", s.sessionID)
		h.mu.Unlock()
	}

	// A-I4 round 4 / Finding A: a shadow stream (a delegated child sub-turn
	// that never owned — and, per the rule above, can never win — this
	// chatID's live-stream slot) must not send its own "done" either. Its
	// content was never shown live in the first place (Update() withheld
	// every token); sending "done" anyway prematurely finalizes whatever
	// bubble the OWNING (parent) turn currently has open. The transcript
	// write below stays unconditional — persistence must not depend on live
	// visibility — only the live-facing signals (done frame, fan-out,
	// markStreamed) are gated.
	if !shadow {
		// ADR-082 D2/FR-014: send one done frame PER bound connection, each
		// carrying that connection's own TokensDropped — a drop on one
		// connection's send buffer must never be reported (or withheld) on
		// another connection's done frame.
		for _, conn := range targets {
			connStats := &generated.DoneStats{
				Tokens:     &tokensF,
				Cost:       &costF,
				DurationMs: &durF,
			}
			if turnFailed {
				tf := turnFailed
				connStats.TurnFailed = &tf
			}
			// ADR-087 D2 (finding #10): mirror the truncation annotation onto
			// the LIVE done frame too, not just the persisted transcript
			// entry (above) and replay's ReplayMessageFrame (replay.go) — a
			// turn cut off while the user is still watching should render
			// the "(cut off at the output limit)" notice immediately,
			// without waiting for a reload/reattach round-trip through
			// replay. Populated from the SAME truncationReason this Finalize
			// call stamped on the transcript entry.
			if truncationReason != "" {
				truncatedCopy := true
				connStats.Truncated = &truncatedCopy
				reasonCopy := truncationReason
				connStats.TruncationReason = &reasonCopy
			}
			// ADR-082 review F10: Swap(0), not Load — droppedTokens is a
			// per-CONNECTION counter that outlives any single turn, so a bare
			// Load would keep re-reporting turn 1's drops on every later
			// turn's done frame forever. Atomically reading-and-resetting here
			// makes this the per-turn DELTA: only drops that happened since
			// the last done frame this connection received are reported.
			if dropped := conn.droppedTokens.Swap(0); dropped > 0 {
				droppedF := float64(dropped)
				connStats.TokensDropped = &droppedF
			}
			doneFrame := generated.DoneFrame{
				Type:      string(generated.WsFrameTypeDone),
				SessionId: s.sessionID,
				Stats:     connStats,
			}
			data, mErr := json.Marshal(doneFrame)
			if mErr != nil {
				slog.Error("ws: marshal done frame failed", "session_id", s.sessionID, "error", mErr)
				continue
			}
			sendRawFrameBytes(conn, string(generated.WsFrameTypeDone), data)
		}
		// Only mark as streamed if we actually sent content. If the LLM failed
		// before producing any tokens, let the outbound Send path deliver the
		// error message — otherwise the user sees a stuck "thinking" spinner.
		if s.channel != nil && s.accumulated.Len() > 0 {
			s.channel.markStreamed(s.chatID)
		}
	}
	// Record the full assistant response to the session transcript — unless the
	// agent loop already persisted this round's narration via
	// appendIntermediateAssistantTranscript (#416 gate fix). This happens when
	// the turn exits via max_tool_iterations exhaustion: the last executed round
	// is a tool-call round whose streamer (this one) becomes the lastStreamer.
	// Writing here too would duplicate the assistant bubble on replay. We still
	// sent the done frame, fan-out, and markStreamed above — only the append is
	// suppressed.
	if s.agentStore != nil && s.sessionID != "" && !transcriptAlreadyPersisted {
		content := s.accumulated.String()
		if hasContinuation {
			// ADR-087 D6.1/§2.8: this streamer is per PROVIDER CALL, not per
			// turn — its own `accumulated` buffer (and finalContent, which
			// for a continuation's per-call streamer would also just be the
			// suffix) holds only the LAST call's text. continuationContent
			// carries the full prefix+suffix answer the live bubble showed;
			// persist THAT, not the buffer.
			content = continuationContent
		} else if content == "" && finalContent != "" {
			// Fallback: when accumulated is empty (every Update() call silently
			// failed because the client WS was already closed), use the
			// finalContent the agent loop passed in. Without this fallback,
			// disconnected mid-stream turns would leave no assistant entry in
			// transcript.jsonl and the user sees nothing on reconnect/replay.
			content = finalContent
		}
		// ADR-087 D2/D4a/D4b: a truncated turn must still get an entry even
		// when content is empty — D4a is the deliberate "cut off before any
		// text was produced" outcome, not a fallthrough with nothing to
		// write. Without the `|| truncationReason != ""` arm, a zero-content
		// truncated turn wrote NO entry at all on the streamed path: replay
		// showed no assistant message (the "(cut off at the output limit)"
		// notice never appeared), and finalizeStreamer's old post-hoc
		// MarkLastEntryTruncated call — removed now that this write does the
		// stamping directly — either silently no-op'd (no entry to find) or,
		// worse, walked back and mis-stamped an EARLIER same-turn narration
		// entry as truncated. See truncationReason's own field doc comment.
		if content != "" || truncationReason != "" {
			entry := session.TranscriptEntry{
				ID:      uuid.New().String(),
				Role:    "assistant",
				AgentID: producerAgentID,
				// TurnID (FIX 5c/1): stamped via SetTurnID so a mid-stream
				// cancel's turn_canceled entry can be correlated with THIS
				// entry on replay.
				TurnID:    turnID,
				Content:   content,
				Timestamp: time.Now().UTC(),
				Tokens:    int(tokensF),
				Cost:      costF,
				Model:     producedModel,
				// The provider's token split. Without these four fields the
				// session-stats aggregator sees no split and falls back to
				// booking the whole turn total as output, which is how every
				// webchat session came to report tokens_in: 0.
				PromptTokens:     promptTokensF,
				CompletionTokens: completionTokensF,
				CacheReadTokens:  cacheReadF,
				CacheWriteTokens: cacheWriteF,
				// ParentSpawnCallID: stamped via SetParentSpawnCallID so a
				// delegation child sub-turn's own streamed narration/final
				// response carries the same nesting correlation its
				// non-streaming siblings (appendIntermediateAssistantTranscript
				// / appendAssistantTranscript) already stamp — see
				// session.TranscriptEntry.ParentSpawnCallID's doc comment.
				// Empty (the common case) for a root turn.
				ParentSpawnCallID: parentSpawnCallID,
			}
			// ADR-087 D2/D4a/D4b, WP C: stamp Truncated/TruncationReason in
			// THIS SAME WRITE — whether content is empty (D4a) or non-empty
			// (D4b, an auto-continue-exhausted/ineligible accumulated
			// answer) — instead of a separate post-hoc
			// MarkLastEntryTruncated call after Finalize returns. See
			// truncationReason's own field doc comment for why the post-hoc
			// call was the bug.
			if truncationReason != "" {
				entry.Truncated = true
				entry.TruncationReason = truncationReason
			}
			// ADR-057 FR-001/FR-002 (W3): AppendTranscriptStrict refuses loudly
			// (and creates nothing on disk) when s.sessionID does not resolve to
			// a real, store-backed session, instead of AppendTranscript's old
			// lenient silent-create branch. The error was already checked here
			// before this conversion — only the runtime behavior of a failure
			// changes (loud vs. silently minting an orphan session directory) —
			// so surface it as a counter increment (BDD-03) alongside the
			// pre-existing WARN.
			if err := s.agentStore.AppendTranscriptStrict(s.sessionID, entry); err != nil {
				wsTranscriptWriteFailures.Add(1)
				slog.Warn("ws: could not record streamed assistant message", "session_id", s.sessionID, "error", err)
			}
		}
	}
	return nil
}

func (s *wsStreamer) Cancel(_ context.Context) {
	// Defensive symmetry with Finalize's release: Cancel is not on the
	// agent loop's normal per-turn path (finalizeStreamer always calls
	// Finalize, never Cancel — see turn.go), but release the ownership
	// claim here too so a future caller of Cancel cannot leak the chatID's
	// live-stream slot forever.
	//
	// ADR-082 D2: this streamer no longer pins a single *wsConn, so there is
	// no longer "the" connection to force-close here — a session-bound
	// streamer's turn ending is not a reason to disconnect every (or any)
	// viewer's WebSocket. Cancel has no production call site today (see
	// ReleaseStreamOwnership's doc comment); this stays a no-op release for
	// defensive symmetry only.
	s.ReleaseStreamOwnership()
}

// ReleaseStreamOwnership releases this streamer's live-stream ownership claim
// for its sessionID (if held), allowing a different, still-running turn on
// the same session to become the live owner (see WSHandler.streamOwners' doc
// comment; keyed by sessionID, not chatID — ADR-082 review F6). Safe to call
// multiple times, concurrently, or when the claim was never held —
// releaseStreamOwnershipClaim only deletes an entry that still matches this
// exact turnID.
//
// [ADR-082 review CR4/F2] Also unregisters this streamer from
// WSHandler.liveStreamers[s.sessionID] when it is still the CURRENTLY
// registered entry for that session — the same guarded delete Finalize used
// to perform inline, now shared by every release path. Without this here,
// only Finalize ever cleared liveStreamers: turnState.finalizeStreamer's B4
// abandoned-turn early return (pkg/agent/turn.go) deliberately skips the rest
// of Finalize and calls ONLY this method (via the streamOwnershipReleaser
// optional interface), so an abandoned turn's streamer stayed registered in
// liveStreamers forever — every later attach_session on that session then
// got a phantom catch-up token (hasCatchUp=true, stale accumulated text)
// with no done frame ever following it, since the turn that would have sent
// one is dead. Cancel (defensive symmetry, no production call site today)
// gets the same fix for free.
//
// This is the single implementation shared by Finalize, Cancel, and — via
// the streamOwnershipReleaser optional interface pkg/agent's finalizeStreamer
// type-asserts for — the B4 abandoned-turn early return (pkg/agent/turn.go).
// That early return deliberately skips the rest of Finalize (no done frame,
// no transcript write, so a stuck goroutine cannot send a spurious signal to
// the frontend) but was found, in a 7-reviewer gate, to also skip releasing
// the ownership claim: a background delegate that became the live owner for
// a session and was later MarkAbandoned()'d by cancel.go's PHASE C left that
// session permanently shadowed, since Finalize (the only other release
// point; Cancel has no production call sites) never ran. Exported so
// pkg/agent can reach it through bus.Streamer's optional-interface pattern
// without either package importing the other's concrete type.
func (s *wsStreamer) ReleaseStreamOwnership() {
	s.statsMu.Lock()
	turnID := s.turnID
	s.statsMu.Unlock()
	h := s.wsHandler()
	if h == nil {
		return
	}
	if s.sessionID != "" {
		releaseStreamOwnershipClaim(&h.streamOwners, s.sessionID, turnID)
		h.mu.Lock()
		if cur, ok := h.liveStreamers[s.sessionID]; ok && cur == s {
			delete(h.liveStreamers, s.sessionID)
		}
		h.mu.Unlock()
	}
}
