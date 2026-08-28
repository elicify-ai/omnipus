// providersCatalog.test.ts — pins the shape of the 190-entry catalog fixture
// (ADR-068 T068-18). Every later ADR-068 SPA test renders against this
// document, so a silent edit that drops the Popular count, the unsupported
// row or the sign-in trio would break those tests far from the cause. These
// assertions are the fixture's contract, restated from the spec (FR-022,
// FR-025) rather than read off the JSON.

import { describe, it, expect } from 'vitest'
import { ProvidersCatalog as ProvidersCatalogSchema } from '@/lib/api/generated/schemas'
import { PROVIDERS_CATALOG, CATALOG_PROVIDERS } from './providersCatalog'

describe('providers-catalog.json fixture', () => {
  it('validates against the generated ProvidersCatalog schema', () => {
    const result = ProvidersCatalogSchema.safeParse(PROVIDERS_CATALOG)
    expect(result.success, JSON.stringify(result.success ? [] : result.error.issues.slice(0, 5))).toBe(true)
  })

  it('carries exactly 190 providers with unique ids', () => {
    expect(CATALOG_PROVIDERS).toHaveLength(190)
    expect(new Set(CATALOG_PROVIDERS.map((p) => p.id)).size).toBe(190)
  })

  it('has exactly 12 popular providers, each a distinct company (FR-022, twelve usage-backed)', () => {
    // Popular tier per catalog repo commit b50f5a6: groq demoted (an
    // inference host, not a model author — no author-usage ranking); ollama
    // promoted (local-model support, on brand for a self-hosted product).
    const popular = CATALOG_PROVIDERS.filter((p) => p.tier === 'popular')
    expect(popular).toHaveLength(12)
    expect(new Set(popular.map((p) => p.company)).size).toBe(12)
    expect(popular.map((p) => p.id).sort()).toEqual(
      ['alibaba', 'anthropic', 'deepseek', 'google', 'minimax', 'mistral', 'moonshot', 'ollama', 'openai', 'openrouter', 'xai', 'zai'],
    )
    expect(popular.map((p) => p.id)).not.toContain('groq')
  })

  it('marks bedrock unsupported with the cloud-iam reason (FR-025)', () => {
    const bedrock = CATALOG_PROVIDERS.find((p) => p.id === 'bedrock')
    expect(bedrock?.tier).toBe('unsupported')
    expect(bedrock?.unsupported_reason).toBe('cloud-iam')
  })

  it('covers all three unsupported reasons', () => {
    const reasons = new Set(
      CATALOG_PROVIDERS.filter((p) => p.tier === 'unsupported').map((p) => p.unsupported_reason),
    )
    expect(reasons).toEqual(new Set(['cloud-iam', 'deployment-url', 'withdrawn']))
  })

  it('groups the Zhipu AI plan x region variants under one company', () => {
    const zhipu = CATALOG_PROVIDERS.filter((p) => p.company === 'Zhipu AI')
    expect(zhipu.map((p) => p.id).sort()).toEqual(
      ['zai', 'zai-coding-plan', 'zhipuai', 'zhipuai-coding-plan'],
    )
    expect(zhipu.find((p) => p.id === 'zai')?.region).toBe('intl')
    expect(zhipu.find((p) => p.id === 'zhipuai')?.region).toBe('china')
    expect(zhipu.find((p) => p.id === 'zai-coding-plan')?.plan).toBe('coding-plan')
  })

  it('carries the glm-coding and 智谱 aliases', () => {
    expect(CATALOG_PROVIDERS.find((p) => p.id === 'zai-coding-plan')?.aliases).toContain('glm-coding')
    expect(CATALOG_PROVIDERS.find((p) => p.id === 'zai')?.aliases).toContain('智谱')
  })

  it('offers sign_in on exactly the three subscription rows (ADR-068 §8b)', () => {
    const signIn = CATALOG_PROVIDERS.filter((p) => p.auth_methods.includes('sign_in'))
    expect(signIn.map((p) => p.id).sort()).toEqual(['codex-cli', 'github-copilot', 'openai-chatgpt'])
    expect(CATALOG_PROVIDERS.find((p) => p.id === 'codex-cli')?.cli_kind).toBe('codex')
    expect(CATALOG_PROVIDERS.find((p) => p.id === 'github-copilot')?.cli_kind).toBe('copilot')
    // xAI stays key-only until Omnipus holds its own client id (§8b D3).
    expect(CATALOG_PROVIDERS.find((p) => p.id === 'xai')?.auth_methods).toEqual(['api_key'])
  })

  it('keeps each OpenAI-family row a single-variant company, so `openai` stays one-click', () => {
    for (const id of ['openai', 'openai-chatgpt', 'codex-cli']) {
      const entry = CATALOG_PROVIDERS.find((p) => p.id === id)
      expect(entry, id).toBeDefined()
      expect(
        CATALOG_PROVIDERS.filter((p) => p.company === entry!.company).map((p) => p.id),
        `${id} must be the only variant of company ${entry!.company}`,
      ).toEqual([id])
    }
  })

  it('omits vllm and litellm — the migration dataset needs them unknown', () => {
    const ids = CATALOG_PROVIDERS.map((p) => p.id)
    expect(ids).not.toContain('vllm')
    expect(ids).not.toContain('litellm')
  })

  it('gives every model the fields the picker and window rung read', () => {
    for (const provider of CATALOG_PROVIDERS) {
      expect(provider.models.length, `${provider.id} has no models`).toBeGreaterThan(0)
      for (const model of provider.models) {
        expect(typeof model.context_window).toBe('number')
        expect(typeof model.max_output_tokens).toBe('number')
        expect(typeof model.tool_call).toBe('boolean')
        expect(model.input_modalities.length).toBeGreaterThan(0)
        expect(['active', 'retired']).toContain(model.status)
      }
    }
  })

  it('carries the OpenRouter vendor/release-date ordering dataset', () => {
    const models = CATALOG_PROVIDERS.find((p) => p.id === 'openrouter')?.models ?? []
    const byId = Object.fromEntries(models.map((m) => [m.id, m]))
    expect(byId['anthropic/claude-sonnet-4.6']?.release_date).toMatch(/^2026-02/)
    expect(byId['anthropic/claude-3.5-haiku']?.release_date).toMatch(/^2024-10/)
    expect(byId['openai/gpt-5.4']?.release_date).toMatch(/^2026-03/)
    expect(byId['x/nodate']).toBeDefined()
    expect(byId['x/nodate']?.release_date).toBeUndefined()
  })

  it('includes a retired model and a leading-digit name group', () => {
    expect(CATALOG_PROVIDERS.some((p) => p.models.some((m) => m.status === 'retired'))).toBe(true)
    expect(CATALOG_PROVIDERS.some((p) => /^\d/.test(p.name))).toBe(true)
  })
})
