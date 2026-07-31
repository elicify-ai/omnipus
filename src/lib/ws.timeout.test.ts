/**
 * ws.timeout.test.ts — WsConnection heartbeat liveness self-heal (ping timeout) tests.
 *
 * Bug: `_startHeartbeat` (~933-964) force-closes the socket after 2 consecutive
 * missed pings (~60s of silence) to trigger a fast reconnect on a silently-dead
 * connection. It used to call `this.ws.close(1006, 'ping timeout')` — but 1006
 * is a RESERVED WebSocket close code; `WebSocket.close()` only accepts 1000 or
 * 3000-4999 and throws InvalidAccessError for any other value. The surrounding
 * try/catch swallowed that throw, so the socket was never actually closed and
 * onclose never fired — the liveness self-heal was dead code.
 *
 * Fix: use 4000 (a valid application-defined code in the 3000-4999 range).
 *
 * This test drives the heartbeat to the force-close point using a fake
 * WebSocket whose close() mirrors real browser behavior — it throws
 * InvalidAccessError for reserved codes and succeeds (invoking onclose, like
 * a real socket would) for valid ones. That makes the test RED for 1006
 * (close() throws, no onclose, no reconnect scheduled) and GREEN for 4000
 * (close() succeeds, onclose fires, reconnect is scheduled).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WsConnection } from './ws'

// ── Mock WebSocket whose close() mirrors real reserved-close-code behavior ────

const RESERVED_CLOSE_CODES = new Set([1004, 1005, 1006, 1015])

function isValidCloseCode(code: number): boolean {
  if (code === 1000) return true
  if (RESERVED_CLOSE_CODES.has(code)) return false
  return code >= 3000 && code <= 4999
}

let lastWsInstance: {
  onopen: (() => void) | null
  onmessage: ((ev: { data: string }) => void) | null
  onclose: ((ev: { code: number; reason: string }) => void) | null
  onerror: (() => void) | null
  send: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
  readyState: number
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const MockWebSocket = vi.fn(function (this: any) {
  this.onopen = null
  this.onmessage = null
  this.onclose = null
  this.onerror = null
  this.readyState = 1 // OPEN
  // Mirrors the real browser: close() throws InvalidAccessError synchronously
  // for a reserved/out-of-range code and never closes the socket (onclose
  // does NOT fire). For a valid code it "closes" — flips readyState and
  // invokes onclose, like a real socket eventually does.
  this.close = vi.fn((code?: number, reason?: string) => {
    if (code !== undefined && !isValidCloseCode(code)) {
      const err = new DOMException(
        `Failed to execute 'close' on 'WebSocket': The code must be either 1000, or between 3000 and 4999. ${code} is neither.`,
        'InvalidAccessError'
      )
      throw err
    }
    this.readyState = 3 // CLOSED
    this.onclose?.({ code: code ?? 1000, reason: reason ?? '' })
  })
  this.send = vi.fn()
  lastWsInstance = this
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

describe('WsConnection — heartbeat liveness self-heal (ping timeout force-close)', () => {
  it('force-closes with a VALID close code after 2 missed pings, without throwing', () => {
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Never simulate a server frame arriving in between — _receivedSinceLastPing
    // stays false the whole time, so every heartbeat tick counts as a missed ping.
    // Tick 1 (t=30s): missedPingCount -> 1, ping sent, no close.
    expect(() => vi.advanceTimersByTime(30_000)).not.toThrow()
    expect(lastWsInstance.close).not.toHaveBeenCalled()

    // Tick 2 (t=60s): missedPingCount -> 2, force-close fires here.
    expect(() => vi.advanceTimersByTime(30_000)).not.toThrow()

    expect(lastWsInstance.close).toHaveBeenCalledTimes(1)
    const [code, reason] = lastWsInstance.close.mock.calls[0] as [number, string]
    // The regression: code must NOT be 1006 (reserved — throws in a real browser).
    expect(code).not.toBe(1006)
    expect(code).toBe(4000)
    expect(reason).toBe('ping timeout')
    // Confirm the code used is actually valid per real WebSocket.close() rules,
    // i.e. the mock's close() did not have to throw to reach this point.
    expect(isValidCloseCode(code)).toBe(true)

    conn.disconnect()
    vi.useRealTimers()
  })

  it('onclose fires and a reconnect is scheduled after the ping-timeout force-close', () => {
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    const wsCallsBeforeTimeout = MockWebSocket.mock.calls.length

    // Advance through 2 missed-ping ticks (60s) to hit the force-close.
    vi.advanceTimersByTime(60_000)

    // onDisconnected must have been invoked — proof onclose actually fired
    // (with the 1006 bug, close() threw, was swallowed, and onclose never ran,
    // so onDisconnected would never be called here).
    expect(cbs.onDisconnected).toHaveBeenCalled()

    // A reconnect must be scheduled: advancing the fast-retry timer creates a
    // new WebSocket instance.
    vi.advanceTimersByTime(5_000)
    expect(MockWebSocket.mock.calls.length).toBeGreaterThan(wsCallsBeforeTimeout)

    conn.disconnect()
    vi.useRealTimers()
  })
})
