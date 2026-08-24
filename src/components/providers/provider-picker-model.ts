// provider-picker-model.ts — ADR-068 FR-022/023/024: the data model behind the
// shared provider picker (onboarding step 3 and the Settings sheet render the
// same component over this).
//
// Everything the picker shows is derived from the catalog document, never from
// a SPA constant (spec resolution #1): which providers are Popular tiles is the
// catalog's `tier`, the tile order is catalog order, and the grouping key is
// the catalog's `company` (ADR-067 X-10). Change the catalog and the picker
// changes with no code edit — that is the property the "changing the fixture so
// groq is standard and cerebras is popular re-renders the band" scenario pins.
//
// Pure functions only (T068-19 DoD) — no React, no fetching. The caller passes
// the catalog document, the operator's already-configured providers, and the
// current query.

import type {
  CatalogProvider,
  Provider,
  ProvidersCatalog,
} from '@/lib/api/generated/openapi-types'

/** FR-022: at most three Recent entries, most recently updated first. */
export const MAX_RECENT_PROVIDERS = 3

/**
 * FR-022 / FR-024: the permanent last row. The saved row is recognised
 * everywhere by `Provider.custom: true`, never by this literal id (X-13) —
 * this id addresses the ROW in the picker, not a provider.
 */
export const CUSTOM_ENDPOINT_ROW_ID = '__custom_endpoint__'

/** The custom row's copy, shared by the component and its tests. */
export const CUSTOM_ENDPOINT_LABEL = 'Custom endpoint'

/** The letter group that collects every company not starting with A-Z. */
export const OTHER_LETTER_GROUP = '#'

/** One picker row: a company, with its plan x region variants behind it. */
export interface PickerCompanyRow {
  /** Grouping key and stable row key — the catalog's `company` (X-10). */
  company: string
  /** Every catalog entry sharing this company, in catalog order. */
  variants: CatalogProvider[]
  /** The variant a one-click selection lands on: the first, in catalog order. */
  primary: CatalogProvider
  /** True when EVERY variant is `tier: unsupported` — shown, but disabled (FR-025). */
  disabled: boolean
  /** Present when `disabled`; the raw catalog code, mapped to copy by the UI. */
  unsupportedReason?: CatalogProvider['unsupported_reason']
  /** Distinct plan labels across the variants, catalog order. */
  plans: string[]
  /** Distinct region codes across the variants, catalog order. */
  regions: string[]
  /** Every alias of every variant, catalog order, de-duplicated. */
  aliases: string[]
  /** True when any variant carries `tier: popular` — this company is a tile. */
  popular: boolean
}

/** One entry in the Recent section: a configured provider, catalog row attached. */
export interface PickerRecentRow {
  provider: Provider
  catalog?: CatalogProvider
  /** What the row reads: the configured display name, falling back to `name`. */
  label: string
}

/** One letter header and the company rows under it. */
export interface PickerLetterGroup {
  /** "A".."Z", or "#" for everything else (digits, CJK, punctuation). */
  letter: string
  rows: PickerCompanyRow[]
}

export type PickerRowKind = 'popular' | 'recent' | 'company' | 'custom'

/** A position in the picker's flat keyboard order. */
export interface PickerRowRef {
  kind: PickerRowKind
  /** Company name, configured provider id, or CUSTOM_ENDPOINT_ROW_ID. */
  key: string
}

export interface PickerModel {
  /** The query as given. */
  query: string
  /** The query with surrounding whitespace removed — what FR-033/FR-022 test. */
  trimmedQuery: string
  /** FR-022: the list is collapsed until the trimmed query is non-empty (or the operator expands it). */
  expanded: boolean
  /** One tile per company whose variants include a `tier: popular` provider, catalog order. */
  popular: PickerCompanyRow[]
  /** Configured providers by `updated_at` desc, at most MAX_RECENT_PROVIDERS. */
  recent: PickerRecentRow[]
  /** The expanded list: letter groups A-Z then "#", each already query-filtered. */
  letterGroups: PickerLetterGroup[]
  /** Company rows surviving the query, across all letter groups. */
  matchCount: number
  /** True when at least one company row matched. */
  hasMatches: boolean
  /** FR-022's "All providers (N)" number: catalog entries that are not Popular tiles. */
  allProvidersCount: number
  /** The no-match copy, present only when a non-empty query matched nothing. */
  emptyMessage?: string
}

export interface BuildPickerModelInput {
  catalog: ProvidersCatalog | undefined | null
  /** The operator's configured providers, for the Recent section. Optional. */
  configured?: readonly Provider[]
  query?: string | null
  /** True when the operator expanded "All providers" without typing. */
  expandedByOperator?: boolean
}

/** Trim + lower-case; the picker treats the query literally (FR-024, X-10). */
export function normalizePickerQuery(query: string | null | undefined): string {
  return (query ?? '').trim().toLocaleLowerCase()
}

/** The letter header a company belongs under (FR-023). */
export function companyLetter(company: string): string {
  const first = company.trim().charAt(0).toLocaleUpperCase()
  return first >= 'A' && first <= 'Z' ? first : OTHER_LETTER_GROUP
}

/**
 * FR-024: a row matches when the query is a literal, case-insensitive substring
 * of its company, any variant's name, plan, region, or any alias. `includes` —
 * never a RegExp — so "(*[" is four ordinary characters, not a syntax error.
 */
export function providerRowMatchesQuery(row: PickerCompanyRow, query: string): boolean {
  const needle = normalizePickerQuery(query)
  if (needle.length === 0) return true
  const haystacks = [row.company, ...row.aliases, ...row.plans, ...row.regions]
  for (const variant of row.variants) {
    haystacks.push(variant.name)
  }
  return haystacks.some((field) => field.toLocaleLowerCase().includes(needle))
}

/** Group the catalog into one row per company, preserving catalog order. */
export function toCompanyRows(providers: readonly CatalogProvider[]): PickerCompanyRow[] {
  const byCompany = new Map<string, CatalogProvider[]>()
  for (const provider of providers) {
    const bucket = byCompany.get(provider.company)
    if (bucket) bucket.push(provider)
    else byCompany.set(provider.company, [provider])
  }
  return Array.from(byCompany.entries()).map(([company, variants]) => {
    const plans = distinct(variants.map((v) => v.plan))
    const regions = distinct(variants.map((v) => v.region))
    const aliases = distinct(variants.flatMap((v) => v.aliases ?? []))
    const unsupported = variants.filter((v) => v.tier === 'unsupported')
    const disabled = unsupported.length === variants.length && variants.length > 0
    return {
      company,
      variants,
      primary: variants[0],
      disabled,
      unsupportedReason: disabled ? unsupported[0].unsupported_reason : undefined,
      plans,
      regions,
      aliases,
      popular: variants.some((v) => v.tier === 'popular'),
    }
  })
}

/**
 * The whole picker in one object.
 *
 * Section order is FR-022's, and the sections are independent: the Popular band
 * and Recent never move or shrink as the operator types (they are the escape
 * hatch back to the common case), while the letter-grouped list is what the
 * query filters.
 */
export function buildPickerModel(input: BuildPickerModelInput): PickerModel {
  const providers = input.catalog?.providers ?? []
  const query = input.query ?? ''
  const trimmedQuery = query.trim()
  const needle = normalizePickerQuery(query)

  const rows = toCompanyRows(providers)
  const popular = rows.filter((row) => row.popular)

  const matching = needle.length === 0 ? rows : rows.filter((row) => providerRowMatchesQuery(row, needle))
  const letterGroups = toLetterGroups(matching)

  const popularProviderCount = providers.filter((p) => p.tier === 'popular').length
  const hasMatches = matching.length > 0

  return {
    query,
    trimmedQuery,
    expanded: trimmedQuery.length > 0 || input.expandedByOperator === true,
    popular,
    recent: toRecentRows(input.configured ?? [], providers),
    letterGroups,
    matchCount: matching.length,
    hasMatches,
    allProvidersCount: providers.length - popularProviderCount,
    emptyMessage:
      trimmedQuery.length > 0 && !hasMatches ? `No provider matches ${trimmedQuery}` : undefined,
  }
}

/**
 * The picker's flat keyboard order — what End lands on and what ArrowDown walks
 * (FR-026's Home/End/typeahead by index). *Custom endpoint* is always the last
 * element, whatever the query (FR-022, "Custom endpoint is last").
 */
export function pickerRowSequence(model: PickerModel): PickerRowRef[] {
  const sequence: PickerRowRef[] = model.popular.map((row) => ({
    kind: 'popular' as const,
    key: row.company,
  }))
  for (const recent of model.recent) {
    sequence.push({ kind: 'recent', key: recent.provider.id })
  }
  if (model.expanded) {
    for (const group of model.letterGroups) {
      for (const row of group.rows) {
        sequence.push({ kind: 'company', key: row.company })
      }
    }
  }
  sequence.push({ kind: 'custom', key: CUSTOM_ENDPOINT_ROW_ID })
  return sequence
}

function toLetterGroups(rows: readonly PickerCompanyRow[]): PickerLetterGroup[] {
  const byLetter = new Map<string, PickerCompanyRow[]>()
  for (const row of rows) {
    const letter = companyLetter(row.company)
    const bucket = byLetter.get(letter)
    if (bucket) bucket.push(row)
    else byLetter.set(letter, [row])
  }
  return Array.from(byLetter.entries())
    .map(([letter, bucket]) => ({
      letter,
      rows: [...bucket].sort((a, b) => compareCompanies(a.company, b.company)),
    }))
    .sort((a, b) => {
      // A-Z first, in order; "#" last (FR-023).
      if (a.letter === b.letter) return 0
      if (a.letter === OTHER_LETTER_GROUP) return 1
      if (b.letter === OTHER_LETTER_GROUP) return -1
      return a.letter < b.letter ? -1 : 1
    })
}

function toRecentRows(
  configured: readonly Provider[],
  providers: readonly CatalogProvider[],
): PickerRecentRow[] {
  const catalogById = new Map(providers.map((p) => [p.id, p]))
  return [...configured]
    .sort((a, b) => {
      const aAt = a.updated_at ?? ''
      const bAt = b.updated_at ?? ''
      if (aAt !== bAt) {
        // Never-updated rows sort last rather than pretending to be oldest.
        if (aAt === '') return 1
        if (bAt === '') return -1
        return aAt < bAt ? 1 : -1
      }
      return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
    })
    .slice(0, MAX_RECENT_PROVIDERS)
    .map((provider) => ({
      provider,
      catalog: catalogById.get(provider.id),
      label: provider.display_name && provider.display_name.length > 0 ? provider.display_name : provider.name,
    }))
}

function compareCompanies(a: string, b: string): number {
  const al = a.toLocaleLowerCase()
  const bl = b.toLocaleLowerCase()
  if (al !== bl) return al < bl ? -1 : 1
  return 0
}

function distinct(values: readonly (string | undefined)[]): string[] {
  const out: string[] = []
  for (const value of values) {
    if (value && value.length > 0 && !out.includes(value)) out.push(value)
  }
  return out
}
