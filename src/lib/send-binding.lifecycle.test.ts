// #1428 step 2 RED. Spec BR-13, BR-13b, BR-18, BR-21. B-13, B-14, B-15, B-17.
import { describe, expect, it } from 'vitest'
import { act } from 'react'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
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

describe('queue, offline first send, resend and sign-out (spec 4.11 / 4.4.3)', () => {
  it('BR-13 a queued send waits until its own chat is open, and Retry keeps the id', async () => {
    resetBindingWorld()
    connection().fail = true
    mountComposer()
    const user = userEvent.setup()
    await typeIntoComposer('queued in A')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const queued = framesOf('message', sent())
    expect(queued, 'BR-13: offline send does not hit the socket').toEqual([])
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    connection().fail = false
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
    const message = framesOf('message', sent())[0] as { origin?: { new_chat_key?: string }; client_message_id?: string }
    const key = assertNewChatKey(message?.origin?.new_chat_key)
    expect(message, 'BR-13b: one first message with that same id').toEqual(expect.objectContaining({
      content: 'first for K',
      agent_id: 'mia',
      client_message_id: originalId,
      origin: { kind: 'new_chat', new_chat_key: key },
    }))
    expect(framesOf('message', sent()), 'BR-13b: delivered once').toHaveLength(1)
  })

  it('BR-21 a restored upload still sends its original ref after 60 other uploads', async () => {
    resetBindingWorld()
    mountComposer()
    const original = 'media://workspace/w1/original'
    for (let i = 0; i < 60; i++) {
      apiMocks.uploadFiles.mockResolvedValueOnce({
        files: [{ ref: `media://workspace/w1/other-${i}`, name: `o${i}.txt`, content_type: 'text/plain' }],
      })
    }
    for (let i = 0; i < 60; i++) {
      await apiMocks.uploadFiles('chat-a', [new File(['o'], `o${i}.txt`, { type: 'text/plain' })], 'w1')
    }
    expect(apiMocks.uploadFiles.mock.calls, 'BR-21: the 60 other uploads ran').toHaveLength(60)
    expect(
      apiMocks.uploadFiles.mock.calls.some((call) => JSON.stringify(call).includes(original)),
      'BR-21: none of those uploads is the restored ref',
    ).toBe(false)
    const { requireFn } = await import('@/test/send-binding.module')
    // Path column of spec 4.6.1 ("Restore, then Send"), not an invented slug.
    const capture = await requireFn('../lib/bound-send', 'captureSubmission', 'spec 4.6.1 / BR-21')
    const frozen = capture('Restore, then Send', {
      text: 'resend me',
      files: [{ kind: 'uploaded', id: 'f1', ref: original, workspaceId: 'w1', name: 'orig.txt', mime: 'text/plain' }],
      libraryPicks: [],
      workspaceId: 'w1',
      agentId: 'mia',
      origin: { kind: 'chat', session_id: 'chat-a' },
      target: 'chat-a',
    })
    const sendBound = await requireFn('../lib/bound-send', 'sendBound', 'spec 4.6.1 / BR-21')
    sendBound(frozen)
    expect(framesOf('message', sent()), 'BR-21: the original ref is sent, not a later upload').toEqual([
      expect.objectContaining({
        content: 'resend me',
        media: [original],
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
    expect(
      JSON.stringify(framesOf('message', sent())),
      'BR-21: a later upload ref is not on the frame',
    ).not.toContain('other-59')
  })

  it('BR-18 sign-out drops an in-flight upload, and completing it changes nothing', async () => {
    resetBindingWorld()
    mountComposer({ sidebar: true })
    let release: (value: unknown) => void = () => {}
    apiMocks.uploadFiles.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    const user = userEvent.setup()
    await typeIntoComposer('in flight')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await user.click(screen.getByRole('button', { name: 'Open user menu' }))
    await user.click(screen.getByRole('menuitem', { name: 'Sign out' }))
    await act(async () => {
      release({ files: [{ ref: 'media://workspace/w1/late' }] })
      await Promise.resolve()
    })
    expect(framesOf('message', sent()), 'BR-18: the late completion sends nothing').toEqual([])
    mountComposer()
    expect(screen.getByTestId('chat-input'), 'BR-18: nothing reappears').toHaveValue('')
    expect(screen.queryByText('1 not sent'), 'BR-18: the Unsent list is gone').toBeNull()
  })
})
