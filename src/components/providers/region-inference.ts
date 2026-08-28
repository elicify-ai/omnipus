// region-inference.ts — ADR-068 FR-027 / §5 item 3: which deployment region the
// second-level picker panel pre-selects, and the copy that says so.
//
// A Chinese vendor's four plan × region variants must not present as four equal
// buttons with nothing chosen; the browser locale is a good enough first guess,
// and the copy always says it WAS a guess ("Detected: China — change") so the
// operator knows it is theirs to override.
//
// Pure functions only (T068-19 DoD) — no React, no `navigator` access. The
// caller passes `navigator.language`; that keeps this testable without a DOM
// and keeps the locale a prop the picker can override.

/** Human copy for the region codes the catalog uses. */
export const REGION_LABELS: Readonly<Record<string, string>> = Object.freeze({
  intl: 'International',
  china: 'China',
  us: 'US',
  eu: 'EU',
})

/** The region every unmatched locale falls back to. */
export const DEFAULT_REGION = 'intl'

/**
 * Display label for a region code. Known codes come from REGION_LABELS;
 * anything else is title-cased so an unrecognised catalog region still reads
 * as a word rather than as a raw key.
 */
export function regionLabel(region: string): string {
  const known = REGION_LABELS[region.toLowerCase()]
  if (known) return known
  if (region.length === 0) return ''
  return region.charAt(0).toUpperCase() + region.slice(1)
}

export interface RegionInference {
  /** The region to pre-select. Empty string only when `regions` is empty. */
  region: string
  /** True when the locale actually decided it; false means "we defaulted". */
  inferred: boolean
  /** FR-027 copy: "Detected: <Region> — change", or "Region — change". */
  copy: string
}

/**
 * The FR-027 inference map, in the spec's own words:
 *   `zh-CN` / `zh-SG` -> china; any other `zh-*` -> intl (Taiwan and Hong Kong
 *   reach a different endpoint and legal entity for some vendors, MIN-003);
 *   `en-US` -> us when the provider offers a US region; everything else -> intl.
 *
 * A locale that is missing or blank infers nothing: the default region is still
 * pre-selected (so the panel is never in a no-selection state) but the copy
 * drops the "Detected:" claim.
 *
 * When the preferred region is not among `regions`, the fall-back order is
 * `intl`, then the first offered region — a provider with an unusual region set
 * still gets a selection rather than none.
 */
export function inferRegionFromLocale(
  locale: string | null | undefined,
  regions: readonly string[],
): RegionInference {
  const offered = regions.filter((r) => r.length > 0)
  if (offered.length === 0) {
    return { region: '', inferred: false, copy: 'Region — change' }
  }

  const normalized = (locale ?? '').trim().toLowerCase().replace(/_/g, '-')
  const resolve = (preferred: string): string => {
    if (offered.includes(preferred)) return preferred
    if (offered.includes(DEFAULT_REGION)) return DEFAULT_REGION
    return offered[0]
  }

  if (normalized.length === 0) {
    return {
      region: resolve(DEFAULT_REGION),
      inferred: false,
      copy: 'Region — change',
    }
  }

  const preferred = preferredRegionForLocale(normalized)
  const region = resolve(preferred)
  return {
    region,
    inferred: true,
    copy: `Detected: ${regionLabel(region)} — change`,
  }
}

/** The locale -> region half of FR-027, before the offered-regions filter. */
export function preferredRegionForLocale(locale: string): string {
  const parts = locale.trim().toLowerCase().replace(/_/g, '-').split('-')
  const language = parts[0] ?? ''
  // BCP-47 puts an optional 4-letter script subtag before the 2-letter region
  // (zh-Hant-TW), so pick the first two-letter subtag after the language.
  const territory = parts.slice(1).find((part) => /^[a-z]{2}$/.test(part)) ?? ''
  if (language === 'zh' && (territory === 'cn' || territory === 'sg')) return 'china'
  if (language === 'en' && territory === 'us') return 'us'
  return DEFAULT_REGION
}
