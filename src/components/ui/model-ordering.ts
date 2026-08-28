// model-ordering.ts — ADR-068 FR-030 / §5 item 4: how a provider's catalog
// models are ordered in the selector, and which of them earn the
// "Recommended for chat" chip.
//
// Two rules, both data-driven so a catalog refresh changes the UI with no code
// change:
//
//   Ordering — group by vendor, newest release first inside each group, undated
//   models last (id ascending). OpenRouter lists 359 models from a dozen
//   vendors; ungrouped that is a wall, and alphabetical puts a 2024 model above
//   this year's flagship.
//
//   Recommended — at most three per provider, and only models that can actually
//   run an agent turn: tool calling, a >=128k window, and still active
//   upstream. It is a hint, never a selection (nothing is pre-selected in
//   onboarding, per operator direction).
//
// Pure functions only (T068-19 DoD) — no React. Inputs are the generated
// CatalogModel type (Constraint #8), never a hand-written shape.

import type { CatalogModel } from '@/lib/api/generated/openapi-types'

/** FR-030 / MIN-002: the window a model needs to carry a Recommended chip. */
export const RECOMMENDED_MIN_CONTEXT_WINDOW = 128000

/** FR-030: never more than this many chips per provider. */
export const MAX_RECOMMENDED_CHIPS = 3

/** The chip's copy, so the component and the tests cannot drift apart. */
export const RECOMMENDED_CHIP_LABEL = 'Recommended for chat'

/** FR-030: above this many models the selector list must be virtualised. */
export const MODEL_VIRTUALISATION_THRESHOLD = 100

export interface ModelVendorGroup {
  /** Grouping key — the id's vendor prefix, or the fallback for bare ids. */
  vendor: string
  models: CatalogModel[]
}

/**
 * The vendor a model belongs to. Aggregators namespace their ids
 * ("anthropic/claude-sonnet-4.6"), so the segment before the first slash is the
 * vendor. A bare id ("gpt-5") comes from a single-vendor provider — the caller
 * passes that provider's company as the fallback label.
 */
export function vendorOfModel(model: CatalogModel, fallbackVendor = ''): string {
  const slash = model.id.indexOf('/')
  if (slash > 0) return model.id.slice(0, slash)
  return fallbackVendor
}

/**
 * Compare two models inside one vendor group: `release_date` descending, then
 * undated last, then id ascending as the tie-break (FR-030; dataset rows 8
 * and 9). Dates are ISO `YYYY-MM-DD`, so a string compare is a date compare.
 */
export function compareModelsWithinGroup(a: CatalogModel, b: CatalogModel): number {
  const aDate = a.release_date ?? ''
  const bDate = b.release_date ?? ''
  if (aDate !== bDate) {
    if (aDate === '') return 1
    if (bDate === '') return -1
    return aDate < bDate ? 1 : -1
  }
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
}

/**
 * FR-030 ordering: vendor groups (alphabetical by vendor, case-insensitive, so
 * the order does not depend on catalog insertion order), each group ordered by
 * `compareModelsWithinGroup`. Never mutates the input array.
 */
export function orderModels(
  models: readonly CatalogModel[],
  options: { fallbackVendor?: string } = {},
): ModelVendorGroup[] {
  const fallback = options.fallbackVendor ?? ''
  const groups = new Map<string, CatalogModel[]>()
  for (const model of models) {
    const vendor = vendorOfModel(model, fallback)
    const bucket = groups.get(vendor)
    if (bucket) bucket.push(model)
    else groups.set(vendor, [model])
  }
  return Array.from(groups.entries())
    .map(([vendor, bucket]) => ({ vendor, models: [...bucket].sort(compareModelsWithinGroup) }))
    .sort((a, b) => {
      const av = a.vendor.toLowerCase()
      const bv = b.vendor.toLowerCase()
      if (av !== bv) return av < bv ? -1 : 1
      return 0
    })
}

/** The same ordering, flattened — the sequence the selector renders top to bottom. */
export function orderedModelList(
  models: readonly CatalogModel[],
  options: { fallbackVendor?: string } = {},
): CatalogModel[] {
  return orderModels(models, options).flatMap((group) => group.models)
}

/**
 * FR-030 / MIN-002 eligibility: tool calling AND a context window of at least
 * 128,000 AND still active upstream. All three, no substitutions — a 1M-window
 * model that cannot call tools is not recommendable for chat.
 */
export function isRecommendedEligible(model: CatalogModel): boolean {
  return (
    model.tool_call === true &&
    model.context_window >= RECOMMENDED_MIN_CONTEXT_WINDOW &&
    model.status === 'active'
  )
}

/**
 * The <=3 models that carry the chip: eligible models, newest release first,
 * undated last, id ascending on a tie — so "one too many" drops the oldest.
 */
export function selectRecommendedModels(models: readonly CatalogModel[]): CatalogModel[] {
  return models
    .filter(isRecommendedEligible)
    .sort(compareModelsWithinGroup)
    .slice(0, MAX_RECOMMENDED_CHIPS)
}

/** Ids only — the shape a renderer wants for a `has-chip` lookup. */
export function recommendedModelIds(models: readonly CatalogModel[]): string[] {
  return selectRecommendedModels(models).map((model) => model.id)
}

/** FR-030: virtualise above 100 items (100 renders whole, 101 does not). */
export function shouldVirtualiseModelList(count: number): boolean {
  return count > MODEL_VIRTUALISATION_THRESHOLD
}
