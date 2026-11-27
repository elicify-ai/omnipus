// H-LATE2 / FR-014: slow native restore, with an entry before blocked About.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { browserEdgesU2 } from '../../../tests/helpers/settings-u2-navigation'
import { cleanupHistoryU42, serialStayU42 } from '../../../tests/helpers/settings-u42-history'
import { cleanupHistoryU44, historyU44, moveU44, stayU44, stayedU44 } from '../../../tests/helpers/settings-u44-history'
beforeEach(browserEdgesU2)
afterEach(cleanupHistoryU42)
it('U4-C1 H-LATE2 — Stay restores Gateway and asks once even when router undo arrives after the old wait', () => serialStayU42('back', false, 600))

// Part-46 move 3 / FR-014: 450ms is a scripted browser input, not a product
// deadline. Keep the original no-API 600ms case above unchanged.
it('U6 F-B — with Navigation API metadata, a 450ms undo asks once and takes no app-owned Back', async () => {
  const context = await historyU44('router', true)
  try {
    const prompt = await moveU44(context, -1, true)
    const entries = context.edge.navigation.entries()
    expect(entries.map((entry) => new URL(entry.url).hash), 'instrument: an entry before About makes an extra Back observable')
      .toEqual(['#/settings?tab=limits', '#/settings?tab=about', '#/settings?tab=gateway'])
    expect(Reflect.get(window, 'navigation').currentEntry?.key, 'instrument: API eagerly tracks the blocked entry').toBe(entries[1].key)
    expect(Reflect.get(window, 'navigation').entries().map((entry: { key: string }) => entry.key), 'instrument: a real scripted entry list, not canGoForward alone')
      .toEqual(entries.map((entry) => entry.key))
    context.edge.delayGo(450)
    const requestsBeforeStay = context.edge.requests.length
    await stayU44(context, prompt)
    stayedU44(context, 1, 'F-B API restore 450ms late')
    expect(context.edge.requests.slice(requestsBeforeStay), 'F-B: only the router undo; no app-owned Back, Forward or second go')
      .toEqual([{ kind: 'go', delta: 1, requestedAt: 1 }])
  } finally {
    context.stop()
    await cleanupHistoryU44()
  }
})
