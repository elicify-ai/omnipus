// U4-1 / T4-1, FR-014: H7b7 realistic native state-less entries; no production swap.
import { afterEach, beforeEach, it } from 'vitest'
import { browserEdgesU2, cleanupNavigationU2 } from '../../../tests/helpers/settings-u2-navigation'
import { olderTypedBackStayU4 } from '../../../tests/helpers/settings-u4-history-cases'
beforeEach(browserEdgesU2)
afterEach(cleanupNavigationU2)
it('U4-1 / T4-1 — H7b7 Back onto an address typed while Settings was unmounted, then Stay asks only once', olderTypedBackStayU4)
