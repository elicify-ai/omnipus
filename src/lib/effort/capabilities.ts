// capabilities.ts — served-capability → presentation classification for the
// effort control (effort-levels-spec-r10.md, "Presentation by capability",
// dataset D3, FR-052).
//
// The input is the SERVED catalog `reasoning` object: the server has already
// removed the levels or switch the route cannot encode (FR-049), so a present
// object with nothing left means "reasons, nothing Omnipus can send".

export interface ServedReasoning {
  /** Served level names, ascending as published; may include "none". */
  levels?: string[] | null
  /** Served switch capability. */
  switch?: boolean
}

export type EffortPresentation = 'chip' | 'muted' | 'none'

/**
 * Classifies the composer presentation for a model from its served catalog
 * reasoning object:
 *   'chip'  — levels and/or switch present → Effort chip + radio popover
 *   'muted' — object present, nothing sendable → muted chip with info icon
 *   'none'  — no object (or no catalog row: custom/local) → no chip
 */
export function classifyEffortPresentation(served: ServedReasoning | undefined | null): EffortPresentation {
  if (served === undefined || served === null) return 'none'
  const hasLevels = Array.isArray(served.levels) && served.levels.length > 0
  if (hasLevels || served.switch === true) return 'chip'
  return 'muted'
}
