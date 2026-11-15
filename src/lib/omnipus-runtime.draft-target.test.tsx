// #1428 step 2: the chat a draft was written in travels with it from the composer to sendMessage.
// Real: the assistant-ui external-store composer (setText, addAttachment, send), the real attachment
// adapter, useOmnipusRuntime, session store. Replaced edges: the chat store's sendMessage (a spy) and the
// upload call (a controlled promise), as in omnipus-runtime.attachments.test.tsx.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

vi.mock('@/lib/api', () => ({ uploadFiles: vi.fn(), createSession: vi.fn() }))
vi.mock('@/components/chat/AttachmentCard', () => ({ isImageAttachment: vi.fn(() => false) }))

import * as api from '@/lib/api'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useUiStore } from '@/store/ui'
import { useOmnipusRuntime } from './omnipus-runtime'
import { resetDraftTarget } from './draft-target'
import { addLibraryAttachment, takeLibraryAttachments } from './library-attachment'

type Composer = ReturnType<typeof useOmnipusRuntime>['thread']['composer']
type UploadResult = Awaited<ReturnType<typeof api.uploadFiles>>

const uploaded = (name: string, ref: string) => ({ files: [{ name, path: `uploads/${name}`, size: 1, content_type: 'text/plain', ref }] }) as unknown as UploadResult
const file = (name: string) => new File(['x'], name, { type: 'text/plain' })

describe('useOmnipusRuntime: the chat a draft was written in', () => {
  let sendSpy: ReturnType<typeof vi.fn>
  const sent = () => sendSpy.mock.calls.map(([text, opts]) => [text, opts?.expectedSessionId, opts?.mediaRefs])
  const switchTo = (id: string) => act(() => { useSessionStore.setState({ activeSessionId: id }) })

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
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); takeLibraryAttachments() })

  // Every submit path ends in composer.send(). The send may still be uploading when this returns.
  function startSubmit(composer: Composer): Promise<void> {
    return Promise.resolve(composer.send())
  }
  async function submit(composer: Composer): Promise<void> {
    await act(async () => { await startSubmit(composer) })
  }
  const deferredUpload = () => {
    let release: (value: UploadResult) => void = () => {}
    vi.mocked(api.uploadFiles).mockImplementationOnce(() => new Promise<UploadResult>((resolve) => { release = resolve }))
    return (value: UploadResult) => release(value)
  }

  it('a draft typed in chat A and sent while chat B is attached carries chat A', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('typed in A') })
    switchTo('chat-b')
    await submit(composer)
    expect(sent()).toStrictEqual([['typed in A', 'chat-a', undefined]])
  })

  it('a draft sent in the chat it was typed in carries that chat', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('typed in A') })
    await submit(composer)
    expect(sent()).toStrictEqual([['typed in A', 'chat-a', undefined]])
  })

  it('a new draft typed after the first one was sent is owned by the chat that is attached then', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('first') })
    await submit(composer)
    switchTo('chat-b')
    act(() => { composer.setText('second') })
    await submit(composer)
    expect(sent()).toStrictEqual([['first', 'chat-a', undefined], ['second', 'chat-b', undefined]])
  })

  it('a draft cleared by hand and typed again in another chat belongs to that chat, not the old one', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('hello') })
    act(() => { composer.setText('') })
    switchTo('chat-b')
    act(() => { composer.setText('hello') })
    await submit(composer)
    expect(sent()).toStrictEqual([['hello', 'chat-b', undefined]])
  })

  it('a draft typed before any chat exists carries no chat (first message of a new chat is unchanged)', async () => {
    useSessionStore.setState({ activeSessionId: null })
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('first ever') })
    await submit(composer)
    expect(sent()).toStrictEqual([['first ever', undefined, undefined]])
  })

  // CHECK r1 F1: the owner is bound to the submission, not to its text.
  it('identical text in A (with a file still uploading) and in B: each send keeps its own chat and its own file', async () => {
    const release = deferredUpload()
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('same') })
    await act(async () => { await composer.addAttachment(file('a.txt')) })
    const sendA = startSubmit(composer) // the upload is still running
    switchTo('chat-b')
    act(() => { composer.setText('same') })
    await submit(composer) // B's plain send finishes first
    await act(async () => { release(uploaded('a.txt', 'media://a')); await sendA })
    expect(sent()).toStrictEqual([['same', 'chat-b', undefined], ['same', 'chat-a', ['media://a']]])
  })

  it('a submission still uploading six minutes later keeps its chat when another draft is sent meanwhile', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    const release = deferredUpload()
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('from A') })
    await act(async () => { await composer.addAttachment(file('a.txt')) })
    const sendA = startSubmit(composer)
    switchTo('chat-b')
    vi.setSystemTime(Date.now() + 6 * 60 * 1000)
    act(() => { composer.setText('from B') })
    await submit(composer)
    await act(async () => { release(uploaded('a.txt', 'media://a')); await sendA })
    expect(sent()).toStrictEqual([['from B', 'chat-b', undefined], ['from A', 'chat-a', ['media://a']]])
  })

  // CHECK r1 F2: a file is a draft too.
  it('a file added in A with no text, sent while B is attached, carries chat A', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValueOnce(uploaded('a.txt', 'media://a'))
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    await act(async () => { await composer.addAttachment(file('a.txt')) })
    switchTo('chat-b')
    await submit(composer)
    expect(sent()).toStrictEqual([['', 'chat-a', ['media://a']]])
  })

  // CHECK r1 F3: a submit that goes nowhere leaves nothing behind.
  it('an empty Enter in A (refused by the composer) does not claim the next file-only draft in B', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValueOnce(uploaded('b.txt', 'media://b'))
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    await submit(composer) // nothing to send
    switchTo('chat-b')
    await act(async () => { await composer.addAttachment(file('b.txt')) })
    await submit(composer)
    expect(sent()).toStrictEqual([['', 'chat-b', ['media://b']]])
  })

  it('a file-only send whose upload failed does not claim the next file-only draft in B', async () => {
    vi.mocked(api.uploadFiles).mockRejectedValueOnce(new Error('upload failed'))
    vi.mocked(api.uploadFiles).mockResolvedValueOnce(uploaded('b.txt', 'media://b'))
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    await act(async () => { await composer.addAttachment(file('a.txt')) })
    await submit(composer) // fails: nothing is sent
    expect(sent()).toStrictEqual([])
    switchTo('chat-b')
    await act(async () => { await composer.addAttachment(file('b.txt')) })
    await submit(composer)
    expect(sent()).toStrictEqual([['', 'chat-b', ['media://b']]])
  })

  it('an unsent draft left in A by a runtime that went away does not claim a new draft in B', async () => {
    const first = renderHook(() => useOmnipusRuntime())
    act(() => { first.result.current.thread.composer.setText('left in A') })
    first.unmount()
    switchTo('chat-b')
    const second = renderHook(() => useOmnipusRuntime())
    const composer = second.result.current.thread.composer
    act(() => { composer.setText('new in B') })
    await submit(composer)
    expect(sent()).toStrictEqual([['new in B', 'chat-b', undefined]])
  })

  // CHECK r2 R2-F1: the slash menu sends "/clear" (and a deferred submit sends its text) straight through
  // composer.send(), with no submit entry point of ours in between. The owner rides in the composer, so it still holds.
  it('a slash command sent straight through the composer while B is attached still carries the draft owner A', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('half written in A') })
    switchTo('chat-b')
    act(() => { composer.setText('/clear') })
    await submit(composer)
    expect(sent()).toStrictEqual([['/clear', 'chat-a', undefined]])
  })

  // CHECK r2 R2-F2: a file in A, empty text, sent from B while a turn is streaming in B.
  it('a file in A with empty text, sent from B while B is streaming, carries chat A', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValueOnce(uploaded('a.txt', 'media://a'))
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    await act(async () => { await composer.addAttachment(file('a.txt')) })
    switchTo('chat-b')
    act(() => { useChatStore.setState({ isStreaming: true }) })
    await submit(composer)
    expect(sent()).toStrictEqual([['', 'chat-a', ['media://a']]])
  })

  // CHECK r2 R2-F3: a library file is staged outside the composer; it is a draft too.
  it('a library file staged in A and sent with text typed in B carries chat A', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { addLibraryAttachment({ mediaId: 'm1', ref: 'media://workspace/w/m1', filename: 'lib.png', contentType: 'image/png' }) })
    switchTo('chat-b')
    act(() => { composer.setText('typed in B') })
    await submit(composer)
    expect(sent()).toStrictEqual([['typed in B', 'chat-a', ['media://workspace/w/m1']]])
  })

  // CHECK r2 R2-F4: nothing is evicted, because there is no table: 60 later sends leave A's pending send alone.
  it('a submission still uploading while 60 other drafts are sent keeps its chat', async () => {
    const release = deferredUpload()
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('from A') })
    await act(async () => { await composer.addAttachment(file('a.txt')) })
    const sendA = startSubmit(composer)
    switchTo('chat-b')
    for (let i = 0; i < 60; i++) {
      act(() => { composer.setText(`b ${i}`) })
      await submit(composer)
    }
    await act(async () => { release(uploaded('a.txt', 'media://a')); await sendA })
    const last = sent().at(-1)
    expect(last).toStrictEqual(['from A', 'chat-a', ['media://a']])
    expect(sent().slice(0, 60).every(([, chat]) => chat === 'chat-b')).toBe(true)
  })
})
