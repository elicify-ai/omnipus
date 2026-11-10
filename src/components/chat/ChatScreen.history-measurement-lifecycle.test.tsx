// Gate I2: D5, D-G1b/c/d, B07/B39. Expected arithmetic is in PLAN.md.
// Probe cases test the REAL viewport hook's DOM-input contract. The final
// case renders the REAL production message list and TanStack virtualizer.
import { useRef } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import type { Message } from '@/lib/api/generated/openapi-types'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { historyAttachSizing } from '@/lib/chat-history-sizing'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { Button } from '@/components/ui/button'
import { VirtualizedMessageList } from './ChatMessageList'
import { useHistoryViewport } from './useHistoryViewport'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { resetStage1App, Stage1Network, stopStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import { deferred, entry, expectValidPage, page, pos } from '../../../tests/chat-history-paging/fixtures'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'

let network: Stage1Network
let layout: ReturnType<typeof installHistoryLayout>
const path = (sid = MAIN_ID) => `/api/v1/sessions/${sid}/messages`
const sources = (first = 1, count = 100) => Array.from({ length: count }, (_, index) => entry(first + index))

beforeEach(() => {
  resetStage1App()
  network = new Stage1Network()
  layout = installHistoryLayout({ virtual: true, hidden: true })
  layout.state.rowHeight = 40
})
afterEach(async () => {
  await stopStage1App()
  layout.restore()
  expect(network.unexpected, 'all network edges are declared').toStrictEqual([])
})

async function loadRows(rows: Message[], { sid = MAIN_ID, earlier = false, drawnRows = rows.length, historical = true } = {}) {
  const limit = Math.max(20, Math.min(100, drawnRows))
  const response = page(rows, { sessionId: sid, earlier, rows: drawnRows, info: { limit, min_rows: Math.min(20, limit) } })
  expectValidPage(response, rows.map(row => row.history_position!), drawnRows)
  network.respond('GET', path(sid), () => json(response))
  await act(async () => {
    await useChatStore.getState().loadLatestHistory(sid, { limit, min_rows: Math.min(20, limit) })
    if (historical) useChatStore.getState().enterHistoryReading(sid)
  })
  return response
}

// not-wire-format: DOM-input fixture for the viewport hook, not an alternate
// message renderer or virtualizer. Internal isStreaming is an input argument;
// no production store readiness/protection/measurement state is seeded.
function MeasurementProbe({ mounted, unstable = [], hiddenRows = [], ariaHiddenRows = [], liveAnchors = [], controls = false }: {
  mounted?: number[]; unstable?: number[]; hiddenRows?: number[]; ariaHiddenRows?: number[]; liveAnchors?: number[]; controls?: boolean
}) {
  const ref = useRef<HTMLDivElement>(null)
  const stored = useChatStore(state => state.messages)
  const messages = stored.map((message, index) => unstable.includes(index) ? { ...message, isStreaming: true } : message)
  const viewport = useHistoryViewport(ref, messages)
  return <div ref={ref} role="region" aria-label="Conversation history" onScroll={viewport.onScroll}>
    <output data-testid="measured-average">{viewport.average}</output>
    <div data-history-content>
      {controls && <><p role="status">Loading earlier messages…</p><p>History hint</p><Button>Retry</Button></>}
      {messages.map((message, index) => mounted && !mounted.includes(index) ? null : <div
        key={message.id} hidden={hiddenRows.includes(index)} aria-hidden={ariaHiddenRows.includes(index) || undefined}
        data-history-position={liveAnchors.includes(index) ? undefined : message.historyPosition}
        data-history-anchor={liveAnchors.includes(index) ? `live:${message.id}` : message.historyPosition}
      >{message.content}{message.tool_calls?.map(call => <div key={call.id} data-history-tool-row={call.id}>{call.tool}</div>)}</div>)}
    </div>
  </div>
}

function RealVirtualRows() {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(state => state.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root>
    <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}

async function resize() {
  await act(async () => {
    fireEvent(window, new Event('resize'))
    layout.flushResize()
  })
}
function expectAverage(value: number, label: string) {
  expect(screen.getByTestId('measured-average'), label).toHaveTextContent(new RegExp(`^${value}$`))
}

describe('I2 actual viewport measurement lifecycle — D5/D-G1c', () => {
  it('major width change drops old unmounted samples before measuring the new width', async () => {
    await loadRows(sources())
    const view = render(<MeasurementProbe />)
    expectAverage(40, 'positive control:100 stable40px samples in the old width')
    layout.state.width = 1200
    layout.state.rowHeight = 120
    view.rerender(<MeasurementProbe mounted={[0]} />)
    await resize()
    // Only one row is now mounted. The99 old-width samples must NOT dilute120.
    expectAverage(120, 'D5 width generation resets old unmounted samples, not only observed rows')
    expect(historyAttachSizing(MAIN_ID), 'ceil(2400/120)=20; ceil(1600/120)=14').toStrictEqual({ history_limit: 20, history_min_rows: 14 })
  })

  it('Verbose presentation change drops prior unmounted samples, even at the same width', async () => {
    await loadRows(sources())
    const view = render(<MeasurementProbe />)
    expectAverage(40, 'positive control: stable old-presentation samples exist')
    const previous = useChatPreferencesStore.getState().verboseChatEnabled
    layout.state.rowHeight = 160
    view.rerender(<MeasurementProbe mounted={[0]} />)
    await act(async () => useChatPreferencesStore.getState().setVerboseChatEnabled(!previous))
    await resize()
    expectAverage(160, 'D5 presentation generation resets all99 unmounted old samples')
    expect(historyAttachSizing(MAIN_ID), '2400/160=15 clamped20;1600/160=10').toStrictEqual({ history_limit: 20, history_min_rows: 10 })
  })

  it('session change forgets old attach geometry and starts80 when no new stable row is measured', async () => {
    await loadRows(sources())
    render(<MeasurementProbe />)
    expectAverage(40, 'positive control: outgoing session has measured geometry')
    expect(historyAttachSizing(MAIN_ID), 'known outgoing H800/A40 geometry').toStrictEqual({ history_limit: 60, history_min_rows: 40 })
    const next = 'I2-next-session'
    await loadRows(sources(1001), { sid: next })
    layout.state.rowHeight = 0
    await act(async () => useSessionStore.getState().setActiveSession(next, 'mia'))
    await resize()
    expectAverage(80, 'D5 new session with no positive measurements uses the seed, not outgoing40')
    expect(historyAttachSizing(MAIN_ID), 'leaving the session clears its ephemeral attach geometry').toStrictEqual({})
    expect(historyAttachSizing(next), 'new H800/A80 uses opening30/floor20').toStrictEqual({ history_limit: 30, history_min_rows: 20 })
  })

  it('excludes zero/negative/nonfinite, unstable, hidden, aria-hidden, live anchors and loader/hint/Retry nodes', async () => {
    await loadRows(sources(1, 9))
    layout.state.rowHeights = new Map([
      [pos(1), 40], [pos(2), 0], [pos(3), -1], [pos(4), Number.NaN],
      [pos(5), Number.POSITIVE_INFINITY], [pos(6), 400], [pos(7), 400], [pos(8), 400], [pos(9), 400],
    ])
    render(<MeasurementProbe unstable={[5]} hiddenRows={[6]} ariaHiddenRows={[7]} liveAnchors={[8]} controls />)
    expectAverage(40, 'only the one stable positive visible recorded row contributes')
    expect(historyAttachSizing(MAIN_ID), 'excluded objects do not bias actual request geometry').toStrictEqual({ history_limit: 60, history_min_rows: 40 })
  })

  it('measures actual tool children once, not wrapper plus children', async () => {
    const tools = [entry(1, { role: 'assistant', content: '', tool_calls: [
      { id: 'child40', tool: 'bash', content_state: 'full', status: 'success', parameters: { command: 'printf first' } },
      { id: 'child80', tool: 'bash', content_state: 'full', status: 'success', parameters: { command: 'printf second' } },
    ] })]
    await loadRows(tools, { drawnRows: 2 })
    layout.state.rowHeights.set(pos(1), 120)
    layout.state.toolHeights.set('child40', 40)
    layout.state.toolHeights.set('child80', 80)
    render(<MeasurementProbe />)
    expectAverage(60, 'D5(40+80)/2, never(120+40+80)/3')
    expect(historyAttachSizing(MAIN_ID), '2400/60=40;ceil(1600/60)=27').toStrictEqual({ history_limit: 40, history_min_rows: 27 })
  })

  it.each(['hidden', 'zero-height'] as const)('%s suppresses real automatic HTTP reads; a visible positive viewport resumes them', async mode => {
    layout.state.hidden = mode === 'hidden'
    layout.state.height = mode === 'zero-height' ? 0 : 800
    await loadRows(sources(61, 40), { earlier: true, historical: false })
    const held = deferred<Response>()
    network.respond('GET', path(), () => held.promise)
    render(<MeasurementProbe />)
    const region = screen.getByRole('region', { name: 'Conversation history' })
    await resize()
    await act(async () => { fireEvent.scroll(region); fireEvent.scroll(region) })
    expect(network.requests.filter(read => read.path === path()), 'D5 hidden/H0 cannot issue an opening or scroll-prefetch read').toHaveLength(1)
    await act(async () => {
      layout.state.height = 800
      layout.visibility(false)
      fireEvent.scroll(region)
    })
    await waitFor(() => expect(network.requests.filter(read => read.path === path()), 'positive instrument: visible viewport actually asks for missing3H coverage').toHaveLength(2))
    const reads = network.requests.filter(read => read.path === path())
    expect(Object.fromEntries(new URLSearchParams(reads[1].query)), '40 initial40px rows cover1600; opening needs800 more, so20/20').toStrictEqual({ before: pos(61), limit: '20', min_rows: '20' })
    const response = page(sources(41, 20), { sessionId: MAIN_ID, later: true, info: { mode: 'older', limit: 20, min_rows: 20 } })
    expectValidPage(response, Array.from({ length: 20 }, (_, index) => pos(41 + index)), 20)
    await act(async () => held.resolve(json(response)))
    await waitFor(() => expect(useChatStore.getState().history.readerPositions, 'real adapter commits the resumed contiguous page').toStrictEqual(Array.from({ length: 60 }, (_, index) => pos(41 + index))))
    expect(network.requests.filter(read => read.path === path()), 'one-screen remainder completes3H with no concurrent duplicate').toHaveLength(2)
  })
})

describe('I2 real virtualizer estimated-prefix invalidation — D5/D-G1d', () => {
  it('remeasures after an80→40 average change and gives unmounted row60 its2400px prefix', async () => {
    layout.state.rowHeight = 80
    await loadRows(sources(), { earlier: true })
    render(<QueryClientProvider client={queryClient}><RealVirtualRows /></QueryClientProvider>)
    await resize()
    const region = screen.getByRole('region', { name: 'Conversation history' })
    const content = region.querySelector<HTMLElement>('[data-history-content]')!
    const mounted = region.querySelectorAll('[data-index]')
    expect(mounted.length, 'positive instrument: real virtual rows are mounted').toBeGreaterThan(0)
    expect(mounted.length, 'positive instrument: some source rows really are unmounted estimates').toBeLessThan(100)
    expect(content.style.height, '100 ordinary rows initially measured/estimated80 =>8000').toBe('8000px')
    expect(historyAttachSizing(MAIN_ID), 'positive control: initial stable measurements really are80').toStrictEqual({ history_limit: 30, history_min_rows: 20 })

    layout.state.rowHeight = 40
    await resize()
    await resize()
    await waitFor(() => expect(historyAttachSizing(MAIN_ID), 'same generation now observes40, after—not instead of—the initial80 measurement').toStrictEqual({ history_limit: 60, history_min_rows: 40 }))
    expect(content.style.height, 'D5 ALL unmeasured prefixes use current40, not stale80;100*40=4000').toBe('4000px')

    // The control prefix is an independently measured physical DOM input.
    // The UNMOUNTED source prefix2400 comes from60*40, not product output.
    const physicalControls = content.getBoundingClientRect().top + region.scrollTop
    await act(async () => {
      region.scrollTop = physicalControls + 2400
      fireEvent.scroll(region)
    })
    const row60 = region.querySelector<HTMLElement>('[data-index="60"]')
    expect(row60, 'correct real prefix brings index60 into the mounted range').not.toBeNull()
    expect(row60!.getAttribute('data-history-position'), 'real virtualizer index60 names the61st source').toBe(pos(61))
    expect(row60!.getBoundingClientRect().top - region.getBoundingClientRect().top, 'real translateY plus actual row layout puts row60 exactly at the viewport top').toBe(0)
    expect(region.querySelectorAll('[data-index]').length, 'scrolling does not fall back to rendering all100 rows').toBeLessThan(100)
    expect(network.requests.filter(read => read.path === path()), 'hidden measurement fixture must not hide a competing prefetch').toHaveLength(1)
  })
})
