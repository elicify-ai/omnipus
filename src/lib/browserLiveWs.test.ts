/**
 * browserLiveWs.test.ts — BrowserLiveWsConnection tests (ADR-038 D1/D5, ADR-044,
 * ADR-047).
 *
 * Covers: connect → browser_attach handshake (auth rides the same-origin
 * omnipus-session HttpOnly cookie — no client-sent auth frame, no JS-readable
 * token per ADR-044), frame parse/dispatch (browser_status / browser_tabs /
 * browser_webrtc_answer / browser_webrtc_state / error — NOT
 * browser_screencast, the JPEG-fallback wire frame this socket no longer
 * handles at all, see ADR-047), sendInput/sendControl/detach wire shapes,
 * close-code 1008 (no reconnect), and bounded reconnect on unexpected close.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  BrowserLiveWsConnection,
  parseBrowserFrame,
  getBrowserFrameDropCount,
  translateBrowserErrorMessage,
} from './browserLiveWs'

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
  // Returning an object explicitly from a constructor function makes `new
  // MockWebSocket()` yield THIS object instead of the implicit `this` —
  // avoids aliasing `this` to a local variable (no-this-alias) while still
  // giving the test a handle on the instance the app code just created.
  const instance = {
    onopen: null as (() => void) | null,
    onmessage: null as ((ev: { data: string }) => void) | null,
    onclose: null as ((ev: { code: number; reason: string }) => void) | null,
    onerror: null as (() => void) | null,
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

function makeCallbacks() {
  return {
    onStatus: vi.fn(),
    onTabs: vi.fn(),
    onWebRTCAnswer: vi.fn(),
    onWebRTCState: vi.fn(),
    onVideoHealth: vi.fn(),
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
  // No storage stubs: post-ADR-044 the client reads no token from
  // session/localStorage — auth rides the omnipus-session cookie on the
  // WS handshake, so the connection never touches web storage.
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('BrowserLiveWsConnection — connect handshake', () => {
  it('sends only browser_attach on open (cookie auth — no client-sent auth frame)', () => {
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', makeCallbacks())
    conn.connect()
    openSocket()

    const frames = sentFrames()
    expect(frames).toEqual([
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

  it('attaches without error even with no JS token present (auth rides the omnipus-session cookie)', () => {
    // ADR-044: the client no longer reads or sends any token — the browser
    // attaches the same-origin HttpOnly cookie on the handshake automatically.
    // Missing web storage must therefore NOT block the attach.
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()

    expect(sentFrames()).toEqual([
      { type: 'browser_attach', session_id: 'sess-1', agent_id: 'agent-1' },
    ])
    expect(callbacks.onError).not.toHaveBeenCalled()
    expect(lastWsInstance.close).not.toHaveBeenCalled()
  })
})

describe('BrowserLiveWsConnection — inbound frame dispatch', () => {
  // ADR-047 — the JPEG screencast fallback was removed outright: this socket
  // no longer recognizes browser_screencast at all, even a well-formed one —
  // it is dropped exactly like any other frame type this socket doesn't
  // understand (see the "chat-only frame type" coverage further down and the
  // parseBrowserFrame describe block below).
  it('drops a browser_screencast frame — no longer a recognized type on this socket', () => {
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

    expect(callbacks.onStatus).not.toHaveBeenCalled()
    expect(callbacks.onTabs).not.toHaveBeenCalled()
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

  // D5 fix ("Site 2"): this was an UNCONDITIONAL raw pass-through of the
  // server ErrorFrame.message (protocol-internal Go strings), unlike Site 1
  // (BrowserLiveView's onStatus), which already translated known cases.
  // Dispatching a synthetic error frame here must now route through the
  // same translation as browser_status error frames.
  it('D5: translates a protocol-internal ErrorFrame message before calling onError, instead of passing it through raw', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    callbacks.onError.mockClear()

    lastWsInstance.onmessage?.({
      data: JSON.stringify({ type: 'error', message: 'browser_control: attach before requesting control' }),
    })

    expect(callbacks.onError).toHaveBeenCalledWith('Reconnect to the live browser before taking control.')
    expect(callbacks.onError).not.toHaveBeenCalledWith(
      expect.stringContaining('browser_control:'),
    )
  })

  it('drops a malformed frame silently (no callback fires)', () => {
    const callbacks = makeCallbacks()
    const conn = new BrowserLiveWsConnection('sess-1', 'agent-1', callbacks)
    conn.connect()
    openSocket()
    callbacks.onError.mockClear()

    lastWsInstance.onmessage?.({ data: 'not json' })
    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'browser_status' /* missing required state */ }) })

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

    // New socket re-runs the browser_attach handshake (cookie auth — no auth frame).
    lastWsInstance.send.mockClear()
    openSocket()
    expect(sentFrames()).toEqual([
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
  // ADR-047 — even a well-formed browser_screencast payload (the JPEG
  // fallback's wire frame) is dropped: this socket no longer treats it as a
  // recognized type at all.
  it('drops a well-formed browser_screencast payload — not a recognized type on this socket', () => {
    const payload = {
      type: 'browser_screencast',
      session_id: 's1',
      seq: 0,
      data: 'abc',
      width: 100,
      height: 100,
    }
    expect(parseBrowserFrame(JSON.stringify(payload))).toBeNull()
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

    parseBrowserFrame(JSON.stringify({ type: 'browser_status', state: 'idle' }))

    expect(getBrowserFrameDropCount()).toBe(before)
  })

  it('does NOT increment for a schema-valid but irrelevant (chat-only) frame type — that is an intentional filter, not a validation failure', () => {
    const before = getBrowserFrameDropCount()

    parseBrowserFrame(JSON.stringify({ type: 'done', session_id: 'sess-1' }))

    expect(getBrowserFrameDropCount()).toBe(before)
  })
})

// D5 fix — the 9-message closed set of protocol-internal errorStatus(...)/
// ErrorFrame strings from pkg/gateway/browser_ws.go's readLoop/handleAttach/
// handleControl/handleTabAction (RCA line refs: :526, :586, :590, :861,
// :867, :907, :940, :946, :991), shared by BOTH D5 leak sites (BrowserLiveView's
// onStatus AND this module's onmessage). Exercises translateBrowserErrorMessage
// directly — the exhaustive per-message coverage the component-level tests
// (BrowserLiveView.annotateAndBar.test.tsx) don't attempt.
describe('translateBrowserErrorMessage — D5 protocol-internal closed set', () => {
  it.each([
    // readLoop inbound-schema gate (:526) — dynamic (%s, %s).
    ['frame schema validation failed (BrowserControlFrame): modifiers must be 0-15'],
    // readLoop unknown-type / non-JSON ErrorFrame-only cases (D5 Site 2).
    ['invalid frame: not JSON'],
    ['unknown frame type "bogus_frame"'],
    // handleAttach (:586, :590).
    ['browser_attach: invalid frame'],
    ['browser_attach: agent_id and session_id are required'],
    // handleControl (:867, :907) — :861 covered by its own dedicated test below.
    ['browser_control: invalid frame'],
    ['browser_control: unknown action "bogus"'],
    // handleTabAction (:946, :991) — :940 covered by its own dedicated test below.
    ['browser_tab_action: invalid frame'],
    ['browser_tab_action: unknown action "bogus"'],
  ])('maps %s to a human, non-jargon message', (raw) => {
    const translated = translateBrowserErrorMessage(raw)
    expect(translated).not.toBe(raw)
    expect(translated).not.toMatch(/^(frame schema validation failed|invalid frame|unknown frame type|browser_attach:|browser_control:|browser_tab_action:)/)
  })

  it('maps browser_control: attach before requesting control (:861) to its own tailored copy', () => {
    expect(translateBrowserErrorMessage('browser_control: attach before requesting control')).toBe(
      'Reconnect to the live browser before taking control.',
    )
  })

  it('maps browser_tab_action: attach before managing tabs (:940) to its own tailored copy', () => {
    expect(translateBrowserErrorMessage('browser_tab_action: attach before managing tabs')).toBe(
      'Reconnect to the live browser before managing tabs.',
    )
  })

  it('leaves already-readable sessionErrorStatus() copy unchanged (not in the jargon set)', () => {
    // These are hand-written, human-readable strings from browser_ws.go's
    // sessionErrorStatus() calls — deliberately NOT translated (see the
    // function's doc comment).
    expect(translateBrowserErrorMessage('another viewer already controls this browser')).toBe(
      'another viewer already controls this browser',
    )
    expect(translateBrowserErrorMessage('take-control is disabled by the operator')).toBe(
      'take-control is disabled by the operator',
    )
    expect(translateBrowserErrorMessage('no browser manager for agent "a1" (browser tools may not be registered for this agent)')).toBe(
      'no browser manager for agent "a1" (browser tools may not be registered for this agent)',
    )
  })

  it('leaves the SOURCE-fixed auth-handshake copy unchanged (already human-readable, not Go jargon)', () => {
    const authMsg = 'Your session expired — reload the page to reconnect.'
    expect(translateBrowserErrorMessage(authMsg)).toBe(authMsg)
  })

  it('passes an unrecognized raw string through unchanged', () => {
    expect(translateBrowserErrorMessage('completely unrelated message')).toBe('completely unrelated message')
  })
})
