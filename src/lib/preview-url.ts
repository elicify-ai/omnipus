/**
 * preview-url.ts — Pure URL-rewrite and path-validation utilities for the
 * chat preview-link feature (FR-010b, FR-016, FR-017 — preview-on-main-listener
 * spec, ADR-044).
 *
 * ADR-044 note: `/preview/` is now served on the SAME origin as the SPA (no
 * separate preview listener/port/origin — see docs/internal/architecture/
 * ADR-044-preview-on-main-listener.md and
 * docs/internal/specs/preview-on-main-listener-spec.md, FR-005/FR-015/FR-016).
 * `serve_web` already returns an absolute URL on that canonical origin, so
 * this module no longer resolves an operator-configured preview origin/port
 * — it only validates and (for old transcripts) normalises legacy bind-all
 * hosts to the current page's own origin.
 *
 * All functions are pure (no DOM access beyond the values callers pass in,
 * no side effects, no React) so they can be exercised in plain Node.js /
 * Vitest without a browser.
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
 * Legacy bind-all hosts that an OLD tool result (recorded before ADR-044, or
 * before the prior two-port fix) may still contain. These are never
 * browser-reachable as-is, so we rewrite them to the current page's own
 * hostname/port. New `serve_web` results never contain these — the backend
 * always emits the canonical gateway origin (FR-005) — this exists purely
 * for historical transcript replay.
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
const PREVIEW_PATH_REGEX = /^\/(?:preview|serve|dev)\/[A-Za-z0-9_-]+\/[A-Za-z0-9_-]+(?:\/.*)?$/

/**
 * Returns `true` when `path` is a safe, well-formed preview path that the
 * SPA may use to build a preview href.
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
 * Rewrites `href` when its host is a legacy bind-all address (historical
 * transcript replay only — see LEGACY_HOSTS above).
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
 *     (both legacy back-compat paths), also swap the port to `port` (the
 *     caller's own origin port — preview now shares that origin, ADR-044).
 *     Otherwise preserve the original port.
 *
 * @param href - The raw href string from the markdown link or tool result.
 * @param hostname - The host the user is accessing the SPA from
 *   (`window.location.hostname`). May be a bare IP, a domain, or `localhost`.
 * @param port - The current page's own port (`window.location.port`,
 *   defaulted per scheme) — preview and the SPA share this origin post-ADR-044.
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
export function rewriteLegacyURL(href: string, hostname: string, port: number): string {
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

  // Rule 6: if the path is a preview/serve/dev path, swap to the caller's own
  // port (preview now shares the SPA's origin, ADR-044); otherwise preserve
  // the port already in the URL.
  if (
    parsed.pathname.startsWith('/preview/') ||
    parsed.pathname.startsWith('/serve/') ||
    parsed.pathname.startsWith('/dev/')
  ) {
    parsed.port = String(port)
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
 * Historically resolved an operator-configured preview host/port override
 * advertised on `/api/v1/about` (before ADR-044) so markdown links pointing
 * at a legacy bind-all host could be rewritten to a reachable address on the
 * (then-separate) preview listener.
 *
 * ADR-044 retires that separate preview listener/port/origin entirely —
 * `/preview/` now always shares the SPA's own origin. There is no override
 * left to resolve, so this always returns `null` (= "no rewrite needed";
 * hrefs render exactly as the backend returned them). The function is kept
 * (rather than deleted), with its original two-argument shape, purely so
 * existing callers (`markdown-text.tsx`, `historical-markdown.tsx` via
 * `markdown-shared.tsx`'s `createLinkRenderer`) do not need to change.
 */
export function resolveEffectivePreview(
  _aboutInfo: unknown,
  _windowHostname: string,
): { hostname: string; port: number } | null {
  // Params are intentionally unused — see doc comment above: kept only so
  // existing call sites don't need to change their arity.
  void _aboutInfo
  void _windowHostname
  return null
}

// ── ADR-094 Mode 1: per-preview `*.localhost` isolated URL (FR-005/FR-023) ────
//
// Mode 1 serves each preview under its own loopback subdomain —
// `http://<label>.localhost:<spa port>/` — so the previewed app's cookies can
// never cross the SPA's own origin. The SPA only ever RENDERS such a URL
// after (a) validating it against the exact grammar the gateway is allowed
// to mint (FR-023 — defence against a tampered transcript re-rendering an
// attacker host, S-8.5 / DS-8 rows 1–6) and (b) FR-024's engine rule says
// this browser resolves `*.localhost` at all. Any failure falls back to the
// Mode 2 same-origin `/preview/` path URL.
//
// Validation runs on the RAW string, not a `new URL()` parse: the WHATWG URL
// constructor LOWER-CASES hostnames, so a tampered `http://MyApp.localhost/`
// would parse to `myapp.localhost` and silently pass a parsed-hostname
// lower-case check. FR-023 rejects non-lower-case labels outright.
const ISOLATED_URL_REGEX = /^http:\/\/([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)\.localhost(?::(\d{1,5}))?(?:[\/?#].*)?$/

/**
 * Validates a tool result's `isolated_url` against FR-005/FR-023 before the
 * SPA may render it (the DS-8 table). The URL must be:
 *   • scheme `http:` — Mode 1 is HTTP-only (the gateway mints no TLS certs
 *     for `*.localhost`; `https:` is a foreign origin, never rendered);
 *   • host exactly ONE grammar-valid label + `.localhost` — letters/digits/
 *     hyphens, 1–63 chars, no leading/trailing hyphen, LOWER-CASE (the
 *     label-group character class excludes `.` and uppercase, which rejects
 *     the second-dot row `myapp.evil.localhost` and the case row
 *     `MyApp.localhost` in one stroke, on the raw string);
 *   • NOT some other suffix — `.localhost` must terminate the host, so
 *     `myapp.localhost.evil.com` never matches;
 *   • port equal to the SPA's own effective port (a portless URL only when
 *     the SPA itself rides http's implicit port 80) — a foreign-port
 *     `isolated_url` would not be the preview the gateway minted.
 *
 * Not exported: callers reach it only through `resolvePreviewHref`, which
 * additionally applies the FR-024 engine gate.
 */
function validateIsolatedUrl(raw: string, spaPort: number): boolean {
  const match = ISOLATED_URL_REGEX.exec(raw)
  if (!match) return false
  const urlPort = match[2]
  if (urlPort === undefined) return spaPort === 80
  return urlPort === String(spaPort)
}

/**
 * FR-024 engine rule: does THIS browser resolve `*.localhost` subdomains to
 * loopback? Chromium and Firefox do; WebKit (Safari) does not.
 *
 * Detection order is deliberate:
 *   1. Feature detection — `navigator.userAgentData` exists only in the
 *      Chromium family. The check is on the VALUE's truthiness, never
 *      `'userAgentData' in navigator`: the property can be present yet
 *      undefined, and an undefined value off a secure context (where the API
 *      is hidden) is CORRECT and must fail safe to Mode 2 (round-2 OBS-001).
 *   2. Firefox hides userAgentData (it is a secure-context-only API), so the
 *      UA token `Firefox/` is the necessary secondary signal here — the UA
 *      sniff is unavoidable and is stated in FR-024's own rule text.
 *   3. Everything else — WebKit, unknown engines, detection failure — is NOT
 *      proven to resolve `*.localhost` and falls safe to Mode 2.
 *
 * Read at CALL time (never cached at module load): jsdom- and test-based
 * stubs swap the navigator fields per test, and a real browser can change
 * UA-reporting state across a session's lifetime.
 */
function browserResolvesLocalhostSubdomains(): boolean {
  if (typeof navigator === 'undefined') return false
  const uad = (navigator as unknown as { userAgentData?: unknown }).userAgentData
  if (uad) return true
  if (typeof navigator.userAgent === 'string' && navigator.userAgent.includes('Firefox/')) {
    return true
  }
  return false
}

/**
 * Resolves the href to render for a `web_serve` tool result (dev or static
 * mode), or the legacy `serve_workspace` / `run_in_workspace` shapes kept
 * for transcript replay.
 *
 * Since ADR-044, `serve_web` already returns an absolute URL on the
 * canonical gateway origin (FR-005) — the SAME origin the SPA itself is
 * served from. This function no longer builds a URL from an
 * operator-configured preview origin/port; it:
 *   1. Prefers the result's own `url` when it is a validatable absolute
 *      http(s) URL or a validatable relative path, normalising any legacy
 *      bind-all host via `rewriteLegacyURL` (old-transcript replay safety).
 *   2. Falls back to building an absolute URL from the relative `path`
 *      field against `origin` (the SPA's own `window.location.origin`).
 *   3. Returns `{ error: 'invalid-path' }` when neither field yields a
 *      validatable preview path — the caller falls back to rendering the
 *      raw `url` (if any) via a scheme-checked link-only fallback.
 *
 * Per FR-016 / FR-017.
 *
 * @example
 * // Happy path — absolute url on the canonical origin
 * resolvePreviewHref({
 *   url: 'https://pod.example.com/preview/agent-1/abc123/',
 *   origin: 'https://pod.example.com',
 *   hostname: 'pod.example.com',
 *   port: 443,
 * })
 * // => { href: 'https://pod.example.com/preview/agent-1/abc123/' }
 *
 * @example
 * // Relative path only (no absolute url) — built from the SPA's own origin
 * resolvePreviewHref({
 *   path: '/preview/agent-1/abc123/',
 *   origin: 'http://localhost:5000',
 *   hostname: 'localhost',
 *   port: 5000,
 * })
 * // => { href: 'http://localhost:5000/preview/agent-1/abc123/' }
 *
 * @example
 * // Legacy bind-all host in an old transcript's url — normalised
 * resolvePreviewHref({
 *   url: 'http://0.0.0.0:5000/preview/agent-1/abc123/',
 *   origin: 'http://146.190.89.151:5000',
 *   hostname: '146.190.89.151',
 *   port: 5000,
 * })
 * // => { href: 'http://146.190.89.151:5000/preview/agent-1/abc123/' }
 *
 * @example
 * // Invalid path in both fields
 * resolvePreviewHref({
 *   path: 'javascript:alert(1)',
 *   origin: 'http://localhost:5000',
 *   hostname: 'localhost',
 *   port: 5000,
 * })
 * // => { error: 'invalid-path' }
 *
 * @example
 * // ADR-094 Mode 1 — a VALID isolated_url on a Chromium/Firefox engine is
 * // returned verbatim (DS-8 row 1): the exact label host the gateway minted.
 * resolvePreviewHref({
 *   path: '/preview/mia/tok-abc123/',
 *   url: 'http://localhost:5000/preview/mia/tok-abc123/',
 *   isolated_url: 'http://myapp.localhost:5000/',
 *   origin: 'http://localhost:5000',
 *   hostname: 'localhost',
 *   port: 5000,
 * })
 * // => { href: 'http://myapp.localhost:5000/' }
 *
 * @example
 * // ADR-094 — a tampered/foreign isolated_url (or an engine without
 * // `*.localhost` support) falls back to the Mode 2 path URL (S-8.5).
 * resolvePreviewHref({
 *   path: '/preview/mia/tok-abc123/',
 *   url: 'http://localhost:5000/preview/mia/tok-abc123/',
 *   isolated_url: 'http://myapp.evil.localhost:5000/',
 *   origin: 'http://localhost:5000',
 *   hostname: 'localhost',
 *   port: 5000,
 * })
 * // => { href: 'http://localhost:5000/preview/mia/tok-abc123/' }
 */
export function resolvePreviewHref(args: {
  path?: string
  /**
   * ADR-094 FR-023: the per-preview `*.localhost` URL the gateway minted.
   * Rendered ONLY when it validates AND FR-024's engine rule selects Mode 1;
   * every failure falls back to the Mode 2 path/url resolution below.
   * Optional so old transcripts (predating ADR-094) resolve as before.
   */
  isolated_url?: string
  url?: string
  origin: string
  hostname: string
  port: number
}): { href: string } | { error: 'invalid-path' } {
  const { path, url, isolated_url, origin, hostname, port } = args

  // ── ADR-094 FR-023/FR-024: Mode 1 candidate, validated + engine-gated ──
  if (
    isolated_url &&
    isolated_url.length > 0 &&
    browserResolvesLocalhostSubdomains() &&
    validateIsolatedUrl(isolated_url, port)
  ) {
    return { href: isolated_url }
  }

  if (url && url.length > 0) {
    if (url.startsWith('/')) {
      if (validatePreviewPath(url)) {
        return { href: rewriteLegacyURL(`${origin}${url}`, hostname, port) }
      }
    } else {
      try {
        const parsed = new URL(url)
        if (
          (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
          validatePreviewPath(parsed.pathname)
        ) {
          return { href: rewriteLegacyURL(url, hostname, port) }
        }
      } catch {
        // Not an absolute URL — fall through to the relative `path` field.
      }
    }
  }

  if (path && validatePreviewPath(path)) {
    return { href: rewriteLegacyURL(`${origin}${path}`, hostname, port) }
  }

  return { error: 'invalid-path' }
}
