// #1428 step 2: a draft is sent only to the chat it was written in. Anything else goes back into the box.
// Real: the assistant-ui external-store composer (setText, addAttachment, send), the real attachment
// adapter, useOmnipusRuntime, session store, library staging. Replaced edges: the chat store's sendMessage
// (a spy that records the chat attached at the moment of the send) and the upload / createSession calls.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

vi.mock('@/lib/api', () => ({ uploadFiles: vi.fn(), createSession: vi.fn() }))
vi.mock('@/components/chat/AttachmentCard', () => ({ isImageAttachment: vi.fn(() => false) }))

import * as api from '@/lib/api'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useUiStore } from '@/store/ui'
import { useOmnipusRuntime } from './omnipus-runtime'
import { resetDraftTarget, takeSessionCreatedForDraft } from './draft-target'
import { addLibraryAttachment, takeLibraryAttachments, libraryAttachmentsFor, refuseLibraryOnlySubmit } from './library-attachment'

type Composer = ReturnType<typeof useOmnipusRuntime>['thread']['composer']
type UploadResult = Awaited<ReturnType<typeof api.uploadFiles>>

const uploaded = (name: string, ref: string) => ({ files: [{ name, path: `uploads/${name}`, size: 1, content_type: 'text/plain', ref }] }) as unknown as UploadResult
const file = (name: string) => new File(['x'], name, { type: 'text/plain' })
const libFile = (id: string) => ({ mediaId: id, ref: `media://workspace/w/${id}`, filename: `${id}.png`, contentType: 'image/png' })


let chatAtSend: Array<[string, string | null, string[] | undefined]>
let addToast: ReturnType<typeof vi.fn>
const sent = () => chatAtSend
const switchTo = (id: string | null) => act(() => { useSessionStore.setState({ activeSessionId: id }) })
const toasts = () => addToast.mock.calls.map(([t]) => t.message as string)

beforeEach(() => {
  vi.clearAllMocks()
  resetDraftTarget()
  takeSessionCreatedForDraft()
  takeLibraryAttachments()
  chatAtSend = []
  addToast = vi.fn()
  useSessionStore.setState({ activeSessionId: 'chat-a', activeAgentId: 'mia', activeAgentType: null, sessionByWorkspace: {} })
  useChatStore.setState({
    messages: [], toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {}, isStreaming: false,
    sendMessage: ((text: string, opts?: { mediaRefs?: string[] }) => {
      chatAtSend.push([text, useSessionStore.getState().activeSessionId, opts?.mediaRefs])
    }) as never,
    cancelStream: vi.fn(),
  })
  useUiStore.setState({ addToast: addToast as never })
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


describe('a draft is sent only to the chat it was written in: the chat attached when you send', () => {
  it('a draft sent in the chat it was typed in is sent', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('typed in A') })
    await submit(composer)
    expect(sent()).toStrictEqual([['typed in A', 'chat-a', undefined]])
  })

  it('a draft typed in chat A and sent while chat B is attached is not sent; it goes back into the box and the notice names B', async () => {
    useSessionStore.setState({ sessionByWorkspace: { w: { id: 'chat-b', type: 'chat', title: 'Budget helper', agentId: 'mia' } as never } })
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('typed in A') })
    switchTo('chat-b')
    await submit(composer)
    expect(sent()).toStrictEqual([])
    expect(composer.getState().text).toBe('typed in A')
    expect(toasts()).toStrictEqual(['Not sent. Your text is back. Sending now goes to "Budget helper".'])
  })

  it('a second Send after the restore goes to the chat the notice named', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('typed in A') })
    switchTo('chat-b')
    await submit(composer)
    await submit(composer)
    expect(sent()).toStrictEqual([['typed in A', 'chat-b', undefined]])
    expect(toasts()).toHaveLength(1)
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
})

describe('a draft is sent only to the chat it was written in: a new chat', () => {
  // CHECK r3b F3 (my regression): new-chat sends must keep working.
  it('the first message of a new chat (no chat yet) is sent', async () => {
    useSessionStore.setState({ activeSessionId: null })
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('first ever') })
    await submit(composer)
    expect(sent()).toStrictEqual([['first ever', null, undefined]])
    expect(toasts()).toStrictEqual([])
  })

  it('a first message with a file is sent: the upload makes its own chat and that chat is its owner', async () => {
    useSessionStore.setState({ activeSessionId: null })
    vi.mocked(api.createSession).mockResolvedValueOnce({ id: 'new-chat', agent_id: 'mia' } as never)
    vi.mocked(api.uploadFiles).mockResolvedValueOnce(uploaded('first.txt', 'media://first'))
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('first with a file') })
    await act(async () => { await composer.addAttachment(file('first.txt')) })
    await submit(composer)
    expect(sent()).toStrictEqual([['first with a file', 'new-chat', ['media://first']]])
    expect(toasts()).toStrictEqual([])
  })

  it('a follow-up typed while the first message is still pending is sent (the pending placeholder is not a chat)', async () => {
    useSessionStore.setState({ activeSessionId: '__pending' })
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('follow-up') })
    await submit(composer)
    expect(sent()).toStrictEqual([['follow-up', '__pending', undefined]])
    expect(toasts()).toStrictEqual([])
  })
})

describe('a draft is sent only to the chat it was written in: files and uploads', () => {
  // CHECK r1 F1: the owner is bound to the submission, not to its text.
  it('identical text in A (with a file still uploading) and in B: B sends, A goes back into the box with its file', async () => {
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
    expect(sent()).toStrictEqual([['same', 'chat-b', undefined]])
    expect(composer.getState().text).toBe('same')
    expect(composer.getState().attachments.map((a) => a.name)).toStrictEqual(['a.txt'])
  })

  // CHECK r1 F2: a file is a draft too. CHECK r3b F7: the uploaded file comes back with the text.
  it('a file added in A with no text, sent while B is attached, is not sent and the file comes back', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValueOnce(uploaded('a.txt', 'media://a'))
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    await act(async () => { await composer.addAttachment(file('a.txt')) })
    switchTo('chat-b')
    await submit(composer)
    expect(sent()).toStrictEqual([])
    expect(composer.getState().attachments.map((a) => a.name)).toStrictEqual(['a.txt'])
    expect(toasts()).toHaveLength(1)
  })

  it('a restored file goes out with the second Send, still as the already uploaded reference', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValueOnce(uploaded('a.txt', 'media://a'))
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('with file') })
    await act(async () => { await composer.addAttachment(file('a.txt')) })
    switchTo('chat-b')
    await submit(composer)
    await submit(composer)
    expect(sent()).toStrictEqual([['with file', 'chat-b', ['media://a']]])
    expect(api.uploadFiles).toHaveBeenCalledTimes(1)
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

  // CHECK r2 R2-F1: a slash command goes straight through composer.send().
  it('a slash command sent while B is attached over a draft written in A is not sent', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('half written in A') })
    switchTo('chat-b')
    act(() => { composer.setText('/clear') })
    await submit(composer)
    expect(sent()).toStrictEqual([])
    expect(composer.getState().text).toBe('/clear')
  })

  // CHECK r2 R2-F4: no table, nothing to evict: 60 later sends leave A's pending send alone.
  it('a submission still uploading while 60 other drafts are sent in B is not sent into B', async () => {
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
    expect(sent()).toHaveLength(60)
    expect(sent().every(([text, chat]) => chat === 'chat-b' && text.startsWith('b '))).toBe(true)
    expect(composer.getState().text).toBe('from A')
  })
})

describe('a draft is sent only to the chat it was written in: library files', () => {
  // Library files belong to the chat they were staged in (CHECK r3b F2, F5).
  it('a library file staged in A is not sent with a message written in B and stays staged for A', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { addLibraryAttachment(libFile('m1')) })
    switchTo('chat-b')
    act(() => { composer.setText('typed in B') })
    await submit(composer)
    expect(sent()).toStrictEqual([['typed in B', 'chat-b', undefined]])
    expect(libraryAttachmentsFor('chat-a').map((a) => a.mediaId)).toStrictEqual(['m1'])
    expect(libraryAttachmentsFor('chat-b')).toStrictEqual([])
  })

  it('a library file staged in B is not drained by an older submission from A that finishes uploading later', async () => {
    const release = deferredUpload()
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('A message') })
    await act(async () => { await composer.addAttachment(file('a.txt')) })
    const sendA = startSubmit(composer)
    switchTo('chat-b')
    act(() => { addLibraryAttachment(libFile('b-secret')) })
    await act(async () => { release(uploaded('a.txt', 'media://upload-a')); await sendA })
    expect(sent()).toStrictEqual([])
    expect(libraryAttachmentsFor('chat-b').map((a) => a.mediaId)).toStrictEqual(['b-secret'])
  })

  it('a library file staged in A survives a runtime remount and is sent from A', async () => {
    const first = renderHook(() => useOmnipusRuntime())
    act(() => { addLibraryAttachment(libFile('m1')) })
    first.unmount()
    const second = renderHook(() => useOmnipusRuntime())
    const composer = second.result.current.thread.composer
    act(() => { composer.setText('in A again') })
    await submit(composer)
    expect(sent()).toStrictEqual([['in A again', 'chat-a', ['media://workspace/w/m1']]])
  })

  it('a library file staged in A is not sent from B after a remount', async () => {
    const first = renderHook(() => useOmnipusRuntime())
    act(() => { addLibraryAttachment(libFile('m1')) })
    first.unmount()
    switchTo('chat-b')
    const second = renderHook(() => useOmnipusRuntime())
    const composer = second.result.current.thread.composer
    act(() => { composer.setText('in B') })
    await submit(composer)
    expect(sent()).toStrictEqual([['in B', 'chat-b', undefined]])
    expect(libraryAttachmentsFor('chat-a')).toHaveLength(1)
  })

  // CHECK r3b F4: library files alone cannot send; the user is told.
  it('Enter with only a library file staged shows a notice instead of doing nothing', () => {
    act(() => { addLibraryAttachment(libFile('m1')) })
    expect(refuseLibraryOnlySubmit('', 0)).toBe(true)
    expect(toasts()).toStrictEqual(['Add a message to send the library file.'])
  })

  it('Enter with text, or with an uploaded file, or with nothing staged for this chat is not refused here', () => {
    act(() => { addLibraryAttachment(libFile('m1')) })
    expect(refuseLibraryOnlySubmit('hello', 0)).toBe(false)
    expect(refuseLibraryOnlySubmit('', 1)).toBe(false)
    switchTo('chat-b')
    expect(refuseLibraryOnlySubmit('', 0)).toBe(false)
    expect(toasts()).toStrictEqual([])
  })
})

describe('a draft is sent only to the chat it was written in: a message with no owner', () => {
  // A message that never came from the composer carries no owner (thread.append).
  it('a message that carries no owner while a chat is attached is not sent and goes back into the box with a notice', async () => {
    const { result } = renderHook(() => useOmnipusRuntime())
    const thread = result.current.thread
    await act(async () => { thread.append({ role: 'user', content: [{ type: 'text', text: 'appended without an owner' }] }) })
    expect(sent()).toStrictEqual([])
    expect(thread.composer.getState().text).toBe('appended without an owner')
    expect(toasts()[0]).toContain('Not sent')
  })

  it('a draft written with no chat attached, sent after a chat was attached, is not sent and goes back into the box', async () => {
    useSessionStore.setState({ activeSessionId: null })
    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer
    act(() => { composer.setText('typed with no chat') })
    switchTo('chat-b')
    await submit(composer)
    expect(sent()).toStrictEqual([])
    expect(composer.getState().text).toBe('typed with no chat')
    expect(toasts()).toHaveLength(1)
  })
})
