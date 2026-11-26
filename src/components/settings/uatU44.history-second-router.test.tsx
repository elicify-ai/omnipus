// U5-C5 / U6-8 REL2, independent CHECK Round U5. Both entries carry real router state.
import { afterEach, beforeEach, it } from 'vitest'
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
