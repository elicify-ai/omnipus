// FR-014 Forward/Stay. Navigation API answers derive from the live entry list.
import { afterEach, beforeEach, it } from 'vitest'
import { browserEdgesU2 } from '../../../tests/helpers/settings-u2-navigation'
import { cleanupHistoryU42, serialStayU42 } from '../../../tests/helpers/settings-u42-history'
beforeEach(browserEdgesU2)
afterEach(cleanupHistoryU42)
it('U4-C2 — Forward onto newest About then Stay keeps Gateway without an extra Back (truthful Navigation API)', () => serialStayU42('forward', true, 50))
