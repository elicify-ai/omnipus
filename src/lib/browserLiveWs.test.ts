/**
 * browserLiveWs.test.ts — BrowserLiveWsConnection tests (ADR-038 D1/D5).
 *
 * Covers: connect → auth + browser_attach handshake, frame parse/dispatch
 * (browser_screencast / browser_status / error), sendInput/sendControl/detach
 * wire shapes, close-code 1008 (no reconnect), and bounded reconnect on
 * unexpected close.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { BrowserLiveWsConnection, parseBrowserFrame, getBrowserFrameDropCount } from './browserLiveWs'

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
}) as unknown as typeof WebSocket & {
  OPEN: number
  CLOSED: number
  mockClear: () => void
  mock: { calls: unknown[][] }
}

MockWebSocket.OPEN = 1
MockWebSocket.CLOSED = 3

function makeCallbacks() {
  return {
    onScreencast: vi.fn(),
    onStatus: vi.fn(),
    onTabs: vi.fn(),
    onWebRTCAnswer: vi.fn(),
    onWebRTCState: vi.fn(),
    onError: vi.fn(),
    onConnected: vi.fn(),
    onDisconnected: vi.fn(),
  }
}

function openSocket() {
  lastWsInstance.onopen?.()
}

function sentFrames(): unknown[] {
  return lastWsInstance.send.mock.calls.map((call) => JSON.parse(call[0] as string) as unknown)
}

beforeEach(() => {
  MockWebSocket.mockClear()
  vi.stubGlobal('WebSocket', MockWebSocket)
  vi.stubGlobal('sessionStorage', { getItem: vi.fn(() => 'test-token') })
  vi.stubGlobal('localStorage', { getItem: vi.fn(() => null) })
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('BrowserLiveWsConnection — connect handshake', () => {
  it('sends auth then browser_attach, in order, on open', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()

    const frames = sentFrames()
    expect(frames).toEqual([
      { type: 'auth', token: 'test-token' },
      { type: 'browser_attach', session_id: 'sess-1', agent_id: 'agent-1' },
    ])
  })

  it('calls onConnected after the handshake is sent', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    expect(callbacks.onConnected).toHaveBeenCalledTimes(1)
  })

  it('surfaces an error and closes without attaching when no auth token is present', () => {
    vi.stubGlobal('sessionStorage', { getItem: vi.fn(() => null) })
    vi.stubGlobal('localStorage', { getItem: vi.fn(() => null) })
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    expect(callbacks.onError).toHaveBeenCalledWith(expect.stringContaining('No auth token'))
    expect(lastWsInstance.send).not.toHaveBeenCalled()
    expect(lastWsInstance.close).toHaveBeenCalled()
  })
})

describe('BrowserLiveWsConnection — inbound frame dispatch', () => {
  it('routes a browser_screencast frame to onScreencast', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    const frame = {
      type: 'browser_screencast',
      session_id: 'sess-1',
      seq: 1,
      data: 'base64jpeg',
      width: 1280,
      height: 720,
    }
    lastWsInstance.onmessage?.({ data: JSON.stringify(frame) })

    expect(callbacks.onScreencast).toHaveBeenCalledWith(frame)
    expect(callbacks.onStatus).not.toHaveBeenCalled()
  })

  it('routes a browser_status frame to onStatus', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    const frame = { type: 'browser_status', state: 'controlling' }
    lastWsInstance.onmessage?.({ data: JSON.stringify(frame) })

    expect(callbacks.onStatus).toHaveBeenCalledWith(frame)
  })

  it('routes a server error frame to onError with its message', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    callbacks.onError.mockClear() // clear any handshake-path calls

    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'error', message: 'session not found' }) })

    expect(callbacks.onError).toHaveBeenCalledWith('session not found')
  })

  it('drops a malformed frame silently (no callback fires)', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    callbacks.onError.mockClear()

    lastWsInstance.onmessage?.({ data: 'not json' })
    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'browser_screencast', width: 1280 /* missing required fields */ }) })

    expect(callbacks.onScreencast).not.toHaveBeenCalled()
    expect(callbacks.onStatus).not.toHaveBeenCalled()
    expect(callbacks.onError).not.toHaveBeenCalled()
  })

  it('routes a browser_tabs frame to onTabs', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    const frame = {
      type: 'browser_tabs',
      session_id: 'sess-1',
      active_index: 1,
      tabs: [
        { index: 0, title: 'First tab', url: 'https://example.com', active: false },
        { index: 1, title: 'Second tab', url: 'https://example.org', active: true },
      ],
    }
    lastWsInstance.onmessage?.({ data: JSON.stringify(frame) })

    expect(callbacks.onTabs).toHaveBeenCalledWith(frame)
    expect(callbacks.onScreencast).not.toHaveBeenCalled()
    expect(callbacks.onStatus).not.toHaveBeenCalled()
  })

  it('routes a browser_webrtc_answer frame to onWebRTCAnswer', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    const frame = { type: 'browser_webrtc_answer', session_id: 'sess-1', sdp: 'v=0...' }
    lastWsInstance.onmessage?.({ data: JSON.stringify(frame) })

    expect(callbacks.onWebRTCAnswer).toHaveBeenCalledWith(frame)
    expect(callbacks.onScreencast).not.toHaveBeenCalled()
    expect(callbacks.onWebRTCState).not.toHaveBeenCalled()
  })

  it('routes a browser_webrtc_state frame to onWebRTCState', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    const frame = { type: 'browser_webrtc_state', available: true, has_audio: true, active: true }
    lastWsInstance.onmessage?.({ data: JSON.stringify(frame) })

    expect(callbacks.onWebRTCState).toHaveBeenCalledWith(frame)
    expect(callbacks.onWebRTCAnswer).not.toHaveBeenCalled()
  })

  it('routes a browser_webrtc_state{available:false, reason} frame to onWebRTCState verbatim', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    const frame = { type: 'browser_webrtc_state', available: false, reason: 'lite_build' }
    lastWsInstance.onmessage?.({ data: JSON.stringify(frame) })

    expect(callbacks.onWebRTCState).toHaveBeenCalledWith(frame)
  })

  it('drops a chat-only frame type (e.g. done) — not relevant to this socket', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'done', session_id: 'sess-1' }) })

    expect(callbacks.onScreencast).not.toHaveBeenCalled()
    expect(callbacks.onStatus).not.toHaveBeenCalled()
  })
})

describe('BrowserLiveWsConnection — outbound sends', () => {
  it('sendInput wraps the payload with type:"browser_input"', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()
    lastWsInstance.send.mockClear()

    conn.sendInput({ kind: 'mouse_move', x: 100, y: 50, modifiers: 0 })

    expect(sentFrames()).toEqual([{ type: 'browser_input', kind: 'mouse_move', x: 100, y: 50, modifiers: 0 }])
  })

  it('sendInput for a text kind sends only kind/text/modifiers', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()
    lastWsInstance.send.mockClear()

    conn.sendInput({ kind: 'text', text: 'a', modifiers: 0 })

    expect(sentFrames()).toEqual([{ type: 'browser_input', kind: 'text', text: 'a', modifiers: 0 }])
  })

  it('sendControl("take") sends a browser_control frame with action:take', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()
    lastWsInstance.send.mockClear()

    conn.sendControl('take')

    expect(sentFrames()).toEqual([{ type: 'browser_control', action: 'take' }])
  })

  it('sendControl("release") sends a browser_control frame with action:release', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()
    lastWsInstance.send.mockClear()

    conn.sendControl('release')

    expect(sentFrames()).toEqual([{ type: 'browser_control', action: 'release' }])
  })

  it('sendTabAction("switch", i) sends a browser_tab_action frame with action:switch and the index', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()
    lastWsInstance.send.mockClear()

    conn.sendTabAction('switch', 2)

    expect(sentFrames()).toEqual([
      { type: 'browser_tab_action', session_id: 'sess-1', agent_id: 'agent-1', action: 'switch', index: 2 },
    ])
  })

  it('sendTabAction("close", i) sends a browser_tab_action frame with action:close and the index', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()
    lastWsInstance.send.mockClear()

    conn.sendTabAction('close', 0)

    expect(sentFrames()).toEqual([
      { type: 'browser_tab_action', session_id: 'sess-1', agent_id: 'agent-1', action: 'close', index: 0 },
    ])
  })

  it('sendTabAction("open") sends a browser_tab_action frame with action:open and no index', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()
    lastWsInstance.send.mockClear()

    conn.sendTabAction('open')

    expect(sentFrames()).toEqual([
      { type: 'browser_tab_action', session_id: 'sess-1', agent_id: 'agent-1', action: 'open' },
    ])
  })

  it('sendWebRTCOffer sends a browser_webrtc_offer frame carrying session_id/agent_id/sdp', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()
    lastWsInstance.send.mockClear()

    conn.sendWebRTCOffer('v=0...')

    expect(sentFrames()).toEqual([
      { type: 'browser_webrtc_offer', session_id: 'sess-1', agent_id: 'agent-1', sdp: 'v=0...' },
    ])
  })

  it('sendWebRTCOffer is a no-op (returns false) when the socket is not open', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    lastWsInstance.readyState = 3 // CLOSED
    expect(conn.sendWebRTCOffer('v=0...')).toBe(false)
  })

  it('detach() sends a browser_detach frame carrying the session id', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()
    lastWsInstance.send.mockClear()

    conn.detach()

    expect(sentFrames()).toEqual([{ type: 'browser_detach', session_id: 'sess-1' }])
  })

  it('sendInput is a no-op (returns false) when the socket is not open', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    // Never opened — readyState defaults away from OPEN once we flip it below.
    lastWsInstance.readyState = 3 // CLOSED
    const result = conn.sendInput({ kind: 'mouse_move', x: 0, y: 0 })
    expect(result).toBe(false)
  })
})

describe('BrowserLiveWsConnection — close / reconnect', () => {
  it('close() marks the close intentional and does not schedule a reconnect', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    conn.close()
    expect(lastWsInstance.close).toHaveBeenCalledWith(1000, 'panel closed')

    // Simulate the browser firing onclose after conn.close() nulled `this.ws` —
    // reconnection must not be scheduled since intentionalClose is true.
    const wsInstanceCountBefore = MockWebSocket.mock.calls.length
    lastWsInstance.onclose?.({ code: 1000, reason: 'panel closed' })
    vi.advanceTimersByTime(30_000)
    expect(MockWebSocket.mock.calls.length).toBe(wsInstanceCountBefore)
  })

  it('code 1008 (auth rejected) surfaces an error and does not reconnect', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    callbacks.onError.mockClear()

    const wsInstanceCountBefore = MockWebSocket.mock.calls.length
    lastWsInstance.onclose?.({ code: 1008, reason: 'unauthorized' })

    expect(callbacks.onError).toHaveBeenCalledWith(expect.stringContaining('Authentication failed'))
    vi.advanceTimersByTime(30_000)
    expect(MockWebSocket.mock.calls.length).toBe(wsInstanceCountBefore)
  })

  it('an unexpected close (e.g. 1006) schedules a bounded reconnect that re-sends the handshake', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    const wsInstanceCountBefore = MockWebSocket.mock.calls.length
    lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' })
    expect(callbacks.onDisconnected).toHaveBeenCalledTimes(1)

    vi.advanceTimersByTime(1000) // first backoff delay
    expect(MockWebSocket.mock.calls.length).toBe(wsInstanceCountBefore + 1)

    // New socket re-runs the auth + attach handshake.
    lastWsInstance.send.mockClear()
    openSocket()
    expect(sentFrames()).toEqual([
      { type: 'auth', token: 'test-token' },
      { type: 'browser_attach', session_id: 'sess-1', agent_id: 'agent-1' },
    ])
  })

  it('gives up after the max reconnect attempts and reports a final error', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    // Fail 5 times in a row (MAX_RECONNECT_ATTEMPTS) without ever reopening.
    for (let i = 0; i < 5; i++) {
      lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' })
      vi.advanceTimersByTime(10_000) // exceeds the capped backoff delay
    }
    callbacks.onError.mockClear()

    // 6th close — attempts exhausted, no further reconnect scheduled.
    const wsInstanceCountBefore = MockWebSocket.mock.calls.length
    lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' })

    expect(callbacks.onError).toHaveBeenCalledWith(expect.stringContaining('Close and reopen the panel'))
    vi.advanceTimersByTime(30_000)
    expect(MockWebSocket.mock.calls.length).toBe(wsInstanceCountBefore)
  })
})

describe('parseBrowserFrame', () => {
  it('accepts a valid browser_screencast payload', () => {
    const payload = {
      type: 'browser_screencast',
      session_id: 's1',
      seq: 0,
      data: 'abc',
      width: 100,
      height: 100,
    }
    expect(parseBrowserFrame(JSON.stringify(payload))).toEqual(payload)
  })

  it('accepts a valid browser_tabs payload', () => {
    const payload = {
      type: 'browser_tabs',
      active_index: 0,
      tabs: [{ index: 0, title: 'A tab', url: 'https://example.com', active: true }],
    }
    expect(parseBrowserFrame(JSON.stringify(payload))).toEqual(payload)
  })

  it('rejects a non-JSON string', () => {
    expect(parseBrowserFrame('{not json')).toBeNull()
  })

  it('rejects a browser_status payload with an out-of-enum state value', () => {
    expect(parseBrowserFrame(JSON.stringify({ type: 'browser_status', state: 'not_a_real_state' }))).toBeNull()
  })

  it('rejects a client-direction frame type (browser_input) — this socket never receives one', () => {
    expect(
      parseBrowserFrame(JSON.stringify({ type: 'browser_input', kind: 'mouse_move', modifiers: 0 })),
    ).toBeNull()
  })

  it('rejects a client-direction frame type (browser_webrtc_offer) — this socket only sends that one, never receives it', () => {
    expect(
      parseBrowserFrame(JSON.stringify({ type: 'browser_webrtc_offer', agent_id: 'a1', session_id: 's1', sdp: 'v=0...' })),
    ).toBeNull()
  })

  it('accepts a valid browser_webrtc_answer payload', () => {
    const payload = { type: 'browser_webrtc_answer', session_id: 's1', sdp: 'v=0...' }
    expect(parseBrowserFrame(JSON.stringify(payload))).toEqual(payload)
  })

  it('rejects a browser_webrtc_answer payload missing the required sdp field', () => {
    expect(parseBrowserFrame(JSON.stringify({ type: 'browser_webrtc_answer', session_id: 's1' }))).toBeNull()
  })

  it('accepts a valid browser_webrtc_state payload (available:true with audio/active)', () => {
    const payload = { type: 'browser_webrtc_state', available: true, has_audio: true, active: true }
    expect(parseBrowserFrame(JSON.stringify(payload))).toEqual(payload)
  })

  it('accepts a valid browser_webrtc_state payload (available:false with a reason)', () => {
    const payload = { type: 'browser_webrtc_state', available: false, reason: 'not_capable' }
    expect(parseBrowserFrame(JSON.stringify(payload))).toEqual(payload)
  })

  it('rejects a browser_webrtc_state payload missing the required available field', () => {
    expect(parseBrowserFrame(JSON.stringify({ type: 'browser_webrtc_state', reason: 'error' }))).toBeNull()
  })

  it('rejects a browser_webrtc_state payload with an out-of-enum reason value', () => {
    expect(
      parseBrowserFrame(JSON.stringify({ type: 'browser_webrtc_state', available: false, reason: 'not_a_real_reason' })),
    ).toBeNull()
  })

  it('rejects a browser_webrtc_state payload with an unknown extra property (schema is .strict())', () => {
    expect(
      parseBrowserFrame(JSON.stringify({ type: 'browser_webrtc_state', available: true, bogus: 'nope' })),
    ).toBeNull()
  })
})

describe('parseBrowserFrame — drop counter (LOW, fix-wave B, Constraint #8)', () => {
  // The counter is module-level (persists for the life of the module), so
  // every assertion here checks the DELTA across one `parseBrowserFrame`
  // call rather than an absolute value — order-independent regardless of
  // what ran earlier in this file.
  it('increments on a payload that fails zod schema validation', () => {
    const before = getBrowserFrameDropCount()

    parseBrowserFrame(JSON.stringify({ type: 'browser_webrtc_state', reason: 'error' /* missing required available */ }))

    expect(getBrowserFrameDropCount()).toBe(before + 1)
  })

  it('does NOT increment for a non-JSON string — that fails before zod ever runs', () => {
    const before = getBrowserFrameDropCount()

    parseBrowserFrame('{not json')

    expect(getBrowserFrameDropCount()).toBe(before)
  })

  it('does NOT increment for a valid, accepted frame', () => {
    const before = getBrowserFrameDropCount()

    parseBrowserFrame(
      JSON.stringify({ type: 'browser_screencast', session_id: 's1', seq: 0, data: 'abc', width: 1, height: 1 }),
    )

    expect(getBrowserFrameDropCount()).toBe(before)
  })

  it('does NOT increment for a schema-valid but irrelevant (chat-only) frame type — that is an intentional filter, not a validation failure', () => {
    const before = getBrowserFrameDropCount()

    parseBrowserFrame(JSON.stringify({ type: 'done', session_id: 'sess-1' }))

    expect(getBrowserFrameDropCount()).toBe(before)
  })
})
