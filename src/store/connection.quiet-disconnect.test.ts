/**
 * connection.quiet-disconnect.test.ts — useConnectionStore's connectionError
 * must stay null across a quiet abnormal disconnect, the full reconnect
 * schedule, and the give-up (#823 spec item 1).
 *
 * Spec source: issue #823 (elicify-ai/omnipus), founder comment 2026-09-23:
 *   "useConnectionStore.getState().connectionError stays null — before,
 *    during and after reconnect attempts, including after the reconnect
 *    give-up. The AppShell error banner … is not rendered."
 *
 * This wires a REAL WsConnection (src/lib/ws.ts) to a REAL useConnectionStore
 * (this module) using the SAME callback shape as the production wiring in
 * `WsLifecycle` (src/components/chat/OmnipusRuntimeProvider.tsx):
 *   onConnected        -> setConnected(true); setConnectionError(null)
 *   onDisconnected     -> recordDisconnect(null)
 *   onError            -> setConnectionError
 *   onReconnectStateChange -> setReconnectState(phase, attempt)
 *
 * (`reattachActiveSession`, also called from the production onConnected, is
 * deliberately NOT wired here — it is a separate concern with its own
 * coverage in src/lib/__tests__/ws.reattach.test.ts and
 * src/components/chat/OmnipusRuntimeProvider.reattach.test.ts, and with no
 * activeSessionId set it would be a no-op regardless.)
 *
 * Precedent for wiring WsConnection to production callbacks "without
 * rendering React": src/lib/__tests__/ws.reattach.test.ts.
 *
 * No mocking of the unit under test: WsConnection and useConnectionStore are
 * both real. Only the global WebSocket (the network boundary) is a test
 * double, matching src/lib/ws.1008.test.ts / ws.timeout.test.ts / ws.reattach.test.ts.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act } from 'react'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from './connection'

// ── Mock WebSocket ────────────────────────────────────────────────────────

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

const DEFAULT_STORE_STATE = {
  connection: null,
  isConnected: false,
  connectionError: null,
  reconnectPhase: null,
  reconnectAttempt: 0,
  disconnectedAt: null,
  reconnectedAt: null,
  lastDisconnectDurationMs: null,
  lastDisconnectWasTerminal: false,
  disconnectedAssistantMessageId: null,
  liteMode: false,
} as const

beforeEach(() => {
  MockWebSocket.mockClear()
  vi.stubGlobal('WebSocket', MockWebSocket)
  act(() => {
    useConnectionStore.setState({ ...DEFAULT_STORE_STATE })
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  act(() => {
    useConnectionStore.setState({ ...DEFAULT_STORE_STATE })
  })
})

/** Wires a real WsConnection to the real store exactly like WsLifecycle does. */
function wireProductionCallbacks(): WsConnection {
  const conn = new WsConnection({
    onFrame: () => {},
    onConnected: () => {
      useConnectionStore.getState().setConnected(true)
      useConnectionStore.getState().setConnectionError(null)
    },
    onDisconnected: () => {
      useConnectionStore.getState().recordDisconnect(null)
    },
    onError: (msg) => useConnectionStore.getState().setConnectionError(msg),
    onReconnectStateChange: (phase, attempt) => useConnectionStore.getState().setReconnectState(phase, attempt),
  })
  useConnectionStore.getState().setConnection(conn)
  return conn
}

describe('useConnectionStore.connectionError — quiet abnormal disconnect (#823 item 1)', () => {
  it('stays null through connect, an abnormal close (1006), and the entire reconnect schedule up to give-up', () => {
    vi.useFakeTimers()
    const conn = wireProductionCallbacks()

    act(() => conn.connect())
    act(() => lastWsInstance.onopen?.())

    // Baseline: connected, no error.
    expect(useConnectionStore.getState().connectionError).toBeNull()

    // The abnormal close itself — "during" the drop, before any reconnect
    // timer has even fired.
    act(() => lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' }))
    expect(useConnectionStore.getState().connectionError).toBeNull()
    expect(useConnectionStore.getState().isConnected).toBe(false)

    // Drive the reconnect schedule (a fresh abnormal close on every new
    // socket) until the store itself reports 'gave_up' — driven by the
    // observable store field, not a re-typed attempt count. SAFETY_CAP
    // guards against a regression that never gives up.
    const SAFETY_CAP = 60
    let i = 0
    while (useConnectionStore.getState().reconnectPhase !== 'gave_up' && i < SAFETY_CAP) {
      act(() => lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' }))
      // connectionError must stay null after EVERY intermediate close too —
      // "during" reconnect attempts, not just at the end.
      expect(useConnectionStore.getState().connectionError, `iteration ${i}`).toBeNull()
      act(() => vi.advanceTimersByTime(65_000))
      i++
    }

    expect(useConnectionStore.getState().reconnectPhase, 'the schedule must actually reach gave_up within the safety cap').toBe('gave_up')
    // "including after the reconnect give-up"
    expect(useConnectionStore.getState().connectionError).toBeNull()

    conn.disconnect()
    vi.useRealTimers()
  })

  it('stays null after a later successful reconnect that follows a give-up ("and after")', () => {
    vi.useFakeTimers()
    const conn = wireProductionCallbacks()

    act(() => conn.connect())
    act(() => lastWsInstance.onopen?.())

    const SAFETY_CAP = 60
    let i = 0
    while (useConnectionStore.getState().reconnectPhase !== 'gave_up' && i < SAFETY_CAP) {
      act(() => lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' }))
      act(() => vi.advanceTimersByTime(65_000))
      i++
    }
    expect(useConnectionStore.getState().reconnectPhase).toBe('gave_up')
    expect(useConnectionStore.getState().connectionError).toBeNull()

    // The user clicks the (quiet, non-alarming) "Try again" affordance —
    // simulated here as a fresh connect() the same way useConnectionStore's
    // own `reconnect()` action would drive it (that action itself only
    // touches connectionError/reconnectPhase/reconnectAttempt and delegates
    // to connection.connect() — see src/store/connection.ts's `reconnect`).
    act(() => conn.connect())
    act(() => lastWsInstance.onopen?.())

    expect(useConnectionStore.getState().isConnected).toBe(true)
    expect(useConnectionStore.getState().connectionError).toBeNull()

    conn.disconnect()
    vi.useRealTimers()
  })
})
