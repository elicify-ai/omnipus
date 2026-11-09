// stop-checkpoint.ts: #1534 — what the screen showed when Stop was pressed,
// and the post-Stop token guards (arch-decision.md, sections 1 and 6).
//
// The browser tells the server, inside the cancel frame, how much of the
// stopped answer it had applied to its store: the last wire message_id it
// applied to the frozen bubble, that token's turn_id, and the Unicode code
// points of token content applied for that message_id. The server saves the
// stopped answer only up to that point (founder 2026-10-31, Q3 = A).
//
// Counting happens where a token is APPLIED to a bubble (frames.ts token
// case), never at socket receipt: frames still waiting in the socket's
// animation-frame batch were never on screen. A `replace: true` token resets
// the count for its message_id; ordinary tokens add. The "\n\n" the client
// inserts between two wire messages of one bubble is not wire text and is
// never counted.

import type { CancelFrame, TokenFrame } from '@/lib/api/generated/asyncapi-types'
import { pendingCancelAckSids } from './runtime-state'
import type { ChatMessage } from './types'

export type CancelShown = NonNullable<CancelFrame['shown']>

interface AppliedRecord {
  /** Last wire message_id whose token was applied to this bubble. */
  messageId: string
  /** turn_id carried by that token, when it had one. */
  turnId?: string
  /** Applied code points per wire message_id of this bubble. */
  codePoints: Map<string, number>
}

// Per session, per bubble id. Only the most recent bubbles of a session can
// still be streaming, so older records are evicted to keep this bounded.
const MAX_BUBBLES_PER_SESSION = 8
const appliedBySession = new Map<string, Map<string, AppliedRecord>>()

function countCodePoints(text: string): number {
  // A surrogate pair is one code point; a lone surrogate counts as one, the
  // same as string iteration ([...text].length) without the allocation.
  let n = 0
  for (let i = 0; i < text.length; i++) {
    const unit = text.charCodeAt(i)
    if (unit >= 0xd800 && unit <= 0xdbff && i + 1 < text.length) {
      const next = text.charCodeAt(i + 1)
      if (next >= 0xdc00 && next <= 0xdfff) i++
    }
    n++
  }
  return n
}

/** Record a token frame (with a message_id) that was just applied to `bubbleId`. */
export function recordAppliedToken(sid: string, bubbleId: string, frame: TokenFrame): void {
  if (!frame.message_id) return
  let bubbles = appliedBySession.get(sid)
  if (!bubbles) {
    bubbles = new Map()
    appliedBySession.set(sid, bubbles)
  }
  const record = bubbles.get(bubbleId) ?? { messageId: frame.message_id, codePoints: new Map<string, number>() }
  bubbles.delete(bubbleId) // re-insert as most recent
  bubbles.set(bubbleId, record)
  const added = countCodePoints(frame.content)
  const before = frame.replace ? 0 : (record.codePoints.get(frame.message_id) ?? 0)
  record.codePoints.set(frame.message_id, before + added)
  record.messageId = frame.message_id
  record.turnId = frame.turn_id ?? record.turnId
  if (bubbles.size > MAX_BUBBLES_PER_SESSION) {
    const oldest = bubbles.keys().next().value
    if (oldest !== undefined) bubbles.delete(oldest)
  }
}

/** Forget every applied-token record of a session (tests and session teardown). */
export function forgetAppliedTokens(sid?: string): void {
  if (sid === undefined) appliedBySession.clear()
  else appliedBySession.delete(sid)
}

/**
 * The checkpoint for a bubble that is being frozen by Stop, read in the same
 * synchronous step as the freeze.
 *
 * - Tokens with a message_id were applied: that id, its token turn and count.
 * - Nothing applied and no text (an empty placeholder): `{code_points: 0}`,
 *   plus the turn when it is known. Never a local placeholder id.
 * - Text that came only from tokens without a message_id: undefined (the
 *   server falls back to cutting at the moment it receives Stop).
 *
 * `bubble` is undefined when the freeze had to create a new placeholder.
 */
export function shownCheckpointFor(sid: string, bubble: ChatMessage | undefined, knownTurnId: string | null | undefined): CancelShown | undefined {
  const record = bubble ? appliedBySession.get(sid)?.get(bubble.id) : undefined
  if (record) {
    const turnId = record.turnId ?? bubble?.turnId
    return {
      message_id: record.messageId,
      ...(turnId ? { turn_id: turnId } : {}),
      code_points: record.codePoints.get(record.messageId) ?? 0,
    }
  }
  if (bubble?.content) return undefined
  const turnId = bubble?.turnId ?? knownTurnId ?? undefined
  return { ...(turnId ? { turn_id: turnId } : {}), code_points: 0 }
}

// ── Post-Stop token guards (arch-decision.md section 6) ─────────────────────
//
// A Stop that froze an answer arms a guard for its session. While the cancel
// still awaits its terminal frame (pendingCancelAckSids), tokens the server
// sent before it processed the Stop must not reopen the answer:
//   - frozen answer with a known turn: discard every token of that turn, even
//     one under a new message_id (the server started the next step);
//   - frozen empty placeholder: discard every token for the session (no
//     token has named the turn yet, so the first real tokens are this turn's).
// The guard is released with the pending cancel acknowledgement.

type StopGuard = { kind: 'placeholder' } | { kind: 'turn'; turnId: string }

const stopGuards = new Map<string, StopGuard>()

/** Arm the guard for the bubble a Stop just froze (or clear it when there is nothing to guard). */
export function armStopGuard(sid: string, shown: CancelShown | undefined, frozen: ChatMessage | undefined): void {
  const placeholder = shown !== undefined && shown.message_id === undefined && !frozen?.content
  if (placeholder) {
    stopGuards.set(sid, { kind: 'placeholder' })
    return
  }
  const turnId = shown?.turn_id ?? frozen?.turnId
  if (turnId) stopGuards.set(sid, { kind: 'turn', turnId })
  else stopGuards.delete(sid)
}

/** A terminal frame acknowledged this session's cancel: the Stop is settled. */
export function settleCancelAck(sid: string): void {
  pendingCancelAckSids.delete(sid)
  stopGuards.delete(sid)
}

/** A socket drop: no terminal frame is coming for any outstanding cancel. */
export function settleAllCancelAcks(): void {
  pendingCancelAckSids.clear()
  stopGuards.clear()
}

/** True when this token belongs to an answer the user already stopped. */
export function isTokenAfterStop(sid: string, frame: TokenFrame): boolean {
  if (!pendingCancelAckSids.has(sid)) return false
  const guard = stopGuards.get(sid)
  if (!guard) return false
  if (guard.kind === 'placeholder') return true
  return frame.turn_id === guard.turnId
}
