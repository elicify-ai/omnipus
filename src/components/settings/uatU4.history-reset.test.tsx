// FR-014 / T4-2: later cleanly shown typed entry after an earlier Stay. Fresh page.
import { afterEach, beforeEach, it } from 'vitest'
import { browserEdgesU2, cleanupNavigationU2 } from '../../../tests/helpers/settings-u2-navigation'
import { afterStayRecordingU4 } from '../../../tests/helpers/settings-u4-history-cases'
beforeEach(browserEdgesU2)
afterEach(cleanupNavigationU2)
it('T4-2 — after a typed-address Stay, a later cleanly shown typed entry is safe to Back to and Stay once (M9h)', afterStayRecordingU4)
