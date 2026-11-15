// #1428 step 2: the chat a draft was written in travels with it from the composer to sendMessage.
// Real: the assistant-ui external-store composer (setText, send), useOmnipusRuntime, session store.
// Replaced edge: the chat store's sendMessage (a spy), as in omnipus-runtime.attachments.test.tsx.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

vi.mock('@/lib/api', () => ({ uploadFiles: vi.fn(), createSession: vi.fn() }))
vi.mock('@/components/chat/AttachmentCard', () => ({ isImageAttachment: vi.fn(() => false) }))

import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useUiStore } from '@/store/ui'
import { useOmnipusRuntime } from './omnipus-runtime'
import { markDraftSubmit, resetDraftTarget } from './draft-target'

describe('useOmnipusRuntime: the chat a draft was written in', () => {
  let sendSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    vi.clearAllMocks()
    resetDraftTarget()
    useSessionStore.setState({ activeSessionId: 'chat-a', activeAgentId: 'mia', activeAgentType: null })
    sendSpy = vi.fn()
    useChatStore.setState({
      messages: [], toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {}, isStreaming: false,
      sendMessage: sendSpy as never, cancelStream: vi.fn(),
    })
    useUiStore.setState({ addToast: vi.fn() })
  })

  async function submit(composer: { send: () => Promise<void> | void; getState: () => { text: string } }) {
    await act(async () => {
      markDraftSubmit(composer.getState().text)
      await composer.send()
    })
  }

  it('a draft typed in chat A and sent while chat B is attached carries chat A', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('typed in A') })
    act(() => { useSessionStore.setState({ activeSessionId: 'chat-b' }) })
    await submit(composer)
    expect(sendSpy).toHaveBeenCalledTimes(1)
    expect(sendSpy.mock.calls[0][0]).toBe('typed in A')
    expect(sendSpy.mock.calls[0][1]?.expectedSessionId).toBe('chat-a')
  })

  it('a draft sent in the chat it was typed in carries that chat', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('typed in A') })
    await submit(composer)
    expect(sendSpy.mock.calls[0][1]?.expectedSessionId).toBe('chat-a')
  })

  it('a new draft typed after the first one was sent is owned by the chat that is attached then', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('first') })
    await submit(composer)
    act(() => { useSessionStore.setState({ activeSessionId: 'chat-b' }) })
    act(() => { composer.setText('second') })
    await submit(composer)
    expect(sendSpy.mock.calls.map((call) => [call[0], call[1]?.expectedSessionId])).toStrictEqual([['first', 'chat-a'], ['second', 'chat-b']])
  })

  it('a draft cleared by hand and typed again in another chat belongs to that chat, not the old one', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('hello') })
    act(() => { composer.setText('') })
    act(() => { useSessionStore.setState({ activeSessionId: 'chat-b' }) })
    act(() => { composer.setText('hello') })
    await submit(composer)
    expect(sendSpy.mock.calls[0][1]?.expectedSessionId).toBe('chat-b')
  })

  it('a draft typed before any chat exists carries no chat (first message of a new chat is unchanged)', async () => {
    useSessionStore.setState({ activeSessionId: null })
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('first ever') })
    await submit(composer)
    expect(sendSpy.mock.calls[0][1]?.expectedSessionId).toBeUndefined()
  })
})
