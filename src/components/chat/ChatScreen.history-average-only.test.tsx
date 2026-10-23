// CHECK survivor2, spec D5/D-G1d: ONLY the drawn-row average changes.
// Real production list/TanStack/useHistoryViewport, including its memoized
// getItemKey. No virtualizer hook, effect or internal option is mocked.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { historyAttachSizing } from '@/lib/chat-history-sizing'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { VirtualizedMessageList } from './ChatMessageList'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { resetStage1App, Stage1Network, stopStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import { entry, expectValidPage, page, pos } from '../../../tests/chat-history-paging/fixtures'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'

let network: Stage1Network
let layout: ReturnType<typeof installHistoryLayout>
const path = `/api/v1/sessions/${MAIN_ID}/messages`

beforeEach(() => {
  resetStage1App()
  network = new Stage1Network()
  layout = installHistoryLayout({ virtual: true, hidden: true })
})
afterEach(async () => {
  await stopStage1App()
  layout.restore()
  expect(network.unexpected, 'all process-edge requests are declared').toStrictEqual([])
})

function RealRows() {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(state => state.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root>
    <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}
async function observeLayout() {
  await act(async () => {
    fireEvent(window, new Event('resize'))
    layout.flushResize()
  })
}

it('recomputes unmeasured composite prefixes when only A changes80→40, with stable keys and unchanged80px item heights', async () => {
  // Independent dataset:50 complete groups, each narration+one tool =>100
  // drawn rows. Physical outer groups remain80px in BOTH measurement phases.
  const sources = Array.from({ length: 50 }, (_, index) => entry(index + 1, {
    role: 'assistant', tool_calls: [{
      id: `average-only-${index + 1}`, tool: 'bash', content_state: 'full', status: 'success',
      parameters: { command: `printf source-${index + 1}` }, result: { content: 'Complete saved tool output' },
    }],
  }))
  for (const source of sources) {
    layout.state.rowHeights.set(source.history_position!, 80)
    layout.state.toolHeights.set(source.tool_calls![0].id, 80)
  }
  const response = page(sources, { sessionId: MAIN_ID, earlier: true, rows: 100, info: { limit: 100, min_rows: 20 } })
  expectValidPage(response, Array.from({ length: 50 }, (_, index) => pos(index + 1)), 100)
  network.respond('GET', path, () => json(response))
  await act(async () => {
    await useChatStore.getState().loadLatestHistory(MAIN_ID, { limit: 100, min_rows: 20 })
    useChatStore.getState().enterHistoryReading(MAIN_ID)
  })
  render(<QueryClientProvider client={queryClient}><RealRows /></QueryClientProvider>)
  await observeLayout()
  const region = screen.getByRole('region', { name: 'Conversation history' })
  const content = region.querySelector<HTMLElement>('[data-history-content]')!
  const mounted = () => [...region.querySelectorAll<HTMLElement>('[data-index]')]
  expect(mounted().length, 'positive instrument: actual virtual items mount').toBeGreaterThan(0)
  expect(mounted().length, 'positive instrument: unmeasured/unmounted source estimates really remain').toBeLessThan(50)
  expect(historyAttachSizing(MAIN_ID), 'only tool80 is positive initially; zero narration residual is excluded').toStrictEqual({ history_limit: 40, history_min_rows: 20 })
  expect(mounted().map(row => row.getBoundingClientRect().height), 'known physical outer item height, not an estimate').toStrictEqual(Array(mounted().length).fill(80))
  expect(Number.parseFloat(content.style.height), 'unmeasured two-row estimates initially use2*80, above physical50*80').toBeGreaterThan(4000)

  const messages = useChatStore.getState().messages
  const positions = useChatStore.getState().history.readerPositions
  const generation = useChatStore.getState().history.viewGeneration
  const verbose = useChatPreferencesStore.getState().verboseChatEnabled
  const initialPrefix = content.getBoundingClientRect().top + region.scrollTop
  const initialWidth = region.getBoundingClientRect().width

  // ONLY the internal drawn-row split changes: tool40 + narration40 instead
  // of tool80 + narration0. Outer resize inputs remain80. Same message array
  // means production useCallback([historicalMessages]) retains the key function.
  for (const source of sources) layout.state.toolHeights.set(source.tool_calls![0].id, 40)
  await observeLayout()
  await observeLayout()
  await waitFor(() => expect(historyAttachSizing(MAIN_ID), 'new stable drawn rows establish A40 without width/Verbose/session/source changes').toStrictEqual({ history_limit: 80, history_min_rows: 40 }))
  expect(useChatStore.getState().messages, 'no message-array identity change can invalidate the key callback').toBe(messages)
  expect(useChatStore.getState().history.readerPositions, 'no source order/count replacement can invalidate estimates').toBe(positions)
  expect(useChatStore.getState().history.viewGeneration).toBe(generation)
  expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
  expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(verbose)
  expect(region.getBoundingClientRect().width).toBe(initialWidth)
  expect(content.getBoundingClientRect().top + region.scrollTop, 'no boundary-prefix change can rescue the missing dependency').toBe(initialPrefix)
  expect(mounted().map(row => row.getBoundingClientRect().height), 'no outer item-size change can invalidate the virtualizer cache').toStrictEqual(Array(mounted().length).fill(80))
  expect(content.style.height, 'D5 current2-row estimate=2*40=80;50 complete groups total4000, not stale80-based prefixes').toBe('4000px')

  await act(async () => {
    region.scrollTop = initialPrefix + 2400 // Independent source30 prefix:30*80.
    fireEvent.scroll(region)
  })
  const row30 = region.querySelector<HTMLElement>('[data-index="30"]')
  expect(row30, 'actual recalculated prefix mounts source index30').not.toBeNull()
  expect(row30!.dataset.historyPosition).toBe(pos(31))
  expect(row30!.getBoundingClientRect().top - region.getBoundingClientRect().top, 'actual production translateY places30*80 at scroll2400').toBe(0)
  expect(mounted().length, 'real virtualization remains active after prefix recomputation').toBeLessThan(50)
  expect(network.requests.filter(read => read.path === path), 'hidden geometry probe performs exactly its bounded source read').toHaveLength(1)
})
