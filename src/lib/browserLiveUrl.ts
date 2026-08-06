// browserLiveUrl — pure URL-normalization helpers for the live browser
// panel's always-visible omnibox (ADR-039 D-A2, ADR-040 D5). Deliberately
// minimal: normalizeNavigateUrl only prepends a scheme when one is missing,
// so a bare "example.com" typed into the address bar becomes a navigable URL
// instead of silently failing to parse/resolve; resolveOmniboxInput layers a
// Chrome-omnibox-style URL-vs-search decision on top of it.
//
// This is NOT a security check. The server-side `BrowserManager.ValidateURL`
// SSRF gate (run on every `navigate` input frame, per ADR-039 D-A2) is the
// sole authority on which schemes/hosts may actually be navigated to — this
// client-side step exists purely for address-bar ergonomics.

/**
 * Normalizes a user-typed address-bar value into a navigable URL string.
 * - Trims surrounding whitespace; returns null for an empty/whitespace-only input.
 * - Leaves an input that already declares an explicit "scheme://" prefix untouched
 *   (e.g. "http://example.com", "https://example.com").
 * - Otherwise prepends "https://".
 *
 * A bare "scheme:" with no "//" (e.g. a "javascript:alert(1)" paste) is
 * treated as schemeless by design — it does not match the scheme regex below
 * (which requires "://"), so it gets "https://" prepended, neutralizing it
 * into an inert (and harmless) URL rather than honouring it. The server-side
 * gate is still the real enforcement point; this is defense in depth.
 */
export function normalizeNavigateUrl(raw: string): string | null {
  const trimmed = raw.trim()
  if (!trimmed) return null
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(trimmed)) {
    return trimmed
  }
  return `https://${trimmed}`
}

/**
 * ADR-040 D5 — resolves a value typed into the always-visible omnibox into a
 * navigable URL, exactly the way Chrome's address bar decides between
 * "navigate" and "search":
 * - empty/whitespace-only → null (nothing to do).
 * - an explicit "scheme://" prefix, OR no whitespace AND at least one "." →
 *   treated as a URL/bare domain and normalized via `normalizeNavigateUrl`
 *   (which prepends "https://" when no scheme is present).
 * - anything else (contains a space, or has no "." — e.g. "cheap flights to
 *   tokyo", or a single bare word like "wikipedia") → a DuckDuckGo search for
 *   the text. DuckDuckGo, not Google, so the omnibox agrees with the Omnipus
 *   start page, whose own search form already posts to duckduckgo.com
 *   (pkg/gateway/browser_start_page.go) — two different engines depending on
 *   whether you typed into the page or the address bar was a split-brain the
 *   user could see. It also fits the product's no-telemetry posture, and in
 *   practice Google far more readily serves a CAPTCHA to a headless,
 *   datacentre-IP browser, which is a failure the user experiences as "search
 *   is broken". Only OUTER whitespace is trimmed (matching `normalizeNavigateUrl`'s
 *   own `.trim()`) — interior whitespace between words is preserved exactly
 *   as typed, not collapsed, so "cheap   flights" is searched with its
 *   original spacing rather than being normalized to single spaces.
 *
 * Deliberately does NOT special-case "host:port" (e.g. "localhost:3000")
 * as a URL the way `normalizeNavigateUrl` alone might suggest — ADR-040 D5
 * defines the boundary strictly as "has a dot, no spaces, or an explicit
 * scheme", so a bare host:port with no dot resolves to a search, matching
 * the documented decision exactly (the user can still reach it by typing
 * "http://localhost:3000", which has an explicit scheme).
 */
export function resolveOmniboxInput(raw: string): string | null {
  const trimmed = raw.trim()
  if (!trimmed) return null
  const hasExplicitScheme = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(trimmed)
  const looksLikeUrl = hasExplicitScheme || (!/\s/.test(trimmed) && trimmed.includes('.'))
  if (looksLikeUrl) {
    return normalizeNavigateUrl(trimmed)
  }
  return `https://duckduckgo.com/?q=${encodeURIComponent(trimmed)}`
}
