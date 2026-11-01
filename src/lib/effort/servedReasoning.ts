// servedReasoning.ts — finds a model's SERVED `reasoning {levels, switch}`
// object in the providers catalog (effort-levels-spec-r10.md, "Presentation
// by capability", FR-035, FR-049, FR-052) and derives the effort items the
// shared picker offers (S-07, S-08, D8 order).
//
// Effort binds to the (provider, model) pair (Edge Cases: two providers may
// serve the same model name), so the lookup is by pair. A slot stored without
// its provider (old recap data, #1291) resolves only when exactly one catalog
// provider serves the model; otherwise the row is unknown and no effort
// control is offered until the provider is chosen (FR-063).

import type { ProvidersCatalog } from '@/lib/api'
import type { ServedReasoning } from './capabilities'
import { classifyEffortPresentation, type EffortPresentation } from './capabilities'
import { effortLabel, PROVIDER_DEFAULT, type EffortTarget } from './mapEffort'

/** The catalog row of one (provider, model) pair: absent (no row) or its served reasoning (null = does not reason). */
export interface ServedRow {
  found: boolean
  reasoning: ServedReasoning | null
}

type CatalogModel = ProvidersCatalog['providers'][number]['models'][number]

const NO_ROW: ServedRow = { found: false, reasoning: null }

function reasoningOf(model: CatalogModel): ServedReasoning | null {
  return model.reasoning ?? null
}

/** Looks up the served catalog row for a pair. `provider` "" means unknown (unique-model fallback). */
export function findServedRow(catalog: ProvidersCatalog | undefined, provider: string, model: string): ServedRow {
  if (!catalog || model === '') return NO_ROW
  if (provider !== '') {
    const entry = catalog.providers.find((p) => p.id === provider)
    const row = entry?.models.find((m) => m.id === model)
    return row ? { found: true, reasoning: reasoningOf(row) } : NO_ROW
  }
  const matches = catalog.providers.flatMap((p) => p.models.filter((m) => m.id === model))
  return matches.length === 1 ? { found: true, reasoning: reasoningOf(matches[0]) } : NO_ROW
}

/** Presentation for a served row: no row or no object → none. */
export function presentationOf(row: ServedRow): EffortPresentation {
  return row.found ? classifyEffortPresentation(row.reasoning) : 'none'
}

/** The mapping target for a served row (a missing row or object does not reason). */
export function effortTargetOf(row: ServedRow): EffortTarget {
  if (!row.found || row.reasoning === null) return { levels: [], switch: false, reasons: false }
  return {
    levels: Array.isArray(row.reasoning.levels) ? row.reasoning.levels : [],
    switch: row.reasoning.switch === true,
    reasons: true,
  }
}

export interface EffortItem {
  /** The stored value this item writes. */
  value: string
  /** The radio's visible text. */
  label: string
}

/**
 * The model's own effort items, in picker order (S-07, S-08): Off through the
 * switch when the levels have no `none` (levels plus switch: Off only; switch
 * only: Off, On), then the levels as published (`none` reads Off).
 */
export function levelItems(target: EffortTarget): EffortItem[] {
  const items: EffortItem[] = []
  const hasNone = target.levels.includes('none')
  if (target.switch && !hasNone) items.push({ value: 'off', label: 'Off' })
  for (const level of target.levels) items.push({ value: level, label: effortLabel(level) })
  if (target.switch && target.levels.length === 0) items.push({ value: 'on', label: 'On' })
  return items
}

/** The full item list: optional Not set row, Provider default, then the model's items. */
export function effortItems(target: EffortTarget, notSetLabel: string | null): EffortItem[] {
  const items: EffortItem[] = []
  if (notSetLabel !== null) items.push({ value: '', label: notSetLabel })
  items.push({ value: PROVIDER_DEFAULT, label: 'Provider default' })
  return items.concat(levelItems(target))
}

/** The item a stored value selects (`off` and `none` are the same Off); undefined when not offered. */
export function itemForStored(items: readonly EffortItem[], stored: string): EffortItem | undefined {
  const exact = items.find((item) => item.value === stored)
  if (exact) return exact
  if (stored === 'off' || stored === 'none') return items.find((item) => item.value === 'off' || item.value === 'none')
  return undefined
}
