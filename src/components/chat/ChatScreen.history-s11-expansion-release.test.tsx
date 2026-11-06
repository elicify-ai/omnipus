// T81 / B96 / S9: release of the key-holding part during a real D2 read.
// All500 sources are loaded through public actions before the real list mounts.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import type { Message } from '@/lib/api/generated/openapi-types'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { VirtualizedMessageList } from './ChatMessageList'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { resetStage1App, Stage1Network, stopStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import { deferred, entry, expectValidPage, page, pos } from '../../../tests/chat-history-paging/fixtures'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'

const A = 's11-expansion-A', B = 's11-expansion-B', T = 's11-expansion-T'
const FULL = 'S11_B_START' + 'b'.repeat(70_000) + 'S11_B_FULL_END'
const PREVIEW = FULL.slice(0, 65_536)
const path = `/api/v1/sessions/${MAIN_ID}/messages`
const fullPath = `${path}/${pos(3)}`
const bucket = () => useChatStore.getState().sessionsById[MAIN_ID]
let network: Stage1Network
let layout: ReturnType<typeof installHistoryLayout>
const releases: (() => void)[] = []
beforeEach(() => {
  resetStage1App(); network = new Stage1Network()
  layout = installHistoryLayout({ virtual: true, hidden: true, innerParts: true })
  layout.state.partHeights.set(pos(1), 1600)
  layout.state.partHeights.set(pos(2), 0)
  layout.state.partHeights.set(pos(3), 80)
})
afterEach(async () => {
  releases.splice(0).forEach(release => release())
  await stopStage1App(); layout.restore()
  expect(network.unexpected).toEqual([])
})
function Reader() {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(state => state.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root>
    <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}
async function read(sources: Message[], mode: 'latest' | 'older' | 'newer', earlier: boolean, later: boolean) {
  const limit = Math.max(20, sources.length)
  // Each selected source here is one narration row or one tool-only call.
  const selected = page(sources, { sessionId: MAIN_ID, earlier, later, rows: sources.length, info: { mode, limit, min_rows: 1, stop_reason: (mode === 'newer' ? later : earlier) ? 'row_target' : 'end' } })
  expectValidPage(selected, sources.map(source => source.history_position!), sources.length)
  const reads = () => network.requests.filter(request => request.method === 'GET' && request.path === path).length
  const before = reads()
  network.respond('GET', path, () => json(selected))
  await act(async () => {
    const store = useChatStore.getState()
    if (mode === 'latest') await store.loadLatestHistory(MAIN_ID, { limit, min_rows: 1 })
    else if (mode === 'older') await store.loadOlderHistory(MAIN_ID, { limit, min_rows: 1 })
    else await store.loadNewerHistory(MAIN_ID, { limit, min_rows: 1 })
  })
  expect(reads(), 'instrument: exactly one real page read, not a disabled public action').toBe(before + 1)
  expect(bucket().history.requests[mode].status).toBe('idle')
}
it('T81 B96: B full read stays addressed to B through A eviction and an atomic A→B rekey', async () => {
  const savedB = entry(3, { id: B, role: 'assistant', agent_id: 'mia', turn_id: 's11-expand-turn', content: PREVIEW, content_original_size_bytes: FULL.length })
  const sources = Array.from({ length: 500 }, (_, i) => entry(i + 1, { role: 'user' }))
  sources.splice(0, 3,
    entry(1, { id: A, role: 'assistant', agent_id: 'mia', turn_id: 's11-expand-turn', content: 'First.' }),
    entry(2, { id: T, type: 'tool_call', role: 'assistant', agent_id: 'mia', turn_id: 's11-expand-turn', content: '', tool_calls: [{ id: 's11-expansion-call', tool: 'bash', status: 'success', content_state: 'full', parameters: {}, result: { text: 'done' } }] }), savedB)
  await read([...sources.slice(420), entry(501, { role: 'user' })], 'latest', true, false)
  useChatStore.getState().enterHistoryReading(MAIN_ID)
  for (let end = 420; end > 0; end -= 100) await read(sources.slice(Math.max(0, end - 100), end), 'older', end > 100, true)
  expect(bucket().history.readerPositions).toEqual(sources.map(source => source.history_position))
  expect(bucket().history.hasLater, 'instrument: real eviction established an enabled newer read, not a seeded flag').toBe(true)
  expect([pos(1), pos(2), pos(3)].map(position => bucket().historySourceAliases?.[position]), 'instrument: S11 folded before beginning the independent D2 operation').toEqual([A, A, A])
  const held = deferred<Response>()
  releases.push(() => held.resolve(json({ ...savedB, content: FULL, content_original_size_bytes: undefined })))
  network.respond('GET', fullPath, () => held.promise)
  render(<QueryClientProvider client={queryClient}><Reader /></QueryClientProvider>)
  await act(async () => { layout.flushResize(); window.dispatchEvent(new Event('resize')) })
  const region = screen.getByRole('region', { name: 'Conversation history' })
  const partB = region.querySelector<HTMLElement>(`[data-history-part="${pos(3)}"]`)
  expect(partB, 'BLOCKED: own-position rendered B marker not implemented — required by S11/S9').not.toBeNull()
  await act(async () => { region.scrollTop += partB!.getBoundingClientRect().top - region.getBoundingClientRect().top; fireEvent.scroll(region); layout.flushResize() })
  const user = userEvent.setup()
  const control = await screen.findByRole('button', { name: /^Show the rest/i })
  await user.click(control)
  const fullReads = () => network.requests.filter(request => request.path.startsWith(path + '/')).map(request => request.path)
  expect(fullReads()).toEqual([fullPath])
  expect(document.body.textContent).not.toContain('S11_B_FULL_END')
  // The reading point is B; A is entirely above the viewport. Release focus
  // to the real history region before trimming, not to an offscreen A control.
  await act(async () => { region.focus({ preventScroll: true }); fireEvent.scroll(region) })
  expect(region).toHaveFocus()
  await read([entry(501, { role: 'user' })], 'newer', true, false)
  expect(bucket().history.readerPositions).toHaveLength(500)
  expect(bucket().historySourceAliases?.[pos(1)]).toBeUndefined()
  expect([pos(2), pos(3)].map(position => bucket().historySourceAliases?.[position])).toEqual([B, B])
  expect(bucket().messagesById[A]).toBeUndefined()
  expect(fullReads(), 'A eviction does not deliver B or change B generation; no row-slot/leading-tool read replaces it').toEqual([fullPath])
  await act(async () => held.resolve(json({ ...savedB, content: FULL, content_original_size_bytes: undefined })))
  await waitFor(() => expect(document.body.textContent).toContain('S11_B_FULL_END'))
  expect(document.body.textContent).toContain(FULL)
  expect(bucket().messagesById[B].content, 'full body is a render overlay, not another chat-store body owner').toBe(PREVIEW)
  expect(JSON.stringify(bucket().messagesById[B])).not.toContain('S11_B_FULL_END')
  expect(fullReads()).toEqual([fullPath])
})
