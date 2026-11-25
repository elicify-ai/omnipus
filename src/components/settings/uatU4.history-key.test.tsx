// FR-014 / T4-2: one shown address is not every address; capped native history.
import { afterEach, beforeEach, it } from 'vitest'
import { browserEdgesU2, cleanupNavigationU2 } from '../../../tests/helpers/settings-u2-navigation'
import { distinctShownKeyU4 } from '../../../tests/helpers/settings-u4-history-cases'
beforeEach(browserEdgesU2)
afterEach(cleanupNavigationU2)
it('T4-2 — capped history distinguishes a shown Limits address from a new typed Data address on the same page path (M9m)', distinctShownKeyU4)
