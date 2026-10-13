// R2-S3-06 (refetch half): an unresolvable /clear (the clear never reached the
// server, or its marker is not recognised) kept the refresh intent armed and
// re-read the WHOLE transcript after EVERY turn end, forever. Ruling S3-02
// still holds - no deadline, no user surface, nothing is given up - so the
// fix is cost only: after a generous run of reads that settle nothing, the
// re-read backs off to every Nth turn end (doubling, capped), while a user
// Retry and the first reads stay immediate.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import { pendingClearRefetches } from './runtime-state'
import { retryClearTranscriptRead, FREE_UNRESOLVED_READS, MAX_TURN_ENDS_BETWEEN_READS } from './clear-refetch'
import type { ServerFrame } from '@/lib/ws'

const requestMock = vi.hoisted(() => vi.fn())
vi.mock('@/lib/api/http', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api/http')>()), request: requestMock }))

const SID = 'sess-clear-backoff'
const raw = (id: string, role: string, content: string, ts: string) => ({ id, role, content, timestamp: ts, agent_id: 'jim', status: 'ok' })
const PRE_CLEAR = [raw('srv-u-1', 'user', 'earlier question', '2026-10-09T23:00:00Z'), raw('srv-a-1', 'assistant', 'earlier answer', '2026-10-09T23:00:05Z')]
const POST_CLEAR = [raw('srv-marker', 'system', 'Conversation context cleared', '2026-10-10T00:00:00Z')]

const settle = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0))
let seq = 0
async function turnEnd(): Promise<void> {
  seq += 1
  useChatStore.getState().handleFrame({ type: 'done', session_id: SID, message_id: `a-${seq}`, turn_id: `turn-${seq}`, seq, stats: { tokens: 1, cost: 0 } } as ServerFrame)
  await settle()
}
const withBucket = (sid: string | null, updater: (b: never) => object) => {
  useChatStore.setState((st) => {
    const b = st.sessionsById[sid!]
    return { ...st, sessionsById: { ...st.sessionsById, [sid!]: { ...b, ...updater(b as never) } } } as never
  })
}

beforeEach(() => {
  seq = 0
  requestMock.mockReset()
  queryClient.removeQueries()
  delete pendingClearRefetches[SID]
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: null })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  useConnectionStore.setState({ connection: { send: () => true, close: () => {} } as never, isConnected: true } as never)
  requestMock.mockImplementation(async () => PRE_CLEAR)
  useChatStore.getState().sendMessage('/clear')
})

describe('unresolvable /clear: the transcript re-read backs off', () => {
  it('the first reads are immediate - one per turn end', async () => {
    for (let i = 1; i <= FREE_UNRESOLVED_READS; i++) {
      await turnEnd()
      expect(requestMock).toHaveBeenCalledTimes(i)
    }
  })

  it('then re-reads far less often than once per turn end, but never stops', async () => {
    const turnEnds = 60
    for (let i = 0; i < turnEnds; i++) await turnEnd()
    const reads = requestMock.mock.calls.length
    expect(reads, 'free reads happened').toBeGreaterThanOrEqual(FREE_UNRESOLVED_READS)
    expect(reads, `not one read per turn end (${turnEnds})`).toBeLessThan(turnEnds / 2)
    const before = reads
    for (let i = 0; i < MAX_TURN_ENDS_BETWEEN_READS; i++) await turnEnd()
    expect(requestMock.mock.calls.length, 'still reads again within the cap').toBeGreaterThan(before)
    expect(pendingClearRefetches[SID], 'nothing is given up').toBeTruthy()
  })

  it('a clear that finally executes is applied within the cap, however long the back-off ran', async () => {
    for (let i = 0; i < 60; i++) await turnEnd()
    requestMock.mockImplementation(async () => POST_CLEAR)
    for (let i = 0; i < MAX_TURN_ENDS_BETWEEN_READS && pendingClearRefetches[SID]; i++) await turnEnd()
    expect(pendingClearRefetches[SID], 'the marker retired the intent').toBeUndefined()
  })

  it('a user Retry reads immediately even in the middle of a back-off', async () => {
    for (let i = 0; i < 20; i++) await turnEnd()
    const before = requestMock.mock.calls.length
    retryClearTranscriptRead(SID, withBucket as never)
    await settle()
    expect(requestMock.mock.calls.length).toBe(before + 1)
  })
})
