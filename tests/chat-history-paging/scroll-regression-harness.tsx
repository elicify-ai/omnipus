// Test-only browser/process boundary for R10-R12. Actual renderer, viewport,
// history actions, adapter and stores are never mocked.
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { VirtualizedMessageList } from '@/components/chat/ChatMessageList'
import { Stage1Network, resetStage1App, stopStage1App } from '../l1-room-ui/stage1-harness'
import { MAIN_ID, json } from '../l1-room-ui/harness'
import { entry, page, pos, expectValidPage } from './fixtures'
import { installHistoryLayout } from './measurement-layout'

export { MAIN_ID, json, entry, page, pos, expectValidPage }
function RealRows() {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(state => state.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root>
    <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}
export function resetScrollCase() {
  resetStage1App()
  const network = new Stage1Network()
  const layout = installHistoryLayout({ virtual: false, hidden: true })
  return { network, layout }
}
export async function stopScrollCase(layout: ReturnType<typeof installHistoryLayout>) {
  await stopStage1App()
  layout.restore()
}
export async function settleScrollLayout() { await act(async () => { fireEvent(window, new Event('resize')) }) }
export async function mountRetained(count = 40, earlier = false) {
  const sources = Array.from({ length: count }, (_, i) => entry(i + 21, { id: `scroll-source-${i + 21}`, role: 'assistant', content: `Retained synthetic source ${i + 21}` }))
  const selected = page(sources, { sessionId: MAIN_ID, earlier, info: { limit: 40, min_rows: 20 } })
  expectValidPage(selected, sources.map(source => source.history_position!), count)
  return { sources, selected }
}
export async function loadAndRender(network: Stage1Network, selected: ReturnType<typeof page>) {
  network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(selected))
  await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID, { limit: 40, min_rows: 20 }) })
  render(<QueryClientProvider client={queryClient}><RealRows /></QueryClientProvider>)
  await settleScrollLayout()
  return screen.getByRole('region', { name: 'Conversation history' })
}
export async function establishLatest(region: HTMLElement, layout: ReturnType<typeof installHistoryLayout>) {
  await act(async () => { region.scrollTop = region.scrollHeight; fireEvent.scroll(region) })
  await act(async () => { layout.visibility(false) })
  await settleScrollLayout()
  if (useChatStore.getState().history.viewMode !== 'latest') {
    await userEvent.click(screen.getByRole('button', { name: 'Return to latest' }))
    await waitFor(() => { if (useChatStore.getState().history.requests.latest.status !== 'idle') throw new Error('real latest read not finished') })
    await settleScrollLayout()
  }
}
