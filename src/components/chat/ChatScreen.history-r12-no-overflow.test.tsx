// Q-T5 / R12 - COVERAGE (a regression guard, not a defect report): upward
// intent in a window that CANNOT scroll is not choosing historical reading.
//
// Oracles (D8's actual-displacement clarification, spec:508, and the direct
// R12 ruling): "content growth with unchanged scrollTop alone MUST NOT end
// follow-latest" and a displacement away from the live end is what ends it - a
// preceding wheel/key/touch event is not. An upward input in a window with
// nothing to scroll cannot be a displacement, so it must not enter historical
// reading, must not offer a Return control, and must not spend a request.
//
// This case PASSES on 75d48f041 - it is the guard the #1452 manual-bottom
// fix (Q-T4) must not break, exactly as the founder's F-BUG ruling requires.
// It is proven non-vacuous by a mutation probe: removing the overflow check
// from useHistoryViewport.ts::bindReaderIntent's `enter` makes all three
// input kinds fail ("expected 'historical' to be 'latest'").
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, fireEvent, screen } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import { resetScrollCase, stopScrollCase, settleScrollLayout, mountRetained, loadAndRender, establishLatest, MAIN_ID } from '../../../tests/chat-history-paging/scroll-regression-harness'
let env: ReturnType<typeof resetScrollCase>
beforeEach(() => { env = resetScrollCase() })
afterEach(async () => { await stopScrollCase(env.layout); expect(env.network.unexpected).toStrictEqual([]) })
it.each(['wheel', 'keyboard', 'touch'] as const)('R12: %s upward intent without overflow or displacement keeps latest and no Return arrow', async input => {
  const { selected } = await mountRetained(2, false)
  const region = await loadAndRender(env.network, selected)
  await establishLatest(region, env.layout)
  expect(region.scrollHeight, 'the retained window truly cannot overflow').toBe(region.clientHeight)
  expect(region.scrollTop).toBe(0)
  expect(useChatStore.getState().history.viewMode).toBe('latest')
  expect(screen.queryByRole('button', { name: 'Return to latest' })).toBeNull()
  const beforeReads = env.network.requests.filter(read => read.path === `/api/v1/sessions/${MAIN_ID}/messages`).length
  await act(async () => {
    if (input === 'wheel') fireEvent.wheel(region, { deltaY: -200 })
    else if (input === 'keyboard') fireEvent.keyDown(region, { key: 'ArrowUp' })
    else {
      fireEvent.touchStart(region, { touches: [{ clientY: 200 }] })
      fireEvent.touchMove(region, { touches: [{ clientY: 400 }] })
      fireEvent.touchEnd(region, { touches: [] })
    }
  })
  await settleScrollLayout()
  expect(region.scrollTop, 'no physical displacement was possible').toBe(0)
  expect(useChatStore.getState().history.viewMode, 'R12: intent alone in a non-overflowing window is not reading older history').toBe('latest')
  expect(screen.queryByRole('button', { name: 'Return to latest' }), 'no pointless Return control when the reader never moved away').toBeNull()
  expect(env.network.requests.filter(read => read.path === `/api/v1/sessions/${MAIN_ID}/messages`), 'no pointless latest fetch').toHaveLength(beforeReads)
})
