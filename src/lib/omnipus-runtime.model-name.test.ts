/**
 * omnipus-runtime.model-name.test.ts
 *
 * W4-21 — FR-010: useOmnipusRuntime.onNew → model_name plumbing.
 *
 * The AssistantUI external-store runtime adapter reads the chat store's
 * `nextModel` value at the moment the user sends a message. If the user
 * picked a model in the picker, `nextModel` is a string and onNew MUST
 * thread it into `sendMessage` as `opts.model_name`. If the user never
 * picked, `nextModel` is null and the opts arg MUST NOT include
 * `model_name` (server falls back to the agent's `model` config).
 *
 * Spec ref: docs/internal/specs/phase-1-chat-model-and-errors.md
 *   - §13 FR-010 (nextMessage picker → metadata.model_name on send)
 *   - §18 Q3 (picker is forward-looking, not persisted)
 *
 * Approach: drive the real useExternalStoreRuntime composer
 * (setText → send) with a mocked sendMessage transport, then assert the
 * captured opts. We do NOT mock the runtime hook itself — we exercise
 * the actual onNew closure.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

vi.mock('@/lib/api', () => ({
  uploadFiles: vi.fn(),
  createSession: vi.fn(),
}))

vi.mock('@/components/chat/AttachmentCard', () => ({
  isImageAttachment: vi.fn(),
}))

import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useUiStore } from '@/store/ui'
import { useOmnipusRuntime } from './omnipus-runtime'

describe('useOmnipusRuntime — onNew → model_name plumbing (FR-010 / W4-21)', () => {
  let sendSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    vi.clearAllMocks()
    useSessionStore.setState({
      activeSessionId: 'sess_mn',
      activeAgentId: 'jim',
      activeAgentType: null,
    })
    sendSpy = vi.fn()
    useChatStore.setState({
      messages: [],
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      isStreaming: false,
      // nextModel is the field under test — start null (no user pick).
      nextModel: null,
      sendMessage: sendSpy as never,
      cancelStream: vi.fn(),
    })
    useUiStore.setState({ addToast: vi.fn() })
  })

  it('threads nextModel into sendMessage as opts.model_name when the user picked a model', async () => {
    // FR-010: the user picked "z-ai/glm-5.2" in the composer picker.
    // The next message MUST be routed via that model — the runtime
    // adapter reads `nextModel` from the chat store and writes it to
    // `opts.model_name`.
    useChatStore.getState().setNextModel('z-ai/glm-5.2')

    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer

    await act(async () => {
      composer.setText('hello')
      await composer.send()
    })

    // sendMessage was called exactly once with the text + opts.
    expect(sendSpy).toHaveBeenCalledTimes(1)
    const [text, opts] = sendSpy.mock.calls[0]
    expect(text).toBe('hello')
    expect(opts).toBeDefined()
    // The picked model slug MUST be present in opts.model_name.
    expect(opts?.model_name).toBe('z-ai/glm-5.2')
  })

  it('omits opts.model_name when nextModel is null (user never picked)', async () => {
    // Spec §18 Q3: the picker is forward-looking. If the user never
    // touched it, the runtime MUST NOT pass model_name — the server
    // falls back to the agent's `model` config. A spurious
    // `model_name: undefined` would still appear on the wire as a key
    // in the WS metadata (object spread semantics), so we assert that
    // the key is absent, not merely falsy.
    useChatStore.getState().setNextModel(null)

    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer

    await act(async () => {
      composer.setText('hello')
      await composer.send()
    })

    expect(sendSpy).toHaveBeenCalledTimes(1)
    const [text, opts] = sendSpy.mock.calls[0]
    expect(text).toBe('hello')
    // The spec says: "If the user never picked it, model_name is
    // absent and the server uses the agent's `model` config." The
    // runtime passes `undefined` as opts in that case, which the
    // store-level sendMessage translates to "no metadata key on the
    // frame." The wire-format invariant is enforced by the
    // ChatScreen.test.tsx::sendMessage-forwards-nextModel test.
    if (opts !== undefined) {
      expect(Object.prototype.hasOwnProperty.call(opts, 'model_name')).toBe(false)
    }
  })

  it('reflects a fresh pick on each send (the store clears nextModel after the first send)', async () => {
    // After the first send, the chat store clears nextModel (it's a
    // "next message" indicator, not a persistent preference). A
    // second send without a re-pick MUST NOT carry the old model.
    useChatStore.getState().setNextModel('z-ai/glm-5.2')

    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer

    // First send — model is forwarded.
    await act(async () => {
      composer.setText('first message')
      await composer.send()
    })
    expect(sendSpy).toHaveBeenCalledTimes(1)
    const [, opts1] = sendSpy.mock.calls[0]
    expect(opts1?.model_name).toBe('z-ai/glm-5.2')

    // Simulate the store clearing nextModel (this happens inside
    // sendMessage after the wire dispatch).
    act(() => {
      useChatStore.setState({ nextModel: null })
    })

    // Second send without a re-pick — no model_name.
    await act(async () => {
      composer.setText('second message')
      await composer.send()
    })
    expect(sendSpy).toHaveBeenCalledTimes(2)
    const [, opts2] = sendSpy.mock.calls[1]
    if (opts2 !== undefined) {
      expect(Object.prototype.hasOwnProperty.call(opts2, 'model_name')).toBe(false)
    }
  })
})
