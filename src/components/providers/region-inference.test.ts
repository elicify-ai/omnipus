// TDD row 7 — TestRegionFromLocale (ADR-068 FR-027).
//
// Oracle: the "Region inferred from locale" scenario outline in
// docs/internal/specs/adr-068-providers-ux-spec.md, all nine rows verbatim,
// plus the §"Edge cases" sentence that names zh-TW / zh-HK explicitly.

import { describe, expect, it } from 'vitest'
import {
  DEFAULT_REGION,
  inferRegionFromLocale,
  preferredRegionForLocale,
  regionLabel,
} from './region-inference'

describe('TestRegionFromLocale', () => {
  // locale | regions | selected | copy — the spec's Examples table, unchanged.
  const outline: Array<[string, string[], string, string]> = [
    ['zh-CN', ['intl', 'china'], 'china', 'Detected: China — change'],
    ['zh-SG', ['intl', 'china'], 'china', 'Detected: China — change'],
    ['zh-TW', ['intl', 'china'], 'intl', 'Detected: International — change'],
    ['zh-HK', ['intl', 'china'], 'intl', 'Detected: International — change'],
    ['en-GB', ['intl', 'us'], 'intl', 'Detected: International — change'],
    ['en-US', ['intl', 'us'], 'us', 'Detected: US — change'],
    ['en-US', ['intl', 'china'], 'intl', 'Detected: International — change'],
    ['de-DE', ['intl', 'china'], 'intl', 'Detected: International — change'],
    ['', ['intl', 'china'], 'intl', 'Region — change'],
  ]

  it.each(outline)('locale %j over %j selects %j', (locale, regions, selected, copy) => {
    const result = inferRegionFromLocale(locale, regions)
    expect(result.region).toBe(selected)
    expect(result.copy).toBe(copy)
    expect(result.inferred).toBe(locale.length > 0)
  })

  it('treats a null or whitespace locale as "not inferred", not as a match', () => {
    for (const locale of [null, undefined, '   ']) {
      const result = inferRegionFromLocale(locale, ['intl', 'china'])
      expect(result).toEqual({ region: 'intl', inferred: false, copy: 'Region — change' })
    }
  })

  it('normalises case and underscore-separated locales', () => {
    expect(inferRegionFromLocale('ZH_CN', ['intl', 'china']).region).toBe('china')
    expect(inferRegionFromLocale('en_us', ['intl', 'us']).region).toBe('us')
  })

  it('reads the region subtag past a script subtag', () => {
    expect(preferredRegionForLocale('zh-Hans-CN')).toBe('china')
    expect(preferredRegionForLocale('zh-Hant-TW')).toBe(DEFAULT_REGION)
  })

  it('falls back to intl when the preferred region is not offered', () => {
    // en-US prefers "us"; a provider offering only intl + china still gets a
    // selection rather than an empty region group.
    expect(inferRegionFromLocale('en-US', ['intl', 'china']).region).toBe('intl')
    // zh-CN prefers "china"; a US-only provider falls all the way to the first
    // offered region.
    expect(inferRegionFromLocale('zh-CN', ['us']).region).toBe('us')
  })

  it('returns an empty region, uninferred, when the provider has no regions', () => {
    expect(inferRegionFromLocale('zh-CN', [])).toEqual({
      region: '',
      inferred: false,
      copy: 'Region — change',
    })
  })

  it('labels known regions from the map and title-cases anything else', () => {
    expect(regionLabel('intl')).toBe('International')
    expect(regionLabel('china')).toBe('China')
    expect(regionLabel('us')).toBe('US')
    expect(regionLabel('apac')).toBe('Apac')
    expect(regionLabel('')).toBe('')
  })
})
