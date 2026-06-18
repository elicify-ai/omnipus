// W6-C4 / G12 — model picker validation.
//
// The AgentProfile model picker accepts free-text slugs via the "Use <slug>"
// row. If the slug doesn't correspond to any model exposed by a connected
// provider, the runtime chat call will fail. This module is the TS twin of
// `IsKnownModel` in `pkg/agent/model_resolution.go` — the two MUST stay in
// sync so the UI's "unresolved" chip and the chat runtime agree on what
// "known" means.
//
// Matching rules (mirror the Go helper exactly):
//
//   1. Empty / whitespace slug → false (nothing to validate).
//   2. Exact case-insensitive match against `provider.model` (the upstream
//      model ID as fetched from /models — typically the protocol-prefixed
//      form, e.g. "anthropic/claude-haiku-4-5").
//   3. Match against the model ID extracted from `provider.model` via
//      `extractProtocolTail` — catches bare slugs ("claude-haiku-4-5")
//      when the provider exposes the protocol-prefixed form. Same case
//      insensitivity.
//   4. Match against `provider.name` and `provider.display_name` (provider
//      identity is the next best signal — a passthrough like openrouter
//      exposes arbitrary upstream slugs, so even a passthrough provider
//      listing a model under its own name still counts as "known").
//
// A passthrough provider (openrouter / vivgrid) does NOT auto-validate
// arbitrary slugs — `isKnownModelSlug` is the conservative "explicitly
// configured" check. The free-text "Use <slug>" path still works, the
// "unresolved" chip is the user-facing signal that the runtime may not
// have a route. That matches G12's product intent: "show a warning; don't
// block the save".

/**
 * Minimal shape the validators need. We don't import the full `Provider`
 * from openapi-types here so this module is unit-testable without a full
 * Zod runtime; callers pass the same fields they already have on hand
 * from `fetchProviders()` (`src/lib/api.ts`).
 */
export interface ProviderForValidation {
  id: string
  name: string
  display_name?: string
  status: 'connected' | 'disconnected' | 'error'
  models: string[]
}

/**
 * Reports whether `slug` corresponds to a model exposed by any provider
 * in `providers`. The first connected provider that lists the slug wins;
 * disconnected/error providers are NOT trusted — a user with a stale
 * "disconnected openrouter" entry should still see the unresolved chip
 * until they reconnect it. (Otherwise we'd be hiding the very problem
 * G12 was filed against.)
 */
export function isKnownModelSlug(
  slug: string,
  providers: readonly ProviderForValidation[],
): boolean {
  const needle = slug.trim().toLowerCase()
  if (needle === '') return false
  if (!providers) return false

  for (const provider of providers) {
    if (provider.status !== 'connected') continue
    for (const raw of provider.models) {
      if (raw.trim().toLowerCase() === needle) return true
      const tail = extractProtocolTail(raw)
      if (tail !== '' && tail.toLowerCase() === needle) return true
    }
    // Some providers surface the slug only via display_name / name (rare,
    // but happens for legacy single-model entries). Same case insensitivity.
    if (provider.name.trim().toLowerCase() === needle) return true
    if (
      provider.display_name !== undefined &&
      provider.display_name.trim().toLowerCase() === needle
    ) {
      return true
    }
  }
  return false
}

/**
 * Convenience helper for callers that already have the flat model list
 * (the legacy `availableModels` shape AgentProfile derives via
 * `connectedProviders.flatMap(p => p.models)`). Avoids re-walking providers
 * when the caller has already done the work.
 */
export function isKnownModelSlugInList(
  slug: string,
  models: readonly string[],
): boolean {
  const needle = slug.trim().toLowerCase()
  if (needle === '') return false
  for (const raw of models) {
    if (raw.trim().toLowerCase() === needle) return true
    const tail = extractProtocolTail(raw)
    if (tail !== '' && tail.toLowerCase() === needle) return true
  }
  return false
}

/**
 * Extract the model ID portion of a protocol-prefixed slug
 * ("anthropic/claude-haiku-4-5" → "claude-haiku-4-5"). Returns the
 * original string when no "/" is present (already a bare slug).
 *
 * This mirrors `providers.ExtractProtocol` in the Go helper. We intentionally
 * do NOT special-case vendor prefixes like "z-ai/" — those are model-vendor
 * slugs the passthrough provider routes verbatim, and the bare-slug lookup
 * still works after `extractProtocolTail` strips the prefix.
 */
export function extractProtocolTail(model: string): string {
  const trimmed = model.trim()
  const slash = trimmed.indexOf('/')
  if (slash === -1) return trimmed
  // Strip the protocol/vendor prefix; everything after the first "/" is the
  // model ID (vendors may themselves embed slashes — e.g. "openai/gpt-4o-mini"
  // → "gpt-4o-mini", not ["gpt-4o", "mini"]).
  return trimmed.slice(slash + 1)
}