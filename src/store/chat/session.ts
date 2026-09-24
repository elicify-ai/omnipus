// session.ts: Empty session construction and normalized message-array mutation

import { useUiStore } from '@/store/ui'
import type { ToolCall } from '@/lib/api'
import { logDiagnostic } from '@/lib/telemetry'
import { MAX_MESSAGES_PER_SESSION } from './messages'
import { evictSpanIndexEntries } from './types'
import type { ChatMessage, PositionedToolCall, SessionChatState } from './types'

export function emptySessionState(): SessionChatState {
  return {
    messagesById: {},
    messageOrder: [],
    trimmedCount: 0,
    toolCalls: {},
    toolCallOrder: [],
    textAtToolCallStart: {},
    toolCallOwnerMessageId: {},
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: null,
    sessionTokens: 0,
    sessionCost: 0,
    rateLimitEvent: null,
    lastUserMessageAt: null,
    cancelStage: null,
    autoApproveEffective: null,
    lastReceivedEventTime: null,
    spanBySpanId: {},
    pendingSpanUpdatesBySpanId: {},
    mergedReplayMessageIds: {},
    goalStatus: null,
    goalPills: {},
    loopStatus: null,
    pendingAsk: null,
    activeTurnId: null,
    activeTurnAgentId: null,
    // #823 catch-up redesign (BE-DESIGN.md §6.1/§6.2) — see SessionCursor's
    // and SessionChatState.cursor's doc comments.
    cursor: null,
    awaitingCatchUp: false,
  }
}

/**
 * Drop `keys` from `obj`, typed as `Omit<T, K>`. Used by bucketToForeground and
 * syncChatForeground to strip the internal-only bucket fields (messageOrder,
 * trimmedCount, span maps, toolCallOwnerMessageId) before exposing a bucket as
 * ChatStore's foreground session fields — a real omit rather than a
 * `const { field: _unused, ...rest } = obj` destructure, which would require
 * binding (and then never reading) one throwaway local per dropped field.
 */
export function omitKeys<T extends object, K extends keyof T>(obj: T, keys: readonly K[]): Omit<T, K> {
  const clone: T = { ...obj }
  for (const key of keys) {
    delete clone[key]
  }
  return clone
}

/**
 * Stamp a single baked tool-call entry with its `textOffset`, and return the
 * new (never mutated in place) entry.
 *
 * Semantics: `textAtToolCallStart[id]` holds the owning message's `content`
 * AT THE MOMENT this call started (see the `tool_call_start` handler, which
 * writes it once per call id and never overwrites it thereafter). Message
 * content is append-only from that point on — every later token/replay-merge
 * write only ever concatenates onto the end, never rewrites an earlier
 * range — so the snapshot's `.length` is already the correct split offset
 * into whatever the content eventually finalizes to, no matter how much
 * more text or how many more calls land after this one started.
 *
 * INVARIANT — once stamped, an offset is correct forever: `prevOffset` —
 * the offset the call was already stamped with on a PRIOR bake, if any —
 * is checked FIRST, ahead of a fresh `textAtToolCallStart` snapshot. A call
 * can be baked more than once over its lifetime (the abandoned-bubble
 * copy-bake in the 'token' handler followed by the turn-end bake at `done`;
 * a WS-replay same-turn merge baking a call that was already copy-baked
 * onto an earlier segment; a reconnect replaying a `tool_call_start` for a
 * call that is already baked into a historical message; etc). In every
 * LEGITIMATE re-bake, `prevOffset === snapshot.length` anyway (content is
 * append-only, so the two agree whenever both are available) — so
 * preferring `prevOffset` changes nothing for those cases. What it fixes is
 * the ILLEGITIMATE case: a WS reconnect replays the transcript into a
 * bucket that already baked-and-wiped this call's snapshot, so the
 * `tool_call_start` reconnect guard (see its own comment) has to record a
 * FRESH snapshot — one that reflects however much text has accumulated by
 * reconnect time, not the original call-start position. Preferring
 * `snapshot` there would silently overwrite a correct, already-stamped
 * offset with a bogus end-of-text one on the very next bake. Falling back
 * to `snapshot?.length` only when there is no `prevOffset` at all (first
 * bake ever) is still required: silently computing `(undefined ?? '').length`
 * would default to 0 and misrepresent "snapshot no longer available" as
 * "started at position 0".
 */
export function stampToolCallOffset(
  id: string,
  tc: ToolCall & { call_id: string },
  textAtToolCallStart: Record<string, string>,
  prevOffset: number | undefined,
): PositionedToolCall {
  const snapshot = textAtToolCallStart[id]
  const textOffset = prevOffset ?? snapshot?.length
  return {
    id,
    tool: tc.tool,
    params: tc.params ?? {},
    result: tc.result,
    status: tc.status,
    duration_ms: tc.duration_ms,
    error: tc.error,
    ...(textOffset !== undefined ? { textOffset } : {}),
  }
}

/**
 * Bake pending tool calls onto their OWNING message (per ownerByCallId,
 * falling back to fallbackMsgId when a call has no recorded owner — legacy
 * calls started before this tracking existed, or whose owner message was
 * evicted from the ring buffer) rather than blindly the single "last"
 * message. Once a turn produces more than one assistant bubble (Fix 5a; the
 * sync/await-mode delegate attribution fix), a flat "bake everything onto
 * the last message" would silently move a tool call onto a DIFFERENT
 * producer's bubble whenever that producer's segment happens to be the one
 * still open when the bake fires.
 *
 * Each baked entry is stamped with `textOffset` via `stampToolCallOffset`
 * (see its doc comment for the exact offset semantics and the
 * re-bake-preservation rule) using `textAtToolCallStart` for the snapshot
 * lookup and any already-baked entry on the target message for the
 * preservation fallback.
 *
 * Replaces each touched entry in `messagesById` with a NEW object (never
 * mutates an existing message object in place) — safe both as an Immer
 * producer helper (draft-slot reassignment is the standard Immer pattern
 * used elsewhere in this file) and against a caller's own
 * shallow-copied plain object (clearStreamingState's manual `next.messagesById
 * = {...bucket.messagesById}` copy shares message object REFERENCES with the
 * original bucket — mutating a shared message in place would corrupt the
 * pre-update state that other code may still hold a reference to). Does NOT
 * touch toolCalls/toolCallOrder/textAtToolCallStart/toolCallOwnerMessageId;
 * the caller decides whether this is a non-destructive visibility copy
 * (leave the live bucket untouched — see the 'token' case's
 * producer-boundary bake) or a final turn-end move (clear them after).
 */
export function bakeToolCallsByOwner(
  messagesById: Record<string, ChatMessage>,
  toolCallOrder: readonly string[],
  toolCalls: Record<string, ToolCall & { call_id: string }>,
  ownerByCallId: Record<string, string>,
  fallbackMsgId: string | null,
  textAtToolCallStart: Record<string, string>,
): void {
  const idsByOwner = new Map<string, string[]>()
  for (const id of toolCallOrder) {
    if (!toolCalls[id]) continue
    const mappedOwner = ownerByCallId[id]
    const owner = mappedOwner && messagesById[mappedOwner] ? mappedOwner : fallbackMsgId
    if (!owner) {
      // No recorded owner AND no fallback message in the bucket to bake
      // onto — the call is silently dropped from every bubble's tool_calls.
      // Every other skip-path in this file already logs (chatReplayDedupSkipped,
      // chatTurnCanceledNoMatch, etc.) — this one shouldn't be the exception.
      console.warn('chat.tool_call_dropped_no_owner', { id, tool: toolCalls[id]?.tool })
      logDiagnostic('chatToolCallDroppedNoOwner', { callId: id, tool: toolCalls[id]?.tool })
      continue
    }
    const bucket = idsByOwner.get(owner)
    if (bucket) bucket.push(id)
    else idsByOwner.set(owner, [id])
  }
  for (const [ownerMsgId, ids] of idsByOwner) {
    const msg = messagesById[ownerMsgId]
    if (!msg || msg.role !== 'assistant') continue
    const existing = (msg.tool_calls ?? []) as PositionedToolCall[]
    const existingById = new Map(existing.map((tc) => [tc.id, tc]))
    const baked = ids.map((id) => stampToolCallOffset(id, toolCalls[id], textAtToolCallStart, existingById.get(id)?.textOffset))
    const mergedById = new Map<string, PositionedToolCall>(existing.map((tc) => [tc.id, tc]))
    for (const tc of baked) mergedById.set(tc.id, tc)
    messagesById[ownerMsgId] = { ...msg, tool_calls: Array.from(mergedById.values()) }
  }
}

/**
 * Scan every assistant message in a bucket for an already-baked tool call
 * with the given id — used by the `tool_call_start` reconnect guard
 * (defense-in-depth alongside `stampToolCallOffset`'s prevOffset-first
 * invariant) to detect a WS reattach replaying `tool_call_start` for a call
 * that has ALREADY been baked into `message.tool_calls` (by a prior `done`
 * or WS-replay-merge bake, which wipes toolCalls/toolCallOrder/
 * textAtToolCallStart/toolCallOwnerMessageId — see those bake sites). When
 * true, the caller must treat the frame as a no-op start: it must NOT
 * re-record a snapshot (the bucket's current content no longer reflects
 * where this call originally started — that position is only preserved in
 * the already-baked entry's `textOffset`) and must NOT re-add the id to
 * `toolCallOrder` (doing so would queue the call for ANOTHER bake, risking
 * it landing on the wrong owner message if the tail message has changed
 * since the original bake — `stampToolCallOffset`'s prevOffset preference
 * only recovers the correct offset when the re-bake lands back on the SAME
 * owner message that already holds it).
 *
 * Must NOT trigger for a still-streaming turn's calls reattaching mid-turn
 * (the legitimate reconnect case the guard's surrounding comment describes)
 * — those calls are not yet baked into any message, so this scan correctly
 * returns false for them and the existing snapshot/order guards run
 * unchanged.
 *
 * O(messages) — deliberately not indexed. Only a `tool_call_start` frame
 * calls this, and only the reconnect/replay path ever revisits a call id
 * that could possibly already be baked (a live turn's own calls are never
 * baked until `done`/a replay merge, so a live call is never found here —
 * see the call site). Bucket message counts are capped by
 * MAX_MESSAGES_PER_SESSION, and there is no cheaper existing index:
 * toolCallOwnerMessageId is wiped by the very same bake that populates
 * message.tool_calls, so it cannot answer "is this id baked" for the one
 * case this function exists to catch.
 */
export function isToolCallBakedInBucket(messagesById: Record<string, ChatMessage>, callId: string): boolean {
  for (const id in messagesById) {
    const msg = messagesById[id]
    if (msg.role !== 'assistant' || !msg.tool_calls) continue
    for (const tc of msg.tool_calls) {
      if (tc.id === callId) return true
    }
  }
  return false
}

/**
 * Convert an ordered messages array into ring-buffer state, applying the cap.
 * Emits a one-time toast when trimming first occurs (trimmedCount 0 → >0).
 */
export function applyMessageArray(
  msgs: ChatMessage[],
  bucket: SessionChatState,
): Partial<SessionChatState> {
  let finalMsgs = msgs
  let newTrimmedCount = bucket.trimmedCount
  let toolCallsPatch: typeof bucket.toolCalls = { ...bucket.toolCalls }
  let toolCallOrderPatch: string[] = [...bucket.toolCallOrder]
  let textAtToolCallStartPatch: typeof bucket.textAtToolCallStart = { ...bucket.textAtToolCallStart }
  let toolCallOwnerMessageIdPatch: Record<string, string> = { ...bucket.toolCallOwnerMessageId }
  let spanBySpanIdPatch: typeof bucket.spanBySpanId = { ...(bucket.spanBySpanId ?? {}) }

  if (msgs.length > MAX_MESSAGES_PER_SESSION) {
    const evictCount = msgs.length - MAX_MESSAGES_PER_SESSION
    const evicted = msgs.slice(0, evictCount)
    finalMsgs = msgs.slice(evictCount)

    // Emit one-time toast on first trim for this session.
    if (newTrimmedCount === 0) {
      useUiStore.getState().addToast({
        message: 'Session is long — earlier messages are hidden from this view; the full transcript is preserved on the server.',
        variant: 'default',
      })
    }
    newTrimmedCount += evictCount

    // Evict tool calls owned by evicted messages.
    const evictedCallIds = new Set(evicted.flatMap((m) => (m.tool_calls ?? []).map((tc) => tc.id)))
    if (evictedCallIds.size > 0) {
      const newToolCalls: typeof bucket.toolCalls = {}
      for (const [k, v] of Object.entries(toolCallsPatch)) {
        if (!evictedCallIds.has(k)) newToolCalls[k] = v
      }
      toolCallsPatch = newToolCalls
      toolCallOrderPatch = toolCallOrderPatch.filter((id) => !evictedCallIds.has(id))
      // Evict textAtToolCallStart entries for the evicted call ids.
      const newText: typeof bucket.textAtToolCallStart = {}
      for (const [k, v] of Object.entries(textAtToolCallStartPatch)) {
        if (!evictedCallIds.has(k)) newText[k] = v
      }
      textAtToolCallStartPatch = newText
      // Evict toolCallOwnerMessageId entries for the evicted call ids.
      const newOwner: Record<string, string> = {}
      for (const [k, v] of Object.entries(toolCallOwnerMessageIdPatch)) {
        if (!evictedCallIds.has(k)) newOwner[k] = v
      }
      toolCallOwnerMessageIdPatch = newOwner
    }

    // Evict the span index via the shared helper (see evictSpanIndexEntries'
    // doc comment).
    const evictedMessageIds = new Set(evicted.map((m) => m.id))
    spanBySpanIdPatch = evictSpanIndexEntries(spanBySpanIdPatch, evictedMessageIds)
  }

  const messagesById: Record<string, ChatMessage> = {}
  const messageOrder: string[] = []
  for (const m of finalMsgs) {
    messagesById[m.id] = m
    messageOrder.push(m.id)
  }

  return {
    messagesById,
    messageOrder,
    trimmedCount: newTrimmedCount,
    toolCalls: toolCallsPatch,
    toolCallOrder: toolCallOrderPatch,
    textAtToolCallStart: textAtToolCallStartPatch,
    toolCallOwnerMessageId: toolCallOwnerMessageIdPatch,
    spanBySpanId: spanBySpanIdPatch,
  }
}
