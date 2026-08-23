import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { buildModelToProvider, useModelToProvider } from './modelToProvider'
import type { Provider } from '@/lib/api/generated/openapi-types'

const openrouter: Provider = {
  id: 'openrouter',
  name: 'openrouter',
  display_name: 'OpenRouter',
  status: 'connected',
  auth_method: 'api_key',
  dependents: [],
  backs_default: false,
  models: ['z-ai/glm-5.2', 'z-ai/glm-5-turbo'],
}

const anthropic: Provider = {
  id: 'anthropic',
  name: 'anthropic',
  display_name: 'Anthropic',
  status: 'connected',
  auth_method: 'api_key',
  dependents: [],
  backs_default: false,
  models: ['claude-sonnet-4-6', 'claude-opus-4-6'],
}

const disconnected: Provider = {
  id: 'openai',
  name: 'openai',
  display_name: 'OpenAI',
  status: 'disconnected',
  auth_method: 'api_key',
  dependents: [],
  backs_default: false,
  models: ['gpt-4o'],
}

// W6-C2 / I9 — the chip's `provider` field must equal the wire
// routing key (provider id), NOT the display name. Pre-I9 the editor
// stored `display_name ?? name ?? id`, which silently downgraded
// `provider` to the brand label and broke runtime resolution.
describe('buildModelToProvider — wires provider IDs (not display names)', () => {
  it('returns provider ids, never display names', () => {
    const { byModel } = buildModelToProvider([openrouter, anthropic])
    expect(byModel['z-ai/glm-5-turbo']).toBe('openrouter')
    expect(byModel['claude-sonnet-4-6']).toBe('anthropic')
  })

  it('does not leak the display name into the lookup table', () => {
    const { byModel } = buildModelToProvider([openrouter])
    // Regression guard: a naive implementation could return
    // 'OpenRouter' here. The wire routing key is the id ('openrouter'),
    // which happens to equal `name` for unknown providers; the
    // discriminator is that 'OpenRouter' (capitalized) must never appear.
    expect(byModel['z-ai/glm-5.2']).not.toBe('OpenRouter')
    expect(byModel['z-ai/glm-5.2']).toBe('openrouter')
  })
})

describe('buildModelToProvider — basic lookup semantics', () => {
  it('returns an empty record for an empty provider list', () => {
    const { byModel, lookup } = buildModelToProvider([])
    expect(byModel).toEqual({})
    expect(lookup('any-model')).toBe('')
  })

  it('returns an empty record when no providers advertise models', () => {
    const empty: Provider = { ...openrouter, models: [] }
    const { byModel } = buildModelToProvider([empty])
    expect(byModel).toEqual({})
  })

  it('preserves the first-wins ordering on duplicate slugs across providers', () => {
    // Build a second provider that ALSO advertises a slug from
    // anthropic's list. Pass anthropic first so anthropic should own
    // the shared entry — this matches ModelSelector's render order so
    // the chip and the selector stay consistent.
    const withShared: Provider = { ...openrouter, models: [...openrouter.models, 'claude-sonnet-4-6'] }
    const { byModel } = buildModelToProvider([anthropic, withShared])
    expect(byModel['claude-sonnet-4-6']).toBe('anthropic')
  })

  it('skips providers whose id is empty (defensive)', () => {
    const nameless: Provider = { ...openrouter, id: '', models: ['orphan-model'] }
    const { byModel } = buildModelToProvider([nameless, anthropic])
    expect(byModel['orphan-model']).toBeUndefined()
    // Other providers still resolve normally.
    expect(byModel['claude-sonnet-4-6']).toBe('anthropic')
  })

  it('lookup returns empty string for unknown models', () => {
    const { lookup } = buildModelToProvider([openrouter])
    expect(lookup('never-listed-model')).toBe('')
  })

  it('treats disconnected providers as resolvable — caller filters by status', () => {
  // buildModelToProvider does not filter by status — the caller (the
  // fallback editor) supplies only `connectedProviders`. This test
  // pins that contract: disconnected providers DO contribute to the
  // map if passed in. If callers ever forget the filter, the editor's
  // add flow would silently route through a disconnected provider;
  // the persistent indicator (I11) catches it at chip time, but the
  // caller's filter is the first line of defense.
    const { byModel } = buildModelToProvider([disconnected])
    expect(byModel['gpt-4o']).toBe('openai')
  })
})

describe('useModelToProvider — react hook memoization', () => {
  it('returns a stable reference when the provider list is unchanged (same array ref)', () => {
    // Pin the consumer contract: when the caller holds a stable
    // reference (the common case — providers is a TanStack Query cache
    // value that only swaps on refetch), useMemo should preserve
    // identity so downstream renders don't churn.
    const stableProviders: Provider[] = [openrouter, anthropic]
    const { result, rerender } = renderHook(
      ({ providers }: { providers: Provider[] }) => useModelToProvider(providers),
      { initialProps: { providers: stableProviders } },
    )
    const first = result.current
    rerender({ providers: stableProviders })
    expect(result.current).toBe(first)
  })

  it('recomputes when the provider list changes', () => {
    const { result, rerender } = renderHook(
      ({ providers }: { providers: Provider[] }) => useModelToProvider(providers),
      { initialProps: { providers: [openrouter] } },
    )
    expect(result.current.lookup('z-ai/glm-5.2')).toBe('openrouter')
    expect(result.current.lookup('claude-sonnet-4-6')).toBe('')
    rerender({ providers: [openrouter, anthropic] })
    expect(result.current.lookup('claude-sonnet-4-6')).toBe('anthropic')
  })
})