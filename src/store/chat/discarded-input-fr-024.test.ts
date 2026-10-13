// FR-024 / U7 (docs/internal/specs/session-core-spec.md, BDD-07.3/BDD-09.4):
// "Show discarded/not delivered live/reload and never later consumed". The
// LIVE path already renders the discarded state (the `message_status` frame's
// `state: 'discarded'` → ChatMessage.deliveryStatus, frames.ts's
// applyMessageStatusFrame → ConnectionStatus.tsx's quiet "Not delivered").
// These tests pin the MISSING half: the SAME state after a reload/reconnect,
// where the stored entry reaches the SPA as a `replay_message` WS frame
// carrying the read-only `input_disposition` marker
// (contracts/components/schemas/ReplayMessageFrame.yaml).
//
// The real frame handler runs (useChatStore.handleFrame) — only the WS
// connection is stubbed by construction (frames are handed straight to the
// reducer, exactly like disconnect-catchup-regressions.test.ts).

import { beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import type { ServerFrame } from '@/lib/ws'

const SID = 'sess-discarded-fr024'

function userBubbles(sid: string = SID) {
  const b = useChatStore.getState().sessionsById[sid]
  if (!b) return []
  return b.messageOrder.map((id) => b.messagesById[id]).filter((m) => m.role === 'user')
}

beforeEach(() => {
  useSessionStore.setState({ activeSessionId: SID })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  useConnectionStore.setState({ connection: null } as never)
})

describe('FR-024 — replay_message maps input_disposition onto the bubble deliveryStatus', () => {
  it('a replayed user entry discarded by Stop mints its bubble as deliveryStatus "discarded", text kept', () => {
    useChatStore.getState().handleFrame({
      type: 'replay_message',
      session_id: SID,
      id: 'hist-disc-1',
      role: 'user',
      content: 'summarize the quarter',
      timestamp: '2026-10-11T10:00:00Z',
      input_disposition: {
        message_id: 'hist-disc-1',
        state: 'discarded',
        reason: 'stopped_before_delivery',
      },
    } as ServerFrame)

    const bubbles = userBubbles()
    expect(bubbles).toHaveLength(1)
    // Same quiet state the live message_status frame stamps — "Not delivered",
    // never an error bubble, and the archived text stays.
    expect(bubbles[0].deliveryStatus).toBe('discarded')
    expect(bubbles[0].content).toBe('summarize the quarter')
    expect(bubbles[0].status).toBe('done')
  })

  it('a replayed user entry WITHOUT input_disposition keeps today’s rendering — no deliveryStatus', () => {
    useChatStore.getState().handleFrame({
      type: 'replay_message',
      session_id: SID,
      id: 'hist-plain-1',
      role: 'user',
      content: 'an ordinary delivered message',
      timestamp: '2026-10-11T10:01:00Z',
    } as ServerFrame)

    const bubbles = userBubbles()
    expect(bubbles).toHaveLength(1)
    expect(bubbles[0].deliveryStatus).toBeUndefined()
    expect(bubbles[0].content).toBe('an ordinary delivered message')
  })

  it('live discarded, then reload: the replayed stored entry still shows discarded (never silently received)', () => {
    // — Live: the tab sent the message (optimistic bubble keyed by the client
    // id), the user hit Stop, and the live message_status frame marked it.
    useChatStore.setState((s) => ({
      sessionsById: {
        ...s.sessionsById,
        [SID]: {
          ...(s.sessionsById[SID] ?? ({} as never)),
          messageOrder: ['client-live-1'],
          messagesById: {
            'client-live-1': { id: 'client-live-1', role: 'user', content: 'summarize the quarter', timestamp: '2026-10-11T10:00:00Z', deliveryStatus: 'sending' },
          },
        },
      },
    }) as never)
    useChatStore.getState().handleFrame({
      type: 'message_status',
      session_id: SID,
      client_message_id: 'client-live-1',
      state: 'discarded',
      reason: 'stopped_before_delivery',
    } as ServerFrame)
    expect(userBubbles()[0].deliveryStatus).toBe('discarded')

    // — Reload: the in-memory bucket is gone; the attach replay delivers the
    // stored entry (server id) with the input_disposition marker.
    useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
    useChatStore.getState().handleFrame({
      type: 'replay_message',
      session_id: SID,
      id: 'server-id-1',
      client_message_id: 'client-live-1',
      role: 'user',
      content: 'summarize the quarter',
      timestamp: '2026-10-11T10:00:00Z',
      input_disposition: {
        message_id: 'server-id-1',
        client_message_id: 'client-live-1',
        state: 'discarded',
        reason: 'stopped_before_delivery',
      },
    } as ServerFrame)

    const bubbles = userBubbles()
    expect(bubbles).toHaveLength(1)
    expect(bubbles[0].id).toBe('server-id-1')
    expect(bubbles[0].deliveryStatus).toBe('discarded')
    expect(bubbles[0].content).toBe('summarize the quarter')
  })

  it('a reconnect replay reconciling the tab’s own optimistic bubble re-keys it as "discarded", not "received"', () => {
    // The optimistic bubble sendMessage created, still keyed by the LOCAL
    // client_message_id (no reload — a WS reconnect replay only).
    useChatStore.setState((s) => ({
      sessionsById: {
        ...s.sessionsById,
        [SID]: {
          ...(s.sessionsById[SID] ?? ({} as never)),
          messageOrder: ['client-abc'],
          messagesById: {
            'client-abc': { id: 'client-abc', role: 'user', content: 'check my tasks', timestamp: '2026-10-11T10:00:00Z', deliveryStatus: 'sending' },
          },
        },
      },
    }) as never)

    useChatStore.getState().handleFrame({
      type: 'replay_message',
      session_id: SID,
      id: 'server-real-id',
      client_message_id: 'client-abc',
      role: 'user',
      content: 'check my tasks',
      timestamp: '2026-10-11T10:00:00Z',
      input_disposition: {
        message_id: 'server-real-id',
        client_message_id: 'client-abc',
        state: 'discarded',
        reason: 'stopped_before_delivery',
      },
    } as ServerFrame)

    const bucket = useChatStore.getState().sessionsById[SID]!
    // Exactly ONE user bubble — the reconcile path, not a duplicate.
    expect(userBubbles()).toHaveLength(1)
    expect(bucket.messagesById['client-abc']).toBeUndefined()
    // The server's stored truth: this input was never delivered — the
    // reconciled bubble must say so, not "received".
    expect(bucket.messagesById['server-real-id'].deliveryStatus).toBe('discarded')
  })
})
