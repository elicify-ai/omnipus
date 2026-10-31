// T24 B26 (CI run 40958296852): a reader parked on an older interval keeps its
// rows when the current turn ends while it reads. Live reducers run on the
// current-tail projection; one that rebuilds the message maps from that tail
// (a Stop placeholder, a fresh error/cancel-ack bubble) must not erase the
// reader's own rows. REAL store/reducers; only HTTP is controlled.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { queryClient } from '@/lib/queryClient'
import { codeToDisplay } from '@/lib/llm-error'
import { resetRealStores } from '../../tests/l1-room-ui/harness'
import { entry, expectValidPage, page, pos, SID } from '../../tests/chat-history-paging/fixtures'

function response(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}
beforeEach(() => {
  resetRealStores()
  useSessionStore.getState().setActiveSession(SID, 'mia')
})
afterEach(() => {
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.unstubAllGlobals()
})

const latestRanks = Array.from({ length: 40 }, (_, i) => 101 + i)
const olderRanks = Array.from({ length: 20 }, (_, i) => 81 + i)
const readerIds = [...olderRanks, ...latestRanks].map(rank => `history-${rank}`)

/** Open the latest page, start reading, and page one older interval in. */
async function readOlderInterval() {
  const latest = page(latestRanks.map(rank => entry(rank)), { earlier: true })
  expectValidPage(latest, latestRanks.map(pos), 40)
  vi.stubGlobal('fetch', async () => response(latest))
  await useChatStore.getState().loadLatestHistory(SID)
  useChatStore.getState().enterHistoryReading(SID)
  const older = page(olderRanks.map(rank => entry(rank)), { earlier: true, later: true, info: { mode: 'older', limit: 20, min_rows: 20 } })
  expectValidPage(older, olderRanks.map(pos), 20)
  vi.stubGlobal('fetch', async () => response(older))
  await useChatStore.getState().loadOlderHistory(SID)
  const bucket = useChatStore.getState().sessionsById[SID]
  expect(bucket.history.viewMode, 'instrument: the reader is in historical mode').toBe('historical')
  expect(bucket.messageOrder, 'instrument: the reader holds the older interval plus the opened page').toStrictEqual(readerIds)
  expect(bucket.history.tailEntries.map(row => row.id), 'instrument: the older interval is not part of the cached current tail').not.toContain('history-81')
}

function readerRows() {
  const bucket = useChatStore.getState().sessionsById[SID]
  return bucket.messageOrder.filter(id => bucket.messagesById[id]?.historyPosition)
}

describe('T24 B26: a parked reader keeps its rows when the current turn ends', () => {
  it('Stop while reading (no open reply in the reader) keeps every reader row', async () => {
    await readOlderInterval()
    useChatStore.getState().markLastMessageInterrupted(SID)
    expect(readerRows(), 'B26: Stop cannot erase the rows the reader is anchored in').toStrictEqual(readerIds)
    expect(useChatStore.getState().messages.map(m => m.id).slice(0, readerIds.length)).toStrictEqual(readerIds)
  })

  it('a live cancel-ack error frame for the current turn keeps every reader row', async () => {
    await readOlderInterval()
    // The cancel-ack text comes from the error catalogue (llm_error_no_hardcopy_test.go).
    const stopCopy = codeToDisplay.turn_canceled
    expect(stopCopy, 'the catalogue carries the stopped-turn message').toMatch(/\S/)
    useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: stopCopy })
    expect(readerRows(), 'B26: the current turn ending cannot erase the rows the reader is anchored in').toStrictEqual(readerIds)
  })
})
