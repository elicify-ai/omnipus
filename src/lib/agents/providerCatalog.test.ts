// providerCatalog.test.ts — catalogue-mode resolution.
//
// ADR-067 T067-13 rewrote the oracle: the three cases that used to assert the
// id-based heuristic ('openrouter'/'openai'/'anthropic' → live with no signal,
// 'z-ai' → live with no signal) exercised a hand-written provider table
// compiled into the SPA. FR-011 / FR-025 delete that table, and Provider.yaml
// already specified the absent case as "treat as false (editable slug list)" —
// so those ids now resolve to 'manual' when the gateway sends no signal. The
// still-valid coverage they carried (an explicit signal is honoured in BOTH
// directions, and the absent case has a defined, safe answer for every id) is
// kept below and strengthened: the ids themselves must no longer change the
// answer.

import { describe, it, expect } from 'vitest'
import { providerCatalogMode, isLiveListingProvider } from './providerCatalog'
import type { Provider } from '@/lib/api/generated/openapi-types'

function provider(overrides: Partial<Provider> & { id: string }): Provider {
  return {
    name: overrides.id,
    status: 'connected',
    models: [],
    ...overrides,
  } as Provider
}

describe('providerCatalogMode (UAT model-catalog)', () => {
  it('honours the backend has_models_endpoint signal = true → live', () => {
    const p = provider({ id: 'custom-x', has_models_endpoint: true })
    expect(providerCatalogMode(p)).toBe('live')
    expect(isLiveListingProvider(p)).toBe(true)
  })

  it('honours the backend has_models_endpoint signal = false → manual', () => {
    const p = provider({ id: 'openrouter', has_models_endpoint: false })
    expect(providerCatalogMode(p)).toBe('manual')
    expect(isLiveListingProvider(p)).toBe(false)
  })

  it('an absent signal is manual — per Provider.yaml, for EVERY id', () => {
    // Negative control for the deleted id table (FR-025): each of these ids was
    // hard-coded 'live' by the old heuristic. If any of them answers 'live'
    // again, a second provider catalog has come back into the SPA.
    for (const id of ['openrouter', 'openai', 'anthropic', 'z-ai', 'gemini', 'zhipu', 'qwen-intl']) {
      expect(providerCatalogMode(provider({ id })), id).toBe('manual')
      expect(isLiveListingProvider(provider({ id })), id).toBe(false)
    }
  })

  it('defaults unknown / endpoint-less providers to manual', () => {
    expect(providerCatalogMode(provider({ id: 'ollama' }))).toBe('manual')
    expect(providerCatalogMode(provider({ id: 'some-self-hosted-thing' }))).toBe('manual')
  })
})
