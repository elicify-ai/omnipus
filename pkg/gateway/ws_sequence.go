// ws_sequence.go: per-session sequence numbers for reconnect catch-up (#823).
//
// # Why this exists
//
// Reconnect catch-up used to be driven by a timestamp cursor: the SPA sent the
// timestamp of the last frame it had processed as `since`, and the gateway
// replayed entries strictly after it (websocket_replay.go::applySinceCursor).
// That filter loses entries for two reasons, both reproduced by the receipts
// captured for #823:
//
//   - two entries written with the same timestamp are both filtered out, since
//     neither is strictly after the cursor; and
//   - a persisted assistant entry whose timestamp PRECEDES the last live frame
//     the SPA processed is filtered out, so the finished answer never arrives
//     and the message stays stuck "running". That is the #822 / S-10 class.
//
// A sequence number is a positional fact, not a clock reading, so neither
// ambiguity can arise: the client stores the highest number it has applied per
// session and the gateway serves everything strictly after it.
//
// # Scope of the numbering
//
// Numbers are per SESSION — not global, not per connection — so a client's
// cursor survives reconnects and means the same thing on every attach. The
// counter and the retained window both live in WSHandler, guarded by WSHandler.mu
// like every other piece of per-session connection state.
//
// Two producers reach a client: shared fan-out paths (wsStreamer.Update's
// tokens, wsStreamerFinalize's done, webchatChannel.Send), where the number is
// assigned ONCE before the fan-out so every attached tab sees the same number
// for the same frame; and the per-connection agent-event forwarder
// (websocket_forward.go), whose frames are converted independently per
// connection — there the number is assigned per emitted frame, so two tabs can
// hold different numbers for one event. Each client's own stream is still
// monotonic and gap-free, which is what its cursor depends on, and a catch-up
// replay of the other tab's copy is absorbed by the SPA's existing content-level
// dedupe (message id, call id). Making the forwarder's numbering shared needs a
// single event->frame conversion point, which does not exist yet; see the
// squad report's "honest gaps".
//
// A frame is numbered only when its emission order equals its delivery order —
// see withFrameSeq's doc comment for the frames that are deliberately excluded
// (session_state, the replay terminator, the catch-up token).
//
// # Retention and snapshot fallback
//
// The gateway retains only the most recent seqJournalCap numbered frames per
// session. When a client asks for a position that window cannot serve
// contiguously, guessing would risk dropping events, so the gateway answers
// with a session_snapshot frame instead and the client rebuilds that session's
// state from a full replay. The same fallback covers a gateway restart (the
// in-memory counter restarts, so a client's cursor is ahead of anything the
// gateway has emitted) and a position it has no record of at all.
//
// A known consequence of counting only emitted frames: a cursor equal to the
// gateway's newest frame is ALSO answered with a snapshot rather than "you are
// up to date", because the gateway emits nothing while no client is attached —
// so content produced during an outage with no listeners leaves no trace in the
// counter, and an empty catch-up would silently lose it. See catchUpFrames.
package gateway

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// seqJournalCap is the target size of a session's retained frame window for
// incremental catch-up. Trimming happens in batches (once the window exceeds
// 150% of this value the oldest entries are dropped back to it), so a session
// retains at least seqJournalCap and at most 150% of it — see
// recordSeqFrameLocked's trim step.
// Correctness never depends on the window's size: a client whose cursor falls
// outside it is served a snapshot instead of a range with a hole in it.
const seqJournalCap = 1024

// seqJournalByteCap bounds a session's retained window by total raw bytes,
// independent of seqJournalCap's frame count (#823 review finding 8). A
// session whose individual frames are large — tool results up to 50KiB
// inline, a separate copy per attached tab from the per-connection forwarder
// — can blow well past a sane memory budget long before it reaches
// seqJournalCap frames. 8 MiB retained per session is generous headroom over
// the worst realistic incremental catch-up (a handful of large tool results
// plus ordinary token traffic) while still bounding the total.
const seqJournalByteCap = 8 * 1024 * 1024

// seqIdleEvictAfter and seqSweepInterval bound how long a session's counter
// and retained window stay in memory with no activity (#823 review finding
// 8: "The stored-frame history grows without limit across sessions... every
// session and delegated child session active since boot stays in memory").
// Dropping an idle session's entry is always safe: a later reconnect for it
// just gets seqReasonUnknownPosition (catchUpFramesLocked) instead of an
// incremental catch-up, which — per catchUpFrames' own doc comment — is the
// same non-lossy snapshot fallback an unservable position already produces
// for any other reason. seqSweepInterval rate-limits the sweep so it stays
// cheap on the hot numbering path (assignSeqLocked) rather than needing a
// dedicated background goroutine with its own lifecycle to manage (and,
// under this repo's "never leak a goroutine in tests" discipline, to leak).
const (
	seqIdleEvictAfter = 30 * time.Minute
	seqSweepInterval  = 5 * time.Minute
)

// wsSeqNow is a swappable clock seam so tests can simulate idle time passing
// without a real 30-minute sleep — same pattern pkg/session uses for its own
// swappable lock seams (FR-101).
var wsSeqNow = time.Now

// Snapshot reasons carried on SessionSnapshotFrame.Reason.
const (
	// seqReasonCursorAhead: the client's position is at or beyond anything the
	// gateway has emitted for this session — for example after a gateway
	// restart, whose counter starts over. Also returned when the client's
	// position is exactly the newest emitted frame: the gateway cannot prove
	// that nothing happened in between (frames are only emitted while at least
	// one client is attached, so an outage with no listeners leaves no trace in
	// the counter), and a needless full replay is strictly safer than a silent
	// content loss.
	seqReasonCursorAhead = "cursor_ahead"
	// seqReasonRetentionExceeded: the frames immediately after the client's
	// position have already been discarded, so the range cannot be served
	// without a hole.
	seqReasonRetentionExceeded = "retention_exceeded"
	// seqReasonUnknownPosition: the gateway has no emitted-frame record for
	// this session at all.
	seqReasonUnknownPosition = "unknown_position"
	// seqReasonBootMismatch (#823 review finding 7): the client's
	// attach_session carried a boot_id that does not match this gateway
	// process' own — see wsHandlerHandleAttachSession.resolveCatchUpLocked.
	// Checked and answered before the other three reasons, so it always wins.
	seqReasonBootMismatch = "boot_mismatch"
)

// sequencedFrame is one already-emitted session frame, retained for byte-exact
// re-delivery: the same bytes and the same seq the client would have received
// live, which is what makes catch-up idempotent.
type sequencedFrame struct {
	seq  uint64
	kind string
	raw  []byte
}

// sessionSeq is one session's monotonic counter plus its retained window.
// frames is contiguous and ascending: frames[i].seq == frames[0].seq + i.
type sessionSeq struct {
	high   uint64
	frames []sequencedFrame
	// bytes is the running total of len(raw) across frames — #823 review
	// finding 8's independent byte cap. Kept incrementally rather than
	// recomputed so the trim step in recordSeqFrameLocked stays O(trimmed),
	// not O(retained).
	bytes int
	// lastActive is the wall-clock time of this session's most recent
	// assignSeqLocked call — #823 review finding 8's idle-eviction input.
	lastActive time.Time
}

// assignSeqLocked returns the next sequence number for sessionID, creating the
// session's counter on first use. Caller must hold h.mu.
//
// The counter only ever moves forward, and only for frames actually emitted to
// at least one connection, so the numbers a client can observe are gap-free.
func (h *WSHandler) assignSeqLocked(sessionID string) uint64 {
	h.maybeEvictIdleSessionsLocked()
	if h.sequences == nil {
		h.sequences = make(map[string]*sessionSeq)
	}
	s := h.sequences[sessionID]
	if s == nil {
		s = &sessionSeq{}
		h.sequences[sessionID] = s
	}
	s.high++
	s.lastActive = wsSeqNow()
	return s.high
}

// recordSeqFrameLocked appends an emitted frame to sessionID's retained window,
// trimming the oldest entries in one batch — once the window has grown past
// 150% of seqJournalCap OR its retained bytes exceed seqJournalByteCap (#823
// review finding 8) — so the per-frame cost stays amortised. Caller must hold
// h.mu.
func (h *WSHandler) recordSeqFrameLocked(sessionID, kind string, seq uint64, raw []byte) {
	s := h.sequences[sessionID]
	if s == nil {
		return
	}
	s.frames = append(s.frames, sequencedFrame{seq: seq, kind: kind, raw: raw})
	s.bytes += len(raw)

	trimTo := 0
	if len(s.frames) > seqJournalCap*3/2 {
		trimTo = len(s.frames) - seqJournalCap
	}
	if s.bytes > seqJournalByteCap {
		// Walk forward from the frame-count trim point until the retained
		// bytes are back under budget. Floor of 1 frame retained so
		// frames[0] always exists for the oldest-boundary check in
		// catchUpFramesLocked.
		bytesToDrop := s.bytes - seqJournalByteCap
		dropped := 0
		for i := trimTo; i < len(s.frames)-1 && dropped < bytesToDrop; i++ {
			dropped += len(s.frames[i].raw)
			trimTo = i + 1
		}
	}
	if trimTo > 0 {
		var droppedBytes int
		for _, f := range s.frames[:trimTo] {
			droppedBytes += len(f.raw)
		}
		// Copy out (not a bare reslice) so the dropped frames' raw bytes are
		// actually released to the GC instead of staying pinned by the old
		// backing array's still-live capacity.
		keep := make([]sequencedFrame, len(s.frames)-trimTo)
		copy(keep, s.frames[trimTo:])
		s.frames = keep
		s.bytes -= droppedBytes
	}
}

// maybeEvictIdleSessionsLocked runs evictIdleSessionsLocked at most once per
// seqSweepInterval, piggybacked on the numbering hot path (assignSeqLocked)
// rather than a dedicated background goroutine — see seqIdleEvictAfter's doc
// comment. Caller must hold h.mu.
func (h *WSHandler) maybeEvictIdleSessionsLocked() {
	now := wsSeqNow()
	if !h.seqLastSweep.IsZero() && now.Sub(h.seqLastSweep) < seqSweepInterval {
		return
	}
	h.seqLastSweep = now
	h.evictIdleSessionsLocked(seqIdleEvictAfter)
}

// evictIdleSessionsLocked drops the counter and retained window for every
// session whose last recorded activity is older than idleAfter (#823 review
// finding 8). Returns the number of sessions evicted, for tests and logging.
// Caller must hold h.mu.
//
// Safe by construction: a session dropped here that later gets a
// reconnect attempt just answers seqReasonUnknownPosition (a servable
// cursor cannot be told apart from "nothing was ever emitted"), which
// catchUpFrames' own doc comment already treats as a safe, non-lossy
// snapshot fallback — the SAME fallback an unservable retention window or a
// gateway restart already produce for the same unforced reason. It is not
// gated on whether any connection is still attached: a connection idle for
// seqIdleEvictAfter has, by definition, produced no frame in that window, so
// there is no turn in flight to interrupt — the very next frame that
// session's own streamer emits after an eviction (via assignSeqLocked)
// simply starts the counter over at 1, which any live-attached connection
// applies directly (it only consults since_seq at reconnect/attach time, not
// for ordinary in-place delivery) and that same connection's own later
// reconnect (if any) resolves via the identical cursor_ahead/unknown_position
// snapshot fallback.
func (h *WSHandler) evictIdleSessionsLocked(idleAfter time.Duration) int {
	if len(h.sequences) == 0 {
		return 0
	}
	cutoff := wsSeqNow().Add(-idleAfter)
	evicted := 0
	for id, s := range h.sequences {
		if s.lastActive.Before(cutoff) {
			delete(h.sequences, id)
			evicted++
		}
	}
	return evicted
}

// sessionHighSeq returns the highest sequence number emitted for sessionID, or
// 0 when the gateway has emitted none.
func (h *WSHandler) sessionHighSeq(sessionID string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessionHighSeqLocked(sessionID)
}

// sessionHighSeqLocked is sessionHighSeq's body, for callers that already
// hold h.mu (bindConnection — #823 review finding 6).
func (h *WSHandler) sessionHighSeqLocked(sessionID string) uint64 {
	if s := h.sequences[sessionID]; s != nil {
		return s.high
	}
	return 0
}

// catchUpFrames returns the retained frames with seq strictly greater than
// `after`, for byte-exact re-delivery to a catching-up client.
//
// ok=false means "do not guess" — the caller must send a snapshot instead, and
// reason says why. The three refusal cases are deliberate:
//
//   - unknown_position: nothing has been emitted for this session, so the
//     client's cursor cannot be validated against anything.
//   - cursor_ahead: `after` is at or beyond the newest emitted frame. Note that
//     `after == high` is refused rather than answered with an empty slice: the
//     gateway emits nothing while no client is attached, so "no frames after
//     your cursor" does NOT imply "nothing happened" — content may have been
//     produced during an outage with no listeners. A snapshot costs a replay;
//     an empty answer could cost the user their answer.
//   - retention_exceeded: the window no longer reaches back to `after + 1`, so
//     serving the retained frames would leave a hole in the client's stream.
func (h *WSHandler) catchUpFrames(sessionID string, after uint64) ([]sequencedFrame, string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.catchUpFramesLocked(sessionID, after)
}

// catchUpFramesLocked is catchUpFrames' body, for callers that already hold
// h.mu — #823 review finding 6 needs the catch-up decision made inside
// bindConnection's own critical section (see that function's doc comment),
// not in a separate, earlier lock acquisition that leaves a window between
// "decide what's owed" and "start receiving live frames for this session".
func (h *WSHandler) catchUpFramesLocked(sessionID string, after uint64) ([]sequencedFrame, string, bool) {
	s := h.sequences[sessionID]
	if s == nil || s.high == 0 {
		return nil, seqReasonUnknownPosition, false
	}
	if after >= s.high {
		return nil, seqReasonCursorAhead, false
	}
	// after < high, so at least one frame is owed. It is servable only if the
	// window still reaches back to it.
	oldest := s.high
	if len(s.frames) > 0 {
		oldest = s.frames[0].seq
	}
	if after+1 < oldest {
		return nil, seqReasonRetentionExceeded, false
	}

	out := make([]sequencedFrame, 0, s.high-after)
	for _, f := range s.frames {
		if f.seq > after {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		// Contiguity guarantees this cannot happen when oldest <= after+1 <= high
		// and after < high; refuse rather than serve a silent hole.
		return nil, seqReasonRetentionExceeded, false
	}
	// #823 review finding 1: verify the range served is gap-free AND reaches
	// the session's current high-water mark before promising it to the
	// caller. Every seq this package hands out is meant to always be paired
	// with a stored frame in the SAME locked step (assignSeqLocked +
	// recordSeqFrameLocked — see sendDone's fix for the one place that used
	// to violate this), so this should never trip in a build with that fix
	// applied. But "should never" is exactly the class of bug that produced
	// finding 1 in the first place — a sequence number spent with nothing
	// retained for it — and a served range with a hole or a gap before the
	// head is indistinguishable, on the wire, from a lost frame. Refuse
	// (forcing a snapshot) rather than trust the invariant blindly.
	if out[0].seq != after+1 || out[len(out)-1].seq != s.high {
		return nil, seqReasonRetentionExceeded, false
	}
	for i := 1; i < len(out); i++ {
		if out[i].seq != out[i-1].seq+1 {
			return nil, seqReasonRetentionExceeded, false
		}
	}
	return out, "", true
}

// withFrameSeq returns a copy of frame carrying seq, and reports whether frame
// participates in numbering.
//
// Numbering requires a frame's EMISSION order to equal its DELIVERY order, so a
// frame is numbered only when both hold. Three kinds are deliberately excluded:
//
//   - session_state is emitted from the attach's bind step but delivered as the
//     attach's FIRST frame (ADR-082 review CR1/S1), i.e. before catch-up frames
//     that were emitted earlier and therefore carry lower numbers. Numbering it
//     would place a higher number ahead of lower ones.
//   - The replay terminator and the catch-up token are emitted after frames the
//     divert buffer delivers later (the drain runs after the terminator). They
//     are control/state frames, not conversation events: the terminator tells
//     the client the replay is complete, and the catch-up token REPLACES the
//     open bubble's text. Neither advances a client's position.
//   - Connection-scoped and global frames (pong, plan_status, task_run_status,
//     notification, library_changed, knowledge_index_progress, browser_*) are
//     not conversation state; browser frames travel on their own socket.
//   - Whole-replay frames (replay_message and friends) are a state
//     reconstruction, not a stream of events.
func withFrameSeq(frame any, seq uint64) (any, bool) {
	n := int64(seq)
	switch f := frame.(type) {
	case generated.TokenFrame:
		f.Seq = &n
		return f, true
	case generated.DoneFrame:
		f.Seq = &n
		return f, true
	case generated.ToolCallStartFrame:
		f.Seq = &n
		return f, true
	case generated.ToolCallResultFrame:
		f.Seq = &n
		return f, true
	case generated.ToolResultProjectionFrame:
		f.Seq = &n
		return f, true
	case generated.SubagentStartFrame:
		f.Seq = &n
		return f, true
	case generated.SubagentEndFrame:
		f.Seq = &n
		return f, true
	case generated.ErrorFrame:
		f.Seq = &n
		return f, true
	case generated.MediaFrame:
		f.Seq = &n
		return f, true
	case generated.CancelStageFrame:
		f.Seq = &n
		return f, true
	case generated.AskUserQuestionFrame:
		f.Seq = &n
		return f, true
	case generated.GoalStatusFrame:
		f.Seq = &n
		return f, true
	case generated.GoalOutcomeFrame:
		f.Seq = &n
		return f, true
	case generated.JudgeVerdictFrame:
		f.Seq = &n
		return f, true
	case generated.ReplayErrorFrame:
		f.Seq = &n
		return f, true
	case generated.MessageStatusFrame:
		f.Seq = &n
		return f, true
	default:
		return frame, false
	}
}

// frameSessionID reports the session a session-scoped frame belongs to.
// AskUserQuestionFrame carries it on its embedded card, matching the SPA's own
// routing rule (src/store/chat/slices/frames.ts reads card.session_id). Frames
// with no session (connection-scoped or global) report false and are not
// numbered.
func frameSessionID(frame any) (string, bool) {
	switch f := frame.(type) {
	case generated.TokenFrame:
		return f.SessionId, f.SessionId != ""
	case generated.DoneFrame:
		return f.SessionId, f.SessionId != ""
	case generated.ToolCallStartFrame:
		return f.SessionId, f.SessionId != ""
	case generated.ToolCallResultFrame:
		return f.SessionId, f.SessionId != ""
	case generated.ToolResultProjectionFrame:
		return f.SessionId, f.SessionId != ""
	case generated.SubagentStartFrame:
		return f.SessionId, f.SessionId != ""
	case generated.SubagentEndFrame:
		return f.SessionId, f.SessionId != ""
	case generated.ErrorFrame:
		if f.SessionId != nil && *f.SessionId != "" {
			return *f.SessionId, true
		}
		return "", false
	case generated.MediaFrame:
		return f.SessionId, f.SessionId != ""
	case generated.CancelStageFrame:
		return f.SessionId, f.SessionId != ""
	case generated.AskUserQuestionFrame:
		return f.Card.SessionId, f.Card.SessionId != ""
	case generated.GoalStatusFrame:
		return f.SessionId, f.SessionId != ""
	case generated.GoalOutcomeFrame:
		return f.SessionId, f.SessionId != ""
	case generated.JudgeVerdictFrame:
		if f.SessionId != nil && *f.SessionId != "" {
			return *f.SessionId, true
		}
		return "", false
	case generated.ReplayErrorFrame:
		return f.SessionId, f.SessionId != ""
	case generated.MessageStatusFrame:
		return f.SessionId, f.SessionId != ""
	default:
		return "", false
	}
}

// emitSessionFrame numbers a session-scoped frame, records it in the session's
// retained window, and enqueues it on wc.
//
// This is the single place a live session frame is numbered. Frames that carry
// no session (see withFrameSeq) fall through to the ordinary unnumbered send,
// so callers can route every frame through here without special-casing.
func (h *WSHandler) emitSessionFrame(wc *wsConn, frameType string, frame any) {
	sessionID, ok := h.numberableSession(frame)
	if !ok {
		sendConnGenFrame(wc, frameType, frame)
		return
	}

	h.mu.Lock()
	seq := h.assignSeqLocked(sessionID)
	numbered, _ := withFrameSeq(frame, seq)
	data, err := json.Marshal(numbered)
	if err != nil {
		h.mu.Unlock()
		slog.Error("ws: marshal session frame failed", "type", frameType, "session_id", sessionID, "error", err)
		return
	}
	// #823 review finding 9 (first half, "take the number and store the
	// frame atomically everywhere"): assign+store happen in this ONE locked
	// step for every numbering path in this file now, including sendDone
	// (see its own doc comment) — no path can spend a number without a
	// stored frame backing it, closing the storage-order half of finding 9
	// and, jointly, finding 1's core bug.
	h.recordSeqFrameLocked(sessionID, frameType, seq, data)
	h.mu.Unlock()

	// numbered=true: this is the single place a live session frame is
	// numbered (see doc comment above) — see #823 review finding 5 and
	// sendRawFrameBytes' numbered parameter doc.
	//
	// #823 review finding 9 (second half, wire DELIVERY order — NOT closed
	// by this pass, documented gap): assign+store above is atomic, but the
	// send below still happens after h.mu is released, same as before this
	// fix. Two concurrent producers for the same session (this connection's
	// forwarder here, and the shared token/done fan-out path in
	// wsStreamer.Update/sendDone, which numbers via numberSessionFrame) can
	// still have their sends reach the wire in the opposite order from the
	// numbers just assigned, if the goroutine that assigned the LOWER
	// number is descheduled before it reaches sendRawFrameBytes. A correct
	// fix needs a dedicated per-session send-ordering primitive that
	// doesn't tie up h.mu — the GLOBAL lock every session's numbering and
	// every connection's bind/attach path depends on — across a
	// sendRawFrameBytes call that can legitimately block up to 5s for a
	// critical frame (done/error) with a stalled receiver; holding h.mu
	// across that would trade a plausible, timing-dependent per-session
	// reordering for a confirmed, gateway-wide stall risk under exactly the
	// dead-connection conditions #823 is about. Scoped out of this pass —
	// see the squad report's honest-gaps section.
	sendRawFrameBytes(wc, frameType, data, true)
}

// numberableSession reports which session a frame belongs to and whether the
// frame participates in numbering, or ok=false when it must go out unnumbered.
//
// It probes withFrameSeq rather than trusting frameSessionID alone, so the two
// type switches cannot disagree in the one way that would be invisible and
// harmful: a frame recognised by frameSessionID but not stamped by withFrameSeq
// would consume a number it never carries, leaving a HOLE in the sequence — and
// a hole is exactly what a client reads as a lost frame. One probe costs a value
// copy and removes the failure mode entirely.
func (h *WSHandler) numberableSession(frame any) (string, bool) {
	if h == nil {
		return "", false
	}
	if _, ok := withFrameSeq(frame, 0); !ok {
		return "", false
	}
	return frameSessionID(frame)
}

// numberSessionFrame assigns and stamps a seq without sending, for fan-out
// paths that build one frame and deliver it to several connections. It returns
// the stamped frame and the assigned seq; every recipient must be sent the
// SAME bytes so all attached clients agree on the number.
//
// ok=false means the frame is not session-scoped — deliver it unnumbered.
func (h *WSHandler) numberSessionFrame(frameType string, frame any) (any, uint64, []byte, bool) {
	sessionID, ok := h.numberableSession(frame)
	if !ok {
		return frame, 0, nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	seq := h.assignSeqLocked(sessionID)
	numbered, _ := withFrameSeq(frame, seq)
	data, err := json.Marshal(numbered)
	if err != nil {
		slog.Error("ws: marshal session frame failed", "type", frameType, "session_id", sessionID, "error", err)
		return frame, 0, nil, false
	}
	h.recordSeqFrameLocked(sessionID, frameType, seq, data)
	return numbered, seq, data, true
}

// nextSessionSeq reserves the next sequence number for sessionID and records
// already-marshalled bytes against it. Used by the replay/attach path, which
// marshals through its own emitter rather than sendRawFrameBytes.
func (h *WSHandler) nextSessionSeq(sessionID, kind string, raw []byte) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	seq := h.assignSeqLocked(sessionID)
	h.recordSeqFrameLocked(sessionID, kind, seq, raw)
	return seq
}
