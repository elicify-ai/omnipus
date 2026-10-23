// Survivor M1 (check-survivor2), spec D5/D-G1d: when ONLY the drawn-row
// average changes, unmeasured groups ABOVE a reader at the recent end must be
// re-estimated. The sibling average-only test reads from the top, where the
// first measurement of newly mounted rows already recomputes every later
// estimate (TanStack resizeItem sets pendingMin), so removing viewport.average
// from VirtualizedMessageListInner's virtualizer.measure() effect survived it.
// Here the stale estimates sit before every measured index, so nothing but
// that effect can refresh them. Real list/TanStack/useHistoryViewport; only
// the browser layout edge is deterministic.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { historyAttachSizing } from '@/lib/chat-history-sizing'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
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

it('re-estimates unmeasured groups above a recent-end reader when only A changes to 40', async () => {
  // 50 groups, each narration + one tool = 2 drawn rows; every group is a
  // physical 80px in BOTH phases, so the physical total is 50*80 = 4000.
  // Older groups (1-30) already split 40+40; recent groups (31-50) start as
  // tool80 + empty narration and later become 40+40, so every retained
  // sample - including those of older rows measured at open and since
  // unmounted - ends at 40 and A becomes exactly 40.
  const sources = Array.from({ length: 50 }, (_, index) => entry(index + 1, {
    role: 'assistant', tool_calls: [{
      id: `above-reader-${index + 1}`, tool: 'bash', content_state: 'full', status: 'success',
      parameters: { command: `printf source-${index + 1}` }, result: { content: 'Complete saved tool output' },
    }],
  }))
  for (const source of sources) {
    layout.state.rowHeights.set(source.history_position!, 80)
    layout.state.toolHeights.set(source.tool_calls![0].id, Number(source.id.split('-')[1]) <= 30 ? 40 : 80)
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
  const total = () => Number.parseFloat(content.style.height)

  // Move the reader to the recent end; repeat because each measured row
  // shrinks the stale 2*80 estimates and therefore the reachable bottom.
  for (let pass = 0; pass < 4; pass++) {
    await act(async () => {
      region.scrollTop = region.scrollHeight
      fireEvent.scroll(region)
    })
    await observeLayout()
  }
  expect(mounted().some(row => row.dataset.index === '49'), 'positive instrument: the newest group is mounted and measured').toBe(true)
  expect(mounted().length, 'positive instrument: real virtualization leaves groups unmounted').toBeLessThan(50)
  expect(historyAttachSizing(MAIN_ID).history_limit, 'precondition: mixed 40/80 samples, A is above 40').toBeLessThan(80)
  expect(total(), 'precondition: unmeasured groups above the reader carry a 2*A estimate above 80').toBeGreaterThan(4000)
  const messages = useChatStore.getState().messages

  // ONLY the internal split changes (tool40 + narration40); outer groups stay
  // 80px, so no virtualizer item resize can refresh the estimates above.
  for (const source of sources) layout.state.toolHeights.set(source.tool_calls![0].id, 40)
  await observeLayout()
  await observeLayout()
  await waitFor(() => expect(historyAttachSizing(MAIN_ID), 'A40 established').toStrictEqual({ history_limit: 80, history_min_rows: 40 }))
  expect(useChatStore.getState().messages, 'no message-array identity change can rescue the estimates').toBe(messages)
  expect(mounted().map(row => row.getBoundingClientRect().height), 'no outer item-size change').toStrictEqual(Array(mounted().length).fill(80))
  expect(content.style.height, 'D5: unmeasured groups above now estimate 2*A40 = 80, so the total is the physical 4000').toBe('4000px')
  expect(network.requests.filter(read => read.path === path), 'hidden geometry probe performs exactly its bounded source read').toHaveLength(1)
})
