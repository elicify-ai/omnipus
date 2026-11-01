// mapEffort.ts — the effort mapping table (effort-levels-spec-r10.md,
// "Mapping table (DE-5)", dataset D2, FR-046, FR-047, FR-058).
//
// TypeScript twin of pkg/agent/reasoning_effort_map.go::MapEffort. Both are
// pinned by the one shared fixture src/lib/effort/fixtures/effort-mapping-d2.json
// (SC-002: identical results for every row).
//
// Rules, in order:
//   - Not set ("") and Provider default are never changed.
//   - A target that does not reason clears the value (Not set), no note.
//   - Off ("off"/"none"): `none` if offered, else the switch's `off`, else
//     blocked (levels: pick a level, nearest marked; nothing sendable: "Let the
//     provider decide" or Cancel).
//   - On: the switch's `on`, else the level nearest rank 3.
//   - A ranked level: exact, else nearest; switch only → `on`.
//   - An unranked name: exact only, else Provider default with a note (never On).
//   - Empty candidate: an On or ranked source whose target offers no eligible
//     destination (only `none` or only unranked names) → Provider default with
//     the mapping explanation. A non-Off value never maps into `none`.
// "Nearest" ties go to the lower level.

export interface EffortTarget {
  /** Served level names (already filtered by the encoder table); may include "none". */
  levels: string[]
  /** Served switch capability. */
  switch: boolean
  /** A served reasoning object is present (the model reasons). */
  reasons: boolean
}

export type EffortNoteKind =
  | 'mapped'
  | 'mapped_on'
  | 'switch_only'
  | 'nothing_sendable'
  | 'empty_candidate'
  | 'unranked_default'
  | 'not_supported'

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
  noteKind: null | EffortNoteKind
}

/** DE-5 rank order. `none` ranks 0 but is Off and never a non-Off destination. */
const RANK: Readonly<Record<string, number>> = {
  none: 0,
  minimal: 1,
  low: 2,
  medium: 3,
  high: 4,
  xhigh: 5,
  max: 6,
}

const ON_RANK = RANK.medium

export const PROVIDER_DEFAULT = 'provider_default'

function isOff(value: string): boolean {
  return value === 'off' || value === 'none'
}

function rankOf(level: string): number | undefined {
  return Object.prototype.hasOwnProperty.call(RANK, level) ? RANK[level] : undefined
}

/** The offered ranked level nearest `rank`, excluding `none`; ties go lower; "" when none qualifies. */
function nearestLevel(levels: readonly string[], rank: number): string {
  let best = ''
  let bestDistance = Number.POSITIVE_INFINITY
  let bestRank = Number.POSITIVE_INFINITY
  for (const level of levels) {
    const r = rankOf(level)
    if (r === undefined || level === 'none') continue
    const distance = Math.abs(r - rank)
    if (distance < bestDistance || (distance === bestDistance && r < bestRank)) {
      best = level
      bestDistance = distance
      bestRank = r
    }
  }
  return best
}

function result(value: string, noteKind: EffortMapping['noteKind'] = null): EffortMapping {
  return { value, blocked: false, nearest: '', providerDefaultEscape: false, noteKind }
}

function mapOff(target: EffortTarget, nothingSendable: boolean): EffortMapping {
  if (target.levels.includes('none')) return result('none')
  if (target.switch) return result('off')
  if (nothingSendable) {
    return { value: '', blocked: true, nearest: '', providerDefaultEscape: true, noteKind: 'nothing_sendable' }
  }
  return { value: '', blocked: true, nearest: nearestLevel(target.levels, 0), providerDefaultEscape: false, noteKind: null }
}

/** Applies the mapping table (DE-5) to one source value and one served target. */
export function mapEffort(source: string, target: EffortTarget): EffortMapping {
  if (source === '' || source === PROVIDER_DEFAULT) return result(source)
  if (!target.reasons) return result('')

  const hasLevels = target.levels.length > 0
  const nothingSendable = !hasLevels && !target.switch

  if (isOff(source)) return mapOff(target, nothingSendable)
  if (nothingSendable) return result(PROVIDER_DEFAULT, 'nothing_sendable')

  if (source === 'on') {
    if (target.switch) return result('on')
    const nearest = nearestLevel(target.levels, ON_RANK)
    return nearest === '' ? result(PROVIDER_DEFAULT, 'empty_candidate') : result(nearest, 'mapped_on')
  }

  const rank = rankOf(source)
  if (rank === undefined) {
    // Unranked name: exact only, never On.
    if (target.levels.includes(source)) return result(source)
    return result(PROVIDER_DEFAULT, 'unranked_default')
  }

  if (target.levels.includes(source)) return result(source)
  const nearest = nearestLevel(target.levels, rank)
  if (nearest !== '') return result(nearest, 'mapped')
  if (target.switch) return result('on', 'switch_only')
  return result(PROVIDER_DEFAULT, 'empty_candidate')
}

/** Display label for a stored effort value ("none" reads Off, "xhigh" XHigh). */
export function effortLabel(value: string): string {
  switch (value) {
    case 'none':
    case 'off':
      return 'Off'
    case 'on':
      return 'On'
    case PROVIDER_DEFAULT:
      return 'Provider default'
    case 'xhigh':
      return 'XHigh'
    case '':
      return 'Not set'
    default:
      return value.charAt(0).toUpperCase() + value.slice(1)
  }
}

function requireValue(kind: EffortNoteKind, value: string | undefined): string {
  if (value === undefined || value === '') {
    throw new Error(`effort note "${kind}" needs the mapped value it reports`)
  }
  return value
}

/**
 * Renders the picker switch note (S-09 exact strings). `value` is the mapped
 * value the switch stored; the `mapped` and `mapped_on` texts name it, so it
 * is required for those kinds.
 */
export function effortSwitchNote(kind: EffortNoteKind, model: string, source: string, value?: string): string {
  switch (kind) {
    case 'mapped':
      return `Effort set to ${effortLabel(requireValue(kind, value))}. ${model} doesn't offer ${effortLabel(source)}.`
    case 'mapped_on':
      return `Effort set to ${effortLabel(requireValue(kind, value))}.`
    case 'switch_only':
      return `Effort set to On. ${model} only turns reasoning on or off.`
    case 'nothing_sendable':
      return `Effort set to Provider default. Omnipus can't change effort for ${model}.`
    case 'unranked_default':
      return `Effort set to Provider default. ${model} doesn't offer that setting.`
    case 'empty_candidate':
      return `Effort set to Provider default. ${model} offers no level here that ${effortLabel(source)} maps to.`
    case 'not_supported':
      return `${model} can't turn reasoning off.`
  }
}

/**
 * Renders the per-turn note ("Sent as X: …" / "Effort not sent: …") for a
 * note kind. `sent` is the value in the final body; the `mapped` text names it.
 */
export function effortTurnNote(kind: EffortNoteKind, model: string, source: string, sent?: string): string {
  switch (kind) {
    case 'mapped':
    case 'mapped_on':
    case 'switch_only':
      return `Sent as ${effortLabel(requireValue(kind, sent))}: ${model} doesn't offer ${effortLabel(source)}.`
    case 'not_supported':
      return `Effort not sent: ${model} can't turn reasoning off.`
    case 'empty_candidate':
      return `Effort not sent: ${model} offers no level here that ${source} maps to.`
    case 'nothing_sendable':
    case 'unranked_default':
      return `Effort not sent: Omnipus can't change effort for ${model}.`
  }
}
