// sequence.ts: #823 phase 2 — apply-by-sequence for reconnect catch-up.
//
// The gateway numbers every conversation frame it emits per session, strictly
// increasing and gap-free (pkg/gateway/ws_sequence.go). The SPA keeps the
// highest number it has applied per session and:
//
//   * ignores any numbered frame at or below that number, which is what makes
//     a byte-exact catch-up redelivery idempotent, and
//   * sends the number back as `attach_session.since_seq`, so the gateway
//     re-delivers exactly the frames that were missed.
//
// Everything here is a pure read/decide/derive helper so `handleFrame` — already
// at its grandfathered line budget (scripts/budgets/functions.txt) — gains one
// call site rather than a block. `gateFrameBySequence` is the only stateful one:
// it advances the cursor it just validated.

import { emptySessionState } from '../session'
import { discardBufferedFramesForSession } from '../routing'
import type { ChatMessage, SessionChatState } from '../types'
import type { SessionSnapshotFrame } from '@/lib/api/generated/asyncapi-types'

/**
 * User messages this client sent but has not yet had acknowledged by the
 * server (`deliveryStatus: 'sending'` — set by `buildQueuedUserMessage` at
 * send time, cleared by the first `message_status` frame). Finding 3
 * (SQUAD-BRIEF-AY): a message drained onto the WS the instant it reconnects
 * (OmnipusRuntimeProvider.tsx's onConnected) races independently against
 * that same reconnect's `attach_session` — the gateway computes the
 * snapshot/replay decision from state that predates the drained message, so
 * neither the snapshot nor the replay that follows it will ever carry it
 * forward. In arrival order, matching how they render today.
 */
function unacknowledgedUserMessages(bucket: SessionChatState): ChatMessage[] {
  const out: ChatMessage[] = []
  for (const id of bucket.messageOrder) {
    const m = bucket.messagesById[id]
    if (m?.role === 'user' && m.deliveryStatus === 'sending') out.push(m)
  }
  return out
}

/** The frame type that tells a client to REPLACE one session's state. */
export const SESSION_SNAPSHOT_FRAME_TYPE = 'session_snapshot'

/**
 * The frame's sequence number, or null when it carries none.
 *
 * Frames without a number are applied exactly as before and leave the cursor
 * untouched. The gateway deliberately leaves 19 frame kinds unnumbered —
 * whole-replay frames (`replay_message` and friends), `session_state`, the
 * replay terminator, the catch-up token, and connection-scoped/global frames
 * (pong, plan_status, notification, browser_*). See `withFrameSeq` in
 * pkg/gateway/ws_sequence.go for the authoritative list and the reason each is
 * excluded; the client does not need to know which is which, only whether a
 * number is present.
 */
export function readFrameSeq(frame: unknown): number | null {
  const seq = (frame as { seq?: unknown }).seq
  return typeof seq === 'number' && Number.isFinite(seq) ? seq : null
}

/**
 * A bucket's applied-frame cursor, or null when it has no position.
 *
 * A stored 0 is normalized to null here — finding 16 (SQUAD-BRIEF-AY): the
 * gateway's own TokenFrame.seq contract states "0 means the session has no
 * numbered event yet" (contracts/components/schemas/TokenFrame.yaml), and a
 * `session_snapshot` can legitimately set the cursor to exactly 0 (e.g.
 * `reason: 'unknown_position'`). Every attach site already treats 0 as "no
 * position" (OmnipusRuntimeProvider.tsx only sends `since_seq` when
 * `appliedSeq > 0`) — this is the single read point every other consumer
 * (prepareSessionForReplay, replayStartPatch, gateFrameBySequence's cursor
 * read, getLastAppliedSeq) goes through, so normalizing here makes all of
 * them agree instead of only the attach knowing the rule. Without it,
 * `readAppliedSeq(...) !== null`-style checks treated 0 as a REAL position —
 * keeping a session's local transcript instead of wiping it for the full
 * replay the gateway is about to send (since `since_seq` was correctly
 * omitted), leaving stale content on screen next to duplicated fresh history.
 */
export function readAppliedSeq(bucket: SessionChatState | undefined): number | null {
  const seq = bucket?.lastAppliedSeq
  return typeof seq === 'number' && seq !== 0 ? seq : null
}

/**
 * True when the frame is a `session_snapshot` — the one frame the ignore rule
 * below must never swallow.
 *
 * A snapshot with `reason: 'cursor_ahead'` is emitted precisely BECAUSE the
 * client's position is at or beyond the gateway's new highest number, so its
 * `seq` is LOWER than the client's cursor by definition. Applying the ordinary
 * `seq <= cursor → ignore` rule to it would discard the instruction to rebuild
 * state, and the full replay that follows would then be merged onto a stale,
 * no-longer-valid transcript — duplicates, or a history belonging to a gateway
 * generation that no longer exists. The snapshot is therefore exempt: it always
 * applies, and it always sets the cursor to its own `seq`.
 */
export function isSessionSnapshotFrame(frame: unknown): boolean {
  return (frame as { type?: string }).type === SESSION_SNAPSHOT_FRAME_TYPE
}

/**
 * Apply-by-sequence gate for one inbound frame.
 *
 * Returns true when the frame must be dispatched, false when it must be dropped
 * as already-applied. As a side effect it advances `targetSid`'s cursor for
 * every numbered frame it passes — deliberately BEFORE the reducer runs, so a
 * frame the reducer then treats as redundant (a token for an already-errored
 * bubble, say) still counts as seen and is never re-applied on the next
 * catch-up.
 *
 * Three exemptions, each with its own reason:
 *   * no `targetSid` — the frame is unrouted (dropped or misrouted as before);
 *     there is no session whose position could mean anything;
 *   * no `seq` — unnumbered frames apply as today (see `readFrameSeq`);
 *   * `session_snapshot` — see `isSessionSnapshotFrame`; it owns both its
 *     cursor and the state replacement.
 */
export function gateFrameBySequence(
  frame: unknown,
  targetSid: string | null,
  getSessionsById: () => Record<string, SessionChatState>,
  withBucket: (sid: string, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void,
): boolean {
  if (!targetSid) return true
  const seq = readFrameSeq(frame)
  if (seq === null || isSessionSnapshotFrame(frame)) return true
  const cursor = readAppliedSeq(getSessionsById()[targetSid])
  if (cursor !== null && seq <= cursor) return false
  withBucket(targetSid, () => ({ lastAppliedSeq: seq }))
  return true
}

/**
 * Replace `bucket`'s state from a `session_snapshot` frame: a full wipe, NOT a
 * merge — the snapshot exists because the gateway cannot serve the range the
 * client asked for, so the local transcript is not a valid prefix of anything.
 * The full replay that follows the snapshot rebuilds the history; the
 * replay-terminating `done` ends it, exactly as on a first load.
 *
 * Several fields are deliberately carried across the wipe rather than reset
 * by `emptySessionState()`:
 *
 *   * `isReplaying` — the attach that triggered this snapshot already armed the
 *     replay window, and clearing it here would re-enable the composer in the
 *     middle of the replay, letting the user send a message into a turn whose
 *     transcript has not landed yet. The replay terminator clears it as usual.
 *   * `pendingAsk`, `activeTurnId`/`activeTurnAgentId`/`activeTurnBubbleOpened`,
 *     `isStreaming`, `goalStatus`, `loopStatus` — finding 2 (SQUAD-BRIEF-AY,
 *     REVIEW-OPUS-823): on reconnect the gateway sends `session_state` (an
 *     AskUserQuestion card still pending, a turn already running) BEFORE the
 *     snapshot (pkg/gateway/replay.go:543-552 — pending question cards are
 *     delivered only through session_state, never restated by the snapshot
 *     itself or the replay that follows it). Wiping to `emptySessionState()`
 *     here discarded exactly the state that frame just delivered for THIS
 *     reconnect: a user waiting on a question saw the card vanish, the Stop
 *     control disappeared, and the composer unlocked mid-turn, with no way
 *     to answer until a full page reload. None of these are part of the
 *     TRANSCRIPT the snapshot is correcting — they are out-of-band session
 *     status the snapshot's own frame says nothing about, so there is
 *     nothing for the snapshot to "correct" them to.
 *   * nothing else. `lastReceivedEventTime` (the legacy timestamp cursor) IS
 *     dropped: the session now has a sequence position, which supersedes it,
 *     and a timestamp the discarded state advanced is not a position the
 *     rebuilt transcript can be measured against.
 *
 * The message list is also not a blind wipe-to-empty: any user message still
 * awaiting server acknowledgement (finding 3, `unacknowledgedUserMessages`)
 * is carried forward, since nothing else will ever re-deliver it — see that
 * helper's doc comment. Every OTHER message (already-acknowledged history,
 * assistant bubbles, tool calls) is genuinely stale relative to a snapshot
 * and is dropped as before; the replay that follows rebuilds it.
 */
export function applySessionSnapshot(
  bucket: SessionChatState,
  frame: SessionSnapshotFrame,
): Partial<SessionChatState> {
  // Buffered orphan frames (tool calls waiting for a subagent_start) belong to
  // the state being replaced — nothing in the rebuilt history can adopt them.
  discardBufferedFramesForSession(frame.session_id)
  const carried = unacknowledgedUserMessages(bucket)
  const carriedById: Record<string, ChatMessage> = {}
  for (const m of carried) carriedById[m.id] = m
  return {
    ...emptySessionState(),
    lastAppliedSeq: frame.seq,
    isReplaying: bucket.isReplaying,
    pendingAsk: bucket.pendingAsk,
    activeTurnId: bucket.activeTurnId,
    activeTurnAgentId: bucket.activeTurnAgentId,
    // activeTurnBubbleOpened deliberately NOT carried over: it tracks
    // whether a placeholder bubble already exists in messageOrder, and the
    // wipe just emptied messageOrder — false (emptySessionState()'s
    // default) is the only value consistent with the post-wipe transcript.
    // The next token/replay-terminator-done reopens it exactly as on a
    // first load.
    isStreaming: bucket.isStreaming,
    goalStatus: bucket.goalStatus,
    loopStatus: bucket.loopStatus,
    messagesById: carriedById,
    messageOrder: carried.map((m) => m.id),
  }
}