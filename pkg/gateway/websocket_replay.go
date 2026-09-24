// websocket_replay.go: Attach to a session — incremental catch-up from the
// session hub's journal, or a snapshot (transcript replay plus the hub's
// active-turn projection). #823 catch-up redesign, BE-DESIGN.md §4.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
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
}

// attachReadTranscript reads a session's history for a snapshot. A var only
// so a test can make the read fail; never reassigned in production.
var attachReadTranscript = func(store *session.UnifiedStore, sessionID string) ([]session.TranscriptEntry, error) {
	return store.ReadTranscript(sessionID)
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
	wh := &wsHandlerHandleAttachSession{h: h, ctx: ctx, chatID: chatID, attachID: attachID, cursor: cursor, wc: wc}
	if !wh.resolveSession() {
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
	wh.store = wh.h.resolveSessionStore(wh.attachID)
	if wh.store == nil {
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "session not found",
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
	slog.Info("ws: catch_up",
		"event", "catch_up", "mode", "incremental",
		"session_id", wh.attachID, "chat_id", wh.chatID,
		"frames", len(wh.res.Tail), "head", wh.res.Head)
	wh.direct(wh.h.sessionStateBytes(wh.wc, wh.attachID))
	for _, frame := range wh.res.Tail {
		if err := wh.wc.directWait(wh.ctx, frame); err != nil {
			wh.abortCatchUp(err)
			return
		}
	}
	wh.sendCatchUpComplete("incremental")
}

// sendSnapshot is A6's snapshot branch.
func (wh *wsHandlerHandleAttachSession) sendSnapshot() {
	slog.Info("ws: catch_up",
		"event", "catch_up", "mode", "snapshot", "reason", wh.res.Reason,
		"session_id", wh.attachID, "chat_id", wh.chatID, "head", wh.res.Head)
	// Read the transcript AFTER the bind (§4.2): everything persisted up to
	// now is in it; whatever was published at or before W but not yet
	// persisted is in the projection; whatever is published after W is held.
	// And read it BEFORE session_snapshot (final-review N4): that frame
	// tells the client to wipe its history for this session, so a read
	// failure must be answered while the client's current view is intact.
	entries, err := attachReadTranscript(wh.store, wh.attachID)
	if err != nil {
		slog.Warn("ws: attach_session: could not read transcript", "session_id", wh.attachID, "error", err)
		wh.failCatchUp("could not read session transcript")
		return
	}
	bootID := wh.h.hubs.bootID
	reason := wh.res.Reason
	wh.directFrame(generated.SessionSnapshotFrame{
		Type:      string(generated.WsFrameTypeSessionSnapshot),
		SessionId: wh.attachID,
		Seq:       int64(wh.res.Head),
		BootId:    &bootID,
		Reason:    &reason,
	})
	// AFTER the snapshot frame (which wipes the client's history for this
	// session), so the wipe can never erase pending approvals, the active
	// turn or goal state (§4.6, review finding 2).
	wh.direct(wh.h.sessionStateBytes(wh.wc, wh.attachID))

	emitted, ok := wh.replayTranscript(entries)
	if !ok {
		return
	}
	for _, frame := range projectionFrames(wh.attachID, wh.res.Proj, emitted) {
		if err := wh.wc.directWait(wh.ctx, frame); err != nil {
			wh.abortCatchUp(err)
			return
		}
	}
	wh.sendCatchUpComplete("snapshot")
}

// replayTranscript streams the persisted transcript to the connection
// (unsequenced), recording every message id, tool call id and span id it
// emitted so the projection filter can skip items the replay already
// covers. Returns false when the attach was aborted.
func (wh *wsHandlerHandleAttachSession) replayTranscript(entries []session.TranscriptEntry) (map[string]bool, bool) {
	rs := computeReplayStats(entries)
	slog.Info("ws: replay_start",
		"event", "replay_start",
		"session_id", wh.attachID,
		"entry_count_loaded", len(entries),
		"tool_call_count_loaded", rs.toolCallCount,
		"span_count_detected", rs.spanCount,
		"orphan_count", rs.orphanCount,
		"duplicate_tool_call_id_count", rs.duplicateToolCallIDCount,
		"truncated_result_count", rs.truncatedResultCount,
	)
	started := time.Now()
	emitted := make(map[string]bool)
	send := wsEmitFunc(wh.ctx, wh.wc)
	emit := func(f any) error {
		recordEmittedIDs(emitted, f)
		return send(f)
	}
	var mediaStore media.MediaStore
	var isSpanActive func(string) bool
	if wh.h.agentLoop != nil {
		mediaStore = wh.h.agentLoop.GetMediaStore()
		// Real sub-turn liveness, so a delegate call whose placeholder ack has
		// not been corrected by its real end is never shown as a fabricated
		// "done" — see agent.AgentLoop.IsSubTurnActiveForSpawnCall.
		isSpanActive = wh.h.agentLoop.IsSubTurnActiveForSpawnCall
	}
	// askuserquestion-tool-spec v3 §0.6: the session's terminal
	// AskUserQuestion record, so the collapsed card is reconstructed.
	terminalAsk := loadTerminalAskRecord(wh.store, wh.attachID)
	framesEmitted, err := streamReplay(wh.ctx, wh.attachID, entries, rs, emit, mediaStore, wh.h.toolStore, isSpanActive, terminalAsk)
	durationMS := time.Since(started).Milliseconds()
	if err != nil {
		slog.Warn("ws: replay_aborted",
			"event", "replay_aborted",
			"session_id", wh.attachID,
			"frames_emitted", framesEmitted,
			"duration_ms", durationMS,
			"error", err,
		)
		wh.abortCatchUp(err)
		return nil, false
	}
	slog.Info("ws: replay_end",
		"event", "replay_end",
		"session_id", wh.attachID,
		"frames_emitted", framesEmitted,
		"duration_ms", durationMS,
		"orphan_count", rs.orphanCount,
		"duplicate_tool_call_id_count", rs.duplicateToolCallIDCount,
		"truncated_result_count", rs.truncatedResultCount,
	)
	return emitted, true
}

// recordEmittedIDs adds the ids a replayed frame carries to emitted.
func recordEmittedIDs(emitted map[string]bool, f any) {
	switch v := f.(type) {
	case generated.ReplayMessageFrame:
		if v.Id != nil {
			emitted[*v.Id] = true
		}
	case generated.ToolCallStartFrame:
		emitted[v.CallId] = true
	case generated.ToolCallResultFrame:
		emitted[v.CallId] = true
	case generated.SubagentStartFrame:
		emitted[v.SpanId] = true
	case generated.SubagentEndFrame:
		emitted[v.SpanId] = true
	case generated.GoalOutcomeFrame:
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
	wh.directFrame(generated.CatchUpCompleteFrame{
		Type:      string(generated.WsFrameTypeCatchUpComplete),
		SessionId: wh.attachID,
		Seq:       int64(wh.res.Head),
		BootId:    &bootID,
		Mode:      mode,
	})
}

func (wh *wsHandlerHandleAttachSession) direct(frame []byte) {
	if frame != nil {
		wh.wc.direct(frame)
	}
}

func (wh *wsHandlerHandleAttachSession) directFrame(frame any) {
	sendConnGenFrameDirect(wh.wc, frame)
}

// sendConnGenFrameDirect marshals frame and queues it on wc bypassing hold
// mode — for the attach goroutine's own catch-up frames.
func sendConnGenFrameDirect(wc *wsConn, frame any) {
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal catch-up frame failed", "error", err)
		return
	}
	wc.direct(data)
}

// resumeLiveSession is A8: hydrate the agent's history and re-emit goal
// status for the attached session.
func (wh *wsHandlerHandleAttachSession) resumeLiveSession() {
	// Hydrate the per-agent session.SessionStore from the transcript so the
	// next LLM turn sees the prior conversation. ADR-066 D5.5 (FR-045): only
	// an EMPTY agent archive is hydrated — an archive with ≥ 1 line is the
	// live record of the session.
	if wh.h.agentLoop.AgentArchiveNonEmpty(wh.attachID) {
		slog.Debug("ws: attach_session: agent archive non-empty; hydration skipped",
			"session_id", wh.attachID)
	} else if err := wh.h.agentLoop.HydrateAgentHistoryFromTranscript(wh.attachID); err != nil {
		slog.Warn("ws: attach_session: hydrate agent history failed",
			"session_id", wh.attachID, "error", err)
		sidCopy := wh.attachID
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			SessionId: &sidCopy,
			Message:   "could not restore conversation context — agent may not remember earlier turns",
		})
	}

	// Item 14 (review-round-1, ADR-088): goal_status is a pure live push —
	// never a replayable transcript entry — so re-emit it for the attached
	// session; this connection is already bound and receives it like any
	// other live goal_status. No-op when there is no active goal.
	wh.h.agentLoop.EmitGoalStatusRehydrate(wh.attachID)
	slog.Debug("ws: attached to session", "chat_id", wh.chatID, "session_id", wh.attachID)
}
