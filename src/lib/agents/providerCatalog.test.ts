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
  })

  it('honours the backend has_models_endpoint signal = false → manual', () => {
    // Even for an id that would heuristically be 'live', the explicit signal wins.
    const p = provider({ id: 'openrouter', has_models_endpoint: false })
    expect(providerCatalogMode(p)).toBe('manual')
  })

  it('falls back to the id heuristic for endpoint providers when no signal', () => {
    expect(providerCatalogMode(provider({ id: 'openrouter' }))).toBe('live')
    expect(providerCatalogMode(provider({ id: 'openai' }))).toBe('live')
    expect(isLiveListingProvider(provider({ id: 'anthropic' }))).toBe(true)
  })

  it('treats the GLM provider id `z-ai` as a live listing provider (heuristic)', () => {
    // The runtime GLM provider registers as `z-ai`, not `zhipu`; its live model
    // list (incl. glm-5.2) must surface on legacy gateways with no signal.
    expect(providerCatalogMode(provider({ id: 'z-ai' }))).toBe('live')
    expect(isLiveListingProvider(provider({ id: 'z-ai' }))).toBe(true)
  })

  it('defaults unknown / endpoint-less providers to manual', () => {
    expect(providerCatalogMode(provider({ id: 'ollama' }))).toBe('manual')
    expect(providerCatalogMode(provider({ id: 'some-self-hosted-thing' }))).toBe('manual')
  })
})
