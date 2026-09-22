// TDD rows 5 and 6 — TestRecommendedChipSelection and TestModelOrdering
// (ADR-068 FR-030).
//
// Oracles: the "Models ordered by vendor then release date" and "At most three
// Recommended chips per provider" scenarios, plus the "Model ordering and
// chips" dataset rows 1-10, in
// docs/internal/specs/adr-068-providers-ux-spec.md. Expected values are read
// off the spec, never off the implementation.

import { describe, expect, it } from 'vitest'
import type { CatalogModel } from '@/lib/api/generated/openapi-types'
import { CATALOG_PROVIDERS } from '@/test/fixtures/providersCatalog'
import {
  MAX_RECOMMENDED_CHIPS,
  MODEL_VIRTUALISATION_THRESHOLD,
  RECOMMENDED_CHIP_LABEL,
  RECOMMENDED_MIN_CONTEXT_WINDOW,
  isRecommendedEligible,
  orderModels,
  orderedModelList,
  recommendedModelIds,
  selectRecommendedModels,
  shouldVirtualiseModelList,
  vendorOfModel,
} from './model-ordering'

function model(over: Partial<CatalogModel> & { id: string }): CatalogModel {
  return {
    name: over.id,
    context_window: 200000,
    max_output_tokens: 8192,
    input_modalities: ['text'],
    tool_call: true,
    status: 'active',
    ...over,
  }
}

describe('TestModelOrdering', () => {
  it('orders the spec scenario: vendor group, then release date desc, undated last', () => {
    // The scenario's own four OpenRouter models, straight from the fixture.
    const openrouter = CATALOG_PROVIDERS.find((p) => p.id === 'openrouter')
    expect(openrouter).toBeDefined()
    const groups = orderModels(openrouter!.models)

    expect(groups.map((g) => g.vendor)).toEqual(['anthropic', 'openai', 'x'])
    const anthropic = groups[0]
    expect(anthropic.models.map((m) => m.id)).toEqual([
      'anthropic/claude-sonnet-4.6', // 2026-02
      'anthropic/claude-3.5-haiku', // 2024-10
    ])
    // x/nodate has no release_date and is the last row of its group.
    const xGroup = groups[groups.length - 1]
    expect(xGroup.models[xGroup.models.length - 1].id).toBe('x/nodate')
  })

  it('dataset 9 — an undated model sorts last within its group, after every dated one', () => {
    const groups = orderModels([
      model({ id: 'v/undated' }),
      model({ id: 'v/old', release_date: '2020-01-01' }),
      model({ id: 'v/new', release_date: '2026-05-05' }),
    ])
    expect(groups).toHaveLength(1)
    expect(groups[0].models.map((m) => m.id)).toEqual(['v/new', 'v/old', 'v/undated'])
  })

  it('dataset 8 — two models sharing a release date tie-break on id ascending', () => {
    const groups = orderModels([
      model({ id: 'v/beta', release_date: '2026-01-01' }),
      model({ id: 'v/alpha', release_date: '2026-01-01' }),
    ])
    expect(groups[0].models.map((m) => m.id)).toEqual(['v/alpha', 'v/beta'])
  })

  it('several undated models in one group order by id ascending', () => {
    const groups = orderModels([model({ id: 'v/zeta' }), model({ id: 'v/alpha' })])
    expect(groups[0].models.map((m) => m.id)).toEqual(['v/alpha', 'v/zeta'])
  })

  it('groups bare ids under the provider fallback vendor, prefixed ids by prefix', () => {
    expect(vendorOfModel(model({ id: 'gpt-5' }), 'OpenAI')).toBe('OpenAI')
    expect(vendorOfModel(model({ id: 'anthropic/claude-3.5-haiku' }), 'OpenRouter')).toBe('anthropic')
    // A leading slash is not a vendor prefix.
    expect(vendorOfModel(model({ id: '/weird' }), 'Fallback')).toBe('Fallback')
  })

  it('orders vendor groups independently of catalog insertion order', () => {
    const forward = orderModels([model({ id: 'zeta/a' }), model({ id: 'alpha/a' })])
    const reversed = orderModels([model({ id: 'alpha/a' }), model({ id: 'zeta/a' })])
    expect(forward.map((g) => g.vendor)).toEqual(['alpha', 'zeta'])
    expect(reversed.map((g) => g.vendor)).toEqual(forward.map((g) => g.vendor))
  })

  it('dataset 1 — zero models yields no groups and an empty flattened list', () => {
    expect(orderModels([])).toEqual([])
    expect(orderedModelList([])).toEqual([])
  })

  it('never mutates the caller array', () => {
    const input = [model({ id: 'v/b', release_date: '2020-01-01' }), model({ id: 'v/a', release_date: '2026-01-01' })]
    const snapshot = input.map((m) => m.id)
    orderModels(input)
    orderedModelList(input)
    selectRecommendedModels(input)
    expect(input.map((m) => m.id)).toEqual(snapshot)
  })

  it('flattens groups in render order', () => {
    const flat = orderedModelList([
      model({ id: 'b/one', release_date: '2026-01-01' }),
      model({ id: 'a/old', release_date: '2020-01-01' }),
      model({ id: 'a/new', release_date: '2026-01-01' }),
    ])
    expect(flat.map((m) => m.id)).toEqual(['a/new', 'a/old', 'b/one'])
  })

  it('dataset 10 — virtualisation kicks in above 100 items, not at 100', () => {
    expect(MODEL_VIRTUALISATION_THRESHOLD).toBe(100)
    expect(shouldVirtualiseModelList(100)).toBe(false)
    expect(shouldVirtualiseModelList(101)).toBe(true)
    expect(shouldVirtualiseModelList(359)).toBe(true)
  })
})

describe('TestRecommendedChipSelection', () => {
  it('the spec scenario — 5 tool-calling models (200k, 200k, 128k, 128k, 127,999) yield exactly 3 chips', () => {
    const models = [
      model({ id: 'p/a', context_window: 200000, release_date: '2026-05-01' }),
      model({ id: 'p/b', context_window: 200000, release_date: '2026-04-01' }),
      model({ id: 'p/c', context_window: 128000, release_date: '2026-03-01' }),
      model({ id: 'p/d', context_window: 128000, release_date: '2026-02-01' }),
      model({ id: 'p/e', context_window: 127999, release_date: '2026-06-01' }),
    ]
    const chips = recommendedModelIds(models)
    expect(chips).toHaveLength(3)
    expect(chips).not.toContain('p/e')
  })

  it('dataset 2 — a single tool-calling 200k model gets one chip', () => {
    expect(recommendedModelIds([model({ id: 'p/only', context_window: 200000 })])).toEqual(['p/only'])
  })

  it('dataset 3 — exactly three eligible models yield three chips', () => {
    const ids = recommendedModelIds([
      model({ id: 'p/a', release_date: '2026-03-01' }),
      model({ id: 'p/b', release_date: '2026-02-01' }),
      model({ id: 'p/c', release_date: '2026-01-01' }),
    ])
    expect(ids).toEqual(['p/a', 'p/b', 'p/c'])
  })

  it('dataset 4 — four eligible models drop the oldest', () => {
    const ids = recommendedModelIds([
      model({ id: 'p/oldest', release_date: '2023-01-01' }),
      model({ id: 'p/a', release_date: '2026-03-01' }),
      model({ id: 'p/b', release_date: '2026-02-01' }),
      model({ id: 'p/c', release_date: '2026-01-01' }),
    ])
    expect(ids).toEqual(['p/a', 'p/b', 'p/c'])
    expect(ids).not.toContain('p/oldest')
  })

  it('datasets 5 and 6 — 127,999 is out, 128,000 is in', () => {
    expect(RECOMMENDED_MIN_CONTEXT_WINDOW).toBe(128000)
    expect(isRecommendedEligible(model({ id: 'p/min-1', context_window: 127999 }))).toBe(false)
    expect(isRecommendedEligible(model({ id: 'p/min', context_window: 128000 }))).toBe(true)
  })

  it('dataset 7 — no tool calling disqualifies a 1M-window model', () => {
    expect(
      isRecommendedEligible(model({ id: 'p/huge', context_window: 1000000, tool_call: false })),
    ).toBe(false)
    expect(recommendedModelIds([model({ id: 'p/huge', context_window: 1000000, tool_call: false })])).toEqual([])
  })

  it('a retired model is never recommended, however capable', () => {
    expect(
      isRecommendedEligible(model({ id: 'p/retired', context_window: 400000, status: 'retired' })),
    ).toBe(false)
  })

  it('dataset 1 — zero models yields zero chips', () => {
    expect(recommendedModelIds([])).toEqual([])
    expect(MAX_RECOMMENDED_CHIPS).toBe(3)
  })

  it('chip ties break on id ascending, so the selection is deterministic', () => {
    const ids = recommendedModelIds([
      model({ id: 'p/d', release_date: '2026-01-01' }),
      model({ id: 'p/c', release_date: '2026-01-01' }),
      model({ id: 'p/b', release_date: '2026-01-01' }),
      model({ id: 'p/a', release_date: '2026-01-01' }),
    ])
    expect(ids).toEqual(['p/a', 'p/b', 'p/c'])
  })

  it('undated eligible models rank below dated ones', () => {
    const ids = recommendedModelIds([
      model({ id: 'p/undated' }),
      model({ id: 'p/dated-2020', release_date: '2020-01-01' }),
    ])
    expect(ids).toEqual(['p/dated-2020', 'p/undated'])
  })

  it('holds the chip copy the spec names', () => {
    expect(RECOMMENDED_CHIP_LABEL).toBe('Recommended for chat')
  })

  it('no provider in the 190-entry fixture exceeds three chips', () => {
    for (const provider of CATALOG_PROVIDERS) {
      expect(recommendedModelIds(provider.models).length).toBeLessThanOrEqual(MAX_RECOMMENDED_CHIPS)
    }
  })
})
