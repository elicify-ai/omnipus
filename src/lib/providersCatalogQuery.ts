// providersCatalogQuery.ts — the single TanStack Query policy for the
// registry-fed providers catalog (ADR-068 FR-037, ADR-067 A-1).
//
// A-1's cadence is "re-validate with If-None-Match on Settings open and every
// 15 min". Both halves live here so the two mount points (Settings →
// ProvidersSection and onboarding step 3) cannot drift apart:
//
//   • "on Settings open" → `staleTime: 0` + `refetchOnMount: 'always'`, so
//     every mount of the section issues the conditional GET;
//   • "every 15 min"     → `refetchInterval: 15 * 60_000` while mounted.
//
// Both are cheap because `fetchProvidersCatalog` replays the strong ETag and a
// 304 resolves with the already-parsed document — FR-037's assertion is at
// most one 200 per ETag value, not at most one request.
//
// `refetchIntervalInBackground` stays false: a hidden tab re-validating on a
// timer buys nothing and the mount refetch covers the return.

import type { UseQueryOptions } from '@tanstack/react-query'
import { fetchProvidersCatalog } from './api'
import type { ProvidersCatalog } from './api'

/** Query key for the catalog document. Also the invalidation target. */
export const PROVIDERS_CATALOG_QUERY_KEY = ['providers', 'catalog'] as const

/** ADR-067 A-1 re-validation interval, in milliseconds. */
export const PROVIDERS_CATALOG_REFETCH_INTERVAL_MS = 15 * 60 * 1000

export function providersCatalogQueryOptions(): UseQueryOptions<
  ProvidersCatalog,
  Error,
  ProvidersCatalog,
  typeof PROVIDERS_CATALOG_QUERY_KEY
> {
  return {
    queryKey: PROVIDERS_CATALOG_QUERY_KEY,
    queryFn: fetchProvidersCatalog,
    staleTime: 0,
    refetchOnMount: 'always',
    refetchInterval: PROVIDERS_CATALOG_REFETCH_INTERVAL_MS,
    refetchIntervalInBackground: false,
    // The catalog is a large document that changes at most daily — keep it
    // resident so a remount re-validates (304) instead of re-downloading.
    gcTime: 24 * 60 * 60 * 1000,
  }
}
