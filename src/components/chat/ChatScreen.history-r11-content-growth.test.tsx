// Q-T5 / R11 - COVERAGE (a regression guard, not a defect report): content
// growth with an UNCHANGED scrollTop alone must not end follow-latest.
//
// Oracle (D8's actual-displacement clarification, spec:508): "While
// following latest, a scroll displacement that leaves the conversation
// viewport away from the live end MUST enter historical reading ...
// content growth with unchanged scrollTop alone MUST NOT end follow-latest."
// Growth is not a displacement: the reader did not move, so the live end is
// still where the reader is following from.
//
// This case PASSES on 75d48f041 - it is the guard the #1452 manual-bottom
// fix (Q-T4) must not break, exactly as the founder's F-BUG ruling requires.
// It is proven non-vacuous by a mutation probe: making onScroll treat a
// grown bottom gap as movement away from the live end makes it fail
// ("Q2: a larger computed bottom gap is not movement away from latest:
// expected 'historical' to be 'latest'").
//
// Real viewport/renderer/store; deterministic row geometry is browser input.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, fireEvent, screen } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import { resetScrollCase, stopScrollCase, settleScrollLayout, mountRetained, loadAndRender, establishLatest, MAIN_ID } from '../../../tests/chat-history-paging/scroll-regression-harness'
let env: ReturnType<typeof resetScrollCase>
beforeEach(() => { env = resetScrollCase() })
afterEach(async () => { await stopScrollCase(env.layout); expect(env.network.unexpected).toStrictEqual([]) })
it('R11: real row growth and a scroll notification without displacement preserve follow-latest', async () => {
  const { selected } = await mountRetained(40, false)
  const region = await loadAndRender(env.network, selected)
  await establishLatest(region, env.layout)
  expect(useChatStore.getState().history.viewMode, 'actual initial follow-latest input').toBe('latest')
  expect(screen.queryByRole('button', { name: 'Return to latest' })).toBeNull()
  const beforeTop = region.scrollTop, beforeHeight = region.scrollHeight
  const beforeReads = env.network.requests.filter(read => read.path === `/api/v1/sessions/${MAIN_ID}/messages`).length
  const lastPosition = selected.entries.at(-1)!.history_position!
  // A delayed image/font/layout may increase a real row's browser-measured
  // height without any reader movement or message-array replacement.
  env.layout.state.rowHeights.set(lastPosition, env.layout.state.rowHeight + 400)
  expect(region.scrollHeight, 'positive browser-edge control: content really grew').toBe(beforeHeight + 400)
  expect(region.scrollTop, 'the input includes NO reader displacement').toBe(beforeTop)
  await act(async () => { fireEvent.scroll(region) })
  expect(useChatStore.getState().history.viewMode, 'Q2: a larger computed bottom gap is not movement away from latest').toBe('latest')
  await settleScrollLayout()
  expect(useChatStore.getState().history.viewMode, 'actual layout/measurement settlement also keeps following').toBe('latest')
  expect(screen.queryByRole('button', { name: 'Return to latest' })).toBeNull()
  expect(env.network.requests.filter(read => read.path === `/api/v1/sessions/${MAIN_ID}/messages`)).toHaveLength(beforeReads)
})
