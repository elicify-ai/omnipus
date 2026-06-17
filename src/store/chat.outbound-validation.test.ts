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
      pendingApprovals: [],
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

  it('integration: sendMessage still sends when the frame would fail validation', () => {
    // The send must NOT be blocked by validation. We wire the connection
    // store with a stub connection.send and exercise sendMessage via the
    // composer path that builds a real MessageFrame.
    const sendSpy = vi.fn().mockReturnValue(true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: sendSpy } as never,
        isConnected: true,
      })
    })
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    act(() => {
      useChatStore.getState().sendMessage('hello world')
    })

    // sendMessage always sends a well-formed MessageFrame, so the spy
    // should be hit and no warning should fire.
    expect(sendSpy).toHaveBeenCalledTimes(1)
    expect(warnSpy).not.toHaveBeenCalled()
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
})