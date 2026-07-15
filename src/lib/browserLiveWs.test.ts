/**
 * browserLiveWs.test.ts — BrowserLiveWsConnection tests (ADR-038 D1/D5, ADR-044).
 *
 * Covers: connect → browser_attach handshake (auth rides the same-origin
 * omnipus-session HttpOnly cookie — no client-sent auth frame, no JS-readable
 * token per ADR-044), frame parse/dispatch (browser_screencast /
 * browser_status / error), sendInput/sendControl/detach wire shapes, close-code
 * 1008 (no reconnect), and bounded reconnect on unexpected close.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { BrowserLiveWsConnection, parseBrowserFrame } from './browserLiveWs'

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
})
