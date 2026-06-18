import { useMemo } from 'react'
import type { Provider } from '@/lib/api/generated/openapi-types'

/**
 * W6-C2 / I9 — Provider lookup for the fallback editor.
 *
 * Builds a `model → provider-id` map from the connected providers list.
 *
 * **Wire contract:** the `provider` field on `FallbackModel` is the
 * **provider routing key** (e.g. `"openrouter"`, `"anthropic"` — see
 * `contracts/components/schemas/FallbackModel.yaml`, mirrored in
 * `openapi-types.ts:3437`), NOT a display name. Pre-I9 the editor
 * stored `display_name ?? name ?? id` in the chip and emitted the same
 * to the wire, which silently downgraded `provider` to the brand label
 * and broke runtime resolution for any fallback whose display name
 * differed from its routing key.
 *
 * This helper returns **only the provider id** so the chip's `provider`
 * field is the wire value 1:1. The display name is layered separately
 * (UI-only) at render time.
 *
 * @param providers — full providers list as returned by `fetchProviders`.
 *   `status === 'connected'` filters are applied by the caller (the
 *   fallback editor only ever sees connected providers anyway).
 * @returns A `useMemo`-stable `Record<model, providerId>` plus a small
 *   lookup helper for single-model queries.
 */
export interface ModelToProviderResult {
  /** `model → providerId` map. Empty string values are not stored (the
   *  map only contains entries that resolved to a non-empty id). */
  byModel: Record<string, string>
  /** Convenience lookup: returns the provider id for a model or `''`
   *  if no connected provider advertises it. */
  lookup: (model: string) => string
}

export function buildModelToProvider(providers: Provider[]): ModelToProviderResult {
  const byModel: Record<string, string> = {}
  for (const p of providers) {
    const id = p.id ?? ''
    if (!id) continue
    for (const m of p.models ?? []) {
      // First-wins on purpose: if the same slug is advertised by two
      // connected providers, the first in the list owns the chip. This
      // matches `ModelSelector`'s render order (it also iterates the
      // provider list in the same order) so the displayed and stored
      // provider line up.
      if (!(m in byModel)) {
        byModel[m] = id
      }
    }
  }
  return {
    byModel,
    lookup: (model: string) => byModel[model] ?? '',
  }
}

/**
 * React hook wrapper around `buildModelToProvider`. Recomputes when the
 * provider list changes (connection toggle, model catalogue refresh).
 */
export function useModelToProvider(providers: Provider[]): ModelToProviderResult {
  return useMemo(() => buildModelToProvider(providers), [providers])
}