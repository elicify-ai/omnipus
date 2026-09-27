/**
 * ws.reattach.test.ts — ADR-082 D5 (FR-010): on EVERY WebSocket reopen (not
 * only the first connect), the SPA re-attaches its active session with its
 * numbered cursor (`since_seq`/`boot_id` — #823 catch-up redesign,
 * BE-DESIGN.md §6.1, replacing the retired `since` RFC3339 cursor), without
 * user action. Spec: T-20.
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
import { emptySessionState } from '@/store/chat/session'
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

function attachFrames(
  instance: typeof lastWsInstance,
): Array<{ type: string; session_id?: string; since_seq?: number; boot_id?: string }> {
  return lastSentFrames(instance).filter(
    (f): f is { type: string; session_id?: string; since_seq?: number; boot_id?: string } =>
      typeof f === 'object' && f !== null && (f as { type?: string }).type === 'attach_session',
  )
}

describe('WsConnection + reattachActiveSession — reconnect re-attach (ADR-082 D5/FR-010)', () => {
  // PROVENANCE (#823 catch-up redesign, BE-DESIGN.md §6.1, Step 0's contract
  // change removing AttachSessionFrame.since — founder decision Q3, REPLACE,
  // guarantee kept, test rewritten never weakened): these two tests used to
  // seed and assert the legacy `since` RFC3339 timestamp cursor
  // (lastReceivedEventTime). That field is REMOVED from the wire contract
  // (Step 0's SQUAD-REPORT-BE0.md); the replacement is the numbered
  // `{since_seq, boot_id}` pair, tracked per-bucket as SessionChatState.cursor
  // (src/store/chat/cursor.ts) and advanced by every sequenced frame (§1.2)
  // — only the four terminal/state frames (session_state, session_snapshot,
  // catch_up_complete, session_started, §3.4) carry `boot_id` on the wire;
  // an ordinary sequenced frame like `user_message` carries only `seq` and
  // inherits the cursor's already-established bootId (gateFrameBySeq's own
  // fallback). Seeded here by setting the bucket's cursor directly, which is
  // exactly that already-established state. The user-visible D5 guarantee
  // (every reopen re-attaches without user action) is unchanged; only the
  // cursor's SHAPE changed.
  it('sends attach_session with since_seq/boot_id on the FIRST connect', () => {
    useSessionStore.setState({ activeSessionId: 'sess-1', activeAgentId: null, activeAgentType: null })
    useChatStore.setState({
      sessionsById: { 'sess-1': { ...emptySessionState(), cursor: { bootId: 'boot-first', seq: 5 } } },
    } as never)

    const connRef: { current: WsConnection | null } = { current: null }
    const setConnectionError = vi.fn()
    const conn = new WsConnection(makeCallbacks(connRef, setConnectionError))
    connRef.current = conn

    conn.connect()
    lastWsInstance.onopen?.()

    const attaches = attachFrames(lastWsInstance)
    expect(attaches).toHaveLength(1)
    expect(attaches[0]).toMatchObject({
      type: 'attach_session',
      session_id: 'sess-1',
      since_seq: 5,
      boot_id: 'boot-first',
    })

    conn.disconnect()
  })

  it('re-sends attach_session with the LATEST since_seq/boot_id on EVERY subsequent reopen (reconnect after a drop), not only the first connect', () => {
    vi.useFakeTimers()
    useSessionStore.setState({ activeSessionId: 'sess-2', activeAgentId: null, activeAgentType: null })
    useChatStore.setState({
      sessionsById: { 'sess-2': { ...emptySessionState(), cursor: { bootId: 'boot-second', seq: 1 } } },
    } as never)

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
      since_seq: 1,
      boot_id: 'boot-second',
    })

    // #823 catch-up redesign (§6.1): a successful attach no longer wipes the
    // bucket — the cursor persists and keeps advancing as more sequenced
    // frames land, which is what the second assertion below proves gets
    // sent again on the NEXT reopen.
    // Advances via a REAL sequenced frame with no boot_id of its own — the
    // ordinary shape (only the four terminal/state frames carry boot_id,
    // §3.4) — proving the cursor correctly inherits the already-established
    // bootId rather than losing it.
    useChatStore.getState().handleFrame({
      type: 'user_message',
      session_id: 'sess-2',
      id: 'u3',
      content: 'seen after first attach, before the drop',
      timestamp: '2026-09-08T09:45:00.000Z',
      seq: 2,
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
      since_seq: 2,
      boot_id: 'boot-second',
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
