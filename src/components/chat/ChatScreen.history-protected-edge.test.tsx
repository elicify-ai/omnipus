// Gate I1 + silent-failure F6: spec D8/D9 and the explicit visible-deferral
// addition. Real production list/virtualizer, page owner, merger and viewport
// hook; network/browser edges only. Populate the cap BEFORE mounting the list,
// rather than repeatedly rendering500 full-app rows during setup.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
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
import { installProtectedEdgeLayout } from '../../../tests/chat-history-paging/protected-edge-layout'

type Direction = 'older' | 'newer' // not-wire-format: test traversal input
let network: Stage1Network
let layout: ReturnType<typeof installProtectedEdgeLayout>
const path = `/api/v1/sessions/${MAIN_ID}/messages`
const bucket = () => useChatStore.getState().sessionsById[MAIN_ID]
const positions = (first: number, count = 500) => Array.from({ length: count }, (_, index) => pos(first + index))
const label = (direction: Direction) => direction === 'older' ? /^Show earlier messages$/u : /^Show newer messages$/u

function rows(first: number, count: number, composite?: number, toolCount = 1): Message[] {
  return Array.from({ length: count }, (_, index) => {
    const rank = first + index
    return rank !== composite ? entry(rank) : entry(rank, { role: 'assistant', tool_calls: Array.from({ length: toolCount }, (_, tool) => ({
      id: `settled-${rank}${tool ? `-${tool}` : ''}`, tool: 'bash', content_state: 'full', status: 'success',
      parameters: { command: `printf edge-${rank}-${tool}` }, result: { content: `Saved tool result ${rank}-${tool}` },
    })) })
  })
}
function RealReader() {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(state => state.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root>
    <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}
const historyRequests = () => network.requests.filter(read => read.path === path)

beforeEach(() => {
  resetStage1App()
  network = new Stage1Network()
  layout = installProtectedEdgeLayout()
})

async function readRealPage(direction: Direction | 'latest', sources: Message[], { earlier = true, later = false, limit = sources.length } = {}) {
  const drawnRows = sources.reduce((sum, source) => sum + 1 + (source.tool_calls?.length ?? 0), 0)
  const response = page(sources, { sessionId: MAIN_ID, earlier, later, rows: drawnRows, info: { mode: direction, limit, min_rows: 20 } })
  expectValidPage(response, sources.map(source => source.history_position!), drawnRows)
  network.respond('GET', path, () => json(response))
  await act(async () => {
    const store = useChatStore.getState()
    if (direction === 'latest') await store.loadLatestHistory(MAIN_ID, { limit, min_rows: 20 })
    else if (direction === 'older') await store.loadOlderHistory(MAIN_ID, { limit, min_rows: 20 })
    else await store.loadNewerHistory(MAIN_ID, { limit, min_rows: 20 })
  })
}

async function reachRealCap(direction: Direction) {
  // Five legal92-source pages + initial40 =500. No oversized fixture or direct
  // bucket/flag seeding. Actual public REST owners run before the UI mounts.
  const latestFirst = direction === 'older' ? 561 : 581
  await readRealPage('latest', rows(latestFirst, 40, direction === 'older' ? 600 : undefined), { limit: direction === 'older' ? 41 : 40 })
  await act(async () => useChatStore.getState().enterHistoryReading(MAIN_ID))
  for (let upper = latestFirst - 1; upper > latestFirst - 461; upper -= 92) {
    await readRealPage('older', rows(upper - 91, 92), { limit: 92 })
  }
  if (direction === 'newer') {
    // Real safe eviction establishes hasLater;101 is the true old end. Its
    // narration+tool is atomic:19 ordinary rows +2-row source =21 at target20.
    await readRealPage('older', rows(101, 20, 101), { earlier: false, later: true, limit: 20 })
  }
  expect(bucket().history.readerPositions, 'D9 genuine public paging reaches exactly101..600 at500 sources').toStrictEqual(positions(101))
  render(<QueryClientProvider client={queryClient}><RealReader /></QueryClientProvider>)
  await act(async () => { fireEvent(window, new Event('resize')); layout.flushResize() })
  const region = screen.getByRole('region', { name: 'Conversation history' })
  const mounted = region.querySelectorAll('[data-history-position]').length
  expect(mounted, 'positive control: production virtualizer mounts real owning rows').toBeGreaterThan(0)
  expect(mounted, 'setup is not500 heavy plain DOM rows or an injected full range').toBeLessThan(500)
  return region
}

function rowAt(region: HTMLElement, rank: number) {
  const row = region.querySelector<HTMLElement>(`[data-history-position="${pos(rank)}"]`)
  expect(row, `fixture instrument: source${rank} is really mounted`).not.toBeNull()
  return row!
}
function anchorAt(region: HTMLElement) {
  const top = region.getBoundingClientRect().top
  const row = [...region.querySelectorAll<HTMLElement>('[data-history-position]')].find(element => element.getBoundingClientRect().bottom > top)
  if (!row) throw new Error('Layout instrument: no positive visible history anchor')
  return { position: row.dataset.historyPosition, screenTop: row.getBoundingClientRect().top - top }
}
async function moveTo(region: HTMLElement, rank: number, direction: Direction) {
  const content = region.querySelector<HTMLElement>('[data-history-content]')!
  const controlPrefix = content.getBoundingClientRect().top - region.getBoundingClientRect().top + region.scrollTop
  // Independent physical fixture offsets:80px narration, plus an extra80px
  // tool only in source101 of the newer dataset. Never inject virtual prefixes.
  const sourceOffset = (rank - 101) * 80 + (direction === 'newer' && rank > 101 ? 80 : 0)
  await act(async () => {
    region.scrollTop = controlPrefix + sourceOffset + 7
    fireEvent.scroll(region)
    layout.flushResize()
  })
  expect(rowAt(region, rank), 'physical scroll exposes the independently chosen source').toBeInTheDocument()
}
function boundaries() {
  const history = bucket().history
  return { older: history.olderBoundary, newer: history.newerBoundary, hasEarlier: history.hasEarlier, hasLater: history.hasLater, range: history.readerRange }
}
function notices(region: HTMLElement) {
  return [...within(region).queryAllByRole('status'), ...within(region).queryAllByRole('alert')]
    .filter(element => element.textContent?.trim() && !/Loading .*messages/u.test(element.textContent))
}

async function expectCommitted(region: HTMLElement, direction: Direction, heldAnchor: ReturnType<typeof anchorAt>) {
  // Deliver the browser observer settlement explicitly: a jsdom observer double
  // does not automatically emit initial notifications for newly mounted rows.
  await act(async () => layout.flushResize())
  await act(async () => layout.flushResize())
  const first = direction === 'older' ? 100 : 102
  expect(bucket().history.readerPositions, 'D9 commit is the exact contiguous bounded500-source window').toStrictEqual(positions(first))
  expect(boundaries()).toMatchObject({ older: pos(first), newer: pos(first + 499), hasEarlier: true, hasLater: direction === 'older' })
  // Keep this exact assertion failing while allowing independent whole-group
  // cleanup/focus postconditions to execute in the same RED case.
  expect.soft(anchorAt(region), 'D8 signed compensation preserves the safe anchor and its intra-row screen offset').toStrictEqual(heldAnchor)
  const evicted = direction === 'older' ? 600 : 101
  expect(region.querySelector(`[data-history-position="${pos(evicted)}"]`), 'whole narration/tool owning source disappears from the reader, not one child').toBeNull()
  expect(region.querySelector(`[data-history-tool-row="settled-${evicted}"]`), 'eviction cannot strand a tool child').toBeNull()
  expect(bucket().historySourceAliases?.[pos(evicted)], 'reader source alias is released with its whole group').toBeUndefined()
  // The legitimate100-group latest-tail cache may still retain recent data;
  // absence from that separate cache is deliberately NOT an eviction oracle.
}

afterEach(async () => {
  await stopStage1App()
  layout.restore()
  vi.restoreAllMocks()
  expect(network.unexpected, 'all network edges remain declared').toStrictEqual([])
})

describe('I1 protected reader edges and F6 explicit deferral — D8/D9', () => {
  it.each([
    ['older', 'anchor-and-focus'], ['newer', 'anchor-and-focus'],
    ['older', 'offscreen-focus'], ['newer', 'offscreen-focus'],
  ] as const)('%s page held across %s protection never silently succeeds; safe retry evicts only whole groups', async (direction, protection) => {
    const user = userEvent.setup()
    const region = await reachRealCap(direction)
    // Five overscan rows keep the offscreen terminal tool mounted while the
    // visible anchor/two-screen guard stay outside the ONE evicted source.
    const safeRank = direction === 'older' ? 595 : 106
    await moveTo(region, safeRank, direction)
    const held = deferred<Response>()
    const readsBefore = historyRequests().length
    network.respond('GET', path, () => held.promise)
    const beforeNoticeText = new Set(notices(region).map(element => element.textContent))
    await user.click(within(region).getByRole('button', { name: label(direction) }))
    expect(historyRequests(), 'real UI request is held, not a simulated merge call').toHaveLength(readsBefore + 1)
    expect(bucket().history.requests[direction].status).toBe('loading')

    const edge = direction === 'older' ? 600 : 101
    if (protection === 'anchor-and-focus') {
      await act(async () => {
        region.scrollTop = direction === 'older' ? region.scrollHeight - region.clientHeight : 0
        fireEvent.scroll(region)
        layout.flushResize()
      })
    }
    const focusedGroup = rowAt(region, edge)
    const tool = focusedGroup.querySelector<HTMLElement>('[data-history-tool-row]')
    expect(tool, 'positive control: protected far-edge source contains a real complete tool row').not.toBeNull()
    const focused = within(tool!).getAllByRole('button')[0]
    expect(focused, 'positive control: focus targets a rendered tool control, not an invented tabindex').toBeDefined()
    await act(async () => {
      if (protection === 'anchor-and-focus') region.scrollTop = direction === 'older' ? region.scrollHeight - region.clientHeight : 0
      focused.focus({ preventScroll: true })
      fireEvent.scroll(region) // REAL capture includes the moved anchor/offscreen focus.
    })
    expect(focused).toHaveFocus()
    const beforeAnchor = anchorAt(region)
    const removed = new Set([pos(edge)]) // One atomic21-drawn-row source adds one source group.
    expect(removed.has(pos(edge)), 'independent trim set really contains the focused group').toBe(true)
    expect(removed.has(beforeAnchor.position!), 'visible anchor is protected only in the anchor case').toBe(protection === 'anchor-and-focus')
    const beforeBoundaries = boundaries()
    const focus = vi.spyOn(region, 'focus') // Browser API observation; not a hook/merger spy.
    const fallbackBeforeRemoval: boolean[] = []
    region.addEventListener('focus', () => fallbackBeforeRemoval.push(focused.isConnected))
    // Canonical whole-source crossing: narration1 +20 terminal tools =21
    // drawn rows at target20. Source count1 makes the cap501, evicting only
    // the opposite atomic source. H80/A80 gives floor2, clamped limit20.
    const incomingRank = direction === 'older' ? 100 : 601
    const incoming = rows(incomingRank, 1, incomingRank, 20)
    const response = page(incoming, { sessionId: MAIN_ID, earlier: true, later: direction === 'older', info: { mode: direction, limit: 20, min_rows: 2 }, rows: 21 })
    expectValidPage(response, [pos(incomingRank)], 21)
    await act(async () => held.resolve(json(response)))
    await waitFor(() => expect(bucket().history.requests[direction].status, 'held real response reaches a settled owner').not.toBe('loading'))

    const committed = bucket().history.readerPositions[0] === pos(direction === 'older' ? 100 : 102)
    if (protection === 'offscreen-focus' && committed) {
      // Explicit alternative authorized by D8/latest F6 brief, not an observed
      // output oracle: scroller fallback MUST precede focused-node removal.
      expect(region).toHaveFocus()
      expect(focus).toHaveBeenCalledWith(expect.objectContaining({ preventScroll: true }))
      expect(fallbackBeforeRemoval, 'fallback after deletion is too late').toStrictEqual([true])
      await expectCommitted(region, direction, beforeAnchor)
    } else {
      expect(bucket().history.readerPositions, 'D9 current anchor/focus prevents unsafe commit or overflow').toStrictEqual(positions(101))
      expect(boundaries(), 'deferral cannot advance either boundary or continuation flag').toStrictEqual(beforeBoundaries)
      expect(focused, 'deferral preserves the same connected focused control').toHaveFocus()
      expect(anchorAt(region), 'deferral preserves visible anchor + intra-row offset despite loading-slot changes').toStrictEqual(beforeAnchor)
      const explanation = notices(region).filter(element => !beforeNoticeText.has(element.textContent))
      expect.soft(explanation, 'F6: protected-page deferral must be VISIBLE, never silent idle/no-op').toHaveLength(1)
      for (const notice of explanation) {
        expect.soft(notice).toBeVisible()
        expect.soft(notice.textContent, 'copy must explain deferral/recovery, not falsely announce loaded history')
          .toMatch(/defer|wait|paus|protect|focus|move|scroll|edge|not (?:load|show)|could not|cannot|unable|try again|keep.*read/iu)
      }
      expect(historyRequests(), 'no hidden resend after a successful but deferred response').toHaveLength(readsBefore + 1)
      await act(async () => region.focus({ preventScroll: true }))
      await moveTo(region, safeRank, direction)
      const safeAnchor = anchorAt(region)
      expect(removed.has(safeAnchor.position!), 'retry anchor is independently outside the trim set').toBe(false)
      expect(historyRequests(), 'moving away alone does not refetch').toHaveLength(readsBefore + 1)
      network.respond('GET', path, () => json(response))
      const retryTrigger = within(region).getByRole('button', { name: label(direction) })
      const focusCallsBeforeRetry = focus.mock.calls.length
      await user.click(retryTrigger)
      await waitFor(() => expect(bucket().history.readerPositions).toStrictEqual(positions(direction === 'older' ? 100 : 102)))
      expect(historyRequests(), 'one explicit retry reuses the bounded read domain').toHaveLength(readsBefore + 2)
      const retried = historyRequests().slice(-2).map(read => Object.fromEntries(new URLSearchParams(read.query)))
      expect(retried, 'both attempts use the original opaque boundary, never a guessed rank').toStrictEqual([
        { [direction === 'older' ? 'before' : 'after']: pos(direction === 'older' ? 101 : 600), limit: '20', min_rows: '2' },
        { [direction === 'older' ? 'before' : 'after']: pos(direction === 'older' ? 101 : 600), limit: '20', min_rows: '2' },
      ])
      await expectCommitted(region, direction, safeAnchor)
      if (retryTrigger.isConnected) {
        expect(retryTrigger, 'D8 retains the SAME mounted retry trigger').toHaveFocus()
      } else {
        expect(region, 'D8 removed trigger falls back to labelled history, never a new row or composer').toHaveFocus()
        expect(focus.mock.calls.slice(focusCallsBeforeRetry), 'D8 fallback during this retry explicitly prevents scrolling')
          .toContainEqual([expect.objectContaining({ preventScroll: true })])
      }
    }
  })
})
