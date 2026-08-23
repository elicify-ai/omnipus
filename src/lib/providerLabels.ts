// providerLabels.ts — shared human-readable labels for the provider catalog's
// plan/region dimensions (provider-ux-fixes-plan.md FIX-4). Single source so
// onboarding (ModelKeyStep) and Settings (ProvidersSection) never drift —
// both surfaces import from here instead of maintaining parallel maps.
//
// The registry-fed catalog (ADR-067 schema 2.0.0, GET /providers/catalog)
// carries `plan` / `region` as free strings on CatalogProvider, so the maps
// are keyed by string and callers fall back to the raw value when a key is
// not listed here (`planLabel` / `regionLabel`).

/** The plan a CatalogProvider without an explicit `plan` is treated as. */
export const DEFAULT_PLAN = 'standard-api'

/** Human labels for the plan dimension (catalog convention, FIX-4). */
export const PLAN_LABELS: Record<string, string> = {
  'standard-api': 'Pay-as-you-go API',
  'coding-plan': 'Coding Plan',
}

/** Human labels for the region dimension. */
export const REGION_LABELS: Record<string, string> = {
  intl: 'International',
  china: 'China',
  us: 'US',
}

export function planLabel(plan: string | undefined): string {
  const key = plan ?? DEFAULT_PLAN
  return PLAN_LABELS[key] ?? key
}

export function regionLabel(region: string | undefined): string {
  if (!region) return ''
  return REGION_LABELS[region] ?? region
}
