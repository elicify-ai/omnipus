// T14, B03/B11/B13. Independent oracle: D6 own-clear/latest_clear, D15.
// Real query client, adapter, store and operation owner; only fetch is controlled.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { waitFor } from '@testing-library/react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { queryClient } from '@/lib/queryClient'
import { armClearRefresh } from './chat/clear-refetch'
import { pendingClearRefetches } from './chat/runtime-state'
import { resetRealStores } from '../../tests/l1-room-ui/harness'
import { deferred, entry, expectValidPage, page, pos, SID, STAMP } from '../../tests/chat-history-paging/fixtures'

beforeEach(() => {
  resetRealStores()
  useSessionStore.getState().setActiveSession(SID, 'mia')
  useChatStore.getState().setMessages([
    { id: 'before-clear', role: 'assistant', content: 'Before clear', timestamp: STAMP },
    { id: 'own-clear', role: 'user', content: '/clear', timestamp: STAMP, clientMessageId: 'own-clear' },
    { id: 'pending-input', role: 'user', content: 'Keep queued input', timestamp: STAMP, deliveryStatus: 'queued' },
  ])
})
afterEach(() => {
  delete pendingClearRefetches[SID]
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  vi.unstubAllGlobals()
})

function holdRead() {
  const seen = deferred<void>()
  const response = deferred<Response>()
  const calls: string[] = []
  vi.stubGlobal('fetch', (url: RequestInfo | URL) => { calls.push(String(url)); seen.resolve(); return response.promise })
  return { seen, response, calls }
}
async function finishRead(barrier: ReturnType<typeof holdRead>, value: unknown) {
  barrier.response.resolve(new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } }))
  await waitFor(() => expect(queryClient.getQueryState(['messages', SID])?.fetchStatus).toBe('idle'))
}

describe('T14 operation-owned page refresh — B03/B11/B13', () => {
  it('a new latest_clear outside the selected page confirms only the owning clear and preserves queued input', async () => {
    armClearRefresh(SID, 'own-clear', null, ['previous-clear'])
    const barrier = holdRead()
    useChatStore.getState().retryClearTranscript(SID)
    await barrier.seen.promise
    const selectedRanks = Array.from({ length: 40 }, (_, i) => 200 + i)
    const selected = page(selectedRanks.map((rank) => entry(rank)), {
      earlier: true,
      info: { latest_clear: { entry_id: 'own-clear-marker', history_position: pos(150) } },
    })
    expectValidPage(selected, selectedRanks.map(pos), 40)
    // The marker is deliberately NOT in entries. D6 says latest_clear still
    // confirms this operation; do not add the distant divider across a gap.
    expect(selected.entries.map((row) => row.id)).toStrictEqual(selectedRanks.map((rank) => `history-${rank}`))
    await finishRead(barrier, selected)
    expect(pendingClearRefetches[SID], 'D6 marker metadata confirms without an unbounded read to find its body').toBeUndefined()
    expect(useChatStore.getState().messages.map((row) => row.id)).toStrictEqual([...selectedRanks.map((rank) => `history-${rank}`), 'pending-input'])
    expect(barrier.calls, 'one bounded latest page, never a clear/user resend').toStrictEqual([`/api/v1/sessions/${SID}/messages`])
  })

  it('an older page carrying an old marker cannot settle a newly armed clear', async () => {
    armClearRefresh(SID, 'own-clear', null, ['previous-clear'])
    const owner = pendingClearRefetches[SID]
    const original = useChatStore.getState().messages.map((row) => row.id)
    const barrier = holdRead()
    useChatStore.getState().retryClearTranscript(SID)
    await barrier.seen.promise
    const stale = page([entry(150, { id: 'previous-clear', role: 'system', type: 'system', content: 'Conversation context cleared' })], {
      earlier: true, info: { mode: 'older', stop_reason: 'clear', stopped_at_clear: true, latest_clear: { entry_id: 'previous-clear', history_position: pos(150) } },
    })
    expectValidPage(stale, [pos(150)], 1)
    await finishRead(barrier, stale)
    expect(pendingClearRefetches[SID], 'D6 no historical marker may confirm a newer operation').toBe(owner)
    expect(useChatStore.getState().messages.map((row) => row.id)).toStrictEqual(original)
    expect(barrier.calls).toStrictEqual([`/api/v1/sessions/${SID}/messages`])
  })

  it('a delayed successful clear read cannot replace the view after a newer clear owns the operation', async () => {
    armClearRefresh(SID, 'own-clear', null, [])
    const barrier = holdRead()
    useChatStore.getState().retryClearTranscript(SID)
    await barrier.seen.promise
    armClearRefresh(SID, 'newer-clear', null, [])
    const replacement = pendingClearRefetches[SID]
    const original = useChatStore.getState().messages.map((row) => row.id)
    const stale = page([entry(1, { role: 'system', type: 'system', content: 'Conversation context cleared' })], {
      info: { latest_clear: { entry_id: 'history-1', history_position: pos(1) } },
    })
    expectValidPage(stale, [pos(1)], 1)
    await finishRead(barrier, stale)
    expect(pendingClearRefetches[SID]).toBe(replacement)
    expect(pendingClearRefetches[SID]?.clientMessageId).toBe('newer-clear')
    expect(useChatStore.getState().messages.map((row) => row.id)).toStrictEqual(original)
  })
})
