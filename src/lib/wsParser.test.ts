import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WsConnection, type WsReceiveFrame } from './ws'

// test_websocket_message_parser (test #1)
// Traces to: wave5a-wire-ui-spec.md — Scenario: User sends message and receives streaming response
// Dataset: WebSocket Message Parsing (10 rows)
//
// NOTE: WsConnection now batches frames via requestAnimationFrame (or setTimeout(0) when the
// document is hidden / rAF unavailable). Tests must flush the batch after triggering onmessage.
// We use vi.useFakeTimers() + vi.runAllTimers() to drain the setTimeout(0) path synchronously.
// To force the setTimeout path (instead of rAF), we stub document.visibilityState to "hidden"
// so that _scheduleFlush() always uses setTimeout(0) regardless of jsdom's rAF availability.

// ── Mock WebSocket ─────────────────────────────────────────────────────────────
// Use a vi.fn with a regular function body so that when called via `new`, `this` is the
// newly constructed instance. We capture it in `lastWsInstance` so tests can trigger handlers.

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
  this.send = vi.fn()
  this.close = vi.fn()
  this.readyState = 1 // OPEN
  lastWsInstance = this
}) as unknown as typeof WebSocket & { OPEN: number; CLOSED: number; mockClear: () => void }

MockWebSocket.OPEN = 1
MockWebSocket.CLOSED = 3

beforeEach(() => {
  MockWebSocket.mockClear()
  vi.stubGlobal('WebSocket', MockWebSocket)
  vi.stubGlobal('localStorage', { getItem: vi.fn(() => null) })
  // Force document.visibilityState to "hidden" so WsConnection._scheduleFlush() uses
  // setTimeout(0) instead of requestAnimationFrame. This makes flushes controllable via
  // vi.useFakeTimers() + vi.runAllTimers() without needing async tests.
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
  vi.useFakeTimers()
})

afterEach(() => {
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function createConnectedWs(onFrame: (frame: WsReceiveFrame) => void) {
  const conn = new WsConnection({
    onFrame,
    onConnected: vi.fn(),
    onDisconnected: vi.fn(),
    onError: vi.fn(),
  })
  conn.connect()
  // Trigger onopen — this is synchronous; _createSocket() has already set the handler
  lastWsInstance.onopen?.()
  return conn
}

/**
 * Send a raw WS message and flush the batch synchronously.
 * Because we've stubbed visibilityState to "hidden", _scheduleFlush() always
 * uses setTimeout(0). We advance by 1ms to drain the zero-delay timer without
 * iterating the heartbeat setInterval (which runs every 30 s and would loop).
 */
function sendAndFlush(data: string) {
  lastWsInstance.onmessage?.({ data })
  vi.advanceTimersByTime(1)
}

// ── Happy path ─────────────────────────────────────────────────────────────────

describe('WsConnection — frame parsing (happy path)', () => {
  it('parses a token frame and calls onFrame with typed object', () => {
    // Dataset row 1: token frame with required session_id field
    // Traces to: wave5a-wire-ui-spec.md — Scenario: User sends message and receives streaming response
    const onFrame = vi.fn()
    createConnectedWs(onFrame)
    sendAndFlush('{"type":"token","session_id":"s1","content":"Hello"}')
    expect(onFrame).toHaveBeenCalledWith({ type: 'token', session_id: 's1', content: 'Hello' })
  })

  it('parses a done frame with stats', () => {
    // Dataset row 2: done frame with required session_id field
    const onFrame = vi.fn()
    createConnectedWs(onFrame)
    sendAndFlush('{"type":"done","session_id":"s1","stats":{"tokens":150,"cost":0.02}}')
    expect(onFrame).toHaveBeenCalledWith({
      type: 'done',
      session_id: 's1',
      stats: { tokens: 150, cost: 0.02 },
    })
  })

  it('parses an error frame', () => {
    // Dataset row 3: error frame (session_id optional)
    const onFrame = vi.fn()
    createConnectedWs(onFrame)
    sendAndFlush('{"type":"error","message":"timeout"}')
    expect(onFrame).toHaveBeenCalledWith({ type: 'error', message: 'timeout' })
  })

  it('parses a tool_call_start frame', () => {
    // Dataset row 7 — includes required session_id
    const onFrame = vi.fn()
    createConnectedWs(onFrame)
    sendAndFlush('{"type":"tool_call_start","session_id":"s1","tool":"exec","call_id":"tc_1","params":{"command":"ls"}}')
    expect(onFrame).toHaveBeenCalledWith({
      type: 'tool_call_start',
      session_id: 's1',
      tool: 'exec',
      call_id: 'tc_1',
      params: { command: 'ls' },
    })
  })

  it('parses a tool_call_result frame', () => {
    // Dataset row 8 — includes required session_id and result
    const onFrame = vi.fn()
    createConnectedWs(onFrame)
    sendAndFlush('{"type":"tool_call_result","session_id":"s1","tool":"exec","call_id":"tc_1","result":{"exit_code":0},"status":"success"}')
    expect(onFrame).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'tool_call_result', tool: 'exec', status: 'success' })
    )
  })

})

// ── Edge cases ─────────────────────────────────────────────────────────────────

describe('WsConnection — frame parsing (edge cases)', () => {
  it('parses a token frame with empty content without crashing', () => {
    // Dataset row 4: empty token content — session_id required by strict schema
    const onFrame = vi.fn()
    createConnectedWs(onFrame)
    sendAndFlush('{"type":"token","session_id":"s1","content":""}')
    expect(onFrame).toHaveBeenCalledWith({ type: 'token', session_id: 's1', content: '' })
  })

  it('passes XSS-containing token content as-is to onFrame (renderer handles escaping)', () => {
    // Dataset row 5: XSS in token content — session_id required by strict schema
    const onFrame = vi.fn()
    createConnectedWs(onFrame)
    const xss = '<script>alert(1)</script>'
    sendAndFlush(JSON.stringify({ type: 'token', session_id: 's1', content: xss }))
    expect(onFrame).toHaveBeenCalledWith({ type: 'token', session_id: 's1', content: xss })
  })

  it('does NOT call onFrame for unknown message types (strict parsing drops unknown types)', () => {
    // Dataset row 6: unknown type — WsConnection now drops unknown types and increments
    // _unknownFrameTypeCount instead of passing through to the store.
    // This is the correct behaviour: the store switch was only silently ignoring these;
    // now they are explicitly rejected at the edge with a separate telemetry counter.
    const onFrame = vi.fn()
    createConnectedWs(onFrame)
    sendAndFlush('{"type":"unknown_type"}')
    // onFrame must NOT be called — unknown types are dropped at the SPA edge
    expect(onFrame).not.toHaveBeenCalled()
  })

  it('does not call onFrame for malformed JSON and does not throw', () => {
    // Dataset row 10: malformed JSON — caught silently
    const onFrame = vi.fn()
    const onError = vi.fn()
    const conn = new WsConnection({
      onFrame,
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError,
    })
    conn.connect()
    lastWsInstance.onopen?.()
    // Malformed JSON is dropped before the batch — no flush needed. Just verify no throw.
    expect(() => lastWsInstance.onmessage?.({ data: 'not valid json' })).not.toThrow()
    // Flush any pending timers with a safe single-tick advance (not runAllTimers which loops heartbeat).
    vi.advanceTimersByTime(0)
    expect(onFrame).not.toHaveBeenCalled()
  })
})

// ── Reconnect behavior ─────────────────────────────────────────────────────────

describe('WsConnection — reconnect behavior', () => {
  it('schedules reconnect on unexpected close (exponential backoff)', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: WebSocket connection error during streaming
    // Note: vi.useFakeTimers() is already active from beforeEach.
    const onDisconnected = vi.fn()
    const conn = new WsConnection({
      onFrame: vi.fn(),
      onConnected: vi.fn(),
      onDisconnected,
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()
    // Simulate unexpected close (code !== 1000)
    lastWsInstance.onclose?.({ code: 1006, reason: 'Abnormal closure' })
    expect(onDisconnected).toHaveBeenCalled()
    // Advance timer to trigger reconnect — first delay is 1000ms (fake timers active from beforeEach).
    vi.advanceTimersByTime(1001)
    // A new WebSocket should have been created (MockWebSocket called twice)
    expect(MockWebSocket).toHaveBeenCalledTimes(2)
  })

  it('does NOT reconnect after intentional disconnect', () => {
    // Note: vi.useFakeTimers() is already active from beforeEach.
    const conn = new WsConnection({
      onFrame: vi.fn(),
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()
    conn.disconnect()
    lastWsInstance.onclose?.({ code: 1000, reason: 'User disconnected' })
    vi.advanceTimersByTime(5000)
    // WebSocket only created once (the initial connect)
    expect(MockWebSocket).toHaveBeenCalledTimes(1)
  })
})
