// #1834: a follow-up queued under a new chat's first message must be released when that
// first message ends in `answer_not_started` (the chat is saved, open, idle and connected,
// and no later terminal frame will arrive to release the queue).
// Real: store actions and frame reducers. Replaced edge: the socket sender.
import { act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { expectValidFrame } from '../../tests/chat-history-paging/fixtures'

function reset() {
  act(() => {
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useSessionStore.setState({ ...useSessionStore.getInitialState(), activeAgentId: 'mia', sessionByWorkspace: {} }, true)
    useChatStore.setState({ ...useChatStore.getInitialState(), sessionsById: {}, pendingFirstSend: null, abandonedFirstSendIds: [], outboundQueue: [], pendingDrainQueue: [] }, true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    queryClient.clear(); localStorage.clear(); sessionStorage.clear()
  })
}

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date('2026-11-14T10:00:00.000Z')); reset() })
afterEach(() => { reset(); vi.clearAllTimers(); vi.useRealTimers() })

function frame(f: ServerFrame) { expectValidFrame(f); act(() => useChatStore.getState().handleFrame(f)) }
function connect() {
  const send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  act(() => { useConnectionStore.getState().setConnection({ send } as unknown as WsConnection); useConnectionStore.getState().setConnected(true) })
  return send
}
function messages(sender: ReturnType<typeof vi.fn>) { return sender.mock.calls.map(([f]) => f).filter((f) => f.type === 'message') }
function queuedTexts() {
  const c = useChatStore.getState()
  return [...c.pendingDrainQueue, ...c.outboundQueue].map((i) => (typeof i === 'string' ? i : i.content))
}
function pendingFirstSend(followUps: string[] = ['A follow-up']) {
  const sender = connect()
  act(() => useChatStore.getState().sendMessage('A first'))
  const id = messages(sender)[0].client_message_id as string
  for (const text of followUps) act(() => useChatStore.getState().sendMessage(text))
  expect(queuedTexts()).toStrictEqual(followUps)
  return { sender, id }
}
const sentTexts = (sender: ReturnType<typeof vi.fn>) => messages(sender).map((f) => f.content)
const notStarted = (id: string) => ({
  type: 'error' as const, message: 'no answer', session_id: 'a', client_message_id: id, first_message_error: 'answer_not_started' as const,
})

describe('#1834 answer_not_started releases the queued follow-up', () => {
  it('foreground chat: the follow-up is sent to the saved chat, once', () => {
    const { sender, id } = pendingFirstSend()
    frame(notStarted(id))
    expect(useSessionStore.getState().activeSessionId).toBe('a')
    expect(messages(sender).filter((f) => f.content === 'A follow-up').map((f) => f.session_id)).toStrictEqual(['a'])
    expect(queuedTexts()).toStrictEqual([])
  })

  it('a repeated identical error frame does not send the follow-up twice', () => {
    const { sender, id } = pendingFirstSend()
    frame(notStarted(id))
    frame(notStarted(id))
    expect(messages(sender).filter((f) => f.content === 'A follow-up')).toHaveLength(1)
  })

  it('background chat: the user has opened another chat, so the follow-up is never sent there', () => {
    const { sender, id } = pendingFirstSend()
    act(() => useSessionStore.getState().setActiveSession('b', 'mia'))
    frame(notStarted(id))
    expect(messages(sender).filter((f) => f.content === 'A follow-up' && f.session_id === 'b')).toStrictEqual([])
  })

  it('CHECK r1 F1: a repeated error while follow-up 1 runs does not release follow-up 2', () => {
    const { sender, id } = pendingFirstSend(['A follow 1', 'A follow 2', 'A follow 3'])
    frame(notStarted(id))
    expect(sentTexts(sender)).toStrictEqual(['A first', 'A follow 1'])
    frame(notStarted(id))
    expect(sentTexts(sender)).toStrictEqual(['A first', 'A follow 1'])
    expect(queuedTexts()).toStrictEqual(['A follow 2', 'A follow 3'])
  })

  it('CHECK r1 F1: a repeated error does not clear the running follow-up turn', () => {
    const { id } = pendingFirstSend(['A follow 1', 'A follow 2'])
    frame(notStarted(id))
    expect(useChatStore.getState().isStreaming).toBe(true)
    frame(notStarted(id))
    expect(useChatStore.getState().isStreaming).toBe(true)
  })

  it('CHECK r1 F2: an error for a background chat does not release another chat\'s queued item', () => {
    const { sender, id } = pendingFirstSend()
    act(() => useSessionStore.getState().setActiveSession('b', 'mia'))
    act(() => useChatStore.setState({
      outboundQueue: [...useChatStore.getState().outboundQueue, { id: 'b-1', content: 'B queued', targetSessionId: 'b', timestamp: '2026-11-14T10:00:00.000Z' }],
    }))
    frame(notStarted(id))
    expect(sentTexts(sender)).toStrictEqual(['A first'])
    expect(queuedTexts().sort()).toStrictEqual(['A follow-up', 'B queued'])
  })

  it('control: the first message itself is not re-sent', () => {
    const { sender, id } = pendingFirstSend()
    frame(notStarted(id))
    expect(messages(sender).filter((f) => f.content === 'A first')).toHaveLength(1)
  })
})
