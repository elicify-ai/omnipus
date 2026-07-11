// browserLiveUrl — pure URL-normalization helper for the live browser panel's
// address bar (ADR-039 D-A2). Deliberately minimal: it only prepends a scheme
// when one is missing, so a bare "example.com" typed into the address bar
// becomes a navigable URL instead of silently failing to parse/resolve.
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
