/**
 * preview-url.ts — Pure URL-rewrite and path-validation utilities for the
 * iframe preview feature (FR-010, FR-010a, FR-010b, FR-016, FR-017,
 * FR-017a, FR-017b).
 *
 * All three functions are pure (no DOM access, no side effects, no React)
 * so they can be exercised in plain Node.js / Vitest without a browser.
 */

/**
 * Type guard for the base preview result shape shared by `web_serve`,
 * `serve_workspace`, and `run_in_workspace` (the latter two are legacy tool
 * names preserved for transcript replay). All result types have at minimum
 * `path: string` and `url: string`; this guard checks only those two fields
 * so each tool UI can perform its own cast for the additional fields it consumes.
 *
 * @example
 * hasPreviewShape({ path: '/preview/a/b/', url: 'http://...' }) // true (canonical)
 * hasPreviewShape({ path: '/serve/a/b/', url: 'http://...' })   // true (legacy replay)
 * hasPreviewShape({ path: 42, url: 'http://...' })              // false
 * hasPreviewShape(null)                                          // false
 */
export function hasPreviewShape(v: unknown): v is { path: string; url: string } {
  return (
    typeof v === 'object' &&
    v !== null &&
    typeof (v as Record<string, unknown>).path === 'string' &&
    typeof (v as Record<string, unknown>).url === 'string'
  )
}

/**
 * Legacy bind-all hosts that the gateway may place in tool-result URLs.
 * These are never browser-reachable as-is, so we rewrite them to the
 * actual hostname the user is accessing the SPA from.
 *
 * Ambiguity note — WHATWG URL normalisation:
 *   • `http://0:5000/…`    → parsed hostname is `"0.0.0.0"` (normalised).
 *   • `http://[::0]:5000/…` → parsed hostname is `"[::]"` (normalised).
 *   • `http://[::]`:5000/…` → parsed hostname is `"[::]"`.
 *   • `"::"` and `"::0"` without brackets are not valid URL authorities and
 *     cause `new URL()` to throw — they never reach this set.
 *
 * Because the URL constructor performs normalisation before we inspect the
 * hostname, the effective set that ever matches is
 * `{"0.0.0.0", "[::]", "127.0.0.1"}`.
 * The additional entries (`"0"`, `"[::0]"`, `"::"`, `"::0"`) are listed
 * explicitly to match the spec literal and as defence-in-depth in case a
 * future runtime differs in normalisation behaviour.
 */
const LEGACY_HOSTS = new Set([
  '0.0.0.0',
  '0',
  '127.0.0.1',
  '[::]',
  '[::0]',
  '::',
  '::0',
])

/**
 * Validates the `path` field returned by `web_serve` tool results (and the
 * legacy `serve_workspace` / `run_in_workspace` tools kept for replay).
 *
 * The regex enforces:
 *   • Starts with `/preview/` (canonical), `/serve/`, or `/dev/` (legacy back-compat)
 *   • Followed by an agent segment (`[A-Za-z0-9_-]+`)
 *   • Followed by a token segment (`[A-Za-z0-9_-]+`)
 *   • Optionally followed by any additional path segments
 *
 * Notably rejects:
 *   • `javascript:alert(1)` — no leading slash with recognised segment
 *   • `//attacker.com/exfil` — scheme-relative
 *   • `/api/v1/agents` — not a `/preview/`, `/serve/`, or `/dev/` path
 *   • `data:text/html,…` — no leading slash
 *   • `/preview/../../etc/passwd` — `..` is not `[A-Za-z0-9_-]`
 *   • `""` (empty) — does not match
 *
 * Per FR-010b / MR-10.
 */
const PREVIEW_PATH_REGEX = /^\/(?:preview|serve|dev)\/[A-Za-z0-9_\-]+\/[A-Za-z0-9_\-]+(?:\/.*)?$/

/**
 * Returns `true` when `path` is a safe, well-formed preview path that the
 * SPA may use as an iframe `src` suffix.
 *
 * @example
 * validatePreviewPath('/preview/agent-1/abc123/')     // true  (canonical)
 * validatePreviewPath('/serve/agent-1/abc123/')       // true  (legacy back-compat)
 * validatePreviewPath('/dev/agent-2/xyz789/')         // true  (legacy back-compat)
 * validatePreviewPath('javascript:alert(1)')          // false
 * validatePreviewPath('//attacker.com/exfil')         // false
 * validatePreviewPath('/api/v1/agents')               // false
 * validatePreviewPath('data:text/html,...')           // false
 * validatePreviewPath('/preview/../../etc/passwd')    // false
 * validatePreviewPath('')                             // false
 */
export function validatePreviewPath(path: string): boolean {
  return PREVIEW_PATH_REGEX.test(path)
}

/**
 * Rewrites `href` when its host is a legacy bind-all address (FR-016/017).
 *
 * Rules applied in order:
 *  1. Relative paths (`/…`) and scheme-relative URLs (`//…`) are returned
 *     unchanged — detected BEFORE parsing to avoid the WHATWG URL constructor
 *     attaching a placeholder origin.
 *  2. If `href` cannot be parsed as an absolute URL, return `href` unchanged.
 *  3. If the scheme is not `http:` or `https:`, return `href` unchanged
 *     (passes through `mailto:`, `tel:`, `javascript:`, `data:`, etc.).
 *  4. If the parsed `hostname` is NOT in `LEGACY_HOSTS`, return unchanged.
 *  5. Rewrite the host to `hostname` (the caller's `window.location.hostname`).
 *  6. If the path starts with `/preview/` (canonical), `/serve/`, or `/dev/`
 *     (both legacy back-compat paths), also swap the port to `previewPort`.
 *     Otherwise preserve the original port.
 *
 * @param href - The raw href string from the markdown link.
 * @param hostname - The host the user is accessing the SPA from
 *   (`window.location.hostname`). May be a bare IP, a domain, or `localhost`.
 * @param previewPort - The preview listener port advertised by
 *   `GET /api/v1/about` as `preview_port`.
 * @returns The rewritten URL string, or `href` unchanged when no rewrite applies.
 *
 * @example
 * // Canonical /preview/ path, port swap
 * rewriteLegacyURL('http://0.0.0.0:5000/preview/m/t/', '146.190.89.151', 5001)
 * // => 'http://146.190.89.151:5001/preview/m/t/'
 *
 * @example
 * // Legacy /serve/ path, port swap — spec row 1 (back-compat for old transcripts)
 * rewriteLegacyURL('http://0.0.0.0:5000/serve/m/t/', '146.190.89.151', 5001)
 * // => 'http://146.190.89.151:5001/serve/m/t/'
 *
 * @example
 * // Legacy /dev/ path, localhost variant — spec row 2 (back-compat for old transcripts)
 * rewriteLegacyURL('http://0.0.0.0:5000/dev/m/t/', 'localhost', 5001)
 * // => 'http://localhost:5001/dev/m/t/'
 *
 * @example
 * // Non-serve path → main port retained — spec row 3
 * rewriteLegacyURL('http://0.0.0.0:5000/about', '1.2.3.4', 5001)
 * // => 'http://1.2.3.4:5000/about'
 *
 * @example
 * // IPv6 wildcard — spec row 4
 * rewriteLegacyURL('http://[::]:5000/serve/m/t/', '1.2.3.4', 5001)
 * // => 'http://1.2.3.4:5001/serve/m/t/'
 *
 * @example
 * // IPv6 explicit zero — spec row 5
 * rewriteLegacyURL('http://[::0]:5000/serve/m/t/', '1.2.3.4', 5001)
 * // => 'http://1.2.3.4:5001/serve/m/t/'
 *
 * @example
 * // Bare-zero — spec row 6
 * rewriteLegacyURL('http://0:5000/serve/m/t/', '1.2.3.4', 5001)
 * // => 'http://1.2.3.4:5001/serve/m/t/'
 *
 * @example
 * // Loopback rewrite — spec row 7
 * rewriteLegacyURL('http://127.0.0.1:5000/serve/m/t/', '1.2.3.4', 5001)
 * // => 'http://1.2.3.4:5001/serve/m/t/'
 *
 * @example
 * // Foreign host unchanged — spec row 8
 * rewriteLegacyURL('https://example.com/page', '1.2.3.4', 5001)
 * // => 'https://example.com/page'
 *
 * @example
 * // Non-http scheme passes through — spec row 9
 * rewriteLegacyURL('mailto:foo@x.com', '1.2.3.4', 5001)
 * // => 'mailto:foo@x.com'
 *
 * @example
 * // javascript: passes through — spec row 10
 * rewriteLegacyURL('javascript:alert(1)', '1.2.3.4', 5001)
 * // => 'javascript:alert(1)'
 *
 * @example
 * // tel: passes through — spec row 11
 * rewriteLegacyURL('tel:+155512345', '1.2.3.4', 5001)
 * // => 'tel:+155512345'
 *
 * @example
 * // Relative path unchanged — spec row 12
 * rewriteLegacyURL('/relative/path', '1.2.3.4', 5001)
 * // => '/relative/path'
 *
 * @example
 * // Scheme-relative unchanged — spec row 13
 * rewriteLegacyURL('//host.com/x', '1.2.3.4', 5001)
 * // => '//host.com/x'
 *
 * @example
 * // Empty string boundary — spec row 14
 * rewriteLegacyURL('', '1.2.3.4', 5001)
 * // => ''
 *
 * @example
 * // Unparseable passes through without throwing — spec row 15
 * rewriteLegacyURL('not-a-url', '1.2.3.4', 5001)
 * // => 'not-a-url'
 */
export function rewriteLegacyURL(href: string, hostname: string, previewPort: number): string {
  // Rule 1: relative paths and scheme-relative URLs pass through unchanged.
  // Check BEFORE parsing so the WHATWG URL constructor cannot attach a
  // placeholder and produce a false positive.
  if (href.startsWith('/') || href.startsWith('//')) {
    return href
  }

  // Rule 2: try to parse as an absolute URL.
  let parsed: URL
  try {
    parsed = new URL(href)
  } catch {
    // Unparseable → pass through unchanged (spec row 15, empty string row 14).
    return href
  }

  // Rule 3: non-http(s) schemes pass through unchanged.
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    return href
  }

  // Rule 4: check if the parsed hostname is a legacy bind-all host.
  // The WHATWG URL normalises `0` → `0.0.0.0`, `[::0]` → `[::]`, so the
  // set lookup works on normalised values.
  if (!LEGACY_HOSTS.has(parsed.hostname)) {
    return href
  }

  // Rule 5: rewrite the host to the caller's actual hostname.
  // We set `hostname` (not `host`) so we can control the port separately.
  parsed.hostname = hostname

  // Rule 6: if the path is a preview/serve/dev path, swap to the preview port;
  // otherwise preserve the port already in the URL.
  if (
    parsed.pathname.startsWith('/preview/') ||
    parsed.pathname.startsWith('/serve/') ||
    parsed.pathname.startsWith('/dev/')
  ) {
    parsed.port = String(previewPort)
  }
  // (If not a preview/serve/dev path, parsed.port was already preserved by the
  // assignment to `parsed.hostname` above, which does not affect the port.)

  try {
    return parsed.toString()
  } catch {
    return href
  }
}

/**
 * Resolve the effective preview hostname + port the SPA should use when
 * rewriting legacy `0.0.0.0` / `::` URLs in markdown.
 *
 * Priority:
 *   1. `aboutInfo.preview_origin` — operator-configured override (a full URL).
 *      We extract hostname + port from it. If the hostname in preview_origin
 *      is itself a legacy bind-all (operator misconfigured), fall back to the
 *      caller's `windowHostname` so the result is still reachable.
 *   2. `aboutInfo.preview_port` — the gateway's preview listener port,
 *      combined with `windowHostname`.
 *   3. nothing — return null; caller should leave the URL unchanged.
 */
export function resolveEffectivePreview(
  aboutInfo: { preview_origin?: string; preview_port?: number } | null | undefined,
  windowHostname: string,
): { hostname: string; port: number } | null {
  if (aboutInfo?.preview_origin) {
    try {
      const u = new URL(aboutInfo.preview_origin)
      const port = u.port ? Number(u.port) : (u.protocol === 'https:' ? 443 : 80)
      if (port > 0) {
        const host = LEGACY_HOSTS.has(u.hostname) ? windowHostname : u.hostname
        return { hostname: host, port }
      }
    } catch {
      // Malformed preview_origin → fall through to preview_port.
    }
  }
  if (aboutInfo?.preview_port && aboutInfo.preview_port > 0) {
    return { hostname: windowHostname, port: aboutInfo.preview_port }
  }
  return null
}

/**
 * Arguments for `buildIframeURL`.
 */
export interface BuildIframeURLArgs { // not-wire-format: arguments bag for the buildIframeURL helper function; never serialised over any HTTP/WS boundary
  /**
   * The relative path from the tool result, e.g.
   * `"/preview/<agent>/<token>/"` (canonical) or the legacy
   * `"/serve/<agent>/<token>/"` / `"/dev/<agent>/<token>/"` for transcript
   * replay.
   */
  path: string
  /**
   * When the operator has set `gateway.preview_origin`, this is that value
   * fully-qualified, e.g. `"https://preview.acme.com"`. When absent or empty,
   * the URL is constructed from `protocol`, `hostname`, and `previewPort`.
   */
  previewOrigin?: string
  /** The preview listener port from `/api/v1/about`. */
  previewPort: number
  /** `window.location.hostname` — the host the user is accessing the SPA from. */
  hostname: string
  /** `window.location.protocol` — e.g. `"http:"` or `"https:"`. */
  protocol: string
}

/**
 * Constructs the iframe `src` URL for a `web_serve` tool result (or the
 * legacy `serve_workspace` / `run_in_workspace` tool results kept for
 * transcript replay).
 *
 * Returns either `{ url: string }` on success or one of three typed errors:
 *   - `{ error: 'invalid-path' }` — `path` failed `validatePreviewPath`.
 *     Indicates a malformed or corrupt tool result.
 *   - `{ error: 'scheme-mismatch' }` — `previewOrigin` is HTTPS but the
 *     SPA was loaded over HTTP (or vice-versa). Mixed-content iframes would
 *     be blocked by the browser; this fails fast with a clear error
 *     (FR-010a, US-5 AS-3).
 *   - `{ error: 'misconfigured-origin' }` — `previewOrigin` is set but
 *     unparseable as a URL. Indicates an operator deployment problem, not a
 *     corrupt tool result.
 *
 * Per FR-010, FR-010a, FR-010b.
 *
 * @example
 * // Happy path — no previewOrigin, HTTP SPA (canonical /preview/ path)
 * buildIframeURL({
 *   path: '/preview/agent-1/abc123/',
 *   previewPort: 5001,
 *   hostname: '146.190.89.151',
 *   protocol: 'http:',
 * })
 * // => { url: 'http://146.190.89.151:5001/preview/agent-1/abc123/' }
 *
 * @example
 * // Happy path — previewOrigin set (canonical /preview/ path)
 * buildIframeURL({
 *   path: '/preview/agent-1/abc123/',
 *   previewOrigin: 'https://preview.acme.com',
 *   previewPort: 5001,
 *   hostname: 'omnipus.acme.com',
 *   protocol: 'https:',
 * })
 * // => { url: 'https://preview.acme.com/preview/agent-1/abc123/' }
 *
 * @example
 * // Legacy back-compat: /serve/ path still accepted for transcript replay
 * buildIframeURL({
 *   path: '/serve/agent-1/abc123/',
 *   previewPort: 5001,
 *   hostname: '146.190.89.151',
 *   protocol: 'http:',
 * })
 * // => { url: 'http://146.190.89.151:5001/serve/agent-1/abc123/' }
 *
 * @example
 * // Invalid path
 * buildIframeURL({
 *   path: 'javascript:alert(1)',
 *   previewPort: 5001,
 *   hostname: '1.2.3.4',
 *   protocol: 'http:',
 * })
 * // => { error: 'invalid-path' }
 *
 * @example
 * // Scheme mismatch — HTTP SPA + HTTPS preview origin
 * buildIframeURL({
 *   path: '/preview/agent-1/abc123/',
 *   previewOrigin: 'https://preview.example.com',
 *   previewPort: 443,
 *   hostname: 'main.example.com',
 *   protocol: 'http:',
 * })
 * // => { error: 'scheme-mismatch' }
 *
 * @example
 * // Invalid path — path traversal attempt
 * buildIframeURL({
 *   path: '/preview/../../etc/passwd',
 *   previewPort: 5001,
 *   hostname: '1.2.3.4',
 *   protocol: 'http:',
 * })
 * // => { error: 'invalid-path' }
 *
 * @example
 * // Invalid path — empty string
 * buildIframeURL({
 *   path: '',
 *   previewPort: 5001,
 *   hostname: '1.2.3.4',
 *   protocol: 'http:',
 * })
 * // => { error: 'invalid-path' }
 *
 * @example
 * // Invalid path — API path
 * buildIframeURL({
 *   path: '/api/v1/agents',
 *   previewPort: 5001,
 *   hostname: '1.2.3.4',
 *   protocol: 'http:',
 * })
 * // => { error: 'invalid-path' }
 */
export function buildIframeURL(args: BuildIframeURLArgs): { url: string } | { error: 'invalid-path' | 'scheme-mismatch' | 'misconfigured-origin' } {
  const { path, previewOrigin, previewPort, hostname, protocol } = args

  // Step 1: validate path via the shared regex (FR-010b).
  if (!validatePreviewPath(path)) {
    return { error: 'invalid-path' }
  }

  // Step 2: if previewOrigin is provided and non-empty, use it.
  if (previewOrigin && previewOrigin.length > 0) {
    // Parse the preview origin to extract its scheme for the mismatch check.
    let originParsed: URL
    try {
      originParsed = new URL(previewOrigin)
    } catch {
      // Unparseable preview origin — this is an operator deployment problem
      // (misconfigured gateway.preview_origin), not a corrupt tool result.
      return { error: 'misconfigured-origin' }
    }

    // Scheme mismatch check (FR-010a): reject HTTP + HTTPS combos.
    if (originParsed.protocol !== protocol) {
      return { error: 'scheme-mismatch' }
    }

    // Strip trailing slash from origin, prepend path.
    const base = previewOrigin.replace(/\/$/, '')
    return { url: base + path }
  }

  // Step 3: no previewOrigin — construct from current window coordinates.
  const url = `${protocol}//${hostname}:${previewPort}${path}`
  return { url }
}
