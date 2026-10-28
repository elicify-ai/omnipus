// D8: each DISTINCT request attempt entering loading produces exactly ONE
// polite status. Reading-mode changes do not create another request attempt.
// Real list/boundaries/store/history action/socket; DOM lifecycle is observed,
// never mocked. Browser geometry and external HTTP/socket are fixture edges.
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
import { Stage1Network, resetStage1App, stopStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import { entry, expectValidPage, page, STAMP, UNAVAILABLE } from '../../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../../tests/chat-history-paging/transport'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'

let network: Stage1Network
let layout: ReturnType<typeof installHistoryLayout>
let wire: ReturnType<typeof historyTransport> | undefined
let observer: MutationObserver | undefined
beforeEach(() => {
  resetStage1App()
  network = new Stage1Network()
  layout = installHistoryLayout({ virtual: true, hidden: true })
})
afterEach(async () => {
  observer?.disconnect(); observer = undefined
  wire?.connection.disconnect(); wire = undefined
  await stopStage1App()
  layout.restore()
  expect(network.unexpected, 'all physical HTTP reads remain explicitly declared').toStrictEqual([])
})
function RealRows() {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(state => state.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root>
    <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}
async function layoutFrame() { await act(async () => { fireEvent(window, new Event('resize')); layout.flushResize() }) }

it('R9: moving the reader during one latest-recovery attempt inserts its polite loading announcement only once', async () => {
  const sources = Array.from({ length: 40 }, (_, i) => entry(i + 1, { id: `r9-source-${i + 1}`, role: 'assistant', content: `Retained source ${i + 1}` }))
  const selected = page(sources, { sessionId: MAIN_ID, info: { limit: 40, min_rows: 20 } })
  expectValidPage(selected, sources.map(row => row.history_position!), 40)
  network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(selected))
  await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID, { limit: 40, min_rows: 20 }) })
  render(<QueryClientProvider client={queryClient}><RealRows /></QueryClientProvider>)
  await layoutFrame()
  const region = screen.getByRole('region', { name: 'Conversation history' })
  for (let pass = 0; pass < 2; pass++) {
    await act(async () => { region.scrollTop = region.scrollHeight; fireEvent.scroll(region) })
    await layoutFrame()
  }
  await act(async () => { layout.visibility(false) })
  await layoutFrame()
  if (useChatStore.getState().history.viewMode !== 'latest') {
    // Initial deterministic layout may move this reader. Establish R9's
    // follow-latest INPUT through the real user action, never a seeded flag.
    await userEvent.click(screen.getByRole('button', { name: 'Return to latest' }))
    await waitFor(() => expect(useChatStore.getState().history.requests.latest.status).toBe('idle'))
    await layoutFrame()
  }
  expect(useChatStore.getState().history.viewMode, 'instrument: reader really follows latest before starting recovery').toBe('latest')
  expect(useChatStore.getState().messages.map(row => row.id)).toStrictEqual(sources.map(row => row.id))

  wire = historyTransport()
  wire.deliver({ type: 'session_state', session_id: MAIN_ID, user_id: 'r9-reader', pending_approvals: [], emitted_at: STAMP,
    history_state: { status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE } })
  expect(screen.getByText(UNAVAILABLE), 'actual unavailable-history recovery surface exists').toBeVisible()
  const announcements: HTMLElement[] = []
  const loadingText = 'Loading latest messages…'
  observer = new MutationObserver(records => {
    const insertedThisBatch = new Set<HTMLElement>()
    for (const record of records) for (const node of record.addedNodes) {
      if (!(node instanceof HTMLElement)) continue
      if (node.getAttribute('role') === 'status' && node.textContent === loadingText) insertedThisBatch.add(node)
      for (const status of node.querySelectorAll<HTMLElement>('[role="status"]')) {
        if (status.textContent === loadingText) insertedThisBatch.add(status)
      }
    }
    // Ancestor records in one batch may expose the same insertion twice;
    // a later remount/re-insertion is still a NEW announcement, even same text.
    announcements.push(...insertedThisBatch)
  })
  observer.observe(region, { childList: true, subtree: true })
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  await waitFor(() => expect(announcements, 'positive instrument: one genuine loading-status insertion occurred').toHaveLength(1))
  const originalStatus = announcements[0]
  expect(originalStatus).toBeVisible()
  const request = structuredClone(useChatStore.getState().history.requests.latest)
  expect(request.status).toBe('loading')
  const attaches = () => wire!.socket.sent.filter(frame => frame.type === 'attach_session')
  expect(attaches(), 'exactly one real recovery request starts').toHaveLength(1)
  expect(attaches()[0]).toMatchObject({ type: 'attach_session', session_id: MAIN_ID })
  const retained = useChatStore.getState().messages.map(row => row.id)

  await act(async () => {
    fireEvent.wheel(region, { deltaY: -400 })
    region.scrollTop = Math.max(0, region.scrollTop - 400)
    fireEvent.scroll(region)
  })
  await waitFor(() => expect(useChatStore.getState().history.viewMode, 'actual viewport movement enters historical reading').toBe('historical'))
  await layoutFrame()
  expect(useChatStore.getState().history.requests.latest, 'the SAME request attempt remains pending across the mode change').toStrictEqual(request)
  expect(attaches(), 'mode change is not a new recovery request').toHaveLength(1)
  expect(useChatStore.getState().messages.map(row => row.id)).toStrictEqual(retained)
  const visibleLoading = [...region.querySelectorAll<HTMLElement>('[role="status"]')].filter(node => node.textContent === loadingText)
  expect(visibleLoading, 'there is one visible status, but this alone cannot detect a remount').toHaveLength(1)
  expect(announcements, 'D8: one unchanged attempt must not insert a second polite loading status when its layout placement changes').toHaveLength(1)
  expect(visibleLoading[0], 'the existing announcement node is preserved, not replaced by identical text').toBe(originalStatus)
})
