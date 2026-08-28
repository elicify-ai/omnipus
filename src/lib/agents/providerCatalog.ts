// providerCatalog — per-provider model-catalogue mode resolution.
//
// UAT fix (model-catalog): the model selector must be a *constrained* dropdown.
// A model that is not in the provider's catalogue must not be selectable, so the
// always-on "unresolved" warning can never fire for a catalogue-fed picker.
//
// Each provider is in one of two catalogue modes:
//
//   'live'   — the gateway fills `Provider.models` for this row: a catalog
//              provider's models come from the served catalog (ADR-067 FR-020,
//              no outbound call) and a `locality: local` provider's from its
//              live endpoint. The user picks from that list; they cannot type a
//              free-text slug.
//
//   'manual' — an operator-named custom row (FR-014). Nothing fills its
//              catalogue, so it is whatever model slugs the user has added in
//              the provider config; those slugs become `Provider.models`. The
//              user curates the list in Settings → Providers and then picks
//              from it.
//
// Backend contract (Provider.yaml): `has_models_endpoint` is the AUTHORITATIVE
// and only signal — true → 'live', false → 'manual'. The schema documents the
// absent case as "treat as false (editable slug list)", which is what this does.
//
// ADR-067 T067-13 deleted the id-based fallback this module used to carry: a
// hand-written set of ~20 provider ids (including ids the registry retired,
// e.g. `z-ai`, `gemini`, `zhipu`) consulted whenever `has_models_endpoint` was
// absent. It was a second, stale provider catalog compiled into the SPA —
// exactly what FR-011 / FR-025 remove. Provider identity is the served
// catalog's, and the mode is the gateway's own signal, never a guess from an id.
//
//   - `ProviderUpdateRequest.models?: string[]` replaces the manual slug list
//     for a 'manual' provider (consumed by `configureProvider`).

import type { Provider } from '@/lib/api/generated/openapi-types'

export type CatalogMode = 'live' | 'manual'

/**
 * Resolve the catalogue mode for a provider from the backend's
 * `has_models_endpoint` signal. Absent → 'manual', so the SPA never promises a
 * live list the gateway has not said it can fill.
 */
export function providerCatalogMode(provider: Provider): CatalogMode {
  return provider.has_models_endpoint === true ? 'live' : 'manual'
}

/** Convenience predicate — true when the provider's catalogue is a live list. */
export function isLiveListingProvider(provider: Provider): boolean {
  return providerCatalogMode(provider) === 'live'
}
