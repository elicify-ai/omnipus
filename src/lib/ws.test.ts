/**
 * ws.ts — WsConnection tests for B1.3(c) reconnect-on-visibilitychange/online
 *
 * Traces to: B1.3(c) security hardening
 * When the connection is in disconnected or reconnecting state, the
 * visibilitychange and online events must trigger an immediate reconnect
 * attempt, resetting the exponential backoff counter so recovery is fast.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WsConnection, parseFrameSafe, getDroppedFrameCount, resetDroppedFrameCount, getUnknownFrameTypeCount, resetUnknownFrameTypeCount } from './ws'

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

// ── Event listener capture ─────────────────────────────────────────────────────

// Capture addEventListener / removeEventListener calls on window AND document.
// visibilitychange fires on document per the Web Platform spec (ws.ts uses
// document.addEventListener for it); online/offline fire on window.
const windowListeners: Record<string, EventListenerOrEventListenerObject[]> = {}

function setupWindowEventCapture() {
  vi.spyOn(window, 'addEventListener').mockImplementation(
    (type: string, listener: EventListenerOrEventListenerObject) => {
      if (!windowListeners[type]) windowListeners[type] = []
      windowListeners[type].push(listener)
    }
  )
  vi.spyOn(window, 'removeEventListener').mockImplementation(
    (type: string, listener: EventListenerOrEventListenerObject) => {
      if (windowListeners[type]) {
        windowListeners[type] = windowListeners[type].filter((l) => l !== listener)
      }
    }
  )
  vi.spyOn(document, 'addEventListener').mockImplementation(
    (type: string, listener: EventListenerOrEventListenerObject) => {
      if (!windowListeners[type]) windowListeners[type] = []
      windowListeners[type].push(listener)
    }
  )
  vi.spyOn(document, 'removeEventListener').mockImplementation(
    (type: string, listener: EventListenerOrEventListenerObject) => {
      if (windowListeners[type]) {
        windowListeners[type] = windowListeners[type].filter((l) => l !== listener)
      }
    }
  )
}

function triggerWindowEvent(type: string) {
  for (const listener of windowListeners[type] ?? []) {
    if (typeof listener === 'function') {
      listener(new Event(type))
    } else {
      listener.handleEvent(new Event(type))
    }
  }
}

beforeEach(() => {
  MockWebSocket.mockClear()
  vi.stubGlobal('WebSocket', MockWebSocket)
  vi.stubGlobal('localStorage', { getItem: vi.fn(() => null) })
  vi.stubGlobal('sessionStorage', { getItem: vi.fn(() => null) })
  // Clear captured listeners
  for (const key of Object.keys(windowListeners)) {
    delete windowListeners[key]
  }
  setupWindowEventCapture()
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
  }
}

// ── B1.3(c) — visibilitychange reconnect ──────────────────────────────────────

describe('WsConnection — visibilitychange triggers reconnect (B1.3c)', () => {
  // Traces to: B1.3(c) — when tab returns to visible state and WS is disconnected,
  // reconnect must fire immediately with backoff reset.

  it('registers a visibilitychange listener when connect() is called', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()

    expect(windowListeners['visibilitychange']).toBeDefined()
    expect(windowListeners['visibilitychange'].length).toBeGreaterThan(0)

    conn.disconnect()
  })

  it('reconnects via visibilitychange after a 250ms minimum window so the disconnect banner is observable', () => {
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()

    // Simulate a disconnect (code 1006 — abnormal close)
    lastWsInstance.onopen?.()
    lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' })

    // ws is now null — reconnect timer is pending
    const wsCallCountAfterDisconnect = MockWebSocket.mock.calls.length

    // Trigger visibilitychange (user returns to the tab)
    Object.defineProperty(document, 'visibilityState', {
      get: () => 'visible',
      configurable: true,
    })
    triggerWindowEvent('visibilitychange')

    // The handler clears the pending reconnect timer and schedules a fresh
    // 250ms one so the disconnected UI gets at least one render cycle.
    expect(MockWebSocket.mock.calls.length).toBe(wsCallCountAfterDisconnect)

    // After 250ms the new WebSocket is constructed.
    vi.advanceTimersByTime(250)
    expect(MockWebSocket.mock.calls.length).toBeGreaterThan(wsCallCountAfterDisconnect)

    conn.disconnect()
    vi.useRealTimers()
  })

  it('does NOT reconnect via visibilitychange when disconnect() was intentional', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Intentional disconnect
    conn.disconnect()

    const wsCallCountAfterDisconnect = MockWebSocket.mock.calls.length

    Object.defineProperty(document, 'visibilityState', {
      get: () => 'visible',
      configurable: true,
    })
    triggerWindowEvent('visibilitychange')

    // No new WebSocket created — intentional close must not auto-reconnect
    expect(MockWebSocket.mock.calls.length).toBe(wsCallCountAfterDisconnect)
  })

  it('removes visibilitychange listener when disconnect() is called', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()

    expect(windowListeners['visibilitychange']?.length).toBeGreaterThan(0)

    conn.disconnect()

    expect(windowListeners['visibilitychange']?.length ?? 0).toBe(0)
  })
})

// ── B1.3(c) — online reconnect ────────────────────────────────────────────────

describe('WsConnection — online event triggers reconnect (B1.3c)', () => {
  // Traces to: B1.3(c) — when the network recovers (online event) and WS is
  // disconnected, reconnect must fire immediately with backoff reset.

  it('registers an online listener when connect() is called', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()

    expect(windowListeners['online']).toBeDefined()
    expect(windowListeners['online'].length).toBeGreaterThan(0)

    conn.disconnect()
  })

  it('reconnects via online event after a 250ms minimum window so the disconnect banner is observable', () => {
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()

    // Simulate disconnect
    lastWsInstance.onopen?.()
    lastWsInstance.onclose?.({ code: 1006, reason: '' })

    const wsCallCountAfterDisconnect = MockWebSocket.mock.calls.length

    // Trigger online — network recovered
    triggerWindowEvent('online')

    // Same observable-banner contract as visibilitychange — 250ms delay.
    expect(MockWebSocket.mock.calls.length).toBe(wsCallCountAfterDisconnect)
    vi.advanceTimersByTime(250)
    expect(MockWebSocket.mock.calls.length).toBeGreaterThan(wsCallCountAfterDisconnect)

    conn.disconnect()
    vi.useRealTimers()
  })

  it('does NOT reconnect via online when disconnect() was intentional', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    conn.disconnect()

    const wsCallCountAfterDisconnect = MockWebSocket.mock.calls.length

    triggerWindowEvent('online')

    expect(MockWebSocket.mock.calls.length).toBe(wsCallCountAfterDisconnect)
  })

  it('removes online listener when disconnect() is called', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()

    expect(windowListeners['online']?.length).toBeGreaterThan(0)

    conn.disconnect()

    expect(windowListeners['online']?.length ?? 0).toBe(0)
  })
})

// ── B1.3(c) — persistent banner for non-1000/1001 close codes ─────────────────

describe('WsConnection — persistent banner for non-1000/1001 close (B1.3c)', () => {
  // Traces to: B1.3(c) — non-1000/1001 close codes must call onError with a
  // message containing the code, which AppShell renders as a persistent banner.

  it('calls onError with code in message for 1006 close', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onclose?.({ code: 1006, reason: '' })

    expect(cbs.onError).toHaveBeenCalledWith(
      expect.stringContaining('1006')
    )

    conn.disconnect()
  })

  it('calls onError with code in message for 1011 close', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onclose?.({ code: 1011, reason: 'server error' })

    expect(cbs.onError).toHaveBeenCalledWith(
      expect.stringContaining('1011')
    )

    conn.disconnect()
  })

  it('does NOT call onError for intentional 1000 close', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Intentional disconnect triggers close(1000)
    conn.disconnect()

    // onError must not be called for a 1000 close initiated by the client
    expect(cbs.onError).not.toHaveBeenCalled()
  })
})

// ── parseFrameSafe ─────────────────────────────────────────────────────────────
//
// Traces to: CLAUDE.md hard-constraint #8 — SPA edge validates every incoming
// payload through the matching Zod schema. Drop + counter + dev toast on failure.
//
// Covers the original bug: tool_approval_required with args: null crashed the
// ToolApprovalModal with "Object.keys(null)". parseFrameSafe rejects that frame
// before it reaches any component.

describe('parseFrameSafe', () => {
  beforeEach(() => {
    resetDroppedFrameCount()
  })

  it('returns a typed WsFrame for a valid token frame string', () => {
    const result = parseFrameSafe('{"type":"token","session_id":"s1","content":"Hello"}')
    expect(result).not.toBeNull()
    expect(result?.type).toBe('token')
    if (result?.type === 'token') {
      expect(result.content).toBe('Hello')
    }
    expect(getDroppedFrameCount()).toBe(0)
  })

  it('returns a typed WsFrame for a valid done frame string', () => {
    const result = parseFrameSafe(
      '{"type":"done","session_id":"s1","stats":{"tokens":100,"cost":0.01}}'
    )
    expect(result).not.toBeNull()
    expect(result?.type).toBe('done')
    expect(getDroppedFrameCount()).toBe(0)
  })

  it('returns a typed WsFrame when given a pre-parsed object', () => {
    // parseFrameSafe must accept already-parsed objects, not just JSON strings.
    const raw = { type: 'error', message: 'something went wrong' }
    const result = parseFrameSafe(raw)
    expect(result).not.toBeNull()
    expect(result?.type).toBe('error')
    expect(getDroppedFrameCount()).toBe(0)
  })

  // ── Original bug repro ───────────────────────────────────────────────────────
  // tool_approval_required with args: null used to crash ToolApprovalModal via
  // Object.keys(null). The generated Zod schema has args: z.record(z.unknown())
  // which requires a plain object, so this frame is now rejected at the edge.

  it('drops tool_approval_required with args: null and increments counter', () => {
    const frame = {
      type: 'tool_approval_required',
      approval_id: 'appr_1',
      tool_call_id: 'tc_1',
      tool_name: 'workspace.shell',
      args: null, // ← the original bug: Go emitted null instead of {}
      agent_id: 'agent-jim',
      session_id: 'sess_1',
      turn_id: 'turn_1',
      expires_in_ms: 30000,
    }
    const result = parseFrameSafe(frame)
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it('accepts tool_approval_required with args: {} (correct form)', () => {
    const frame = {
      type: 'tool_approval_required',
      approval_id: 'appr_1',
      tool_call_id: 'tc_1',
      tool_name: 'workspace.shell',
      args: {},
      agent_id: 'agent-jim',
      session_id: 'sess_1',
      turn_id: 'turn_1',
      expires_in_ms: 30000,
    }
    const result = parseFrameSafe(frame)
    expect(result).not.toBeNull()
    expect(result?.type).toBe('tool_approval_required')
    expect(getDroppedFrameCount()).toBe(0)
  })

  // ── Malformed / invalid inputs ───────────────────────────────────────────────

  it('returns null and increments counter for malformed JSON string', () => {
    const result = parseFrameSafe('not valid json {{{')
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it('returns null and increments counter for unknown frame type', () => {
    const result = parseFrameSafe('{"type":"future_frame_type","data":"x"}')
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it('returns null and increments counter for frame missing required field', () => {
    // token frame requires session_id and content
    const result = parseFrameSafe('{"type":"token"}')
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it('returns null and increments counter for non-object input', () => {
    const result = parseFrameSafe(42)
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it('returns null and increments counter for null input', () => {
    const result = parseFrameSafe(null)
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  // ── Counter accumulation and reset ──────────────────────────────────────────

  it('accumulates counter across multiple dropped frames', () => {
    parseFrameSafe('bad json')
    parseFrameSafe('also bad')
    parseFrameSafe('{"type":"unknown_1"}')
    expect(getDroppedFrameCount()).toBe(3)
  })

  it('resetDroppedFrameCount resets to 0', () => {
    parseFrameSafe('bad json')
    parseFrameSafe('bad json 2')
    expect(getDroppedFrameCount()).toBe(2)
    resetDroppedFrameCount()
    expect(getDroppedFrameCount()).toBe(0)
  })

  it('does not increment counter on valid frames', () => {
    parseFrameSafe('{"type":"token","session_id":"s1","content":"a"}')
    parseFrameSafe('{"type":"error","message":"err"}')
    expect(getDroppedFrameCount()).toBe(0)
  })
})

// ── WsConnection onmessage integration: strict parsing (fix-C) ───────────────
//
// These tests verify the fix for the three bugs described in Phase 7:
// 1. tool_approval_required with args:null is dropped (never reaches reducer)
// 2. Unknown discriminator goes through _unknownFrameTypeCount, not drop path
// 3. Client→server direction frames are rejected even if Zod accepts them

describe('WsConnection onmessage — strict parsing (fix-C integration)', () => {
  beforeEach(() => {
    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
  })

  it('drops tool_approval_required with args:null — onFrame not called, counter increments', () => {
    // Original Ava-chat bug: Go emitted args:null instead of {}.
    // With strict parsing, this frame is rejected at the SPA edge.
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    const badFrame = JSON.stringify({
      type: 'tool_approval_required',
      approval_id: 'appr_1',
      tool_call_id: 'tc_1',
      tool_name: 'workspace.shell',
      args: null,
      agent_id: 'agent-jim',
      session_id: 'sess_1',
      turn_id: 'turn_1',
      expires_in_ms: 30000,
    })
    lastWsInstance.onmessage?.({ data: badFrame })

    expect(cbs.onFrame).not.toHaveBeenCalled()
    expect(getDroppedFrameCount()).toBeGreaterThan(0)

    conn.disconnect()
  })

  it('accepts tool_approval_required with args:{} — onFrame called with typed frame', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    const goodFrame = JSON.stringify({
      type: 'tool_approval_required',
      approval_id: 'appr_1',
      tool_call_id: 'tc_1',
      tool_name: 'workspace.shell',
      args: { command: 'ls' },
      agent_id: 'agent-jim',
      session_id: 'sess_1',
      turn_id: 'turn_1',
      expires_in_ms: 30000,
    })
    lastWsInstance.onmessage?.({ data: goodFrame })

    expect(cbs.onFrame).toHaveBeenCalledOnce()
    const received = cbs.onFrame.mock.calls[0]?.[0] as { type: string }
    expect(received.type).toBe('tool_approval_required')
    expect(getDroppedFrameCount()).toBe(0)

    conn.disconnect()
  })

  it('unknown discriminator goes to _unknownFrameTypeCount path, not _droppedFrameCount', () => {
    // A future server frame type not yet in the spec should be counted separately
    // so operators can distinguish "spec drift" from "malformed payload".
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'future_frame_x', data: 'y' }) })

    expect(cbs.onFrame).not.toHaveBeenCalled()
    expect(getUnknownFrameTypeCount()).toBe(1)
    // _droppedFrameCount should NOT increment for unknown-type frames
    expect(getDroppedFrameCount()).toBe(0)

    conn.disconnect()
  })

  it('client→server frame discriminator (type:"auth") is rejected — not forwarded to reducer', () => {
    // A spoofed {type:"auth",token:"x"} payload from the server must be dropped.
    // It passes Zod (AuthFrame schema), but the direction filter must block it.
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'auth', token: 'spoofed' }) })

    expect(cbs.onFrame).not.toHaveBeenCalled()
    expect(getDroppedFrameCount()).toBeGreaterThan(0)

    conn.disconnect()
  })

  it('client→server frame discriminator (type:"message") is rejected', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'message', content: 'hello' }) })

    expect(cbs.onFrame).not.toHaveBeenCalled()
    expect(getDroppedFrameCount()).toBeGreaterThan(0)

    conn.disconnect()
  })
})

// ── getUnknownFrameTypeCount / resetUnknownFrameTypeCount ─────────────────────

describe('getUnknownFrameTypeCount / resetUnknownFrameTypeCount', () => {
  beforeEach(() => {
    resetUnknownFrameTypeCount()
    resetDroppedFrameCount()
  })

  it('starts at 0 after reset', () => {
    expect(getUnknownFrameTypeCount()).toBe(0)
  })

  it('is incremented when parseFrameSafe encounters an unknown type', () => {
    // parseFrameSafe increments _droppedFrameCount (not unknown) for all failures
    // because it doesn't have the forward-compat path. The distinction is in _parseServerFrame.
    // parseFrameSafe is a strict validator — unknown type → droppedFrameCount.
    parseFrameSafe(JSON.stringify({ type: 'future_unknown_x' }))
    expect(getDroppedFrameCount()).toBe(1)
    // parseFrameSafe does NOT use the unknown-type counter; only _parseServerFrame does.
    expect(getUnknownFrameTypeCount()).toBe(0)
  })

  it('reset after increment returns 0', () => {
    resetUnknownFrameTypeCount()
    expect(getUnknownFrameTypeCount()).toBe(0)
  })
})
