// Architect seam U; spec D7a/D8/D9. Expectations come from the written
// retained-reader/control rules, not from HistoryBoundary's current output.
// Real route/store/adapters/renderers; only HTTP/WS and DOM layout are doubles.
import { afterEach, describe, expect, it } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { deferred, entry, page, pos, STAMP, UNAVAILABLE } from '../../../tests/chat-history-paging/fixtures'
import { openHistory, sendFrames, sendSnapshot, sentMessages, stopHistory, wirePage } from '../../../tests/chat-history-paging/application'

let app: Awaited<ReturnType<typeof openHistory>> | undefined
afterEach(async () => { await stopHistory(app); app = undefined })

// Independent fixture: newest40 sources81..120; older read adds79..80.
const newest = Array.from({ length: 40 }, (_, index) => entry(81 + index))
const cached = [entry(79), entry(80), ...newest]
const cachedIds = cached.map(source => source.id)
const cachedPositions = cached.map(source => source.history_position!)
const DRAFT = 'Keep this unsent draft while history is unavailable'
const latestName = { name: /^Return to latest$/ }

function expectCachedReader() {
  expect(useChatStore.getState().messages.map(message => message.id), 'D7a retains the exact cached reader, not an empty/latest replacement').toStrictEqual(cachedIds)
  expect(useChatStore.getState().history.readerPositions, 'D9 cached source identities survive availability failure').toStrictEqual(cachedPositions)
  expect(useChatStore.getState().history.viewMode, 'U recovery is not a passive Return action').toBe('historical')
  expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
  expect(screen.getByText('Saved row 79', { exact: true })).toBeVisible()
  expect(screen.getByText('Saved row 120', { exact: true })).toBeVisible()
  expect(screen.getByTestId('chat-input')).toHaveValue(DRAFT)
}

async function openOlderReader() {
  app = await openHistory(page(newest, { sessionId: MAIN_ID, earlier: true }))
  const current = app
  const requested = deferred<URLSearchParams>()
  const response = deferred<Response>()
  current.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, read => {
    requested.resolve(new URLSearchParams(read.query))
    return response.promise
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: /^Show earlier messages$/ }))
  expect(Object.fromEntries(await requested.promise), 'positive control: real older-page adapter request').toStrictEqual({ before: pos(81), limit: '20', min_rows: '20' })
  await act(async () => response.resolve(json(wirePage([entry(79), entry(80)]))))
  await screen.findByText('Saved row 79', { exact: true })
  const input = screen.getByTestId('chat-input')
  await user.type(input, DRAFT)
  const region = screen.getByLabelText('Conversation history', { exact: true })
  // Logical DOM scroll sentinel only: two synthetic80px rows, not pixel/UAT proof.
  fireEvent.scroll(region, { target: { scrollTop: 160 } })
  const latest = screen.getByRole('button', latestName)
  expect(latest, 'positive control: Return really existed before availability changed').toBeVisible()
  expectCachedReader()
  return { current, user, region, input, latest }
}

async function unavailable(current: NonNullable<typeof app>, seq: number) {
  await sendFrames(current.socket, [
    { type: 'session_state', session_id: MAIN_ID, user_id: 'history-reader', pending_approvals: [], emitted_at: STAMP,
      history_state: { status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE },
      active_turn: { turn_id: 'unavailable-current-turn', agent_id: 'mia', started_at: STAMP } },
    { type: 'catch_up_complete', session_id: MAIN_ID, mode: 'live_only', seq, boot_id: 'history-boot' },
  ])
  await waitFor(() => expect(useChatStore.getState().sessionsById[MAIN_ID].cursor).toStrictEqual({ bootId: 'history-boot', seq }))
  expect(useChatStore.getState().history.serverState).toStrictEqual({ status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE })
  const failure = screen.getByRole('alert')
  expect(failure).toHaveTextContent(UNAVAILABLE)
  return { failure, retry: within(failure).getByRole('button', { name: /^Retry$/ }) }
}

describe('seam U — historical cached reader receives unsolicited unavailable history', () => {
  it('keeps ErrorState/Retry AND the focused Return, cached rows/draft and real current Stop without passive navigation or resend', async () => {
    const { current, user, region, input, latest } = await openOlderReader()
    latest.focus()
    expect(latest).toHaveFocus()
    const { retry } = await unavailable(current, 200)
    expect(retry).toBeVisible()
    expect(retry).toBeEnabled()
    // Soft assertions still FAIL the case, but let the other independent
    // retention/live-control observations run when the Return regression fires.
    expect.soft(latest, 'D9/U must not remove Return when the history error appears').toBeVisible()
    expect.soft(screen.queryByRole('button', latestName), 'Return and ErrorState/Retry must coexist').toBe(latest)
    expect.soft(latest, 'D8 unsolicited failure must not unmount the focused Return').toHaveFocus()
    expectCachedReader()
    expect(region.scrollTop, 'unsolicited state cannot navigate the old reader').toBe(160)
    expect(input).toBeEnabled()
    expect(screen.getByRole('button', { name: /^Stop/ })).toBeEnabled()
    expect(current.socket.sent.filter(frame => frame.type === 'attach_session' && !frame.ack_attention), 'only the original opening attach, no passive recovery/navigation').toHaveLength(1)
    expect(sentMessages(current.socket), 'history failure cannot resend the draft/input').toStrictEqual([])
    await user.type(input, ' — still editable')
    expect(input).toHaveValue(`${DRAFT} — still editable`)
    await user.click(screen.getByRole('button', { name: /^Stop/ }))
    expect(current.socket.sent.filter(frame => frame.type === 'cancel'), 'D7a actual current-chat Stop remains callable').toStrictEqual([{ type: 'cancel', session_id: MAIN_ID }])
    expect(sentMessages(current.socket)).toStrictEqual([])
  })

  it('deduplicates pending/repeated failed recovery, retains focused Retry plus Return, and never replaces or resends the cached reader', async () => {
    const { current, user, latest } = await openOlderReader()
    const { failure, retry } = await unavailable(current, 200)
    for (const attempt of [1, 2]) {
      await user.click(retry)
      expect(retry).toHaveFocus()
      await user.keyboard('{Enter}')
      expect(current.socket.sent.filter(frame => frame.type === 'attach_session' && !frame.ack_attention).slice(1), 'D7a one recovery attach WITHOUT since_seq per explicit attempt').toStrictEqual(Array.from({ length: attempt }, () => ({ type: 'attach_session', session_id: MAIN_ID })))
      expect(retry, 'D8 pending action stays mounted/focused, not natively disabled').not.toBeDisabled()
      expect(retry).toHaveAttribute('aria-disabled', 'true')
      expect.soft(latest, 'Return stays discoverable during unavailable recovery').toBeVisible()
      expectCachedReader()
      await unavailable(current, 200 + attempt)
      expect(within(failure).getByRole('button', { name: /^Retry$/ }), 'same focused recovery node after repeated refusal').toBe(retry)
      expect(retry).toHaveFocus()
      expect(retry).not.toHaveAttribute('aria-disabled', 'true')
      expect.soft(latest, 'repeated refusal is not permission to remove Return').toBeVisible()
      expectCachedReader()
      expect(sentMessages(current.socket)).toStrictEqual([])
    }
  })

  it('successful recovery preserves the old reader; only a successful explicit Return commits latest, without duplicate reads or resend', async () => {
    const { current, user, region, input } = await openOlderReader()
    const { retry } = await unavailable(current, 200)
    const refreshRequest = deferred<URLSearchParams>()
    const latestRequest = deferred<URLSearchParams>()
    const latestResponse = deferred<Response>()
    const explicitLatestQueries: Record<string, string>[] = []
    const refreshed = page(cached, { sessionId: MAIN_ID, info: { mode: 'window_refresh', limit: 100, min_rows: 20, stop_reason: 'range_end' } })
    current.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, read => {
      const query = new URLSearchParams(read.query)
      if (query.has('window_start')) { refreshRequest.resolve(query); return json(refreshed) }
      explicitLatestQueries.push(Object.fromEntries(query))
      latestRequest.resolve(query)
      return latestResponse.promise
    })
    await user.click(retry)
    const fresh = page(Array.from({ length: 40 }, (_, index) => entry(121 + index)), { sessionId: MAIN_ID, earlier: true })
    await sendSnapshot(current.socket, fresh, 'retention_exceeded', 300)
    expect(Object.fromEntries(await refreshRequest.promise), 'D7 recovery refreshes the retained interval, not a navigation instruction').toStrictEqual({ window_start: pos(79), window_end: pos(120), limit: '100', min_rows: '20' })
    await waitFor(() => expect(useChatStore.getState().history.requests.refresh.status).toBe('idle'))
    expectCachedReader()
    expect(screen.queryByText('Saved row 160', { exact: true }), 'fresh tail is not passively grafted into old history').not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(sentMessages(current.socket)).toStrictEqual([])

    const latest = screen.getByRole('button', latestName)
    await user.click(latest)
    expect(Object.fromEntries(await latestRequest.promise), 'D9 explicit Return reads a fresh bounded latest page').toStrictEqual({ limit: '40', min_rows: '20' })
    await user.keyboard('{Enter}')
    expect(explicitLatestQueries, 'D8 exactly ONE explicit latest read; a configured but unused opening response is not a request').toStrictEqual([{ limit: '40', min_rows: '20' }])
    expectCachedReader()
    expect(latest).toHaveFocus()
    expect(latest).not.toBeDisabled()
    expect(latest).toHaveAttribute('aria-disabled', 'true')
    await act(async () => latestResponse.resolve(json(fresh)))
    await screen.findByText('Saved row 160', { exact: true })
    expect(useChatStore.getState().history.viewMode).toBe('latest')
    expect(useChatStore.getState().messages.map(message => message.id)).toStrictEqual(Array.from({ length: 40 }, (_, index) => `history-${121 + index}`))
    expect(screen.queryByText('Saved row 79', { exact: true })).not.toBeInTheDocument()
    expect(region, 'D8 successful Return moves focus to labelled history with no composer theft').toHaveFocus()
    expect(input).toHaveValue(DRAFT)
    expect(input).not.toHaveFocus()
    expect(sentMessages(current.socket)).toStrictEqual([])
  })
})
