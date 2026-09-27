/**
 * ws.4008.test.ts — #823 catch-up redesign, BE-DESIGN.md §6.7 (founder
 * decision Q5): close code 4008 ("catch-up required" — this connection's
 * per-connection queue overflowed, §2.1; the frame is NEVER dropped, the
 * journal still holds it) must:
 *   1. Reconnect IMMEDIATELY — no exponential backoff, no slow-phase delay.
 *   2. NEVER route through the reconnect-phase banner machinery
 *      (onReconnectStateChange) — no "reconnecting…" / "unreachable" UI.
 *
 * Differentiation: code 1006 (abnormal close, e.g. a real network drop)
 * MUST still go through the ordinary backoff + banner path — this test file
 * proves 4008 is a NARROW exception, not a general behavior change.
 *
 * PROVISIONAL: exercises WsConnection directly with a mock WebSocket
 * (mirrors ws.1008.test.ts's own harness) — Lane A's gateway hub is what
 * actually emits a real 4008 in production; this proves the SPA's own
 * handling of the close code in isolation.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WsConnection } from './ws'

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
    onopen: null,
    onmessage: null,
    onclose: null,
    onerror: null,
    send: vi.fn(),
    close: vi.fn(),
    readyState: 1, // OPEN
  }
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
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function makeCallbacks() {
  return {
    onFrame: vi.fn(),
    onConnected: vi.fn(),
    onDisconnected: vi.fn(),
    onError: vi.fn(),
    onReconnectStateChange: vi.fn(),
  }
}

describe('WsConnection — close code 4008 (§6.7, Q5)', () => {
  it('reconnects IMMEDIATELY (a new socket exists synchronously, no timer to advance)', () => {
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    const wsCallsBeforeClose = MockWebSocket.mock.calls.length
    lastWsInstance.onclose?.({ code: 4008, reason: 'catch-up required' })

    // No setTimeout needed to advance — the reconnect already happened.
    expect(MockWebSocket.mock.calls.length).toBe(wsCallsBeforeClose + 1)

    conn.disconnect()
    vi.useRealTimers()
  })

  it('does NOT invoke onReconnectStateChange — no banner for a 4008 close', () => {
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()
    // The initial successful open already fired onReconnectStateChange(null, 0)
    // (clearing any prior phase) — that is normal, pre-existing behavior
    // unrelated to this fix. What must NOT happen is a NEW call as a result
    // of the 4008 close itself (a 'reconnecting'/'slow'/'gave_up' phase,
    // which is what _scheduleReconnect would have reported).
    const callsBeforeClose = cbs.onReconnectStateChange.mock.calls.length

    lastWsInstance.onclose?.({ code: 4008, reason: 'catch-up required' })

    expect(cbs.onReconnectStateChange.mock.calls.length).toBe(callsBeforeClose)

    conn.disconnect()
    vi.useRealTimers()
  })

  it('does NOT call forceLogout (4008 is not an auth failure like 1008)', () => {
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // A 4008 close, followed by the fresh connection also opening, must
    // proceed normally — no forced logout/redirect.
    lastWsInstance.onclose?.({ code: 4008, reason: 'catch-up required' })
    lastWsInstance.onopen?.()

    expect(cbs.onConnected).toHaveBeenCalled()

    conn.disconnect()
    vi.useRealTimers()
  })

  it('differentiation: code 1006 (real drop) still goes through backoff — no synchronous reconnect', () => {
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    const wsCallsBeforeClose = MockWebSocket.mock.calls.length
    lastWsInstance.onclose?.({ code: 1006, reason: 'network blip' })

    // No new socket yet — 1006 goes through the backoff timer, unlike 4008.
    expect(MockWebSocket.mock.calls.length).toBe(wsCallsBeforeClose)
    expect(cbs.onReconnectStateChange).toHaveBeenCalled()

    conn.disconnect()
    vi.useRealTimers()
  })
})
