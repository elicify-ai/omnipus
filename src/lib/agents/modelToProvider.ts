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
 * @param providers — the caller's already-filtered provider list
 *   (`providerStatus.isProviderUsable` — connected OR signed_in; the
 *   fallback editor only ever sees usable providers).
 * @returns A `useMemo`-stable `Record<model, providerId>` plus a small
 *   lookup helper for single-model queries.
 */
export interface ModelToProviderResult { // not-wire-format: hook return shape (Record + lookup fn), never crosses the gateway boundary
  /** `model → providerId` map. Empty string values are not stored (the
   *  map only contains entries that resolved to a non-empty id). */
  byModel: Record<string, string>
  /** Convenience lookup: returns the provider id for a model or `''`
   *  if no usable provider advertises it. */
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

/**
 * H6 R2 (review round 1 — F3): the DISPLAY counterpart of `lookup` for values
 * whose provider was never recorded (a transcript seed, a pre-O3 agent row).
 *
 * Unlike `lookup`'s deliberate first-wins, this resolves a provider ONLY when
 * EXACTLY ONE usable provider advertises the model: two providers serving the
 * same slug make list order a guess, not evidence — the picker then shows NO
 * chosen row (the honest "provider unknown" state) instead of inventing a
 * selection. Zero providers → '' as well.
 *
 * Not a wire helper — display only (`valueProvider`), never sent anywhere.
 */
export function resolveUniqueModelProvider(providers: Provider[], model: string): string {
  const slug = model.trim()
  if (!slug) return ''
  let found: string | null = null
  for (const p of providers) {
    const id = p.id ?? ''
    if (!id) continue
    if (!(p.models ?? []).includes(slug)) continue
    if (found !== null && found !== id) return '' // two providers serve it — ambiguous
    found = id
  }
  return found ?? ''
}