// Spec 4.8: a real generated ErrorFrame has no `code` field. Fed through the
// WsConnection parser, a missing-chat error is a failed bubble plus Retry.
// It is not "Deleted chat". That list row is the parked BR-12 signal (#1904),
// which nothing can send until ErrorFrame grows a code.
import { createElement } from 'react'
import { describe, expect, it } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { VirtualUserMessageRow } from '@/components/chat/ChatScreen'
import { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
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

describe('a missing chat reported by a generated error frame (spec 4.8)', () => {
  it('shows a failed bubble plus Retry and never "Deleted chat"', async () => {
    resetBindingWorld()
    mountComposer()
    await typeIntoComposer('where did the chat go')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const sentMessage = framesOf('message', sent())[0] as { client_message_id?: string }
    const id = sentMessage?.client_message_id
    expect(id, 'fixture: the message left with an id the error can name').toEqual(expect.any(String))

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
    const wire = { type: 'error' as const, message: 'session not found', session_id: 'chat-a', client_message_id: id }
    expect(wire, 'spec 4.8: the generated error frame has no code field').not.toHaveProperty('code')
    act(() => { socket.receive(wire) })
    await flushInbound()
    expect(parsed, 'spec 4.8: the real parser delivered the generated error frame').toHaveLength(1)
    expect(parsed[0], 'spec 4.8: the parsed frame still has no code field').not.toHaveProperty('code')

    expect(screen.queryByText('Deleted chat'), 'spec 4.8: a code-less error is not the parked deleted-chat signal').toBeNull()
    const bubble = useChatStore.getState().messages.find((message) => message.role === 'user' && message.content === 'where did the chat go')
    expect(bubble, 'spec 4.8: the failed bubble is still the message that was sent').toBeTruthy()
    expect(bubble?.deliveryStatus, 'spec 4.8: an ordinary missing-chat error fails the existing bubble').toBe('failed')
    render(createElement(VirtualUserMessageRow, {
      message: bubble!, skills: [], commandLabels: [], agentName: 'Mia', latest: true,
    }))
    expect(
      screen.getByRole('button', { name: /Retry|Try again/ }),
      'spec 4.8: the failed bubble offers Retry',
    ).toBeTruthy()
  })
})
