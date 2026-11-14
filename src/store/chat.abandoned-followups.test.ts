// #1833 — a follow-up typed under a new chat's first message, when that chat is
// abandoned ("start a new chat" after the first message was not saved).
//
// Oracle: the issue's expected line and the chief's decision Q-b = A: the
// follow-up is neither sent into any other chat nor silently lost; it stays
// visible as held ("Not sent: its chat was not saved") until the user discards
// it. Real: chat, session and connection stores, sendMessage, the real
// new-chat action, first-send frames. Replaced edge: the socket sender only.
import { act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { runStartNewSession } from '@/store/session/newChatFlow'
import { useUiStore } from '@/store/ui'
import { selectQueuedRows } from '@/store/chat/queue-target'
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

function connect() {
  const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
  })
  return sender
}
type Sent = { type: string; content?: string; session_id?: string; client_message_id?: string }
const allFrames = (sender: { send: ReturnType<typeof vi.fn> }) =>
  sender.send.mock.calls.map(([frame]) => frame as Sent).filter((frame) => frame.type === 'message')
const held = () => [...useChatStore.getState().pendingDrainQueue, ...useChatStore.getState().outboundQueue] as QueuedOutboundMessage[]
const frame = (value: Record<string, unknown>) => act(() => { useChatStore.getState().handleFrame(value as never) })

/** First message of chat A is not saved, two follow-ups wait under it, the user starts a new chat B and B is confirmed. */
function abandonAWithFollowUps() {
  const sender = connect()
  act(() => { useChatStore.getState().sendMessage('A first') })
  const aId = allFrames(sender)[0].client_message_id as string
  act(() => {
    useChatStore.getState().sendMessage('A follow 1')
    useChatStore.getState().sendMessage('A follow 2')
  })
  frame({ type: 'error', message: 'not saved', client_message_id: aId, first_message_error: 'not_saved' })
  act(() => { runStartNewSession({ choice: 'confirm', clientMessageId: aId }) })
  return { sender, aId }
}

describe('#1833 follow-ups of an abandoned unconfirmed chat', () => {
  it('are held, visible in the next chat, never sent anywhere', async () => {
    const { sender } = abandonAWithFollowUps()
    act(() => { useChatStore.getState().sendMessage('B first') })
    const bId = allFrames(sender).find((entry) => entry.content === 'B first')?.client_message_id as string
    frame({ type: 'session_started', session_id: 'chat-b', agent_id: 'mia', client_message_id: bId })
    await flush()
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia') })
    await flush()

    expect(allFrames(sender).filter((entry) => entry.content?.startsWith('A follow')), 'sent nowhere').toStrictEqual([])
    const mine = held().filter((entry) => entry.content.startsWith('A follow'))
    expect(mine.map((entry) => entry.content).sort()).toStrictEqual(['A follow 1', 'A follow 2'])
    expect(mine.map((entry) => entry.abandoned), 'marked as held for an unsaved chat').toStrictEqual([true, true])
    expect(mine.map((entry) => entry.targetSessionId), 'bound to no chat').toStrictEqual([undefined, undefined])
    const rows = selectQueuedRows(useChatStore.getState().pendingDrainQueue, useChatStore.getState().outboundQueue, new Set(), 'chat-b')
    expect(rows.map((row) => row.content), 'still visible in an established chat, so it can be discarded').toStrictEqual(['A follow 1', 'A follow 2'])
  })

  it('Discard removes exactly that message and frees its place in the queue', async () => {
    abandonAWithFollowUps()
    const [first, second] = held().filter((entry) => entry.content.startsWith('A follow'))
    act(() => { useChatStore.getState().discardQueuedMessage(first.id) })
    expect(held().map((entry) => entry.id), 'only the discarded one is gone').toStrictEqual([second.id])
    act(() => { useChatStore.getState().discardQueuedMessage(second.id) })
    expect(held()).toStrictEqual([])
    act(() => { useChatStore.getState().discardQueuedMessage('no-such-id') })
    expect(held(), 'discarding an unknown id changes nothing').toStrictEqual([])
  })

  it('a follow-up typed in an ordinary chat is not marked held by an abandon elsewhere', async () => {
    const { sender } = abandonAWithFollowUps()
    act(() => { useChatStore.getState().sendMessage('B first') })
    const bId = allFrames(sender).find((entry) => entry.content === 'B first')?.client_message_id as string
    act(() => { useChatStore.getState().sendMessage('B follow') })
    expect(held().find((entry) => entry.content === 'B follow')?.abandoned, 'B follow-up is not held').toBeUndefined()
    frame({ type: 'session_started', session_id: 'chat-b', agent_id: 'mia', client_message_id: bId })
    await flush()
    expect(held().find((entry) => entry.content === 'B follow')?.targetSessionId, 'B follow-up bound to B').toBe('chat-b')
  })
})
