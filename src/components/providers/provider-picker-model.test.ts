// TDD row 8 — TestPickerModel (ADR-068 FR-022, FR-023, FR-024).
//
// Oracles: the "Picker opens with 12 Popular tiles and a collapsed list",
// "Search expands and filters the full list", "Expanded list is letter-grouped
// and virtualised", "Recently used row appears" and "Custom endpoint is last"
// scenarios, plus the "Picker search" dataset rows 1-10, in
// docs/internal/specs/adr-068-providers-ux-spec.md.

import { describe, expect, it } from 'vitest'
import type { CatalogProvider, Provider, ProvidersCatalog } from '@/lib/api/generated/openapi-types'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import {
  CUSTOM_ENDPOINT_ROW_ID,
  MAX_RECENT_PROVIDERS,
  OTHER_LETTER_GROUP,
  buildPickerModel,
  companyLetter,
  normalizePickerQuery,
  pickerRowSequence,
  providerRowMatchesQuery,
  toCompanyRows,
} from './provider-picker-model'

const catalog = PROVIDERS_CATALOG

function build(query?: string, extra: { configured?: Provider[]; expandedByOperator?: boolean } = {}) {
  return buildPickerModel({ catalog, query, ...extra })
}

function catalogProvider(over: Partial<CatalogProvider> & { id: string; company: string }): CatalogProvider {
  return {
    name: over.id,
    api: 'https://api.example.test/v1',
    tier: 'standard',
    auth_methods: ['api_key'],
    aliases: [],
    locality: 'cloud',
    models: [],
    ...over,
  }
}

function configuredProvider(over: Partial<Provider> & { id: string }): Provider {
  return {
    name: over.id,
    status: 'connected',
    auth_method: 'api_key',
    dependents: [],
    backs_default: false,
    models: [],
    ...over,
  }
}

describe('TestPickerModel', () => {
  describe('Popular band (FR-022)', () => {
    it('renders exactly 12 tiles, one per popular company, in catalog order', () => {
      // Twelve, usage-backed (catalog repo commit b50f5a6): groq demoted (an
      // inference host, not a model author); ollama promoted (local-model
      // support, on brand for a self-hosted product).
      const model = build()
      expect(model.popular.map((row) => row.company)).toEqual([
        'OpenAI',
        'Anthropic',
        'OpenRouter',
        'Google Gemini',
        'DeepSeek',
        'xAI',
        'Zhipu AI',
        'Moonshot AI',
        'MiniMax',
        'Alibaba Cloud',
        'Ollama',
        'Mistral AI',
      ])
      expect(model.popular.map((row) => row.company)).not.toContain('Groq')
    })

    it('follows the catalog, not a SPA constant — retiering re-derives the band', () => {
      // The scenario's own fixture edit: ollama becomes standard, cerebras popular.
      const edited: ProvidersCatalog = {
        ...catalog,
        providers: catalog.providers.map((provider) =>
          provider.id === 'ollama'
            ? { ...provider, tier: 'standard' as const }
            : provider.id === 'cerebras'
              ? { ...provider, tier: 'popular' as const }
              : provider,
        ),
      }
      const companies = buildPickerModel({ catalog: edited }).popular.map((row) => row.company)
      expect(companies).not.toContain('Ollama')
      expect(companies).toContain('Cerebras')
      expect(companies).toHaveLength(12)
      // Catalog order still, so Cerebras lands where cerebras sits in the document.
      const catalogOrder = edited.providers.filter((p) => p.tier === 'popular').map((p) => p.company)
      expect(companies).toEqual(catalogOrder)
    })

    it('collapses a company\'s plan x region variants into one tile', () => {
      const zhipu = build().popular.find((row) => row.company === 'Zhipu AI')
      expect(zhipu).toBeDefined()
      expect(zhipu!.variants.map((v) => v.id)).toEqual([
        'zai',
        'zai-coding-plan',
        'zhipuai',
        'zhipuai-coding-plan',
      ])
      // The one-click target is the first variant in catalog order — which,
      // in this fixture, also happens to be the tier: popular row.
      expect(zhipu!.primary.id).toBe('zai')
      expect(zhipu!.plans).toEqual(['coding-plan'])
      expect(zhipu!.regions).toEqual(['intl', 'china'])
    })

    // ADR-068 FR-006 — UAT-confirmed defect on a real running instance: the
    // popular OpenAI tile carried data-testid "picker-popular-codex-cli"
    // instead of "picker-popular-openai". Root cause: the served catalog's
    // providers[] array sorts alphabetically by id, which is an assembly-job
    // presentation detail (not a spec decision) and put the standard-tier
    // "codex-cli" ahead of the popular-tier "openai" within the OpenAI
    // company group — `primary` used to mean "first in that array", so it
    // silently inherited the wrong row.
    it('picks the tier: popular variant as primary even when it is not first in catalog order (FR-006)', () => {
      const row = toCompanyRows([
        catalogProvider({ id: 'codex-cli', name: 'Codex CLI', company: 'OpenAI', tier: 'standard', auth_methods: ['sign_in'] }),
        catalogProvider({ id: 'openai', name: 'OpenAI', company: 'OpenAI', tier: 'popular', auth_methods: ['api_key'] }),
        catalogProvider({ id: 'openai-chatgpt', name: 'ChatGPT (subscription)', company: 'OpenAI', tier: 'standard', auth_methods: ['sign_in'] }),
      ])[0]
      expect(row.variants.map((v) => v.id)).toEqual(['codex-cli', 'openai', 'openai-chatgpt'])
      expect(row.primary.id).toBe('openai')
    })

    it('falls back to the first variant in catalog order when no variant is tier: popular', () => {
      const row = toCompanyRows([
        catalogProvider({ id: 'acme-b', company: 'Acme', tier: 'standard' }),
        catalogProvider({ id: 'acme-a', company: 'Acme', tier: 'standard' }),
      ])[0]
      expect(row.primary.id).toBe('acme-b')
    })

    it('counts "All providers (N)" as the catalog entries that are not Popular tiles', () => {
      const model = build()
      const popularEntries = catalog.providers.filter((p) => p.tier === 'popular').length
      expect(popularEntries).toBe(12)
      expect(model.allProvidersCount).toBe(catalog.providers.length - popularEntries)
      expect(model.allProvidersCount).toBe(178)
    })
  })

  describe('Collapse and expand (FR-022, Picker search dataset 1-2)', () => {
    it('dataset 1 — an empty query leaves the list collapsed', () => {
      expect(build().expanded).toBe(false)
      expect(build('').expanded).toBe(false)
      expect(build(undefined).expanded).toBe(false)
    })

    it('dataset 2 — a whitespace-only query is empty, so the list stays collapsed', () => {
      const model = build('   ')
      expect(model.trimmedQuery).toBe('')
      expect(model.expanded).toBe(false)
      expect(model.matchCount).toBe(model.letterGroups.flatMap((g) => g.rows).length)
    })

    it('expands on any non-empty trimmed query', () => {
      expect(build('z').expanded).toBe(true)
      expect(build('  z  ').expanded).toBe(true)
    })

    it('expands without a query when the operator opens the list', () => {
      const model = build('', { expandedByOperator: true })
      expect(model.expanded).toBe(true)
      expect(model.letterGroups.flatMap((g) => g.rows).length).toBeGreaterThan(0)
    })
  })

  describe('Search (FR-024, Picker search dataset 3-10)', () => {
    it('dataset 3 — a single character matches every row containing it', () => {
      const model = build('z')
      expect(model.hasMatches).toBe(true)
      for (const row of model.letterGroups.flatMap((g) => g.rows)) {
        expect(providerRowMatchesQuery(row, 'z')).toBe(true)
      }
      // And nothing that matches was dropped.
      const expected = toCompanyRows(catalog.providers).filter((row) => providerRowMatchesQuery(row, 'z'))
      expect(model.matchCount).toBe(expected.length)
    })

    it('dataset 4 — the query is case-insensitive and matches a variant name', () => {
      const lower = build('coding plan')
      const mixed = build('Coding Plan')
      expect(lower.matchCount).toBe(mixed.matchCount)
      expect(lower.matchCount).toBeGreaterThan(0)
      const companies = mixed.letterGroups.flatMap((g) => g.rows).map((r) => r.company)
      expect(companies).toContain('Zhipu AI')
    })

    it('dataset 5 — a region code matches', () => {
      const companies = build('china').letterGroups.flatMap((g) => g.rows).map((r) => r.company)
      expect(companies).toContain('Zhipu AI')
      expect(companies.length).toBeGreaterThan(0)
    })

    it('dataset 6 — an alias matches', () => {
      const companies = build('glm-coding').letterGroups.flatMap((g) => g.rows).map((r) => r.company)
      expect(companies).toEqual(['Zhipu AI'])
    })

    it('dataset 10 — a CJK alias matches', () => {
      const companies = build('智谱').letterGroups.flatMap((g) => g.rows).map((r) => r.company)
      expect(companies).toEqual(['Zhipu AI'])
    })

    it('dataset 7 — an unsupported provider still matches and is marked disabled with its reason', () => {
      const rows = build('bedrock').letterGroups.flatMap((g) => g.rows)
      const amazon = rows.find((row) => row.company === 'Amazon')
      expect(amazon).toBeDefined()
      expect(amazon!.disabled).toBe(true)
      expect(amazon!.unsupportedReason).toBe('cloud-iam')
    })

    it('dataset 8 — regex metacharacters are matched literally, never compiled', () => {
      expect(() => build('(*[')).not.toThrow()
      const model = build('(*[')
      expect(model.matchCount).toBe(0)
      expect(model.hasMatches).toBe(false)
      expect(model.emptyMessage).toBe('No provider matches (*[')
      // ".*" would match everything if the query were a pattern.
      expect(build('.*').matchCount).toBe(0)
    })

    it('dataset 9 — a 200-character query matches nothing and reports the no-match state', () => {
      const long = 'a'.repeat(200)
      const model = build(long)
      expect(model.matchCount).toBe(0)
      expect(model.emptyMessage).toBe(`No provider matches ${long}`)
    })

    it('shows the no-match copy only for a non-empty query', () => {
      expect(build('').emptyMessage).toBeUndefined()
      expect(build('   ').emptyMessage).toBeUndefined()
      expect(build('zzzz').emptyMessage).toBe('No provider matches zzzz')
    })

    it('matches company, variant name, plan, region and alias — and nothing else', () => {
      const row = toCompanyRows([
        catalogProvider({
          id: 'acme-eu',
          name: 'Acme Cloud EU',
          company: 'Acme',
          api: 'https://api.acme.test/v1',
          plan: 'coding-plan',
          region: 'eu',
          aliases: ['acme-alias'],
        }),
      ])[0]
      for (const query of ['acme', 'Acme Cloud EU', 'coding-plan', 'eu', 'acme-alias']) {
        expect(providerRowMatchesQuery(row, query)).toBe(true)
      }
      // The provider id is deliberately NOT a search field (FR-024's list).
      expect(providerRowMatchesQuery(row, 'acme-eu')).toBe(false)
      expect(providerRowMatchesQuery(row, 'nothing-here')).toBe(false)
      // An empty query matches everything, so the collapsed list is the full list.
      expect(providerRowMatchesQuery(row, '  ')).toBe(true)
    })

    it('normalises a query by trimming and lower-casing only', () => {
      expect(normalizePickerQuery('  Coding Plan ')).toBe('coding plan')
      expect(normalizePickerQuery(null)).toBe('')
      expect(normalizePickerQuery(undefined)).toBe('')
    })
  })

  describe('Letter grouping (FR-023)', () => {
    it('orders letter headers A-Z and puts "#" last', () => {
      const letters = build('', { expandedByOperator: true }).letterGroups.map((g) => g.letter)
      const hashIndex = letters.indexOf(OTHER_LETTER_GROUP)
      expect(hashIndex).toBe(letters.length - 1)
      const alpha = letters.slice(0, hashIndex)
      expect(alpha).toEqual([...alpha].sort())
      expect(new Set(letters).size).toBe(letters.length)
    })

    it('files leading-digit and non-latin companies under "#"', () => {
      expect(companyLetter('01.AI')).toBe(OTHER_LETTER_GROUP)
      expect(companyLetter('302.AI')).toBe(OTHER_LETTER_GROUP)
      expect(companyLetter('智谱')).toBe(OTHER_LETTER_GROUP)
      expect(companyLetter('  acme')).toBe('A')
      expect(companyLetter('Zhipu AI')).toBe('Z')
      const hash = build('', { expandedByOperator: true }).letterGroups.find(
        (g) => g.letter === OTHER_LETTER_GROUP,
      )
      expect(hash!.rows.map((r) => r.company)).toEqual(['01.AI', '302.AI'])
    })

    it('lists every catalog company exactly once across the groups', () => {
      const rows = build('', { expandedByOperator: true }).letterGroups.flatMap((g) => g.rows)
      const companies = rows.map((r) => r.company)
      expect(new Set(companies).size).toBe(companies.length)
      expect(new Set(companies)).toEqual(new Set(catalog.providers.map((p) => p.company)))
    })

    it('sorts companies inside a group case-insensitively', () => {
      const groups = build('', { expandedByOperator: true }).letterGroups
      for (const group of groups) {
        const names = group.rows.map((r) => r.company.toLocaleLowerCase())
        expect(names).toEqual([...names].sort())
      }
    })

    it('keeps popular companies in the expanded list, so search can reach them', () => {
      // xAI is a Popular tile; typing "xai" must still find its row.
      const companies = build('xai').letterGroups.flatMap((g) => g.rows).map((r) => r.company)
      expect(companies).toContain('xAI')
    })
  })

  describe('Recent (FR-022)', () => {
    it('lists configured providers by updated_at desc, capped at three', () => {
      const model = build('', {
        configured: [
          configuredProvider({ id: 'openai', name: 'OpenAI', updated_at: '2026-08-01T00:00:00Z' }),
          configuredProvider({ id: 'zai-coding-plan', name: 'Z.AI Coding Plan', updated_at: '2026-08-20T00:00:00Z' }),
          configuredProvider({ id: 'groq', name: 'Groq', updated_at: '2026-08-10T00:00:00Z' }),
          configuredProvider({ id: 'anthropic', name: 'Anthropic', updated_at: '2026-07-01T00:00:00Z' }),
        ],
      })
      expect(model.recent.map((r) => r.provider.id)).toEqual(['zai-coding-plan', 'groq', 'openai'])
      expect(model.recent).toHaveLength(MAX_RECENT_PROVIDERS)
      expect(model.recent[0].label).toBe('Z.AI Coding Plan')
      expect(model.recent[0].catalog?.company).toBe('Zhipu AI')
    })

    it('prefers display_name and tolerates a missing catalog row', () => {
      const model = build('', {
        configured: [
          configuredProvider({
            id: 'my-endpoint',
            name: 'my-endpoint',
            display_name: 'Lab box',
            custom: true,
            updated_at: '2026-08-21T00:00:00Z',
          }),
        ],
      })
      expect(model.recent[0].label).toBe('Lab box')
      expect(model.recent[0].catalog).toBeUndefined()
    })

    it('sorts never-updated providers last and breaks ties on id', () => {
      const model = build('', {
        configured: [
          configuredProvider({ id: 'no-date' }),
          configuredProvider({ id: 'b', updated_at: '2026-08-01T00:00:00Z' }),
          configuredProvider({ id: 'a', updated_at: '2026-08-01T00:00:00Z' }),
        ],
      })
      expect(model.recent.map((r) => r.provider.id)).toEqual(['a', 'b', 'no-date'])
    })

    it('is empty when nothing is configured, and never filtered by the query', () => {
      expect(build('zzzz').recent).toEqual([])
      const model = build('zzzz', {
        configured: [configuredProvider({ id: 'openai', updated_at: '2026-08-01T00:00:00Z' })],
      })
      expect(model.recent.map((r) => r.provider.id)).toEqual(['openai'])
    })
  })

  describe('Row sequence (FR-022, FR-026)', () => {
    it('ends on Custom endpoint whatever the query', () => {
      for (const query of ['', 'z', 'zzzz', '智谱']) {
        const sequence = pickerRowSequence(build(query))
        expect(sequence[sequence.length - 1]).toEqual({ kind: 'custom', key: CUSTOM_ENDPOINT_ROW_ID })
        expect(sequence.filter((ref) => ref.kind === 'custom')).toHaveLength(1)
      }
    })

    it('orders Popular, then Recent, then the expanded list, then Custom', () => {
      const model = build('z', {
        configured: [configuredProvider({ id: 'openai', updated_at: '2026-08-01T00:00:00Z' })],
      })
      const kinds = pickerRowSequence(model).map((ref) => ref.kind)
      expect(kinds[0]).toBe('popular')
      expect(kinds.indexOf('recent')).toBe(model.popular.length)
      expect(kinds.indexOf('company')).toBe(model.popular.length + 1)
      expect(kinds[kinds.length - 1]).toBe('custom')
    })

    it('omits the collapsed list from the keyboard order', () => {
      const collapsed = pickerRowSequence(build(''))
      expect(collapsed.some((ref) => ref.kind === 'company')).toBe(false)
      expect(collapsed).toHaveLength(build('').popular.length + 1)
    })
  })

  describe('Degenerate inputs', () => {
    it('returns an empty, collapsed model when the catalog is unavailable', () => {
      for (const missing of [undefined, null]) {
        const model = buildPickerModel({ catalog: missing })
        expect(model.popular).toEqual([])
        expect(model.letterGroups).toEqual([])
        expect(model.allProvidersCount).toBe(0)
        expect(model.hasMatches).toBe(false)
        // Custom endpoint survives a dead catalog — onboarding must still proceed.
        expect(pickerRowSequence(model)).toEqual([{ kind: 'custom', key: CUSTOM_ENDPOINT_ROW_ID }])
      }
    })

    it('marks a company disabled only when every variant is unsupported', () => {
      const mixed: CatalogProvider[] = [
        catalogProvider({
          id: 'mix-a',
          name: 'Mix A',
          company: 'Mix',
          api: '',
          tier: 'unsupported',
          unsupported_reason: 'deployment-url',
        }),
        catalogProvider({ id: 'mix-b', name: 'Mix B', company: 'Mix', api: 'https://api.mix.test/v1' }),
      ]
      const [row] = toCompanyRows(mixed)
      expect(row.disabled).toBe(false)
      expect(row.unsupportedReason).toBeUndefined()
      const [allUnsupported] = toCompanyRows([mixed[0]])
      expect(allUnsupported.disabled).toBe(true)
      expect(allUnsupported.unsupportedReason).toBe('deployment-url')
    })
  })
})
