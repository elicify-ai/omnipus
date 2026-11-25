// FR-014 / T4-2: already-shown typed address + real native length growth.
import { afterEach, beforeEach, it } from 'vitest'
import { browserEdgesU2, cleanupNavigationU2 } from '../../../tests/helpers/settings-u2-navigation'
import { shownAddressRetypeStayU4 } from '../../../tests/helpers/settings-u4-history-cases'
beforeEach(browserEdgesU2)
afterEach(cleanupNavigationU2)
it('T4-2 — H6 retyping an already shown Settings address with a draft restores Gateway even when the address was recorded', shownAddressRetypeStayU4)
