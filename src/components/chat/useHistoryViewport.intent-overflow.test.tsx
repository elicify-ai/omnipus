// Final-gate architect item A: reader intent (an upward wheel, key or touch)
// enters history reading only when the conversation actually overflows. With
// nothing to scroll, the reader cannot move away from the live end, so no
// floating Return to latest may appear. Real app, real plain list and store.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, fireEvent, screen } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import { MAIN_ID } from '../../../tests/l1-room-ui/harness'
import { entry, page } from '../../../tests/chat-history-paging/fixtures'
import { openHistory, stopHistory } from '../../../tests/chat-history-paging/application'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'

let app: Awaited<ReturnType<typeof openHistory>> | undefined
let layout: ReturnType<typeof installHistoryLayout> | undefined
// 800px viewport, 80px rows: 3 rows fit, 40 rows overflow (the real layout model).
beforeEach(() => { layout = installHistoryLayout({ virtual: false }) })
afterEach(async () => { await stopHistory(app); app = undefined; layout?.restore(); layout = undefined })

async function wheelUp() {
  const region = screen.getByRole('region', { name: 'Conversation history' })
  await act(async () => { fireEvent.wheel(region, { deltaY: -120 }) })
  return region
}

describe('reader intent and overflow (arch A)', () => {
  it('an upward wheel on a conversation that fits leaves follow-latest on and shows no Return arrow', async () => {
    app = await openHistory(page([entry(1), entry(2), entry(3)], { sessionId: MAIN_ID }))
    const region = await wheelUp()
    expect(region.clientHeight, 'positive control: a real 800px viewport').toBe(800)
    expect(region.scrollHeight, 'positive control: the three rows fit inside the viewport').toBeLessThanOrEqual(region.clientHeight)
    expect(useChatStore.getState().history.viewMode, 'nothing to scroll away from: still following latest').toBe('latest')
    expect(screen.queryByRole('button', { name: 'Return to latest' }), 'no arrow with nothing to return to').toBeNull()
  })

  it('CONTROL: the same upward wheel on an overflowing conversation enters history reading', async () => {
    app = await openHistory(page(Array.from({ length: 40 }, (_, i) => entry(81 + i)), { sessionId: MAIN_ID, earlier: true }))
    const region = await wheelUp()
    expect(region.clientHeight, 'positive control: a real 800px viewport').toBe(800)
    expect(region.scrollHeight, 'positive control: forty rows overflow the viewport').toBeGreaterThan(region.clientHeight)
    expect(useChatStore.getState().history.viewMode, 'real reader intent with somewhere to go').toBe('historical')
  })
})
