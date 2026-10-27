// T15/T20: plain/manual and recovery behavior through the REAL app.
// B02/B10/B11/B23/B28/B29/B31/B34. Oracles D5/D6/D7a/D8.
import { afterEach, describe, expect, it } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { availableDetail, deferred, entry, page, pos, session, STAMP, UNAVAILABLE } from '../../../tests/chat-history-paging/fixtures'
import {
  historyRequests, openHistory, sendFrames, sendSnapshot, sentMessages, stopHistory, wirePage, WELCOME_HEADING,
} from '../../../tests/chat-history-paging/application'

let app: Awaited<ReturnType<typeof openHistory>> | undefined
afterEach(async () => { await stopHistory(app); app = undefined })

const initialRanks = Array.from({ length: 40 }, (_, i) => 81 + i)
const initialIds = initialRanks.map((rank) => `history-${rank}`)

async function openPlain() {
  // Full default40 page: a short nonterminal page cannot claim row_target/end.
  const initial = page(initialRanks.map((rank) => entry(rank)), { sessionId: MAIN_ID, earlier: true })
  app = await openHistory(initial)
  expect(screen.getByText('Saved row 82', { exact: true }), 'positive control: real plain transcript rendered').toBeInTheDocument()
  return app
}

function earlierControl() {
  return screen.getByRole('button', { name: /^Show earlier messages$/ })
}

function interceptEarlier(current: NonNullable<typeof app>) {
  const request = deferred<URLSearchParams>()
  const response = deferred<Response>()
  current.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, (read) => {
    request.resolve(new URLSearchParams(read.query))
    return response.promise
  })
  return { request, response }
}

describe('T15/T20 actual history actions — B10/B28/B29/B34', () => {
  it('a valid available empty page renders the real welcome heading as a negative-oracle control — B02', async () => {
    app = await openHistory(page([], { sessionId: MAIN_ID }))
    expect(await screen.findByRole('heading', WELCOME_HEADING), 'the unavailable/expired-range tests must be able to detect a real empty-chat welcome').toBeVisible()
    expect(useChatStore.getState().messages).toStrictEqual([])
  })

  it('plain fallback exposes earlier paging with one exact boundary request and no user resend', async () => {
    const current = await openPlain()
    const barrier = interceptEarlier(current)
    await userEvent.setup().click(earlierControl())
    const query = await barrier.request.promise
    expect(Object.fromEntries(query), 'D5 default plain sizing, D3 exclusive occurrence boundary')
      .toStrictEqual({ before: pos(81), limit: '20', min_rows: '20' })
    expect(screen.getByText('Saved row 82', { exact: true })).toBeInTheDocument()
    await act(async () => barrier.response.resolve(json(wirePage([entry(79), entry(80)]))))
    await screen.findByText('Saved row 79', { exact: true })
    expect(useChatStore.getState().messages.map((m) => m.id)).toStrictEqual(['history-79', 'history-80', ...initialIds])
    expect(sentMessages(current.socket)).toStrictEqual([])
    expect(screen.getByTestId('chat-input')).toBeEnabled()
  })

  it.each(['{Enter}', ' '] as const)('keyboard %s coalesces duplicate activation and retains the focused pending control', async (key) => {
    const current = await openPlain()
    const barrier = interceptEarlier(current)
    const user = userEvent.setup()
    const control = earlierControl()
    control.focus()
    await user.keyboard(key)
    await barrier.request.promise
    await user.keyboard(key)
    expect(control, 'D8 aria-disabled, never native disabled while focused').not.toBeDisabled()
    expect(control).toHaveAttribute('aria-disabled', 'true')
    expect(control).toHaveFocus()
    expect(historyRequests(current.network).filter((r) => new URLSearchParams(r.query).has('before'))).toHaveLength(1)
    expect(screen.getAllByRole('status').filter((node) => node.textContent === 'Loading earlier messages…')).toHaveLength(1)
    await act(async () => barrier.response.resolve(json(wirePage([entry(80)]))))
    const history = await screen.findByLabelText('Conversation history', { exact: true })
    await waitFor(() => expect(control).not.toBeInTheDocument())
    expect(history, 'D8 terminal success transfers focus to labelled scroller, not new rows/composer').toHaveFocus()
    expect(screen.getByTestId('chat-input')).not.toHaveFocus()
    expect(sentMessages(current.socket)).toStrictEqual([])
  })

  it('Retry retains the same failed boundary and focused node across repeated failures', async () => {
    const current = await openPlain()
    let barrier = interceptEarlier(current)
    const user = userEvent.setup()
    await user.click(earlierControl())
    await barrier.request.promise
    await act(async () => barrier.response.resolve(json({ error: 'storage unavailable' }, 500)))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load earlier messages.')
    const retry = within(alert).getByRole('button', { name: /^Retry$/ })
    retry.focus()
    barrier = interceptEarlier(current)
    await user.keyboard('{Enter}')
    expect(Object.fromEntries(await barrier.request.promise)).toStrictEqual({ before: pos(81), limit: '20', min_rows: '20' })
    expect(retry).toHaveFocus()
    expect(within(alert).getByRole('button', { name: /^Retry$/ })).toBe(retry)
    await act(async () => barrier.response.resolve(json({ error: 'storage unavailable' }, 500)))
    await waitFor(() => expect(alert).toHaveTextContent('Could not load earlier messages. Retry 2 failed.'))
    expect(screen.getAllByRole('alert')).toStrictEqual([alert])
    expect(retry).toHaveFocus()
    expect(screen.getByText('Saved row 81', { exact: true })).toBeInTheDocument()
    expect(screen.getByTestId('chat-input')).toBeEnabled()
    expect(sentMessages(current.socket)).toStrictEqual([])
  })

  it('Return to latest has its own failure/Retry, retains draft and never resends pending input', async () => {
    const current = await openPlain()
    let barrier = interceptEarlier(current)
    const user = userEvent.setup()
    await user.click(earlierControl())
    await barrier.request.promise
    await act(async () => barrier.response.resolve(json(wirePage([entry(79), entry(80)]))))
    await screen.findByText('Saved row 79', { exact: true })
    const input = screen.getByTestId('chat-input')
    await user.type(input, 'Keep this unsent draft')
    barrier = interceptEarlier(current)
    const latest = screen.getByRole('button', { name: /^Return to latest$/ })
    await user.click(latest)
    expect((await barrier.request.promise).has('before'), 'D9 fresh latest read, not older retry').toBe(false)
    expect(screen.getByText('Saved row 79', { exact: true })).toBeInTheDocument()
    expect(within(screen.getByLabelText('Conversation history', { exact: true })).getByRole('status')).toHaveTextContent('Loading latest messages…')
    await act(async () => barrier.response.resolve(json({ error: 'latest read failed' }, 500)))
    const failure = await screen.findByRole('alert')
    expect(failure).toHaveTextContent('Could not load latest messages.')
    expect(input).toHaveValue('Keep this unsent draft')
    barrier = interceptEarlier(current)
    await user.click(within(failure).getByRole('button', { name: /^Retry$/ }))
    expect((await barrier.request.promise).has('before')).toBe(false)
    const latestSources = Array.from({ length: 40 }, (_, i) => entry(121 + i))
    await act(async () => barrier.response.resolve(json(wirePage(latestSources, true, 'latest'))))
    await screen.findByText('Saved row 160', { exact: true })
    expect(screen.queryByText('Saved row 79', { exact: true })).not.toBeInTheDocument()
    expect(input).toHaveValue('Keep this unsent draft')
    expect(sentMessages(current.socket)).toStrictEqual([])
  })

  it('historical prepend does not announce old answers as a new response', async () => {
    const current = await openPlain()
    const barrier = interceptEarlier(current)
    await userEvent.setup().click(earlierControl())
    await barrier.request.promise
    const oldAnswer = entry(80, { role: 'assistant', type: 'message', content: 'This is an OLD answer' })
    await act(async () => barrier.response.resolve(json(wirePage([oldAnswer]))))
    await screen.findByText('This is an OLD answer', { exact: true })
    expect(screen.queryByText('New response from Mia', { exact: true }), 'D8 historical hydration is not a live answer announcement').not.toBeInTheDocument()
    const history = screen.getByLabelText('Conversation history', { exact: true })
    expect(history).not.toHaveAttribute('aria-busy', 'true')
    expect(screen.queryByText('Loading earlier messages…', { exact: true })).not.toBeInTheDocument()
    expect(sentMessages(current.socket)).toStrictEqual([])
  })
})

describe('T08/T15 view generation, not only equal session ID — B11', () => {
  it.each(['success', 'failure'] as const)('fences an obsolete older-page %s across A→B→A', async (outcome) => {
    const current = await openPlain()
    const oldRequest = deferred<void>()
    const oldResponse = deferred<Response>()
    current.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, (read) => {
      if (new URLSearchParams(read.query).has('before')) { oldRequest.resolve(); return oldResponse.promise }
      return current.initialRest.promise
    })
    await userEvent.setup().click(earlierControl())
    await oldRequest.promise
    const otherId = 'history-other-view'
    const other = page([], { sessionId: otherId })
    current.network.respond('GET', `/api/v1/sessions/${otherId}`, () => json(availableDetail(other, session(otherId))))
    current.network.respond('GET', `/api/v1/sessions/${otherId}/messages`, () => json(other))
    await act(async () => { expect(useSessionStore.getState().attachToSession(otherId, 'chat', 'Other view', 'mia')).toBe(true) })
    await sendSnapshot(current.socket, other)
    await act(async () => { expect(useSessionStore.getState().attachToSession(MAIN_ID, 'main', 'Mia main chat', 'mia')).toBe(true) })
    const freshRanks = Array.from({ length: 40 }, (_, i) => 501 + i)
    await sendSnapshot(current.socket, page(freshRanks.map((rank) => entry(rank)), { sessionId: MAIN_ID, earlier: true }), 'retention_exceeded', 200)
    await screen.findByText('Saved row 540', { exact: true })
    await act(async () => oldResponse.resolve(outcome === 'success'
      ? json(wirePage([entry(79), entry(80)])) : json({ error: 'obsolete read failure' }, 500)))
    expect(useChatStore.getState().messages.map((message) => message.id), 'D6 old A generation cannot edit the NEW A view')
      .toStrictEqual(freshRanks.map((rank) => `history-${rank}`))
    expect(screen.queryByText('Could not load earlier messages.', { exact: true })).not.toBeInTheDocument()
    expect(sentMessages(current.socket)).toStrictEqual([])
  })
})

describe('T15 unavailable history keeps current controls — B31/B43', () => {
  it('live-only history ErrorState coexists with an enabled composer and current Stop', async () => {
    const current = await openPlain()
    await sendFrames(current.socket, [
      { type: 'session_state', session_id: MAIN_ID, user_id: 'history-reader', pending_approvals: [], emitted_at: STAMP,
        history_state: { status: 'unavailable', reason: 'index_missing', message: UNAVAILABLE },
        active_turn: { turn_id: 'running-current', agent_id: 'mia', started_at: STAMP } },
      { type: 'catch_up_complete', session_id: MAIN_ID, mode: 'live_only', seq: 200, boot_id: 'history-boot' },
    ])
    const failure = await screen.findByRole('alert')
    expect(failure).toHaveTextContent(UNAVAILABLE)
    expect(within(failure).getByRole('button', { name: /^Retry$/ })).toBeEnabled()
    expect(screen.getByTestId('chat-input'), 'D7a history failure must not replace the whole thread/composer shell').toBeEnabled()
    expect(screen.getByRole('button', { name: /^Stop/ })).toBeEnabled()
    expect(screen.queryByText(/preparing history/i)).not.toBeInTheDocument()
    expect(sentMessages(current.socket)).toStrictEqual([])
  })
})
