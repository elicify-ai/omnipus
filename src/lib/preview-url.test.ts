/**
 * Tests for preview-url.ts — validatePreviewPath, rewriteLegacyURL, buildIframeURL
 *
 * Traces to: docs/internal/specs/chat-served-iframe-preview-spec.md
 * FR-010, FR-010a, FR-010b, FR-016, FR-017, FR-017a, FR-017b
 *
 * Test order mirrors the spec's 15-row rewrite dataset and 8-row path validation
 * dataset from the @example blocks in preview-url.ts.
 */

import { describe, it, expect } from 'vitest'
import { validatePreviewPath, rewriteLegacyURL, buildIframeURL } from './preview-url'

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
    // Traces to: chat-served-iframe-preview-spec.md — Scenario: validatePreviewPath rejects unsafe paths
    expect(validatePreviewPath(path)).toBe(expected)
  })

  it('differentiation: three valid paths produce true, one invalid produces false', () => {
    // Traces to: chat-served-iframe-preview-spec.md — anti-shortcut differentiation
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
  // Each row: { name, href, hostname, previewPort, expected }

  it.each([
    // Row 1 — wildcard 0.0.0.0, serve path → port swapped to preview port
    {
      name: 'row 1: 0.0.0.0 + serve path → preview port',
      href: 'http://0.0.0.0:5000/serve/m/t/',
      hostname: '146.190.89.151',
      previewPort: 5001,
      expected: 'http://146.190.89.151:5001/serve/m/t/',
    },
    // Row 2 — 0.0.0.0 + /dev/ path, localhost destination
    {
      name: 'row 2: 0.0.0.0 + dev path → localhost',
      href: 'http://0.0.0.0:5000/dev/m/t/',
      hostname: 'localhost',
      previewPort: 5001,
      expected: 'http://localhost:5001/dev/m/t/',
    },
    // Row 3 — 0.0.0.0, non-serve path → main port preserved
    {
      name: 'row 3: 0.0.0.0 + non-serve path → main port retained',
      href: 'http://0.0.0.0:5000/about',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: 'http://1.2.3.4:5000/about',
    },
    // Row 4 — IPv6 wildcard [::]
    {
      name: 'row 4: [::] + serve path → rewritten',
      href: 'http://[::]:5000/serve/m/t/',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: 'http://1.2.3.4:5001/serve/m/t/',
    },
    // Row 5 — IPv6 explicit zero [::0]
    {
      name: 'row 5: [::0] + serve path → rewritten',
      href: 'http://[::0]:5000/serve/m/t/',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: 'http://1.2.3.4:5001/serve/m/t/',
    },
    // Row 6 — bare zero "0" (WHATWG normalises to 0.0.0.0)
    {
      name: 'row 6: bare 0 + serve path → rewritten',
      href: 'http://0:5000/serve/m/t/',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: 'http://1.2.3.4:5001/serve/m/t/',
    },
    // Row 7 — loopback 127.0.0.1
    {
      name: 'row 7: 127.0.0.1 + serve path → rewritten',
      href: 'http://127.0.0.1:5000/serve/m/t/',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: 'http://1.2.3.4:5001/serve/m/t/',
    },
    // Row 8 — foreign host unchanged
    {
      name: 'row 8: foreign host → unchanged',
      href: 'https://example.com/page',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: 'https://example.com/page',
    },
    // Row 9 — mailto: passes through
    {
      name: 'row 9: mailto: scheme → unchanged',
      href: 'mailto:foo@x.com',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: 'mailto:foo@x.com',
    },
    // Row 10 — javascript: passes through (XSS safety)
    {
      name: 'row 10: javascript: scheme → unchanged',
      href: 'javascript:alert(1)',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: 'javascript:alert(1)',
    },
    // Row 11 — tel: passes through
    {
      name: 'row 11: tel: scheme → unchanged',
      href: 'tel:+155512345',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: 'tel:+155512345',
    },
    // Row 12 — relative path unchanged
    {
      name: 'row 12: relative path → unchanged',
      href: '/relative/path',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: '/relative/path',
    },
    // Row 13 — scheme-relative unchanged
    {
      name: 'row 13: scheme-relative → unchanged',
      href: '//host.com/x',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: '//host.com/x',
    },
    // Row 14 — empty string boundary
    {
      name: 'row 14: empty string → unchanged',
      href: '',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: '',
    },
    // Row 15 — unparseable href
    {
      name: 'row 15: unparseable href → unchanged without throw',
      href: 'not-a-url',
      hostname: '1.2.3.4',
      previewPort: 5001,
      expected: 'not-a-url',
    },
  ])('$name', ({ href, hostname, previewPort, expected }) => {
    const result = rewriteLegacyURL(href, hostname, previewPort)
    expect(result).toBe(expected)
  })

  it('differentiation: two different legacy hosts produce two different rewritten URLs', () => {
    // Traces to: chat-served-iframe-preview-spec.md — anti-shortcut differentiation
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

  it('0.0.0.0 + /preview/ path → port swapped to preview port (canonical new shape)', () => {
    // Traces to: FR-017 — /preview/ is now a first-class preview path prefix.
    const result = rewriteLegacyURL('http://0.0.0.0:5000/preview/agent-1/tok/', '146.190.89.151', 5001)
    expect(result).toBe('http://146.190.89.151:5001/preview/agent-1/tok/')
  })

  it('127.0.0.1 + /preview/ path → host and port rewritten', () => {
    const result = rewriteLegacyURL('http://127.0.0.1:5000/preview/m/t/', '1.2.3.4', 5001)
    expect(result).toBe('http://1.2.3.4:5001/preview/m/t/')
  })
})

// ── buildIframeURL ────────────────────────────────────────────────────────────

describe('buildIframeURL — success and error paths (FR-010 / FR-010a / FR-010b)', () => {
  // Traces to: preview-url.ts @example block — buildIframeURL dataset

  it('happy path — no previewOrigin, HTTP SPA', () => {
    // Traces to: chat-served-iframe-preview-spec.md — Scenario: buildIframeURL constructs URL from window coordinates
    const result = buildIframeURL({
      path: '/serve/agent-1/abc123/',
      previewPort: 5001,
      hostname: '146.190.89.151',
      protocol: 'http:',
    })
    expect(result).toEqual({ url: 'http://146.190.89.151:5001/serve/agent-1/abc123/' })
  })

  it('happy path — previewOrigin set (HTTPS)', () => {
    // Traces to: chat-served-iframe-preview-spec.md — Scenario: buildIframeURL uses previewOrigin when set
    const result = buildIframeURL({
      path: '/serve/agent-1/abc123/',
      previewOrigin: 'https://preview.acme.com',
      previewPort: 5001,
      hostname: 'omnipus.acme.com',
      protocol: 'https:',
    })
    expect(result).toEqual({ url: 'https://preview.acme.com/serve/agent-1/abc123/' })
  })

  it('invalid path — javascript: injection', () => {
    // Traces to: chat-served-iframe-preview-spec.md — Scenario: buildIframeURL rejects invalid path
    const result = buildIframeURL({
      path: 'javascript:alert(1)',
      previewPort: 5001,
      hostname: '1.2.3.4',
      protocol: 'http:',
    })
    expect(result).toEqual({ error: 'invalid-path' })
  })

  it('scheme mismatch — HTTP SPA + HTTPS preview origin', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-010a scheme mismatch
    const result = buildIframeURL({
      path: '/serve/agent-1/abc123/',
      previewOrigin: 'https://preview.example.com',
      previewPort: 443,
      hostname: 'main.example.com',
      protocol: 'http:',
    })
    expect(result).toEqual({ error: 'scheme-mismatch' })
  })

  it('scheme mismatch — HTTPS SPA + HTTP preview origin', () => {
    // Reverse direction mismatch
    const result = buildIframeURL({
      path: '/serve/agent-1/abc123/',
      previewOrigin: 'http://preview.example.com',
      previewPort: 5001,
      hostname: 'main.example.com',
      protocol: 'https:',
    })
    expect(result).toEqual({ error: 'scheme-mismatch' })
  })

  it('invalid path — path traversal attempt', () => {
    // Traces to: preview-url.ts @example block — row 5
    const result = buildIframeURL({
      path: '/serve/../../etc/passwd',
      previewPort: 5001,
      hostname: '1.2.3.4',
      protocol: 'http:',
    })
    expect(result).toEqual({ error: 'invalid-path' })
  })

  it('invalid path — empty string', () => {
    // Traces to: preview-url.ts @example block — row 6
    const result = buildIframeURL({
      path: '',
      previewPort: 5001,
      hostname: '1.2.3.4',
      protocol: 'http:',
    })
    expect(result).toEqual({ error: 'invalid-path' })
  })

  it('invalid path — API path rejected', () => {
    // Traces to: preview-url.ts @example block — row 7
    const result = buildIframeURL({
      path: '/api/v1/agents',
      previewPort: 5001,
      hostname: '1.2.3.4',
      protocol: 'http:',
    })
    expect(result).toEqual({ error: 'invalid-path' })
  })

  it('differentiation: two different valid paths produce two different URLs', () => {
    // Traces to: chat-served-iframe-preview-spec.md — anti-shortcut differentiation
    // Proves the url field is computed from the path, not hardcoded.
    const result1 = buildIframeURL({
      path: '/serve/agent-a/token-alpha/',
      previewPort: 5001,
      hostname: '10.0.0.1',
      protocol: 'http:',
    })
    const result2 = buildIframeURL({
      path: '/serve/agent-b/token-beta/',
      previewPort: 5001,
      hostname: '10.0.0.1',
      protocol: 'http:',
    })
    expect('url' in result1 && result1.url).toBe('http://10.0.0.1:5001/serve/agent-a/token-alpha/')
    expect('url' in result2 && result2.url).toBe('http://10.0.0.1:5001/serve/agent-b/token-beta/')
    expect(result1).not.toEqual(result2)
  })

  it('previewOrigin trailing slash is stripped before path concatenation', () => {
    // Traces to: chat-served-iframe-preview-spec.md — Scenario: trailing slash on origin
    const result = buildIframeURL({
      path: '/serve/agent-1/abc123/',
      previewOrigin: 'https://preview.acme.com/',
      previewPort: 5001,
      hostname: 'omnipus.acme.com',
      protocol: 'https:',
    })
    // Should not produce double-slash: https://preview.acme.com//serve/...
    expect(result).toEqual({ url: 'https://preview.acme.com/serve/agent-1/abc123/' })
  })

  it('/dev/ path is also valid for buildIframeURL', () => {
    const result = buildIframeURL({
      path: '/dev/agent-1/xyz789/',
      previewPort: 5001,
      hostname: '10.0.0.2',
      protocol: 'http:',
    })
    expect(result).toEqual({ url: 'http://10.0.0.2:5001/dev/agent-1/xyz789/' })
  })

  it('happy path — /preview/ canonical path, no previewOrigin', () => {
    // Traces to: FR-010 — canonical new /preview/ shape accepted by buildIframeURL.
    const result = buildIframeURL({
      path: '/preview/agent-1/abc123/',
      previewPort: 5001,
      hostname: '146.190.89.151',
      protocol: 'http:',
    })
    expect(result).toEqual({ url: 'http://146.190.89.151:5001/preview/agent-1/abc123/' })
  })

  it('happy path — /preview/ canonical path with previewOrigin', () => {
    const result = buildIframeURL({
      path: '/preview/agent-1/abc123/',
      previewOrigin: 'https://preview.acme.com',
      previewPort: 5001,
      hostname: 'omnipus.acme.com',
      protocol: 'https:',
    })
    expect(result).toEqual({ url: 'https://preview.acme.com/preview/agent-1/abc123/' })
  })

  it('unparseable previewOrigin returns misconfigured-origin error (F-31)', () => {
    // Traces to: chat-served-iframe-preview-spec.md — F-31 misconfigured-origin discriminant
    // Fix-D split the former 'invalid-path' bucket into:
    //   • 'invalid-path'          — corrupt tool result path (validatePreviewPath fails)
    //   • 'misconfigured-origin'  — previewOrigin is set but unparseable (operator problem)
    // Unparseable previewOrigin is an operator deployment problem, not a path issue.
    const result = buildIframeURL({
      path: '/serve/agent-1/abc123/',
      previewOrigin: 'not-a-valid-url',
      previewPort: 5001,
      hostname: '10.0.0.1',
      protocol: 'http:',
    })
    expect(result).toEqual({ error: 'misconfigured-origin' })
  })
})
