// H-LATE2 / FR-014: slow native restore, with an entry before blocked About.
import { afterEach, beforeEach, it } from 'vitest'
import { browserEdgesU2 } from '../../../tests/helpers/settings-u2-navigation'
import { cleanupHistoryU42, serialStayU42 } from '../../../tests/helpers/settings-u42-history'
beforeEach(browserEdgesU2)
afterEach(cleanupHistoryU42)
it('U4-C1 H-LATE2 — Stay restores Gateway and asks once even when router undo arrives after the old wait', () => serialStayU42('back', false, 600))
