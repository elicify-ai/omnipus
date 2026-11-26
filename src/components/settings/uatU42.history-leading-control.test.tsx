// FR-014 router-owned control, native jsdom traversal, no artificial delay.
// The original zero-leading-entry control file remains unchanged.
import { afterEach, beforeEach, it } from 'vitest'
import { browserEdgesU2 } from '../../../tests/helpers/settings-u2-navigation'
import { cleanupHistoryU42, leadingControlU42 } from '../../../tests/helpers/settings-u42-history'
beforeEach(browserEdgesU2)
afterEach(cleanupHistoryU42)
it('U4-C1 control — Limits before blocked About makes an extra Back visible; ordinary Stay keeps Gateway', leadingControlU42)
