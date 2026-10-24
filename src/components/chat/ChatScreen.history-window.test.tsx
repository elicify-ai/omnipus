// T09 whole safe far-edge eviction, B12/B34/D9/D-M1. Actual earlier action,
// full app/store/adapter and real group order. No invented store paging API.
// Many page commits: CI only; not a quick local test or heap certification.
import { afterEach, describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { entry, page } from '../../../tests/chat-history-paging/fixtures'
import { openHistory, sentMessages, stopHistory, wirePage } from '../../../tests/chat-history-paging/application'

let app: Awaited<ReturnType<typeof openHistory>> | undefined
afterEach(async () => { await stopHistory(app); app = undefined })

describe('T09 real reader-window boundary — B12/B34', () => {
  it('a 501st earlier source keeps the new prepend and evicts only the unprotected newest whole group', async () => {
    app = await openHistory(page(Array.from({ length: 40 }, (_, i) => entry(561 + i)), { sessionId: MAIN_ID, earlier: true }))
    const current = app
    const user = userEvent.setup()
    for (let upper = 560; upper >= 120; upper -= 20) {
      const sources = Array.from({ length: 20 }, (_, i) => entry(upper - 19 + i))
      current.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(wirePage(sources, true)))
      await user.click(screen.getByRole('button', { name: /^Show earlier messages$/ }))
      await screen.findByText(`Saved row ${upper - 19}`, { exact: true })
    }
    const atCap = Array.from({ length: 500 }, (_, i) => `history-${101 + i}`)
    expect(useChatStore.getState().messages.map((message) => message.id), 'positive control: reader really reached500 whole groups').toStrictEqual(atCap)
    current.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(wirePage([entry(100)])))
    await user.click(screen.getByRole('button', { name: /^Show earlier messages$/ }))
    await screen.findByText('Saved row 100', { exact: true })
    const expected = Array.from({ length: 500 }, (_, i) => `history-${100 + i}`)
    await waitFor(() => expect(useChatStore.getState().messages.map((message) => message.id)).toStrictEqual(expected))
    expect(screen.queryByText('Saved row 600', { exact: true }), 'distant tail is not concatenated under the reader').not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Return to latest$/ })).toBeEnabled()
    expect(sentMessages(current.socket), 'paging/eviction never resends user input').toStrictEqual([])
  })
})
