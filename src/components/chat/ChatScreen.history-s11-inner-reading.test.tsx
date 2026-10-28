// T83 / S11 B98: real list, runtime, paging and sole viewport compensation.
// Browser metrics are independent physical inputs. No part/store/hook seeding.
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
import { deferred, entry, page, pos } from '../../../tests/chat-history-paging/fixtures'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'

const A = 's11-reader-A', B = 's11-reader-B', T = 's11-reader-T', TURN = 's11-reader-turn'
const BODY = 'Lead paragraph.\n\nS11_READING_POINT inside B with a [S11 focus link](https://example.test).\n\nTrailing paragraph.'
const A_FULL = 'Earlier text.' + 'a'.repeat(70_000) + 'S11_A_FULL_END'
const path = `/api/v1/sessions/${MAIN_ID}/messages`
const bucket = () => useChatStore.getState().sessionsById[MAIN_ID]
const user = (rank: number) => entry(rank, { id: `s11-user-${rank}`, role: 'user', content: `Independent question ${rank}` })
const text = (rank: number, id: string, content: string) => entry(rank, { id, role: 'assistant', agent_id: 'mia', turn_id: TURN, content })
const a = () => text(1, A, 'Earlier text.')
const b = () => text(3, B, BODY)
const tool = () => text(2, T, '')
function segmentSources(): Message[] {
  return [a(), { ...tool(), type: 'tool_call', tool_calls: [{ id: 's11-reader-call', tool: 'bash', status: 'success', content_state: 'full', parameters: { command: 'echo earlier' }, result: { text: 'Earlier output.' } }] }, b()]
}
let network: Stage1Network
let layout: ReturnType<typeof installHistoryLayout> | undefined
const releases: (() => void)[] = []
beforeEach(() => { resetStage1App(); network = new Stage1Network() })
afterEach(async () => {
  releases.splice(0).forEach(release => release())
  await stopStage1App(); layout?.restore(); layout = undefined
  expect(network.unexpected, 'network edges are declared').toEqual([])
})
function RealReader() {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(state => state.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root>
    <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}
async function read(sources: Message[], mode: 'latest' | 'older' | 'newer', earlier = false, later = false, limit = Math.max(20, sources.length)) {
  const selected = page(sources, { sessionId: MAIN_ID, earlier, later, rows: sources.length, info: { mode, limit, min_rows: 1, stop_reason: (mode === 'newer' ? later : earlier) ? 'row_target' : 'end' } })
  network.respond('GET', path, () => json(selected))
  await act(async () => {
    const store = useChatStore.getState()
    if (mode === 'latest') await store.loadLatestHistory(MAIN_ID, { limit, min_rows: 1 })
    else if (mode === 'older') await store.loadOlderHistory(MAIN_ID, { limit, min_rows: 1 })
    else await store.loadNewerHistory(MAIN_ID, { limit, min_rows: 1 })
  })
  expect(bucket().history.requests[mode].status, 'fixture: actual read commits').toBe('idle')
}
async function settle() {
  await act(async () => { layout!.flushResize(); fireEvent(window, new Event('resize')) })
  await act(async () => { await new Promise<void>(resolve => requestAnimationFrame(() => resolve())); layout!.flushResize() })
  await act(async () => { await new Promise<void>(resolve => requestAnimationFrame(() => resolve())); layout!.flushResize() })
}
function point(region: HTMLElement) {
  const part = region.querySelector<HTMLElement>(`[data-history-part="${pos(3)}"]`)
  expect(part, 'BLOCKED: source-position DOM part marker not implemented — required by S11 Reading position / T83').not.toBeNull()
  const paragraph = [...part!.querySelectorAll<HTMLElement>('p')].find(node => node.textContent?.startsWith('S11_READING_POINT'))
  expect(paragraph, 'the reading point is an actual rendered inner paragraph, not the outer row').toBeDefined()
  return paragraph!
}
const top = (region: HTMLElement) => point(region).getBoundingClientRect().top - region.getBoundingClientRect().top
async function positionInsideB(region: HTMLElement, direction: 'older' | 'newer') {
  await settle()
  // For virtual mode first reveal B using the physical row offset, not a store
  // rank→virtual-prefix injection. The real virtualizer owns its translation.
  await act(async () => { region.scrollTop = direction === 'newer' ? region.scrollHeight - region.clientHeight : 0; fireEvent.scroll(region); layout!.flushResize() })
  await waitFor(() => expect(region.querySelector(`[data-history-part="${pos(3)}"]`), 'BLOCKED: B part marker not implemented — required by S11/T83').not.toBeNull())
  const marked = point(region)
  const marker = marked.closest<HTMLElement>('[data-history-part]')!
  expect(marked.getBoundingClientRect().top - marker.getBoundingClientRect().top, 'instrument: independent inner geometry sees the second paragraph60px inside B').toBe(60)
  await act(async () => { region.scrollTop += top(region) + 7; fireEvent.scroll(region); layout!.flushResize() })
  await settle()
  expect(top(region), 'instrument: chosen content point lies seven pixels above the viewport top').toBe(-7)
  const link = screen.getByRole('link', { name: 'S11 focus link' })
  await act(async () => { link.focus({ preventScroll: true }); fireEvent.scroll(region) })
  expect(link).toHaveFocus()
  return link
}
async function assertPlace(region: HTMLElement, previous: number, focus: HTMLElement, event: string) {
  await settle()
  expect(Math.abs(top(region) - previous), `B98 ${event}: preserve the source part's inner content point, not just its row top`).toBeLessThanOrEqual(1)
  expect(focus, `B98 ${event}: preserve the same connected focused link`).toHaveFocus()
}
async function refresh(sources: Message[]) {
  network.respond('GET', path, request => {
    const before = new URLSearchParams(request.query).get('before')
    const upper = before ? sources.findIndex(source => source.history_position === before) : sources.length
    expect(upper, 'fixture: real refresh continuation selects a known source').toBeGreaterThan(0)
    const lower = Math.max(0, upper - 100), selected = sources.slice(lower, upper)
    return json(page(selected, { sessionId: MAIN_ID, earlier: lower > 0, later: upper < sources.length, rows: selected.length, info: { mode: 'window_refresh', limit: 100, min_rows: 20, stop_reason: lower ? 'row_target' : 'range_end' } }))
  })
  await act(async () => useChatStore.getState().refreshLoadedHistory(MAIN_ID))
  expect(bucket().history.requests.refresh.status).toBe('idle')
}
async function setup(direction: 'older' | 'newer', virtual: boolean) {
  layout = installHistoryLayout({ virtual, hidden: true, innerParts: true })
  layout.state.partHeights.set(pos(1), 240)
  layout.state.partHeights.set(pos(2), 80)
  layout.state.partHeights.set(pos(3), 1600)
  if (direction === 'older') await read([b(), ...Array.from({ length: 39 }, (_, i) => user(i + 4))], 'latest', true)
  else {
    // A true older-edge eviction establishes hasLater at B. No direct flag
    // seeding:497 older sources +20 =517, evicting the17 groups after B.
    // Source4 is the same later text on both cuts, never a user source whose
    // position gets silently reused for a different record on the newer read.
    const initial = [...segmentSources(), text(4, 's11-reader-C', 'Later segment text.'), ...Array.from({ length: 16 }, (_, i) => user(i + 5))]
    await read(initial, 'latest', true)
    useChatStore.getState().enterHistoryReading(MAIN_ID)
    const padding = Array.from({ length: 497 }, (_, i) => user(i - 497))
    for (let end = padding.length; end > 0; end -= 100) await read(padding.slice(Math.max(0, end - 100), end), 'older', end > 100, true, 100)
    expect(bucket().history.readerPositions).toHaveLength(500)
    expect(bucket().history.newerBoundary).toBe(pos(3))
  }
  useChatStore.getState().enterHistoryReading(MAIN_ID)
  render(<QueryClientProvider client={queryClient}><RealReader /></QueryClientProvider>)
  const region = screen.getByRole('region', { name: 'Conversation history' })
  const focus = await positionInsideB(region, direction)
  return { region, focus }
}

it('T83 instrument control: inner geometry detects movement that an unchanged row top misses', () => {
  layout = installHistoryLayout({ innerParts: true })
  const viewport = document.createElement('div')
  viewport.setAttribute('aria-label', 'Conversation history')
  viewport.innerHTML = `<div data-history-content><div data-history-anchor="fixture-row"><section data-history-part="${pos(1)}"><p>A</p></section><section data-history-part="${pos(3)}"><p>Lead</p><p>S11_READING_POINT</p></section></div></div>`
  document.body.append(viewport)
  try {
    const row = viewport.querySelector<HTMLElement>('[data-history-anchor]')!
    const marked = point(viewport)
    const rowBefore = row.getBoundingClientRect().top, pointBefore = marked.getBoundingClientRect().top
    expect(pointBefore - rowBefore).toBe(140) // A80 + B's second paragraph60.
    layout.state.partHeights.set(pos(1), 240)
    expect(row.getBoundingClientRect().top).toBe(rowBefore)
    expect(marked.getBoundingClientRect().top - pointBefore, 'positive sensitivity: independent A growth moves B inside the same unchanged row').toBe(160)
  } finally { viewport.remove() }
})

for (const virtual of [false, true]) {
  it.each(['older', 'newer'] as const)(`T83 B98: ${virtual ? 'virtual' : 'plain'} %s fold/rekey, pending scroll and delayed layout retain the inner point within1px`, async direction => {
    const { region, focus } = await setup(direction, virtual)
    const incoming = direction === 'older' ? segmentSources().slice(0, 2) : [text(4, 's11-reader-C', 'Later segment text.'), ...Array.from({ length: 19 }, (_, i) => user(i + 5))]
    const held = deferred<Response>()
    const selected = page(incoming, { sessionId: MAIN_ID, rows: incoming.length, earlier: direction === 'newer', later: direction === 'older', info: { mode: direction, limit: 20, min_rows: 1, stop_reason: 'end' } })
    releases.push(() => held.resolve(json(selected)))
    network.respond('GET', path, () => held.promise)
    let pending!: Promise<void>
    await act(async () => { pending = direction === 'older' ? useChatStore.getState().loadOlderHistory(MAIN_ID, { limit: 20, min_rows: 1 }) : useChatStore.getState().loadNewerHistory(MAIN_ID, { limit: 20, min_rows: 1 }) })
    expect(bucket().history.requests[direction].status).toBe('loading')
    await act(async () => { region.scrollTop += 31; fireEvent.scroll(region) })
    const afterOwnScroll = top(region)
    await act(async () => { held.resolve(json(selected)); await pending })
    expect(bucket().historySourceAliases?.[pos(3)]).toBe(A)
    expect(bucket().messagesById[A].historyParts?.slice(0, 3).map(part => part.sourceId)).toEqual([A, T, B])
    await assertPlace(region, afterOwnScroll, focus, 'fold/rekey after user scroll during read')
    const prior = top(region)
    // A real delayed tool measurement changes layout inside the row above B.
    // The browser supplies the growth; the production observer/hook compensates.
    layout!.state.toolHeights.set('s11-reader-call', 240)
    await assertPlace(region, prior, focus, 'delayed tool growth')
    layout!.state.partHeights.set(pos(1), 480)
    await assertPlace(region, prior, focus, 'expanded earlier-part browser layout')
  })
  it.each(['older', 'newer'] as const)(`T83 B98: ${virtual ? 'virtual' : 'plain'} %s saved settlement grows A above the marked B paragraph without moving it`, async direction => {
    const { region, focus } = await setup(direction, virtual)
    if (direction === 'older') await read(segmentSources().slice(0, 2), 'older', false, true)
    await settle()
    const prior = top(region)
    // A's independently supplied browser height changes above B at settlement.
    // Source order is the same literal closed interval used by setup.
    layout!.state.partHeights.set(pos(1), 600)
    const corrected = [{ ...a(), content: 'Corrected earlier text.\n\nAn added saved paragraph.' }, segmentSources()[1], b()]
    const sources = direction === 'older' ? [...corrected, ...Array.from({ length: 39 }, (_, i) => user(i + 4))] : [...Array.from({ length: 497 }, (_, i) => user(i - 497)), ...corrected]
    await refresh(sources)
    expect(bucket().messagesById[A].content).toBe('Corrected earlier text.\n\nAn added saved paragraph.\n\n' + BODY)
    await assertPlace(region, prior, focus, 'saved settlement')
  })
  it.each(['older', 'newer'] as const)(`T83 B98: ${virtual ? 'virtual' : 'plain'} %s actual earlier-part expansion preserves B's inner point within1px`, async direction => {
    const { region } = await setup(direction, virtual)
    if (direction === 'older') await read(segmentSources().slice(0, 2), 'older', false, true)
    const shortenedA = { ...a(), content: A_FULL.slice(0, 65_536), content_original_size_bytes: A_FULL.length }
    const segment = [shortenedA, segmentSources()[1], b()]
    const sources = direction === 'older' ? [...segment, ...Array.from({ length: 39 }, (_, i) => user(i + 4))] : [...Array.from({ length: 497 }, (_, i) => user(i - 497)), ...segment]
    await refresh(sources); await settle()
    const held = deferred<Response>()
    const fullPath = `${path}/${pos(1)}`
    releases.push(() => held.resolve(json({ ...a(), content: A_FULL })))
    network.respond('GET', fullPath, () => held.promise)
    await userEvent.setup().click(await screen.findByRole('button', { name: /^Show the rest/i }))
    expect(network.requests.filter(request => request.path.startsWith(path + '/')).map(request => request.path), 'S9: expansion addresses A, never the combined row slot of another source').toEqual([fullPath])
    const focus = screen.getByRole('link', { name: 'S11 focus link' })
    await act(async () => { focus.focus({ preventScroll: true }); fireEvent.scroll(region) })
    await settle()
    const prior = top(region)
    layout!.state.partHeights.set(pos(1), 800)
    await act(async () => held.resolve(json({ ...a(), content: A_FULL })))
    await waitFor(() => expect(document.body.textContent).toContain('S11_A_FULL_END'))
    await assertPlace(region, prior, focus, 'actual full-content expansion above B')
    expect(bucket().messagesById[A].content).toBe(shortenedA.content + '\n\n' + BODY)
  })
}
