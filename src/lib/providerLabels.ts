// providerLabels.ts — shared human-readable labels for the provider catalog's
// plan/region dimensions (provider-ux-fixes-plan.md FIX-4). Single source so
// onboarding (ModelKeyStep) and Settings (ProvidersSection) never drift —
// both surfaces import from here instead of maintaining parallel maps.

import type { ProviderCatalogEntry } from '@/lib/api/generated/openapi-types'

/** Human labels for the plan dimension (catalog convention, FIX-4). */
export const PLAN_LABELS: Record<ProviderCatalogEntry['plan'], string> = {
  'standard-api': 'Pay-as-you-go API',
  'coding-plan': 'Coding Plan',
}

/** Human labels for the region dimension. */
export const REGION_LABELS: Record<NonNullable<ProviderCatalogEntry['region']>, string> = {
  intl: 'International',
  china: 'China',
  us: 'US',
}
