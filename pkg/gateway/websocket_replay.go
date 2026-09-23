// websocket_replay.go: Attach to a session and replay its history.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// snapshotLiveStreamerLocked returns sessionID's currently in-flight
// streamer's accumulated-so-far text and producer agent id (ADR-082 D3), or
// ok=false when no streamer is registered for sessionID (no turn in flight,
// or the round's streamer already finalized and unregistered). Caller must
// already hold h.mu — this MUST share the exact same critical section as the
// caller's own connection-binding step (see wsStreamer.Update's doc comment
// for the "no duplicate, no gap" ordering argument this depends on).
func (h *WSHandler) snapshotLiveStreamerLocked(sessionID string) (text, agentID string, ok bool) {
	st, exists := h.liveStreamers[sessionID]
	if !exists || st == nil {
		return "", "", false
	}
	st.statsMu.Lock()
	agentID = st.agentID
	persisted := st.transcriptPersisted
	st.statsMu.Unlock()
	// ADR-082 review CR5/F1: liveStreamers is per LLM ROUND, not per turn — a
	// later round's GetStreamer call simply overwrites the map entry (see
	// liveStreamers' own doc comment); the PREVIOUS round's streamer is never
	// explicitly removed when the round ends, only implicitly superseded. A
	// round whose narration text was already written to the transcript via
	// appendIntermediateAssistantTranscript (Bug #416; the agent loop then
	// calls markLastStreamerTranscriptPersisted → SuppressTranscriptWrite,
	// setting transcriptPersisted here) BEFORE its tool calls run therefore
	// stays registered as sessionID's live streamer for the ENTIRE tool-call
	// window that follows, with its already-persisted text still sitting in
	// st.accumulated. A connection binding during that window would
	// otherwise receive this text TWICE: once from replay (already on disk)
	// and again as a "catch-up" token from this snapshot — a duplicate
	// bubble. Reporting ok=false once the round's text is confirmed persisted
	// makes the catch-up carry only text NOT yet in the transcript: replay
	// alone covers a persisted round, and a genuinely still-streaming round
	// (transcriptPersisted still false) is unaffected.
	if persisted {
		return "", "", false
	}
	return st.accumulated.String(), agentID, true
}

// applySinceCursor applies the since-cursor filter to a slice of transcript entries.
//
// Returns entries strictly after `since`. Entries with zero timestamps (legacy
// data written before timestamps were added) are treated as oldest and dropped
// when a non-zero cursor is set — clients with legacy sessions should omit
// `since` to get full replay.
//
// When since is nil or empty, the original slice is returned unchanged (full
// replay). On parse failure an error frame is sent and the original slice is
// returned so the caller falls through to a full replay.
//
// wc may be nil in tests; the error-frame send is skipped when it is nil.
func applySinceCursor(
	_ context.Context,
	sessionID string,
	since *string,
	entries []session.TranscriptEntry,
	wc *wsConn,
) []session.TranscriptEntry {
	if since == nil || *since == "" {
		return entries
	}

	// Try RFC3339Nano first; fall back to RFC3339.
	cursor, parseErr := time.Parse(time.RFC3339Nano, *since)
	if parseErr != nil {
		var err2 error
		cursor, err2 = time.Parse(time.RFC3339, *since)
		if err2 != nil {
			slog.Warn("ws: attach_session: invalid since cursor — falling through to full replay",
				"event", "replay_since_parse_error",
				"session_id", sessionID,
				"since", *since,
				"error", parseErr,
			)
			if wc != nil {
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: "invalid since timestamp — performing full replay",
				})
			}
			return entries
		}
	}

	// Filter: keep only entries with Timestamp strictly after the cursor.
	// <= cursor means the SPA already has this entry; > cursor is new to the SPA.
	// Zero-timestamp entries (legacy data) are never After(cursor) when cursor is
	// non-zero, so they are silently dropped — log a warning if any are present.
	filtered := entries[:0:0] // reuse backing array without aliasing
	var zeroTimestampCount int
	for _, e := range entries {
		if e.Timestamp.IsZero() {
			zeroTimestampCount++
			continue
		}
		if e.Timestamp.After(cursor) {
			filtered = append(filtered, e)
		}
	}

	if zeroTimestampCount > 0 {
		slog.Warn("replay cursor: dropped legacy entries with zero timestamp",
			"event", "replay_cursor_zero_timestamp_drop",
			"session_id", sessionID,
			"zero_timestamp_count", zeroTimestampCount,
		)
	}

	skipped := len(entries) - len(filtered) - zeroTimestampCount
	if skipped > 0 || zeroTimestampCount > 0 {
		slog.Debug("replay cursor applied",
			"event", "replay_cursor_applied",
			"session_id", sessionID,
			"cursor", cursor.Format(time.RFC3339Nano),
			"skipped_count", skipped,
			"zero_timestamp_dropped", zeroTimestampCount,
		)
	}
	return filtered
}

// replayLiveBufferCap is the capacity of replayDivertCh (FR-I-009).
// Frames are diverted here via sendConnGenFrame when isReplayingLive is set;
// drained into sendCh after replay's done frame.  When the channel is full
// sendConnGenFrame drops the frame and emits a degraded warning to sendCh directly
// (W1-6) so the client still receives the overflow notice.
const replayLiveBufferCap = 1000

// errSendTimeout is returned by the replay emitFn when the send channel is
// full for more than 5 seconds (W1-10). The caller (streamReplay) surfaces
// this via W1-5's error+done emission so the client can recover.
var errSendTimeout = fmt.Errorf("ws: send channel full — replay send timeout")

// wsHandlerHandleAttachSession carries the shared state of handleAttachSession across its stages.
type wsHandlerHandleAttachSession struct {
	h              *WSHandler
	ctx            context.Context
	chatID         string
	attachID       string
	since          *string
	sinceSeq       *int64
	wc             *wsConn
	store          *session.UnifiedStore
	entries        []session.TranscriptEntry
	rs             replayStats
	replayStart    time.Time
	catchUpText    string
	catchUpAgentID string
	hasCatchUp     bool
	framesEmitted  int
	replayErr      error
	durationMS     int64

	// Incremental catch-up state (#823 phase 2). incremental is set when the
	// client's sequence cursor could be served contiguously from the session's
	// retained window; catchUp holds those already-numbered frames for
	// byte-exact re-delivery. useSnapshot is set instead when the position
	// cannot be served (retention, unknown position, or a cursor at/ahead of
	// anything emitted) — the client is then told to replace its state and gets
	// a full replay. When neither is set, the client sent no cursor at all and
	// gets the ordinary full replay.
	incremental    bool
	catchUp        []sequencedFrame
	useSnapshot    bool
	snapshotReason string
	// resetSeq is the session's high-water sequence number as captured BEFORE
	// bindConnection emits anything, and is what a snapshot tells the client to
	// reset its cursor to. It must predate the bind: frames emitted from the
	// bind onward are delivered AFTER the snapshot's full replay, so if the
	// snapshot claimed a later position the client would discard exactly those
	// frames as "already applied".
	resetSeq uint64
}

// handleAttachSession loads an existing session's transcript and replays it to
// the client via streamReplay, then sets the connection's active session to the
// requested session.
//
// FR-I-009: the connection is registered for live-event forwarding BEFORE the
// replay starts. Live events arriving during replay are buffered in a capped
// channel; after the done frame is emitted the buffer is drained to the WS in
// arrival order.
//
// since is the legacy RFC3339/RFC3339Nano cursor from AttachSessionFrame.Since;
// sinceSeq is the per-session sequence cursor from AttachSessionFrame.SinceSeq
// (#823 phase 2) and supersedes it when present.
//
// Three catch-up modes, decided by planCatchUp:
//
//   - no cursor at all   → full replay (the first-load path).
//   - since_seq servable → the retained, already-numbered frames strictly after
//     the cursor are re-delivered byte-for-byte, then a replay-terminating done
//     carrying the session's current high-water seq. O(missed window), and
//     idempotent because every frame carries the number it was first emitted
//     with.
//   - since_seq unservable → a session_snapshot frame, then a full replay, so
//     the client replaces that session's state rather than merging a partial
//     history. Never a silent drop.
func (h *WSHandler) handleAttachSession(
	ctx context.Context,
	chatID string,
	attachID string,
	since *string,
	sinceSeq *int64,
	wc *wsConn,
) {
	wh := &wsHandlerHandleAttachSession{h: h, ctx: ctx, chatID: chatID, attachID: attachID, since: since, sinceSeq: sinceSeq, wc: wc}

	if wh.loadReplay() {
		return
	}

	wh.planCatchUp()

	wh.bindConnection()

	wh.runCatchUp()

	if wh.finishReplay() {
		return
	}

	wh.sendCatchUp()

	wh.resumeLiveSession()
}

// planCatchUp decides which of the three modes this attach uses, by asking the
// session's retained window whether it can serve everything after the client's
// cursor. It runs BEFORE bindConnection so the decision is taken against a
// stable window and the frames it returns are the exact bytes already emitted.
func (wh *wsHandlerHandleAttachSession) planCatchUp() {
	if wh.sinceSeq == nil || *wh.sinceSeq <= 0 {
		// No usable cursor: first load, or a client that predates since_seq.
		return
	}
	frames, reason, ok := wh.h.catchUpFrames(wh.attachID, uint64(*wh.sinceSeq))
	if !ok {
		wh.useSnapshot = true
		wh.snapshotReason = reason
		// Captured before the bind, deliberately — see resetSeq's doc comment.
		wh.resetSeq = wh.h.sessionHighSeq(wh.attachID)
		slog.Info("ws: attach_session: cursor not servable — sending snapshot",
			"event", "replay_snapshot_fallback",
			"session_id", wh.attachID,
			"since_seq", *wh.sinceSeq,
			"reason", reason,
		)
		return
	}
	wh.incremental = true
	wh.catchUp = frames
	slog.Info("ws: attach_session: incremental catch-up",
		"event", "replay_incremental_catchup",
		"session_id", wh.attachID,
		"since_seq", *wh.sinceSeq,
		"frame_count", len(frames),
	)
}

// runCatchUp delivers this attach's frames: a snapshot notice plus a full replay
// (state replacement), the retained window's frames plus a terminator
// (incremental), or a bare full replay (no cursor).
func (wh *wsHandlerHandleAttachSession) runCatchUp() {
	if wh.useSnapshot {
		wh.emitSnapshotFrame()
		wh.runReplay()
		return
	}
	if wh.incremental {
		wh.emitIncrementalCatchUp()
		return
	}
	wh.runReplay()
}

// emitSnapshotFrame tells the client to replace this session's state, naming why
// the position it asked for could not be served. Its seq is the session's
// current high-water mark, which is also what the replay terminator will carry,
// so the client ends up positioned exactly at the gateway's newest frame.
func (wh *wsHandlerHandleAttachSession) emitSnapshotFrame() {
	frame := generated.SessionSnapshotFrame{
		Type:      string(generated.WsFrameTypeSessionSnapshot),
		SessionId: wh.attachID,
		Seq:       int64(wh.resetSeq),
	}
	reason := wh.snapshotReason
	if reason != "" {
		frame.Reason = &reason
	}
	if err := wh.emitReplayFrame(frame); err != nil {
		slog.Warn("ws: snapshot frame emit failed", "session_id", wh.attachID, "error", err)
	}
}

// emitIncrementalCatchUp re-delivers the retained frames the client missed,
// byte-exact and in order, then a replay-terminating done so the client clears
// its replaying state and adopts the session's current high-water position.
func (wh *wsHandlerHandleAttachSession) emitIncrementalCatchUp() {
	for _, sf := range wh.catchUp {
		if err := wh.emitReplayRaw(sf.raw); err != nil {
			wh.replayErr = err
			return
		}
		wh.framesEmitted++
	}
	wh.emitIncrementalTerminator()
}

// emitIncrementalTerminator sends the same shape of replay terminator
// streamReplay emits (stats.frames_emitted only — never tokens/cost, which is
// what the SPA keys on to tell a replay terminator from a real turn done).
//
// It carries NO sequence number, deliberately. The terminator is delivered
// before the divert-buffered live frames, which were emitted earlier in wall
// time but hold numbers ABOVE the ones just re-delivered; stamping the
// terminator with the session's high-water mark would set the client's cursor
// past those frames and make it discard them. The client's position after an
// incremental catch-up is the last re-delivered frame's own number.
func (wh *wsHandlerHandleAttachSession) emitIncrementalTerminator() {
	emitted := float64(wh.framesEmitted)
	frame := generated.DoneFrame{
		Type:      string(generated.WsFrameTypeDone),
		SessionId: wh.attachID,
		Stats:     &generated.DoneStats{FramesEmitted: &emitted},
	}
	if err := wh.emitReplayFrame(frame); err != nil {
		wh.replayErr = err
	}
}

// emitReplayFrame marshals one frame and hands it to the attach emitter. These
// frames go straight to sendCh (never through the divert), so they land ahead of
// any live frame buffered during the catch-up window.
func (wh *wsHandlerHandleAttachSession) emitReplayFrame(f any) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return wh.emitReplayRaw(data)
}

// emitReplayRaw hands already-marshalled bytes to the attach emitter. The
// retained catch-up frames travel this path so their bytes — and therefore
// their sequence numbers — are exactly what the client would have received live.
func (wh *wsHandlerHandleAttachSession) emitReplayRaw(data []byte) error {
	if wh.ctx.Err() != nil {
		return wh.ctx.Err()
	}
	select {
	case wh.wc.sendCh <- data:
		return nil
	case <-wh.ctx.Done():
		return wh.ctx.Err()
	case <-time.After(5 * time.Second):
		return errSendTimeout
	}
}

// loadReplay validates the session, loads its transcript, applies the cursor, and records replay-start statistics.
func (wh *wsHandlerHandleAttachSession) loadReplay() bool {
	if err := validateEntityID(wh.attachID); err != nil {
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "invalid session_id",
		})
		return true
	}

	wh.store = wh.h.resolveSessionStore(wh.attachID)
	if wh.store == nil {
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "session not found",
		})
		return true
	}

	var err error
	wh.entries, err = wh.store.ReadTranscript(wh.attachID)
	if err != nil {
		slog.Warn("ws: attach_session: could not read transcript", "session_id", wh.attachID, "error", err)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "could not read session transcript",
		})
		return true
	}

	// Apply the legacy since-cursor filter only when the client did NOT send a
	// sequence cursor. Since #823 phase 2, sequence numbers supersede the
	// timestamp filter: the timestamp filter loses entries whenever two share a
	// timestamp or a persisted entry predates the client's last live frame, and
	// a client that sends since_seq never goes through it. With since_seq the
	// transcript read below still backs the full-replay (snapshot) path, but no
	// timestamp filtering is applied to it — a snapshot replays everything.
	//
	// Boundary condition (legacy path): entries with Timestamp == cursor are
	// skipped (<=). Rationale: the cursor is the most recent frame the SPA has
	// *already processed*, so an entry at exactly that timestamp was already
	// seen. Strict less-than would re-emit the boundary entry and cause a
	// duplicate on the SPA.
	if wh.sinceSeq == nil {
		wh.entries = applySinceCursor(wh.ctx, wh.attachID, wh.since, wh.entries, wh.wc)
	}

	wh.rs = computeReplayStats(wh.entries)

	// FR-I-013: structured log at replay start.
	// Include orphan/duplicate/truncated counts so the replay_start log
	// line carries enough context to debug fidelity issues without replay_end.
	slog.Info("ws: replay_start",
		"event", "replay_start",
		"session_id", wh.attachID,
		"entry_count_loaded", len(wh.entries),
		"tool_call_count_loaded", wh.rs.toolCallCount,
		"span_count_detected", wh.rs.spanCount,
		"orphan_count", wh.rs.orphanCount,
		"duplicate_tool_call_id_count", wh.rs.duplicateToolCallIDCount,
		"truncated_result_count", wh.rs.truncatedResultCount,
	)
	wh.replayStart = time.Now()
	return false
}

// bindConnection binds the connection to the attached session, snapshots live text, and arms live-frame diversion.
func (wh *wsHandlerHandleAttachSession) bindConnection() {
	// FR-I-009 / W1-1: register for live-event forwarding BEFORE starting replay
	// so no live events are lost during the replay window.
	//
	// Live events arriving via sendConnGenFrame during replay are diverted into
	// wc.replayDivertCh (allocated below) by the atomic flag wc.isReplayingLive.
	// writePump drains wc.sendCh as normal — replay frames go there directly.
	// After the done frame, the flag is cleared and the divert buffer is drained
	// into wc.sendCh in arrival order.
	//
	// This replaces the previous wc.sendCh swap which caused a data race because
	// writePump and pingPump read wc.sendCh concurrently with no synchronization.
	if wh.wc.replayDivertCh == nil {
		wh.wc.replayDivertCh = make(chan []byte, replayLiveBufferCap)
	}

	// Register for live event forwarding now (before flipping the replay flag).
	// h.sessions is keyed by chatID for the lifetime of the connection — do NOT
	// add an attachID alias here. taskChatIDs maps chatID→attachID so the event
	// forwarder can match events emitted under the attached session's ID.
	//
	// Finding E (A-I4 round 5): h.sessionIDs[chatID] — this connection's OWN
	// chatID→session mapping, read by fanOutToSessionPeers/matchesEvent/
	// GetStreamer to decide "does this connection currently belong to session
	// X" — used to be written ONLY after replay+hydrate finished (see the
	// second h.sessionIDs[chatID] assignment below, previously the sole
	// writer). Between this function's entry and that later write, the
	// mapping kept whatever value a PRIOR attach_session on this same
	// connection last set it to (or "" for a brand-new connection). A
	// connection that attaches twice in quick succession — e.g. a reconnect
	// whose first attach_session targets a stale/leftover session id
	// (frontend activeSessionId not yet corrected to the URL's real session)
	// followed immediately by a second, correcting attach_session for the
	// right session — left h.sessionIDs[chatID] pointing at the FIRST
	// (wrong, unrelated) session for this whole replay window. Any OTHER,
	// genuinely unrelated session's background delegate fanning out a live
	// token/done frame during that window (fanOutToSessionPeers, matched
	// purely by sessionID equality) found this connection listed as one of
	// its peers and delivered straight into wc.sendCh — landing mid-replay,
	// live-verified as a stray, uninstructed bubble duplicating another
	// session's already-delivered content with a garbled leading fragment.
	// Setting the real mapping HERE, atomically with taskChatIDs/the
	// self-map, closes the window: from this point on h.sessionIDs[chatID]
	// always reflects the CURRENT attach target, so no other session can
	// ever be mistaken for a peer of this connection. Purely additive — the
	// second assignment after replay/hydrate is left in place as a
	// redundant, idempotent reaffirmation.
	//
	// ADR-082 D2: fanOutToSessionPeers (named above, historical) no longer
	// exists — WSHandler.resolveSessionConnsLocked is its successor,
	// reading this SAME h.sessionIDs[chatID] mapping (plus h.sessions) as
	// its single per-frame resolution point for every wsStreamer.Update/
	// Finalize call. The invariant this comment documents — h.sessionIDs
	// must reflect the CURRENT attach target atomically with this bind —
	// is unchanged and, if anything, more load-bearing now: it is also what
	// D3's catch-up snapshot ordering depends on (see
	// snapshotLiveStreamerLocked's doc comment, called a few lines below).
	// ADR-082 D3: snapshot attachID's in-flight streamer (if any) atomically
	// with binding this connection, under the SAME h.mu critical section
	// wsStreamer.Update uses for its own append+resolve — see
	// WSHandler.snapshotLiveStreamerLocked's doc comment for the "no
	// duplicate, no gap" ordering argument this depends on. catchUpText/
	// catchUpAgentID are used after replay finishes but before the
	// divert-buffered live frames are drained, below.
	wh.h.mu.Lock()
	if oldTID, ok := wh.h.taskChatIDs[wh.chatID]; ok {
		delete(wh.h.sessionIDs, oldTID)
	}
	wh.h.taskChatIDs[wh.chatID] = wh.attachID
	wh.h.sessionIDs[wh.attachID] = wh.attachID
	wh.h.sessionIDs[wh.chatID] = wh.attachID
	wh.catchUpText, wh.catchUpAgentID, wh.hasCatchUp = wh.h.snapshotLiveStreamerLocked(wh.attachID)
	// ADR-082 review CR1/S1: emit session_state{session_id, active_turn} from
	// this SAME h.mu critical section, BEFORE any replay frame — not, as
	// before this fix, at the very end of this function (after the replay-
	// terminating done, the catch-up token, AND the divert drain). Emitting
	// it here, atomically with the bind+snapshot above, is what makes it the
	// FIRST frame this attach delivers: emitSessionState only enqueues onto
	// wc.sendCh (a non-blocking, best-effort select) and touches no state
	// this critical section doesn't already hold, so calling it while still
	// holding h.mu is safe — and it means the active_turn this frame reports
	// is resolved at the exact same instant as the catch-up snapshot, closing
	// the window where a turn ending between two separate lock acquisitions
	// could make the two disagree. The wire order this produces:
	// session_state → replay_message* → done{frames_emitted} →
	// token{catch-up} → token*{live} → done{stats.tokens}. This call also
	// covers the replay-error early return below (CR10) — session_state is
	// already sent by the time any replay failure could occur.
	wh.h.emitSessionState(wh.wc, wh.attachID)
	// Issue #822: arm diversion BEFORE releasing h.mu. Finalize resolves this
	// connection under the same lock and sends its terminal done immediately
	// afterward. Unlocking first left a gap where done could reach sendCh ahead
	// of replay/catch-up; the later catch-up token then had no terminal frame
	// left to close it. session_state is already in sendCh, so it remains the
	// first frame for this attach.
	wh.wc.isReplayingLive.Store(true)
	wh.h.mu.Unlock()
}

// runReplay constructs the replay emitter and streams the persisted session frames.
func (wh *wsHandlerHandleAttachSession) runReplay() {
	// Run replay: emit frames directly into wc.sendCh via emitFn, bypassing the
	// divert.  W1-10: a per-frame 5 s timeout prevents indefinite blocking when
	// the client is not draining the socket.
	emitFn := func(f any) error {
		if wh.ctx.Err() != nil {
			return wh.ctx.Err()
		}
		data, merr := json.Marshal(f)
		if merr != nil {
			return merr
		}
		select {
		case wh.wc.sendCh <- data:
			return nil
		case <-wh.ctx.Done():
			return wh.ctx.Err()
		case <-time.After(5 * time.Second):
			return errSendTimeout
		}
	}

	// Pass pre-computed rs into streamReplay so it doesn't rebuild
	// spawnIDsWithChildren for a second time.
	var mediaStore media.MediaStore
	var isSpanActive func(string) bool
	if wh.h.agentLoop != nil {
		mediaStore = wh.h.agentLoop.GetMediaStore()
		// Wire real sub-turn liveness into replay so a spawn/delegate call
		// whose placeholder ack (async delegation: Status="success",
		// DurationMS≈0) has not yet been corrected by the real
		// EventKindSubTurnEnd is never shown as a fabricated "done" — see
		// agent.AgentLoop.IsSubTurnActiveForSpawnCall's doc comment.
		isSpanActive = wh.h.agentLoop.IsSubTurnActiveForSpawnCall
	}
	// askuserquestion-tool-spec v3 §0.6: hand replay the session's terminal
	// (answered/cancelled) AskUserQuestion record, if any, so the collapsed
	// card is reconstructed on cold history load and the §0.2 resume message
	// never renders as a raw JSON bubble — see streamReplay's terminalAsk doc.
	terminalAsk := loadTerminalAskRecord(wh.store, wh.attachID)
	wh.framesEmitted, wh.replayErr = streamReplay(wh.ctx, wh.attachID, wh.entries, wh.rs, emitFn, mediaStore, wh.h.toolStore, isSpanActive, terminalAsk)

	wh.durationMS = time.Since(wh.replayStart).Milliseconds()
}

// finishReplay handles replay failure or records successful replay completion.
func (wh *wsHandlerHandleAttachSession) finishReplay() bool {
	if wh.replayErr != nil {
		// Disarm the divert before emitting the abort frames so that sendConnGenFrame
		// routes them to sendCh.
		wh.wc.isReplayingLive.Store(false)
		// ADR-082 review CR10: the divert was armed (wc.isReplayingLive.Store(true),
		// above) and this connection could have been diverting live frames into
		// wc.replayDivertCh for the whole failed-replay window — those frames
		// have nowhere meaningful left to go (the client is about to be told,
		// via the error+done frames below, that this replay was aborted and it
		// should reset), so discard them here rather than leaving them sitting
		// in the channel. wc.replayDivertCh is allocated once and REUSED across
		// attaches on the same connection (see its allocation above) — an
		// undrained buffer here does not just vanish, it leaks into the NEXT
		// attach_session on this connection, delivered at the wrong point in
		// that later attach's own frame sequence as stale duplicates. This
		// connection was already bound (h.sessionIDs/h.taskChatIDs, above) and
		// already got its session_state{session_id, active_turn} (CR1, emitted
		// atomically with the bind before replay even started) — this fix adds
		// only the missing divert cleanup; the previous version returned here
		// leaving replayDivertCh untouched.
		drainReplayDivertChDiscard(wh.wc)
		slog.Warn("ws: replay_aborted",
			"event", "replay_aborted",
			"session_id", wh.attachID,
			"frames_emitted", wh.framesEmitted,
			"duration_ms", wh.durationMS,
			"error", wh.replayErr,
		)
		// W1-5: emit error + synthetic done so the client clears isReplaying and
		// re-enables the composer.  Use sendConnGenFrame (generated types).
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			SessionId: &wh.attachID,
			Message:   "replay aborted: " + wh.replayErr.Error(),
		})
		replayErrTrue := true
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeDone), generated.DoneFrame{
			Type:      string(generated.WsFrameTypeDone),
			SessionId: wh.attachID,
			Stats: &generated.DoneStats{
				ReplayError: &replayErrTrue,
			},
		})
		return true
	}

	// FR-I-013: structured log at replay end.
	// Include the full stats set so replay_end is a self-contained diagnostic record.
	slog.Info("ws: replay_end",
		"event", "replay_end",
		"session_id", wh.attachID,
		"frames_emitted", wh.framesEmitted,
		"duration_ms", wh.durationMS,
		"orphan_count", wh.rs.orphanCount,
		"duplicate_tool_call_id_count", wh.rs.duplicateToolCallIDCount,
		"truncated_result_count", wh.rs.truncatedResultCount,
	)
	return false
}

// sendCatchUp sends the live streamer's accumulated text after persisted replay frames.
func (wh *wsHandlerHandleAttachSession) sendCatchUp() {
	// ADR-082 D3/FR-007: catch-up token. If attachID had an in-flight
	// streamer at bind time (snapshotted above, atomically with the bind),
	// emit ONE token frame carrying its accumulated-so-far text — written
	// DIRECTLY to wc.sendCh (like the replay frames above), never through
	// sendConnGenFrame/sendRawFrameBytes, so it is never itself diverted.
	// This must land strictly between the replay history (already in
	// wc.sendCh) and the divert-buffered live frames drained just below:
	// any Update() that ran AFTER this connection's bind is (a) resolved as
	// including this connection (wsStreamer.Update shares the exact same
	// h.mu critical section the bind+snapshot above used) and (b) buffered
	// in wc.replayDivertCh, since wc.isReplayingLive is still true right
	// now — so the drain immediately below delivers exactly the deltas that
	// postdate this snapshot, in arrival order, with no duplicate and no
	// gap. Skipped when there is no in-flight turn (hasCatchUp false) or
	// its accumulated text is still empty.
	if wh.hasCatchUp && wh.catchUpText != "" {
		// #823 phase 2: this catch-up token REPLACES the open bubble's content
		// rather than appending to it. The client may already have applied part
		// of this turn's text before the drop, and the gateway cannot know how
		// much, so replacing is the only way the frame is idempotent: applying
		// it twice leaves the same text. It carries a freshly assigned
		// per-session seq so it is ordered with (and deduped against) the rest
		// of the session's numbered stream.
		replace := true
		catchUpFrame := generated.TokenFrame{
			Type:      string(generated.WsFrameTypeToken),
			Content:   wh.catchUpText,
			SessionId: wh.attachID,
			Replace:   &replace,
		}
		if wh.catchUpAgentID != "" {
			catchUpFrame.AgentId = &wh.catchUpAgentID
		}
		// #823 phase 2: this frame carries NO sequence number. It is emitted here,
		// after the divert buffer has already been filled by frames emitted
		// during the replay window, but delivered BEFORE them (the drain runs
		// below). Numbering it by emission order would put a higher number ahead
		// of lower ones and make the client discard those live frames. It is a
		// state frame, not an event: Replace makes it idempotent on its own.
		if data, mErr := json.Marshal(catchUpFrame); mErr == nil {
			// ADR-082 review CR9/F4: route this through the SAME droppedTokens
			// accounting path every other token send uses (sendRawFrameBytes),
			// instead of the previous bare select that neither counted a
			// timeout drop nor handled a dead connection at all (the
			// ctx.Done() arm silently discarded the frame with no counter
			// update, no log line). This send still must NOT go through
			// sendRawFrameBytes itself — wc.isReplayingLive is still true at
			// this point, so that function would (correctly, for every OTHER
			// caller) divert it into replayDivertCh instead of sendCh, which
			// would be wrong here: the catch-up token must land directly in
			// sendCh, strictly between the replay history and the
			// divert-buffered live frames drained just below.
			select {
			case wh.wc.sendCh <- data:
			case <-wh.wc.doneCh:
				wh.wc.droppedTokens.Add(1)
			case <-wh.ctx.Done():
				wh.wc.droppedTokens.Add(1)
			case <-time.After(5 * time.Second):
				slog.Warn("ws: catch-up token send timed out", "session_id", wh.attachID)
				wh.wc.droppedTokens.Add(1)
			}
		} else {
			slog.Error("ws: marshal catch-up token frame failed", "session_id", wh.attachID, "error", mErr)
		}
	}
}

// resumeLiveSession drains diverted live frames, hydrates agent history, and re-emits goal status.
func (wh *wsHandlerHandleAttachSession) resumeLiveSession() {
	// FR-I-009: drain any live events buffered during replay, in arrival order,
	// BEFORE disarming the divert flag.
	//
	// Ordering guarantee (see docs/internal/investigation/bug-5-replay-order.md and
	// code-reviewer Finding #2 / architect Finding #4):
	//
	//   The flag must be cleared AFTER the drain, not before.  Clearing it first
	//   opens a window where concurrent sendRawFrameBytes callers write live frames
	//   directly to sendCh while the drain loop is still moving buffered divert
	//   frames into sendCh, inverting FIFO order.
	//
	//   Drain-then-disarm is safe when guarded by replayMu:
	//   - We hold replayMu.Lock() for the entire drain+disarm sequence.
	//   - sendRawFrameBytes holds replayMu.RLock() while choosing a target channel
	//     and completing its send.  This prevents the TOCTOU race where a writer
	//     snapshots isReplayingLive==true, is descheduled, the drain empties
	//     replayDivertCh and clears the flag, and the writer then sends to the
	//     now-abandoned replayDivertCh.
	//   - After the drain+disarm, replayMu is released; future sendRawFrameBytes
	//     calls see isReplayingLive==false on the first atomic load (fast path, no
	//     lock taken) and route directly to sendCh in the correct position.
	//
	//   Back-pressure defense (architect Finding #4): each frame send inside the
	//   drain uses a 1-second deadline.  If sendCh is full and the client is slow,
	//   the frame is dropped rather than blocking the drain indefinitely.  A drop
	//   here is a live message the user will never see, so drainReplayDivert
	//   reports it to the client instead of only counting it — see its doc comment.
	if !drainReplayDivert(wh.ctx, wh.wc, wh.attachID, wh.chatID) {
		return
	}

	wh.h.mu.Lock()
	wh.h.sessionIDs[wh.chatID] = wh.attachID
	wh.h.mu.Unlock()

	// Hydrate the per-agent session.SessionStore from the transcript so the
	// next LLM turn sees the prior conversation. Without this, the SPA
	// shows replayed messages but the agent answers as if the session just
	// started — see pkg/agent/attach_hydrate.go for the rationale.
	//
	// ADR-066 D5.5 (FR-045): only an EMPTY agent archive is hydrated. An
	// archive with ≥ 1 line is the live record of the session — rebuilding
	// it from the UI transcript was the verified mechanism that dropped
	// every tool result and reset Skip on each reopen (US-15).
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

	// Item 14 (review-round-1, ADR-088): goal_status is a pure live push
	// (agent.EventKindGoalStatusChanged) — never a persisted, replayable
	// transcript entry the streamReplay above reconstructs — so a
	// registered goal's record card (definition/criteria/dod) had no
	// rehydration path on an SPA reload/reconnect. This connection already
	// registered for live-event forwarding earlier in this function (before
	// replay started), so a re-emitted event here reaches it exactly like
	// any other live goal_status push. No-op when the attached session has
	// no active goal or no record registered yet.
	wh.h.agentLoop.EmitGoalStatusRehydrate(wh.attachID)

	// ADR-082 review CR1/S1: session_state is now emitted ONCE, at the START
	// of this attach (atomically with the bind + catch-up snapshot, above) —
	// not here. The trailing re-emit this comment used to describe was itself
	// the bug S1 found: it left session_state as the LAST frame of the
	// attach (after the replay-terminating done, the catch-up token, and the
	// divert drain), so a reconnecting SPA had no active_turn signal until
	// after everything else had already arrived.
	slog.Debug("ws: attached to session", "chat_id", wh.chatID, "session_id", wh.attachID)
}

// drainReplayDivertChDiscard empties wc.replayDivertCh, discarding every
// buffered frame, without touching wc.sendCh or wc.isReplayingLive.
//
// [ADR-082 review CR10] Used by handleAttachSession's replay-error path. The
// client has already been told, via the error+done frames sent immediately
// around this call, that the replay was aborted and it should reset — so any
// live frames buffered in replayDivertCh from the aborted replay window have
// nowhere meaningful left to land in THIS attach. Simply leaving them
// in the channel would not just be inert: wc.replayDivertCh is allocated
// once and reused across every later attach_session on this same connection
// (see its lazy allocation in handleAttachSession), so an undrained buffer
// here leaks stale frames into the NEXT attach's own post-replay drain,
// delivered at the wrong point in that later attach's sequence as
// duplicates.
func drainReplayDivertChDiscard(wc *wsConn) {
	for {
		select {
		case <-wc.replayDivertCh:
		default:
			return
		}
	}
}

// drainReplayDivert moves every live frame buffered during a since-cursor replay
// out of wc.replayDivertCh into wc.sendCh, then disarms the divert.  It returns
// false when the connection context was cancelled mid-drain, in which case the
// caller must abandon the rest of the attach.
//
// Ordering guarantee: the drain holds wc.replayMu.Lock() for the ENTIRE
// drain+disarm sequence, and clears wc.isReplayingLive AFTER the drain, never
// before.  Clearing first would let concurrent sendRawFrameBytes callers write
// live frames straight to sendCh while buffered divert frames are still being
// moved, inverting FIFO order.  See handleAttachSession's call site for the full
// rationale.
//
// Dropped-frame reporting — the reason this is not just a counter bump:
// a frame dropped here is a LIVE message that arrived while the client was
// replaying and that the client will now never receive.  The connection stays
// open and nothing else re-sends it, so the user sees a silently truncated reply
// on reconnect.  Incrementing wc.droppedFrames alone cannot surface that: the
// only threshold check that emits the user-visible "connection degraded" frame
// lives in sendRawFrameBytes, and every success path in that function calls
// wc.droppedFrames.Store(0) — so the next frame that goes through erases the
// evidence before it can ever be reported.  We therefore count drops LOCALLY and
// tell the client directly, once, as soon as the drain finishes.
//
// The report goes out as an "error" frame, which sendRawFrameBytes treats as
// critical (see its isCritical expression): it takes the blocking path with a
// 5 s budget instead of the best-effort backoff that dropped the frames in the
// first place, so the report itself cannot be lost to the same backpressure.
func drainReplayDivert(ctx context.Context, wc *wsConn, attachID, chatID string) bool {
	var dropped int

	wc.replayMu.Lock()
drainLoop:
	for {
		select {
		case raw := <-wc.replayDivertCh:
			select {
			case wc.sendCh <- raw:
			case <-time.After(1 * time.Second):
				// Counted locally, NOT on wc.droppedFrames — see the doc comment.
				dropped++
				slog.Warn("ws: replay drain frame timed out, dropping",
					"session_id", attachID,
					"chat_id", chatID)
			case <-ctx.Done():
				wc.isReplayingLive.Store(false)
				wc.replayMu.Unlock()
				if dropped > 0 {
					// The connection is going away, so there is nobody left to
					// tell — but the loss still happened and must not vanish
					// from the record.
					slog.Error("ws: replay drain dropped live frames before the connection closed",
						"event", "replay_drain_frames_dropped",
						"session_id", attachID,
						"chat_id", chatID,
						"dropped_count", dropped,
						"reported_to_client", false)
				}
				return false
			}
		default:
			break drainLoop
		}
	}
	// Disarm AFTER drain, while still holding replayMu.Lock().  Releasing the lock
	// after the Store ensures any writer that is queued behind our Lock() will see
	// isReplayingLive==false on its re-check and route to sendCh directly.
	wc.isReplayingLive.Store(false)
	wc.replayMu.Unlock()

	if dropped == 0 {
		return true
	}

	slog.Error("ws: replay drain dropped live frames",
		"event", "replay_drain_frames_dropped",
		"session_id", attachID,
		"chat_id", chatID,
		"dropped_count", dropped,
		"reported_to_client", true)

	// Emitted AFTER the Unlock above: sendRawFrameBytes takes replayMu.RLock() on
	// its divert path, and an "error" frame must in any case reach the canonical
	// sendCh rather than the divert buffer we have just abandoned.
	//
	// The wording deliberately starts with the count (a digit) rather than an
	// identifier: the SPA runs sanitizeLegacyErrorMessage (src/lib/llm-error.ts)
	// over any untyped error message and replaces anything shaped like
	// "<identifier>: ..." with a generic apology, which would erase the
	// instruction this frame exists to deliver.
	sessionIDCopy := attachID
	const recovery = " Reopen this conversation to reload the full transcript."
	message := fmt.Sprintf(
		"%d live updates could not be delivered while reconnecting and are missing from this view.%s",
		dropped, recovery)
	if dropped == 1 {
		message = "1 live update could not be delivered while reconnecting and is missing from this view." + recovery
	}
	sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
		Type:      string(generated.WsFrameTypeError),
		SessionId: &sessionIDCopy,
		Message:   message,
	})
	return true
}
