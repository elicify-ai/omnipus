/**
 * SessionBar.token.test.tsx — trimmed stub.
 *
 * The SessionBar render tests and the formatTokens unit tests have been
 * consolidated into ChatControls.test.tsx (the component that now owns the
 * token counter, model selector, agent picker, New Chat, and Sessions
 * controls). This file is kept as a no-op to avoid orphaned import errors;
 * all meaningful coverage is in ChatControls.test.tsx.
 *
 * formatTokens unit tests also exist in src/lib/formatTokens.test.ts.
 */

import { describe, it, expect } from 'vitest'
import { formatTokens } from './SessionBar'

// ── formatTokens re-export sanity check ──────────────────────────────────────
// Confirms the SessionBar stub correctly re-exports formatTokens from
// @/lib/formatTokens so any remaining import sites still compile.

describe('formatTokens (via SessionBar stub re-export)', () => {
  it('formats 0 as "0"', () => {
    expect(formatTokens(0)).toBe('0')
  })
  it('formats 44000 as "44.0k"', () => {
    expect(formatTokens(44000)).toBe('44.0k')
  })
  it('formats 1200000 as "1.2M"', () => {
    expect(formatTokens(1_200_000)).toBe('1.2M')
  })
})
