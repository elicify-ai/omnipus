// FR-014 / T4-3: one history case per file gives this flow a fresh Settings module.
import { afterEach, beforeEach, it } from 'vitest'
import { browserEdgesU2, cleanupNavigationU2 } from '../../../tests/helpers/settings-u2-navigation'
import { typedStayU4 } from '../../../tests/helpers/settings-u4-history-cases'
beforeEach(browserEdgesU2)
afterEach(cleanupNavigationU2)
it('U2-4 — typed hash then Stay restores Gateway address and exact active history state without a rollback entry (uncapped)', () => typedStayU4('uncapped'))
