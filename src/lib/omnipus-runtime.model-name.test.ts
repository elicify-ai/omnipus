/**
 * omnipus-runtime.model-name.test.ts
 *
 * W4-21 — FR-010, moved onto the r9 send (spec 4.5 rule 5).
 * A runtime append calls submitText. The model is read at capture
 * (modelChoiceNow) and leaves on the bound message frame as
 * metadata.model_name. The same slug, the same absence, and the same
 * sticky second send as the old sendMessage opts.
 *
 * Spec ref: docs/internal/specs/phase-1-chat-model-and-errors.md
 *   - §13 FR-010 (nextMessage picker → metadata.model_name on send)
 *   - §18 Q3 (picker is forward-looking, not persisted; absent means no key)
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { WsConnection } from '@/lib/ws'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'
import { useOmnipusRuntime } from './omnipus-runtime'

vi.mock('@/lib/api', () => ({
  uploadFiles: vi.fn(),
  createSession: vi.fn(),
}))

vi.mock('@/components/chat/AttachmentCard', () => ({
  isImageAttachment: vi.fn(),
}))

const sent: unknown[] = []

function messageFrames(): { content?: string; metadata?: { model_name?: string } }[] {
  return sent.filter((frame) => (frame as { type?: string }).type === 'message') as { content?: string; metadata?: { model_name?: string } }[]
}

describe('useOmnipusRuntime — onNew → model_name on the bound frame (FR-010 / spec 4.5 rule 5)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    sent.length = 0
    useSessionStore.setState({
      ...useSessionStore.getInitialState(),
      activeSessionId: 'sess_mn',
      activeAgentId: 'jim',
      activeAgentType: null,
    })
    useChatStore.setState({ ...useChatStore.getInitialState(), nextModel: null })
    useConnectionStore.setState({
      ...useConnectionStore.getInitialState(),
      connection: { send: (frame: unknown) => { sent.push(frame); return true } } as unknown as WsConnection,
      isConnected: true,
    })
    useUiStore.setState(useUiStore.getInitialState(), true)
  })

  it('puts the picked model on the message frame as metadata.model_name', async () => {
    // FR-010: the user picked "z-ai/glm-5.2". Spec 4.5 rule 5 reads it at
    // capture, so the frame that leaves carries that slug.
    useChatStore.getState().setNextModel('z-ai/glm-5.2')

    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer

    await act(async () => {
      composer.setText('hello')
      await composer.send()
    })

    expect(messageFrames(), 'spec 4.5 rule 5: one runtime append leaves one message').toHaveLength(1)
    expect(messageFrames()[0]?.content).toBe('hello')
    expect(messageFrames()[0]?.metadata?.model_name).toBe('z-ai/glm-5.2')
  })

  it('omits metadata.model_name when nextModel is null (user never picked)', async () => {
    // Spec §18 Q3: no pick means the key is absent, not model_name: undefined.
    // The server then uses the agent's model config.
    useChatStore.getState().setNextModel(null)

    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer

    await act(async () => {
      composer.setText('hello')
      await composer.send()
    })

    expect(messageFrames()).toHaveLength(1)
    expect(messageFrames()[0]?.content).toBe('hello')
    expect(Object.prototype.hasOwnProperty.call(messageFrames()[0]?.metadata ?? {}, 'model_name')).toBe(false)
  })

  it('keeps the picked model on the next message frame (sticky — nextModel is not cleared)', async () => {
    // A pick persists across sends, so a second append without a re-pick
    // still carries the same model_name.
    useChatStore.getState().setNextModel('z-ai/glm-5.2')

    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer

    await act(async () => {
      composer.setText('first message')
      await composer.send()
    })
    expect(messageFrames()).toHaveLength(1)
    expect(messageFrames()[0]?.metadata?.model_name).toBe('z-ai/glm-5.2')

    await act(async () => {
      composer.setText('second message')
      await composer.send()
    })
    expect(messageFrames()).toHaveLength(2)
    expect(messageFrames()[1]?.content).toBe('second message')
    expect(messageFrames()[1]?.metadata?.model_name).toBe('z-ai/glm-5.2')
  })
})
