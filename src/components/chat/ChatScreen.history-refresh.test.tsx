// T34 / B26/B27/B47/B48, D7/D8/D-F1/D-F2. Full real app, adapter,
// reducers and controls. No fake paging reducer or loading/error component.
import { afterEach, describe, expect, it } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { deferred, entry, expectValidPage, page, pos } from '../../../tests/chat-history-paging/fixtures'
import { historyRequests, openHistory, sendSnapshot, sentMessages, stopHistory, wirePage, WELCOME_HEADING } from '../../../tests/chat-history-paging/application'

let app: Awaited<ReturnType<typeof openHistory>> | undefined
afterEach(async () => { await stopHistory(app); app = undefined })

function heldEntry(rank: number) {
  return rank === 2999 || rank === 3001
    ? entry(rank, { type: 'system', role: 'system', content: 'Conversation context cleared' }) : entry(rank)
}
async function oldReader() {
  // Two short clear segments are legitimate underfills; never fabricate a
  // short row_target page with unexplained retained predecessors.
  app = await openHistory(page([heldEntry(3001), heldEntry(3002)], { sessionId: MAIN_ID, earlier: true, info: {
    stopped_at_clear: true, stop_reason: 'clear', latest_clear: { entry_id: 'history-3001', history_position: pos(3001) },
  } }))
  const current = app
  const requested = deferred<void>()
  current.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => {
    requested.resolve()
    return json(page([heldEntry(2999), heldEntry(3000)], { sessionId: MAIN_ID, earlier: true, later: true, info: {
      mode: 'older', limit: 20, min_rows: 20, stopped_at_clear: true, stop_reason: 'clear',
      latest_clear: { entry_id: 'history-3001', history_position: pos(3001) },
    } }))
  })
  await userEvent.setup().click(screen.getByRole('button', { name: /^Show earlier messages$/ }))
  await requested.promise
  await screen.findByText('Saved row 3000', { exact: true })
  expect(useChatStore.getState().messages.map((m) => m.id)).toStrictEqual(['history-2999', 'history-3000', 'history-3001', 'history-3002'])
  return current
}

function rangeBarrier(current: NonNullable<typeof app>) {
  const request = deferred<URLSearchParams>()
  const response = deferred<Response>()
  current.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, (read) => {
    request.resolve(new URLSearchParams(read.query))
    return response.promise
  })
  return { request, response }
}
async function freshTail(current: NonNullable<typeof app>) {
  // Real sent attach owns the incoming staged batch; no second replay buffer.
  expect(useConnectionStore.getState().connection?.send({ type: 'attach_session', session_id: MAIN_ID, since_seq: 100, boot_id: 'history-boot' })).toBe(true)
  const latest = Array.from({ length: 40 }, (_, i) => entry(6001 + i))
  await sendSnapshot(current.socket, page(latest, { sessionId: MAIN_ID, earlier: true }), 'retention_exceeded', 200)
}

function expectHeldRange(query: URLSearchParams) {
  expect(query.get('window_start'), 'D7 exactly the old lower bound, never current tail').toBe(pos(2999))
  expect(query.get('window_end')).toBe(pos(3002))
  expect(query.has('after')).toBe(false)
}

describe('T34 retained range refresh and expiry — B26/B47/B48', () => {
  it('failed refresh keeps all cached rows, clears busy, pauses prefetch and retries only the same saved range', async () => {
    const current = await oldReader()
    const oldIds = ['history-2999', 'history-3000', 'history-3001', 'history-3002']
    const input = screen.getByTestId('chat-input')
    input.focus()
    let barrier = rangeBarrier(current)
    const attachBefore = current.socket.sent.filter((frame) => frame.type === 'attach_session').length
    await freshTail(current)
    expectHeldRange(await barrier.request.promise)
    expect(useChatStore.getState().messages.map((m) => m.id), 'D7 distant tail must not be grafted below retained reader').toStrictEqual(oldIds)
    const region = screen.getByLabelText('Conversation history', { exact: true })
    expect(region).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByRole('status')).toHaveTextContent('Refreshing loaded messages…')
    await act(async () => barrier.response.resolve(json({ error: 'refresh unavailable' }, 500)))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not refresh loaded messages.')
    expect(screen.getByText(/out.of.date/i), 'D7 cached rows are explicitly labelled stale').toBeInTheDocument()
    expect(region).not.toHaveAttribute('aria-busy', 'true')
    expect(useChatStore.getState().messages.map((m) => m.id)).toStrictEqual(oldIds)
    expect(input).toHaveFocus()
    const beforeScroll = historyRequests(current.network).length
    await act(async () => { fireEvent.scroll(region, { target: { scrollTop: 0 } }) })
    expect(historyRequests(current.network), 'D7 ordinary prefetch is paused after range failure').toHaveLength(beforeScroll)
    expect(useChatStore.getState().sessionsById[MAIN_ID].cursor, 'revalidation failure does not undo completed live catch-up')
      .toStrictEqual({ bootId: 'history-boot', seq: 200 })
    barrier = rangeBarrier(current)
    await userEvent.setup().click(within(alert).getByRole('button', { name: /^Retry$/ }))
    expectHeldRange(await barrier.request.promise)
    const reconciled = wirePage([heldEntry(2999), heldEntry(3000), heldEntry(3001), heldEntry(3002)], false, 'window_refresh')
    reconciled.page_info.stop_reason = 'range_end'
    expectValidPage(reconciled, [pos(2999), pos(3000), pos(3001), pos(3002)], 4)
    await act(async () => barrier.response.resolve(json(reconciled)))
    await waitFor(() => expect(screen.queryByText('Could not refresh loaded messages.', { exact: true })).not.toBeInTheDocument())
    expect(useChatStore.getState().messages.map((m) => m.id)).toStrictEqual(oldIds)
    expect(current.socket.sent.filter((frame) => frame.type === 'attach_session')).toHaveLength(attachBefore + 1)
    expect(sentMessages(current.socket)).toStrictEqual([])
  })

  it.each([
    { label: 'anchor survives', ranks: [2999, 3001, 3002], notice: 'Some loaded messages have expired.' },
    { label: 'anchor expired with later survivor', ranks: [3000, 3001, 3002], notice: 'Some loaded messages have expired.' },
    { label: 'entire interval expired', ranks: [], notice: 'These loaded messages have expired.' },
  ])('$label uses exact expiry text without passive latest navigation or composer focus theft', async ({ ranks, notice }) => {
    const current = await oldReader()
    const composer = screen.getByTestId('chat-input')
    composer.focus()
    const barrier = rangeBarrier(current)
    await freshTail(current)
    expectHeldRange(await barrier.request.promise)
    const response = wirePage(ranks.map(heldEntry), false, 'window_refresh')
    response.page_info.stop_reason = 'range_end'
    response.page_info.retention_generation = 'fixture-retained-2'
    expectValidPage(response, ranks.map(pos), ranks.length)
    await act(async () => barrier.response.resolve(json(response)))
    await screen.findByText(notice, { exact: true })
    expect(useChatStore.getState().messages.map((m) => m.id)).toStrictEqual(ranks.map((rank) => `history-${rank}`))
    expect(composer, 'D8 unrelated composer focus stays').toHaveFocus()
    expect(screen.queryByText('Saved row 6002', { exact: true }), 'D7 expiration does not jump to staged latest tail').not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Return to latest$/ })).toBeEnabled()
    expect(screen.queryByRole('heading', WELCOME_HEADING)).not.toBeInTheDocument()
    expect(sentMessages(current.socket)).toStrictEqual([])
  })
})

// Alternate advertised positive budgets are explicitly supported (D-B5/D10).
// Keep the consumer continuation test small without manufacturing 100MiB of
// tool output: each independently encoded 20-row page sets its advertised soft
// budget to exactly its metered bytes. A 21st same-width group exceeds it.
// Server DEFAULT=4194304 and huge-output selection are backend/T27 + browser/T28.
function budgetRangePage(first: number, last: number, earlier: boolean, limit: number) {
  const sources = Array.from({ length: last - first + 1 }, (_, i) => entry(first + i))
  const result = page(sources, { sessionId: MAIN_ID, earlier, later: last < 5500, info: {
    mode: 'window_refresh', limit, min_rows: 20,
    stop_reason: earlier ? (limit === 20 ? 'row_target' : 'byte_budget') : 'range_end', retention_generation: 'fixture-retained-2',
  } })
  result.page_info.soft_byte_budget = result.page_info.projected_bytes
  expectValidPage(result, sources.map((source) => source.history_position!), sources.length)
  return result
}

describe('T34 bounded range continuation — B26/B47', () => {
  it('revalidates a 500-group held interval through 25 budget-limited pages, not a five-request cap or tail walk', async () => {
    const firstPage = Array.from({ length: 40 }, (_, i) => entry(5461 + i))
    app = await openHistory(page(firstPage, { sessionId: MAIN_ID, earlier: true }))
    const current = app
    const user = userEvent.setup()
    // Exercise REAL paging to establish historical mode. setMessages is not
    // substituted for the paging action. 40 + 23*20 = exactly500 source groups.
    for (let upper = 5460; upper >= 5020; upper -= 20) {
      const sources = Array.from({ length: 20 }, (_, i) => entry(upper - 19 + i))
      current.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(wirePage(sources, true)))
      await user.click(screen.getByRole('button', { name: /^Show earlier messages$/ }))
      await screen.findByText(`Saved row ${upper - 19}`, { exact: true })
    }
    const held = Array.from({ length: 500 }, (_, i) => `history-${5001 + i}`)
    expect(useChatStore.getState().messages.map((m) => m.id)).toStrictEqual(held)
    const reads: URLSearchParams[] = []
    current.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, (request) => {
      const query = new URLSearchParams(request.query)
      reads.push(query)
      expect(query.get('window_start')).toBe(pos(5001))
      expect(query.get('window_end')).toBe(pos(5500))
      expect(query.has('after')).toBe(false)
      const upper = 5500 - (reads.length - 1) * 20
      expect(query.get('before'), 'closed interval is newest-to-oldest, exclusive continuation')
        .toBe(reads.length === 1 ? null : pos(upper + 1))
      expect(reads.length, 'no unchanged-cursor loop').toBeLessThanOrEqual(25)
      return json(budgetRangePage(upper - 19, upper, reads.length < 25, Number(query.get('limit') ?? 40)))
    })
    await freshTail(current)
    await waitFor(() => expect(reads).toHaveLength(25))
    await waitFor(() => expect(screen.queryByText('Refreshing loaded messages…', { exact: true })).not.toBeInTheDocument())
    expect(useChatStore.getState().messages.map((m) => m.id), 'entire kept interval remains contiguous, no distant latest rows').toStrictEqual(held)
    expect(reads.filter((query) => !query.has('window_start'))).toStrictEqual([])
    expect(sentMessages(current.socket)).toStrictEqual([])
  })
})
