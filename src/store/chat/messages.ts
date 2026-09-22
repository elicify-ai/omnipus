// messages.ts: Message limits, setup content, tool-result guards, selectors, mutation, and event-time ordering

import type { ToolResultRef, TruncatedResult } from '@/lib/api/generated/asyncapi-types'
import { evictSpanIndexEntries } from './types'
import type { ChatMessage, ClientTruncatedResult, SessionChatState } from './types'

// Maximum messages kept in the visible ring buffer per session.
// Older messages are evicted once this limit is exceeded; full transcript is preserved server-side.
export const MAX_MESSAGES_PER_SESSION = 500

// Kickoff content template for sendWorkspaceSetupKickoff — sent as
// the (synthetic, never user-visible) first turn of a freshly-created
// workspace. The backend records the message this content rides on as a
// SYSTEM-role transcript entry (centered pill on replay), not a user
// message, so this text is never shown verbatim in the UI — it only
// instructs Ava what to do; her own reply is what the user actually reads.
// Exported (module-level constant fn) so tests can assert on the exact
// content without duplicating the template.
export const buildWorkspaceSetupKickoffContent = (workspaceName: string): string =>
  `The workspace "${workspaceName}" was just created. Introduce yourself and interview the user about this workspace's purpose so you can determine which agents and skills its team needs, then set up the team.`

// Maximum byte size of a tool result stored in client state; oversized results become a sentinel.
const MAX_TOOL_RESULT_BYTES = 50_000

// Preview size for client-side truncated results (4 KiB).
const CLIENT_TRUNCATION_PREVIEW_BYTES = 4_096

/** Returns true when the value is the client-side truncation sentinel. */
export function isClientTruncatedResult(value: unknown): value is ClientTruncatedResult {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as Record<string, unknown>)['_truncated_client'] === true
  )
}

/** Returns true when the value is the server-side ToolResultRef sentinel. */
export function isToolResultRef(value: unknown): value is ToolResultRef {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as Record<string, unknown>)['_ref'] === true &&
    typeof (value as Record<string, unknown>)['ref'] === 'string'
  )
}

/** Returns true when the value is the server-side TruncatedResult sentinel. */
export function isTruncatedResult(value: unknown): value is TruncatedResult {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as Record<string, unknown>)['_truncated'] === true
  )
}

/** Clamp a tool result to MAX_TOOL_RESULT_BYTES; pass-through for existing sentinels. */
export function clampToolResult(result: unknown): unknown {
  // Pass-through for existing sentinels.
  if (isToolResultRef(result) || isTruncatedResult(result) || isClientTruncatedResult(result)) {
    return result
  }
  let serialized: string
  try {
    serialized = typeof result === 'string' ? result : JSON.stringify(result)
  } catch {
    serialized = String(result)
  }
  if (serialized.length <= MAX_TOOL_RESULT_BYTES) {
    return result
  }
  const preview = serialized.slice(0, CLIENT_TRUNCATION_PREVIEW_BYTES)
  const originalSizeBytes = new TextEncoder().encode(serialized).length
  const sentinel: ClientTruncatedResult = {
    _truncated_client: true,
    original_size_bytes: originalSizeBytes,
    preview,
  }
  return sentinel
}

/** Return the ordered message array for a bucket. O(N) — call once per reducer, not per frame. */
export function getMessages(bucket: Pick<SessionChatState, 'messagesById' | 'messageOrder'>): ChatMessage[] {
  return bucket.messageOrder.map((id) => bucket.messagesById[id]).filter(Boolean)
}

/**
 * Backward-scan a bucket's message order for the id of the most recent
 * assistant message, returning null when none exists. This is the single
 * shared implementation of a "find last assistant message" scan that used
 * to be hand-rolled at every WS-frame-handler / cancel / span call site in
 * this store. Takes the two order/lookup fields directly (rather than a
 * bucket object) so it accepts a plain SessionChatState's fields as well as
 * an Immer produce() draft's fields without an assignability question.
 */
export function findLastAssistantMessageId(
  order: readonly string[],
  messagesById: Record<string, ChatMessage>,
): string | null {
  for (let i = order.length - 1; i >= 0; i--) {
    const id = order[i]
    if (messagesById[id]?.role === 'assistant') return id
  }
  return null
}

/**
 * ADR-070 §2.1/§2.7 — the assistant bubble still eligible to receive more
 * live content or be mutated as "the current turn's reply": the raw tail of
 * `order` if it's an assistant message (the common case), or — when
 * something else (a mid-turn steer's user message) has landed after it —
 * the last assistant message ONLY if it is still genuinely open
 * (`isStreaming`). Returns null when no bubble is eligible; the caller must
 * open a new one (or, for read-only consumers, treat "no eligible bubble
 * yet" as its own state rather than falling back to a closed one).
 *
 * `findLastAssistantMessageId`'s bare backward scan is intentionally NOT
 * replaced everywhere — several callers (the C8 error sweep's `lastMsgId`,
 * `findAssistantMessageIdByTurnId`'s sibling use, `done`/`error` sweeps that
 * must finalize EVERY still-streaming bubble regardless of position) have a
 * genuinely different job than "can I still write here," and stay on the
 * bare scan. This helper is for the specific "is this bubble still open"
 * question — see ADR-070 §2.1 (token/tool_call_start already implement this
 * rule inline; subagent_start/media/markLastMessageInterrupted/
 * lastAssistantMessageId are routed through this shared helper instead of
 * re-deriving it ad hoc).
 */
export function findOpenAssistantMessageId(
  order: readonly string[],
  messagesById: Record<string, ChatMessage>,
): string | null {
  const tailId = order[order.length - 1]
  const tail = tailId ? messagesById[tailId] : undefined
  if (tail?.role === 'assistant') return tailId ?? null
  const candidateId = findLastAssistantMessageId(order, messagesById)
  const candidate = candidateId ? messagesById[candidateId] : undefined
  return candidate?.isStreaming ? candidateId : null
}

/**
 * Find the id of the assistant message carrying the given turnId — used to
 * correlate a replayed `turn_canceled` entry (Fix 5c) to the specific
 * assistant message it interrupted. Deliberately independent of
 * findLastAssistantMessageId: async delegation can interleave other agents'/
 * turns' frames between an assistant entry and its later cancellation, so
 * "last assistant message" is not a safe proxy here — only an exact turnId
 * match is.
 */
export function findAssistantMessageIdByTurnId(
  order: readonly string[],
  messagesById: Record<string, ChatMessage>,
  turnId: string,
): string | null {
  for (let i = order.length - 1; i >= 0; i--) {
    const id = order[i]
    const m = messagesById[id]
    if (m?.role === 'assistant' && m.turnId === turnId) return id
  }
  return null
}

/** Test helper: build ring-buffer fields for a SessionChatState from a plain ChatMessage array. */
export function makeBucketMessages(msgs: ChatMessage[]): Pick<SessionChatState, 'messagesById' | 'messageOrder' | 'trimmedCount'> {
  const messagesById: Record<string, ChatMessage> = {}
  const messageOrder: string[] = []
  for (const m of msgs) { messagesById[m.id] = m; messageOrder.push(m.id) }
  return { messagesById, messageOrder, trimmedCount: 0 }
}

/**
 * Evict one message from a bucket, purging all dependent maps.
 *
 * Removes the message from messagesById/messageOrder, evicts its tool calls
 * from toolCalls/toolCallOrder/textAtToolCallStart, and removes any
 * spanBySpanId entries whose messageId matches the evicted message.
 */
export function evictMessageFromBucket(
  bucket: SessionChatState,
  messageId: string,
): void {
  // Remove from order and map.
  const orderIdx = bucket.messageOrder.indexOf(messageId)
  if (orderIdx !== -1) bucket.messageOrder.splice(orderIdx, 1)
  const msg = bucket.messagesById[messageId]
  delete bucket.messagesById[messageId]

  // Evict tool calls owned by this message.
  const evictedCallIds = new Set((msg?.tool_calls ?? []).map((tc) => tc.id))
  if (evictedCallIds.size > 0) {
    for (const id of evictedCallIds) {
      delete bucket.toolCalls[id]
      delete bucket.textAtToolCallStart[id]
      if (bucket.toolCallOwnerMessageId) delete bucket.toolCallOwnerMessageId[id]
    }
    bucket.toolCallOrder = bucket.toolCallOrder.filter((id) => !evictedCallIds.has(id))
  }

  // Evict the span index via the shared helper (see evictSpanIndexEntries'
  // doc comment).
  bucket.spanBySpanId = evictSpanIndexEntries(bucket.spanBySpanId, new Set([messageId]))
}

/** Captures an ISO-8601 timestamp's head, its fractional-seconds digits, and any trailing zone designator. */
const ISO_FRACTIONAL_SECONDS = /^(.*T\d{2}:\d{2}:\d{2})\.(\d+)(.*)$/

/**
 * Splits an ISO-8601 timestamp into its whole-second-plus-milliseconds part and
 * the sub-millisecond remainder, so two timestamps can be ordered CHRONOLOGICALLY.
 *
 * Why this is not a plain string compare, and not a plain `Date.parse` either:
 *
 * The gateway writes these timestamps with Go's `time.RFC3339Nano`
 * (pkg/gateway/replay.go, `buildReplayErrorFrame`), which STRIPS trailing zeros
 * from the fractional seconds. Two chronologically ordered instants can therefore
 * arrive with different fractional widths:
 *
 *   "2026-09-12T10:00:00.5Z"        (earlier)
 *   "2026-09-12T10:00:00.5000001Z"  (later)
 *
 *   - A string compare gets this BACKWARDS: at the first differing character it
 *     compares '0' (0x30) against 'Z' (0x5A), so the later timestamp sorts BELOW
 *     the earlier one and the cursor refuses to advance past it.
 *   - `Date.parse` alone cannot separate them either: JS `Date` has only
 *     millisecond resolution, so both collapse to the same epoch value and the
 *     `>` test is false — the cursor again fails to advance.
 *
 * Either way the cursor is left behind the newest entry the SPA has actually
 * seen, the next reconnect sends a `since` that is too early, and the server
 * (whose `applySinceCursor` compares real parsed instants with `.After()`)
 * correctly replays entries the SPA already has — duplicate bubbles.
 *
 * So: take the first three fractional digits as milliseconds and hand those to
 * `Date.parse` explicitly (never relying on how a given engine truncates or
 * rounds the rest), and keep the remaining digits as an integer tiebreaker
 * normalised to a FIXED six-digit width so they compare as plain numbers. Go
 * emits at most nine fractional digits, so three + six covers the whole wire
 * range with nothing to truncate.
 *
 * Returns null when the value is not a parseable timestamp.
 */
function parseEventTime(value: string): { ms: number; subMs: number } | null {
  const match = ISO_FRACTIONAL_SECONDS.exec(value)
  if (!match) {
    const whole = Date.parse(value)
    return Number.isNaN(whole) ? null : { ms: whole, subMs: 0 }
  }
  const [, head, digits, tail] = match
  const ms = Date.parse(`${head}.${digits.slice(0, 3).padEnd(3, '0')}${tail}`)
  if (Number.isNaN(ms)) return null
  return { ms, subMs: Number(digits.slice(3, 9).padEnd(6, '0')) }
}

/**
 * Advance lastReceivedEventTime if `incoming` is chronologically newer than
 * `current`. Monotonic: it never moves the cursor backwards.
 *
 * Exported for direct unit testing — see chat.replay-cursor.test.ts.
 */
export function advanceEventTime(current: string | null, incoming: string | null | undefined): string | null {
  if (!incoming) return current
  if (!current) return incoming
  const inc = parseEventTime(incoming)
  // An unparseable incoming value must never move the cursor: erring towards a
  // stale cursor costs a duplicate replay, erring forwards would lose messages.
  if (!inc) return current
  const cur = parseEventTime(current)
  // A cursor we can no longer parse is useless — the server rejects it and falls
  // back to a full replay — so a parseable incoming value is strictly better.
  if (!cur) return incoming
  if (inc.ms !== cur.ms) return inc.ms > cur.ms ? incoming : current
  return inc.subMs > cur.subMs ? incoming : current
}
