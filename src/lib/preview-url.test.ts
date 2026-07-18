/**
 * Tests for preview-url.ts — validatePreviewPath, rewriteLegacyURL,
 * resolveEffectivePreview, resolvePreviewHref.
 *
 * Traces to: docs/internal/specs/preview-on-main-listener-spec.md
 * FR-010b, FR-016, FR-017 (ADR-044 — preview-on-main-listener).
 *
 * Test order mirrors the spec's rewrite dataset and path-validation dataset
 * from the @example blocks in preview-url.ts.
 */

import { describe, it, expect } from 'vitest'
import {
  validatePreviewPath,
  rewriteLegacyURL,
  resolveEffectivePreview,
  resolvePreviewHref,
} from './preview-url'

// ── validatePreviewPath (8-row dataset) ───────────────────────────────────────

describe('validatePreviewPath — 8-row spec dataset (FR-010b / MR-10)', () => {
  // Traces to: preview-url.ts @example block — validatePreviewPath dataset

  it.each([
    // row 1 — valid /preview/ path (canonical new shape)
    { name: 'preview path with trailing slash', path: '/preview/agent-1/abc123/', expected: true },
    // row 2 — valid /serve/ path (back-compat)
    { name: 'serve path with trailing slash', path: '/serve/agent-1/abc123/', expected: true },
    // row 3 — valid /dev/ path (back-compat)
    { name: 'dev path with trailing slash', path: '/dev/agent-2/xyz789/', expected: true },
    // row 4 — XSS injection
    { name: 'javascript: scheme rejected', path: 'javascript:alert(1)', expected: false },
    // row 5 — scheme-relative
    { name: 'scheme-relative rejected', path: '//attacker.com/exfil', expected: false },
    // row 6 — API path
    { name: 'API path rejected', path: '/api/v1/agents', expected: false },
    // row 7 — data: URI
    { name: 'data: URI rejected', path: 'data:text/html,...', expected: false },
    // row 8 — path traversal
    { name: 'path traversal rejected', path: '/serve/../../etc/passwd', expected: false },
    // row 9 — empty string
    { name: 'empty string rejected', path: '', expected: false },
  ])('$name: validatePreviewPath($path) === $expected', ({ path, expected }) => {
    expect(validatePreviewPath(path)).toBe(expected)
  })

  it('differentiation: three valid paths produce true, one invalid produces false', () => {
    // Proves the function is not hardcoded to always return true or always return false.
    expect(validatePreviewPath('/preview/agent-a/token0/')).toBe(true)
    expect(validatePreviewPath('/serve/agent-a/token1/')).toBe(true)
    expect(validatePreviewPath('/dev/agent-b/token2/')).toBe(true)
    expect(validatePreviewPath('/notserve/agent-a/token1/')).toBe(false)
  })

  it('serve path without trailing slash is still valid', () => {
    // The regex ends with (?:/.*)?$ — no trailing slash required
    expect(validatePreviewPath('/serve/my-agent/my-token')).toBe(true)
  })

  it('dev path with sub-path is valid', () => {
    expect(validatePreviewPath('/dev/my-agent/my-token/static/index.html')).toBe(true)
  })
})

// ── rewriteLegacyURL — 15-row dataset ────────────────────────────────────────

describe('rewriteLegacyURL — 15-row spec dataset (FR-016 / FR-017)', () => {
  // Traces to: preview-url.ts @example block — rewriteLegacyURL dataset
  // Each row: { name, href, hostname, port, expected }

  it.each([
    // Row 1 — wildcard 0.0.0.0, serve path → port swapped to the given port
    {
      name: 'row 1: 0.0.0.0 + serve path → given port',
      href: 'http://0.0.0.0:5000/serve/m/t/',
      hostname: '146.190.89.151',
      port: 5001,
      expected: 'http://146.190.89.151:5001/serve/m/t/',
    },
    // Row 2 — 0.0.0.0 + /dev/ path, localhost destination
    {
      name: 'row 2: 0.0.0.0 + dev path → localhost',
      href: 'http://0.0.0.0:5000/dev/m/t/',
      hostname: 'localhost',
      port: 5001,
      expected: 'http://localhost:5001/dev/m/t/',
    },
    // Row 3 — 0.0.0.0, non-serve path → main port preserved
    {
      name: 'row 3: 0.0.0.0 + non-serve path → main port retained',
      href: 'http://0.0.0.0:5000/about',
      hostname: '1.2.3.4',
      port: 5001,
      expected: 'http://1.2.3.4:5000/about',
    },
    // Row 4 — IPv6 wildcard [::]
    {
      name: 'row 4: [::] + serve path → rewritten',
      href: 'http://[::]:5000/serve/m/t/',
      hostname: '1.2.3.4',
      port: 5001,
      expected: 'http://1.2.3.4:5001/serve/m/t/',
    },
    // Row 5 — IPv6 explicit zero [::0]
    {
      name: 'row 5: [::0] + serve path → rewritten',
      href: 'http://[::0]:5000/serve/m/t/',
      hostname: '1.2.3.4',
      port: 5001,
      expected: 'http://1.2.3.4:5001/serve/m/t/',
    },
    // Row 6 — bare zero "0" (WHATWG normalises to 0.0.0.0)
    {
      name: 'row 6: bare 0 + serve path → rewritten',
      href: 'http://0:5000/serve/m/t/',
      hostname: '1.2.3.4',
      port: 5001,
      expected: 'http://1.2.3.4:5001/serve/m/t/',
    },
    // Row 7 — loopback 127.0.0.1
    {
      name: 'row 7: 127.0.0.1 + serve path → rewritten',
      href: 'http://127.0.0.1:5000/serve/m/t/',
      hostname: '1.2.3.4',
      port: 5001,
      expected: 'http://1.2.3.4:5001/serve/m/t/',
    },
    // Row 8 — foreign host unchanged
    {
      name: 'row 8: foreign host → unchanged',
      href: 'https://example.com/page',
      hostname: '1.2.3.4',
      port: 5001,
      expected: 'https://example.com/page',
    },
    // Row 9 — mailto: passes through
    {
      name: 'row 9: mailto: scheme → unchanged',
      href: 'mailto:foo@x.com',
      hostname: '1.2.3.4',
      port: 5001,
      expected: 'mailto:foo@x.com',
    },
    // Row 10 — javascript: passes through (XSS safety)
    {
      name: 'row 10: javascript: scheme → unchanged',
      href: 'javascript:alert(1)',
      hostname: '1.2.3.4',
      port: 5001,
      expected: 'javascript:alert(1)',
    },
    // Row 11 — tel: passes through
    {
      name: 'row 11: tel: scheme → unchanged',
      href: 'tel:+155512345',
      hostname: '1.2.3.4',
      port: 5001,
      expected: 'tel:+155512345',
    },
    // Row 12 — relative path unchanged
    {
      name: 'row 12: relative path → unchanged',
      href: '/relative/path',
      hostname: '1.2.3.4',
      port: 5001,
      expected: '/relative/path',
    },
    // Row 13 — scheme-relative unchanged
    {
      name: 'row 13: scheme-relative → unchanged',
      href: '//host.com/x',
      hostname: '1.2.3.4',
      port: 5001,
      expected: '//host.com/x',
    },
    // Row 14 — empty string boundary
    {
      name: 'row 14: empty string → unchanged',
      href: '',
      hostname: '1.2.3.4',
      port: 5001,
      expected: '',
    },
    // Row 15 — unparseable href
    {
      name: 'row 15: unparseable href → unchanged without throw',
      href: 'not-a-url',
      hostname: '1.2.3.4',
      port: 5001,
      expected: 'not-a-url',
    },
  ])('$name', ({ href, hostname, port, expected }) => {
    const result = rewriteLegacyURL(href, hostname, port)
    expect(result).toBe(expected)
  })

  it('differentiation: two different legacy hosts produce two different rewritten URLs', () => {
    // Proves the output depends on the hostname argument, not hardcoded.
    const result1 = rewriteLegacyURL('http://0.0.0.0:5000/serve/a/b/', 'host-a.example.com', 5001)
    const result2 = rewriteLegacyURL('http://0.0.0.0:5000/serve/a/b/', 'host-b.example.com', 5001)
    expect(result1).toBe('http://host-a.example.com:5001/serve/a/b/')
    expect(result2).toBe('http://host-b.example.com:5001/serve/a/b/')
    expect(result1).not.toBe(result2)
  })

  it('HTTPS legacy host is rewritten with HTTPS preserved', () => {
    // The function rewrites host+port but preserves the original scheme.
    const result = rewriteLegacyURL('https://0.0.0.0:5000/serve/a/b/', '10.0.0.1', 5001)
    expect(result).toBe('https://10.0.0.1:5001/serve/a/b/')
  })

  it('0.0.0.0 + /preview/ path → port swapped to the given port (canonical shape)', () => {
    // Traces to: FR-017 — /preview/ is the canonical preview path prefix.
    const result = rewriteLegacyURL('http://0.0.0.0:5000/preview/agent-1/tok/', '146.190.89.151', 5001)
    expect(result).toBe('http://146.190.89.151:5001/preview/agent-1/tok/')
  })

  it('127.0.0.1 + /preview/ path → host and port rewritten', () => {
    const result = rewriteLegacyURL('http://127.0.0.1:5000/preview/m/t/', '1.2.3.4', 5001)
    expect(result).toBe('http://1.2.3.4:5001/preview/m/t/')
  })
})

// ── resolveEffectivePreview — always null post-ADR-044 ───────────────────────

describe('resolveEffectivePreview — always null (ADR-044: no separate preview origin/port)', () => {
  // Traces to: preview-on-main-listener-spec.md — US-8, FR-015. Since preview
  // now shares the SPA's own origin, there is no operator-configured
  // override left to resolve; the function is retained (2-arg shape) purely
  // for call-site compatibility.

  it('returns null regardless of the (ignored) first argument', () => {
    expect(resolveEffectivePreview(null, 'localhost')).toBeNull()
    expect(resolveEffectivePreview(undefined, 'localhost')).toBeNull()
    expect(resolveEffectivePreview({ preview_port: 5001, preview_origin: 'https://x' }, 'localhost')).toBeNull()
    expect(resolveEffectivePreview({ anything: 'goes' }, '1.2.3.4')).toBeNull()
  })

  it('returns null regardless of the hostname argument (differentiation guard)', () => {
    // Proves this isn't accidentally reintroducing a hidden non-null branch.
    expect(resolveEffectivePreview(null, 'host-a.example.com')).toBeNull()
    expect(resolveEffectivePreview(null, 'host-b.example.com')).toBeNull()
  })
})

// ── resolvePreviewHref — main-origin URL resolution (ADR-044) ────────────────

describe('resolvePreviewHref — main-origin URL, no preview_port/preview_origin (FR-016/FR-017)', () => {
  it('happy path — absolute url on the canonical origin is used as-is', () => {
    const result = resolvePreviewHref({
      url: 'https://pod.example.com/preview/agent-1/abc123/',
      origin: 'https://pod.example.com',
      hostname: 'pod.example.com',
      port: 443,
    })
    expect(result).toEqual({ href: 'https://pod.example.com/preview/agent-1/abc123/' })
  })

  it('happy path — localhost canonical origin (no public_url)', () => {
    const result = resolvePreviewHref({
      url: 'http://localhost:5000/preview/agent-1/abc123/',
      origin: 'http://localhost:5000',
      hostname: 'localhost',
      port: 5000,
    })
    expect(result).toEqual({ href: 'http://localhost:5000/preview/agent-1/abc123/' })
  })

  it('relative path only (no absolute url) — built from the SPA origin', () => {
    const result = resolvePreviewHref({
      path: '/preview/agent-1/abc123/',
      origin: 'http://localhost:5000',
      hostname: 'localhost',
      port: 5000,
    })
    expect(result).toEqual({ href: 'http://localhost:5000/preview/agent-1/abc123/' })
  })

  it('relative url (starts with "/") is treated like a relative path', () => {
    const result = resolvePreviewHref({
      url: '/preview/agent-1/abc123/',
      origin: 'http://localhost:5000',
      hostname: 'localhost',
      port: 5000,
    })
    expect(result).toEqual({ href: 'http://localhost:5000/preview/agent-1/abc123/' })
  })

  it('legacy bind-all host in an old transcript url is normalised to the current origin', () => {
    const result = resolvePreviewHref({
      url: 'http://0.0.0.0:5000/preview/agent-1/abc123/',
      origin: 'http://146.190.89.151:5000',
      hostname: '146.190.89.151',
      port: 5000,
    })
    expect(result).toEqual({ href: 'http://146.190.89.151:5000/preview/agent-1/abc123/' })
  })

  it('prefers the absolute url over path when both are present', () => {
    const result = resolvePreviewHref({
      path: '/preview/agent-1/abc123/',
      url: 'https://pod.example.com/preview/agent-1/abc123/',
      origin: 'https://pod.example.com',
      hostname: 'pod.example.com',
      port: 443,
    })
    expect(result).toEqual({ href: 'https://pod.example.com/preview/agent-1/abc123/' })
  })

  it('falls back to path when url has an invalid path portion', () => {
    const result = resolvePreviewHref({
      path: '/preview/agent-1/abc123/',
      url: 'https://pod.example.com/api/v1/agents',
      origin: 'https://pod.example.com',
      hostname: 'pod.example.com',
      port: 443,
    })
    expect(result).toEqual({ href: 'https://pod.example.com/preview/agent-1/abc123/' })
  })

  it('rejects a javascript: url even if its "pathname" looks like a preview path', () => {
    const result = resolvePreviewHref({
      url: 'javascript:/preview/agent-1/abc123/',
      origin: 'http://localhost:5000',
      hostname: 'localhost',
      port: 5000,
    })
    expect(result).toEqual({ error: 'invalid-path' })
  })

  it('invalid path in both fields → error', () => {
    const result = resolvePreviewHref({
      path: 'javascript:alert(1)',
      url: 'data:text/html,hi',
      origin: 'http://localhost:5000',
      hostname: 'localhost',
      port: 5000,
    })
    expect(result).toEqual({ error: 'invalid-path' })
  })

  it('no path, no url → error', () => {
    const result = resolvePreviewHref({
      origin: 'http://localhost:5000',
      hostname: 'localhost',
      port: 5000,
    })
    expect(result).toEqual({ error: 'invalid-path' })
  })

  it('differentiation: two different valid results produce two different hrefs', () => {
    const result1 = resolvePreviewHref({
      path: '/preview/agent-a/token-alpha/',
      origin: 'http://10.0.0.1:5000',
      hostname: '10.0.0.1',
      port: 5000,
    })
    const result2 = resolvePreviewHref({
      path: '/preview/agent-b/token-beta/',
      origin: 'http://10.0.0.1:5000',
      hostname: '10.0.0.1',
      port: 5000,
    })
    expect('href' in result1 && result1.href).toBe('http://10.0.0.1:5000/preview/agent-a/token-alpha/')
    expect('href' in result2 && result2.href).toBe('http://10.0.0.1:5000/preview/agent-b/token-beta/')
    expect(result1).not.toEqual(result2)
  })

  it('/serve/ and /dev/ legacy path prefixes are still accepted', () => {
    const serveResult = resolvePreviewHref({
      path: '/serve/agent-1/abc123/',
      origin: 'http://localhost:5000',
      hostname: 'localhost',
      port: 5000,
    })
    expect(serveResult).toEqual({ href: 'http://localhost:5000/serve/agent-1/abc123/' })

    const devResult = resolvePreviewHref({
      path: '/dev/agent-1/xyz789/',
      origin: 'http://localhost:5000',
      hostname: 'localhost',
      port: 5000,
    })
    expect(devResult).toEqual({ href: 'http://localhost:5000/dev/agent-1/xyz789/' })
  })

  it('href never carries a preview_port/preview_origin-style query override — uses only origin/path', () => {
    // Regression guard for FR-015/US-8: the resolved href is built purely from
    // `origin` + the validated path — there is no port/origin override field
    // anywhere in this function's inputs or output.
    const result = resolvePreviewHref({
      path: '/preview/agent-1/abc123/',
      origin: 'https://pod.example.com',
      hostname: 'pod.example.com',
      port: 443,
    })
    expect(result).toEqual({ href: 'https://pod.example.com/preview/agent-1/abc123/' })
  })
})
