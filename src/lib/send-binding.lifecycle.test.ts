// #1428 step 2 RED. Spec BR-13, BR-13b, BR-18, BR-21. B-13, B-14, B-15, B-17.
import { describe, expect, it } from 'vitest'
import {
  apiMocks,
  assertNewChatKey,
  connection,
  deleteOpenChatThroughSidebar,
  framesOf,
  makeSession,
  mountComposer,
  resetBindingWorld,
  seedSavedChat,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'
import { act } from 'react'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { aliasNewChat, getDraft, openChatKey } from '@/store/chat/drafts'
import { useSessionStore } from '@/store/session'
import { enterNewChat } from '@/store/session/newChatKey'
import { useConnectionStore } from '@/store/connection'

async function confirmListDiscard(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByRole('button', { name: 'Discard' }))
  const dialog = await screen.findByRole('alertdialog')
  await user.click(within(dialog).getByRole('button', { name: 'Discard' }))
}

/** Spec BR-13b: offline first message for K, then saved chat B is open. */
async function queueOfflineFirst(text: string, fileName?: string): Promise<{ key: string }> {
  resetBindingWorld()
  mountComposer({ sidebar: true })
  const saved = makeSession({ id: 'saved-discard', title: 'Saved Discard', workspace_id: 'w1', agent_id: 'mia' })
  seedSavedChat(saved, undefined, undefined)
  apiMocks.fetchSessions.mockResolvedValue([saved])
  apiMocks.fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core' } as never])
  await deleteOpenChatThroughSidebar('Saved Discard')
  const key = assertNewChatKey(useSessionStore.getState().newChatKey)
  connection().fail = true
  if (fileName) dropFile(fileName)
  const user = userEvent.setup()
  await typeIntoComposer(text)
  await user.click(screen.getByRole('button', { name: 'Send message' }))
  act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
  expect(screen.getByText('New chat with Mia, not saved yet'), 'BR-13b: B lists the unsaved chat').toBeTruthy()
  expect(framesOf('message', sent()), 'BR-13b: nothing is sent while B is open').toEqual([])
  return { key }
}

function dropFile(name: string): void {
  const zone = screen.getByTestId('composer-card').parentElement
  if (!zone) throw new Error('drop zone missing')
  fireEvent.drop(zone, { dataTransfer: { files: [new File(['x'], name, { type: 'text/plain' })] } })
}

describe('queue, offline first send, resend and sign-out (spec 4.11 / 4.4.3)', () => {
  registerAliasedQueueCase()

  it('BR-13 a queued send waits until its own chat is open, and Retry keeps the id', async () => {
    resetBindingWorld()
    act(() => { useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting' }) })
    mountComposer()
    const user = userEvent.setup()
    await typeIntoComposer('queued in A')
    await user.click(screen.getByRole('button', { name: 'Queue message (will send on reconnect)' }))
    const queued = framesOf('message', sent())
    expect(queued, 'BR-13: offline send does not hit the socket').toEqual([])
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    act(() => { useConnectionStore.setState({ isConnected: true, reconnectPhase: null }) })
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(framesOf('message', sent()), 'BR-13: nothing is sent while B is open').toEqual([])
    act(() => { useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core') })
    act(() => { useChatStore.getState().drainOutboundQueue() })
    const delivered = framesOf('message', sent())
    expect(delivered, 'BR-13: opening A sends origin A').toEqual([
      expect.objectContaining({
        content: 'queued in A',
        session_id: 'chat-a',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
    const id = (delivered[0] as { client_message_id?: string }).client_message_id
    act(() => { useChatStore.getState().resendMessage(id ?? 'missing') })
    const again = framesOf('message', sent())
    expect(again, 'BR-13: Retry uses the same id and origin').toHaveLength(2)
    expect(again[1]).toEqual(expect.objectContaining({
      client_message_id: id,
      origin: { kind: 'chat', session_id: 'chat-a' },
    }))
  })

  it('BR-13 a legacy string queue entry becomes an Unsent item and is not sent', () => {
    resetBindingWorld()
    mountComposer()
    act(() => {
      useChatStore.setState((s) => ({
        ...s,
        outboundQueue: ['legacy string' as never],
      }))
    })
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(framesOf('message', sent()), 'BR-13: a legacy string is not sent').toEqual([])
    expect(screen.getByText('1 not sent'), 'BR-13: it becomes an Unsent item').toBeTruthy()
  })

  it('BR-13b an offline first message for K sends nothing until Open and send', async () => {
    resetBindingWorld()
    mountComposer({ sidebar: true })
    const saved = makeSession({ id: 'saved-k', title: 'Saved K', workspace_id: 'w1', agent_id: 'mia' })
    seedSavedChat(saved, undefined, undefined)
    apiMocks.fetchSessions.mockResolvedValue([saved])
    apiMocks.fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core' } as never])
    await deleteOpenChatThroughSidebar('Saved K')
    // Spec 4.11 / BR-13b: the key is the one minted when K was opened, not a
    // key read back off the frame Open and send happens to emit.
    const originalK = assertNewChatKey(useSessionStore.getState().newChatKey)
    connection().fail = true
    const user = userEvent.setup()
    await typeIntoComposer('first for K')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const queued = useChatStore.getState().outboundQueue.find((item) =>
      typeof item !== 'string' && item.content === 'first for K')
    const originalId = useChatStore.getState().pendingFirstSend?.clientMessageId
      ?? (queued && typeof queued !== 'string' ? queued.id : undefined)
    expect(originalId, 'BR-13b: the offline Send captured the original client_message_id').toEqual(
      expect.stringMatching(/^[A-Za-z0-9_-]{1,128}$/),
    )
    expect(openChatKey(), 'BR-13b: the offline Send does not mint a second key').toBe(originalK)
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    connection().fail = false
    expect(apiMocks.createSession, 'BR-13b: no create while B is open').not.toHaveBeenCalled()
    expect(framesOf('message', sent()), 'BR-13b: no first message while B is open').toEqual([])
    expect(screen.getByText('New chat with Mia, not saved yet'), 'BR-13b: the list names the unsaved chat').toBeTruthy()
    expect(screen.getByRole('button', { name: 'Open and send' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Copy' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Discard' })).toBeTruthy()
    await user.click(screen.getByRole('button', { name: 'Copy' }))
    expect(await navigator.clipboard.readText(), 'BR-13b: Copy is the exact text').toBe('first for K')
    await user.click(screen.getByRole('button', { name: 'Open and send' }))
    expect(openChatKey(), 'BR-13b: Open and send re-enters the same K').toBe(originalK)
    const message = framesOf('message', sent())[0] as {
      origin?: { kind?: string; new_chat_key?: string }
      client_message_id?: string
      agent_id?: string
      session_id?: string
      metadata?: { workspace_id?: string }
    }
    expect(message?.session_id, 'BR-13b: the first message names no saved session').toBeUndefined()
    expect(message, 'BR-13b: Open and send keeps the original K, id and agent').toEqual(expect.objectContaining({
      content: 'first for K',
      agent_id: 'mia',
      client_message_id: originalId,
      origin: { kind: 'new_chat', new_chat_key: originalK },
    }))
    expect(message?.metadata?.workspace_id, 'BR-13b: the first message stays in W1').toBe('w1')
    expect(framesOf('message', sent()), 'BR-13b: delivered once').toHaveLength(1)
  })

  it('BR-13b Discard drops the offline first message, and re-entering K sends nothing', async () => {
    // Spec BR-13b (c): Discard confirmed removes the Unsent item and the pending
    // send. Leaving B and re-entering the same K still produces zero frames.
    const { key } = await queueOfflineFirst('discard me')
    const user = userEvent.setup()
    await confirmListDiscard(user)
    expect(screen.queryByText('New chat with Mia, not saved yet'), 'BR-13b: Discard removes the Unsent item').toBeNull()
    expect(useChatStore.getState().pendingFirstSend, 'BR-13b: Discard removes the pending send').toBeNull()
    expect(framesOf('message', sent()), 'BR-13b: Discard sends nothing').toEqual([])
    act(() => { enterNewChat('w1', 'mia', 'core', key) })
    connection().fail = false
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(framesOf('message', sent()), 'BR-13b: re-entering K still sends nothing').toEqual([])
    expect(getDraft(key).text, 'BR-13b: the discarded text is not put back').toBe('')
  })

  it('BR-21 a restored upload still sends its original ref after 60 other uploads', async () => {
    // Spec BR-21 / 4.6.1 "Restore, then Send": the restored draft keeps the
    // uploaded ref. Sixty later uploads go through the composer, not the mock.
    // A second upload of orig.txt returns a different ref, so a re-upload cannot
    // impersonate the stored one.
    resetBindingWorld()
    mountComposer()
    const original = 'media://workspace/w1/original'
    const reupload = 'media://workspace/w1/orig-again'
    let origUploads = 0
    apiMocks.uploadFiles.mockImplementation(async (_sessionId: unknown, files: unknown) => {
      const list = Array.isArray(files) ? files as { name?: string }[] : []
      const name = list[0]?.name ?? 'file'
      const ref = name === 'orig.txt'
        ? (origUploads += 1) === 1 ? original : reupload
        : `media://workspace/w1/${name.replace(/\.txt$/, '')}`
      return { files: [{ ref, path: name, name, content_type: 'text/plain' }] }
    })
    dropFile('orig.txt')
    const user = userEvent.setup()
    await typeIntoComposer('resend me')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => {
      expect(framesOf('message', sent()).some((frame) => (frame as { content?: string }).content === 'resend me')).toBe(true)
    })
    const firstId = (framesOf('message', sent())[0] as { client_message_id?: string }).client_message_id
    const { deliverFrame } = await import('@/test/sendBinding1428')
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'message',
      session_id: 'chat-b',
      origin: { kind: 'chat', session_id: 'chat-a' },
      client_message_id: firstId,
    })
    expect(screen.getByTestId('chat-input'), 'BR-21: the refusal restores the text').toHaveValue('resend me')
    expect(screen.getByText('orig.txt'), 'BR-21: the uploaded file is restored with the draft').toBeTruthy()
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    for (let i = 0; i < 60; i++) {
      dropFile(`other-${i}.txt`)
      const box = screen.getByTestId('chat-input')
      fireEvent.change(box, { target: { value: `other ${i}` } })
      fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
      await waitFor(() => {
        expect(
          framesOf('message', sent()).some((frame) => (frame as { content?: string }).content === `other ${i}`),
          `BR-21: other ${i} was sent through the composer`,
        ).toBe(true)
      })
      deliverFrame({ type: 'done', session_id: 'chat-b' } as never)
    }
    const later = framesOf('message', sent()).filter((frame) => (frame as { content?: string }).content === 'other 59')
    expect(JSON.stringify(later), 'BR-21: a later upload really carries other-59').toContain('other-59')
    act(() => { useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core') })
    expect(screen.getByTestId('chat-input'), 'BR-21: A still holds the restored text').toHaveValue('resend me')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const restored = framesOf('message', sent()).filter((frame) => (frame as { content?: string }).content === 'resend me')
    expect(restored.at(-1), 'BR-21: the restored send carries the original ref').toEqual(
      expect.objectContaining({
        content: 'resend me',
        media: [original],
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    )
    expect(JSON.stringify(restored.at(-1)), 'BR-21: a later upload ref is not on the restored frame').not.toContain('other-59')
    expect(JSON.stringify(restored.at(-1)), 'BR-21: a re-upload is not the stored ref').not.toContain(reupload)
  })

  it('BR-18 sign-out drops an in-flight upload, and completing it changes nothing', async () => {
    resetBindingWorld()
    const first = mountComposer({ sidebar: true })
    let release: (value: unknown) => void = () => {}
    apiMocks.uploadFiles.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    const user = userEvent.setup()
    dropFile('late.txt')
    await typeIntoComposer('in flight')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await user.click(screen.getByRole('button', { name: 'Open user menu' }))
    await user.click(screen.getByRole('menuitem', { name: 'Sign out' }))
    await act(async () => {
      release({ files: [{ ref: 'media://workspace/w1/late' }] })
      await Promise.resolve()
    })
    expect(framesOf('message', sent()), 'BR-18: the late completion sends nothing').toEqual([])
    first.unmount()
    mountComposer()
    expect(screen.getByTestId('chat-input'), 'BR-18: nothing reappears').toHaveValue('')
    expect(screen.queryByText('1 not sent'), 'BR-18: the Unsent list is gone').toBeNull()
  })
})

function registerAliasedQueueCase(): void {
  it('MU-7 a queued first message keeps {new_chat, K} after K is aliased to S', async () => {
    // Spec 4.6.1 queue drain: the stored origin is not rewritten to whatever
    // chat is open when the queue drains. 4.6.2: nothing goes out while B is open.
    resetBindingWorld()
    mountComposer({ sidebar: true })
    const saved = makeSession({ id: 'saved-q', title: 'Saved Q', workspace_id: 'w1', agent_id: 'mia' })
    seedSavedChat(saved, undefined, undefined)
    apiMocks.fetchSessions.mockResolvedValue([saved])
    apiMocks.fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core' } as never])
    await deleteOpenChatThroughSidebar('Saved Q')
    const key = assertNewChatKey(useSessionStore.getState().newChatKey)
    act(() => { useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting' }) })
    const user = userEvent.setup()
    await typeIntoComposer('queued for K')
    await user.click(screen.getByRole('button', { name: 'Queue message (will send on reconnect)' }))
    expect(framesOf('message', sent()), 'MU-7: offline enqueue sends nothing').toEqual([])
    const queued = useChatStore.getState().outboundQueue.find((item) =>
      typeof item !== 'string' && item.content === 'queued for K')
    const originalId = queued && typeof queued !== 'string' ? queued.submission?.clientMessageId ?? queued.id : undefined
    expect(originalId, 'MU-7: the queued submission keeps its id').toEqual(expect.stringMatching(/^[A-Za-z0-9_-]{1,128}$/))
    expect(
      queued && typeof queued !== 'string' ? queued.submission?.origin : undefined,
      'MU-7: enqueue stores {new_chat, K}',
    ).toEqual({ kind: 'new_chat', new_chat_key: key })
    aliasNewChat(key, 'chat-a')
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    act(() => { useConnectionStore.setState({ isConnected: true, reconnectPhase: null }) })
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(framesOf('message', sent()), 'MU-7: nothing is sent while B is open').toEqual([])
    act(() => { useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core') })
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(framesOf('message', sent()), 'MU-7: opening S sends the original origin and id').toEqual([
      expect.objectContaining({
        content: 'queued for K',
        client_message_id: originalId,
        origin: { kind: 'new_chat', new_chat_key: key },
      }),
    ])
  })
}
