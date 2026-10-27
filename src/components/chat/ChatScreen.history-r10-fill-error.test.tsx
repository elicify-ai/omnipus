// D8/history-error: an action ends in a visible recoverable error, never a
// permanent loading guard. Drive the REAL automatic fill/hook/store/adapter.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import { deferred } from '../../../tests/chat-history-paging/fixtures'
import { resetScrollCase, stopScrollCase, settleScrollLayout, mountRetained, loadAndRender, establishLatest, MAIN_ID, entry, page, pos, expectValidPage, json } from '../../../tests/chat-history-paging/scroll-regression-harness'
let env: ReturnType<typeof resetScrollCase>
beforeEach(() => { env = resetScrollCase() })
afterEach(async () => {
  await stopScrollCase(env.layout)
  expect(env.network.unexpected, 'no undeclared physical HTTP reads').toStrictEqual([])
})
// The 60s budget covers THIS case's real work, not a relaxed assertion: it
// renders a 40-row real list, drives a real failing fill, and then drives a
// real Retry that commits a new page - each step re-renders every row. It
// measures 17-21s locally and exceeded the 30s default in 2 of 3 runs under
// CI contention. No assertion, oracle or scenario was changed to fit.
it('R10: a raw TypeError during a real history fill becomes a visible error with one working Retry', async () => {
  const { selected } = await mountRetained(40, true)
  const region = await loadAndRender(env.network, selected)
  await establishLatest(region, env.layout)
  expect(useChatStore.getState().history.viewMode).toBe('latest')
  const olderSources = Array.from({ length: 20 }, (_, i) => entry(i + 1, { id: `r10-older-${i + 1}`, role: 'assistant', content: `Older source ${i + 1}` }))
  const older = page(olderSources, { sessionId: MAIN_ID, later: true, info: { mode: 'older', limit: 20, min_rows: 20 } })
  expectValidPage(older, olderSources.map(row => row.history_position!), 20)
  const pendingResponse = deferred<Response>()
  let olderRequests = 0, faultReads = 0
  const fault = new TypeError('R10 synthetic response-handling fault')
  env.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, request => {
    const query = new URLSearchParams(request.query)
    if (!query.has('before')) return json(selected)
    olderRequests++
    expect(query.get('before')).toBe(pos(21))
    return olderRequests === 1 ? pendingResponse.promise : json(older)
  })
  await act(async () => {
    fireEvent.wheel(region, { deltaY: -400 })
    region.scrollTop = 0
    fireEvent.scroll(region)
  })
  await waitFor(() => expect(olderRequests, 'the actual viewport fill started its real store read').toBe(1))
  expect(useChatStore.getState().history.requests.older.status).toBe('loading')
  expect(screen.getByText('Loading earlier messages…')).toBeVisible()

  const brokenResponse = json(older)
  // Deliberate process-edge Response fault AFTER fetch succeeded. Ordinary
  // rejected fetch is wrapped as ApiError and would not price this channel.
  // No hook/store/API module is mocked; no rejection listener hides failures.
  Object.defineProperty(brokenResponse, 'headers', { get() { faultReads++; throw fault } })
  await act(async () => { pendingResponse.resolve(brokenResponse); await Promise.resolve() })
  await waitFor(() => expect(faultReads, 'the raw response-access fault really reached the real adapter').toBeGreaterThan(0))
  await waitFor(() => expect(useChatStore.getState().history.requests.older.status,
    'D8: escaped TypeError cannot leave the history direction permanently loading').toBe('error'))
  const alert = screen.getByRole('alert')
  expect(alert).toBeVisible()
  expect(alert).toHaveTextContent('Could not load earlier messages.')
  const retry = screen.getByRole('button', { name: 'Retry' })
  expect(retry).toBeEnabled()
  // fireEvent.click, not userEvent: the Retry control is a plain button whose
  // onClick is the whole contract, and the userEvent pointer sequence spends
  // ~6s stepping a 40-row list through pointer/mouse/focus events - enough to
  // push this case past the 30s testTimeout under CI contention. The
  // repository activates plain buttons this way elsewhere (e.g.
  // ChatScreen.restart-interrupted.test.tsx).
  await act(async () => { fireEvent.click(retry) })
  await waitFor(() => expect(olderRequests, 'working Retry issues exactly ONE new request').toBe(2))
  await waitFor(() => expect(useChatStore.getState().history.requests.older.status).toBe('idle'))
  await settleScrollLayout()
  expect(screen.queryByText('Loading earlier messages…')).toBeNull()
  expect(screen.queryByRole('alert')).toBeNull()
  expect(olderRequests).toBe(2)
  expect(useChatStore.getState().history.readerPositions).toStrictEqual([...olderSources, ...selected.entries].map(row => row.history_position))
}, 60_000)
