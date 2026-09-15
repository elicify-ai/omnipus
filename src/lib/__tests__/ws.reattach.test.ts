/**
 * ws.reattach.test.ts — ADR-082 D5 (FR-010): on EVERY WebSocket reopen (not
 * only the first connect), the SPA re-attaches its active session with the
 * `since` cursor, without user action. Spec: T-20.
 *
 * `WsConnection` (this file) owns the transport/reconnect lifecycle only —
 * it never sends `attach_session` itself. That is `reattachActiveSession`'s
 * job (src/components/chat/OmnipusRuntimeProvider.tsx), wired into
 * `WsConnection`'s `onConnected` callback, fired from `ws.onopen` on every
 * socket, first connect and every reconnect alike (see `_createSocket`).
 * This test wires the two together exactly the way `OmnipusRuntimeProvider`
 * does, without rendering React, and drives a real drop + reconnect cycle to
 * prove the re-attach happens on the SECOND socket too, not just the first.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WsConnection } from '../ws'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'

// ── Mock WebSocket (mirrors src/lib/ws.timeout.test.ts's harness) ───────────

let lastWsInstance: {
  onopen: (() => void) | null
  onmessage: ((ev: { data: string }) => void) | null
  onclose: ((ev: { code: number; reason: string }) => void) | null
  onerror: (() => void) | null
  send: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
  readyState: number
}

const MockWebSocket = vi.fn(function () {
  const instance = {
    onopen: null as (() => void) | null,
    onmessage: null as ((ev: { data: string }) => void) | null,
    onclose: null as ((ev: { code: number; reason: string }) => void) | null,
    onerror: null as (() => void) | null,
    readyState: 1, // OPEN — matches real WebSocket immediately after construction in these mocks
    close: vi.fn(),
    send: vi.fn(),
  }
  instance.close = vi.fn((code?: number, reason?: string) => {
    instance.readyState = 3 // CLOSED
    instance.onclose?.({ code: code ?? 1006, reason: reason ?? '' })
  })
  lastWsInstance = instance
  return instance
}) as unknown as typeof WebSocket & {
  OPEN: number
  CLOSED: number
  mockClear: () => void
  mock: { calls: unknown[][] }
}

MockWebSocket.OPEN = 1
MockWebSocket.CLOSED = 3

beforeEach(() => {
  MockWebSocket.mockClear()
  vi.stubGlobal('WebSocket', MockWebSocket)
  useChatStore.setState({ sessionsById: {} })
  useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function makeCallbacks(conn: { current: WsConnection | null }, setConnectionError: (msg: string | null) => void) {
  // Mirrors OmnipusRuntimeProvider.tsx's WsLifecycle: onConnected re-attaches
  // the active session on every open, first connect and every reconnect alike.
  return {
    onFrame: vi.fn(),
    onConnected: vi.fn(() => {
      if (conn.current) reattachActiveSession(conn.current, setConnectionError)
    }),
    onDisconnected: vi.fn(),
    onError: vi.fn(),
    onReconnectStateChange: vi.fn(),
  }
}

function lastSentFrames(instance: typeof lastWsInstance): unknown[] {
  return instance.send.mock.calls.map((call) => JSON.parse(call[0] as string))
}

function attachFrames(instance: typeof lastWsInstance): Array<{ type: string; session_id?: string; since?: string }> {
  return lastSentFrames(instance).filter(
    (f): f is { type: string; session_id?: string; since?: string } =>
      typeof f === 'object' && f !== null && (f as { type?: string }).type === 'attach_session',
  )
}

describe('WsConnection + reattachActiveSession — reconnect re-attach (ADR-082 D5/FR-010)', () => {
  it('sends attach_session with the since cursor on the FIRST connect', () => {
    useSessionStore.setState({ activeSessionId: 'sess-1', activeAgentId: null, activeAgentType: null })
    // Seed a lastReceivedEventTime for sess-1 via a real frame, mirroring how
    // the store actually populates the `since` cursor (chat.ts's I1 fix).
    useChatStore.getState().handleFrame({
      type: 'replay_message',
      session_id: 'sess-1',
      role: 'assistant',
      content: 'seen before',
      timestamp: '2026-09-08T09:00:00.000Z',
    })

    const connRef: { current: WsConnection | null } = { current: null }
    const setConnectionError = vi.fn()
    const conn = new WsConnection(makeCallbacks(connRef, setConnectionError))
    connRef.current = conn

    conn.connect()
    lastWsInstance.onopen?.()

    const attaches = attachFrames(lastWsInstance)
    expect(attaches).toHaveLength(1)
    expect(attaches[0]).toMatchObject({ type: 'attach_session', session_id: 'sess-1', since: '2026-09-08T09:00:00.000Z' })

    conn.disconnect()
  })

  it('re-sends attach_session with the since cursor on EVERY subsequent reopen (reconnect after a drop), not only the first connect', () => {
    vi.useFakeTimers()
    useSessionStore.setState({ activeSessionId: 'sess-2', activeAgentId: null, activeAgentType: null })
    useChatStore.getState().handleFrame({
      type: 'replay_message',
      session_id: 'sess-2',
      role: 'assistant',
      content: 'seen before reconnect',
      timestamp: '2026-09-08T09:30:00.000Z',
    })

    const connRef: { current: WsConnection | null } = { current: null }
    const setConnectionError = vi.fn()
    const conn = new WsConnection(makeCallbacks(connRef, setConnectionError))
    connRef.current = conn

    conn.connect()
    const firstInstance = lastWsInstance
    firstInstance.onopen?.()
    expect(attachFrames(firstInstance)[0]).toMatchObject({
      type: 'attach_session',
      session_id: 'sess-2',
      since: '2026-09-08T09:30:00.000Z',
    })

    // A successful attach wipes the local bucket so the gateway's replay can
    // repopulate it from scratch (reattachActiveSession's own contract).
    // Simulate that replay landing before the NEXT drop — this is what
    // actually advances the since-cursor across a full reconnect cycle, and
    // is what the second assertion below proves gets sent again.
    useChatStore.getState().handleFrame({
      type: 'replay_message',
      session_id: 'sess-2',
      role: 'assistant',
      content: 'seen after first attach, before the drop',
      timestamp: '2026-09-08T09:45:00.000Z',
    })

    // Simulate an abnormal drop (network blip) — NOT an intentional close —
    // which schedules a reconnect via the fast-retry backoff.
    firstInstance.onclose?.({ code: 1006, reason: 'network blip' })

    // Advance past the first fast-retry delay to let the reconnect fire.
    vi.advanceTimersByTime(2_000)
    expect(MockWebSocket.mock.calls.length).toBeGreaterThan(1)

    const secondInstance = lastWsInstance
    expect(secondInstance).not.toBe(firstInstance)
    secondInstance.onopen?.()

    // The NEW socket must have sent its own attach_session — this is the
    // literal D5 requirement: every reopen re-attaches, without user action.
    const secondAttaches = attachFrames(secondInstance)
    expect(secondAttaches).toHaveLength(1)
    expect(secondAttaches[0]).toMatchObject({
      type: 'attach_session',
      session_id: 'sess-2',
      since: '2026-09-08T09:45:00.000Z',
    })

    conn.disconnect()
    vi.useRealTimers()
  })

  it('does NOT send attach_session on reopen when there is no active session', () => {
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })

    const connRef: { current: WsConnection | null } = { current: null }
    const setConnectionError = vi.fn()
    const conn = new WsConnection(makeCallbacks(connRef, setConnectionError))
    connRef.current = conn

    conn.connect()
    lastWsInstance.onopen?.()

    expect(attachFrames(lastWsInstance)).toHaveLength(0)
    expect(setConnectionError).not.toHaveBeenCalled()

    conn.disconnect()
  })

  it('does NOT send attach_session on reopen for the "__pending" kickoff sentinel — sending it would be a protocol violation (chat.ts ~L1281 guard)', () => {
    useSessionStore.setState({ activeSessionId: '__pending', activeAgentId: 'ava', activeAgentType: 'core' })
    useChatStore.setState({ pendingKickoff: { workspaceId: 'ws-1' } } as never)

    const connRef: { current: WsConnection | null } = { current: null }
    const setConnectionError = vi.fn()
    const conn = new WsConnection(makeCallbacks(connRef, setConnectionError))
    connRef.current = conn

    conn.connect()
    lastWsInstance.onopen?.()

    expect(attachFrames(lastWsInstance)).toHaveLength(0)
    // The dead kickoff is torn down instead — not left wedged.
    expect(useChatStore.getState().pendingKickoff).toBeNull()

    conn.disconnect()
  })
})
