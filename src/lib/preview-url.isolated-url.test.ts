/**
 * preview-url.isolated-url.test.ts — RED tests, ADR-094 TDD Plan order 21
 * (TestResolvePreviewHref_Validation).
 *
 * Traces to: docs/internal/specs/adr-094-preview-isolation-spec.md
 *   FR-023 — resolvePreviewHref MUST accept `isolated_url` and return it only
 *   when: host = exactly one lower-cased grammar-valid label + `.localhost`,
 *   port = window.location.port, scheme http, and FR-024's engine rule selects
 *   Mode 1; any failure ⇒ the existing fallback resolution.
 *   S-8.1 (Chromium link from one result, re-validated client-side),
 *   S-8.5 (tampered isolated_url falls back), DS-8 rows 1–6.
 *
 * ORACLE INDEPENDENCE: every expected value below is transcribed from the
 * spec's DS-8 table and FR-023/S-8.5 rule text — none from running
 * preview-url.ts. The grammar rows additionally derive from FR-005's label
 * grammar (letters/digits/hyphens, 1–63 chars, no leading/trailing hyphen).
 *
 * RED (current failure mode): `resolvePreviewHref` has no `isolated_url`
 * parameter — the Mode 1 acceptance rows fail on behaviour (the function
 * resolves the Mode 2 fallback for every row) and the extended call sites
 * also carry a TypeScript excess-property error at `npm run typecheck`
 * (expected: the extended interface genuinely does not exist yet).
 */

import { afterEach, describe, expect, it } from 'vitest'
import { resolvePreviewHref } from './preview-url'

// ── Engine stubbing (FR-024) ──────────────────────────────────────────────────
//
// navigator.userAgentData is not in the TS DOM lib (non-standard) — cast
// through unknown. `undefined` (jsdom default) is the FAIL-SAFE state that
// selects Mode 2, per FR-024's own note (OBS-001).

type EngineKind = 'chromium' | 'firefox' | 'webkit' | 'unknown'

const ORIGINAL_UA = navigator.userAgent
const ORIGINAL_UAD = Object.getOwnPropertyDescriptor(
  Object.getPrototypeOf(navigator),
  'userAgentData',
)

function stubEngine(kind: EngineKind): void {
  if (kind === 'chromium') {
    // FR-024: feature detection via navigator.userAgentData (Chromium family).
    Object.defineProperty(navigator, 'userAgentData', {
      configurable: true,
      value: {
        brands: [
          { brand: 'Not.A/Brand', version: '99' },
          { brand: 'Chromium', version: '130' },
        ],
        mobile: false,
        platform: 'macOS',
      },
    })
    Object.defineProperty(navigator, 'userAgent', {
      configurable: true,
      value: ORIGINAL_UA,
    })
    return
  }
  // Firefox/WebKit/unknown: no userAgentData (secure-context-only API —
  // FR-024: undefined off a secure context is CORRECT, fails safe to Mode 2).
  Object.defineProperty(navigator, 'userAgentData', {
    configurable: true,
    get: () => undefined,
  })
  Object.defineProperty(navigator, 'userAgent', {
    configurable: true,
    value:
      kind === 'firefox'
        ? 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:141.0) Gecko/20100101 Firefox/141.0'
        : kind === 'webkit'
          ? 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15'
          : ORIGINAL_UA,
  })
}

afterEach(() => {
  if (ORIGINAL_UAD) {
    Object.defineProperty(
      Object.getPrototypeOf(navigator),
      'userAgentData',
      ORIGINAL_UAD,
    )
  } else {
    Reflect.deleteProperty(navigator, 'userAgentData')
    Reflect.deleteProperty(Object.getPrototypeOf(navigator), 'userAgentData')
  }
  Object.defineProperty(navigator, 'userAgent', {
    configurable: true,
    value: ORIGINAL_UA,
  })
})

// ── Shared fixture values ─────────────────────────────────────────────────────
//
// The SPA's own origin is pinned for every row: window.location.port = 5000,
// origin http://localhost:5000 (a Mode 1 deployment shape: loopback host with
// an explicit port). The fallback path/url pair is a VALID Mode 2 preview URL
// so the "fallback" verdicts have a well-defined expected href — the Mode 2
// URL itself (S-8.5: "renders the Mode 2 fallback").

const SPA_ORIGIN = 'http://localhost:5000'
const SPA_HOSTNAME = 'localhost'
const SPA_PORT = 5000
const MODE2_PATH = '/preview/mia/tok-abc123/'
const MODE2_URL = `${SPA_ORIGIN}${MODE2_PATH}`

describe('resolvePreviewHref — isolated_url validation (order 21, DS-8 / FR-023)', () => {
  it('DS-8 row 1: valid http://<label>.localhost:<same port>/ accepted as the Mode 1 href (Chromium engine)', () => {
    stubEngine('chromium')
    const isolated = 'http://myapp.localhost:5000/'
    const result = resolvePreviewHref({
      path: MODE2_PATH,
      url: MODE2_URL,
      isolated_url: isolated,
      origin: SPA_ORIGIN,
      hostname: SPA_HOSTNAME,
      port: SPA_PORT,
    })
    // DS-8 row 1: "Mode 1 href (engine-gated)". FR-023: return `it` — the
    // isolated_url itself. Exact equality at the highest lattice rung.
    expect('href' in result && result.href).toBe(isolated)
  })

  it('S-8.1: Firefox UA token also selects the Mode 1 href when the URL is valid', () => {
    stubEngine('firefox')
    const isolated = 'http://myapp.localhost:5000/'
    const result = resolvePreviewHref({
      path: MODE2_PATH,
      url: MODE2_URL,
      isolated_url: isolated,
      origin: SPA_ORIGIN,
      hostname: SPA_HOSTNAME,
      port: SPA_PORT,
    })
    expect('href' in result && result.href).toBe(isolated)
  })

  it('DS-8 row 2: second dot (myapp.evil.localhost) rejected → Mode 2 fallback', () => {
    stubEngine('chromium')
    const result = resolvePreviewHref({
      path: MODE2_PATH,
      url: MODE2_URL,
      isolated_url: 'http://myapp.evil.localhost:5000/',
      origin: SPA_ORIGIN,
      hostname: SPA_HOSTNAME,
      port: SPA_PORT,
    })
    expect('href' in result && result.href).toBe(MODE2_URL)
  })

  it('S-8.5: another suffix (myapp.localhost.evil.com) rejected → Mode 2 fallback', () => {
    stubEngine('chromium')
    const result = resolvePreviewHref({
      path: MODE2_PATH,
      url: MODE2_URL,
      isolated_url: 'http://myapp.localhost.evil.com:5000/',
      origin: SPA_ORIGIN,
      hostname: SPA_HOSTNAME,
      port: SPA_PORT,
    })
    expect('href' in result && result.href).toBe(MODE2_URL)
  })

  it('DS-8 row 3: foreign port (:9999 ≠ window.location.port 5000) rejected → Mode 2 fallback', () => {
    stubEngine('chromium')
    const result = resolvePreviewHref({
      path: MODE2_PATH,
      url: MODE2_URL,
      isolated_url: 'http://myapp.localhost:9999/',
      origin: SPA_ORIGIN,
      hostname: SPA_HOSTNAME,
      port: SPA_PORT,
    })
    expect('href' in result && result.href).toBe(MODE2_URL)
  })

  it('DS-8 row 4: https scheme rejected → Mode 2 fallback', () => {
    stubEngine('chromium')
    const result = resolvePreviewHref({
      path: MODE2_PATH,
      url: MODE2_URL,
      isolated_url: 'https://myapp.localhost:5000/',
      origin: SPA_ORIGIN,
      hostname: SPA_HOSTNAME,
      port: SPA_PORT,
    })
    expect('href' in result && result.href).toBe(MODE2_URL)
  })

  it('DS-8 row 5: grammar-invalid label (-bad-) rejected → Mode 2 fallback', () => {
    stubEngine('chromium')
    const result = resolvePreviewHref({
      path: MODE2_PATH,
      url: MODE2_URL,
      isolated_url: 'http://-bad-.localhost:5000/',
      origin: SPA_ORIGIN,
      hostname: SPA_HOSTNAME,
      port: SPA_PORT,
    })
    expect('href' in result && result.href).toBe(MODE2_URL)
  })

  it('FR-023: non-lower-cased label (MyApp.localhost) rejected → Mode 2 fallback', () => {
    stubEngine('chromium')
    const result = resolvePreviewHref({
      path: MODE2_PATH,
      url: MODE2_URL,
      isolated_url: 'http://MyApp.localhost:5000/',
      origin: SPA_ORIGIN,
      hostname: SPA_HOSTNAME,
      port: SPA_PORT,
    })
    expect('href' in result && result.href).toBe(MODE2_URL)
  })

  it('DS-8 row 6a: absent isolated_url → fallback exactly as today (S-8.3 old transcripts)', () => {
    stubEngine('chromium')
    const result = resolvePreviewHref({
      path: MODE2_PATH,
      url: MODE2_URL,
      origin: SPA_ORIGIN,
      hostname: SPA_HOSTNAME,
      port: SPA_PORT,
    })
    expect('href' in result && result.href).toBe(MODE2_URL)
  })

  it('DS-8 row 6b: empty isolated_url → fallback exactly as today', () => {
    stubEngine('chromium')
    const result = resolvePreviewHref({
      path: MODE2_PATH,
      url: MODE2_URL,
      isolated_url: '',
      origin: SPA_ORIGIN,
      hostname: SPA_HOSTNAME,
      port: SPA_PORT,
    })
    expect('href' in result && result.href).toBe(MODE2_URL)
  })
})
