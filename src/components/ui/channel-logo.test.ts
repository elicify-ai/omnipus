// channel-logo.test.ts — drift guard for the channel base-type -> <BrandIcon>
// logoSlug mapping (A8, 7-reviewer fix wave on the Channels redesign).
//
// Two independent checks:
//   1. VENDORED_CHANNEL_SLUGS must match the actual c_*.svg files vendored
//      under src/assets/brand-logos/ — catches drift in either direction
//      (a new asset added without updating the list, or a stale slug left
//      in the list after an asset was removed/renamed).
//   2. Every base type channelSlug() is expected to handle (the vendored
//      set, the remapped inputs, and the documented lettermark-only set)
//      resolves to either a real vendored mark or a documented lettermark
//      type — never an undocumented dangling slug.

import { describe, it, expect } from 'vitest'
import { channelSlug, VENDORED_CHANNEL_SLUGS, LETTERMARK_ONLY_CHANNEL_TYPES } from './channel-logo'

// Mirrors the glob BrandIcon uses internally (src/components/ui/brand-icon.tsx)
// but kept independent here so a mismatch proves real drift rather than a
// shared fixture masking it.
const channelSvgModules = import.meta.glob('../../assets/brand-logos/c_*.svg', {
  query: '?raw',
  eager: true,
})

function vendoredSlugsOnDisk(): string[] {
  return Object.keys(channelSvgModules)
    .map((path) => path.split('/').pop() ?? '')
    .map((filename) => filename.replace(/^c_/, '').replace(/\.svg$/, ''))
    .filter(Boolean)
    .sort()
}

describe('channelSlug — vendored-asset drift guard', () => {
  it('VENDORED_CHANNEL_SLUGS matches the actual c_*.svg files on disk', () => {
    expect([...VENDORED_CHANNEL_SLUGS].sort()).toEqual(vendoredSlugsOnDisk())
  })

  it('every mappable channelSlug() output resolves to a vendored mark or a documented lettermark type', () => {
    const onDisk = new Set(vendoredSlugsOnDisk())
    const documented: Set<string> = new Set(LETTERMARK_ONLY_CHANNEL_TYPES)
    const inputs = [
      ...VENDORED_CHANNEL_SLUGS,
      ...LETTERMARK_ONLY_CHANNEL_TYPES,
      // Base types that remap to a vendored slug (see channelSlug()'s switch).
      'google-chat',
      'weixin',
      'qq',
    ]
    for (const baseType of inputs) {
      const slug = channelSlug(baseType)
      expect(onDisk.has(slug) || documented.has(slug)).toBe(true)
    }
  })
})
