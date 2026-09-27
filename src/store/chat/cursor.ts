// cursor.ts: #823 catch-up redesign (BE-DESIGN.md §6.1/§6.2) — the SPA half
// of the gateway hub's per-session sequence contract. Two pure, dependency-
// light pieces:
//
//   - gateFrameBySeq: the idempotent, gap-detecting "apply rule" (§6.2's
//     table). Called once per inbound frame, BEFORE the frame reaches
//     handleFrame's per-type switch (slices/frames.ts) — so every frame
//     type that starts carrying a `seq` (§1.2's full list: token, done,
//     tool_call_start/result, subagent_start/end, media, error,
//     agent_switched, rate_limit, cancel_stage, message_status, goal_status,
//     loop_status, goal_outcome, judge_verdict, user_message) is covered by
//     ONE gate rather than one per case. A frame with no `seq` (every frame
//     this store handled before Lane A's gateway hub lands, and every frame
//     the design deliberately keeps unsequenced — §1.2's "Not sequenced"
//     list) is a no-op pass-through: this makes the gate purely additive
//     against every existing (pre-hub) test and live-gateway behavior.
//
//   - applySnapshotHistoryWipe: the §6.2 rule for the new `session_snapshot`
//     frame — wipe HISTORY only, preserving the pending tail (unsent/
//     unacknowledged user messages, §4.7) and every out-of-band field
//     (pendingAsk, goalStatus/goalPills, loopStatus, activeTurnId/
//     activeTurnAgentId, cancelStage, rateLimitEvent, session totals). This
//     replaces attempt 1's `resetChatBucketForReplay` full wipe on the
//     attach path (§6.1: "no longer wipe the bucket ... Only
//     session_snapshot wipes").
//
// PROVISIONAL: both functions are exercised here against hand-derived frame
// sequences (cursor.test.ts) matching BE-DESIGN.md §6.2's table and §6.4's
// exact wire examples, not against Lane A's real gateway output — Lane A's
// hub (pkg/gateway/ws_session_hub.go) had not landed on this branch's base
// at the time this was written. See SQUAD-REPORT-BEC.md's gaps section.

import { emptySessionState } from './session'
import type { ChatMessage, SessionChatState, SessionCursor } from './types'

/** The subset of a WS frame's own fields the apply rule needs — every
 * sequenced frame type carries these two (seq required when present at all;
 * boot_id optional, defaulting to whatever the cursor already has). */
export interface SeqFrameLike {
  seq?: number
  boot_id?: string
}

/**
 * Frame types that MINT the cursor directly from their own seq/boot_id
 * (`cursorFromTerminalFrame`, below) rather than being gated against it —
 * `session_snapshot`, `catch_up_complete`, and `session_started` (§3.4).
 * These are NOT "the next number after the cursor": `catch_up_complete{seq:
 * W}` in particular typically carries the SAME seq as the last regular
 * frame the connection already applied (W is the head at bind time, §4.1),
 * which `gateFrameBySeq`'s ordinary `seq <= cursor.seq` rule would
 * misclassify as an already-applied duplicate and silently DROP — never
 * reaching the handler that is supposed to clear `awaitingCatchUp`/
 * `isReplaying`. `applySeqGate` (slices/frames.ts) checks this set FIRST
 * and skips the gate entirely for these three types, exactly like an
 * unsequenced frame, letting their own case handler
 * (slices/catchup-frames.ts) mint the cursor unconditionally.
 */
export const CURSOR_MINTING_FRAME_TYPES: ReadonlySet<string> = new Set([
  'session_snapshot',
  'catch_up_complete',
  'session_started',
])

export type CursorGateDecision =
  | { kind: 'drop' }
  | { kind: 'apply'; cursor: SessionCursor | null }
  | { kind: 'gap'; cursor: SessionCursor }

/**
 * BE-DESIGN.md §6.2's apply-rule table, verbatim:
 *
 *   | cursor != null && seq <= cursor.seq        | drop (already applied) |
 *   | cursor == null || seq == cursor.seq + 1    | advance, then apply    |
 *   | seq > cursor.seq + 1                       | gap: re-attach         |
 *   | no seq                                     | apply as today, cursor untouched |
 *
 * Returns a decision, never mutates — the caller (slices/frames.ts) applies
 * `decision.cursor` via `withBucket` and decides what "re-attach" means
 * (sending `attach_session{since_seq, boot_id}` on the live connection).
 */
export function gateFrameBySeq(cursor: SessionCursor | null, frame: SeqFrameLike): CursorGateDecision {
  if (frame.seq === undefined || frame.seq === null) {
    return { kind: 'apply', cursor }
  }
  const bootId = frame.boot_id ?? cursor?.bootId ?? ''
  if (cursor === null) {
    return { kind: 'apply', cursor: { bootId, seq: frame.seq } }
  }
  if (frame.seq <= cursor.seq) {
    return { kind: 'drop' }
  }
  if (frame.seq === cursor.seq + 1) {
    return { kind: 'apply', cursor: { bootId, seq: frame.seq } }
  }
  return { kind: 'gap', cursor }
}

/**
 * Mint a cursor directly from a terminal frame's OWN seq/boot_id — used for
 * `session_started`, `session_snapshot`, and `catch_up_complete`, which per
 * §3.4/§6.1 always carry the authoritative post-attach position rather than
 * being gated like an ordinary sequenced frame (they aren't "the next
 * number after the cursor", they ARE the new cursor).
 */
export function cursorFromTerminalFrame(
  frame: { seq: number; boot_id?: string },
  priorBootId?: string | null,
): SessionCursor {
  return { bootId: frame.boot_id ?? priorBootId ?? '', seq: frame.seq }
}

// #823 catch-up redesign, Opus review round 2 item 2 (founder decision Q1):
// 'failed' is included alongside 'queued'/'sending' — a send that failed
// outright is still an unresolved user message the founder's Q1 rule covers
// ("pending/failed messages render at the end and move into history when
// the server echoes them"). Before this fix a failed send silently
// vanished on the very next snapshot rebuild, since isPendingTailMessage
// didn't recognize it and applySnapshotHistoryWipe drops everything it
// doesn't recognize as pending.
const PENDING_TAIL_STATUSES: ReadonlySet<ChatMessage['deliveryStatus']> = new Set(['queued', 'sending', 'failed'])

export function isPendingTailMessage(m: ChatMessage): boolean {
  return m.role === 'user' && !!m.deliveryStatus && PENDING_TAIL_STATUSES.has(m.deliveryStatus)
}

/**
 * Opus review round 2 item 2 (founder decision Q1 — "pending/failed messages
 * render at the END and move into history when the server echoes them"):
 * insert a freshly-reconstructed HISTORY entry (a `replay_message`, i.e.
 * something the server has already persisted and is now replaying) into
 * `draft.messageOrder`, always positioned BEFORE the pending tail rather
 * than blindly pushed to the true end of the array. A prior pass pushed
 * every replay_message straight onto messageOrder — since
 * applySnapshotHistoryWipe puts the surviving pending tail FIRST (kept in
 * place, nothing else in the bucket yet), every history entry that then
 * streamed in during the snapshot rebuild landed AFTER it, rendering the
 * pending/failed message ABOVE the history it was actually sent after.
 * Returns the index the entry was inserted at (mirrors `Array.prototype.
 * push`'s return-length convention loosely enough for callers that don't
 * need it — most just call this and move on).
 */
export function insertHistoryMessageId(
  messageOrder: string[],
  messagesById: Record<string, ChatMessage>,
  id: string,
): void {
  const firstPendingIdx = messageOrder.findIndex((existingId) => {
    const m = messagesById[existingId]
    return !!m && isPendingTailMessage(m)
  })
  if (firstPendingIdx === -1) {
    messageOrder.push(id)
  } else {
    messageOrder.splice(firstPendingIdx, 0, id)
  }
}

/**
 * §6.2's `session_snapshot` rule: wipe the bucket's HISTORY (messages, tool
 * calls, spans) but preserve:
 *   - the pending tail (§4.7 — messages not yet acknowledged by the server,
 *     `deliveryStatus` 'queued' or 'sending'), in their existing order;
 *   - every out-of-band field a `session_state` frame governs, since A6 of
 *     the attach algorithm (§4.1) sends `session_state` AFTER
 *     `session_snapshot` specifically so the wipe can never erase it
 *     (§4.6) — pendingAsk, goalStatus/goalPills, loopStatus, activeTurnId/
 *     activeTurnAgentId, cancelStage, rateLimitEvent, and the running
 *     session token/cost totals.
 *
 * Returns a full, fresh `SessionChatState` (not a partial patch) — the
 * caller (`replay-and-status-frames.ts`'s `session_snapshot` case) applies
 * it via `withBucket`, which shallow-merges a `Partial<SessionChatState>`
 * over the existing bucket; returning every field here (rather than a
 * partial) guarantees every OTHER piece of transient state (toolCalls,
 * spanBySpanId, textAtToolCallStart, …) is genuinely cleared rather
 * than left stale from before the wipe.
 */
export function applySnapshotHistoryWipe(bucket: SessionChatState): SessionChatState {
  // Capture, before the wipe below erases it, which turns had a still-open
  // assistant bubble — the one signal that survives a full replay
  // reconstruction (which always marks a rebuilt bubble 'done', even for a
  // turn genuinely cut short by e.g. a gateway crash mid-stream).
  const wipedOpenTurnIds = bucket.messageOrder
    .map((id) => bucket.messagesById[id])
    .filter((m): m is ChatMessage => !!m && m.role === 'assistant' && !!m.turnId && (m.isStreaming === true || m.status === 'streaming'))
    .map((m) => m.turnId as string)
  const pendingTail = bucket.messageOrder
    .map((id) => bucket.messagesById[id])
    .filter((m): m is ChatMessage => !!m && isPendingTailMessage(m))

  const messagesById: Record<string, ChatMessage> = {}
  const messageOrder: string[] = []
  for (const m of pendingTail) {
    messagesById[m.id] = m
    messageOrder.push(m.id)
  }

  return {
    ...emptySessionState(),
    messagesById,
    messageOrder,
    // Out-of-band state — never erased by a history wipe (§4.6).
    pendingAsk: bucket.pendingAsk,
    goalStatus: bucket.goalStatus,
    goalPills: bucket.goalPills,
    loopStatus: bucket.loopStatus,
    activeTurnId: bucket.activeTurnId,
    activeTurnAgentId: bucket.activeTurnAgentId,
    cancelStage: bucket.cancelStage,
    rateLimitEvent: bucket.rateLimitEvent,
    sessionTokens: bucket.sessionTokens,
    sessionCost: bucket.sessionCost,
    // ADR-092 (merged from release): the chat's resolved per-chat
    // Auto-approve state is session state, not history — never erased by a
    // history wipe (the session_state that follows re-asserts it anyway).
    autoApproveEffective: bucket.autoApproveEffective,
    // The cursor itself is set separately by the caller from the
    // session_snapshot frame's own seq/boot_id (cursorFromTerminalFrame) —
    // carried through here only as a safe default if the caller doesn't.
    cursor: bucket.cursor,
    wipedOpenTurnIds: wipedOpenTurnIds.length > 0 ? wipedOpenTurnIds : bucket.wipedOpenTurnIds,
    awaitingCatchUp: true,
    // Item 3 follow-up (orchestrator, Lane A confirmed the gateway side is
    // correct): the client sets isReplaying:true BEFORE session_snapshot
    // ever arrives (attachToSession). Without preserving it here, this
    // full-state reset silently dropped it back to false the instant the
    // snapshot landed — every replayed tool_call_start/tool_call_result
    // frame that follows then sees isReplaying:false and incorrectly sets
    // isStreaming:true (frames.ts's shouldMarkStreaming guard exists
    // specifically to suppress this during replay, but only works if
    // isReplaying genuinely stays true through the whole rebuild). Left the
    // Stop button/composer lock stuck on after opening any fully-completed,
    // never-live-streamed session (real-browser regression,
    // replay-fidelity.spec.ts (e)). catch_up_complete is still the only
    // frame that clears it back to false, at the genuine end of the rebuild.
    isReplaying: bucket.isReplaying,
  }
}
