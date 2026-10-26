// websocket_replay.go: Attach to a session — incremental catch-up from the
// session hub's journal, or a snapshot (transcript replay plus the hub's
// active-turn projection). #823 catch-up redesign, BE-DESIGN.md §4.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// errSendTimeout is returned by an attach's flow-controlled writes when the
// connection makes no progress for attachReplayStallTimeout: the client is
// too slow to take its own catch-up, so it is closed with 4008 and
// reconnects (ws_conn_queue.go).
var errSendTimeout = fmt.Errorf("ws: send queue not draining — attach catch-up timed out")

// wsHandlerHandleAttachSession carries the shared state of handleAttachSession across its stages.
type wsHandlerHandleAttachSession struct {
	h        *WSHandler
	ctx      context.Context
	chatID   string
	attachID string
	cursor   *attachCursor
	wc       *wsConn
	store    *session.UnifiedStore
	res      attachResult
	failed   bool // the catch-up failed; nothing else of this attach runs
	// ack is the attach frame's acknowledgement of what was shown (U11); nil
	// for every attach that does not carry one.
	ack              *attachAck
	historyOptions   session.ChatHistoryPageOptions
	historyRequested *int
	liveOnly         bool
}

// attachAck is the acknowledging half of an attach_session frame (FR-047,
// C-ATTENTION): ack_attention true plus the attention_bound the client was
// shown. Either alone writes nothing.
type attachAck struct {
	Ack   bool
	Bound *int64
}

// attachReadTranscript reads a session's history for a snapshot. A var only
// so a test can make the read fail; never reassigned in production.
var attachReadTranscript = func(store *session.UnifiedStore, sessionID string) (session.TranscriptView, error) {
	return store.ReadTranscriptView(sessionID)
}

// attachAfterBindHook, when set, runs right after an attach has bound the
// connection and before it sends any catch-up — the window in which a turn
// can publish or persist concurrently with the catch-up (BE-DESIGN.md §4.2,
// H6/H16). A var only so tests can drive that window deterministically;
// always nil in production.
var attachAfterBindHook func(sessionID string)

// handleAttachSession binds wc to attachID's session hub and answers with
// either an incremental catch-up or a snapshot, then releases the live
// frames that were held while it did so (BE-DESIGN.md §4.1):
//
//	A1  validate the session id, resolve its store
//	A2-A4  under WSHandler.mu and the hub's lock: move wc off its previous
//	    hub, bind it in hold mode, decide servability (§3.3), read head W and
//	    either the journal tail (since_seq, W] or the active-turn projection
//	A5-A6 incremental: session_state · tail · catch_up_complete{W}
//	      snapshot:    session_snapshot{W} · session_state · transcript
//	                   replay (read AFTER the bind) · projection items the
//	                   replay did not cover · catch_up_complete{W}
//	A7  release hold: every frame published after the bind follows, in order
//	A8  hydrate the agent's history, re-emit goal status
//	A9  acknowledge attention: only for an acknowledging attach of a main whose
//	    A1-A8 all succeeded (attention_ack.go)
//
// It runs synchronously in the connection's read loop, so a frame the
// client sends after attach_session (e.g. a queued offline message) is
// handled only after the catch-up has been queued (§6.6).
//
// This replaces the pre-#823 path — timestamp cursor (applySinceCursor),
// transcript read BEFORE the bind, a per-round live-streamer text snapshot,
// and a divert channel that could drop live frames under backpressure
// (founder decision Q3: replaced, the #822 guarantee kept by hold mode).
func (h *WSHandler) handleAttachSession(
	ctx context.Context,
	chatID string,
	attachID string,
	cursor *attachCursor,
	wc *wsConn,
) {
	h.handleAttachSessionWithAck(ctx, chatID, attachID, cursor, nil, wc)
}

// handleAttachSessionWithAck is handleAttachSession plus the optional
// attention acknowledgement carried by the attach frame (A9, below).
func (h *WSHandler) handleAttachSessionWithAck(
	ctx context.Context,
	chatID string,
	attachID string,
	cursor *attachCursor,
	ack *attachAck,
	wc *wsConn,
	rawFrame ...[]byte,
) {
	wh := &wsHandlerHandleAttachSession{h: h, ctx: ctx, chatID: chatID, attachID: attachID, cursor: cursor, wc: wc, ack: ack}
	if !wh.resolveSession() {
		return
	}
	if !wh.validateHistoryAttach(rawFrame) {
		return
	}
	wh.bindHub()
	if hook := attachAfterBindHook; hook != nil {
		hook(wh.attachID)
	}
	if wh.res.Servable {
		wh.sendIncremental()
	} else {
		wh.sendSnapshot()
	}
	if wh.failed {
		return
	}
	wh.wc.releaseHold()
	wh.resumeLiveSession()
	if !wh.liveOnly {
		wh.ackAttention()
	}
}

// resolveSession is A1: validate the session id and resolve its store.
func (wh *wsHandlerHandleAttachSession) resolveSession() bool {
	if err := validateEntityID(wh.attachID); err != nil {
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "invalid session_id",
		})
		return false
	}
	// Final-gate M4: a main the pair is no longer entitled to is not attachable.
	if !wh.h.mainSessionAllowed(wh.wc, wh.attachID) {
		return false
	}
	wh.store = wh.h.resolveSessionStore(wh.attachID)
	if wh.store == nil {
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			SessionId: &wh.attachID,
			Message:   "session not found",
		})
		return false
	}
	return true
}

// bindHub is A2-A4. It also updates the connection's chat→session mapping
// (h.sessionIDs, read by GetStreamer's fallback and by chat-id-only event
// routing) in the same WSHandler.mu critical section as the hub bind, so
// the two can never disagree about which session this connection is on
// (Finding E, A-I4 round 5: a stale mapping once let another session's
// frames land on this connection mid-replay).
func (wh *wsHandlerHandleAttachSession) bindHub() {
	h := wh.h
	h.mu.Lock()
	if oldTID, ok := h.taskChatIDs[wh.chatID]; ok {
		delete(h.sessionIDs, oldTID)
	}
	h.taskChatIDs[wh.chatID] = wh.attachID
	h.sessionIDs[wh.attachID] = wh.attachID
	h.sessionIDs[wh.chatID] = wh.attachID
	wh.res = h.attachConnToSessionHubLocked(wh.wc, wh.attachID, wh.cursor)
	h.mu.Unlock()
}

// sendIncremental is A6's incremental branch: the client's cursor is
// servable, so it gets exactly the journal frames after it — byte-identical
// to what every live tab received — and nothing it already has.
func (wh *wsHandlerHandleAttachSession) sendIncremental() {
	logsafeInfo("ws: catch_up",
		"event", "catch_up", "mode", "incremental",
		"session_id", wh.attachID, "chat_id", wh.chatID,
		"frames", len(wh.res.Tail), "head", wh.res.Head)
	state := wh.incrementalHistoryState()
	wh.liveOnly = state.Status == "unavailable"
	if !wh.sendHistoryState(state) {
		return
	}
	for _, frame := range wh.res.Tail {
		if err := wh.wc.directWait(wh.ctx, frame); err != nil {
			wh.historyDeliveryFailed(err)
			return
		}
	}
	wh.sendCatchUpComplete("incremental")
	if !wh.failed {
		wh.wc.rememberHistoryState(wh.attachID, state)
	}
}

// sendSnapshot is A6's snapshot branch.
func (wh *wsHandlerHandleAttachSession) sendSnapshot() {
	wh.sendPreparedHistorySnapshot()
}

// recordEmittedIDs adds the ids a replayed frame carries to emitted.
// emittedResultKey is the emitted-set key of a tool call's RESULT, kept
// separate from the call id that records its start.
func emittedResultKey(callID string) string { return "result\x00" + callID }

func recordEmittedIDs(emitted map[string]bool, f any) {
	switch v := f.(type) {
	case generated.ReplayMessageFrame:
		if v.Id != nil {
			emitted[*v.Id] = true
		}
	case generated.ToolCallStartFrame:
		emitted[v.CallId] = true
	case generated.ToolCallResultFrame:
		// A start is not result coverage (D4): record the terminal result
		// under its own key so the live projection can still deliver it.
		emitted[emittedResultKey(v.CallId)] = true
	case generated.SubagentStartFrame:
		emitted[v.SpanId] = true
	case generated.SubagentEndFrame:
		emitted[v.SpanId] = true
	case generated.GoalOutcomeFrame:
		emitted[v.MessageId] = true
	case generated.HelperEventFrame:
		emitted[v.MessageId] = true
	}
}

// abortCatchUp ends an attach whose catch-up could not be delivered. A
// connection too slow to take its own catch-up is closed with 4008 so it
// reconnects and tries again; a connection already closed gets nothing. Any
// other failure (including a cancelled context) goes through failCatchUp.
func (wh *wsHandlerHandleAttachSession) abortCatchUp(err error) {
	switch {
	case errors.Is(err, errSendTimeout):
		wh.wc.closeCatchUp()
		return
	case errors.Is(err, errConnClosed):
		return
	}
	wh.failCatchUp("replay aborted: " + err.Error())
}

// failCatchUp ends an attach whose history could not be rebuilt (#823 review
// item 9). It must NOT send catch_up_complete{W}: that would set the
// client's cursor to W over a history it never fully received, and a later
// reconnect would resume from W with the missing part lost for good.
// Instead the connection is unbound from the session and its held live
// frames are dropped (they are all in the journal), and the client is told
// with an error plus done{replay_error} — the contract it already handles by
// leaving catch-up mode — so it can retry the attach.
func (wh *wsHandlerHandleAttachSession) failCatchUp(message string) {
	wh.h.mu.Lock()
	wh.h.unbindConnHubLocked(wh.wc)
	wh.h.mu.Unlock()
	wh.wc.discardHold()
	wh.failed = true
	sid := wh.attachID
	sendConnGenFrameDirect(wh.wc, generated.ErrorFrame{
		Type:      string(generated.WsFrameTypeError),
		SessionId: &sid,
		Message:   message,
	})
	replayErr := true
	sendConnGenFrameDirect(wh.wc, generated.DoneFrame{
		Type:      string(generated.WsFrameTypeDone),
		SessionId: wh.attachID,
		Stats:     &generated.DoneStats{ReplayError: &replayErr},
	})
}

// sendCatchUpComplete ends the catch-up: the client's cursor for the
// session becomes W, and the held live frames (seq > W) follow.
func (wh *wsHandlerHandleAttachSession) sendCatchUpComplete(mode string) {
	bootID := wh.h.hubs.bootID
	wh.sendHistoryFrame(generated.CatchUpCompleteFrame{
		Type:      string(generated.WsFrameTypeCatchUpComplete),
		SessionId: wh.attachID,
		Seq:       int64(wh.res.Head),
		BootId:    &bootID,
		Mode:      mode,
	})
}

// sendConnGenFrameDirect marshals frame and queues it on wc bypassing hold
// mode — for the attach goroutine's own catch-up frames.
func sendConnGenFrameDirect(wc *wsConn, frame any) {
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal catch-up frame failed", "error", err)
		return
	}
	wc.direct(data)
}

// resumeLiveSession is A8: re-emit goal status for the attached session. The
// model window is the addressed archive itself (session-core U2 DEL-12), so
// nothing is rebuilt from the transcript on attach.
func (wh *wsHandlerHandleAttachSession) resumeLiveSession() {
	// Item 14 (review-round-1, ADR-088): goal_status is a pure live push —
	// never a replayable transcript entry — so re-emit it for the attached
	// session; this connection is already bound and receives it like any
	// other live goal_status. No-op when there is no active goal.
	wh.h.agentLoop.EmitGoalStatusRehydrate(wh.attachID)
	logsafeDebug("ws: attached to session", "chat_id", wh.chatID, "session_id", wh.attachID)
}
