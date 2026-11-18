// capabilities.ts — served-capability → presentation classification for the
// effort control (effort-levels-spec-r10.md, "Presentation by capability",
// dataset D3, FR-052).
//
// The input is the SERVED catalog `reasoning` object: the server has already
// removed the levels or switch the route cannot encode (FR-049), so a present
// object with nothing left means "reasons, nothing Omnipus can send".

import type { CatalogModelReasoning } from '@/lib/api/generated/openapi-types'

/** The served catalog `reasoning` object: the contract's CatalogModelReasoning. */
export type ServedReasoning = CatalogModelReasoning

export type EffortPresentation = 'chip' | 'muted' | 'none'

/**
 * Classifies the composer presentation for a model from its served catalog
 * reasoning object:
 *   'chip'  — levels and/or switch present → Effort chip + radio popover
 *   'muted' — object present, nothing sendable → muted chip with info icon
 *   'none'  — no object (or no catalog row: custom/local) → no chip
 */
// Read defensively: a member missing or null (off-contract data) counts as not offered.
export function classifyEffortPresentation(
  served: (Partial<Omit<ServedReasoning, 'levels'>> & { levels?: ServedReasoning['levels'] | null }) | undefined | null,
): EffortPresentation {
  if (served === undefined || served === null) return 'none'
  const hasLevels = Array.isArray(served.levels) && served.levels.length > 0
  if (hasLevels || served.switch === true) return 'chip'
  return 'muted'
}
