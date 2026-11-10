// T07 public-path bootstrap/hidden guards, B01/B07/D5/D-G1c.
// No virtualizer mocking or fabricated prefix sizes. Measured A/2H±1 cases
// require the T16/T28 browser handoff; these tests do NOT certify that geometry.
import { afterEach, describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { MAIN_ID } from '../../../tests/l1-room-ui/harness'
import { entry, page } from '../../../tests/chat-history-paging/fixtures'
import { historyRequests, openHistory, stopHistory } from '../../../tests/chat-history-paging/application'

let app: Awaited<ReturnType<typeof openHistory>> | undefined
afterEach(async () => { await stopHistory(app); app = undefined })

describe('T07 actual attach bootstrap and reader guard — B01/B07', () => {
  it('unknown initial geometry uses 40/20 once and never starts a competing full REST history open', async () => {
    const sources = Array.from({ length: 40 }, (_, i) => entry(81 + i))
    app = await openHistory(page(sources, { sessionId: MAIN_ID, earlier: true }))
    const attaches = app.socket.sent.filter((frame) => frame.type === 'attach_session' && !frame.ack_attention)
    expect(attaches, 'D5 no second attach just to correct sizing').toHaveLength(1)
    const first = attaches[0]
    if (first.type !== 'attach_session') throw new Error('Fixture instrument: wrong attach discriminator')
    expect({ limit: first.history_limit ?? 40, minRows: first.history_min_rows ?? 20 }, 'D5 omitted geometry means server defaults40/20')
      .toStrictEqual({ limit: 40, minRows: 20 })
    expect(screen.getByText('Saved row 82', { exact: true }), 'positive control: WS history is rendered').toBeInTheDocument()
    expect(historyRequests(app.network).filter((read) => !new URLSearchParams(read.query).has('before')), 'D6 WS owns initial history; do not fetch a parallel full transcript')
      .toStrictEqual([])
  })
})
