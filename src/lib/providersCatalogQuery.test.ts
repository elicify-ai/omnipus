// providersCatalogQuery.test.ts — ADR-068 T068-18 / FR-037.
//
// The A-1 cadence is a policy, not a call-site detail: both the Settings
// section and onboarding step 3 must re-validate on open and every 15 minutes.
// These assertions pin the policy so a well-meaning "staleTime: 5 minutes"
// tweak that silences the Settings-open re-validation fails here.

import { describe, it, expect } from 'vitest'
import {
  providersCatalogQueryOptions,
  PROVIDERS_CATALOG_QUERY_KEY,
  PROVIDERS_CATALOG_REFETCH_INTERVAL_MS,
} from './providersCatalogQuery'
import { fetchProvidersCatalog } from './api'

describe('providersCatalogQueryOptions', () => {
  it('re-validates on every mount (Settings open)', () => {
    const opts = providersCatalogQueryOptions()
    expect(opts.staleTime).toBe(0)
    expect(opts.refetchOnMount).toBe('always')
  })

  it('re-validates every 15 minutes, foreground only', () => {
    expect(PROVIDERS_CATALOG_REFETCH_INTERVAL_MS).toBe(15 * 60 * 1000)
    const opts = providersCatalogQueryOptions()
    expect(opts.refetchInterval).toBe(15 * 60 * 1000)
    expect(opts.refetchIntervalInBackground).toBe(false)
  })

  it('uses the shared key and the ETag-aware fetcher, never an ad-hoc one', () => {
    const opts = providersCatalogQueryOptions()
    expect(opts.queryKey).toEqual(PROVIDERS_CATALOG_QUERY_KEY)
    expect(PROVIDERS_CATALOG_QUERY_KEY).toEqual(['providers', 'catalog'])
    expect(opts.queryFn).toBe(fetchProvidersCatalog)
  })
})
