// Q-T4 — founder bug (2026-10-27): the floating "Return to latest" arrow stays
// visible after the user scrolls MANUALLY to the very bottom of the chat; it
// only disappears when the arrow is clicked.
//
// Expected (the founder's ruling, F-BUG): "reaching the bottom by scrolling
// (within the at-bottom threshold the viewport already uses) re-enters
// follow-latest and hides the arrow, exactly as clicking does; the arrow
// still appears only when content overflows and the user is above the bottom."
//
// The spec's own statement of the same behaviour — User Story 2 (spec:166):
// "A reader scrolls upward, continues toward newer history after eviction, or
// deliberately returns to the live end." And D8's "Return to latest
// pending/error/success" row (spec:517): success "commit[s] fresh latest
// page, merge[s] current tail/pending input, enter[s] follow-latest and
// scroll[s] to bottom" — so the arrow's disappearance and follow-latest
// resuming are one outcome, reached by scrolling as well as by clicking.
//
// The at-bottom threshold is the one the viewport ALREADY uses to decide it
// has left the live end (src/components/chat/useHistoryViewport.ts::
// onScroll): a viewport is away from the end when
// `scrollHeight - scrollTop - clientHeight > 1`. This test scrolls to the
// real bottom, which is inside that threshold.
//
// Real list/store/hook; only HTTP, socket and browser geometry are edges.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import {
  resetScrollCase, stopScrollCase, settleScrollLayout, mountRetained, loadAndRender,
  establishLatest,
} from '../../../tests/chat-history-paging/scroll-regression-harness'

let env: ReturnType<typeof resetScrollCase>
beforeEach(() => { env = resetScrollCase() })
afterEach(async () => {
  await stopScrollCase(env.layout)
  expect(env.network.unexpected, 'no undeclared physical HTTP reads').toStrictEqual([])
})

const arrow = () => screen.queryByRole('button', { name: 'Return to latest' })

// The reader's own scroll, as a person performs it: a wheel gesture and the
// resulting scroll position. The wheel is the input the product's reader
// intent listens for; the scroll position is what it acts on.
async function scrollBy(region: HTMLElement, deltaY: number, to: number) {
  await act(async () => {
    fireEvent.wheel(region, { deltaY })
    region.scrollTop = to
    fireEvent.scroll(region)
  })
}

// The 60s budget covers THIS case's real work, not a relaxed assertion: it
// renders a 40-row real list, drives a real upward scroll into history and a
// real downward scroll back to the live end, each re-rendering every row. It
// measures 17-21s locally and exceeded the 30s default under contention. No
// assertion, oracle or scenario was changed to fit.
it('Q-T4: scrolling manually to the very bottom re-enters follow-latest and hides the Return arrow', async () => {
  const { selected } = await mountRetained(40, false)
  const region = await loadAndRender(env.network, selected)
  await establishLatest(region, env.layout)
  expect(useChatStore.getState().history.viewMode, 'the reader really follows latest to begin with').toBe('latest')
  expect(region.scrollHeight, 'positive control: the conversation really overflows its viewport').toBeGreaterThan(region.clientHeight)
  expect(arrow(), 'no arrow while the reader is at the live end').toBeNull()

  // The reader scrolls UP into history: a real overflow, so historical
  // reading starts and the floating Return arrow appears.
  const upTo = region.scrollHeight - region.clientHeight - 400
  await scrollBy(region, -400, upTo)
  await waitFor(() => expect(useChatStore.getState().history.viewMode,
    'an upward scroll in an overflowing conversation enters historical reading').toBe('historical'))
  expect(arrow(), 'the arrow is offered while the reader is above the bottom').toBeVisible()

  // The reader scrolls back DOWN, by hand, to the very bottom of the
  // conversation. This is the founder's case: no arrow was clicked.
  const bottom = region.scrollHeight - region.clientHeight
  await scrollBy(region, 400, bottom)
  await settleScrollLayout()
  await waitFor(() => expect(arrow(),
    'reaching the live end by scrolling hides the Return arrow, exactly as clicking it does').toBeNull())

  expect(useChatStore.getState().history.viewMode,
    'reaching the live end by scrolling re-enters follow-latest').toBe('latest')
  expect(region.scrollHeight - region.scrollTop - region.clientHeight,
    'the reader is at the live end, inside the viewport\'s own at-bottom threshold').toBeLessThanOrEqual(1)
}, 60_000)

it('Q-T4: the Return arrow stays visible while the reader is above the bottom', async () => {
  const { selected } = await mountRetained(40, false)
  const region = await loadAndRender(env.network, selected)
  await establishLatest(region, env.layout)

  const upTo = region.scrollHeight - region.clientHeight - 400
  await scrollBy(region, -400, upTo)
  await waitFor(() => expect(useChatStore.getState().history.viewMode).toBe('historical'))

  // Scrolling back down, but stopping ABOVE the bottom: the reader is still
  // reading older history and still needs the way back.
  await scrollBy(region, 400, upTo + 200)
  await settleScrollLayout()
  expect(useChatStore.getState().history.viewMode, 'a reader above the bottom is still reading history').toBe('historical')
  expect(arrow(), 'the arrow remains while the reader is above the bottom').toBeVisible()
  expect(region.scrollHeight - region.scrollTop - region.clientHeight,
    'positive control: the reader really is above the bottom').toBeGreaterThan(1)
})

it('Q-T4: a conversation that does not overflow never shows the Return arrow', async () => {
  // Two rows in an 800px viewport: nothing to scroll, so there is nothing to
  // return from (the arrow's own precondition, D8/architect item A). This is
  // the regression guard for the manual-bottom fix itself: a window that is
  // trivially "at the bottom" must not gain a Return control.
  const { selected } = await mountRetained(2, false)
  const region = await loadAndRender(env.network, selected)
  await establishLatest(region, env.layout)
  expect(region.scrollHeight, 'positive control: the conversation fits its viewport').toBeLessThanOrEqual(region.clientHeight)
  await scrollBy(region, -400, 0)
  await settleScrollLayout()
  expect(useChatStore.getState().history.viewMode, 'nothing to scroll away from: still following latest').toBe('latest')
  expect(arrow(), 'no arrow with nothing to return to').toBeNull()
})
