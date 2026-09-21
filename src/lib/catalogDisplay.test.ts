// catalogDisplay.test.ts — orchestrator review round 2, D2 / D2-addendum
// (issue #800).
//
// After Continue in onboarding, the confirmed-row summary card kept reading
// "Pay-as-you-go, per token · bedrock-runtime.us-east-1.amazonaws.com" even
// after the operator picked eu-central-1 — because catalogSubtitle /
// catalogEndpointHint / catalogLabel derive the host and region suffix from
// the catalog row's own STATIC default fields (CatalogProvider.api /
// .region), never from the operator's own selection. The same static values
// leaked into Settings → Providers afterward (D2-addendum): the provider
// card title ("Amazon Bedrock (us-east-1)"), its subtitle, and the Default
// model line all kept showing us-east-1 even once eu-central-1 was saved.
//
// These three functions gain an optional SECOND parameter — the operator's
// own configured/selected region — that wins over the static default
// whenever the row carries its own `regions` picker (CatalogProvider.regions,
// the Bedrock region contract) and the value names one of the offered
// regions. Every existing call site (no second argument) is unaffected —
// this is the regression this file pins FIRST.

import { describe, it, expect } from 'vitest'
import { catalogEndpointHint, catalogLabel, catalogSubtitle } from './catalogDisplay'
import type { CatalogProvider } from '@/lib/api/generated/openapi-types'

const BEDROCK_ENTRY: CatalogProvider = {
  id: 'amazon-bedrock',
  name: 'Amazon Bedrock',
  company: 'Amazon Bedrock',
  api: 'https://bedrock-runtime.us-east-1.amazonaws.com',
  protocol: 'bedrock',
  region: 'us-east-1',
  regions: [
    { id: 'us-east-1', group: 'us' },
    { id: 'eu-central-1', group: 'eu' },
    { id: 'ap-northeast-1', group: 'jp' },
    { id: 'us-gov-west-1', group: '' },
  ],
  tier: 'standard',
  auth_methods: ['api_key'],
  aliases: ['bedrock', 'aws'],
  locality: 'cloud',
  models: [],
}

// A row with NO regions picker at all — the un-prefixed default's-still-
// wins case (e.g. Anthropic, Z.ai's plan x region variants use `regions:
// string[]` on PickerCompanyRow, a completely different mechanism from this
// CatalogProvider.regions picker).
const ZAI_ENTRY: CatalogProvider = {
  id: 'zai',
  name: 'Z.ai',
  company: 'Z.ai',
  api: 'https://api.z.ai/api/paas/v4',
  protocol: 'openai-compatible',
  region: 'intl',
  tier: 'popular',
  auth_methods: ['api_key'],
  aliases: [],
  locality: 'cloud',
  models: [],
}

describe('catalogEndpointHint — configuredRegion (issue #800 D2)', () => {
  it('derives the host from the configured region when the row carries its own regions picker', () => {
    expect(catalogEndpointHint(BEDROCK_ENTRY, 'eu-central-1')).toBe(
      'bedrock-runtime.eu-central-1.amazonaws.com',
    )
  })

  it('falls back to the static default host when configuredRegion is omitted (no regression)', () => {
    expect(catalogEndpointHint(BEDROCK_ENTRY)).toBe('bedrock-runtime.us-east-1.amazonaws.com')
  })

  it('falls back to the static default host when configuredRegion names a region the row does not offer', () => {
    expect(catalogEndpointHint(BEDROCK_ENTRY, 'sa-east-1')).toBe(
      'bedrock-runtime.us-east-1.amazonaws.com',
    )
  })

  it('ignores configuredRegion entirely for a row with no regions picker', () => {
    expect(catalogEndpointHint(ZAI_ENTRY, 'eu-central-1')).toBe(catalogEndpointHint(ZAI_ENTRY))
  })
})

describe('catalogLabel — configuredRegion (issue #800 D2-addendum)', () => {
  // regionLabel (providerLabels.ts) passes an unmapped code through raw —
  // unrelated to this fix; only WHICH region string feeds it changes.
  it('shows the configured region, not the catalog default', () => {
    expect(catalogLabel(BEDROCK_ENTRY, 'eu-central-1')).toBe('Amazon Bedrock (eu-central-1)')
  })

  it('falls back to the catalog default when configuredRegion is omitted', () => {
    expect(catalogLabel(BEDROCK_ENTRY)).toBe('Amazon Bedrock (us-east-1)')
  })
})

describe('catalogSubtitle — configuredRegion (issue #800 D2-addendum)', () => {
  it('shows the configured region host, not the catalog default host', () => {
    expect(catalogSubtitle(BEDROCK_ENTRY, 'eu-central-1')).toBe(
      'Pay-as-you-go, per token · bedrock-runtime.eu-central-1.amazonaws.com',
    )
  })

  it('falls back to the catalog default host when configuredRegion is omitted', () => {
    expect(catalogSubtitle(BEDROCK_ENTRY)).toBe(
      'Pay-as-you-go, per token · bedrock-runtime.us-east-1.amazonaws.com',
    )
  })
})
