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
import type { SessionChatState } from '../types'
import type { SessionSnapshotFrame } from '@/lib/api/generated/asyncapi-types'

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

/** A bucket's applied-frame cursor, or null when it has no position. */
export function readAppliedSeq(bucket: SessionChatState | undefined): number | null {
  const seq = bucket?.lastAppliedSeq
  return typeof seq === 'number' ? seq : null
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
 * Two fields are deliberately carried across the wipe:
 *
 *   * `isReplaying` — the attach that triggered this snapshot already armed the
 *     replay window, and clearing it here would re-enable the composer in the
 *     middle of the replay, letting the user send a message into a turn whose
 *     transcript has not landed yet. The replay terminator clears it as usual.
 *   * nothing else. `lastReceivedEventTime` (the legacy timestamp cursor) IS
 *     dropped: the session now has a sequence position, which supersedes it,
 *     and a timestamp the discarded state advanced is not a position the
 *     rebuilt transcript can be measured against.
 */
export function applySessionSnapshot(
  bucket: SessionChatState,
  frame: SessionSnapshotFrame,
): Partial<SessionChatState> {
  // Buffered orphan frames (tool calls waiting for a subagent_start) belong to
  // the state being replaced — nothing in the rebuilt history can adopt them.
  discardBufferedFramesForSession(frame.session_id)
  return {
    ...emptySessionState(),
    lastAppliedSeq: frame.seq,
    isReplaying: bucket.isReplaying,
  }
}