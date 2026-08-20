/**
 * ws.1008.test.ts — WsConnection close-code 1008 (policy violation / auth failure) tests.
 *
 * M1: code 1008 must:
 *   1. Invoke forceLogout() (from @/lib/authLogout).
 *   2. NOT schedule a reconnect (the `return` after forceLogout prevents _scheduleReconnect).
 *
 * Differentiation: code 1006 (abnormal close) must NOT call forceLogout and MUST schedule
 * a reconnect.
 *
 * Traces to: project-task-management-level1-spec.md — M1 (WS 1008 → forceLogout, no reconnect)
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WsConnection } from './ws'

// ── Mock forceLogout from @/lib/authLogout ─────────────────────────────────────
//
// vi.hoisted ensures the mock function is available before vi.mock (which is
// hoisted to the top of the file by Vitest). This avoids the temporal dead zone
// error that would occur with `const mockForceLogout = vi.fn()` before vi.mock.
const { mockForceLogout } = vi.hoisted(() => ({
  mockForceLogout: vi.fn(),
}))

vi.mock('@/lib/authLogout', () => ({
  forceLogout: mockForceLogout,
}))

// ── Mock WebSocket ─────────────────────────────────────────────────────────────

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
  // Returning an object from a function invoked with `new` overrides the
  // constructed `this` with the returned object — lets us build the
  // instance as a plain local value instead of aliasing `this`.
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

// ── Test setup / teardown ──────────────────────────────────────────────────────

beforeEach(() => {
  MockWebSocket.mockClear()
  mockForceLogout.mockClear()
  vi.stubGlobal('WebSocket', MockWebSocket)
  // Provide full storage stubs (forceLogout calls removeItem in the real impl,
  // but our mock intercepts it before that code runs).
  const makeStorage = () => ({
    getItem: vi.fn(() => null),
    removeItem: vi.fn(),
    setItem: vi.fn(),
    clear: vi.fn(),
    length: 0,
    key: vi.fn(() => null),
  })
  vi.stubGlobal('localStorage', makeStorage())
  vi.stubGlobal('sessionStorage', makeStorage())
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

// ── Helper ─────────────────────────────────────────────────────────────────────

function makeCallbacks() {
  return {
    onFrame: vi.fn(),
    onConnected: vi.fn(),
    onDisconnected: vi.fn(),
    onError: vi.fn(),
    onReconnectStateChange: vi.fn(),
  }
}

// ── M1: WS code 1008 → forceLogout, no reconnect ──────────────────────────────

describe('WsConnection — code 1008 policy violation (M1)', () => {
  // Traces to: project-task-management-level1-spec.md — M1
  // When the server closes the WebSocket with code 1008 (policy violation, e.g.
  // expired/invalid auth token), the client must:
  //   1. Call forceLogout() to clear auth state and redirect to /login.
  //   2. NOT schedule a reconnect — reconnecting with a dead token would loop forever.

  it('calls forceLogout() when the server sends close code 1008', () => {
    // Traces to: M1 — code 1008 must invoke forceLogout()
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Simulate server closing with code 1008 (policy violation / auth failure).
    lastWsInstance.onclose?.({ code: 1008, reason: 'policy violation' })

    expect(mockForceLogout).toHaveBeenCalledTimes(1)

    conn.disconnect()
    vi.useRealTimers()
  })

  it('does NOT schedule a reconnect after close code 1008', () => {
    // Traces to: M1 — the `return` after forceLogout() must prevent _scheduleReconnect()
    // from firing. No new WebSocket should be created regardless of timer advancement.
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    const wsCallsBeforeClose = MockWebSocket.mock.calls.length

    // Simulate 1008.
    lastWsInstance.onclose?.({ code: 1008, reason: 'policy violation' })

    // Advance by much more than the fastest reconnect delay (1000ms) to give any
    // pending timer the chance to fire. If _scheduleReconnect was called, a new
    // WebSocket would be constructed.
    vi.advanceTimersByTime(10_000)

    expect(MockWebSocket.mock.calls.length).toBe(wsCallsBeforeClose)

    conn.disconnect()
    vi.useRealTimers()
  })

  it('forceLogout is called exactly once even when code 1008 fires', () => {
    // Traces to: M1 — forceLogout must not be called multiple times per 1008 event.
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onclose?.({ code: 1008, reason: 'auth expired' })

    expect(mockForceLogout).toHaveBeenCalledTimes(1)

    conn.disconnect()
    vi.useRealTimers()
  })
})

// ── Differentiation: code 1006 → no forceLogout, schedules reconnect ──────────

describe('WsConnection — code 1006 does NOT call forceLogout (M1 differentiation)', () => {
  // Traces to: M1 differentiation — a different close code produces a different outcome.
  // Code 1006 (abnormal closure) must NOT call forceLogout and MUST schedule a reconnect.

  it('does NOT call forceLogout for close code 1006', () => {
    // Traces to: M1 — code 1006 is a network interruption, not a policy violation;
    // forceLogout must not be invoked.
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' })

    expect(mockForceLogout).not.toHaveBeenCalled()

    conn.disconnect()
    vi.useRealTimers()
  })

  it('schedules a reconnect for close code 1006 (not 1008)', () => {
    // Traces to: M1 differentiation — 1006 triggers _scheduleReconnect(); 1008 does not.
    // This proves the return statement after forceLogout() in the 1008 path is the
    // gate that prevents reconnect, not some other condition.
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    const wsCallsBeforeClose = MockWebSocket.mock.calls.length

    // Code 1006 — abnormal close (e.g. network blip).
    lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' })

    // After advancing the reconnect timer, a new WebSocket must be created.
    vi.advanceTimersByTime(2_000)

    expect(MockWebSocket.mock.calls.length).toBeGreaterThan(wsCallsBeforeClose)

    conn.disconnect()
    vi.useRealTimers()
  })

  it('code 1008 and code 1006 produce different outcomes (differentiation)', () => {
    // Traces to: M1 differentiation — the two close codes must behave differently.
    // This is the explicit differentiation assertion.
    vi.useFakeTimers()

    // --- 1008 path ---
    const cbs1 = makeCallbacks()
    const conn1 = new WsConnection(cbs1)
    conn1.connect()
    lastWsInstance.onopen?.()
    const wsCount1Before = MockWebSocket.mock.calls.length
    lastWsInstance.onclose?.({ code: 1008, reason: 'policy violation' })
    vi.advanceTimersByTime(5_000)
    const wsCount1After = MockWebSocket.mock.calls.length
    const forceLogoutCalls1 = mockForceLogout.mock.calls.length
    conn1.disconnect()

    // Reset for the 1006 path.
    mockForceLogout.mockClear()
    MockWebSocket.mockClear()

    const cbs2 = makeCallbacks()
    const conn2 = new WsConnection(cbs2)
    conn2.connect()
    lastWsInstance.onopen?.()
    const wsCount2Before = MockWebSocket.mock.calls.length
    lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' })
    vi.advanceTimersByTime(5_000)
    const wsCount2After = MockWebSocket.mock.calls.length
    const forceLogoutCalls2 = mockForceLogout.mock.calls.length
    conn2.disconnect()

    // 1008: forceLogout called, no reconnect.
    expect(forceLogoutCalls1).toBe(1)
    expect(wsCount1After).toBe(wsCount1Before) // no new WS

    // 1006: forceLogout NOT called, reconnect scheduled.
    expect(forceLogoutCalls2).toBe(0)
    expect(wsCount2After).toBeGreaterThan(wsCount2Before) // new WS

    // Different outcomes — not hardcoded.
    expect(forceLogoutCalls1).not.toBe(forceLogoutCalls2)

    vi.useRealTimers()
  })
})
