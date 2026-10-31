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
  expect(historyAttachSizing(MAIN_ID), 'only tool80 is positive initially; zero narration residual is excluded').toStrictEqual({ history_limit: 30, history_min_rows: 20 })
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
  await waitFor(() => expect(historyAttachSizing(MAIN_ID), 'new stable drawn rows establish A40 without width/Verbose/session/source changes').toStrictEqual({ history_limit: 60, history_min_rows: 40 }))
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

it('M1 recomputes unmeasured estimates when only the drawn average changes inside a fixed mounted window', async () => {
  // D5:50 two-row sources. H80 is smaller than the initial default estimate
  // (2*80), every outer item128, and both measured composite estimates.
  // Bootstrap and both phases keep ONLY index0 plus the SAME five overscan
  // rows: no retained sample from a formerly mounted source can pollute A.
  layout.state.height = 80
  const sources = Array.from({ length: 50 }, (_, index) => entry(index + 1, {
    role: 'assistant', tool_calls: [{
      id: `average-fixed-window-${index + 1}`, tool: 'bash', content_state: 'full', status: 'success',
      parameters: { command: `printf source-${index + 1}` }, result: { content: 'Complete saved tool output' },
    }],
  }))
  for (const source of sources) {
    layout.state.rowHeights.set(source.history_position!, 128)
    layout.state.toolHeights.set(source.tool_calls![0].id, 128)
  }
  const response = page(sources, { sessionId: MAIN_ID, earlier: true, rows: 100, info: { limit: 100, min_rows: 20 } })
  expectValidPage(response, Array.from({ length: 50 }, (_, index) => pos(index + 1)), 100)
  network.respond('GET', path, () => json(response))
  await act(async () => {
    await useChatStore.getState().loadLatestHistory(MAIN_ID, { limit: 100, min_rows: 20 })
    useChatStore.getState().enterHistoryReading(MAIN_ID)
  })
  // An ancestor insertion can report the same indexed descendant again.
  // Deduplicate by Element identity, never by index: a replacement node
  // with the same index must still count as a separate physical mount.
  const initialMounts = new Set<Element>()
  const bootstrap = new MutationObserver(records => {
    for (const record of records) for (const node of record.addedNodes) {
      if (!(node instanceof Element)) continue
      const indexed = [...(node.matches('[data-index]') ? [node] : []), ...node.querySelectorAll('[data-index]')]
      for (const row of indexed) initialMounts.add(row)
    }
  })
  bootstrap.observe(document.body, { subtree: true, childList: true })
  try {
    render(<QueryClientProvider client={queryClient}><RealRows /></QueryClientProvider>)
    await observeLayout()
    // Initial split: tool128 + narration0 (excluded) => A128.
    await waitFor(() => expect(historyAttachSizing(MAIN_ID)).toStrictEqual({ history_limit: 20, history_min_rows: 2 }))
    await observeLayout()
  } finally {
    bootstrap.disconnect()
  }
  expect([...initialMounts].map(row => row.getAttribute('data-index')), 'bootstrap never samples a source outside the fixed six-node window').toStrictEqual(['0', '1', '2', '3', '4', '5'])
  const region = screen.getByRole('region', { name: 'Conversation history' })
  const content = region.querySelector<HTMLElement>('[data-history-content]')!
  const mounted = () => [...region.querySelectorAll<HTMLElement>('[data-index]')]
  expect(mounted().map(row => row.dataset.index)).toStrictEqual(['0', '1', '2', '3', '4', '5'])
  const originalRows = mounted()
  expect(originalRows.map(row => row.getBoundingClientRect().height)).toStrictEqual(Array(6).fill(128))
  expect(Number.parseFloat(content.style.height), 'positive control: unmeasured two-row A128 estimates exceed50 actual128px sources').toBeGreaterThan(6400)

  const messages = useChatStore.getState().messages
  const positions = useChatStore.getState().history.readerPositions
  const generation = useChatStore.getState().history.viewGeneration
  const verbose = useChatPreferencesStore.getState().verboseChatEnabled
  const initialPrefix = content.getBoundingClientRect().top + region.scrollTop
  const initialWidth = region.getBoundingClientRect().width
  const initialTop = region.scrollTop
  const additions: Element[] = []
  const removals: Element[] = []
  const collectMountChanges = (records: MutationRecord[]) => {
    for (const record of records) {
      for (const [nodes, changes] of [[record.addedNodes, additions], [record.removedNodes, removals]] as const) {
        for (const node of nodes) {
          if (!(node instanceof Element)) continue
          if (node.matches('[data-index]')) changes.push(node)
          changes.push(...node.querySelectorAll('[data-index]'))
        }
      }
    }
  }
  const mounts = new MutationObserver(collectMountChanges)
  mounts.observe(region, { subtree: true, childList: true })
  try {
    // ONLY the tool/narration split changes. Outer128, messages, key owners,
    // source count/order, session, width and presentation remain unchanged.
    for (const source of sources) layout.state.toolHeights.set(source.tool_calls![0].id, 64)
    await observeLayout()
    await observeLayout()
    // New split: tool64 + narration64 => A64. D5 floors are2→3.
    await waitFor(() => expect(historyAttachSizing(MAIN_ID)).toStrictEqual({ history_limit: 20, history_min_rows: 3 }))
    collectMountChanges(mounts.takeRecords())
    expect(additions, 'no indexed row mounted, even transiently, during the average change').toStrictEqual([])
    expect(removals, 'no indexed row unmounted during the average change').toStrictEqual([])
    expect(mounted().map(row => row.dataset.index), 'same virtual window, not merely the same mounted count').toStrictEqual(['0', '1', '2', '3', '4', '5'])
    for (const [index, row] of originalRows.entries()) {
      expect(mounted()[index], 'same physical row node and key, not a replacement').toBe(row)
      expect(row.isConnected).toBe(true)
    }
    expect(screen.getByRole('region', { name: 'Conversation history' })).toBe(region)
    expect(region.querySelector('[data-history-content]')).toBe(content)
    expect(useChatStore.getState().messages).toBe(messages)
    expect(useChatStore.getState().history.readerPositions).toBe(positions)
    expect(useChatStore.getState().history.viewGeneration).toBe(generation)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(verbose)
    expect(region.getBoundingClientRect().width).toBe(initialWidth)
    expect(region.getBoundingClientRect().height).toBe(80)
    expect(region.scrollTop).toBe(initialTop)
    expect(content.getBoundingClientRect().top + region.scrollTop).toBe(initialPrefix)
    expect(mounted().map(row => row.getBoundingClientRect().height), 'no physical item-size change can invalidate the cache').toStrictEqual(Array(6).fill(128))
    expect(content.style.height, 'D5:50 complete two-row groups at current A64 total50*2*64, including unmeasured sources').toBe('6400px')
    expect(network.requests.filter(read => read.path === path), 'no source replacement or extra network load can rescue the estimate').toHaveLength(1)
  } finally {
    mounts.disconnect()
  }
})
