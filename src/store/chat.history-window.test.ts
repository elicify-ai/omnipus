// T09 (B12/B34), D9/D-M1/D-M2. Real session leave/store/query cleanup.
// This file does not pretend an ordinary setMessages call is a paging action.
// Whole-edge trim/forward reload use the actual UI in the companion UI pack.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { queryClient } from '@/lib/queryClient'
import { resetRealStores } from '../../tests/l1-room-ui/harness'
import { SID, STAMP, entry, page } from '../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../tests/chat-history-paging/transport'

let socket: ReturnType<typeof historyTransport> | undefined
beforeEach(() => { resetRealStores(); useSessionStore.getState().setActiveSession(SID, 'mia') })
afterEach(() => {
  socket?.connection.disconnect(); socket = undefined
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.unstubAllGlobals()
})

describe('T09 inactive reader payload release — B12', () => {
  it.each([499, 500])('leaving a %i-group reader releases old rows, results, aliases and query payload', (count) => {
    const messages = Array.from({ length: count }, (_, i) => ({
      id: `held-${i}`, role: 'assistant' as const, status: 'done' as const, content: `Held ${i}`, timestamp: STAMP,
      tool_calls: [{ id: `call-${i}`, tool: 'read_file', params: { path: 'held.txt' }, status: 'success' as const, result: `PRIVATE-CACHED-RESULT-${i}` }],
      mergedReplayIds: [`alias-${i}`],
    }))
    useChatStore.getState().setMessages(messages)
    const cachedPage = page([entry(1), entry(2)])
    queryClient.setQueryData(['messages', SID], cachedPage)
    expect(useChatStore.getState().messages.map((message) => message.id)).toStrictEqual(messages.map((message) => message.id))
    expect(queryClient.getQueryData(['messages', SID]), 'positive control: old payload is actually cached').toStrictEqual(cachedPage)
    socket = historyTransport()
    expect(useSessionStore.getState().attachToSession('new-history-session', 'chat', 'Other chat', 'mia')).toBe(true)
    const old = useChatStore.getState().sessionsById[SID]
    expect(old?.messageOrder ?? [], 'D9 inactive history may not retain 500 rows for every visited chat').toStrictEqual([])
    expect(old?.messagesById ?? {}, 'D9 remove old bodies, result/media ownership and display aliases together').toStrictEqual({})
    expect(old?.toolCalls ?? {}).toStrictEqual({})
    expect(old?.textAtToolCallStart ?? {}).toStrictEqual({})
    expect(old?.toolCallOwnerMessageId ?? {}).toStrictEqual({})
    expect(old?.mergedReplayMessageIds ?? {}).toStrictEqual({})
    expect(queryClient.getQueryData(['messages', SID]), 'D9 query cache cannot keep what store released').toBeUndefined()
    expect(useSessionStore.getState().activeSessionId).toBe('new-history-session')
    expect(socket.socket.sent.filter((frame) => frame.type === 'cancel'), 'leaving the reader never stops execution').toStrictEqual([])
  })

  it('visiting 50 sessions does not keep a reader payload per inactive bucket', () => {
    socket = historyTransport()
    const visited = Array.from({ length: 50 }, (_, i) => `history-visit-${i}`)
    for (const sid of visited) {
      useSessionStore.getState().setActiveSession(sid, 'mia')
      useChatStore.getState().setMessages([{ id: `row-${sid}`, role: 'user', status: 'done', content: `Cached ${sid}`, timestamp: STAMP }])
    }
    useSessionStore.getState().setActiveSession('last-destination', 'mia')
    expect(visited.map((sid) => ({ sid, rows: useChatStore.getState().sessionsById[sid]?.messageOrder ?? [] })), 'D-M1 no retained feature lifetime per visited session')
      .toStrictEqual(visited.map((sid) => ({ sid, rows: [] })))
    expect(socket.socket.sent.filter((frame) => frame.type === 'message' || frame.type === 'cancel')).toStrictEqual([])
  })
})
