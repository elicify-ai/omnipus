import { expect } from '@playwright/test'
import { test as canonicalTest } from '../canonical-history'
import { startMintProvider } from './mint-provider'

/**
 * The real owned canonical-history gateway (a private gateway per test, the
 * fixture S-8.1's transcript seeding requires) with only its model edge
 * scripted. No row of the preview-isolation shard needs a live model.
 */
export const test = canonicalTest.extend<{ mintProvider: Awaited<ReturnType<typeof startMintProvider>> }>({
  mintProvider: async ({ browserName }, use) => {
    void browserName
    const provider = await startMintProvider()
    try {
      await use(provider)
    } finally {
      await provider.close()
      expect(provider.failures(), 'the scripted model edge saw no unexpected request').toEqual([])
    }
  },
  canonicalProviderBase: async ({ mintProvider }, use) => {
    await use(mintProvider.url)
  },
  // Mode 1 mints only with a public URL (ADR-094 FR-001/FR-011); a loopback one
  // is what the spec's E2E wiring note requires of this shard's gateway.
  canonicalLoopbackPublicURL: true,
})

export { expect }
