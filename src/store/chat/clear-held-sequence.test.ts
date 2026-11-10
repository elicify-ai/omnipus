// A /clear sent while a reply is running: the server (deferClearUntilStopped)
// answers in this exact order - received, then held (the running reply is being
// stopped so the clear can apply), then EITHER the clear marker lands in the
// transcript (no further message_status; the row is replaced) OR failed. A
// received/working ack never moves a /clear row; held stays until the end.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import { pendingClearRefetches } from './runtime-state'
import type { ServerFrame } from '@/lib/ws'
import { clearHistoryPage } from '../../../tests/chat-history-paging/legacy-wire'

const requestMock = vi.hoisted(() => vi.fn())
vi.mock('@/lib/api/http', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api/http')>()), request: requestMock }))

const SID = 'sess-clear-held-seq'
const raw = (id: string, role: string, content: string, ts: string) => ({ id, role, content, timestamp: ts, agent_id: 'jim', status: 'ok' })
const POST_CLEAR = clearHistoryPage(SID, [raw('srv-marker', 'system', 'Conversation context cleared', '2026-10-10T00:00:00Z'), raw('srv-a', 'assistant', 'fresh start', '2026-10-10T00:00:01Z')], 2)

let sent: Array<Record<string, unknown>> = []
const feed = (frame: Record<string, unknown>) => useChatStore.getState().handleFrame({ session_id: SID, ...frame } as unknown as ServerFrame)
const status = (state: string) => feed({ type: 'message_status', client_message_id: String(sent[0].client_message_id), state })
const rowStatus = () => useChatStore.getState().sessionsById[SID]?.messagesById[String(sent[0].client_message_id)]?.deliveryStatus

beforeEach(() => {
  sent = []
  requestMock.mockReset()
  queryClient.removeQueries()
  delete pendingClearRefetches[SID]
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: null })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  useConnectionStore.setState({
    connection: { send: (p: unknown) => { sent.push(p as Record<string, unknown>); return true }, close: () => {} } as never,
    isConnected: true,
  } as never)
  useChatStore.getState().sendMessage('/clear')
})

describe('held /clear: received -> held -> end', () => {
  it('received -> held -> clear marker: held through the acks, then the fresh view replaces the row', async () => {
    expect(rowStatus(), 'in flight: the queued look').toBe('queued')
    status('received')
    expect(rowStatus(), 'received never moves a /clear row').toBe('queued')
    status('held')
    expect(rowStatus()).toBe('held')

    requestMock.mockImplementation(async () => POST_CLEAR)
    feed({ type: 'done', message_id: 'a-1', turn_id: 'turn-1', seq: 1, stats: { tokens: 1, cost: 0 } })
    await vi.waitFor(() => expect(pendingClearRefetches[SID]).toBeUndefined())

    const b = useChatStore.getState().sessionsById[SID]!
    expect(b.messageOrder.slice(0, 2), 'the marker leads the fresh view').toEqual(['srv-marker', 'srv-a'])
    expect(b.messageOrder.some((id) => b.messagesById[id]?.content === '/clear'), 'the held /clear row is gone').toBe(false)
  })

  it('received -> held -> failed: the failed chip, the intent is retired, and no marker is sought', () => {
    status('received')
    status('held')
    expect(rowStatus()).toBe('held')
    status('failed')
    expect(rowStatus()).toBe('failed')
    expect(pendingClearRefetches[SID], 'a refused clear leaves nothing armed').toBeUndefined()
  })

  it('negative control: an idle /clear that only gets received shows no held state', () => {
    status('received')
    expect(rowStatus()).toBe('queued')
  })
})
