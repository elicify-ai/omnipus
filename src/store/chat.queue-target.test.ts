// #1428 step 1 — a queued message is only ever sent to the chat it was written in.
//
// Oracle: the issue's "Expected" line ("A typed or queued message is only ever
// sent to the conversation it was written in") and the chief's decision for
// Q-a: a queued message for a chat that is not open waits until that chat is
// opened. No expected value was read from the implementation. Real: chat,
// session and connection stores, sendMessage, drain. Replaced edge: the socket
// sender only. Boundary: the server-side effect is not observable here.
import { act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import type { QueuedOutboundMessage } from '@/store/chat'

function resetStores() {
  act(() => {
    useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: null, activeAgentId: 'mia', sessionByWorkspace: {} }, true)
    useChatStore.setState({ ...useChatStore.getInitialState(), sessionsById: {}, abandonedFirstSendIds: [], pendingDrainQueue: [], outboundQueue: [] }, true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
  })
}
beforeEach(resetStores)
afterEach(resetStores)

const flush = () => act(async () => { await Promise.resolve() })

function openChat(id: string) {
  act(() => { useSessionStore.getState().setActiveSession(id, 'mia') })
}
function connect() {
  const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
  })
  return sender
}
const sentFrames = (sender: { send: ReturnType<typeof vi.fn> }) =>
  sender.send.mock.calls.map(([frame]) => frame as { type: string; content?: string; session_id?: string })
    .filter((frame) => frame.type === 'message')
const waiting = () => [...useChatStore.getState().pendingDrainQueue, ...useChatStore.getState().outboundQueue]
  .map((item) => typeof item === 'string' ? item : item.content)

/** Typed in chat A while the connection is down, so it is queued. */
function queueInChatA() {
  openChat('chat-a')
  act(() => { useChatStore.getState().sendMessage('written in chat A') })
  expect(waiting(), 'fixture: the message is queued, not sent').toStrictEqual(['written in chat A'])
}

describe('#1428 step 1: queued messages keep their chat', () => {
  it('records the chat a message was typed in when it is queued', () => {
    queueInChatA()
    const item = useChatStore.getState().outboundQueue[0] as QueuedOutboundMessage
    expect(item.targetSessionId).toBe('chat-a')
  })

  it('does not drain a message into another chat after navigation, and keeps it queued', async () => {
    queueInChatA()
    openChat('chat-b')
    const sender = connect()
    act(() => { useChatStore.getState().drainOutboundQueue() })
    await flush()
    expect(sentFrames(sender), 'nothing written in A goes to B').toStrictEqual([])
    expect(waiting(), 'the message stays queued for A').toStrictEqual(['written in chat A'])
  })

  it('sends it to chat A once chat A is opened again', async () => {
    queueInChatA()
    openChat('chat-b')
    const sender = connect()
    act(() => { useChatStore.getState().drainOutboundQueue() })
    openChat('chat-a')
    await flush()
    const frames = sentFrames(sender)
    expect(frames.map((frame) => ({ content: frame.content, sid: frame.session_id })))
      .toStrictEqual([{ content: 'written in chat A', sid: 'chat-a' }])
    expect(waiting()).toStrictEqual([])
  })

  it('a message waiting for chat A does not block a message written in chat B', async () => {
    queueInChatA()
    openChat('chat-b')
    act(() => { useChatStore.getState().sendMessage('written in chat B') })
    const sender = connect()
    act(() => { useChatStore.getState().drainOutboundQueue() })
    await flush()
    expect(sentFrames(sender).map((frame) => ({ content: frame.content, sid: frame.session_id })))
      .toStrictEqual([{ content: 'written in chat B', sid: 'chat-b' }])
    expect(waiting(), 'A\'s message is still waiting').toStrictEqual(['written in chat A'])
  })

  it('a first message of a new chat (no chat id yet) is not held back', async () => {
    act(() => { useSessionStore.setState({ activeSessionId: null }) })
    act(() => { useChatStore.getState().sendMessage('very first message') })
    const item = useChatStore.getState().outboundQueue[0] as QueuedOutboundMessage
    expect(item.targetSessionId, 'no chat id exists yet, so none is recorded').toBeUndefined()
  })
})

describe('#1428 step 1: which queued items a chat shows and may send', () => {
  const item = (targetSessionId?: string): QueuedOutboundMessage => ({ id: 'q', content: 'c', timestamp: 't', ...(targetSessionId ? { targetSessionId } : {}) })
  it('shows and sends only items of the open chat, plus items with no chat yet and plain legacy strings', async () => {
    const { queuedItemBelongsHere, firstDrainableIndex } = await import('./chat/queue-target')
    expect(queuedItemBelongsHere(item('chat-a'), 'chat-a')).toBe(true)
    expect(queuedItemBelongsHere(item('chat-a'), 'chat-b')).toBe(false)
    expect(queuedItemBelongsHere(item('chat-a'), null)).toBe(false)
    expect(queuedItemBelongsHere(item(), 'chat-b')).toBe(true)
    expect(queuedItemBelongsHere('legacy text', 'chat-b')).toBe(true)
    expect(firstDrainableIndex([item('chat-a'), item('chat-b'), item('chat-b')], 'chat-b')).toBe(1)
    expect(firstDrainableIndex([item('chat-a')], 'chat-b')).toBe(-1)
  })
})
