// U5-C5 / U6-8 REL2, independent CHECK Round U5. Both entries carry real router state.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, within } from '@testing-library/react'
import { browserEdgesU2 } from '../../../tests/helpers/settings-u2-navigation'
import { cleanupHistoryU44, historyU44, moveU44, stayU44, stayedU44 } from '../../../tests/helpers/settings-u44-history'

beforeEach(browserEdgesU2)
afterEach(cleanupHistoryU44)

it.each([true, false])('U5-C5 REL2 — router Gateway, Back/Stay then go(-2)/Stay keeps the original entry and draft (Navigation API %s)', async (withNavigation) => {
  const context = await historyU44('router', withNavigation)
  context.edge.delayGo(30) // Distinct serialized browser tasks, not a performance threshold.
  try {
    await stayU44(context, await moveU44(context, -1, true))
    stayedU44(context, 1, 'REL2 first Stay')
    await stayU44(context, await moveU44(context, -2))
    stayedU44(context, 2, 'REL2 second Stay')
  } finally { context.stop() }
})

// Part-46 move 2 supplies the observation bounds; no production timeout is
// imported or inferred. Only the clock and browser traversal edge are scripted.
async function safetyU46(restoreDelay: number | null, openAt: number, closedBy: number) {
  const context = await historyU44('router', true)
  const prompt = await moveU44(context, -1, true)
  const entries = context.edge.navigation.entries()
  expect(entries.map((entry) => new URL(entry.url).hash), 'instrument: Limits precedes the blocked About entry')
    .toEqual(['#/settings?tab=limits', '#/settings?tab=about', '#/settings?tab=gateway'])
  expect(Reflect.get(window, 'navigation').currentEntry?.key, 'instrument: live Navigation API key before Stay').toBe(entries[1].key)
  expect(Reflect.get(window, 'navigation').entries().map((entry: { key: string }) => entry.key), 'instrument: scripted Navigation API entries')
    .toEqual(entries.map((entry) => entry.key))
  const blockedAddress = window.location.href
  const go = vi.spyOn(window.history, 'go')
  if (restoreDelay === null) go.mockImplementation(() => {}) // browser undo is lost, never a unit-under-test mock
  else context.edge.delayGo(restoreDelay)
  const back = vi.spyOn(window.history, 'back')
  const forward = vi.spyOn(window.history, 'forward')
  const push = vi.spyOn(window.history, 'pushState')
  const replace = vi.spyOn(window.history, 'replaceState')
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] })
  try {
    await act(async () => { fireEvent.click(within(prompt).getByRole('button', { name: 'Stay' })) })
    await act(async () => { await vi.advanceTimersByTimeAsync(openAt) })
    expect.soft(screen.queryAllByRole('alertdialog'), `F-A: question stays open at ${openAt}ms while the undo is outstanding`).toEqual([prompt])
    expect.soft(window.location.href, 'instrument: address is still the blocked entry before the delayed/missing undo').toBe(blockedAddress)
    await act(async () => { await vi.advanceTimersByTimeAsync(closedBy - openAt) })
    expect.soft(screen.queryAllByRole('alertdialog'), `F-A: question is gone by ${closedBy}ms`).toEqual([])
    expect.soft(go.mock.calls, 'F-A: exactly the router undo (+1), never another go by the app').toEqual([[1]])
    expect.soft(back.mock.calls, 'F-A: the app takes no Back of its own').toEqual([])
    expect.soft(forward.mock.calls, 'F-A: the app takes no Forward of its own').toEqual([])
    expect.soft(push.mock.calls, 'F-A: the app adds no history entry').toEqual([])
    expect.soft(replace.mock.calls, 'F-A: the app does not replace the address to hide a failed undo').toEqual([])
    expect.soft(context.seen.size, 'F-A: no transient second question').toBe(1)
    expect.soft(window.history.length, 'F-A: history length is preserved').toBe(context.home.length)
    expect.soft(screen.getByRole('spinbutton', { name: 'Port' }), 'F-A: draft is retained').toHaveValue(8081)
    expect(context.t.writes(), 'F-A: Stay never persists the draft').toEqual([])
    if (restoreDelay !== null) stayedU44(context, 1, 'F-A undo 3s late')
    else expect.soft(window.location.href, 'instrument: a missing undo supplies no address restoration').toBe(blockedAddress)
  } finally {
    context.stop()
    // Drain browser-edge tasks even after a failed assertion, before restoring
    // real timers and the edge; this is teardown, not an acceptance observation.
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    vi.useRealTimers()
  }
}

it('U6 F-A — a missing exact undo keeps the question at 3s, closes it by 10s and adds no history move', () => safetyU46(null, 3000, 10_000))
it('U6 F-A — an exact undo 3s late keeps the question at 2.5s, then restores the address and closes by 3.2s', () => safetyU46(3000, 2500, 3200))
