// mapEffort.ts — RED SCAFFOLDING for the effort-levels feature (train T7,
// branch landing/effort). Spec: effort-levels-spec-r10.md, "Mapping table
// (DE-5)", dataset D2, FR-046, FR-058.
//
// The RED tests (src/lib/effort/mapEffort.test.ts) pin this function against
// the shared fixture src/lib/effort/fixtures/effort-mapping-d2.json; the Go
// twin is pkg/agent/reasoning_effort_map.go::MapEffort. SC-002 requires both
// implementations to return identical results for all 27 fixture rows.
//
// GREEN replaces this stub with the real mapping table; the signature below
// is the contract the tests pin. The stub throws, so every mapping assertion
// fails until the real table lands — never a silent wrong value.

export interface EffortTarget {
  /** Served level names (already filtered by the encoder table); may include "none". */
  levels: string[]
  /** Served switch capability. */
  switch: boolean
  /** A served reasoning object is present (the model reasons). */
  reasons: boolean
}

export interface EffortMapping {
  /** Mapped stored value: a level, "on", "off", "provider_default", or "" (Not set / cleared). */
  value: string
  /** The switch must wait (picker blocked step) or be refused (REST, tools). */
  blocked: boolean
  /** Nearest level marked (not chosen) on the blocked step; "" otherwise. */
  nearest: string
  /** Blocked step offers "Let the provider decide" / Cancel (P-Q2 A). */
  providerDefaultEscape: boolean
  /** Stable note kind; null = no note. */
  noteKind:
    | null
    | 'mapped'
    | 'mapped_on'
    | 'switch_only'
    | 'nothing_sendable'
    | 'empty_candidate'
    | 'unranked_default'
    | 'not_supported'
}

/** Renders the picker switch note (S-09 exact strings) for a note kind. */
export function effortSwitchNote(
  kind: NonNullable<EffortMapping['noteKind']>,
  model: string,
  source: string,
): string {
  throw new Error(
    `RED scaffolding: effortSwitchNote(${kind}, ${model}, ${source}) not implemented — required by effort-levels-spec-r10.md FR-058`,
  )
}

/** Renders the per-turn note ("Sent as X: …" / "Effort not sent: …") for a note kind. */
export function effortTurnNote(
  kind: NonNullable<EffortMapping['noteKind']>,
  model: string,
  source: string,
): string {
  throw new Error(
    `RED scaffolding: effortTurnNote(${kind}, ${model}, ${source}) not implemented — required by effort-levels-spec-r10.md FR-058`,
  )
}

/** Applies the mapping table (DE-5) to one source value and one served target. */
export function mapEffort(source: string, target: EffortTarget): EffortMapping {
  throw new Error(
    `RED scaffolding: mapEffort(${JSON.stringify(source)}) not implemented — required by effort-levels-spec-r10.md DE-5/D2`,
  )
}
