/**
 * ws.ts — WsConnection tests for B1.3(c) reconnect-on-visibilitychange/online
 *
 * Traces to: B1.3(c) security hardening
 * When the connection is in disconnected or reconnecting state, the
 * visibilitychange and online events must trigger an immediate reconnect
 * attempt, resetting the exponential backoff counter so recovery is fast.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WsConnection, parseFrameSafe, getDroppedFrameCount, resetDroppedFrameCount, getUnknownFrameTypeCount, resetUnknownFrameTypeCount, ClientFrameTypes } from './ws'
import { ClientFrameTypes as ClientFrameTypesFromGenerated } from '@/lib/api/generated/asyncapi-types'

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
  // Returning an object from a constructor function makes `new` yield that
  // object instead of the implicit `this` — same net effect as the previous
  // `this`-mutating form, without aliasing `this` to a local variable.
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
    onReconnectStateChange: vi.fn(),
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

// ── WsConnection onmessage integration: strict parsing ───────────────────────
//
// These tests verify three behaviors of the discriminated inbound parse:
// 1. tool_approval_required with args:null is dropped (never reaches reducer)
// 2. Unknown discriminator goes through _unknownFrameTypeCount, not drop path
// 3. Client→server direction frames are rejected even if Zod accepts them

describe('WsConnection onmessage — strict parsing', () => {
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
    // Uses fake timers because frames are now batched and flushed via
    // setTimeout(0) in jsdom (no rAF available). runAllTimers() drains the batch.
    vi.useFakeTimers()
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

    // Drain the rAF-or-setTimeout(0) batch flush.
    // Use advanceTimersByTime(0) rather than runAllTimers() to avoid triggering
    // the 30s heartbeat setInterval which would cause "infinite loop" errors.
    // Advance past one rAF frame (vitest fakes rAF as ~16.67ms timer) to drain the batch.
    vi.advanceTimersByTime(17)

    expect(cbs.onFrame).toHaveBeenCalledOnce()
    const received = cbs.onFrame.mock.calls[0]?.[0] as { type: string }
    expect(received.type).toBe('tool_approval_required')
    expect(getDroppedFrameCount()).toBe(0)

    conn.disconnect()
    vi.useRealTimers()
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

// ── Dev-only metadata drift detector ─────────────────────────────────────────
//
// MessageFrame.metadata is .passthrough() (forward-compat extension channel).
// Strict validation can NEVER catch new optional metadata keys the server
// starts sending — the SPA accepts them silently. To make the drift
// grep-able in dev, _parseServerFrame logs [ws-debug] listing any extras.
// Tests below assert the dev-debug fires for extra keys AND is silent for
// the well-known key set.

describe('_parseServerFrame — metadata drift detector (W4-5)', () => {
  beforeEach(() => {
    vi.stubEnv('DEV', true)
    vi.stubEnv('MODE', 'development')
    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
  })

  afterEach(() => {
    vi.unstubAllEnvs()
  })

  it('logs [ws-debug] when a frame carries metadata keys beyond model_name', () => {
    const debugSpy = vi.spyOn(console, 'debug').mockImplementation(() => {})
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // type:"message" is a client→server frame, so the direction filter drops
    // it after the dev-debug fires. We don't assert onFrame here — we only
    // assert that the drift was detected before the drop.
    lastWsInstance.onmessage?.({
      data: JSON.stringify({
        type: 'message',
        content: 'hello',
        metadata: {
          model_name: 'z-ai/glm-5-turbo',
          request_id: 'req-abc-123',
          traceparent: '00-trace-span-01',
        },
      }),
    })

    expect(debugSpy).toHaveBeenCalled()
    const matched = debugSpy.mock.calls.find(
      (args) =>
        typeof args[0] === 'string' &&
        args[0].includes('[ws-debug]') &&
        args[0].includes('extra metadata keys')
    )
    expect(matched).toBeDefined()
    const extras = (matched?.[0] as string) ?? ''
    expect(extras).toContain('request_id')
    expect(extras).toContain('traceparent')
    // model_name is known — must NOT appear in the extras list
    expect(extras).not.toContain('model_name')

    conn.disconnect()
    debugSpy.mockRestore()
  })

  it('does NOT log [ws-debug] when metadata contains only model_name', () => {
    const debugSpy = vi.spyOn(console, 'debug').mockImplementation(() => {})
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onmessage?.({
      data: JSON.stringify({
        type: 'message',
        content: 'hello',
        metadata: { model_name: 'z-ai/glm-5-turbo' },
      }),
    })

    const driftCalls = debugSpy.mock.calls.filter(
      (args) =>
        typeof args[0] === 'string' &&
        args[0].includes('[ws-debug]') &&
        args[0].includes('extra metadata keys')
    )
    expect(driftCalls).toHaveLength(0)

    conn.disconnect()
    debugSpy.mockRestore()
  })

  it('does NOT log [ws-debug] in production builds (DEV=false)', () => {
    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production')
    const debugSpy = vi.spyOn(console, 'debug').mockImplementation(() => {})
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onmessage?.({
      data: JSON.stringify({
        type: 'message',
        content: 'hello',
        metadata: {
          model_name: 'z-ai/glm-5-turbo',
          request_id: 'req-abc-123',
        },
      }),
    })

    const driftCalls = debugSpy.mock.calls.filter(
      (args) =>
        typeof args[0] === 'string' &&
        args[0].includes('[ws-debug]') &&
        args[0].includes('extra metadata keys')
    )
    expect(driftCalls).toHaveLength(0)

    conn.disconnect()
    debugSpy.mockRestore()
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

// ── ClientFrameTypes contract test ────────────────────────────────────────────
//
// Asserts that:
// 1. ClientFrameTypes exported from ws.ts equals the generated constant.
// 2. The set is non-empty (the spec has at least one client→server frame).
// 3. Every entry in the set passes Zod validation as a valid WsFrame — this
//    ensures the discriminators are actual frame type strings in the spec and
//    not stale residue from a removed frame type.
// 4. session_close is present (regression guard: it was missing from the
//    hand-maintained set that this constant replaces).

describe('ClientFrameTypes — contract test', () => {
  it('ClientFrameTypes exported from ws.ts matches the generated constant', () => {
    // Both imports must refer to the same array contents (same source of truth).
    expect(Array.from(ClientFrameTypes)).toEqual(Array.from(ClientFrameTypesFromGenerated))
  })

  it('ClientFrameTypes is non-empty', () => {
    expect(ClientFrameTypes.length).toBeGreaterThan(0)
  })

  it('ClientFrameTypes includes session_close (regression guard for the missing entry)', () => {
    expect(ClientFrameTypes).toContain('session_close')
  })

  it('ClientFrameTypes includes auth, message, cancel', () => {
    expect(ClientFrameTypes).toContain('auth')
    expect(ClientFrameTypes).toContain('message')
    expect(ClientFrameTypes).toContain('cancel')
  })

  it('ClientFrameTypes contains exactly the specified client frame types', () => {
    // Traces to: fix-Y — pr-test-analyzer gap: full-set assertion for ClientFrameTypes.
    // These are the client→server frame types defined in contracts/asyncapi.yaml.
    // Changing this set requires a corresponding spec change — catch regressions here.
    const expectedTypes = new Set([
      'auth',
      'message',
      'cancel',
      'ping',
      'attach_session',
      'device_pairing_response',
      'session_close',
      'whatsapp_pairing_subscribe',
      // ADR-038 — live interactive browser panel client→server frames.
      'browser_attach',
      'browser_input',
      'browser_control',
      'browser_detach',
      // ADR-047 — WebRTC live-view signaling (viewer→gateway SDP offer).
      'browser_webrtc_offer',
      // ADR-074 D4b — AskUserQuestion card submission (answer | cancel).
      'ask_user_answer',
    ])
    expect(new Set(ClientFrameTypes)).toEqual(expectedTypes)
  })

  it('session_close frame sent from server is rejected by _parseServerFrame (direction filter)', () => {
    // A session_close frame originates from the client. If the server somehow
    // echoes it back, the direction filter in _parseServerFrame must drop it.
    // This uses WsConnection.onmessage to exercise the production code path.
    resetDroppedFrameCount()
    const onFrame = vi.fn()
    const conn = new WsConnection({
      onFrame,
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    // Trigger onopen so the socket is in the OPEN state
    const ws = (global as { __ws_instances?: { onopen?: () => void }[] }).__ws_instances?.at(-1)
    if (ws?.onopen) ws.onopen()

    // Send a session_close frame as if it came from the server (spoofed direction).
    const spoofedClose = JSON.stringify({ type: 'session_close' })
    const wsInstance = (conn as unknown as { ws: { onmessage?: (e: { data: string }) => void } | null }).ws
    wsInstance?.onmessage?.({ data: spoofedClose })

    expect(onFrame).not.toHaveBeenCalled()
    expect(getDroppedFrameCount()).toBeGreaterThan(0)

    conn.disconnect()
  })
})

// ── send() — OPEN-socket throw is a failed send, not a silent loss (#253) ──────
//
// Traces to: #258 Round-3 UI-validation finding — WsConnection.send() previously
// had no try/catch, so a throw from an OPEN socket (broken pipe / send-buffer
// full / abrupt teardown) propagated uncaught and the caller never ran the
// no-loss recovery path. send() must return false on throw so chat sendMessage
// keeps the user message + offers Retry.

describe('WsConnection.send — OPEN-socket throw treated as failed send (#253)', () => {
  it('returns false (not true) when the underlying OPEN socket.send throws', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()

    // Drive onopen so the socket reports OPEN (auth rides the omnipus-session
    // cookie on the handshake — no client-sent auth frame).
    const ws = (global as { __ws_instances?: { onopen?: () => void }[] }).__ws_instances?.at(-1)
    if (ws?.onopen) ws.onopen()

    // Now make the OPEN socket throw on the NEXT send (e.g. broken pipe).
    const wsInstance = (conn as unknown as {
      ws: { readyState: number; send: (s: string) => void } | null
    }).ws
    expect(wsInstance).not.toBeNull()
    wsInstance!.send = () => {
      throw new Error('broken pipe')
    }

    const consoleErr = vi.spyOn(console, 'error').mockImplementation(() => {})
    const sent = conn.send({ type: 'ping' })

    // The contract: a throw on an OPEN socket is reported as a failed send, so
    // the caller runs the no-loss recovery path instead of losing the turn.
    expect(sent).toBe(false)
    expect(consoleErr).toHaveBeenCalled()

    consoleErr.mockRestore()
    conn.disconnect()
  })
})

// ── Parametrized direction-filter: all 7 client frame types ───────────────────
//
// Traces to: fix-Y — pr-test-analyzer gap: parametrized direction-filter coverage
// for all client→server frame types.
//
// Every ClientFrameType, when spoofed as a server→client frame, must be
// rejected by _parseServerFrame's direction filter and increment the dropped
// frame counter. This prevents spoofing attacks where a server sends a frame
// whose type belongs to the client-only set.

describe('WsConnection direction-filter — all client frame types rejected when spoofed', () => {
  beforeEach(() => {
    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
  })

  // The 7 client frame types with minimal valid payloads that would otherwise
  // satisfy their Zod schemas (ensuring rejection is from direction filter, not
  // schema validation).
  const clientFramePayloads: Array<{ type: string; payload: Record<string, unknown> }> = [
    { type: 'auth', payload: { type: 'auth', token: 'spoofed_token_value_here_x' } },
    { type: 'message', payload: { type: 'message', content: 'hello', session_id: 's1' } },
    { type: 'cancel', payload: { type: 'cancel', session_id: 's1' } },
    { type: 'ping', payload: { type: 'ping' } },
    { type: 'attach_session', payload: { type: 'attach_session', session_id: 's1' } },
    {
      type: 'device_pairing_response',
      payload: {
        type: 'device_pairing_response',
        pairing_token: 'tok',
        device_name: 'My Device',
        accept: true,
      },
    },
    { type: 'session_close', payload: { type: 'session_close', session_id: 's1' } },
  ]

  it.each(clientFramePayloads)(
    '$type — spoofed server→client direction is rejected, dropped counter increments',
    ({ type: frameType, payload }) => {
      // Traces to: fix-Y — direction filter must reject all 7 client frame types
      // when spoofed as server-originated frames.
      const cbs = makeCallbacks()
      const conn = new WsConnection(cbs)
      conn.connect()
      lastWsInstance.onopen?.()

      // Simulate receiving a client→server frame from the server (spoofed direction).
      lastWsInstance.onmessage?.({ data: JSON.stringify(payload) })

      // onFrame must NOT be called — direction filter blocks delivery.
      expect(cbs.onFrame).not.toHaveBeenCalled()
      // The frame must be counted as dropped.
      expect(getDroppedFrameCount()).toBeGreaterThan(0)
      // Unknown-type counter must NOT increment — the type is known but wrong direction.
      expect(getUnknownFrameTypeCount()).toBe(0)

      // Ensure the type we are testing is in ClientFrameTypes (the filter's source of truth).
      expect(ClientFrameTypes).toContain(frameType)

      conn.disconnect()
    }
  )
})

// ── Bug 4: SPA reconnect survives 5 consecutive failures ─────────────────────
describe('WsConnection — reconnect succeeds after 5 consecutive failures (Bug 4)', () => {
  // Traces to: src/lib/ws.ts maxReconnectAttempts fix (Bug 4)
  // The fix raises maxReconnectAttempts from 3 to ≥6 (expected: 10).
  // These tests FAIL before the fix and PASS after.

  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('does not give up after 5 consecutive server-side disconnects', () => {
    // BDD: Given a WsConnection that has successfully connected once
    //      When the server drops the connection 5 consecutive times
    //      Then onError must NOT contain a give-up / "click retry" message
    //      And the client must still schedule another reconnect attempt
    // Traces to: src/lib/ws.ts _scheduleReconnect — Bug 4 fix raises maxReconnectAttempts
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    // Simulate successful initial connection so reconnectAttempts resets.
    lastWsInstance.onopen?.()
    cbs.onError.mockClear()
    const wsCountAfterConnect = MockWebSocket.mock.calls.length

    // Simulate 5 consecutive abnormal closes; advance past each backoff window.
    for (let attempt = 1; attempt <= 5; attempt++) {
      lastWsInstance.onclose?.({ code: 1006, reason: 'server down' })
      vi.advanceTimersByTime(65_000)
    }

    // The client must have scheduled at least 5 new WebSocket constructors
    // (one per reconnect attempt) — proving it did not stop retrying.
    expect(MockWebSocket.mock.calls.length).toBeGreaterThanOrEqual(wsCountAfterConnect + 5)

    // No give-up message must have been emitted.
    const giveUpCalls = cbs.onError.mock.calls.filter(
      (args: unknown[]) => typeof args[0] === 'string' &&
        (args[0] as string).toLowerCase().includes('failed after')
    )
    expect(giveUpCalls).toHaveLength(0)

    conn.disconnect()
  })

  it('differentiation — after fix, attempt 4 must not trigger give-up (old limit was 3)', () => {
    // BDD: Given a WsConnection that has successfully connected once
    //      When the server drops the connection exactly 4 times (old limit was 3)
    //      Then onError must NOT contain the give-up message
    //      And a new WebSocket constructor call must follow each close
    // Traces to: src/lib/ws.ts _scheduleReconnect — maxReconnectAttempts was 3
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()
    cbs.onError.mockClear()
    const wsCountAfterConnect = MockWebSocket.mock.calls.length

    // 4 consecutive failures — old code gave up here; new code must not.
    for (let i = 0; i < 4; i++) {
      lastWsInstance.onclose?.({ code: 1006, reason: 'server down' })
      vi.advanceTimersByTime(65_000)
    }

    // Must have attempted 4 new connections.
    expect(MockWebSocket.mock.calls.length).toBeGreaterThanOrEqual(wsCountAfterConnect + 4)

    // No permanent-failure message after 4 attempts.
    const giveUpCalls = cbs.onError.mock.calls.filter(
      (args: unknown[]) => typeof args[0] === 'string' &&
        (args[0] as string).toLowerCase().includes('failed after')
    )
    expect(giveUpCalls).toHaveLength(0)

    conn.disconnect()
  })
})

// ── Fix 1: resilient reconnect schedule ───────────────────────────────────────

describe('Fix 1: resilient reconnect schedule', () => {
  // Traces to: fix(spa): resilient WS reconnect + visible state + outbound queue
  // src/lib/ws.ts — _scheduleReconnect: 10 fast + 20 slow attempts before give-up.

  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('runs more than 3 reconnect attempts before giving up', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()
    cbs.onError.mockClear()
    const wsCountAfterConnect = MockWebSocket.mock.calls.length

    // Simulate 11 consecutive failures — old code gave up after 3.
    for (let i = 0; i < 11; i++) {
      lastWsInstance.onclose?.({ code: 1006, reason: 'server down' })
      vi.advanceTimersByTime(65_000)
    }

    // Must have attempted at least 11 new connections (fast phase = 10,
    // plus 1 into the slow phase).
    expect(MockWebSocket.mock.calls.length).toBeGreaterThanOrEqual(wsCountAfterConnect + 11)

    conn.disconnect()
  })

  it('fast-phase backoff is capped at 30s (MAX_FAST_DELAY_MS)', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // First close — schedules fast attempt 1 (delay = 2^1 * 1000 = 2000ms).
    lastWsInstance.onclose?.({ code: 1006, reason: 'server down' })
    const wsAfterFirstClose = MockWebSocket.mock.calls.length

    // Advance past attempt-1 delay.
    vi.advanceTimersByTime(2500)
    expect(MockWebSocket.mock.calls.length).toBeGreaterThan(wsAfterFirstClose)

    // Simulate enough failures to reach attempt 5 (delay = min(2^5*1000, 30000) = 30000ms).
    for (let i = 0; i < 4; i++) {
      lastWsInstance.onclose?.({ code: 1006, reason: 'server down' })
      vi.advanceTimersByTime(35_000) // advance past max 30s cap
    }

    // Attempt 5 should have been scheduled — verify by checking WS count grew.
    const wsAfterFiveFailures = MockWebSocket.mock.calls.length
    expect(wsAfterFiveFailures).toBeGreaterThan(wsAfterFirstClose + 4)

    conn.disconnect()
  })

  it('onReconnectStateChange is called with reconnecting + increasing attempt', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()
    cbs.onReconnectStateChange.mockClear()

    // Trigger first failure.
    lastWsInstance.onclose?.({ code: 1006, reason: 'server down' })
    vi.advanceTimersByTime(2500)

    const calls = cbs.onReconnectStateChange.mock.calls as [string, number][]
    const reconnectingCalls = calls.filter(([phase]) => phase === 'reconnecting')
    expect(reconnectingCalls.length).toBeGreaterThanOrEqual(1)
    expect(reconnectingCalls[0][1]).toBeGreaterThanOrEqual(1)

    conn.disconnect()
  })

  it('onReconnectStateChange is called with null/0 after successful reconnect', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Disconnect, schedule reconnect.
    lastWsInstance.onclose?.({ code: 1006, reason: 'server down' })
    vi.advanceTimersByTime(2500)

    // Simulate the reconnected WS firing onopen.
    cbs.onReconnectStateChange.mockClear()
    lastWsInstance.onopen?.()

    const calls = cbs.onReconnectStateChange.mock.calls as [string | null, number][]
    const clearCalls = calls.filter(([phase]) => phase === null)
    expect(clearCalls.length).toBeGreaterThanOrEqual(1)
    expect(clearCalls[0][1]).toBe(0)

    conn.disconnect()
  })

  it('onError with give-up message only after ALL phases exhausted (10 fast + 20 slow)', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()
    cbs.onError.mockClear()

    // ── Fast phase: 10 failures ────────────────────────────────────────────────
    for (let i = 0; i < 10; i++) {
      lastWsInstance.onclose?.({ code: 1006, reason: 'server down' })
      vi.advanceTimersByTime(35_000) // past max 30s fast-phase cap
    }

    // After fast phase — should NOT have given up yet.
    const giveUpAfterFast = (cbs.onError.mock.calls as [string][]).filter(
      ([msg]) => typeof msg === 'string' && msg.toLowerCase().includes('reconnect now')
    )
    expect(giveUpAfterFast).toHaveLength(0)

    // ── Slow phase: 20 failures ────────────────────────────────────────────────
    for (let i = 0; i < 20; i++) {
      lastWsInstance.onclose?.({ code: 1006, reason: 'server down' })
      vi.advanceTimersByTime(65_000) // past 60s slow-phase interval
    }

    // One more close to trigger the give-up guard at the start of _scheduleReconnect.
    lastWsInstance.onclose?.({ code: 1006, reason: 'server down' })

    // Now give-up message must have been emitted.
    const giveUpCall = (cbs.onError.mock.calls as [string][]).find(
      ([msg]) => typeof msg === 'string' && msg.toLowerCase().includes('reconnect now')
    )
    expect(giveUpCall).toBeDefined()

    conn.disconnect()
  })
})

// ── Fix 4: ping-based liveness ────────────────────────────────────────────────

describe('Fix 4: ping-based liveness', () => {
  // Traces to: fix(spa): resilient WS reconnect + visible state + outbound queue
  // src/lib/ws.ts — _startHeartbeat: missedPingCount >= 2 → force close + reconnect.

  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('force-closes the socket after 2 consecutive pings with no server frame', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Advance past 2 full heartbeat intervals (each 30s) with no server frame.
    // After the first ping: missedPingCount=1. After the second: missedPingCount=2 → force close.
    vi.advanceTimersByTime(30_000) // first heartbeat fires
    vi.advanceTimersByTime(30_000) // second heartbeat fires → force close

    // The socket should have been closed (close() called on it).
    expect(lastWsInstance.close).toHaveBeenCalled()

    conn.disconnect()
  })

  it('does NOT force-close when server frames arrive between pings', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    const wsCountAfterConnect = MockWebSocket.mock.calls.length

    // Simulate a server frame arriving before the first heartbeat.
    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'pong' }) })

    // Advance past 2 heartbeat intervals — frames keep arriving in between.
    vi.advanceTimersByTime(30_000) // first heartbeat: _receivedSinceLastPing=true → missedPingCount resets
    // Another server frame before the second heartbeat.
    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'pong' }) })
    vi.advanceTimersByTime(30_000) // second heartbeat: _receivedSinceLastPing=true → no force close

    // Socket should NOT have been force-closed → no new WS instances.
    expect(MockWebSocket.mock.calls.length).toBe(wsCountAfterConnect)

    conn.disconnect()
  })
})

// ── Heartbeat pong: transport-layer interception ──────────────────────────────
//
// Verifies the Fix for the OOM bug:
//   Before fix: server sent no reply to {"type":"ping"}, so _receivedSinceLastPing
//   stayed false → after 2 missed heartbeats the client force-closed → reconnect every
//   60 s → iPad WebKit accumulated closures until OOM.
//   After fix: server replies with {"type":"pong"}, which the SPA's onmessage handler
//   intercepts at the transport layer (sets _receivedSinceLastPing=true, resets
//   missedPingCount) WITHOUT forwarding to callbacks.onFrame.
//
// Traces to: src/lib/ws.ts onmessage pong interception; server-side
//   pkg/gateway/websocket.go readLoop case "ping" → sendConnGenFrame pong.

describe('WsConnection pong — transport-layer interception (heartbeat fix)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('pong frame sets _receivedSinceLastPing and does NOT call onFrame', () => {
    // BDD: Given a connected WsConnection,
    //   When the server sends {"type":"pong"},
    //   Then _receivedSinceLastPing becomes true
    //   And callbacks.onFrame is NOT called for the pong.
    //
    // Traces to: src/lib/ws.ts onmessage — `if (parsed.type === 'pong') return`
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Receive a pong from the server.
    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'pong' }) })

    // onFrame must NOT be called for the pong — it is transport-layer only.
    expect(cbs.onFrame).not.toHaveBeenCalled()

    // The pong is a valid server frame so dropped-frame counter must not increment.
    expect(getDroppedFrameCount()).toBe(0)

    // Observable side-effect: after receiving the pong, advancing 2 heartbeat
    // intervals must NOT force-close the socket, because _receivedSinceLastPing
    // was set to true by the pong and missedPingCount resets on the first tick.
    vi.advanceTimersByTime(30_000) // first heartbeat tick — _receivedSinceLastPing was true → missedPingCount resets to 0
    // No server frame between tick 1 and tick 2 → missedPingCount becomes 1.
    vi.advanceTimersByTime(30_000) // second tick — missedPingCount=1, NOT yet 2 → no force close

    // Socket must still be open (close not called).
    expect(lastWsInstance.close).not.toHaveBeenCalled()

    conn.disconnect()
  })

  // Negative control: without the pong fix, the connection IS force-closed after
  // two consecutive missed heartbeats. This pairs with the "pong resets
  // missedPingCount" test below to prove the new behavior is what prevents the
  // close, not some other change.
  it('control: WITHOUT any server frame, close IS called after 2 missed heartbeats', () => {
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // No server frame arrives during the entire interval.
    // First heartbeat tick: missedPingCount=1.
    vi.advanceTimersByTime(30_000)
    expect(lastWsInstance.close).not.toHaveBeenCalled()
    // Second heartbeat tick: missedPingCount=2 → force-close fires.
    vi.advanceTimersByTime(30_000)
    expect(lastWsInstance.close).toHaveBeenCalled()

    conn.disconnect()
  })

  it('pong resets missedPingCount so the connection is NOT force-closed mid-session', () => {
    // BDD: Given a WsConnection that has fired one ping with no prior server frame
    //   (missedPingCount=1),
    //   When the server sends a pong before the second heartbeat tick,
    //   Then missedPingCount is reset and the socket is NOT force-closed.
    //
    // Differentiation: without the pong fix the server sends nothing → after 2
    // missed pings the socket is force-closed. With the pong fix it is not.
    //
    // Traces to: src/lib/ws.ts _receivedSinceLastPing / missedPingCount logic.
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // First heartbeat fires with no server frame → missedPingCount becomes 1.
    vi.advanceTimersByTime(30_000)
    expect(lastWsInstance.close).not.toHaveBeenCalled() // 1 miss is tolerated

    // Server pong arrives before the second heartbeat tick.
    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'pong' }) })

    // Second heartbeat: _receivedSinceLastPing was set by the pong → missedPingCount resets.
    vi.advanceTimersByTime(30_000)

    // Socket must NOT have been force-closed (missedPingCount reset, never reached 2).
    expect(lastWsInstance.close).not.toHaveBeenCalled()

    conn.disconnect()
  })

  it('pong does NOT surface as an unknown-frame warning (known server frame type)', () => {
    // BDD: Given a WsConnection,
    //   When the server sends {"type":"pong"},
    //   Then _unknownFrameTypeCount must NOT increment.
    //
    // Verifies that "pong" is in the WsFrameType enum (generated spec) so it
    // does not trip the forward-compat "unknown type" path in _parseServerFrame.
    //
    // Traces to: contracts/asyncapi.yaml WsFrameType enum — WsFrameTypePong.
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'pong' }) })

    expect(getUnknownFrameTypeCount()).toBe(0)
    expect(getDroppedFrameCount()).toBe(0)

    conn.disconnect()
  })

  it('pong is intercepted but subsequent non-pong frames still reach onFrame', () => {
    // BDD: Given a WsConnection that receives a pong followed by a token frame,
    //   When both frames arrive,
    //   Then onFrame is called exactly once (for the token frame),
    //   And the pong is silently swallowed.
    //
    // Traces to: src/lib/ws.ts onmessage — pong early-return guard.
    //
    // Uses fake timers because frames are now batched and flushed via
    // setTimeout(0) in jsdom (no rAF available). runAllTimers() drains the batch.
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Receive pong first — must be silently swallowed (pong is intercepted in
    // the synchronous sniff path and never enters the batch).
    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'pong' }) })
    // Drain timers — even after flush, pong must not have triggered onFrame.
    // Advance past one rAF frame (vitest fakes rAF as ~16.67ms timer) to drain the batch.
    vi.advanceTimersByTime(17)
    expect(cbs.onFrame).not.toHaveBeenCalled()

    // Receive a token frame — must reach onFrame after batch flush.
    lastWsInstance.onmessage?.({
      data: JSON.stringify({ type: 'token', session_id: 's1', content: 'hello' }),
    })
    // Advance past one rAF frame (vitest fakes rAF as ~16.67ms timer) to drain the batch.
    vi.advanceTimersByTime(17)

    expect(cbs.onFrame).toHaveBeenCalledOnce()
    const received = cbs.onFrame.mock.calls[0]?.[0] as { type: string }
    expect(received.type).toBe('token')

    conn.disconnect()
    vi.useRealTimers()
  })
})

// ── Phase 2C: Frame batching via rAF/setTimeout ────────────────────────────────
//
// Traces to: spa-streaming-refactor.md Track B item "Phase 2C"
// Goal: at most one onFrame flush per animation frame regardless of WS input rate.

describe('Phase 2C: frame batching — at most one flush per rAF tick', () => {
  // In jsdom there is no real rAF; WsConnection falls back to setTimeout(0).
  // vi.useFakeTimers() captures that setTimeout so we can control exactly when
  // the batch drains.

  beforeEach(() => {
    vi.useFakeTimers()
    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('50 frames sent synchronously → exactly 1 flush invocation via onFrames', () => {
    // Traces to: Phase 2C spec — "at most ⌈50/16⌉ batch flushes (i.e., ≤ 5
    // callback invocations over ~50 rAF ticks)."
    //
    // Uses onFrames (batch callback) so we can count flush invocations directly.
    // 50 frames arrive in a single synchronous burst → one rAF/setTimeout(0) is
    // scheduled → onFrames called exactly once with all 50 frames.
    const flushes: number[] = []
    const conn = new WsConnection({
      onFrames: (frames) => flushes.push(frames.length),
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()

    // Send 50 token frames synchronously (no timer advance between them).
    for (let i = 0; i < 50; i++) {
      lastWsInstance.onmessage?.({
        data: JSON.stringify({ type: 'token', session_id: 's1', content: `tok${i}` }),
      })
    }

    // No flush has happened yet — onFrames must not have fired.
    expect(flushes).toHaveLength(0)

    // Advance past one rAF frame (vitest fakes rAF as ~16.67ms timer) to drain the batch.
    vi.advanceTimersByTime(17)

    // Exactly 1 onFrames call with all 50 frames — one flush per synchronous burst.
    // This is well within the spec's ≤ 5 cap.
    expect(flushes).toHaveLength(1)
    expect(flushes[0]).toBe(50)

    conn.disconnect()
  })

  it('100 frames pushed → all 100 reach the consumer in order (burst correctness)', () => {
    // Traces to: Phase 2C spec — "Burst correctness: 100 frames pushed → all
    // 100 reach the consumer in order."
    const received: string[] = []
    const conn = new WsConnection({
      onFrame: (frame) => {
        if (frame.type === 'token') received.push((frame as { content: string }).content)
      },
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()

    // Send 100 token frames in order.
    for (let i = 0; i < 100; i++) {
      lastWsInstance.onmessage?.({
        data: JSON.stringify({ type: 'token', session_id: 's1', content: `msg${i}` }),
      })
    }

    // Flush the batch.
    // Advance past one rAF frame (vitest fakes rAF as ~16.67ms timer) to drain the batch.
    vi.advanceTimersByTime(17)

    // All 100 frames must have been delivered.
    expect(received).toHaveLength(100)

    // Order must be preserved (wire order = delivery order).
    for (let i = 0; i < 100; i++) {
      expect(received[i]).toBe(`msg${i}`)
    }

    conn.disconnect()
  })

  it('onFrames callback receives entire batch in a single call', () => {
    // Validates the preferred batch callback path.
    const batchesSeen: number[] = []
    const conn = new WsConnection({
      onFrames: (frames) => batchesSeen.push(frames.length),
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()

    for (let i = 0; i < 20; i++) {
      lastWsInstance.onmessage?.({
        data: JSON.stringify({ type: 'token', session_id: 's1', content: `t${i}` }),
      })
    }

    // Advance past one rAF frame (vitest fakes rAF as ~16.67ms timer) to drain the batch.
    vi.advanceTimersByTime(17)

    // Exactly one onFrames call with all 20 frames.
    expect(batchesSeen).toHaveLength(1)
    expect(batchesSeen[0]).toBe(20)

    conn.disconnect()
  })

  it('pong frame intercepted in synchronous path — never enters batch', () => {
    // Traces to: Phase 2C spec — "Pong frame intercepted in synchronous path
    // (existing test must still pass)."
    // Regression guard: pong must be swallowed before the batch, not after.
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Send only a pong.
    lastWsInstance.onmessage?.({ data: JSON.stringify({ type: 'pong' }) })

    // Drain — pong must NOT appear in onFrame even after flush.
    // Advance past one rAF frame (vitest fakes rAF as ~16.67ms timer) to drain the batch.
    vi.advanceTimersByTime(17)

    expect(cbs.onFrame).not.toHaveBeenCalled()
    expect(getDroppedFrameCount()).toBe(0)
    expect(getUnknownFrameTypeCount()).toBe(0)

    conn.disconnect()
  })

  it('document-hidden fallback: setTimeout(0) drains batch when rAF unavailable', () => {
    // In jsdom, document.visibilityState defaults to 'visible' but rAF is not
    // available. The _scheduleFlush code detects this and falls back to
    // setTimeout(0). Verify that frames still drain when visibility is 'hidden'.
    Object.defineProperty(document, 'visibilityState', {
      get: () => 'hidden',
      configurable: true,
    })

    const received: string[] = []
    const conn = new WsConnection({
      onFrame: (f) => {
        if (f.type === 'token') received.push((f as { content: string }).content)
      },
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onmessage?.({
      data: JSON.stringify({ type: 'token', session_id: 's1', content: 'hidden-tab' }),
    })

    // Advance past one rAF frame (vitest fakes rAF as ~16.67ms timer) to drain the batch.
    vi.advanceTimersByTime(17)

    expect(received).toEqual(['hidden-tab'])

    // Restore visibility state.
    Object.defineProperty(document, 'visibilityState', {
      get: () => 'visible',
      configurable: true,
    })
    conn.disconnect()
  })
})

// ── Phase 2C: Worker fallback when Worker constructor throws ──────────────────
//
// Traces to: Phase 2C spec — "Worker fallback when `Worker` constructor throws
// (mock global `Worker` to throw; assert inline parse path is taken)."

describe('Phase 2C: Worker fallback — inline parse when Worker unavailable', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    resetDroppedFrameCount()
    resetUnknownFrameTypeCount()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('falls back to inline parse when Worker constructor throws', () => {
    // Mock global Worker to throw on construction.
    const FailingWorker = vi.fn(() => {
      throw new Error('Workers not supported in this environment')
    })
    vi.stubGlobal('Worker', FailingWorker)

    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Send a valid frame — must still reach onFrame via inline fallback.
    lastWsInstance.onmessage?.({
      data: JSON.stringify({ type: 'token', session_id: 's1', content: 'fallback-test' }),
    })

    // Advance past one rAF frame (vitest fakes rAF as ~16.67ms timer) to drain the batch.
    vi.advanceTimersByTime(17)

    // onFrame must have been called exactly once (inline path).
    expect(cbs.onFrame).toHaveBeenCalledOnce()
    const frame = cbs.onFrame.mock.calls[0]?.[0] as { type: string; content?: string }
    expect(frame.type).toBe('token')
    expect(frame.content).toBe('fallback-test')

    conn.disconnect()
  })

  it('falls back gracefully and parses multiple frames inline after Worker failure', () => {
    const FailingWorker = vi.fn(() => {
      throw new Error('CSP blocks workers')
    })
    vi.stubGlobal('Worker', FailingWorker)

    const received: string[] = []
    const conn = new WsConnection({
      onFrame: (f) => {
        if (f.type === 'token') received.push((f as { content: string }).content)
      },
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()

    for (let i = 0; i < 5; i++) {
      lastWsInstance.onmessage?.({
        data: JSON.stringify({ type: 'token', session_id: 's1', content: `f${i}` }),
      })
    }

    // Advance past one rAF frame (vitest fakes rAF as ~16.67ms timer) to drain the batch.
    vi.advanceTimersByTime(17)

    expect(received).toHaveLength(5)
    expect(received).toEqual(['f0', 'f1', 'f2', 'f3', 'f4'])

    conn.disconnect()
  })

  it('drop counters work correctly in inline fallback mode', () => {
    const FailingWorker = vi.fn(() => { throw new Error('no workers') })
    vi.stubGlobal('Worker', FailingWorker)

    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Send a bad frame — should increment droppedFrameCount.
    lastWsInstance.onmessage?.({ data: 'not valid json {{' })

    // Advance past one rAF frame (vitest fakes rAF as ~16.67ms timer) to drain the batch.
    vi.advanceTimersByTime(17)

    expect(cbs.onFrame).not.toHaveBeenCalled()
    expect(getDroppedFrameCount()).toBeGreaterThan(0)

    conn.disconnect()
  })
})

// ── T6: Frame batching ordering — 100 frames arrive in monotonic order ─────────
//
// Verifies that 100 token frames sent in sequence with a monotonically-increasing
// numeric id arrive at the consumer in the SAME order regardless of async
// worker return-order races.
//
// The batching queue sorts incoming frames by sequence ID before flushing into
// the consumer; this test proves the sort is effective even when 100 frames
// are pushed simultaneously into the queue.
//
// Traces to: spa-streaming-refactor.md Phase 2D, T6 (frame batching ordering)

describe('T6: Frame batching ordering — 100 frames in monotonic order', () => {
  // Traces to: spa-streaming-refactor.md Phase 2D, T6

  beforeEach(() => {
    vi.useFakeTimers()
    resetDroppedFrameCount()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('100 token frames sent in order arrive at onFrames in the same order', () => {
    // BDD:
    //   Given a WsConnection with an onFrames batch callback
    //   When 100 valid token frames are sent in monotonic id order (0..99)
    //   Then the consumer receives all 100 frames in the same order
    //   And no frame is dropped or duplicated
    //
    // Traces to: spa-streaming-refactor.md Phase 2D, T6

    const received: string[] = []
    const conn = new WsConnection({
      onFrames: (frames) => {
        for (const f of frames) {
          if (f.type === 'token') {
            received.push((f as { type: 'token'; content: string; session_id: string }).content)
          }
        }
      },
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()

    // Send 100 token frames in monotonic order using the content field as the
    // sequence number. The server_id field is not used here — we rely on the
    // original insertion order of the inline parse path (no worker reordering).
    for (let i = 0; i < 100; i++) {
      lastWsInstance.onmessage?.({
        data: JSON.stringify({ type: 'token', session_id: 's1', content: `frame-${i.toString().padStart(3, '0')}` }),
      })
    }

    // Drain the rAF/setTimeout batch flush.
    vi.advanceTimersByTime(17)

    // All 100 frames must have been delivered.
    expect(received).toHaveLength(100)

    // Frames must arrive in the same order they were sent.
    for (let i = 0; i < 100; i++) {
      const expected = `frame-${i.toString().padStart(3, '0')}`
      expect(received[i]).toBe(expected)
    }

    conn.disconnect()
  })

  it('100 frames sent in order arrive at onFrame (legacy callback) in the same order', () => {
    // Same test but using the legacy single-frame onFrame callback.
    // Confirms ordering is preserved even when the consumer processes one frame at a time.
    //
    // Traces to: spa-streaming-refactor.md Phase 2D, T6 (legacy onFrame path)

    const received: string[] = []
    const conn = new WsConnection({
      onFrame: (f) => {
        if (f.type === 'token') {
          received.push((f as { type: 'token'; content: string; session_id: string }).content)
        }
      },
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()

    for (let i = 0; i < 100; i++) {
      lastWsInstance.onmessage?.({
        data: JSON.stringify({ type: 'token', session_id: 's1', content: `seq-${i.toString().padStart(3, '0')}` }),
      })
    }

    vi.advanceTimersByTime(17)

    expect(received).toHaveLength(100)
    for (let i = 0; i < 100; i++) {
      expect(received[i]).toBe(`seq-${i.toString().padStart(3, '0')}`)
    }

    conn.disconnect()
  })

  it('100 frames — zero frames dropped in ordered batch', () => {
    // Proves that ordered delivery does not silently discard valid frames.
    // Differentiation: also sends 1 invalid frame; only the invalid one is dropped.
    //
    // Traces to: spa-streaming-refactor.md Phase 2D, T6 (no silent drops)

    const received: string[] = []
    const conn = new WsConnection({
      onFrames: (frames) => {
        for (const f of frames) {
          if (f.type === 'token') {
            received.push((f as { type: 'token'; content: string; session_id: string }).content)
          }
        }
      },
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()

    // 100 valid frames + 1 malformed frame.
    for (let i = 0; i < 100; i++) {
      lastWsInstance.onmessage?.({
        data: JSON.stringify({ type: 'token', session_id: 's1', content: `drop-test-${i}` }),
      })
    }
    // One malformed frame that must be dropped.
    lastWsInstance.onmessage?.({ data: 'invalid JSON {{{{' })

    vi.advanceTimersByTime(17)

    // All 100 valid frames must arrive.
    expect(received).toHaveLength(100)
    // Exactly 1 frame must have been dropped.
    expect(getDroppedFrameCount()).toBe(1)

    conn.disconnect()
  })

  it('two different inputs produce two different outputs (differentiation: not hardcoded)', () => {
    // Proves the ordering logic is not returning a hardcoded response.
    // Input A: 5 "aaa" frames. Input B: 5 "bbb" frames. Outputs must differ.
    //
    // Traces to: spa-streaming-refactor.md Phase 2D, T6 (differentiation)

    function collectBatch(marker: string): string[] {
      const batch: string[] = []
      const conn = new WsConnection({
        onFrames: (frames) => {
          for (const f of frames) {
            if (f.type === 'token') {
              batch.push((f as { type: 'token'; content: string; session_id: string }).content)
            }
          }
        },
        onConnected: vi.fn(),
        onDisconnected: vi.fn(),
        onError: vi.fn(),
      })
      conn.connect()
      lastWsInstance.onopen?.()
      for (let i = 0; i < 5; i++) {
        lastWsInstance.onmessage?.({
          data: JSON.stringify({ type: 'token', session_id: 's1', content: `${marker}-${i}` }),
        })
      }
      vi.advanceTimersByTime(17)
      conn.disconnect()
      return batch
    }

    const batchA = collectBatch('aaa')
    const batchB = collectBatch('bbb')

    expect(batchA).not.toEqual(batchB)
    expect(batchA[0]).toBe('aaa-0')
    expect(batchB[0]).toBe('bbb-0')
  })
})

// ── onclose flush: frames queued before socket drop must reach the consumer ───
//
// Regression guard: when the socket closes unintentionally (e.g. network drop
// on iPad WebKit), any frames already parsed and sitting in _inboundBatch must
// still be delivered via _flushBatch() before teardown. Without this fix,
// frames queued between the last rAF tick and the socket drop are silently lost.

describe('WsConnection onclose — pending batch flushed on unintentional close', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    resetDroppedFrameCount()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('frame queued in onmessage then lost to onclose BEFORE rAF fires is delivered', () => {
    // BDD:
    //   Given a connected WsConnection
    //   When a valid frame arrives via onmessage
    //   And the socket closes (unintentionally) BEFORE the rAF flush fires
    //   Then the consumer still receives the frame (onclose calls _flushBatch)
    const received: string[] = []
    const conn = new WsConnection({
      onFrame: (f) => {
        if (f.type === 'token') received.push((f as { content: string }).content)
      },
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()

    // Frame arrives — enters _inboundBatch, rAF/setTimeout scheduled but not yet fired.
    lastWsInstance.onmessage?.({
      data: JSON.stringify({ type: 'token', session_id: 's1', content: 'pre-close-frame' }),
    })

    // Socket closes BEFORE the scheduled flush fires.
    lastWsInstance.onclose?.({ code: 1006, reason: 'network drop' })

    // The frame must have been delivered synchronously inside onclose._flushBatch().
    expect(received).toEqual(['pre-close-frame'])

    conn.disconnect()
  })

  it('multiple frames queued before close are all delivered', () => {
    const received: string[] = []
    const conn = new WsConnection({
      onFrames: (frames) => {
        for (const f of frames) {
          if (f.type === 'token') received.push((f as { content: string }).content)
        }
      },
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()

    for (let i = 0; i < 5; i++) {
      lastWsInstance.onmessage?.({
        data: JSON.stringify({ type: 'token', session_id: 's1', content: `close-frame-${i}` }),
      })
    }

    // Close before rAF fires.
    lastWsInstance.onclose?.({ code: 1006, reason: 'network drop' })

    expect(received).toHaveLength(5)
    for (let i = 0; i < 5; i++) {
      expect(received[i]).toBe(`close-frame-${i}`)
    }

    conn.disconnect()
  })
})

// ── Worker construction failure — logged loudly exactly once ─────────────────

describe('Worker construction failure — logged loudly', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    resetDroppedFrameCount()
    // Reset the static one-shot flag so each test starts fresh.
    ;(WsConnection as unknown as { _workerConstructionFailureLogged: boolean })._workerConstructionFailureLogged = false
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('emits a console.warn when Worker constructor throws', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const FailingWorker = vi.fn(() => { throw new Error('CSP blocks workers') })
    vi.stubGlobal('Worker', FailingWorker)

    const conn = new WsConnection({
      onFrame: vi.fn(),
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
    })
    conn.connect()
    lastWsInstance.onopen?.()

    const wsCalls = warnSpy.mock.calls.filter(
      (args) => typeof args[0] === 'string' && (args[0] as string).includes('[ws] Web Worker construction failed')
    )
    expect(wsCalls.length).toBeGreaterThanOrEqual(1)

    conn.disconnect()
    warnSpy.mockRestore()
  })
})
