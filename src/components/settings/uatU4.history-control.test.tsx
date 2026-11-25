// FR-014 / T4-1 control, in a fresh application lifetime.
import { afterEach, beforeEach, it } from 'vitest'
import { browserEdgesU2, cleanupNavigationU2 } from '../../../tests/helpers/settings-u2-navigation'
import { routerOwnedStayU4 } from '../../../tests/helpers/settings-u4-history-cases'
beforeEach(browserEdgesU2)
afterEach(cleanupNavigationU2)
it('T4-1 control — router-owned Back then Stay keeps Gateway, its draft and its active entry', routerOwnedStayU4)
