// Founder decision 2026-10-30: the opening fill targets about THREE screens
// of history (was four); the older-scroll trigger stays at two screens.
// Real adapter, store, virtualizer and fill loop; only HTTP and physical
// layout are controlled by the existing browser-boundary instrument (T64).
// The view opens visible, so the opening fill starts in the latest mode.
import { afterEach, expect, it } from 'vitest'
import { act, render, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { queryClient } from '@/lib/queryClient'
import { VirtualizedMessageList } from './ChatMessageList'
import { useChatStore } from '@/store/chat'
import { forgetHistoryGeometry, historyAttachSizing, recordHistoryGeometry } from '@/lib/chat-history-sizing'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { Stage1Network, resetStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import { entry, page, pos } from '../../../tests/chat-history-paging/fixtures'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'
import { settleScrollLayout, stopScrollCase } from '../../../tests/chat-history-paging/scroll-regression-harness'

let layout: ReturnType<typeof installHistoryLayout> | undefined
function RealRows() {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(state => state.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root>
    <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}
afterEach(async () => {
  forgetHistoryGeometry('three-screens')
  if (layout) await stopScrollCase(layout)
  layout = undefined
})

it('attach sizing asks for about three screens and keeps the two-screen floor', () => {
  // H=800, A=40: ceil(3*800/40)=60 rows; floor ceil(2*800/40)=40.
  recordHistoryGeometry('three-screens', 800, 40)
  expect(historyAttachSizing('three-screens')).toStrictEqual({ history_limit: 60, history_min_rows: 40 })
  // H=800, A=80: ceil(3*800/80)=30 rows; floor 20.
  recordHistoryGeometry('three-screens', 800, 80)
  expect(historyAttachSizing('three-screens')).toStrictEqual({ history_limit: 30, history_min_rows: 20 })
})

it('the opening fill stops once about three screens of rows are loaded', async () => {
  // H=800, every row 80px. The latest page holds 25 rows (2000px < 3H=2400).
  // Each older page holds 5 rows (400px, cut by the byte ceiling below the
  // floor, as T64 does). One older page reaches 3H exactly, so the opening
  // ends after ONE older read; a four-screen goal (3200px) would need three.
  resetStage1App()
  const network = new Stage1Network()
  layout = installHistoryLayout({ virtual: true, hidden: false })
  const path = `/api/v1/sessions/${MAIN_ID}/messages`
  network.respond('GET', path, request => {
    const query = new URLSearchParams(request.query)
    const before = query.get('before')
    if (!before) return json(page(Array.from({ length: 25 }, (_, i) => entry(36 + i)), { sessionId: MAIN_ID, earlier: true, info: { limit: 25, min_rows: 20 } }))
    const last = before === pos(36) ? 35 : before === pos(31) ? 30 : before === pos(26) ? 25 : -1
    expect(last, 'every older read continues from the loaded boundary').not.toBe(-1)
    return json(page(Array.from({ length: 5 }, (_, i) => entry(last - 4 + i)), {
      sessionId: MAIN_ID, earlier: true, later: true,
      info: { mode: 'older', limit: Number(query.get('limit')), min_rows: Number(query.get('min_rows')), stop_reason: 'byte_ceiling' },
    }))
  })
  await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID, { limit: 25, min_rows: 20 }) })
  expect(useChatStore.getState().history.readerPositions, 'positive instrument: the latest page is held').toHaveLength(25)
  render(<QueryClientProvider client={queryClient}><RealRows /></QueryClientProvider>)
  await settleScrollLayout()
  await waitFor(() => expect(useChatStore.getState().history.readerPositions.length, 'positive instrument: the opening fill reads older rows').toBeGreaterThanOrEqual(30))
  // Give a further top-up every chance to start before counting the reads.
  for (let i = 0; i < 5; i++) {
    await settleScrollLayout()
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)) })
  }
  await waitFor(() => expect(useChatStore.getState().history.requests.older.status).toBe('idle'))
  const older = network.requests.filter(request => request.path === path && new URLSearchParams(request.query).has('before'))
  expect(older.map(request => new URLSearchParams(request.query).get('before')), 'three screens are reached after one older page').toEqual([pos(36)])
  expect(useChatStore.getState().history.readerPositions).toEqual(Array.from({ length: 30 }, (_, i) => pos(31 + i)))
  expect(network.unexpected).toEqual([])
})
