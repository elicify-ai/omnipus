// U5-C5 / U6-8 REL1, independent CHECK Round U5. No interaction edits between Stays.
import { afterEach, beforeEach, it } from 'vitest'
import { browserEdgesU2 } from '../../../tests/helpers/settings-u2-navigation'
import { cleanupHistoryU44, historyU44, moveU44, stayU44, stayedU44 } from '../../../tests/helpers/settings-u44-history'

beforeEach(browserEdgesU2)
afterEach(cleanupHistoryU44)

it.each([true, false])('U5-C5 REL1 — typed Gateway, Back/Stay then go(-1)/Stay keeps the original entry and draft (Navigation API %s)', async (withNavigation) => {
  const context = await historyU44('typed', withNavigation)
  context.edge.delayGo(30) // Browser input: undo occurs in a separate task, never coalesced.
  try {
    await stayU44(context, await moveU44(context, -1, true))
    stayedU44(context, 1, 'REL1 first Stay')
    await stayU44(context, await moveU44(context, -1))
    stayedU44(context, 2, 'REL1 second Stay')
  } finally { context.stop() }
})
