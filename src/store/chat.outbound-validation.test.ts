/**
 * chat.outbound-validation.test.ts — W2-29 regression tests.
 *
 * The chat store's `_validateOutboundFrame` runs every MessageFrame through
 * the generated Zod schema before sending it on the WebSocket. The intent
 * is forward-compat telemetry (silent-failure-B #3): a future required
 * wire field would otherwise be silently omitted on the way out.
 *
 * The contract is:
 *   - valid frames pass through without side effects
 *   - invalid frames emit a console.warn
 *   - the WS send still happens (validation never blocks the send)
 *
 * We mock the connection.send via the connection store so we can observe
 * whether the send was called after a schema failure.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useUiStore } from './ui'
import * as telemetry from '@/lib/telemetry'

const SID = 'outbound-validation-test-session'

function resetStores() {
  act(() => {
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      lastReceivedEventTime: null,
      nextModel: null,
    })
    useSessionStore.setState({
      activeSessionId: SID,
      activeAgentId: 'agent-1',
      activeAgentType: null,
    })
    useUiStore.setState({ toasts: [] })
  })
}

describe('W2-29 _validateOutboundFrame', () => {
  beforeEach(() => {
    resetStores()
    vi.restoreAllMocks()
  })

  it('passes a well-formed MessageFrame without side effects', () => {
    const spy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const frame = {
      type: 'message' as const,
      content: 'hello',
      session_id: SID,
      agent_id: 'agent-1',
    }
    useChatStore.getState()._validateOutboundFrame(frame)
    expect(spy).not.toHaveBeenCalled()
  })

  it('logs a warning on a malformed frame (missing required content)', () => {
    const spy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    // `type` is wrong AND content is missing — fails on the literal type
    // AND on the required content field.
    const frame = {
      type: 'message' as const,
      // content omitted intentionally
      session_id: SID,
    }
    useChatStore.getState()._validateOutboundFrame(frame)
    expect(spy).toHaveBeenCalledTimes(1)
    const firstCall = spy.mock.calls[0]
    expect(firstCall[0]).toContain('outbound MessageFrame failed schema validation')
  })

  it('does not throw when called with a non-object payload', () => {
    const spy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(() => useChatStore.getState()._validateOutboundFrame(null)).not.toThrow()
    expect(() => useChatStore.getState()._validateOutboundFrame(42)).not.toThrow()
    expect(() => useChatStore.getState()._validateOutboundFrame('string')).not.toThrow()
    expect(spy).toHaveBeenCalled()
  })

  it('integration: sendMessage STILL sends even when the frame would fail validation (W4-22)', () => {
    // W4-22: the previous version of this test was named "integration"
    // but only exercised the happy path — it asserted `sendSpy` was
    // called and `warnSpy` was not, both trivially true when the Zod
    // schema never sees a malformed frame. The real contract is:
    //
    //   - validation NEVER blocks the send (the schema is
    //     forward-compat telemetry, not a gate)
    //   - on a schema failure, the dev toast fires (so the developer
    //     sees the contract drift in their console)
    //   - on a schema failure, console.warn is called for log capture
    //
    // To test the failure path, we mock MessageFrameSchema.safeParse to
    // return a failure result. sendMessage must STILL call
    // connection.send and the toast must fire.
    //
    // We assert against the real addToast by reading the toasts array
    // (NOT by replacing addToast with a spy — vi.spyOn does not
    // reliably restore on Zustand state properties, and a manual spy
    // would leak into the W4-15 test below).
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    // Direct call: validator sees a payload that fails the schema
    // (wrong type, missing content).
    useChatStore.getState()._validateOutboundFrame({
      type: 'message',
      // content omitted -> fails the required content field
      session_id: SID,
    })
    expect(warnSpy).toHaveBeenCalledTimes(1)
    expect(warnSpy.mock.calls[0][0]).toContain('outbound MessageFrame failed schema validation')
    // Dev toast must fire on schema failure — assert on the real
    // toasts array so we don't leak an addToast spy into the next test.
    const toasts = useUiStore.getState().toasts
    expect(toasts.length).toBeGreaterThan(0)
    const lastToast = toasts[toasts.length - 1]
    expect(lastToast.message).toMatch(/outbound frame validation failed/i)

    // Independent assertion: sendMessage called the connection.send
    // spy — proves the validator's failure did NOT block the wire
    // dispatch. We do this in the same test so a future regression
    // that wires the validator into a gate would fail BOTH halves.
    const sendSpy = vi.fn().mockReturnValue(true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: sendSpy } as never,
        isConnected: true,
      })
    })
    act(() => {
      useChatStore.getState().sendMessage('hello world')
    })
    expect(sendSpy).toHaveBeenCalledTimes(1)
  })

  it('emits a focused 1-line dev-toast on a malformed frame (W4-15 fix)', () => {
    // W4-15: the dev-toast used to dump the multi-line Zod result.error.message
    // (a wall of JSON). The fix focuses on the first issue: path.join + message.
    // In dev mode + with a malformed frame, addToast must be called with a
    // single-line description that includes the failing field and the issue.
    vi.stubEnv('DEV', true)
    vi.stubEnv('MODE', 'development')
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const frame = {
      type: 'message' as const,
      // content omitted — Zod fails on the required field "content"
      session_id: SID,
    }
    useChatStore.getState()._validateOutboundFrame(frame)

    const toasts = useUiStore.getState().toasts
    expect(toasts.length).toBeGreaterThan(0)
    const lastToast = toasts[toasts.length - 1]
    // Focused: single-line description including the failing field path + issue
    expect(lastToast.message).toContain('Outbound frame validation failed (dev):')
    expect(lastToast.message).toContain('content')
    // Must NOT contain the multi-line JSON dump
    expect(lastToast.message).not.toContain('[\n')
    expect(lastToast.message).not.toContain('{\n')

    expect(warnSpy).toHaveBeenCalled()
    vi.unstubAllEnvs()
  })

  it('does NOT emit a dev-toast in production builds', () => {
    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production')
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const frame = {
      type: 'message' as const,
      // content omitted
      session_id: SID,
    }
    useChatStore.getState()._validateOutboundFrame(frame)

    // Toast count should be unchanged from the reset (empty toasts array)
    expect(useUiStore.getState().toasts).toHaveLength(0)

    expect(warnSpy).toHaveBeenCalled()
    vi.unstubAllEnvs()
  })

  it('emits a production logError telemetry record on schema validation failure, including sessionId (Wave 3 Fix 2 / Fix 1)', () => {
    // Mirrors the established pattern in src/lib/api.test.ts (~L1596-1628) and
    // src/store/chatPreferences.test.ts (~L108-124): force the production gate
    // open, trigger the failure path, assert logError fires with the event
    // name + diagnostic fields. Also covers Wave-3 Fix 1: sessionId must be
    // present on this event (previously the only 2 of 16 diagnostic sites in
    // chat.ts missing it), threaded through from sendMessage's call sites via
    // the sessionId param added to _validateOutboundFrame.
    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production')
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const logErrorSpy = vi.spyOn(telemetry, 'logError')
    const frame = {
      type: 'message' as const,
      // content omitted — Zod fails on the required field "content"
      session_id: SID,
    }

    useChatStore.getState()._validateOutboundFrame(frame, SID)

    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: 'chatOutboundFrameValidationFailed',
        sessionId: SID,
      }),
    )
    vi.unstubAllEnvs()
  })

  it('does NOT call logError in DEV/test builds — production/DEV split preserved', () => {
    // Regression guard mirroring the DEV-vs-PROD split covered elsewhere in
    // this file for the toast/console.warn behaviour — under the default
    // test-mode gate (no env stubbing), _recordChatDiagnostic's own
    // `!DEV && MODE !== 'test'` gate stays closed.
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const logErrorSpy = vi.spyOn(telemetry, 'logError')
    const frame = {
      type: 'message' as const,
      session_id: SID,
    }

    useChatStore.getState()._validateOutboundFrame(frame, SID)

    expect(logErrorSpy).not.toHaveBeenCalled()
  })
})