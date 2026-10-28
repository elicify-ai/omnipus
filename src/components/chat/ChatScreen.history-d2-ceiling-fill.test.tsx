// T64/A2.3: real adapter, store, virtualizer and fill loop. Only HTTP and
// physical layout are controlled by the existing browser-boundary instrument.
import { afterEach, expect, it } from 'vitest'
import { act, waitFor } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import { MAIN_ID, json } from '../../../tests/l1-room-ui/harness'
import { Stage1Network, resetStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import { entry, page, pos } from '../../../tests/chat-history-paging/fixtures'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'
import { loadAndRender, settleScrollLayout, stopScrollCase } from '../../../tests/chat-history-paging/scroll-regression-harness'

let layout: ReturnType<typeof installHistoryLayout> | undefined
afterEach(async () => { if (layout) await stopScrollCase(layout); layout = undefined })
it('T64: a below-floor byte-ceiling page continues contiguously until the measured height goal', async () => {
  resetStage1App()
  const network = new Stage1Network()
  layout = installHistoryLayout({ virtual: true, hidden: true })
  const initial = page([entry(61)], { sessionId: MAIN_ID, earlier: true, info: {
    limit: 40, min_rows: 20, stop_reason: 'byte_ceiling', byte_ceiling: 8388608,
  } })
  await loadAndRender(network, initial)
  expect(useChatStore.getState().history.readerPositions).toEqual([pos(61)])
  network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, request => {
    const query = new URLSearchParams(request.query)
    const before = query.get('before')
    const limit = Number(query.get('limit'))
    const minRows = Number(query.get('min_rows'))
    const last = before === pos(61) ? 60 : before === pos(41) ? 40 : -1
    expect(last, 'top-up never repeats a boundary or invents an omitted middle').not.toBe(-1)
    return json(page(Array.from({ length: 20 }, (_, i) => entry(last - 19 + i)), {
      sessionId: MAIN_ID, earlier: true, later: true,
      info: { mode: 'older', limit, min_rows: minRows, stop_reason: limit > 20 ? 'byte_ceiling' : 'row_target' },
    }))
  })
  await act(async () => layout!.visibility(false))
  await settleScrollLayout()
  await waitFor(() => expect(useChatStore.getState().history.readerPositions).toEqual(Array.from({ length: 41 }, (_, i) => pos(21 + i))))
  const older = network.requests.filter(request => request.path.endsWith('/messages') && new URLSearchParams(request.query).has('before'))
  expect(older.map(request => new URLSearchParams(request.query).get('before'))).toEqual([pos(61), pos(41)])
  expect(useChatStore.getState().history.requests.older.status).toBe('idle')
  expect(useChatStore.getState().history.requests.latest.status).toBe('idle')
  expect(network.unexpected).toEqual([])
})
