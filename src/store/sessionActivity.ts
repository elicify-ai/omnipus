// sessionActivity.ts — FE-5 Agent-View session-list data store.
//
// ADR-053 §6 "Mid-span subagent frames" + design §4 (H-1..H-6 Agent-View).
// The ActivityPanel's open/close `subagent_start`/`subagent_end` span
// brackets grow into a LIVE SESSION LIST: each delegated (child) session
// shows its durable lifecycle state (8-state, SessionLifecycleRecord) plus
// its latest typed SessionMessage pings (progress / checkpoint / artifact /
// blocker / question / decision_request / handback), with peek / reply /
// steer / stop affordances.
//
// Mid-span `subagent_message` / `subagent_state` frames (the flat UI
// projections defined in SubagentMessageFrame.yaml / SubagentStateFrame.yaml)
// ride the existing `subagent_start`/`subagent_end` bracket channel and
// UPDATE THE ROW IN PLACE — this store holds them keyed by `span_id`, and
// useRunningActivity joins them onto the matching AgentActivityItem.
//
// INGRESS PATTERN: `chat.ts`'s `handleFrame` forwards `subagent_message` and
// `subagent_state` here via `useSessionActivityStore.getState().apply(frame)`
// — the established frame-driven-store precedent (mirrors
// `judgeActivity.ts` / `whatsappPairing.ts` / `notifications`). This store
// never touches the chat buckets; it is consumed only by the ActivityPanel
// session-list surface.

import { create } from 'zustand'
import type {
  SubagentMessageFrame,
  SubagentStateFrame,
} from '@/lib/api/generated/asyncapi-types'

/**
 * The 8-state durable lifecycle enum (SessionLifecycleRecord.state). Re-used
 * verbatim from the wire type's `state` field so this never drifts from the
 * contract. A `SubagentStateFrame.state` is the live projection of this same
 * authority. Re-exported for the session-list UI.
 */
export type SessionLifecycleState = SubagentStateFrame['state']

/** A single child→parent (or steer/respond) message projection row. */
export interface SessionMessageRow {
  messageId: string
  kind: SubagentMessageFrame['kind']
  text?: string
  pct?: number
  correlationId?: string
  senderIdentity: string
  untrustedOrigin: boolean
  createdAt: string
}

/** The latest durable lifecycle state + steering receipt for one span. */
export interface SpanSessionState {
  /** The 8-state lifecycle value. */
  state: SessionLifecycleState
  /** Durable session id the ping reported (the child's session). */
  sessionId: string
  /** Span id — joins onto SubagentSpan.spanId / AgentActivityItem.key. */
  spanId: string
  /** RFC3339 timestamp the state ping was emitted. */
  updatedAt: string
  /** Present when this ping acknowledges an applied steer/respond (INV-3). */
  steeringReceipt?: { correlationId: string; appliedAt: string }
}

/** Cap per-span retained message rows (most-recent-last). Bounds memory. */
const MSG_CAP_PER_SPAN = 50
/** Cap the overall span map (FIFO eviction across the whole store). */
const SPAN_CAP = 64

interface SessionActivityStore {
  /** spanId -> message rows (most-recent-last, capped per span). */
  messagesBySpan: Record<string, SessionMessageRow[]>
  /** spanId -> latest lifecycle state (last-write-wins by updatedAt). */
  stateBySpan: Record<string, SpanSessionState>
  /** Apply a mid-span frame (message or state) — the chat.ts forwarding target. */
  apply: (frame: SubagentMessageFrame | SubagentStateFrame) => void
  applyMessage: (frame: SubagentMessageFrame) => void
  applyState: (frame: SubagentStateFrame) => void
  /** Drop a span's data when its subagent_end lands (terminal — detail stays on the ActivityItem). */
  forgetSpan: (spanId: string) => void
  /** Test-only reset hook. */
  reset: () => void
}

/** FIFO-evict the oldest span entry when the span cap is exceeded. */
function evictOldestSpan(
  messagesBySpan: Record<string, SessionMessageRow[]>,
  stateBySpan: Record<string, SpanSessionState>,
): void {
  const spanIds = Object.keys(messagesBySpan)
  if (spanIds.length <= SPAN_CAP) return
  // Oldest = smallest max(updatedAt / last message createdAt). Fall back to
  // insertion order via a stable scan if timestamps tie.
  let oldest = spanIds[0]
  let oldestTs = stateBySpan[oldest]?.updatedAt
    ?? messagesBySpan[oldest]?.at(-1)?.createdAt
    ?? ''
  for (const id of spanIds) {
    const ts = stateBySpan[id]?.updatedAt ?? messagesBySpan[id]?.at(-1)?.createdAt ?? ''
    if (ts < oldestTs) {
      oldest = id
      oldestTs = ts
    }
  }
  delete messagesBySpan[oldest]
  delete stateBySpan[oldest]
}

export const useSessionActivityStore = create<SessionActivityStore>((set) => ({
  messagesBySpan: {},
  stateBySpan: {},
  apply: (frame) => {
    if (frame.type === 'subagent_message') {
      set((s) => applyMessageImpl(s, frame))
    } else {
      set((s) => applyStateImpl(s, frame))
    }
  },
  applyMessage: (frame) => set((s) => applyMessageImpl(s, frame)),
  applyState: (frame) => set((s) => applyStateImpl(s, frame)),
  forgetSpan: (spanId) =>
    set((s) => {
      if (!(spanId in s.messagesBySpan) && !(spanId in s.stateBySpan)) return s
      const messagesBySpan = { ...s.messagesBySpan }
      const stateBySpan = { ...s.stateBySpan }
      delete messagesBySpan[spanId]
      delete stateBySpan[spanId]
      return { messagesBySpan, stateBySpan }
    }),
  reset: () => set({ messagesBySpan: {}, stateBySpan: {} }),
}))

function applyMessageImpl(
  s: Pick<SessionActivityStore, 'messagesBySpan' | 'stateBySpan'>,
  frame: SubagentMessageFrame,
) {
  const spanId = frame.span_id
  const existing = s.messagesBySpan[spanId] ?? []
  // De-dupe by message_id — the wire is at-least-once; a WS reconnect can
  // re-deliver the same ping via since-cursor replay.
  if (existing.some((m) => m.messageId === frame.message_id)) return s
  const row: SessionMessageRow = {
    messageId: frame.message_id,
    kind: frame.kind,
    text: frame.text,
    pct: frame.pct,
    correlationId: frame.correlation_id,
    senderIdentity: frame.sender_identity,
    untrustedOrigin: frame.untrusted_origin,
    createdAt: frame.created_at,
  }
  const next = [...existing, row].slice(-MSG_CAP_PER_SPAN)
  const messagesBySpan = { ...s.messagesBySpan, [spanId]: next }
  // Touch the span map so a span with only messages (no state ping yet) is
  // still tracked for eviction accounting.
  if (!(spanId in messagesBySpan)) {
    evictOldestSpan(messagesBySpan, s.stateBySpan)
  }
  return { messagesBySpan }
}

function applyStateImpl(
  s: Pick<SessionActivityStore, 'messagesBySpan' | 'stateBySpan'>,
  frame: SubagentStateFrame,
) {
  const spanId = frame.span_id
  const prev = s.stateBySpan[spanId]
  // Last-write-wins by createdAt (monotonic wire order); ignore an
  // out-of-order stale ping (older than what we already have).
  if (prev && prev.updatedAt > frame.created_at) return s
  const next: SpanSessionState = {
    state: frame.state,
    sessionId: frame.session_id,
    spanId,
    updatedAt: frame.created_at,
    steeringReceipt:
      frame.steering_receipt != null
        ? {
            correlationId: frame.steering_receipt.correlation_id,
            appliedAt: frame.steering_receipt.applied_at,
          }
        : prev?.steeringReceipt,
  }
  const stateBySpan = { ...s.stateBySpan, [spanId]: next }
  if (!(spanId in s.messagesBySpan) && !(spanId in stateBySpan)) {
    evictOldestSpan(s.messagesBySpan, stateBySpan)
  }
  return { stateBySpan }
}
