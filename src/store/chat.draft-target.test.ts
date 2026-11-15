// #1428 step 2: a typed draft is only ever sent to the chat it was written in.
// Oracle: the issue's "Expected" line ("A typed or queued message is only ever sent to the
// conversation it was written in") and the chief's rule: on a mismatch the text is queued for
// its own chat (never sent into the chat that is attached now, never dropped).
// Real: chat, session and connection stores, sendMessage, drain. Replaced edge: the socket sender.
import { act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { QueuedOutboundMessage } from '@/store/chat'

function resetStores() {
  act(() => {
    useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: null, activeAgentId: 'mia', sessionByWorkspace: {} }, true)
    useChatStore.setState({ ...useChatStore.getInitialState(), sessionsById: {}, abandonedFirstSendIds: [], pendingDrainQueue: [], outboundQueue: [] }, true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
  })
}
beforeEach(resetStores)
afterEach(resetStores)

function openChat(id: string) {
  act(() => { useSessionStore.getState().setActiveSession(id, 'mia') })
}
function connect() {
  const send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  act(() => {
    useConnectionStore.getState().setConnection({ send } as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
  })
  return send
}
const messageFrames = (send: ReturnType<typeof vi.fn>) =>
  send.mock.calls.map(([frame]) => frame as { type: string; content?: string; session_id?: string; media?: string[] })
    .filter((frame) => frame.type === 'message')
const queued = () => [...useChatStore.getState().pendingDrainQueue, ...useChatStore.getState().outboundQueue] as QueuedOutboundMessage[]

describe('#1428 step 2: a draft goes to the chat it was written in', () => {
  it('draft written in chat A, chat B attached at send time: nothing is sent into B, the text waits for A', () => {
    const send = connect()
    openChat('chat-b')
    act(() => { useChatStore.getState().sendMessage('written in A', { expectedSessionId: 'chat-a' }) })
    expect(messageFrames(send), 'nothing is sent anywhere').toStrictEqual([])
    expect(queued().map((item) => [item.content, item.targetSessionId])).toStrictEqual([['written in A', 'chat-a']])
  })

  it('the held draft is sent to chat A, once, when A is attached again', () => {
    const send = connect()
    openChat('chat-b')
    act(() => { useChatStore.getState().sendMessage('written in A', { expectedSessionId: 'chat-a' }) })
    openChat('chat-a')
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(messageFrames(send).map((frame) => [frame.content, frame.session_id])).toStrictEqual([['written in A', 'chat-a']])
    expect(queued()).toStrictEqual([])
  })

  it('a draft with attachments keeps them while it waits and sends them to A', () => {
    const send = connect()
    openChat('chat-b')
    act(() => {
      useChatStore.getState().sendMessage('see file', {
        expectedSessionId: 'chat-a',
        mediaRefs: ['media://one'],
        attachments: [{ type: 'file', url: '/m/one', filename: 'one.txt', contentType: 'text/plain' }],
      })
    })
    expect(messageFrames(send)).toStrictEqual([])
    openChat('chat-a')
    act(() => { useChatStore.getState().drainOutboundQueue() })
    const frames = messageFrames(send)
    expect(frames).toHaveLength(1)
    expect(frames[0].session_id).toBe('chat-a')
    expect(frames[0].media).toStrictEqual(['media://one'])
  })

  it('a full queue does not drop the draft', () => {
    connect()
    openChat('chat-b')
    act(() => {
      useChatStore.setState({
        outboundQueue: [1, 2, 3, 4, 5].map((n): QueuedOutboundMessage => ({ id: `q${n}`, content: `q${n}`, timestamp: `2026-11-14T10:00:0${n}.000Z`, targetSessionId: 'chat-c' })),
      })
    })
    act(() => { useChatStore.getState().sendMessage('written in A', { expectedSessionId: 'chat-a' }) })
    expect(queued().map((item) => item.content)).toContain('written in A')
  })

  it('control: the draft goes straight to the attached chat when it is the chat it was written in', () => {
    const send = connect()
    openChat('chat-a')
    act(() => { useChatStore.getState().sendMessage('written in A', { expectedSessionId: 'chat-a' }) })
    expect(messageFrames(send).map((frame) => [frame.content, frame.session_id])).toStrictEqual([['written in A', 'chat-a']])
    expect(queued()).toStrictEqual([])
  })

  it('control: a send with no recorded chat behaves as before', () => {
    const send = connect()
    openChat('chat-b')
    act(() => { useChatStore.getState().sendMessage('plain') })
    expect(messageFrames(send).map((frame) => frame.session_id)).toStrictEqual(['chat-b'])
  })
})
