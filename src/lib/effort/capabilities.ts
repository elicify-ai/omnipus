// capabilities.ts — RED SCAFFOLDING for the effort-levels feature (train T7).
// Spec: effort-levels-spec-r10.md, "Presentation by capability", dataset D3,
// FR-052. GREEN replaces the stub body; the signature is the contract the
// tests (src/lib/effort/capabilities.test.ts) pin. The stub throws so every
// classification assertion fails until the real logic lands.

export interface ServedReasoning {
  /** Served level names, ascending as published; may include "none". */
  levels?: string[] | null
  /** Served switch capability. */
  switch?: boolean
}

export type EffortPresentation = 'chip' | 'muted' | 'none'

/**
 * Classifies the composer presentation for a model from its SERVED catalog
 * reasoning object (the server already removed capabilities the route cannot
 * encode):
 *   'chip'  — levels and/or switch present → Effort chip + radio popover
 *   'muted' — object present, nothing sendable (budget-only, no published
 *             control, or no encoder) → muted chip with info icon
 *   'none'  — no object (or no catalog row: custom/local) → no chip
 */
export function classifyEffortPresentation(served: ServedReasoning | undefined | null): EffortPresentation {
  throw new Error(
    'RED scaffolding: classifyEffortPresentation not implemented — required by effort-levels-spec-r10.md D3/FR-052',
  )
}
