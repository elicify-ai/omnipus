// P14: D5 contiguous older fill + D9 one contiguous bounded reader. Loaded
// recent rows may be virtualized offscreen, never skipped by one earlier read.
// D8 preserves the actual visible anchor across prepend/control layout.
// Entirely synthetic data; no Lane T account/session/transcript is copied.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { VirtualizedMessageList } from './ChatMessageList'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { resetStage1App, Stage1Network, stopStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import { deferred, entry, expectValidPage, page, pos } from '../../../tests/chat-history-paging/fixtures'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'

let network: Stage1Network
let layout: ReturnType<typeof installHistoryLayout>
const endpoint = `/api/v1/sessions/${MAIN_ID}/messages`
const label = (rank: number) => `P14 synthetic source ${String(rank).padStart(2, '0')}`
beforeEach(() => {
  resetStage1App()
  network = new Stage1Network()
  layout = installHistoryLayout({ virtual: true, hidden: true })
})
afterEach(async () => {
  await stopStage1App()
  layout.restore()
  expect(network.unexpected, 'all process-edge reads remain explicitly declared').toStrictEqual([])
})
function RealRows() {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(state => state.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root>
    <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}
async function settleLayout() {
  await act(async () => { fireEvent(window, new Event('resize')); layout.flushResize() })
}

it('P14 one older read after reload keeps the initially unmounted recent band reachable exactly once', async () => {
  // 40*80=3200=4H recent fill;20*80=1600=2H older fill; all60<500.
  const sources = Array.from({ length: 60 }, (_, i) => entry(i + 1, { id: `p14-source-${i + 1}`, role: 'assistant', content: label(i + 1) }))
  const recent = page(sources.slice(20), { sessionId: MAIN_ID, earlier: true, info: { limit: 40, min_rows: 20 } })
  const older = page(sources.slice(0, 20), { sessionId: MAIN_ID, later: true, info: { mode: 'older', limit: 20, min_rows: 20 } })
  expectValidPage(recent, sources.slice(20).map(row => row.history_position!), 40)
  expectValidPage(older, sources.slice(0, 20).map(row => row.history_position!), 20)
  const earlierResponse = deferred<Response>()
  network.respond('GET', endpoint, request => {
    if (new URLSearchParams(request.query).get('before')) {
      expect(Object.fromEntries(new URLSearchParams(request.query)), 'D3/D5: the next contiguous page uses the actual loaded source boundary, never a guessed virtual index').toStrictEqual({ before: pos(21), limit: '20', min_rows: '20' })
      return earlierResponse.promise
    }
    expect(Object.fromEntries(new URLSearchParams(request.query))).toStrictEqual({ limit: '40', min_rows: '20' })
    return json(recent)
  })
  await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID, { limit: 40, min_rows: 20 }) })
  render(<QueryClientProvider client={queryClient}><RealRows /></QueryClientProvider>)
  await settleLayout()
  const region = screen.getByRole('region', { name: 'Conversation history' })
  const mounted = () => [...region.querySelectorAll<HTMLElement>('[data-history-anchor]')]
  for (let i = 0; i < 2; i++) {
    await act(async () => { region.scrollTop = region.scrollHeight; fireEvent.scroll(region) })
    await settleLayout()
  }
  await waitFor(() => expect(region.querySelector(`[data-history-position="${pos(60)}"]`), 'positive virtualizer instrument: recent end actually mounted').not.toBeNull())
  expect(mounted().length, 'positive virtualizer instrument: some loaded recent rows really are unmounted').toBeLessThan(40)
  expect(region.querySelector(`[data-history-position="${pos(21)}"]`), 'the later walk must recover a loaded-but-unmounted band, not merely test a plain list').toBeNull()
  await act(async () => { layout.visibility(false) })
  await settleLayout()
  expect(network.requests.filter(read => read.path === endpoint)).toHaveLength(1)
  const firstVisible = mounted().find(row => { const rect = row.getBoundingClientRect(); return rect.top >= 0 && rect.bottom <= layout.state.height })
  expect(firstVisible, 'instrument: an actual visible source anchor exists').toBeDefined()
  const anchorPosition = firstVisible!.dataset.historyPosition!
  const anchorTop = firstVisible!.getBoundingClientRect().top

  await userEvent.click(screen.getByRole('button', { name: 'Show earlier messages' }))
  await waitFor(() => expect(network.requests.filter(read => read.path === endpoint && new URLSearchParams(read.query).get('before'))).toHaveLength(1))
  expect(useChatStore.getState().history.viewMode, 'Show earlier leaves follow-latest before its real read').toBe('historical')
  await act(async () => { earlierResponse.resolve(json(older)) })
  await waitFor(() => expect(useChatStore.getState().history.requests.older.status).toBe('idle'))
  await settleLayout()
  const held = region.querySelector<HTMLElement>(`[data-history-position="${anchorPosition}"]`)
  expect(held, 'D8: the previously visible source did not jump out of the window at prepend').not.toBeNull()
  expect(held!.getBoundingClientRect().top, 'D8 signed anchor compensation keeps the same actual reader offset').toBe(anchorTop)

  const seen = new Set<string>()
  const observeRows = async () => {
    await waitFor(() => {
      const rows = mounted()
      const ids = rows.map(row => row.dataset.historyPosition!)
      expect(new Set(ids).size, 'one source occurrence is never duplicated in a rendered layout').toBe(ids.length)
      for (const row of rows) {
        const rank = sources.findIndex(source => source.history_position === row.dataset.historyPosition) + 1
        expect(rank, 'no fabricated occurrence is rendered').toBeGreaterThan(0)
        expect(row.textContent, 'a position wrapper alone is not readable source content').toContain(label(rank))
      }
    })
    for (const row of mounted()) seen.add(row.dataset.historyPosition!)
  }
  // Overlapping half-screen steps cannot jump an80px synthetic group. They
  // drive the REAL virtualizer, not a mocked list of all source rows.
  for (let top = Math.max(0, region.scrollHeight - region.clientHeight); ; top = Math.max(0, top - layout.state.height / 2)) {
    await act(async () => { region.scrollTop = top; fireEvent.scroll(region) })
    await settleLayout()
    const expectedRank = Math.min(60, Math.floor(top / 80) + 1)
    await waitFor(() => expect(region.querySelector(`[data-history-position="${pos(expectedRank)}"]`), 'the real viewport walk reaches each retained band').not.toBeNull())
    await observeRows()
    if (top === 0) break
  }
  expect(seen, 'D5/D9: no loaded-but-unmounted band or page-boundary source becomes unreachable after the one read').toStrictEqual(new Set(sources.map(row => row.history_position!)))
  const b = useChatStore.getState().sessionsById[MAIN_ID]!
  expect(b.messageOrder).toStrictEqual(sources.map(row => row.id))
  expect(b.history.readerPositions).toStrictEqual(sources.map(row => row.history_position))
  expect(b.history.readerPositions.length).toBeLessThanOrEqual(500)
  expect(b.history.tailEntries.length).toBeLessThanOrEqual(100)
  expect(network.requests.filter(read => read.path === endpoint), 'one fresh load plus exactly one physical older fetch').toHaveLength(2)
})
