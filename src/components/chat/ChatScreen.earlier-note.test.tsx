// D15 intentional replacement of the INTERIM note-only contract by D3/D8's
// reachable earlier action. The three original cases (earlier=true, false,
// never-cleared) remain; real app/store/adapters replace the old inner mocks.
import { afterEach, describe, expect, it } from 'vitest'
import { act, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { deferred, entry, expectValidPage, page, pos } from '../../../tests/chat-history-paging/fixtures'
import { historyRequests, openHistory, sentMessages, stopHistory } from '../../../tests/chat-history-paging/application'

let app: Awaited<ReturnType<typeof openHistory>> | undefined
afterEach(async () => { await stopHistory(app); app = undefined })
const marker = entry(100, { type: 'system', role: 'system', content: 'Conversation context cleared' })
function cleared(earlier: boolean) {
  return page([marker], { sessionId: MAIN_ID, earlier, info: {
    stopped_at_clear: true, stop_reason: 'clear', latest_clear: { entry_id: marker.id, history_position: pos(100) },
  } })
}

describe('ChatScreen — actionable retained history above the clear divider', () => {
  it('earlier=true renders a keyboard-reachable action above the divider and opens exactly one preceding segment', async () => {
    const initial = cleared(true)
    expectValidPage(initial, [pos(100)], 1)
    app = await openHistory(initial)
    const current = app
    const earlier = screen.getByRole('button', { name: 'Show earlier messages', exact: true })
    const divider = screen.getByTestId('clear-context-divider')
    expect(earlier.compareDocumentPosition(divider) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
    const response = deferred<Response>()
    const requested = deferred<URLSearchParams>()
    current.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, (read) => {
      requested.resolve(new URLSearchParams(read.query)); return response.promise
    })
    const user = userEvent.setup()
    earlier.focus()
    await user.keyboard('{Enter}')
    expect((await requested.promise).get('before')).toBe(pos(100))
    const previousMarker = entry(50, { type: 'system', role: 'system', content: 'Conversation context cleared' })
    const previous = page([previousMarker, entry(99)], { sessionId: MAIN_ID, earlier: true, later: true, info: {
      mode: 'older', limit: 20, min_rows: 20, stop_reason: 'clear', stopped_at_clear: true,
      latest_clear: { entry_id: marker.id, history_position: pos(100) },
    } })
    expectValidPage(previous, [pos(50), pos(99)], 2)
    await act(async () => response.resolve(json(previous)))
    await screen.findByText('Saved row 99', { exact: true })
    expect(screen.getAllByTestId('clear-context-divider')).toHaveLength(2)
    expect(historyRequests(current.network).filter((read) => new URLSearchParams(read.query).has('before'))).toHaveLength(1)
    expect(sentMessages(current.socket), 'crossing saved history never sends /clear or user input').toStrictEqual([])
  })

  it('earlier=false keeps the clear divider without a phantom earlier control', async () => {
    const initial = cleared(false)
    expectValidPage(initial, [pos(100)], 1)
    app = await openHistory(initial)
    expect(screen.getByTestId('clear-context-divider')).toHaveTextContent('Conversation context cleared')
    expect(screen.queryByRole('button', { name: 'Show earlier messages', exact: true })).not.toBeInTheDocument()
    expect(historyRequests(app.network).filter((read) => new URLSearchParams(read.query).has('before'))).toStrictEqual([])
  })

  it('a never-cleared terminal chat has neither clear divider nor earlier affordance', async () => {
    const initial = page([entry(1)], { sessionId: MAIN_ID })
    expectValidPage(initial, [pos(1)], 1)
    app = await openHistory(initial)
    expect(screen.getByText('Saved row 1', { exact: true })).toBeInTheDocument()
    expect(screen.queryByTestId('clear-context-divider')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Show earlier messages', exact: true })).not.toBeInTheDocument()
  })
})
