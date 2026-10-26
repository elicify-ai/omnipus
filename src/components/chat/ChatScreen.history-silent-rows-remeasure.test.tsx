// Final-campaign blank history (lane V / lane T R1): after a window loads, the
// drawn-row average changes and the list calls virtualizer.measure(), which in
// @tanstack/virtual-core drops EVERY cached size. Mounted rows whose size did
// not change are never re-observed, so they fall back to an estimate of one
// full row. A silent source row (a saved helper lifecycle entry) draws nothing,
// so each one silently added a whole row of empty canvas, and the live-end pin
// then showed blank space below the real rows. D8: no large blank replacement.
// Real production list/TanStack/useHistoryViewport; only layout is modelled.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { VirtualizedMessageList } from './ChatMessageList'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { resetStage1App, Stage1Network, stopStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import { entry, page } from '../../../tests/chat-history-paging/fixtures'
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
async function observeLayoutOnce() {
  await act(async () => {
    fireEvent(window, new Event('resize'))
    layout.flushResize()
  })
}

it('a drawn-row average change keeps mounted silent rows at their measured zero height, not a full-row estimate', async () => {
  // Six visible saved messages, then five silent saved rows (nothing drawn),
  // the shape a finished helper pair leaves at the recent end.
  const visible = Array.from({ length: 6 }, (_, index) => entry(index + 1, { role: index % 2 ? 'assistant' : 'user', content: `Visible saved row ${index + 1}` }))
  const silent = Array.from({ length: 5 }, (_, index) => entry(index + 7, { role: 'assistant', content: '' }))
  for (const source of visible) layout.state.rowHeights.set(source.history_position!, 80)
  for (const source of silent) layout.state.rowHeights.set(source.history_position!, 0)
  network.respond('GET', path, () => json(page([...visible, ...silent], { sessionId: MAIN_ID })))
  await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID, { limit: 40, min_rows: 20 }) })
  render(<QueryClientProvider client={queryClient}><RealRows /></QueryClientProvider>)
  await observeLayoutOnce()
  await observeLayoutOnce()
  const region = screen.getByRole('region', { name: 'Conversation history' })
  const content = region.querySelector<HTMLElement>('[data-history-content]')!
  const canvas = () => Number.parseFloat(content.style.height)
  expect(region.querySelectorAll('[data-index]'), 'positive instrument: every row is mounted').toHaveLength(11)
  expect(canvas(), 'positive instrument: the canvas is the real drawn height before the change').toBe(6 * 80)

  // ONLY the drawn-row average changes (visible rows reflow 80 -> 120); the
  // silent rows keep drawing nothing, so their observer has nothing to report.
  for (const source of visible) layout.state.rowHeights.set(source.history_position!, 120)
  await observeLayoutOnce()
  expect(canvas(), 'the canvas is still exactly the real rows: silent rows stay zero, never a full-row estimate').toBe(6 * 120)
  expect(region.scrollTop + region.clientHeight, 'the live end is real content, not blank estimated space').toBeLessThanOrEqual(canvas() + 200)
})
