// providerCatalog — per-provider model-catalogue mode resolution.
//
// UAT fix (model-catalog): the model selector must be a *constrained* dropdown.
// A model that is not in the provider's catalogue must not be selectable, so the
// always-on "unresolved" warning can never fire for a catalogue-fed picker.
//
// Each provider is in one of two catalogue modes:
//
//   'live'   — the provider exposes a live upstream `/models` endpoint
//              (openrouter / openai / anthropic / gemini / groq / …). The
//              catalogue is the real-time list fetched by the backend and
//              returned in `Provider.models`. The user picks from it; they
//              cannot type a free-text slug.
//
//   'manual' — the provider has no listing endpoint (ollama / vllm / litellm /
//              azure / bedrock / a custom OpenAI-compatible endpoint). The
//              catalogue is whatever model slugs the user has added in the
//              provider config; those slugs become `Provider.models`. The user
//              curates the list in Settings → Providers and then picks from it.
//
// Backend contract (Provider.yaml — owned by the backend agent):
//   - `has_models_endpoint?: boolean` is the authoritative signal:
//       true  → 'live', false → 'manual'.
//   - When the field is absent (older gateway) we fall back to an id-based
//     heuristic so the FE degrades gracefully against legacy gateways.
//   - `ProviderUpdateRequest.models?: string[]` replaces the manual slug list
//     for a 'manual' provider (consumed by `configureProvider`).

import type { Provider } from '@/lib/api/generated/openapi-types'

export type CatalogMode = 'live' | 'manual'

/**
 * Provider protocol ids that expose a live upstream `/models` listing endpoint.
 * Used ONLY as the fallback when the backend has not (yet) sent
 * `has_models_endpoint` (legacy gateway). Mirrors the set of protocols the
 * gateway can probe for a model list. Keep conservative: an unknown id defaults
 * to 'manual' so we never promise a live list we cannot fetch.
 */
const LIVE_LISTING_PROVIDER_IDS: ReadonlySet<string> = new Set([
  'openrouter',
  'openai',
  'anthropic',
  'anthropic-messages',
  'gemini',
  'google',
  'groq',
  'mistral',
  'deepseek',
  'moonshot',
  'zhipu',
  'cerebras',
  'novita',
  'nvidia',
  'qwen',
  'qwen-intl',
  'qwen-international',
  'dashscope-intl',
  'minimax',
])

/**
 * Resolve the catalogue mode for a provider.
 *
 * Prefers the backend's `has_models_endpoint` signal; falls back to the
 * id-based heuristic when the field is absent (legacy gateway).
 */
export function providerCatalogMode(provider: Provider): CatalogMode {
  if (typeof provider.has_models_endpoint === 'boolean') {
    return provider.has_models_endpoint ? 'live' : 'manual'
  }
  return LIVE_LISTING_PROVIDER_IDS.has(provider.id) ? 'live' : 'manual'
}

/** Convenience predicate — true when the provider's catalogue is a live list. */
export function isLiveListingProvider(provider: Provider): boolean {
  return providerCatalogMode(provider) === 'live'
}
