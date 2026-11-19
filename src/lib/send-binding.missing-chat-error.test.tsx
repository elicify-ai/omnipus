// Spec 4.8 / F-q: a generated ErrorFrame has no `code`. With session_id and
// client_message_id and no first_message_error, the unchanged reducer adds a
// message-scoped notice in that chat and touches nothing turn-shaped. It does
// not fail the user row and it is not "Deleted chat" (BR-12 parked, #1904).
// The notice text is the frame's own message: sanitizeLegacyErrorMessage
// leaves a plain sentence unchanged (not JSON, not a dotted prefix).
import { describe, expect, it } from 'vitest'
import { act, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import {
  deliverFrame,
  FakeSocket,
  flushInbound,
  framesOf,
  installFakeWebSocket,
  mountComposer,
  resetBindingWorld,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'

const CHAT = 'chat-a'
const NOTICE = 'session not found'

function chatMessages() {
  const bucket = useChatStore.getState().sessionsById[CHAT]
  if (!bucket) return []
  return bucket.messageOrder.map((id) => bucket.messagesById[id]).filter((message) => message != null)
}

describe('a missing chat reported by a generated error frame (spec 4.8 / F-q)', () => {
  it('adds a notice in that chat and does not mark the row failed or deleted', async () => {
    resetBindingWorld()
    mountComposer()
    await typeIntoComposer('where did the chat go')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const sentMessage = framesOf('message', sent())[0] as { client_message_id?: string }
    const id = sentMessage?.client_message_id
    expect(id, 'fixture: the message left with an id the error can name').toEqual(expect.any(String))

    const before = useChatStore.getState().sessionsById[CHAT]
    const userBefore = chatMessages().find((message) => message.role === 'user' && message.content === 'where did the chat go')
    expect(userBefore, 'fixture: the sent row is in chat-a before the error').toBeTruthy()
    const turnBefore = { streaming: before?.isStreaming ?? null, turn: before?.activeTurnId ?? null }

    // The generated ErrorFrame has no code. Do not add one. It is fed through
    // the real WsConnection parser (socket text -> batched onFrames), not handleFrame.
    installFakeWebSocket()
    const parsed: unknown[] = []
    const live = new WsConnection({
      onFrames: (frames) => {
        parsed.push(...frames)
        for (const frame of frames) deliverFrame(frame as never)
      },
      onConnected: () => {},
      onDisconnected: () => {},
      onError: () => {},
    })
    live.connect()
    const socket = FakeSocket.instances.at(-1)
    if (!socket) throw new Error('socket missing')
    act(() => { socket.open() })
    const wire = { type: 'error' as const, message: NOTICE, session_id: CHAT, client_message_id: id }
    expect(wire, 'spec 4.8: the generated error frame has no code field').not.toHaveProperty('code')
    act(() => { socket.receive(wire) })
    await flushInbound()
    expect(parsed, 'spec 4.8: the real parser delivered the generated error frame').toHaveLength(1)
    expect(parsed[0], 'spec 4.8: the parsed frame still has no code field').not.toHaveProperty('code')

    const notice = chatMessages().find((message) => message.refusalNotice === true)
    expect(notice, 'F-q: the code-less error is a notice bubble in chat-a').toMatchObject({
      role: 'assistant',
      content: NOTICE,
      status: 'error',
      refusalNotice: true,
      isStreaming: false,
    })
    const userAfter = chatMessages().find((message) => message.id === userBefore!.id)
    expect(userAfter, 'F-q: the user row is still that message').toMatchObject({
      role: 'user',
      content: 'where did the chat go',
      deliveryStatus: userBefore!.deliveryStatus,
      status: userBefore!.status,
    })
    expect(userAfter?.deliveryStatus, 'spec 4.8: this frame does not mark the row failed').not.toBe('failed')
    const after = useChatStore.getState().sessionsById[CHAT]
    expect(
      { streaming: after?.isStreaming ?? null, turn: after?.activeTurnId ?? null },
      'F-q: nothing turn-shaped is touched',
    ).toEqual(turnBefore)
    const unsent = (useChatStore.getState() as { draftState?: { unsent?: { kind: string }[] } }).draftState?.unsent ?? []
    expect(
      unsent.filter((item) => item.kind === 'deleted'),
      'spec 4.8: the chat is not marked deleted in the store',
    ).toEqual([])
    expect(useSessionStore.getState().activeSessionId, 'spec 4.8: chat-a stays the open chat').toBe(CHAT)
    expect(screen.queryByText('Deleted chat'), 'spec 4.8: a code-less error is not the parked deleted-chat signal').toBeNull()
    void live
  })
})
