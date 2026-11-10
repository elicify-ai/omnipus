package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func (wc *wsConn) rememberHistoryState(id string, state generated.SessionHistoryState) {
	wc.historyStateMu.Lock()
	defer wc.historyStateMu.Unlock()
	wc.historyStateSession = id
	wc.historyReadiness = &state
}
func (wc *wsConn) rememberedHistoryState(id string) (generated.SessionHistoryState, bool) {
	wc.historyStateMu.Lock()
	defer wc.historyStateMu.Unlock()
	if wc.historyStateSession != id || wc.historyReadiness == nil {
		return generated.SessionHistoryState{}, false
	}
	return *wc.historyReadiness, true
}

// Decode only the non-history part first. The same addressed-session access
// check as a normal attach precedes history number/null/type/schema validation.
func (wh *wsHandlerReadLoop) handleHistoryAttachFrame(data []byte) wsHandlerReadLoopFlow {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		logsafeWarn("ws: malformed attach_session frame", "phase", "decode_object")
		return wsHandlerReadLoopContinue
	}
	delete(fields, "history_limit")
	delete(fields, "history_min_rows")
	raw, err := json.Marshal(fields)
	if err != nil {
		logsafeWarn("ws: malformed attach_session frame", "phase", "encode_envelope")
		return wsHandlerReadLoopContinue
	}
	var f generated.AttachSessionFrame
	if json.Unmarshal(raw, &f) != nil {
		logsafeWarn("ws: malformed attach_session frame", "phase", "decode_attach")
		return wsHandlerReadLoopContinue
	}
	wh.h.handleAttachSessionWithAck(wh.ctx, wh.chatID, f.SessionId, &attachCursor{SinceSeq: f.SinceSeq, BootID: f.BootId}, &attachAck{Ack: f.AckAttention != nil && *f.AckAttention, Bound: f.AttentionBound}, wh.wc, data)
	return wsHandlerReadLoopNext
}
func (wh *wsHandlerHandleAttachSession) validateHistoryAttach(raw [][]byte) bool {
	opts := session.ChatHistoryPageOptions{Limit: 40, MinRows: 20}
	var requested *int
	var err error
	if len(raw) > 0 {
		opts, requested, err = parseChatHistoryAttach(raw[0])
	}
	if err != nil {
		attempt := newHistoryPageAttempt("ws_attach")
		attempt.err = err
		attempt.requested = requested
		attempt.finish()
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{Type: "error", SessionId: &wh.attachID, Message: err.Error()})
		return false
	}
	if len(raw) > 0 && wh.h.agentLoop.GetConfig().Gateway.ValidateInbound {
		if message, _ := ValidateInboundFrameJSON("AttachSessionFrame", raw[0]); message != "" {
			err = historyQueryRefusal("invalid attach_session frame")
			attempt := newHistoryPageAttempt("ws_attach")
			attempt.validated(opts, requested)
			attempt.err = err
			attempt.finish()
			sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{Type: "error", SessionId: &wh.attachID, Message: err.Error()})
			return false
		}
	}
	wh.historyOptions = opts
	wh.historyRequested = requested
	return true
}
func (wh *wsHandlerHandleAttachSession) incrementalHistoryState() generated.SessionHistoryState {
	err := wh.store.CheckChatHistoryReadiness(wh.ctx, wh.attachID)
	if err != nil {
		return historyStateForError(err)
	}
	// A journal catch-up is not evidence that a previously failed projection can
	// now be reconstructed. Only a real successful snapshot clears that failure.
	if old, ok := wh.wc.rememberedHistoryState(wh.attachID); ok && old.Status == "unavailable" {
		return old
	}
	return historyStateForError(nil)
}
func (wh *wsHandlerHandleAttachSession) historyDeliveryFailed(err error) bool {
	wh.failed = true
	if errors.Is(err, errConnClosed) {
		wh.h.mu.Lock()
		wh.h.unbindConnHubLocked(wh.wc)
		wh.h.mu.Unlock()
		wh.wc.discardHold()
		return false
	}
	wh.abortCatchUp(err)
	return false
}
func (wh *wsHandlerHandleAttachSession) sendHistoryFrame(frame any) bool {
	raw, err := json.Marshal(frame)
	if err != nil {
		return wh.historyDeliveryFailed(err)
	}
	if err = wh.wc.directWait(wh.ctx, raw); err != nil {
		return wh.historyDeliveryFailed(err)
	}
	return true
}
func (wh *wsHandlerHandleAttachSession) sendHistoryState(state generated.SessionHistoryState) bool {
	raw := wh.h.sessionStateBytes(wh.wc, wh.attachID, state)
	if raw == nil {
		return wh.historyDeliveryFailed(errors.New("session state unavailable"))
	}
	if err := wh.wc.directWait(wh.ctx, raw); err != nil {
		return wh.historyDeliveryFailed(err)
	}
	return true
}
func (wh *wsHandlerHandleAttachSession) historyDeps() historyPageProjectionDeps {
	var mediaStore media.MediaStore
	var lifecycle lifecycleRecordLoader
	if wh.h.agentLoop != nil {
		mediaStore = wh.h.agentLoop.GetMediaStore()
		if ls := wh.h.agentLoop.GetSessionLifecycleStore(); ls != nil {
			lifecycle = ls
		}
	}
	return historyPageProjectionDeps{MediaStore: mediaStore, ToolStore: wh.h.toolStore, TerminalAsk: loadTerminalAskRecord(wh.store, wh.attachID), Lifecycle: lifecycle}
}
func (wh *wsHandlerHandleAttachSession) sendPreparedHistorySnapshot() {
	logsafeInfo("ws: catch_up", "event", "catch_up", "mode", "snapshot", "reason", wh.res.Reason, "session_id", wh.attachID, "chat_id", wh.chatID, "head", wh.res.Head)
	attempt := newHistoryPageAttempt("ws_attach")
	attempt.validated(wh.historyOptions, wh.historyRequested)
	defer attempt.finish()
	deps := wh.historyDeps()
	request, err := newHistoryPageRequest(wh.attachID, wh.historyOptions, deps)
	var prepared preparedHistoryPage
	if err == nil {
		request.keep = historyKeepFrames
		prepared, err = request.Read(attempt.context(wh.ctx), wh.store)
	}
	attempt.err = err
	attempt.preparationComplete()
	if err != nil {
		if historyStorageFailure(err) {
			slog.Error("ws: session storage failure", "op", "read", "session_id", wh.attachID, "error", err)
		}
		if wh.ctx.Err() != nil {
			wh.historyDeliveryFailed(wh.ctx.Err())
			return
		}
		state := historyStateForError(err)
		wh.liveOnly = true
		if !wh.sendHistoryState(state) {
			return
		}
		if !wh.sendHistoryProjection(nil) {
			return
		}
		wh.sendCatchUpComplete("live_only")
		if !wh.failed {
			wh.wc.rememberHistoryState(wh.attachID, state)
		}
		return
	}
	attempt.page = &prepared.Page
	response := prepared.Response
	bootID, reason := wh.h.hubs.bootID, wh.res.Reason
	snapshot := generated.SessionSnapshotFrame{Type: "session_snapshot", SessionId: wh.attachID, Seq: int64(wh.res.Head), BootId: &bootID, Reason: &reason, HasEarlier: response.HasEarlier, HasLater: response.HasLater, NextBefore: response.NextBefore, NextAfter: response.NextAfter, PageInfo: response.PageInfo}
	// Train-3 merge parity: the snapshot frame carries this connection's
	// pending and recent terminal secret requests, as the pre-paging inline
	// snapshot did (ws_secret_requests.go).
	snapshot.SecretRequests = wh.h.secretRequestSnapshot(wh.wc, wh.attachID)
	if !wh.sendHistoryFrame(snapshot) {
		return
	}
	state := historyStateForError(nil)
	if !wh.sendHistoryState(state) {
		return
	}
	for i, raw := range prepared.ReplayFrames {
		// Once handed to the connection a frame is the queue's; the prepared
		// page releases its copy so a slow client holds only unsent frames.
		prepared.ReplayFrames[i] = nil
		var peek wsTypeOnly
		if err = json.Unmarshal(raw, &peek); err != nil {
			wh.historyDeliveryFailed(err)
			return
		}
		if peek.Type == "done" && deps.TerminalAsk != nil && !prepared.terminalAskEmitted {
			if !wh.sendHistoryFrame(buildAskUserQuestionFrame(deps.TerminalAsk)) {
				return
			}
		}
		if err = wh.wc.directWait(wh.ctx, raw); err != nil {
			wh.historyDeliveryFailed(err)
			return
		}
	}
	if !wh.sendHistoryProjection(prepared.emitted) {
		return
	}
	wh.sendCatchUpComplete("snapshot")
	if !wh.failed {
		wh.wc.rememberHistoryState(wh.attachID, state)
	}
}
func (wh *wsHandlerHandleAttachSession) sendHistoryProjection(emitted map[string]bool) bool {
	for _, raw := range projectionFrames(wh.attachID, wh.res.Proj, emitted) {
		if err := wh.wc.directWait(wh.ctx, raw); err != nil {
			return wh.historyDeliveryFailed(err)
		}
	}
	return true
}
