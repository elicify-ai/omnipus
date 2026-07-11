import { create } from 'zustand'
import { produce } from 'immer'
import { generateId } from '@/lib/constants'
import { useUiStore } from '@/store/ui'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore, registerChatSetReplaying, registerChatResetForReplay } from '@/store/session'
import { queryClient } from '@/lib/queryClient'
import type { Message, ToolCall } from '@/lib/api'
import type { WsReceiveFrame, WsReplayMessageFrame, WsRateLimitFrame, WsSubagentStartFrame, WsSubagentEndFrame } from '@/lib/ws'
import type { ToolResultRef, TruncatedResult, WhatsAppPairingFrame, NotificationFrame } from '@/lib/api/generated/asyncapi-types'
import { MessageFrame as MessageFrameSchema } from '@/lib/api/generated/schemas'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useNotificationsStore } from '@/store/notifications'
import { useToolApprovalStore } from '@/store/toolApproval'
import { registerSyncChatForeground } from '@/store/session'
import { logDiagnostic } from '@/lib/telemetry'

// Maximum messages kept in the visible ring buffer per session.
// Older messages are evicted once this limit is exceeded; full transcript is preserved server-side.
export const MAX_MESSAGES_PER_SESSION = 500

// Maximum byte size of a tool result stored in client state; oversized results become a sentinel.
const MAX_TOOL_RESULT_BYTES = 50_000

// Preview size for client-side truncated results (4 KiB).
const CLIENT_TRUNCATION_PREVIEW_BYTES = 4_096

export interface MediaAttachment {
  type: 'image' | 'audio' | 'video' | 'file'
  url: string
  filename: string
  contentType: string
  caption?: string
}

// SpanStep is one step in a subagent span.
// The discriminant `kind` allows renderers to switch between tool calls
// and interleaved text fragments without a runtime type-check on all fields.
// Text steps are reserved for future subagent-text streaming; no emit site
// writes them yet, but the type admits them so a future sprint can add
// subagent-text streaming without a type change.
export type SpanStep =
  | { kind: 'tool'; tool: ToolCall & { call_id: string } }
  | { kind: 'text'; text: string; ts: number }

// FR-H-008/FR-H-009: a subagent span brackets one sub-turn.
// Discriminated union: 'running' vs terminal so TypeScript enforces that
// durationMs / finalResult / reason are only accessible on terminal spans.
interface SubagentSpanBase {
  spanId: string
  parentCallId: string
  taskLabel: string
  steps: SpanStep[]
  /**
   * Id of the agent running this sub-turn (the delegate), when the frame
   * carried one — used to resolve name/icon/type for display. Note: for
   * native (non-external-CLI) delegation to a named target agent, this
   * currently reflects the PARENT's id due to a backend limitation in
   * pkg/agent/subturn.go — see the agent-resolution fallback in
   * useRunningActivity.ts which works around this using the originating
   * delegate call's own agent_id param.
   */
  agentId?: string
}

export interface SubagentSpanRunning extends SubagentSpanBase {
  status: 'running'
}

export interface SubagentSpanTerminal extends SubagentSpanBase {
  status: 'success' | 'error' | 'cancelled' | 'interrupted' | 'timeout'
  durationMs: number
  finalResult?: string
  /** Reason populated when status is 'interrupted'. */
  reason?: 'parent_timeout' | 'parent_cancelled' | 'parent_done_early' | 'unknown'
}

export type SubagentSpan = SubagentSpanRunning | SubagentSpanTerminal

// A buffered frame waiting for its subagent_start to arrive (FR-H-009)
interface BufferedFrame {
  frame: WsReceiveFrame & { type: 'tool_call_start' | 'tool_call_result' }
  arrivedAt: number
}

// #3: ChatMessage is the SPA-internal display type. It intersects Message (the
// discriminated union) with extra display-only fields so each role variant
// still carries its role-specific status constraints. Using a type alias (not
// interface extends) because TypeScript does not allow extending a union type.
export type ChatMessage = Message & {
  isStreaming?: boolean
  media?: MediaAttachment[]
  spans?: SubagentSpan[]
  /** Agent that produced this message (assistant messages only). */
  agentId?: string
  /**
   * Turn-correlation id (Fix 5c; wire field ReplayMessageFrame.turn_id, sourced
   * from TranscriptEntry.TurnID), stamped on assistant messages hydrated via WS
   * replay. Lets a later `turn_canceled` replay entry find and re-mark this
   * exact message as interrupted, without relying on stream adjacency — async
   * delegation can interleave other agents'/turns' frames in between, so "last
   * assistant message" is not a safe proxy for replay correlation. Not
   * populated for messages created via live token streaming (TokenFrame
   * carries no turn_id on the wire — live cancellation instead uses
   * markLastMessageInterrupted()'s last-assistant scan) or via the REST
   * cold-load path (fetchSessionMessages), whose persisted entries already carry the
   * true status directly and need no correlation.
   */
  turnId?: string
}

// Client-side truncation sentinel — parallel to server TruncatedResult/ToolResultRef shapes.
export interface ClientTruncatedResult {
  _truncated_client: true
  original_size_bytes: number
  preview: string
}

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

export interface RateLimitEventData {
  scope: 'agent' | 'channel' | 'global'
  resource: string
  policyRule: string
  retryAfterSeconds: number
  agentId?: string
  tool?: string
}

/** All per-session chat state for one concurrent session. */
export interface SessionChatState {
  // Map-indexed ring buffer. Use getMessages(bucket) to get the ordered array.
  messagesById: Record<string, ChatMessage>
  messageOrder: string[]
  /** Number of messages trimmed from the front of messageOrder (evicted from the ring buffer). */
  trimmedCount: number

  toolCalls: Record<string, ToolCall & { call_id: string }>
  toolCallOrder: string[]
  textAtToolCallStart: Record<string, string>
  isStreaming: boolean
  /** True from attach_session until first done frame — disables send input during replay. */
  isReplaying: boolean
  /** Set when a done frame arrives while isReplaying was true. */
  replayCompletedForSession: string | null
  sessionTokens: number
  sessionCost: number
  rateLimitEvent: RateLimitEventData | null
  /**
   * H1-FE: Unix timestamp (ms) when the most recent user message was sent for
   * this session. Used to guard against force-clearing isStreaming on the active
   * bucket when an unknown-sid done arrives — if the active session just sent a
   * user message recently it is very likely mid-stream, not a stale spinner.
   */
  lastUserMessageAt: number | null
  /**
   * B3: current cancel stage for this session, or null when no cancel is in
   * progress. Set by cancel_stage frames from the gateway:
   *   graceful  — cancel acknowledged; agent finishing current tool call.
   *   hard      — graceful deadline expired; force-killing the agent turn.
   *   detached  — session detached from the turn; treat as idle.
   * Cleared to null when a done frame arrives.
   */
  cancelStage: 'graceful' | 'hard' | 'detached' | null
  /**
   * ISO timestamp of the most recent server frame the SPA has applied.
   * Used as the `since` cursor in attach_session to avoid replaying already-seen frames.
   */
  lastReceivedEventTime: string | null
  /**
   * O(1) index from parent_call_id → { messageId, spanIdx } for the currently-running subagent span.
   * Written by subagent_start, cleared by subagent_end.
   */
  spanByParentCallId: Record<string, { messageId: string; spanIdx: number }>
}

function emptySessionState(): SessionChatState {
  return {
    messagesById: {},
    messageOrder: [],
    trimmedCount: 0,
    toolCalls: {},
    toolCallOrder: [],
    textAtToolCallStart: {},
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: null,
    sessionTokens: 0,
    sessionCost: 0,
    rateLimitEvent: null,
    lastUserMessageAt: null,
    cancelStage: null,
    lastReceivedEventTime: null,
    spanByParentCallId: {},
  }
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
 * spanByParentCallId entries whose messageId matches the evicted message.
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
    }
    bucket.toolCallOrder = bucket.toolCallOrder.filter((id) => !evictedCallIds.has(id))
  }

  // Evict spanByParentCallId entries pointing at this message.
  for (const [parentCallId, entry] of Object.entries(bucket.spanByParentCallId)) {
    if (entry.messageId === messageId) {
      delete bucket.spanByParentCallId[parentCallId]
    }
  }
}

/**
 * Convert an ordered messages array into ring-buffer state, applying the cap.
 * Emits a one-time toast when trimming first occurs (trimmedCount 0 → >0).
 */
function applyMessageArray(
  msgs: ChatMessage[],
  bucket: SessionChatState,
): Partial<SessionChatState> {
  let finalMsgs = msgs
  let newTrimmedCount = bucket.trimmedCount
  let toolCallsPatch: typeof bucket.toolCalls = { ...bucket.toolCalls }
  let toolCallOrderPatch: string[] = [...bucket.toolCallOrder]
  let textAtToolCallStartPatch: typeof bucket.textAtToolCallStart = { ...bucket.textAtToolCallStart }
  let spanByParentCallIdPatch: typeof bucket.spanByParentCallId = { ...bucket.spanByParentCallId }

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
    }

    // Evict spanByParentCallId entries whose message is being evicted.
    const evictedMessageIds = new Set(evicted.map((m) => m.id))
    const newSpanIndex: typeof bucket.spanByParentCallId = {}
    for (const [parentCallId, entry] of Object.entries(spanByParentCallIdPatch)) {
      if (!evictedMessageIds.has(entry.messageId)) newSpanIndex[parentCallId] = entry
    }
    spanByParentCallIdPatch = newSpanIndex
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
    spanByParentCallId: spanByParentCallIdPatch,
  }
}

/** Advance lastReceivedEventTime if the provided timestamp is newer (lexicographic ISO-8601 comparison). */
function advanceEventTime(current: string | null, incoming: string | null | undefined): string | null {
  if (!incoming) return current
  if (!current) return incoming
  return incoming > current ? incoming : current
}

interface ChatStore {
  /** Per-session state buckets keyed by session_id. */
  sessionsById: Record<string, SessionChatState>

  // ── Foreground selectors (derived from sessionsById[activeSessionId]) ────────
  // These are convenience getters for the UI to read the active session's state.
  // They return stable empty values when no session is active.
  //
  // NOTE: `messages` is the COMPUTED ordered array derived from the active
  // bucket's messagesById + messageOrder. It is synced after every bucket
  // mutation. Consumers should NOT access sessionsById[id].messagesById or
  // sessionsById[id].messageOrder directly — use getMessages(bucket) instead.
  messages: ChatMessage[]
  isStreaming: boolean
  isReplaying: boolean
  replayCompletedForSession: string | null
  toolCalls: Record<string, ToolCall & { call_id: string }>
  toolCallOrder: string[]
  textAtToolCallStart: Record<string, string>
  sessionTokens: number
  sessionCost: number
  rateLimitEvent: RateLimitEventData | null
  lastUserMessageAt: number | null
  /** B3: cancel progress stage for the active session, or null when idle. */
  cancelStage: 'graceful' | 'hard' | 'detached' | null
  /** ISO timestamp of the most recent server frame for the active session. */
  lastReceivedEventTime: string | null
  /**
   * Phase 1 / FR-008/009/010: per-thread model override for the next
   * outgoing message. The composer model selector writes the picker's
   * value here; the AssistantUI runtime reads it in `onNew` and threads
   * it into `sendMessage` as `model_name` (forwarded to the server as
   * `metadata.model_name` in the WS frame). Cleared after each send
   * so the next session reopen re-derives the default from transcript
   * history or the agent's `model` config (per spec §18 Q3).
   */
  nextModel: string | null
  /** Set the next-turn model override (called by the composer). */
  setNextModel: (model: string | null) => void

  // ── Actions that operate on the foreground session ───────────────────────────
  setReplaying: (value: boolean) => void
  setMessages: (messages: Message[]) => void
  appendMessage: (message: ChatMessage) => void
  updateLastAssistantMessage: (content: string, done?: boolean) => void
  markLastMessageInterrupted: () => void

  startToolCall: (callId: string, tool: string, params: Record<string, unknown>) => void
  resolveToolCall: (callId: string, result: unknown, status: 'success' | 'error', durationMs?: number, error?: string) => void
  cancelToolCall: (callId: string) => void

  updateSessionStats: (tokens: number, cost: number) => void
  /**
   * Seed sessionTokens from the persisted total_tokens on a historic session
   * attach. Only sets the value when the bucket is fresh (sessionTokens === 0)
   * so live deltas from the `done` frame are never double-counted.
   */
  seedSessionTokens: (total: number) => void
  setRateLimitEvent: (event: RateLimitEventData) => void
  clearRateLimitEvent: () => void

  startSpan: (frame: WsSubagentStartFrame) => void
  endSpan: (frame: WsSubagentEndFrame) => void
  attachStepToSpan: (parentCallId: string, step: ToolCall & { call_id: string }) => void

  // Resets only the foreground session bucket. Does NOT affect other sessions.
  resetSession: () => void

  // Wipes a specific session bucket and marks it as replaying so the next
  // replay frames rebuild from scratch. Used on WS reconnect to prevent
  // duplicate bubbles when the gateway re-replays the transcript.
  resetSessionForReplay: (sessionId: string) => void

  // ── Outbound queue (Fix 3) ────────────────────────────────────────────────────
  // Messages typed while the WS is disconnected are buffered here (max 5).
  // drainOutboundQueue() is called by OmnipusRuntimeProvider on reconnect.
  /** Pending outbound messages queued while WS was disconnected. Max 5. */
  outboundQueue: string[]
  /** Queue a message for when the WS reconnects. Returns false if queue is full. */
  enqueueOutboundMessage: (content: string) => boolean
  /** Send all queued messages now that the WS is connected. */
  drainOutboundQueue: () => void
  /**
   * BUG FIX (2026-07): messages handed off from `outboundQueue` by
   * `drainOutboundQueue()`, sent ONE AT A TIME. `sendMessage` only allows one
   * in-flight turn (`isStreaming`) — the previous implementation looped over
   * the whole queue synchronously, so every message after the first hit the
   * `isStreaming` guard and was silently dropped (no chat bubble, no error).
   * `drainOutboundQueue` now moves the queue here and sends only the head;
   * `maybeDrainNext()` (module-private, called from every place a turn ends —
   * the `done`/`error` frame handlers, `cancelStream`, `clearStreamingState`,
   * `markLastMessageInterrupted`, and the failed-send rollback in
   * `sendMessage` itself) pops and sends the next item once `isStreaming`
   * clears. Exposed on the store (not a closured helper) so tests can invoke
   * it directly if needed.
   */
  pendingDrainQueue: string[]

  // ── Actions ───────────────────────────────────────────────────────────────────
  // opts.mediaRefs: optional media:// refs (e.g. uploaded images) threaded
  //   into the outbound message frame so the agent sees the attachment (#254).
  // opts.attachments: optional MediaAttachment[] rendered inline on the
  //   optimistic user bubble (display only — not sent on the wire).
  // opts.model_name: optional model slug for THIS turn only (Phase 1, FR-010).
  //   The composer model selector writes the picker's value here; sendMessage
  //   forwards it as `metadata.model_name` in the WS message frame. The
  //   server honors it when present and falls back to the agent's `model`
  //   config when absent.
  sendMessage: (content: string, opts?: { mediaRefs?: string[]; attachments?: MediaAttachment[]; model_name?: string }) => void
  /** Validate an outbound MessageFrame against the generated Zod schema. Logs and dev-toasts on failure but never blocks the send. `sessionId` (the sending session, or the pending-bucket key when no session exists yet) is threaded through into the production telemetry record for operator correlation. */
  _validateOutboundFrame: (payload: unknown, sessionId?: string | null) => void
  cancelStream: () => void
  respondToPairing: (deviceId: string, decision: 'approve' | 'reject') => void

  // C8: defensively clear in-flight/streaming state for every session bucket.
  // Called when the stream is terminated by something OTHER than a clean done
  // frame (WS close/disconnect, a terminal error frame, an error event). Without
  // this, a turn whose terminal frame is missed would leave isStreaming=true
  // forever — the composer stays disabled and the "thinking" spinner never
  // resolves (the "stuck chat stream" wedge). Marks any still-streaming
  // assistant message as 'done' so AssistantUI stops rendering it as running.
  clearStreamingState: () => void

  handleFrame: (frame: WsReceiveFrame) => void
}

// Module-scoped handle for the 60s auto-clear timer on rate-limit events, keyed per session.
const rateLimitClearTimers: Record<string, ReturnType<typeof setTimeout>> = {}
const RATE_LIMIT_CLEAR_MS = 60_000

// Tracks when isReplaying was most recently set to true per session, keyed by session_id.
const replayingStartedAt: Record<string, number> = {}

// Pending setTimeout handles that will flip isReplaying=false after
// MIN_REPLAY_DISPLAY_MS - elapsed. Tracked per-session so a new
// setReplaying(true) (e.g. re-attach to the same session) can cancel the
// stale timer before it stomps the freshly-started replay window.
const replayingClearTimers: Record<string, ReturnType<typeof setTimeout>> = {}

// Diagnostic flag per session — true when at least one replay_message was processed this turn.
const sawReplayMessageThisTurn: Record<string, boolean> = {}

// FR-H-009: out-of-order frame buffer — tool_call_start/result frames that
// arrived before their subagent_start. Keyed by `${sessionId}:${parentCallId}`.
// Dropped to flat rendering after ORPHAN_BUFFER_TTL_MS if no subagent_start arrives.
const ORPHAN_BUFFER_TTL_MS = 10_000
const pendingByParentCallId: Record<string, BufferedFrame[]> = {}
const orphanTimers: Record<string, ReturnType<typeof setTimeout>> = {}

// ── Frame-routing helpers ─────────────────────────────────────────────────────

/** O(1) span check using the spanByParentCallId index. */
function hasOpenSpanFast(bucket: SessionChatState, parentCallId: string): boolean {
  return parentCallId in bucket.spanByParentCallId
}

function bufferForSpan(
  bufferKey: string,
  frame: BufferedFrame['frame'],
  onTimeout: (buffered: BufferedFrame[]) => void,
): void {
  if (!pendingByParentCallId[bufferKey]) {
    pendingByParentCallId[bufferKey] = []
    if (!orphanTimers[bufferKey]) {
      orphanTimers[bufferKey] = setTimeout(() => {
        const buffered = pendingByParentCallId[bufferKey] ?? []
        delete pendingByParentCallId[bufferKey]
        delete orphanTimers[bufferKey]
        if (buffered.length > 0) {
          onTimeout(buffered)
        }
      }, ORPHAN_BUFFER_TTL_MS)
    }
  }
  pendingByParentCallId[bufferKey].push({ frame, arrivedAt: Date.now() })
}

const EMPTY_BUCKET = emptySessionState()

// F-S1: all server→client frames that must carry session_id.
// Frames in this set without a session_id are routing errors.
// Global frames (error, auth_*, ping, pong, device_pairing_*) are intentionally absent.
const SESSION_SCOPED_FRAME_TYPES = new Set([
  'token', 'done', 'tool_call_start', 'tool_call_result',
  'subagent_start', 'subagent_end', 'replay_message', 'replay_done',
  'agent_switched', 'task_status_changed',
  'tool_approval_required', 'rate_limit', 'media', 'session_started',
  'system_overload', 'session_close_ack', 'cancel_stage',
])

// F-S2: FALLBACK_SID exists only in test mode so tests that don't establish a session
// still route frames to a consistent bucket. In production getActiveSid() returns null
// when no session is active; frame writers must early-return on null.
const FALLBACK_SID = import.meta.env.MODE === 'test' ? '__default' : null

// HIGH-2: consecutive unknown frame counter. Reset on any known-good frame.
// On threshold (5), promotes to a user-visible warning toast.
let unknownFrameCount = 0
const UNKNOWN_FRAME_TOAST_THRESHOLD = 5

export const useChatStore = create<ChatStore>((set, get) => {
  // ── Internal helpers that mutate a named session bucket ─────────────────────
  // These read/write sessionsById[sid] and then re-sync foreground fields.

  // F-S2: returns null in production when no session is active.
  // In test mode returns FALLBACK_SID ('__default') for test compatibility.
  function getActiveSid(): string | null {
    return useSessionStore.getState().activeSessionId ?? FALLBACK_SID
  }

  /** Project a session bucket to foreground ChatStore fields (messagesById+messageOrder → messages[]). */
  function bucketToForeground(bucket: SessionChatState): Omit<SessionChatState, 'messagesById' | 'messageOrder' | 'trimmedCount' | 'spanByParentCallId'> & { messages: ChatMessage[] } {
    const { messagesById: _mb, messageOrder: _mo, trimmedCount: _tc, spanByParentCallId: _sp, ...rest } = bucket
    return { ...rest, messages: getMessages(bucket) }
  }

  /** Find or lazily create a bucket for sid. No-op if sid is null. */
  function withBucket(sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>): void {
    if (!sid) return
    set((state) => {
      const existing = state.sessionsById[sid] ?? emptySessionState()
      const patch = updater(existing)
      const updated: SessionChatState = { ...existing, ...patch }
      const sessionsById = { ...state.sessionsById, [sid]: updated }
      const activeSid = getActiveSid()
      const activeBucket = (activeSid ? sessionsById[activeSid] : null) ?? EMPTY_BUCKET
      return { sessionsById, ...bucketToForeground(activeBucket) }
    })
  }

  /** Re-sync foreground fields from sessionsById after an external session switch. */
  function syncForeground(): void {
    set((state) => {
      const activeSid = getActiveSid()
      const fg = (activeSid ? state.sessionsById[activeSid] : null) ?? EMPTY_BUCKET
      return bucketToForeground(fg)
    })
  }

  /**
   * Sets a session's rate-limit event and arms the shared auto-clear timer:
   * cancels any existing timer for the session, stores the new event, then
   * after RATE_LIMIT_CLEAR_MS clears it — but only if the bucket's event is
   * still referentially the one just set (a newer rate_limit event that
   * replaced it before the timer fired is left alone). Shared by
   * setRateLimitEvent and the 'rate_limit' WS-frame handler so the
   * "set + timeout + clear" logic isn't implemented twice.
   */
  function armRateLimitClear(sid: string, event: RateLimitEventData): void {
    if (rateLimitClearTimers[sid] != null) {
      clearTimeout(rateLimitClearTimers[sid])
      delete rateLimitClearTimers[sid]
    }
    withBucket(sid, () => ({ rateLimitEvent: event }))
    rateLimitClearTimers[sid] = setTimeout(() => {
      delete rateLimitClearTimers[sid]
      set((state) => {
        const bucket = state.sessionsById[sid]
        if (!bucket || bucket.rateLimitEvent !== event) return {}
        const updated: SessionChatState = { ...bucket, rateLimitEvent: null }
        const sessionsById = { ...state.sessionsById, [sid]: updated }
        const activeSid = getActiveSid()
        const fg = (activeSid ? sessionsById[activeSid] : null) ?? EMPTY_BUCKET
        return { sessionsById, ...bucketToForeground(fg) }
      })
    }, RATE_LIMIT_CLEAR_MS)
  }

  /**
   * BUG FIX (2026-07, offline-queue drain): pop and send the next drained
   * message, but ONLY if no turn is currently in flight. Called every time a
   * turn ends (done/error frames, explicit cancel, the C8 stream-clear sweep,
   * or a failed send) so queued messages go out one at a time instead of the
   * old synchronous loop that fired `sendMessage` for the whole queue in one
   * tick — which silently dropped every message after the first because the
   * very first call flips `isStreaming` synchronously and every subsequent
   * call in that same loop hit the `isStreaming` guard in `sendMessage` (that
   * guard shows a connection-error banner and returns WITHOUT re-enqueuing,
   * unlike the disconnected-WS branch checked immediately after the
   * isStreaming guard in sendMessage).
   *
   * Reads `isStreaming` fresh via `get()` rather than trusting a closed-over
   * value, since callers invoke this synchronously from inside a `set()`
   * update that may have just flipped it.
   */
  function maybeDrainNext(): void {
    const { pendingDrainQueue, isStreaming } = get()
    if (pendingDrainQueue.length === 0 || isStreaming) return
    const [next, ...rest] = pendingDrainQueue
    set({ pendingDrainQueue: rest })
    get().sendMessage(next)
  }

  return {
    sessionsById: {},

    // ── Outbound queue initial state ─────────────────────────────────────────
    outboundQueue: [],
    pendingDrainQueue: [],

    // Foreground selectors — derived from sessionsById[activeSessionId].
    // Initial values are the empty-session defaults projected through bucketToForeground.
    // Note: we spread emptySessionState() here but consumers of messages: ChatMessage[]
    // expect an array — the ring buffer fields (messagesById/messageOrder) are not
    // exported on ChatStore; only messages (the derived array) is.
    messages: [],
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: null,
    toolCalls: {},
    toolCallOrder: [],
    textAtToolCallStart: {},
    sessionTokens: 0,
    sessionCost: 0,
    rateLimitEvent: null,
    lastUserMessageAt: null,
    cancelStage: null,
    lastReceivedEventTime: null,
    // Phase 1 / FR-008/009/010: per-thread model override for the next
    // outgoing message. null means "no override" — the server uses the
    // agent's `model` config. The composer writes here on picker
    // change; the runtime reads here in onNew and clears it.
    nextModel: null,
    setNextModel: (model) => set({ nextModel: model }),

    setReplaying: (value) => {
      const sid = getActiveSid()
      if (!sid) return
      if (value) {
        // Always reset the window start on setReplaying(true), even when
        // isReplaying is already true. A second attach to the same session
        // (e.g. user re-clicks the session button, or the SPA re-fires attach
        // after a session_started frame) must give a fresh MIN_REPLAY_DISPLAY_MS
        // window — otherwise a stale `replayingStartedAt` from minutes earlier
        // makes the elapsed-time computation in the false-path collapse the
        // disabled window to zero on the next 'done' frame.
        replayingStartedAt[sid] = Date.now()
        // Cancel any pending false-flip timer scheduled by a previous attach;
        // letting it fire would clobber the freshly-started replay window.
        if (replayingClearTimers[sid]) {
          clearTimeout(replayingClearTimers[sid])
          delete replayingClearTimers[sid]
        }
        withBucket(sid, () => ({ isReplaying: true }))
        return
      }
      // No-op if already false.
      const current = get().sessionsById[sid]
      if (!current?.isReplaying) {
        if (sawReplayMessageThisTurn[sid]) {
          console.warn('[chat] setReplaying(false) ignored — isReplaying was already false despite replay_message having been processed. Likely attachToSession race.')
          logDiagnostic('chatSetReplayingIgnored', { sessionId: sid })
        }
        return
      }
      sawReplayMessageThisTurn[sid] = false
      const elapsed = Date.now() - (replayingStartedAt[sid] ?? 0)
      // FR-I-014: keep the "Loading session history…" overlay visible for at least
      // MIN_REPLAY_DISPLAY_MS after replay starts.  250ms was too short — Playwright's
      // page.click() waits for network-idle before returning, which can take 250+ ms,
      // meaning the timer fired before the test could observe the disabled state.
      // 750ms is a conservative minimum that survives typical CI latency while still
      // clearing quickly enough for interactive use.
      const MIN_REPLAY_DISPLAY_MS = 750
      if (elapsed >= MIN_REPLAY_DISPLAY_MS) {
        withBucket(sid, () => ({ isReplaying: false }))
      } else {
        // Cancel any previous pending timer before scheduling a new one so a
        // burst of `done` frames doesn't queue multiple stale clears.
        if (replayingClearTimers[sid]) {
          clearTimeout(replayingClearTimers[sid])
        }
        replayingClearTimers[sid] = setTimeout(() => {
          delete replayingClearTimers[sid]
          withBucket(sid, () => ({ isReplaying: false }))
        }, MIN_REPLAY_DISPLAY_MS - elapsed)
      }
    },

    setMessages: (messages) => {
      const sid = getActiveSid()
      if (!sid) return
      const empty = emptySessionState()
      const msgs = messages as ChatMessage[]
      const msgById: Record<string, ChatMessage> = {}
      const msgOrder: string[] = []
      for (const m of msgs) { msgById[m.id] = m; msgOrder.push(m.id) }
      withBucket(sid, () => ({
        ...empty,
        messagesById: msgById,
        messageOrder: msgOrder,
      }))
    },

    appendMessage: (message) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        const msgs = [...getMessages(b), message]
        return applyMessageArray(msgs, b)
      })
    },

    updateLastAssistantMessage: (content, done = false) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        return produce(b, (draft) => {
          let msgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
          if (msgId === null) {
            const placeholder: ChatMessage = {
              id: generateId(),
              role: 'assistant',
              content: '',
              timestamp: new Date().toISOString(),
              status: 'streaming',
              isStreaming: true,
            }
            draft.messagesById[placeholder.id] = placeholder
            draft.messageOrder.push(placeholder.id)
            msgId = placeholder.id
          }
          const msg = draft.messagesById[msgId]
          msg.content = msg.content + content
          msg.isStreaming = !done
          msg.status = done ? 'done' : 'streaming'
          draft.isStreaming = !done
        }) as Partial<SessionChatState>
      })
    },

    markLastMessageInterrupted: () => {
      const sid = getActiveSid()
      withBucket(sid, (b) => {
        const lastMsgId = findLastAssistantMessageId(b.messageOrder, b.messagesById)
        if (!lastMsgId) {
          // FR-21 / T21–T23: No assistant message exists yet (cancel fired between
          // session_started and the first token frame). The server may still send
          // "Error processing message: turn canceled" as token+done frames via the
          // outbound bus. We must create a placeholder interrupted message NOW so:
          //   1. The UI shows the (interrupted) label immediately.
          //   2. The token handler discards the error-string token (it checks
          //      msgs[lastIdx].status === 'interrupted' and returns {} on match).
          //   3. The done handler preserves 'interrupted' status over 'done'.
          // Bucket-level isStreaming is set to false immediately here because the
          // server may take several seconds to process the cancel and send the done
          // frame. Clearing isStreaming now lets the useEffect([isStreaming]) fire
          // and schedule the "Stopping…" → "stop" label reset via the T25 minimum-
          // display timer (stoppingStartedAt was set BEFORE cancelStream() was called
          // by the Escape/click handler, so the timer fires after the remaining
          // portion of the 1000ms minimum window — not immediately).
          const placeholder: ChatMessage = {
            id: generateId(),
            role: 'assistant',
            content: '',
            timestamp: new Date().toISOString(),
            status: 'interrupted',
            isStreaming: false,
          }
          const msgs = [...getMessages(b), placeholder]
          return { ...applyMessageArray(msgs, b), isStreaming: false }
        }
        // FR-21 / T21–T26: set isStreaming:false AND status:'interrupted' on the message.
        // Setting isStreaming:false is necessary so that buildMessageStatus() in
        // omnipus-runtime.ts returns { type: "incomplete", reason: "cancelled" } rather
        // than { type: "running" } — only then does AssistantUI properly render the
        // message as cancelled and the (interrupted) label becomes visible.
        // Trailing tokens from the server are handled in the 'token' case handler which
        // now checks `status === 'interrupted'` FIRST and discards any trailing tokens
        // rather than creating a second placeholder or overwriting the interrupted status.
        return produce(b, (draft) => {
          const m = draft.messagesById[lastMsgId!]
          if (m) { m.isStreaming = false; m.status = 'interrupted' }
        }) as Partial<SessionChatState>
      })
      // If no streaming assistant message was found in the active bucket, search
      // all buckets. This handles scenarios where a message was appended to a
      // different bucket before the active session was set (e.g. in test scaffolding).
      const state = get()
      const activeBucket = sid ? state.sessionsById[sid] : undefined
      const hasInterruptedInActive = activeBucket && getMessages(activeBucket).some(
        (m: ChatMessage) => m.role === 'assistant' && m.status === 'interrupted'
      )
      if (!hasInterruptedInActive) {
        for (const [bucketSid, bucket] of Object.entries(state.sessionsById)) {
          if (bucketSid === sid) continue
          const lastMsgId = findLastAssistantMessageId(bucket.messageOrder, bucket.messagesById)
          if (lastMsgId && bucket.messagesById[lastMsgId].isStreaming) {
            // Update the background bucket AND sync the updated messages to the foreground
            // flat field so callers reading get().messages can see the change.
            useChatStore.setState((s) => {
              const b = s.sessionsById[bucketSid]
              if (!b) return {}
              const updatedById = { ...b.messagesById }
              const target = updatedById[lastMsgId!]
              if (target && target.role === 'assistant') {
                // #3: role guard ensures status:'interrupted' is only stamped onto
                // AssistantMessage where that status is legal per the discriminated union.
                // The outer loop already restricts lastMsgId to role:'assistant' entries,
                // so this guard is defence-in-depth against future refactors that could
                // break that invariant.
                updatedById[lastMsgId!] = { ...target, isStreaming: false, status: 'interrupted' } as ChatMessage
              }
              const updated: SessionChatState = { ...b, messagesById: updatedById }
              const updatedSessions = { ...s.sessionsById, [bucketSid]: updated }
              // Propagate to flat foreground so observers see the interrupted message.
              return {
                sessionsById: updatedSessions,
                messages: getMessages(updated),
              }
            })
            break
          }
        }
      }
      // This turn is over (interrupted) — send the next drained message, if any.
      maybeDrainNext()
    },

    startToolCall: (callId, tool, params) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        const lastMsgId = b.messageOrder[b.messageOrder.length - 1]
        const lastMsg = lastMsgId ? b.messagesById[lastMsgId] : undefined
        const textSnapshot = (lastMsg?.role === 'assistant' ? lastMsg.content : '') ?? ''
        return {
          toolCalls: {
            ...b.toolCalls,
            [callId]: { id: callId, call_id: callId, tool, params, status: 'running' },
          },
          toolCallOrder: [...b.toolCallOrder, callId],
          textAtToolCallStart: { ...b.textAtToolCallStart, [callId]: textSnapshot },
        }
      })
    },

    resolveToolCall: (callId, result, status, durationMs, error) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        if (!b.toolCalls[callId]) {
          console.debug('[chat] resolveToolCall for unknown call_id', callId)
          return {}
        }
        return {
          toolCalls: {
            ...b.toolCalls,
            [callId]: { ...b.toolCalls[callId], result, status, duration_ms: durationMs, error },
          },
        }
      })
    },

    cancelToolCall: (callId) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        if (!b.toolCalls[callId]) return {}
        return {
          toolCalls: {
            ...b.toolCalls,
            [callId]: { ...b.toolCalls[callId], status: 'cancelled' },
          },
        }
      })
    },

    startSpan: (frame) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        const lastMsgId = findLastAssistantMessageId(b.messageOrder, b.messagesById)
        if (!lastMsgId) return {}
        const span: SubagentSpanRunning = {
          spanId: frame.span_id,
          parentCallId: frame.parent_call_id,
          taskLabel: frame.task_label,
          status: 'running',
          steps: [],
          agentId: frame.agent_id,
        }
        const bufferKey = `${sid}:${frame.parent_call_id}`
        const buffered = pendingByParentCallId[bufferKey] ?? []
        delete pendingByParentCallId[bufferKey]
        if (orphanTimers[bufferKey]) {
          clearTimeout(orphanTimers[bufferKey])
          delete orphanTimers[bufferKey]
        }
        for (const { frame: bf } of buffered) {
          if (bf.type === 'tool_call_start') {
            span.steps.push({
              kind: 'tool',
              tool: {
                id: bf.call_id,
                call_id: bf.call_id,
                tool: bf.tool,
                params: bf.params,
                status: 'running',
              },
            })
          } else if (bf.type === 'tool_call_result') {
            const existingIdx = span.steps.findIndex(
              (s) => s.kind === 'tool' && s.tool.call_id === bf.call_id
            )
            if (existingIdx !== -1) {
              const existing = span.steps[existingIdx]
              if (existing.kind === 'tool') {
                span.steps[existingIdx] = {
                  kind: 'tool',
                  tool: {
                    ...existing.tool,
                    result: clampToolResult(bf.result),
                    status: bf.status,
                    duration_ms: bf.duration_ms,
                    error: bf.error,
                  },
                }
              }
            }
          }
        }
        const lastMsg = b.messagesById[lastMsgId]
        const spanIdx = (lastMsg.spans ?? []).length
        const updatedMsg = {
          ...lastMsg,
          spans: [...(lastMsg.spans ?? []), span],
        }
        // Record the span index for O(1) tool_call_result lookup.
        const spanByParentCallId = {
          ...b.spanByParentCallId,
          [frame.parent_call_id]: { messageId: lastMsgId, spanIdx },
        }
        return {
          messagesById: { ...b.messagesById, [lastMsgId]: updatedMsg },
          spanByParentCallId,
        }
      })
    },

    endSpan: (frame) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        return produce(b, (draft) => {
          for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
            const msgId = draft.messageOrder[i]
            const msg = draft.messagesById[msgId]
            if (msg.role !== 'assistant' || !msg.spans) continue
            const spanIdx = msg.spans.findIndex((s) => s.spanId === frame.span_id)
            if (spanIdx === -1) continue
            const existingSpan = msg.spans[spanIdx]
            const terminalSpan: SubagentSpanTerminal = {
              spanId: existingSpan.spanId,
              parentCallId: existingSpan.parentCallId,
              taskLabel: existingSpan.taskLabel,
              steps: existingSpan.steps,
              // Defensive fallback: SubagentEndFrame carries its own optional
              // agent_id; prefer it if the server ever populates it, else
              // keep the value already stamped by subagent_start.
              agentId: frame.agent_id ?? existingSpan.agentId,
              status: frame.status,
              durationMs: frame.duration_ms ?? 0,
              finalResult: frame.final_result,
              reason: frame.reason,
            }
            msg.spans[spanIdx] = terminalSpan
            // Clear the span index entry since the span is now terminal.
            delete draft.spanByParentCallId[existingSpan.parentCallId]
            return
          }
          console.warn('[chat] subagent_end received for unknown span_id', { spanId: frame.span_id })
          logDiagnostic('chatSubagentEndUnknownSpanId', { spanId: frame.span_id, sessionId: sid })
        }) as Partial<SessionChatState>
      })
    },

    attachStepToSpan: (parentCallId, step) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        // O(1) lookup first.
        const indexEntry = b.spanByParentCallId[parentCallId]
        if (indexEntry) {
          return produce(b, (draft) => {
            const msg = draft.messagesById[indexEntry.messageId]
            if (!msg?.spans) return
            const span = msg.spans[indexEntry.spanIdx]
            if (!span) return
            const existingIdx = span.steps.findIndex(
              (s) => s.kind === 'tool' && s.tool.call_id === step.call_id
            )
            if (existingIdx !== -1) {
              const existingStep = span.steps[existingIdx]
              if (existingStep.kind === 'tool') {
                span.steps[existingIdx] = { kind: 'tool', tool: { ...existingStep.tool, ...step } }
              }
            } else {
              span.steps.push({ kind: 'tool', tool: step })
            }
          }) as Partial<SessionChatState>
        }
        // Fallback: O(N) scan (legacy path, index miss).
        console.warn('[chat] attachStepToSpan: span index miss, falling back to O(N) scan', { parentCallId })
        logDiagnostic('chatAttachStepSpanIndexMiss', { parentCallId, sessionId: sid })
        for (let i = b.messageOrder.length - 1; i >= 0; i--) {
          const msgId = b.messageOrder[i]
          const msg = b.messagesById[msgId]
          if (msg.role !== 'assistant' || !msg.spans) continue
          const spanIdx = msg.spans.findIndex((s) => s.parentCallId === parentCallId)
          if (spanIdx === -1) continue
          return produce(b, (draft) => {
            const draftMsg = draft.messagesById[msgId]
            const span = draftMsg.spans![spanIdx]
            const existingIdx = span.steps.findIndex(
              (s) => s.kind === 'tool' && s.tool.call_id === step.call_id
            )
            if (existingIdx !== -1) {
              const existingStep = span.steps[existingIdx]
              if (existingStep.kind === 'tool') {
                span.steps[existingIdx] = { kind: 'tool', tool: { ...existingStep.tool, ...step } }
              }
            } else {
              span.steps.push({ kind: 'tool', tool: step })
            }
          }) as Partial<SessionChatState>
        }
        return {}
      })
    },

    updateSessionStats: (tokens, cost) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => ({
        sessionTokens: b.sessionTokens + tokens,
        sessionCost: b.sessionCost + cost,
      }))
    },

    seedSessionTokens: (total) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        // Only seed when bucket is fresh — don't overwrite live-accumulated totals.
        if (b.sessionTokens !== 0) return {}
        return { sessionTokens: total }
      })
    },

    setRateLimitEvent: (event) => {
      const sid = getActiveSid()
      if (!sid) return
      armRateLimitClear(sid, event)
    },

    clearRateLimitEvent: () => {
      const sid = getActiveSid()
      if (!sid) return
      if (rateLimitClearTimers[sid] != null) {
        clearTimeout(rateLimitClearTimers[sid])
        delete rateLimitClearTimers[sid]
      }
      withBucket(sid, () => ({ rateLimitEvent: null }))
    },

    resetSession: () => {
      const sid = getActiveSid()
      if (!sid) return
      // Clear orphan buffers for this session only.
      const prefix = `${sid}:`
      for (const key of Object.keys(orphanTimers)) {
        if (key.startsWith(prefix)) {
          clearTimeout(orphanTimers[key])
          delete orphanTimers[key]
        }
      }
      for (const key of Object.keys(pendingByParentCallId)) {
        if (key.startsWith(prefix)) {
          delete pendingByParentCallId[key]
        }
      }
      if (rateLimitClearTimers[sid] != null) {
        clearTimeout(rateLimitClearTimers[sid])
        delete rateLimitClearTimers[sid]
      }
      // Cancel any pending deferred replay-clear timer for this session, or it
      // later fires withBucket(sid, {isReplaying:false}) on the freshly-reset
      // bucket. Mirror the sibling timer maps above.
      if (replayingClearTimers[sid] != null) {
        clearTimeout(replayingClearTimers[sid])
        delete replayingClearTimers[sid]
      }
      sawReplayMessageThisTurn[sid] = false
      withBucket(sid, () => emptySessionState())
    },

    resetSessionForReplay: (sessionId) => {
      // Clear all transient state so the upcoming replay rebuilds from
      // scratch. This is the targeted reset for WS reconnect: without it,
      // replay frames append duplicate bubbles to the existing bucket.
      const prefix = `${sessionId}:`
      for (const key of Object.keys(orphanTimers)) {
        if (key.startsWith(prefix)) {
          clearTimeout(orphanTimers[key])
          delete orphanTimers[key]
        }
      }
      for (const key of Object.keys(pendingByParentCallId)) {
        if (key.startsWith(prefix)) {
          delete pendingByParentCallId[key]
        }
      }
      sawReplayMessageThisTurn[sessionId] = false
      replayingStartedAt[sessionId] = Date.now()
      // Re-attach refreshes the replay window — cancel any stale
      // setReplaying(false) timer left over from the previous attach so it
      // can't fire mid-window and prematurely re-enable the composer.
      if (replayingClearTimers[sessionId]) {
        clearTimeout(replayingClearTimers[sessionId])
        delete replayingClearTimers[sessionId]
      }
      withBucket(sessionId, () => ({
        ...emptySessionState(),
        isReplaying: true,
      }))
    },

    // ── Outbound queue actions ────────────────────────────────────────────────

    enqueueOutboundMessage: (content) => {
      const MAX_QUEUE = 5
      const current = get().outboundQueue
      if (current.length >= MAX_QUEUE) {
        return false
      }
      set({ outboundQueue: [...current, content] })
      return true
    },

    drainOutboundQueue: () => {
      const queue = get().outboundQueue
      if (queue.length === 0) return
      // BUG FIX (2026-07): used to `for`-loop `sendMessage` over the whole
      // queue synchronously — see the `maybeDrainNext` doc comment above for
      // why that silently dropped every message after the first. Instead,
      // hand the whole batch to `pendingDrainQueue` and let `maybeDrainNext`
      // send one at a time, re-triggered as each turn completes.
      //
      // Merge (not overwrite) `pendingDrainQueue`: if a previous drain cycle
      // is still mid-flight (e.g. the connection dropped again partway
      // through and a couple of its items got kicked back to `outboundQueue`
      // via sendMessage's disconnected-WS branch) this preserves whatever is
      // still queued from that cycle instead of silently discarding it.
      //
      // BUG FIX (2026-07, ordering regression): the bounced-back `queue` items
      // were previously appended AFTER whatever remained in
      // `pendingDrainQueue`. But anything still in `pendingDrainQueue` was
      // dequeued from the FRONT of a FIFO by `maybeDrainNext()` — so an item
      // still parked there was queued LATER (chronologically) than any item
      // that already made it out to `sendMessage` and bounced back. E.g.
      // A,B,C queued → drain sends A, pendingDrainQueue=[B,C] → mid-flight
      // disconnect frees B, which bounces back to `outboundQueue=[B]`,
      // leaving `pendingDrainQueue=[C]`. Appending (`[...pendingDrainQueue,
      // ...queue]`) produced [C,B] — C (typed later) sent before B (typed
      // earlier). Prepending the bounced queue instead restores the correct
      // chronological order: [B,C].
      set((state) => ({ outboundQueue: [], pendingDrainQueue: [...queue, ...state.pendingDrainQueue] }))
      maybeDrainNext()
    },

    // Validate outbound MessageFrame against the generated Zod schema before
    // we hand it to the WebSocket. We DO NOT block the send — that would
    // freeze the composer on the first contract drift. Instead we log +
    // bump the dev counter + emit a dev-mode toast (matches the inbound
    // parseFrameSafe pattern in src/lib/ws.ts). A future required wire
    // field would otherwise be silently omitted on the way out.
    _validateOutboundFrame: (payload: unknown, sessionId?: string | null) => {
      const result = MessageFrameSchema.safeParse(payload)
      if (result.success) return
      console.warn('[chat] outbound MessageFrame failed schema validation', result.error)
      // Focused 1-line Zod message (matches src/lib/ws.ts parseFrameSafe).
      // result.error.message is multi-line JSON; downstream consumers (the
      // dev-toast below and the production telemetry record) only need the
      // first failing field + its issue.
      const first = result.error.issues[0]
      const description = first
        ? `${first.path.join('.') || 'root'}: ${first.message}`
        : result.error.message
      logDiagnostic('chatOutboundFrameValidationFailed', { issue: description, sessionId })
      // Gate the dev-toast on MODE (not DEV) so the toast also fires in
      // Vitest's 'test' mode, which bakes DEV=false at compile time. MODE
      // is 'production' for shipped builds so the toast is suppressed
      // there. Without this gate change the W2-29 / W4-15 dev-toast test
      // is unreachable (it has always been — see pre-W4 history).
      if (import.meta.env.MODE !== 'production') {
        useUiStore.getState().addToast({
          message: `Outbound frame validation failed (dev): ${description}`,
          variant: 'error',
        })
      }
    },

    sendMessage: (content, opts) => {
      const mediaRefs = opts?.mediaRefs ?? []
      const attachments = opts?.attachments ?? []
      // Phase 1 / FR-010: per-turn model override. Trim and strip empty
      // strings so absent and "" are equivalent (the WS frame is omitted
      // entirely when no model was picked this session, per spec §18 Q3).
      const modelNameRaw = opts?.model_name
      const modelName = typeof modelNameRaw === 'string' ? modelNameRaw.trim() : ''

      // M4 workspace→turn binding (BLOCKER 1). When the user is chatting inside
      // a workspace, the active workspace id is the single source of truth in
      // useWorkspacesStore (set by WorkspaceTabContainer from the route param;
      // null on the global/inbox chat). We forward it as `metadata.workspace_id`
      // so the server stamps the session with this workspace and any task the
      // agent creates this turn (task_create / delegation) lands on THIS
      // workspace's board instead of the agent's default workspace. Absent (or
      // empty) when not in a workspace, matching the backend's default-workspace
      // fallback. The contract caps it at 128 chars; an over-length id is dropped
      // rather than sent (the outbound Zod validator would otherwise toast every
      // turn for a malformed local id).
      const activeWorkspaceIdRaw = useWorkspacesStore.getState().activeWorkspaceId
      const workspaceId =
        typeof activeWorkspaceIdRaw === 'string' &&
        activeWorkspaceIdRaw.length > 0 &&
        activeWorkspaceIdRaw.length <= 128
          ? activeWorkspaceIdRaw
          : ''

      // Merge model_name + workspace_id into a single `metadata` object so the
      // two payload build sites below stay in sync. The frame omits `metadata`
      // entirely when neither field is present (an empty object would be sent
      // otherwise, which the server treats the same but is noise on the wire).
      const metadata: { model_name?: string; workspace_id?: string } = {}
      if (modelName.length > 0) metadata.model_name = modelName
      if (workspaceId.length > 0) metadata.workspace_id = workspaceId
      const metadataFrame = Object.keys(metadata).length > 0 ? { metadata } : {}
      const { connection, isConnected } = useConnectionStore.getState()
      const { activeSessionId, activeAgentId } = useSessionStore.getState()
      const { isStreaming } = get()

      if (isStreaming) {
        useConnectionStore.getState().setConnectionError('Please wait — a response is still generating.')
        return
      }
      if (!connection || !isConnected) {
        // WS is disconnected — buffer the message for when the connection
        // recovers rather than losing it silently or showing a hard error.
        const enqueued = get().enqueueOutboundMessage(content)
        if (!enqueued) {
          useConnectionStore.getState().setConnectionError(
            'Queue full (5 messages max) — waiting to reconnect. Oldest pending messages will be sent first.'
          )
        }
        return
      }

      // When activeSessionId is null we do NOT render optimistically until
      // session_started arrives and gives us a real bucket key. This avoids
      // a temporary bucket that we'd have to migrate on the ack, at the cost
      // of ~1 round-trip of perceived latency on the very first message.
      if (activeSessionId !== null) {
        const userMsg: ChatMessage = {
          id: generateId(),
          session_id: activeSessionId,
          role: 'user',
          content,
          timestamp: new Date().toISOString(),
          status: 'done',
          ...(attachments.length > 0 ? { media: attachments } : {}),
        }
        const assistantMsg: ChatMessage = {
          id: generateId(),
          session_id: activeSessionId,
          role: 'assistant',
          content: '',
          timestamp: new Date().toISOString(),
          status: 'streaming',
          isStreaming: true,
        }

        withBucket(activeSessionId, (b) => {
          const msgs = getMessages(b)
          // Shares the single backward-scan implementation (findLastAssistantMessageId)
          // with every other "find last assistant message" call site in this store.
          // This site is the one exception that needs an array *index* (for the
          // finalMsgs[prevAssistantIdx] splice below) rather than an id, so it
          // resolves the id to an index via msgs.findIndex.
          const prevAssistantId = findLastAssistantMessageId(b.messageOrder, b.messagesById)
          const prevAssistantIdx = prevAssistantId !== null ? msgs.findIndex((m) => m.id === prevAssistantId) : -1
          let toolCallsAfterReset: typeof b.toolCalls = b.toolCalls
          let toolCallOrderAfterReset: string[] = b.toolCallOrder
          let finalMsgs = msgs

          if (prevAssistantIdx !== -1) {
            const prev = msgs[prevAssistantIdx]
            const alreadySeen = new Set((prev.tool_calls ?? []).map((tc) => tc.id))
            const liveIds = b.toolCallOrder.filter(
              (id) => !alreadySeen.has(id) && b.toolCalls[id],
            )
            if (liveIds.length > 0) {
              const baked = liveIds.map((id) => {
                const tc = b.toolCalls[id]
                return {
                  id,
                  tool: tc.tool,
                  params: tc.params,
                  result: tc.result,
                  status: tc.status,
                  duration_ms: tc.duration_ms,
                  error: tc.error,
                }
              })
              // Dedupe the merged tool_calls list by id so a re-bake (after
              // an attach + replay, or any other path that revisits live
              // ids) cannot leave duplicate ids on the message.
              const mergedById = new Map<string, NonNullable<typeof prev.tool_calls>[number]>()
              for (const tc of (prev.tool_calls ?? [])) mergedById.set(tc.id, tc)
              for (const tc of baked) mergedById.set(tc.id, tc)
              finalMsgs = [...msgs]
              // #3: prev is guaranteed assistant (prevAssistantIdx only set for
              // role:'assistant' entries above), so the role guard is defence-in-depth
              // to prevent tool_calls — which is illegal on UserMessage/SystemMessage —
              // being stamped if the invariant is ever broken by a future refactor.
              if (prev.role === 'assistant') {
                finalMsgs[prevAssistantIdx] = {
                  ...prev,
                  tool_calls: Array.from(mergedById.values()),
                } as ChatMessage
              }
              const liveSet = new Set(liveIds)
              const remainingCalls: typeof b.toolCalls = {}
              for (const [k, v] of Object.entries(b.toolCalls)) {
                if (!liveSet.has(k)) remainingCalls[k] = v
              }
              toolCallsAfterReset = remainingCalls
              toolCallOrderAfterReset = b.toolCallOrder.filter((id) => !liveSet.has(id))
            }
          }

          const allMsgs = [...finalMsgs, userMsg, assistantMsg]
          const msgArrayPatch = applyMessageArray(allMsgs, { ...b, toolCalls: toolCallsAfterReset, toolCallOrder: toolCallOrderAfterReset })
          return {
            ...msgArrayPatch,
            isStreaming: true,
            // H1-FE: record when user last sent a message so the unknown-sid
            // done handler can tell whether the active bucket is mid-stream.
            lastUserMessageAt: Date.now(),
          }
        })

        const payload = {
          type: 'message' as const,
          content,
          session_id: activeSessionId,
          agent_id: activeAgentId ?? undefined,
          ...(mediaRefs.length > 0 ? { media: mediaRefs } : {}),
          ...metadataFrame,
        }
        get()._validateOutboundFrame(payload, activeSessionId)
        const sent = connection.send(payload)

        // W2-7c: clear the nextModel slot AFTER the WS send so a failed
        // send leaves the user's pick intact (they can Retry without
        // having to re-pick the model). Clearing before the send would
        // silently lose the override on a transport rejection.
        if (sent) {
          set({ nextModel: null })
        }

        if (!sent) {
          // #253 (P0 data loss): the user turn must NEVER be silently dropped.
          // Previously this rollback deleted BOTH the user and assistant
          // bubbles, so a failed send wiped the message the user just typed.
          // Now we KEEP the user bubble (re-marked as 'error' so the UI shows
          // a failed state + Retry affordance) and only remove the empty
          // streaming assistant placeholder. The error is also surfaced.
          withBucket(activeSessionId, (b) => {
            return produce(b, (draft) => {
              // Drop the empty assistant placeholder.
              const aIdx = draft.messageOrder.indexOf(assistantMsg.id)
              if (aIdx !== -1) draft.messageOrder.splice(aIdx, 1)
              delete draft.messagesById[assistantMsg.id]
              // Keep the user message, but flag it as failed.
              const um = draft.messagesById[userMsg.id]
              if (um) { um.status = 'error' }
              draft.isStreaming = false
            }) as Partial<SessionChatState>
          })
          useConnectionStore.getState().setConnectionError('Message could not be sent — connection dropped. Your message was kept; press Retry to resend.')
          // The attempted turn is over (it never started) — free the next
          // drained message to send, if any.
          maybeDrainNext()
        }
      } else {
        // No active session — send without session_id; server will mint one
        // and ack with session_started.
        //
        // #253(a): Render the user message optimistically in a temporary bucket
        // so it is visible immediately. If the WS send fails, mark the message
        // with status:'error' so the user sees a Retry affordance instead of a
        // silent drop. The temporary bucket key '__pending' is replaced by the
        // real session_id once session_started arrives (see handleFrame case
        // 'session_started'). If the send succeeds, the message stays visible
        // until session_started migrates the bucket.
        const pendingSid = '__pending'
        const userMsg: ChatMessage = {
          id: generateId(),
          session_id: pendingSid,
          role: 'user',
          content,
          timestamp: new Date().toISOString(),
          status: 'done',
        }
        const assistantMsg: ChatMessage = {
          id: generateId(),
          session_id: pendingSid,
          role: 'assistant',
          content: '',
          timestamp: new Date().toISOString(),
          status: 'streaming',
          isStreaming: true,
        }
        // Render optimistically in the pending bucket and activate it.
        withBucket(pendingSid, (b) => {
          const allMsgs = [...getMessages(b), userMsg, assistantMsg]
          return { ...applyMessageArray(allMsgs, b), isStreaming: true, lastUserMessageAt: Date.now() }
        })
        useSessionStore.getState().setActiveSession(pendingSid, activeAgentId)

        const payload2 = {
          type: 'message' as const,
          content,
          agent_id: activeAgentId ?? undefined,
          ...(mediaRefs.length > 0 ? { media: mediaRefs } : {}),
          ...metadataFrame,
        }
        get()._validateOutboundFrame(payload2, pendingSid)
        const sent = connection.send(payload2)

        // W2-7c: see comment in the active-session branch above — we
        // clear AFTER the send so a transport rejection leaves the
        // pick in place for Retry.
        if (sent) {
          set({ nextModel: null })
        }
        if (!sent) {
          // #253(a): Mark the user message with status:'error' and remove the
          // optimistic assistant placeholder. This preserves the typed content
          // as a retriable error bubble rather than silently dropping the message.
          withBucket(pendingSid, (b) => {
            const msgs = getMessages(b).map((m) =>
              // #3: UserMessage allows status:'error'; cast is safe because userMsg was created
              // with role:'user'. The discriminated union prevents inline spread without cast.
              m.id === userMsg.id ? ({ ...m, status: 'error' as const } as ChatMessage) : m
            ).filter((m) => m.id !== assistantMsg.id)
            return { ...applyMessageArray(msgs, b), isStreaming: false }
          })
          useConnectionStore.getState().setConnectionError('Message could not be sent — connection dropped. Please try again.')
          // The attempted turn is over (it never started) — free the next
          // drained message to send, if any.
          maybeDrainNext()
        }
      }
  },

    cancelStream: () => {
      const { connection } = useConnectionStore.getState()
      const { activeSessionId } = useSessionStore.getState()
      const { isStreaming } = get()

      // FR-21 / T21–T25: always mark the last assistant message as interrupted
      // when the user explicitly invokes cancel (stop button, Escape, /cancel).
      // We do this BEFORE the isStreaming guard so that a stop-button click that
      // races a done frame still produces the (interrupted) label. Without this,
      // a turn that completes in <100ms after the stop button appears but before
      // Playwright (or a real user) clicks it would silently do nothing because
      // isStreaming flips to false between render and click.
      get().markLastMessageInterrupted()

      if (!connection) return
      if (!activeSessionId) {
        // No server-side session established yet — just clear local streaming state.
        withBucket(getActiveSid(), () => ({ isStreaming: false }))
        maybeDrainNext()
        return
      }

      if (isStreaming) {
        // Only send the cancel frame to the server if the turn is still active.
        // Sending cancel for a completed turn is a no-op on the server but wastes
        // a round-trip and may confuse the audit log.
        const sent = connection.send({ type: 'cancel', session_id: activeSessionId })
        if (!sent) {
          console.warn('[chat] cancelStream: send failed — connection may be closed')
          logDiagnostic('chatCancelStreamSendFailed', { sessionId: activeSessionId })
          useUiStore.getState().addToast({
            message: 'Could not send cancel — connection dropped. The response may continue briefly.',
            variant: 'error',
          })
        }
      }

      withBucket(activeSessionId, (b) => {
        const updated = { ...b.toolCalls }
        for (const key of Object.keys(updated)) {
          if (updated[key].status === 'running') {
            updated[key] = { ...updated[key], status: 'cancelled' }
          }
        }
        // cancelStage intentionally NOT reset here — hold label state until
        // the server sends the next cancel_stage frame or done/error clears it.
        // isStreaming is intentionally NOT set to false here. The done frame will
        // clear it. Clearing it here would cause the useEffect([isStreaming]) to
        // immediately reset stopLabel to 'stop', making the "Stopping..." button
        // disappear before the server confirms the cancel (T25). The done frame
        // arrives within a few seconds and performs the correct isStreaming:false
        // transition. markLastMessageInterrupted() above already set the message's
        // own isStreaming:false so AssistantUI renders it as incomplete/cancelled.
        return { toolCalls: updated }
      })
    },

    clearStreamingState: () => {
      // Sweep every bucket — not just the active one — because a background
      // session can be mid-stream when the socket drops. Any bucket left with
      // isStreaming=true would wedge if the user switches to it later.
      set((state) => {
        let mutated = false
        const sessionsById: Record<string, SessionChatState> = {}
        for (const [sid, bucket] of Object.entries(state.sessionsById)) {
          // Mark any still-streaming assistant message as done and flip any
          // running tool calls to cancelled so nothing renders as in-flight.
          const order = bucket.messageOrder
          let needsMsgFix = false
          for (let i = order.length - 1; i >= 0; i--) {
            const m = bucket.messagesById[order[i]]
            if (m?.role === 'assistant' && (m.isStreaming || m.status === 'streaming')) {
              needsMsgFix = true
              break
            }
          }
          // Gate on "is there anything to bake at all" (mirrors the `done` case's
          // `toolCallOrder.length > 0` gate/baking block below) rather than
          // "is something still running": a tool call flips to a terminal status
          // ('success'/'error') the instant its tool_call_result frame arrives,
          // which commonly happens before the trailing assistant text finishes
          // streaming. If the WS drops in that window the tool call is already
          // resolved but never baked — a status-'running' filter here would miss
          // it and let it silently vanish once isStreaming flips false.
          const hasPendingTools = bucket.toolCallOrder.length > 0
          if (!bucket.isStreaming && !needsMsgFix && !hasPendingTools && bucket.cancelStage === null) {
            sessionsById[sid] = bucket
            continue
          }
          mutated = true
          const next: SessionChatState = { ...bucket, isStreaming: false, cancelStage: null }
          if (needsMsgFix) {
            const messagesById = { ...bucket.messagesById }
            for (let i = order.length - 1; i >= 0; i--) {
              const m = messagesById[order[i]]
              if (m?.role === 'assistant' && (m.isStreaming || m.status === 'streaming')) {
                // Preserve an already-'interrupted' status; otherwise close as 'done'.
                messagesById[order[i]] = {
                  ...m,
                  isStreaming: false,
                  status: m.status === 'interrupted' ? 'interrupted' : 'done',
                } as ChatMessage
              }
            }
            next.messagesById = messagesById
          }
          if (hasPendingTools) {
            const toolCalls = { ...bucket.toolCalls }
            for (const key of Object.keys(toolCalls)) {
              if (toolCalls[key].status === 'running') {
                toolCalls[key] = { ...toolCalls[key], status: 'cancelled' }
              }
            }
            // Bake every pending tool call — not just the ones just flipped to
            // 'cancelled' above — into the last assistant message's tool_calls
            // array before clearing the live bucket state below. Terminal
            // ('success'/'error') calls need this too: otherwise they vanish
            // the instant isStreaming flips false, because the renderer
            // switches from the live toolCalls bucket to message.tool_calls at
            // that point (mirrors the `done` case's `toolCallOrder.length > 0`
            // gate/baking block below).
            const lastAssistantId = findLastAssistantMessageId(order, bucket.messagesById)
            const lastMsg = lastAssistantId ? next.messagesById[lastAssistantId] : undefined
            if (lastAssistantId && lastMsg?.role === 'assistant') {
              const baked = bucket.toolCallOrder
                .filter((id) => toolCalls[id])
                .map((id) => {
                  const tc = toolCalls[id]
                  return { id, tool: tc.tool, params: tc.params ?? {}, result: tc.result, status: tc.status, duration_ms: tc.duration_ms, error: tc.error }
                })
              const existing = lastMsg.tool_calls ?? []
              const mergedById = new Map(existing.map((tc) => [tc.id, tc]))
              for (const tc of baked) mergedById.set(tc.id, tc)
              next.messagesById = {
                ...next.messagesById,
                [lastAssistantId]: { ...lastMsg, tool_calls: Array.from(mergedById.values()) },
              }
            }
            next.toolCalls = {}
            next.toolCallOrder = []
            next.textAtToolCallStart = {}
          }
          sessionsById[sid] = next
        }
        if (!mutated) return {}
        const activeSid = getActiveSid()
        const fg = (activeSid ? sessionsById[activeSid] : null) ?? EMPTY_BUCKET
        return { sessionsById, ...bucketToForeground(fg) }
      })
      // The stream was just force-terminated (WS close/terminal error) — if a
      // drained message is waiting, try it now. If the connection is in fact
      // down, sendMessage's own disconnected-WS branch will put it back on
      // outboundQueue (visible in the UI) rather than leaving it stranded and
      // invisible in pendingDrainQueue.
      maybeDrainNext()
    },

    respondToPairing: (deviceId, decision) => {
      const { connection } = useConnectionStore.getState()
      if (!connection) {
        useConnectionStore.getState().setConnectionError('Cannot respond to pairing — not connected. Reconnect and try again.')
        return
      }

      const sent = connection.send({ type: 'device_pairing_response', device_id: deviceId, decision })
      if (!sent) {
        useConnectionStore.getState().setConnectionError('Failed to send pairing response — connection dropped. Reconnect and try again.')
      }
    },

    handleFrame: (frame) => {
      // Resolve which session this frame belongs to.
      // session_started is special: it carries the new id for the pending message.
      const frameSessionId = (frame as { session_id?: string }).session_id
      const activeSid = getActiveSid()

      // F-S1: Route to the correct bucket.
      // Session-scoped frames missing session_id are treated differently per environment.
      const targetSid: string | null = (() => {
        if (frame.type === 'session_started') return activeSid // handled below, value unused
        if (frameSessionId) return frameSessionId
        if (SESSION_SCOPED_FRAME_TYPES.has(frame.type)) {
          if (import.meta.env.MODE === 'test') {
            // In test mode: fall back to active session so test scaffolding stays simple.
            console.warn('[chat] frame missing session_id — routing to active session', { type: frame.type, activeSid })
            return activeSid
          }
          // In production: drop the frame and surface a one-shot connection error.
          console.error('[chat] server frame missing session_id — dropping', { type: frame.type })
          logDiagnostic('chatFrameMissingSessionId', { frameType: frame.type })
          useConnectionStore.getState().setConnectionError(
            'internal: server frame missing session_id — please reload'
          )
          return null
        }
        // Global frame (error, ping, pong, device_pairing_*, session_state) — use active.
        return activeSid
      })()

      const originalActiveSid = activeSid

      const store = get()

      // HIGH-2: reset unknown-frame counter on every known-good frame.
      unknownFrameCount = 0

      // I1: advance the reconnect `since` cursor for ANY frame that carries a
      // sequence timestamp — not only replay_message. The cursor is sent as
      // `since` on attach_session so the gateway skips frames the SPA already
      // saw; if it only advanced on replay_message, every replayed/live frame
      // that DID carry a timestamp would be re-replayed on the next reconnect.
      // advanceEventTime is monotonic (only moves forward), so this is safe to
      // run before the per-frame reducer regardless of dedup/early-return paths.
      {
        const frameTimestamp = (frame as { timestamp?: string }).timestamp
        if (frameTimestamp && targetSid) {
          withBucket(targetSid, (b) => ({
            lastReceivedEventTime: advanceEventTime(b.lastReceivedEventTime, frameTimestamp),
          }))
        }
      }

      switch (frame.type) {
        case 'session_started': {
          // Server minted a new session_id in response to a message sent without one.
          const newSid = frame.session_id
          // Register in session store and create the bucket.
          useSessionStore.getState().setActiveSession(newSid, frame.agent_id ?? useSessionStore.getState().activeAgentId)
          // Bucket is lazily created by first withBucket call; ensure it exists now
          // so the foreground syncs immediately.
          // FR-21 / T21–T25: session_started fires when the server begins a new turn
          // for a message sent without a session_id. The agent is about to stream —
          // pre-set isStreaming:true so the Stop button appears immediately without
          // waiting for the first token or tool_call_start frame.
          //
          // #253(a): If a '__pending' bucket exists (from the no-session optimistic
          // render path), migrate its messages into the real session bucket so the
          // user sees a continuous conversation rather than a blank slate + re-render.
          set((state) => {
            const pendingBucket = state.sessionsById['__pending']
            if (state.sessionsById[newSid]) {
              // Real bucket already exists — drop the pending bucket if present.
              if (!pendingBucket) return {}
              const sessionsById = { ...state.sessionsById }
              delete sessionsById['__pending']
              return { sessionsById }
            }
            // Migrate pending bucket messages into the new bucket, or start fresh.
            const baseBucket: SessionChatState = pendingBucket
              ? {
                  ...pendingBucket,
                  isStreaming: true,
                  lastUserMessageAt: Date.now(),
                }
              : { ...emptySessionState(), isStreaming: true }
            const sessionsById = { ...state.sessionsById, [newSid]: baseBucket }
            // Remove the temporary pending bucket.
            delete sessionsById['__pending']
            return { sessionsById, ...bucketToForeground(baseBucket) }
          })
          // Invalidate sessions list so SessionPanel shows the new session.
          queryClient.invalidateQueries({ queryKey: ['sessions'] })
          break
        }

        case 'token':
          if (targetSid) {
            withBucket(targetSid, (b) => {
              return produce(b, (draft) => {
                let lastMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                // FR-21 / T21–T26: if the last assistant message was already
                // interrupted (user clicked Stop / pressed Escape / used /cancel),
                // discard any trailing tokens the server sends before it processes
                // the cancel. markLastMessageInterrupted() sets isStreaming:false on
                // the message so that AssistantUI renders the correct "incomplete/cancelled"
                // status. We must NOT append to the interrupted message or create a new
                // placeholder — either would erase the (interrupted) label or create a
                // ghost streaming bubble without the label.
                if (lastMsgId && draft.messagesById[lastMsgId].status === 'interrupted') return
                // Only reuse the last assistant bubble if it is still
                // streaming. A closed bubble (status=done) means the prior
                // LLM call has finalized and any new tokens are part of a
                // *new* turn-segment — typically a follow-up call after a
                // tool returned. Stuffing them back into the closed bubble
                // is what produced the "text-then-image-at-bottom" ordering.
                if (lastMsgId && !draft.messagesById[lastMsgId].isStreaming) {
                  lastMsgId = null
                }
                if (!lastMsgId) {
                  const placeholder: ChatMessage = {
                    id: generateId(),
                    role: 'assistant',
                    content: '',
                    timestamp: new Date().toISOString(),
                    status: 'streaming',
                    isStreaming: true,
                    // Fix 5a: prefer the real producer's agent id (populated by the
                    // backend at token-emission time) over the client-side
                    // activeAgentId guess — the guess is wrong for background/
                    // delegated sub-turns where the true producer isn't "whoever
                    // the user happens to be chatting with". Falls back to the
                    // guess only for legacy/older frames that omit agent_id.
                    agentId: frame.agent_id ?? useSessionStore.getState().activeAgentId ?? undefined,
                  }
                  draft.messagesById[placeholder.id] = placeholder
                  draft.messageOrder.push(placeholder.id)
                  lastMsgId = placeholder.id
                }
                const msg = draft.messagesById[lastMsgId]
                // Fix 5a (cont.): the bubble being appended to here may be the
                // OPTIMISTIC placeholder created synchronously in sendMessage()
                // (which has no agentId yet — the true producer isn't known at
                // send time) rather than the one created above. Stamp/refresh the
                // attribution from the frame as soon as the backend tells us,
                // rather than only at placeholder-creation time — this is the
                // path that actually fires for the common "user message → agent
                // reply" case. Strict improvement: only overrides when the frame
                // carries agent_id; otherwise the existing value (possibly
                // undefined, falling back to activeAgentId at render time) is
                // left untouched — no behavior change for legacy frames.
                if (frame.agent_id) {
                  msg.agentId = frame.agent_id
                }
                msg.content = msg.content + frame.content
                msg.isStreaming = true
                msg.status = 'streaming'
                draft.isStreaming = true
              }) as Partial<SessionChatState>
            })
          }
          break

        case 'done':
          if (targetSid) {
            // B1.3d: when done arrives for a targetSid that isn't in sessionsById yet,
            // the session was probably switched away mid-stream. The active bucket's
            // isStreaming flag would otherwise stay true forever (infinite spinner).
            // Log a diagnostic warning and conditionally force-clear isStreaming on
            // the active bucket.
            //
            // H1-FE: Guard against corrupting an active mid-stream session.
            // Two cases where we must NOT force-clear the active bucket:
            //   1. targetSid === activeSid — the active session itself produced an
            //      unknown-sid done, which should never happen; the normal path below
            //      will handle it correctly, so do not fall through to the break.
            //   2. The active bucket sent a user message recently (< 10 s ago) and is
            //      still streaming — the done belongs to a different (wiped/replayed)
            //      session and the active spinner is legitimate.
            const knownSid = !!get().sessionsById[targetSid]
            if (!knownSid) {
              console.warn('chat.done_unknown_sid', { targetSid, activeSid: activeSid })
              logDiagnostic('chatDoneUnknownSid', { targetSid, activeSid })
              const STREAM_GRACE_MS = 10_000
              if (activeSid && activeSid !== targetSid && get().sessionsById[activeSid]) {
                const activeBucket = get().sessionsById[activeSid]!
                const isActiveMidStream =
                  activeBucket.isStreaming &&
                  activeBucket.lastUserMessageAt !== null &&
                  Date.now() - activeBucket.lastUserMessageAt < STREAM_GRACE_MS
                if (!isActiveMidStream) {
                  // Active bucket spinner is likely a stale remnant from the wiped
                  // session — safe to clear.
                  withBucket(activeSid, () => ({ isStreaming: false }))
                  maybeDrainNext()
                } else {
                  console.warn('chat.done_unknown_sid_skipped_active_mid_stream', {
                    targetSid,
                    activeSid,
                    lastUserMessageAt: activeBucket.lastUserMessageAt,
                  })
                  logDiagnostic('chatDoneUnknownSidSkippedActiveMidStream', {
                    targetSid,
                    activeSid,
                    lastUserMessageAt: activeBucket.lastUserMessageAt,
                  })
                }
              } else {
                // Defensive (boundary case, not an observed failure): no active
                // bucket to force-clear here — e.g. no active session at all, or
                // its bucket doesn't exist yet. isStreaming may already be false
                // via other means, but drain anyway so a message queued behind
                // this unknown-sid done can't get permanently stranded.
                maybeDrainNext()
              }
              break
            }

            // Decide whether isReplaying must clear now vs. defer to a setTimeout.
            // The clear happens INSIDE the same withBucket return below — never via
            // a nested withBucket call, because the outer set() commits the bucket
            // last and clobbers any nested writes that ran during the updater.
            const sid = targetSid
            const wasReplaying = (get().sessionsById[sid] ?? EMPTY_BUCKET).isReplaying
            const elapsed = wasReplaying ? Date.now() - (replayingStartedAt[sid] ?? 0) : 0
            // FR-I-014: mirror the same MIN_REPLAY_DISPLAY_MS used in setReplaying above.
            // Both code paths that clear isReplaying must use the same threshold.
            const MIN_REPLAY_DISPLAY_MS = 750
            const clearReplayingNow = wasReplaying && elapsed >= MIN_REPLAY_DISPLAY_MS
            if (wasReplaying) {
              sawReplayMessageThisTurn[sid] = false
              if (!clearReplayingNow) {
                if (replayingClearTimers[sid]) {
                  clearTimeout(replayingClearTimers[sid])
                }
                replayingClearTimers[sid] = setTimeout(() => {
                  delete replayingClearTimers[sid]
                  withBucket(sid, () => ({ isReplaying: false }))
                }, MIN_REPLAY_DISPLAY_MS - elapsed)
              }
            }
            withBucket(sid, (b) => {
              return produce(b, (draft) => {
                const lastMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                if (lastMsgId) {
                  // FR-21 / T21–T25: do NOT overwrite 'interrupted' status with 'done'.
                  const msg = draft.messagesById[lastMsgId]
                  msg.isStreaming = false
                  msg.status = msg.status === 'interrupted' ? 'interrupted' : 'done'
                }
                // Bake any pending tool calls into the last assistant message so
                // VirtualAssistantMessageRow can render them from message.tool_calls.
                // This covers two cases:
                //   1. Replay: replay_message coalesces into the empty placeholder and
                //      returns before baking; done is the only signal that all frames
                //      for the final entry are complete.
                //   2. Live turns: tool calls stay in toolCallOrder until the next
                //      sendMessage bakes them, causing them to disappear the moment
                //      isStreaming goes false and the message moves to the historical
                //      renderer (VirtualAssistantMessageRow reads message.tool_calls, not
                //      the bucket live map). Confirmed: ChatScreen.tsx switches to
                //      VirtualAssistantMessageRow at isStreaming=false, so baking at
                //      done is required for live turns too (hotfix/v0.1.1 aff2caa).
                if (lastMsgId && draft.toolCallOrder.length > 0) {
                  const baked = draft.toolCallOrder
                    .filter((id) => draft.toolCalls[id])
                    .map((id) => {
                      const tc = draft.toolCalls[id]
                      return { id, tool: tc.tool, params: tc.params ?? {}, result: tc.result, status: tc.status, duration_ms: tc.duration_ms, error: tc.error }
                    })
                  const lastMsg = draft.messagesById[lastMsgId]
                  const existing = lastMsg.tool_calls ?? []
                  const mergedById = new Map(existing.map((tc) => [tc.id, tc]))
                  for (const tc of baked) mergedById.set(tc.id, tc)
                  lastMsg.tool_calls = Array.from(mergedById.values())
                  draft.toolCalls = {}
                  draft.toolCallOrder = []
                  draft.textAtToolCallStart = {}
                }
                const tokenDelta = frame.stats?.tokens ?? 0
                const costDelta = frame.stats?.cost ?? 0
                draft.isStreaming = false
                draft.sessionTokens = draft.sessionTokens + tokenDelta
                draft.sessionCost = draft.sessionCost + costDelta
                draft.replayCompletedForSession = draft.isReplaying ? sid : draft.replayCompletedForSession
                draft.cancelStage = null
                if (clearReplayingNow) {
                  draft.isReplaying = false
                }
              }) as Partial<SessionChatState>
            })
            // The turn that just completed may be one we sent from the
            // offline-queue drain — send the next queued message, if any.
            maybeDrainNext()
          } else {
            // Defensive (boundary case, not an observed failure): a 'done'
            // frame with no session_id at all (a protocol-violating/malformed
            // frame) skips the whole block above, including the
            // maybeDrainNext() call inside it. isStreaming may already be
            // false via other means, but drain anyway so a message queued
            // behind this malformed done can't get permanently stranded.
            maybeDrainNext()
          }
          break

        case 'error':
          {
            // C8: a terminal error frame must always resolve the in-flight turn.
            // When the frame can't be routed to a bucket (no active session /
            // missing session_id in production), fall back to a global sweep so
            // no bucket is left wedged in a streaming state.
            if (!targetSid) {
              const isCancelAck = /turn.cancel/i.test(frame.message ?? '')
              if (!isCancelAck) {
                useConnectionStore.getState().setConnectionError(frame.message)
              }
              get().clearStreamingState()
              break
            }
            // An error frame arriving during replay must also clear isReplaying —
            // otherwise the session is permanently wedged behind the "Loading
            // session history…" overlay with a disabled composer, and (unlike the
            // done path) nothing else ever clears it. Mirror the done-frame logic:
            // clear immediately once MIN_REPLAY_DISPLAY_MS has elapsed, otherwise
            // defer to a timer (cancelling any stale one first).
            const sid = targetSid
            const wasReplaying = (get().sessionsById[sid] ?? EMPTY_BUCKET).isReplaying
            const replayElapsed = wasReplaying ? Date.now() - (replayingStartedAt[sid] ?? 0) : 0
            const MIN_REPLAY_DISPLAY_MS = 750
            const clearReplayingNow = wasReplaying && replayElapsed >= MIN_REPLAY_DISPLAY_MS
            if (wasReplaying) {
              sawReplayMessageThisTurn[sid] = false
              if (!clearReplayingNow) {
                if (replayingClearTimers[sid]) {
                  clearTimeout(replayingClearTimers[sid])
                }
                replayingClearTimers[sid] = setTimeout(() => {
                  delete replayingClearTimers[sid]
                  withBucket(sid, () => ({ isReplaying: false }))
                }, MIN_REPLAY_DISPLAY_MS - replayElapsed)
              }
            }
            withBucket(targetSid, (b) => {
              const lastMsgId = findLastAssistantMessageId(b.messageOrder, b.messagesById)
              if (lastMsgId) {
                const prevMsg = b.messagesById[lastMsgId]
                const prevStatus = prevMsg.status
                // FR-21 / T21–T23: do NOT overwrite 'interrupted' status with 'error'.
                const isCancelAck = /turn.cancel/i.test(frame.message ?? '')
                const resolvedStatus = (prevStatus === 'interrupted' || isCancelAck)
                  ? 'interrupted'
                  : 'error'
                return produce(b, (draft) => {
                  const msg = draft.messagesById[lastMsgId!]
                  msg.content = (resolvedStatus === 'interrupted')
                    ? msg.content
                    : (msg.content || frame.message)
                  msg.isStreaming = false
                  msg.status = resolvedStatus
                  draft.isStreaming = false
                  if (clearReplayingNow) {
                    draft.isReplaying = false
                  }
                }) as Partial<SessionChatState>
              }
              // No assistant message — push one. Only show an error toast for non-cancel errors.
              const isCancelAck = /turn.cancel/i.test(frame.message ?? '')
              if (!isCancelAck) {
                useConnectionStore.getState().setConnectionError(frame.message)
              }
              const errMsg: ChatMessage = {
                id: generateId(),
                role: 'assistant',
                content: isCancelAck ? '' : frame.message,
                timestamp: new Date().toISOString(),
                status: isCancelAck ? 'interrupted' : 'error',
                isStreaming: false,
              }
              const msgs = [...getMessages(b), errMsg]
              return {
                ...applyMessageArray(msgs, b),
                isStreaming: false,
                ...(clearReplayingNow ? { isReplaying: false } : {}),
              }
            })
            // The failed turn may have been one we sent from the offline-queue
            // drain — send the next queued message, if any.
            maybeDrainNext()
          }
          break

        case 'tool_call_start': {
          if (!targetSid) break
          const parentCallId = frame.parent_call_id
          if (parentCallId) {
            const b = get().sessionsById[targetSid] ?? emptySessionState()
            if (hasOpenSpanFast(b, parentCallId)) {
              // Temporarily patch active session for attachStepToSpan.
              if (targetSid === originalActiveSid) {
                store.attachStepToSpan(parentCallId, {
                  id: frame.call_id,
                  call_id: frame.call_id,
                  tool: frame.tool,
                  params: frame.params,
                  status: 'running',
                })
              } else {
                withBucket(targetSid, (bucket) => {
                  const entry = bucket.spanByParentCallId[parentCallId]
                  if (!entry) return {}
                  return produce(bucket, (draft) => {
                    const msg = draft.messagesById[entry.messageId]
                    if (!msg?.spans) return
                    const span = msg.spans[entry.spanIdx]
                    if (!span) return
                    span.steps.push({
                      kind: 'tool' as const,
                      tool: { id: frame.call_id, call_id: frame.call_id, tool: frame.tool, params: frame.params, status: 'running' as const },
                    })
                  }) as Partial<SessionChatState>
                })
              }
            } else {
              const bufferKey = `${targetSid}:${parentCallId}`
              bufferForSpan(bufferKey, frame, (buffered) => {
                console.warn(`[chat] orphan frame: parent_call_id="${parentCallId}" session="${targetSid}" — subagent_start never arrived within ${ORPHAN_BUFFER_TTL_MS}ms. Releasing as flat tool calls.`)
                logDiagnostic('chatOrphanFrameReleased', { parentCallId, sessionId: targetSid, ttlMs: ORPHAN_BUFFER_TTL_MS })
                useUiStore.getState().addToast({
                  variant: 'default',
                  message: 'Some subagent steps arrived without their span — displayed as flat tool calls',
                })
                withBucket(targetSid, (bucket) => {
                  let patchToolCalls = { ...bucket.toolCalls }
                  let patchOrder = [...bucket.toolCallOrder]
                  let patchText = { ...bucket.textAtToolCallStart }
                  let patchMsgs = getMessages(bucket)
                  for (const { frame: bf } of buffered) {
                    if (bf.type === 'tool_call_start') {
                      const lastMsg = patchMsgs[patchMsgs.length - 1]
                      const textSnapshot = (lastMsg?.role === 'assistant' ? lastMsg.content : '') ?? ''
                      if (!lastMsg || lastMsg.role !== 'assistant') {
                        const ph: ChatMessage = { id: generateId(), role: 'assistant', content: '', timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true, agentId: bf.agent_id ?? useSessionStore.getState().activeAgentId ?? undefined }
                        patchMsgs = [...patchMsgs, ph]
                      }
                      patchToolCalls[bf.call_id] = { id: bf.call_id, call_id: bf.call_id, tool: bf.tool, params: bf.params, status: 'running' }
                      patchOrder = [...patchOrder, bf.call_id]
                      patchText[bf.call_id] = textSnapshot
                    } else if (bf.type === 'tool_call_result') {
                      if (patchToolCalls[bf.call_id]) {
                        patchToolCalls[bf.call_id] = { ...patchToolCalls[bf.call_id], result: clampToolResult(bf.result), status: bf.status, duration_ms: bf.duration_ms, error: bf.error }
                      }
                    }
                  }
                  const msgArrayPatch = applyMessageArray(patchMsgs, { ...bucket, toolCalls: patchToolCalls, toolCallOrder: patchOrder })
                  return { ...msgArrayPatch, textAtToolCallStart: patchText }
                })
              })
            }
          } else {
            withBucket(targetSid, (b) => {
              const lastMsgId = b.messageOrder[b.messageOrder.length - 1]
              const lastMsg = lastMsgId ? b.messagesById[lastMsgId] : undefined
              const textSnapshot = (lastMsg?.role === 'assistant' ? lastMsg.content : '') ?? ''
              // Reconnect/replay safety: if this call_id is already recorded
              // (we have a textAtToolCallStart snapshot for it), keep the
              // ORIGINAL snapshot. A reattach replays from the start of the
              // transcript while the bucket already holds the completed
              // assistant text — without this guard every snapshot gets
              // overwritten with "end of full text", which makes the
              // runtime adapter render every tool call AFTER the text
              // (the "tool calls grouped at the bottom" reconnect bug).
              // Likewise, don't downgrade a tool call's status from
              // success/error back to running.
              const orderHasCall = b.toolCallOrder.includes(frame.call_id)
              const existingSnapshot = b.textAtToolCallStart[frame.call_id]
              const existingTC = b.toolCalls[frame.call_id]
              // FR-21 / T21–T25: a tool_call_start for a top-level (non-subagent)
              // tool means the agent is actively working — set isStreaming:true so
              // the Stop button appears even when the LLM emits a tool call without
              // streaming any text first (e.g. glm-5v-turbo immediately calling
              // write_file).  Do not set it during replay (b.isReplaying) because
              // replay frames reconstruct history and should not trigger the spinner.
              const shouldMarkStreaming = !b.isReplaying
              return produce(b, (draft) => {
                if (!lastMsg || lastMsg.role !== 'assistant') {
                  const ph: ChatMessage = { id: generateId(), role: 'assistant', content: '', timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true, agentId: frame.agent_id ?? useSessionStore.getState().activeAgentId ?? undefined }
                  draft.messagesById[ph.id] = ph
                  draft.messageOrder.push(ph.id)
                }
                if (shouldMarkStreaming) draft.isStreaming = true
                if (!existingTC || existingTC.status === 'running') {
                  draft.toolCalls[frame.call_id] = { id: frame.call_id, call_id: frame.call_id, tool: frame.tool, params: frame.params, status: 'running' }
                }
                if (!orderHasCall) draft.toolCallOrder.push(frame.call_id)
                if (existingSnapshot === undefined) {
                  draft.textAtToolCallStart[frame.call_id] = textSnapshot
                }
              }) as Partial<SessionChatState>
            })
          }
          break
        }

        case 'tool_call_result': {
          if (!targetSid) break
          const clampedResult = clampToolResult(frame.result)
          const parentCallId = frame.parent_call_id
          if (parentCallId) {
            const b = get().sessionsById[targetSid] ?? emptySessionState()
            if (hasOpenSpanFast(b, parentCallId)) {
              withBucket(targetSid, (bucket) => {
                const entry = bucket.spanByParentCallId[parentCallId]
                if (entry) {
                  return produce(bucket, (draft) => {
                    const msg = draft.messagesById[entry.messageId]
                    if (!msg?.spans) return
                    const span = msg.spans[entry.spanIdx]
                    if (!span) return
                    const step = { id: frame.call_id, call_id: frame.call_id, tool: frame.tool, params: {}, result: clampedResult, status: frame.status ?? 'success' as const, duration_ms: frame.duration_ms, error: frame.error }
                    const existingIdx = span.steps.findIndex((s) => s.kind === 'tool' && s.tool.call_id === frame.call_id)
                    if (existingIdx !== -1) {
                      const existingStep = span.steps[existingIdx]
                      if (existingStep.kind === 'tool') {
                        span.steps[existingIdx] = { kind: 'tool', tool: { ...existingStep.tool, ...step } }
                      }
                    } else {
                      span.steps.push({ kind: 'tool' as const, tool: step })
                    }
                  }) as Partial<SessionChatState>
                }
                // Fallback: O(N) scan (index miss — log a warning).
                console.warn('[chat] tool_call_result: span index miss, falling back to O(N) scan', { parentCallId, callId: frame.call_id })
                logDiagnostic('chatToolCallResultSpanIndexMiss', { parentCallId, callId: frame.call_id, sessionId: targetSid })
                for (let i = bucket.messageOrder.length - 1; i >= 0; i--) {
                  const msgId = bucket.messageOrder[i]
                  const msg = bucket.messagesById[msgId]
                  if (msg.role !== 'assistant' || !msg.spans) continue
                  const spanIdx = msg.spans.findIndex((s) => s.parentCallId === parentCallId)
                  if (spanIdx === -1) continue
                  return produce(bucket, (draft) => {
                    const draftMsg = draft.messagesById[msgId]
                    const span = draftMsg.spans![spanIdx]
                    const step = { id: frame.call_id, call_id: frame.call_id, tool: frame.tool, params: {}, result: clampedResult, status: frame.status ?? 'success' as const, duration_ms: frame.duration_ms, error: frame.error }
                    const existingIdx = span.steps.findIndex((s) => s.kind === 'tool' && s.tool.call_id === frame.call_id)
                    if (existingIdx !== -1) {
                      const existingStep = span.steps[existingIdx]
                      if (existingStep.kind === 'tool') {
                        span.steps[existingIdx] = { kind: 'tool', tool: { ...existingStep.tool, ...step } }
                      }
                    } else {
                      span.steps.push({ kind: 'tool' as const, tool: step })
                    }
                  }) as Partial<SessionChatState>
                }
                return {}
              })
            } else {
              const bufferKey = `${targetSid}:${parentCallId}`
              bufferForSpan(bufferKey, frame, (buffered) => {
                console.warn(`[chat] orphan frame: parent_call_id="${parentCallId}" session="${targetSid}" — subagent_start never arrived within ${ORPHAN_BUFFER_TTL_MS}ms. Releasing as flat tool calls.`)
                logDiagnostic('chatOrphanFrameReleased', { parentCallId, sessionId: targetSid, ttlMs: ORPHAN_BUFFER_TTL_MS })
                useUiStore.getState().addToast({
                  variant: 'default',
                  message: 'Some subagent steps arrived without their span — displayed as flat tool calls',
                })
                withBucket(targetSid, (bucket) => {
                  let patchToolCalls = { ...bucket.toolCalls }
                  let patchOrder = [...bucket.toolCallOrder]
                  let patchText = { ...bucket.textAtToolCallStart }
                  let patchMsgs = getMessages(bucket)
                  for (const { frame: bf } of buffered) {
                    if (bf.type === 'tool_call_start') {
                      const lastMsg = patchMsgs[patchMsgs.length - 1]
                      const textSnapshot = (lastMsg?.role === 'assistant' ? lastMsg.content : '') ?? ''
                      patchToolCalls[bf.call_id] = { id: bf.call_id, call_id: bf.call_id, tool: bf.tool, params: bf.params, status: 'running' }
                      patchOrder = [...patchOrder, bf.call_id]
                      patchText[bf.call_id] = textSnapshot
                    } else if (bf.type === 'tool_call_result') {
                      if (patchToolCalls[bf.call_id]) {
                        patchToolCalls[bf.call_id] = { ...patchToolCalls[bf.call_id], result: clampToolResult(bf.result), status: bf.status, duration_ms: bf.duration_ms, error: bf.error }
                      }
                    }
                  }
                  const msgArrayPatch = applyMessageArray(patchMsgs, { ...bucket, toolCalls: patchToolCalls, toolCallOrder: patchOrder })
                  return { ...msgArrayPatch, textAtToolCallStart: patchText }
                })
              })
            }
          } else {
            withBucket(targetSid, (b) => {
              if (!b.toolCalls[frame.call_id]) {
                console.debug('[chat] resolveToolCall for unknown call_id', frame.call_id)
                return {}
              }
              return produce(b, (draft) => {
                const tc = draft.toolCalls[frame.call_id]
                tc.result = clampedResult
                tc.status = frame.status ?? 'success'
                tc.duration_ms = frame.duration_ms
                tc.error = frame.error
              }) as Partial<SessionChatState>
            })
          }
          break
        }

        case 'subagent_start': {
          if (!targetSid) break
          const sf = frame as WsSubagentStartFrame
          withBucket(targetSid, (b) => {
            return produce(b, (draft) => {
              const lastMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
              if (!lastMsgId) return
              const span: SubagentSpanRunning = {
                spanId: sf.span_id,
                parentCallId: sf.parent_call_id,
                taskLabel: sf.task_label,
                status: 'running',
                steps: [],
                agentId: sf.agent_id,
              }
              const bufferKey = `${targetSid}:${sf.parent_call_id}`
              const buffered = pendingByParentCallId[bufferKey] ?? []
              delete pendingByParentCallId[bufferKey]
              if (orphanTimers[bufferKey]) {
                clearTimeout(orphanTimers[bufferKey])
                delete orphanTimers[bufferKey]
              }
              for (const { frame: bf } of buffered) {
                if (bf.type === 'tool_call_start') {
                  span.steps.push({ kind: 'tool', tool: { id: bf.call_id, call_id: bf.call_id, tool: bf.tool, params: bf.params, status: 'running' } })
                } else if (bf.type === 'tool_call_result') {
                  const existingIdx = span.steps.findIndex((s) => s.kind === 'tool' && s.tool.call_id === bf.call_id)
                  if (existingIdx !== -1) {
                    const existing = span.steps[existingIdx]
                    if (existing.kind === 'tool') {
                      span.steps[existingIdx] = { kind: 'tool', tool: { ...existing.tool, result: clampToolResult(bf.result), status: bf.status, duration_ms: bf.duration_ms, error: bf.error } }
                    }
                  }
                }
              }
              const lastMsg = draft.messagesById[lastMsgId]
              const spanIdx = (lastMsg.spans ?? []).length
              if (!lastMsg.spans) lastMsg.spans = []
              lastMsg.spans.push(span)
              draft.spanByParentCallId[sf.parent_call_id] = { messageId: lastMsgId, spanIdx }
            }) as Partial<SessionChatState>
          })
          break
        }

        case 'subagent_end': {
          if (!targetSid) break
          const ef = frame as WsSubagentEndFrame
          withBucket(targetSid, (b) => {
            return produce(b, (draft) => {
              for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
                const msgId = draft.messageOrder[i]
                const msg = draft.messagesById[msgId]
                if (msg.role !== 'assistant' || !msg.spans) continue
                const spanIdx = msg.spans.findIndex((s) => s.spanId === ef.span_id)
                if (spanIdx === -1) continue
                const existingSpan = msg.spans[spanIdx]
                const terminalSpan: SubagentSpanTerminal = {
                  spanId: existingSpan.spanId,
                  parentCallId: existingSpan.parentCallId,
                  taskLabel: existingSpan.taskLabel,
                  steps: existingSpan.steps,
                  // Defensive fallback: SubagentEndFrame carries its own optional
                  // agent_id; prefer it if the server ever populates it, else
                  // keep the value already stamped by subagent_start.
                  agentId: ef.agent_id ?? existingSpan.agentId,
                  status: ef.status,
                  durationMs: ef.duration_ms ?? 0,
                  finalResult: ef.final_result,
                  reason: ef.reason,
                }
                msg.spans[spanIdx] = terminalSpan
                delete draft.spanByParentCallId[existingSpan.parentCallId]
                return
              }
              console.warn('[chat] subagent_end received for unknown span_id', { spanId: ef.span_id })
              logDiagnostic('chatSubagentEndUnknownSpanId', { spanId: ef.span_id, sessionId: targetSid })
            }) as Partial<SessionChatState>
          })
          break
        }

        case 'task_status_changed':
          queryClient.invalidateQueries({ queryKey: ['tasks'] })
          break

        case 'agent_switched': {
          const newAgentId = frame.agent_id
          if (newAgentId) {
            const sessionStore = useSessionStore.getState()
            // Use the frame's session_id if present; fall back to active.
            const switchSid = frameSessionId ?? sessionStore.activeSessionId
            sessionStore.setActiveSession(switchSid, newAgentId)
          }
          queryClient.invalidateQueries({ queryKey: ['sessions'] })
          break
        }

        case 'replay_message': {
          if (!targetSid) break
          sawReplayMessageThisTurn[targetSid] = true
          const replayFrame = frame as WsReplayMessageFrame
          // FR-16 / Fix 5c: turn_canceled entries are metadata-only and must
          // never render as their own chat bubble. ReplayMessageFrame carries
          // no status/truncated field, so — unlike a fresh REST cold-load,
          // where the persisted TranscriptEntry.Status already says
          // "interrupted" — this WS-replay path only learns a turn was
          // cancelled from this separate turn_canceled entry, correlated by
          // TurnId to the specific assistant entry it interrupted (both
          // stamped from TranscriptEntry.TurnID by pkg/gateway/replay.go).
          // Find that message via turnId (captured below whenever an
          // assistant replay_message frame carries one) and mark it
          // interrupted the same way the live-cancel path
          // (markLastMessageInterrupted) does, so reload and live rendering
          // match — that parity is the entire point of this fix.
          if (replayFrame.role === 'turn_canceled') {
            const canceledTurnId = replayFrame.turn_id
            if (!canceledTurnId) {
              // Legacy/undecorated cancellation entry (no turn_id) — nothing
              // to correlate against. Drop gracefully rather than guessing at
              // "last assistant message", which could mis-mark an unrelated
              // turn when async delegation has interleaved other frames.
              console.warn('chat.turn_canceled_missing_turn_id', { sessionId: targetSid })
              logDiagnostic('chatTurnCanceledMissingTurnId', { sessionId: targetSid })
              break
            }
            withBucket(targetSid, (b) => {
              return produce(b, (draft) => {
                const matchId = findAssistantMessageIdByTurnId(draft.messageOrder, draft.messagesById, canceledTurnId)
                if (!matchId) {
                  // No assistant message in this bucket carries this turnId
                  // (e.g. evicted from the ring buffer, or replay delivered the
                  // cancellation before its assistant entry). No-op — never
                  // guess which message to mark.
                  console.warn('chat.turn_canceled_no_match', { sessionId: targetSid, turnId: canceledTurnId })
                  logDiagnostic('chatTurnCanceledNoMatch', { sessionId: targetSid, turnId: canceledTurnId })
                  return
                }
                const m = draft.messagesById[matchId]
                if (m) { m.isStreaming = false; m.status = 'interrupted' }
              }) as Partial<SessionChatState>
            })
            break
          }
          const role = (replayFrame.role || 'assistant') as 'user' | 'assistant'
          const text = replayFrame.content ?? ''
          const messageId = replayFrame.id
          const messageTimestamp = replayFrame.timestamp
          const replayAgentId = replayFrame.agent_id
          // Per-turn model record on replay. The field is optional on the wire;
          // we read it as `model?: string` so frames without it (legacy or non-
          // model-producing turns) still parse. Trim and treat empty/whitespace
          // as absent — matches the renderer trim guard.
          const replayModelRaw = replayFrame.model
          const replayModel = typeof replayModelRaw === 'string' ? replayModelRaw.trim() : ''
          // Fix 5c: turn-correlation id, stamped by the backend on assistant
          // replay entries so a later turn_canceled entry (handled above) can
          // find this exact message. Captured on the ChatMessage so it survives
          // for the lifetime of the bucket entry (until ring-buffer eviction).
          const replayTurnId = replayFrame.turn_id
          withBucket(targetSid, (b) => {
            return produce(b, (draft) => {
              // Cursor advancement is handled centrally before the switch (I1);
              // no per-case advance needed here.
              const msgs = getMessages(b)
              // Reconnection dedup: prefer server-assigned id match when present;
              // fall back to (content + role + timestamp) tuple. Content-only dedup
              // was silently dropping legitimate identical user retries.
              if (messageId) {
                if (draft.messageOrder.includes(messageId)) {
                  console.warn('chat.replay_dedup_skipped', { id: messageId, role, reason: 'id-match' })
                  logDiagnostic('chatReplayDedupSkipped', { messageId, role, reason: 'id-match', sessionId: targetSid })
                  return
                }
              } else {
                const tailId = draft.messageOrder[draft.messageOrder.length - 1]
                const tail = tailId ? draft.messagesById[tailId] : null
                const tailTs = tail?.timestamp ?? ''
                const frameTs = messageTimestamp ?? ''
                // Only dedup on content+role if timestamps also match (or both absent).
                if (
                  tail &&
                  tail.role === role &&
                  (tail.content ?? '') === text &&
                  (tailTs === frameTs || (tailTs === '' && frameTs === ''))
                ) {
                  console.warn('chat.replay_dedup_skipped', { role, reason: 'content-tuple-match' })
                  logDiagnostic('chatReplayDedupSkipped', { role, reason: 'content-tuple-match', sessionId: targetSid })
                  return
                }
              }
              // Coalesce assistant text into the trailing empty assistant bubble
              // that tool_call_start frames already created.
              if (role === 'assistant') {
                const lastMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                if (lastMsgId && (draft.messagesById[lastMsgId].content ?? '') === '') {
                  // Bake any tool calls that belong to this turn BEFORE taking the early
                  // return. Without this, toolCallOrder accumulates across turns and ends
                  // up baked onto the wrong (later) assistant message.
                  if (draft.toolCallOrder.length > 0) {
                    const baked = draft.toolCallOrder
                      .filter((id) => draft.toolCalls[id])
                      .map((id) => {
                        const tc = draft.toolCalls[id]
                        return { id, tool: tc.tool, params: tc.params ?? {}, result: tc.result, status: tc.status, duration_ms: tc.duration_ms, error: tc.error }
                      })
                    const existing = draft.messagesById[lastMsgId].tool_calls ?? []
                    const mergedById = new Map(existing.map((tc) => [tc.id, tc]))
                    for (const tc of baked) mergedById.set(tc.id, tc)
                    draft.messagesById[lastMsgId].tool_calls = Array.from(mergedById.values())
                    draft.toolCalls = {}
                    draft.toolCallOrder = []
                    draft.textAtToolCallStart = {}
                  }
                  const m = draft.messagesById[lastMsgId]
                  m.content = text
                  m.status = 'done'
                  m.isStreaming = false
                  if (replayAgentId) m.agentId = replayAgentId
                  // Stamp the per-turn model on the coalesced assistant turn (FR-014).
                  // Only set when the frame carried a non-empty model —
                  // legacy frames and non-model-producing turns stay
                  // model-less.
                  if (replayModel) m.model = replayModel
                  // Fix 5c: stamp the turn-correlation id so a later
                  // turn_canceled replay entry can find this exact message.
                  if (replayTurnId) m.turnId = replayTurnId
                  // Coalesce path: this empty placeholder was created by the
                  // turn's own tool_call_start frames, so any pending live tool
                  // calls belong to THIS assistant. Bake them in before the early
                  // return — otherwise toolCallOrder leaks into the next turn and
                  // all calls get attributed to the LAST assistant at `done`.
                  // Mirrors the non-coalesce bake path immediately below.
                  if (draft.toolCallOrder.length > 0) {
                    const baked = draft.toolCallOrder
                      .filter((id) => draft.toolCalls[id])
                      .map((id) => {
                        const tc = draft.toolCalls[id]
                        return { id, tool: tc.tool, params: tc.params ?? {}, result: tc.result, status: tc.status, duration_ms: tc.duration_ms, error: tc.error }
                      })
                    const existing = m.tool_calls ?? []
                    const mergedById = new Map(existing.map((tc) => [tc.id, tc]))
                    for (const tc of baked) mergedById.set(tc.id, tc)
                    m.tool_calls = Array.from(mergedById.values())
                    draft.toolCalls = {}
                    draft.toolCallOrder = []
                    draft.textAtToolCallStart = {}
                  }
                  return
                }
                // T1.10: Bake any live tool calls from the previous turn.
                if (lastMsgId && draft.toolCallOrder.length > 0) {
                  const baked = draft.toolCallOrder
                    .filter((id) => draft.toolCalls[id])
                    .map((id) => {
                      const tc = draft.toolCalls[id]
                      return { id, tool: tc.tool, params: tc.params ?? {}, result: tc.result, status: tc.status, duration_ms: tc.duration_ms, error: tc.error }
                    })
                  const lastMsg = draft.messagesById[lastMsgId]
                  const existing = lastMsg.tool_calls ?? []
                  const mergedById = new Map(existing.map((tc) => [tc.id, tc]))
                  for (const tc of baked) mergedById.set(tc.id, tc)
                  lastMsg.tool_calls = Array.from(mergedById.values())
                  draft.toolCalls = {}
                  draft.toolCallOrder = []
                  draft.textAtToolCallStart = {}
                }
              }
              const newMsg: ChatMessage = {
                id: messageId ?? generateId(),
                role,
                content: text,
                timestamp: messageTimestamp ?? new Date().toISOString(),
                status: 'done' as const,
                ...(replayAgentId ? { agentId: replayAgentId } : {}),
                // Per-turn model record. Only on assistant messages (user/system turns
                // don't carry a producer model). Empty model is treated as
                // absent so the renderer doesn't show a phantom footer.
                ...(replayModel && role === 'assistant' ? { model: replayModel } : {}),
                // Fix 5c: turn-correlation id. Only meaningful on assistant
                // messages (the backend stamps turn_id on assistant + turn-
                // cancellation entries only) — lets a later turn_canceled
                // replay entry find this exact message.
                ...(replayTurnId && role === 'assistant' ? { turnId: replayTurnId } : {}),
              }
              draft.messagesById[newMsg.id] = newMsg
              draft.messageOrder.push(newMsg.id)
              // Ring buffer enforcement during replay — evict oldest entry plus all dependent maps.
              if (draft.messageOrder.length > MAX_MESSAGES_PER_SESSION) {
                const evictId = draft.messageOrder[0]
                evictMessageFromBucket(draft as unknown as SessionChatState, evictId)
                draft.trimmedCount += 1
              }
              void msgs // suppress unused warning — only used for dedup context above
            }) as Partial<SessionChatState>
          })
          break
        }

        case 'media': {
          if (!targetSid) break
          if (!Array.isArray(frame.parts) || frame.parts.length === 0) {
            console.warn('[chat] Received media frame with empty or invalid parts — appending notice')
            logDiagnostic('chatMediaFrameInvalidParts', { sessionId: targetSid })
            withBucket(targetSid, (b) => {
              return produce(b, (draft) => {
                const lastMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                if (lastMsgId) {
                  const msg = draft.messagesById[lastMsgId]
                  msg.content = (msg.content ?? '') + (msg.content ? '\n\n' : '') + '_1 attachment could not be displayed._'
                }
              }) as Partial<SessionChatState>
            })
            break
          }
          const attachments: MediaAttachment[] = frame.parts
            .filter((p) => p.url && p.type)
            .map((p) => ({
              type: p.type,
              url: p.url,
              filename: p.filename,
              contentType: p.content_type,
              caption: p.caption,
            }))
          if (attachments.length === 0) {
            withBucket(targetSid, (b) => {
              return produce(b, (draft) => {
                const lastMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                if (lastMsgId) {
                  const msg = draft.messagesById[lastMsgId]
                  msg.content = (msg.content ?? '') + (msg.content ? '\n\n' : '') +
                    `_${frame.parts.length} attachment${frame.parts.length > 1 ? 's' : ''} could not be displayed._`
                }
              }) as Partial<SessionChatState>
            })
            break
          }
          withBucket(targetSid, (b) => {
            return produce(b, (draft) => {
              const lastMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
              const dedupe = (existing: MediaAttachment[] | undefined, incoming: MediaAttachment[]) => {
                const seen = new Set((existing ?? []).map((a) => a.url))
                const fresh = incoming.filter((a) => !seen.has(a.url))
                return [...(existing ?? []), ...fresh]
              }
              if (lastMsgId) {
                const msg = draft.messagesById[lastMsgId]
                const canAttach = msg.isStreaming || (msg.content ?? '') === ''
                if (canAttach) {
                  msg.media = dedupe(msg.media, attachments)
                  return
                }
              }
              const newMsg: ChatMessage = {
                id: generateId(),
                role: 'assistant',
                content: '',
                timestamp: new Date().toISOString(),
                media: attachments,
              }
              draft.messagesById[newMsg.id] = newMsg
              draft.messageOrder.push(newMsg.id)
            }) as Partial<SessionChatState>
          })
          break
        }

        case 'rate_limit': {
          const rlFrame = frame as WsRateLimitFrame
          const sid = targetSid ?? getActiveSid()
          if (!sid) break
          const event: RateLimitEventData = {
            scope: rlFrame.scope,
            resource: rlFrame.resource,
            policyRule: rlFrame.policy_rule,
            retryAfterSeconds: rlFrame.retry_after_seconds,
            agentId: rlFrame.agent_id,
            tool: rlFrame.tool,
          }
          armRateLimitClear(sid, event)
          break
        }
        case 'whatsapp_pairing': {
          // #283: global (not session-tied) — record QR/status for the Channels
          // config panel. Accessed via getState() at frame time (not a hook
          // subscription) so chatStore stays decoupled from the pairing store.
          useWhatsAppPairingStore
            .getState()
            .apply(frame as WhatsAppPairingFrame)
          break
        }

        case 'notification': {
          // #264: global (not session-tied) — push into the dedicated
          // Notifications store backing the header notification center. Mirrors
          // the #283 whatsapp_pairing case: accessed via getState() at frame
          // time so chatStore stays decoupled from the notifications store.
          useNotificationsStore.getState().apply(frame as NotificationFrame)
          break
        }

        case 'tool_approval_required':
          useToolApprovalStore.getState().enqueue(frame)
          break

        case 'session_state':
          useToolApprovalStore.getState().reconcileWithSessionState(frame)
          break

        case 'system_overload':
          useUiStore.getState().addToast({
            message: frame.message ?? 'System at capacity — agent action blocked. Retry shortly.',
            variant: 'warning',
          })
          break

        case 'replay_warning':
          // Gateway detected duplicate tool_call_ids in the transcript on
          // replay. Server-only slog.Warn was invisible to operators because
          // the count was buried in done.Stats. One-shot toast surfaces it.
          useUiStore.getState().addToast({
            message: frame.message,
            variant: 'warning',
          })
          break

        case 'cancel_stage':
          // B3: gateway is broadcasting cancel progress for this session.
          // Write the stage into the per-session bucket so the UI can update
          // the stop-button label in real time. The done handler (above) clears
          // it back to null once the turn is definitively over.
          withBucket(targetSid, () => ({ cancelStage: frame.stage }))
          break

        case 'device_pairing_request':
          // I2: a new device is requesting pairing approval. DevicesSection
          // polls the ['devices'] query while open; invalidating it surfaces the
          // new pending request immediately instead of waiting for the next poll.
          queryClient.invalidateQueries({ queryKey: ['devices'] })
          break

        default:
          unknownFrameCount++
          console.warn('[chat] Unknown frame type', { type: (frame as { type?: string }).type, count: unknownFrameCount })
          logDiagnostic('chatUnknownFrameType', { frameType: (frame as { type?: string }).type, count: unknownFrameCount })
          if (unknownFrameCount >= UNKNOWN_FRAME_TOAST_THRESHOLD) {
            useUiStore.getState().addToast({
              message: "Server is sending events this UI doesn't understand — refresh to update.",
              variant: 'warning',
            })
            // Reset so we don't spam the toast on every subsequent unknown frame.
            unknownFrameCount = 0
          }
          break
      }

      // After processing a frame for the foreground session, re-sync foreground fields
      // in case withBucket targeted a non-foreground session (background sessions).
      // When the target was foreground, withBucket already synced; this call is idempotent.
      syncForeground()
    },
  }
})

// Expose syncForeground so setActiveSession can call it after switching sessions.
// Avoiding a direct import of the session store here to keep the cycle-break intact.
export function syncChatForeground(): void {
  // Re-read active session from the session store and sync foreground fields.
  const activeSid = useSessionStore.getState().activeSessionId ?? FALLBACK_SID
  useChatStore.setState((state) => {
    const fg = (activeSid ? state.sessionsById[activeSid] : null) ?? EMPTY_BUCKET
    // Project messagesById+messageOrder → messages for foreground consumers.
    const { messagesById: _mb, messageOrder: _mo, trimmedCount: _tc, spanByParentCallId: _sp, ...rest } = fg
    return { ...rest, messages: getMessages(fg) }
  })
}

// Register callbacks with the session store to break circular imports.
registerChatSetReplaying((value) => useChatStore.getState().setReplaying(value))
registerChatResetForReplay((sessionId) => useChatStore.getState().resetSessionForReplay(sessionId))
registerSyncChatForeground(syncChatForeground)


// F-S8: removed flat→bucket bidirectional sync subscriber.
// Tests now seed sessionsById directly (see resetStores() in test files).
// The subscriber was only needed for test scaffolding that set messages on the flat state;
// that pattern is no longer used.

// Detect direct useSessionStore.setState({activeSessionId: ...}) bypasses (used in tests).
// We intentionally do NOT auto-sync foreground here because it would overwrite flat fields
// (like isStreaming) that tests set directly before switching sessions. Foreground sync
// happens only through the store actions (setActiveSession, attachToSession, startNewSession).
// This comment documents the intentional gap for future maintainers.
