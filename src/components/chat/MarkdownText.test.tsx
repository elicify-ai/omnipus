/**
 * MarkdownText.test.tsx — isSafeHref unit tests (the core URL scheme predicate).
 *
 * Covers the scheme allow-list introduced in V2.C (FE H1 + FE H8):
 *   - javascript:, data:, vbscript: → rejected
 *   - http:, https:, mailto:, tel: → allowed
 *
 * The REAL renderer behavior (createLinkRenderer / MarkdownImage) is tested in
 * markdown-shared.test.tsx, which imports the actual exports from markdown-shared
 * and renders them via React Testing Library. The copy-based TestLinkRenderer /
 * TestImageRenderer that previously lived here were deleted: they tested stale
 * copies of the renderer logic and had already drifted (missing rewriteLegacyURL
 * rewrite, missing line-through class). Do not reintroduce copies — extend
 * markdown-shared.test.tsx instead.
 */

import { describe, it, expect, vi } from 'vitest'
import { isSafeHref } from '@/lib/url-safe'

// ── Mocks required to import markdown-text without crashing ──────────────────

vi.mock('@assistant-ui/react-markdown', () => ({
  MarkdownTextPrimitive: () => null,
  unstable_memoizeMarkdownComponents: (m: Record<string, unknown>) => m,
}))
vi.mock('remark-gfm', () => ({ default: () => {} }))
vi.mock('remark-math', () => ({ default: () => {} }))
vi.mock('rehype-katex', () => ({ default: () => {} }))
vi.mock('katex/dist/katex.min.css', () => ({}))
vi.mock('@tanstack/react-query', () => ({
  useQuery: () => ({ data: { preview_port: 5001 } }),
}))
vi.mock('@/lib/api', () => ({
  fetchAboutInfo: vi.fn().mockResolvedValue({}),
}))
vi.mock('./shiki-highlighter', () => ({
  SyntaxHighlighter: () => null,
  CopyCodeHeader: () => null,
}))
vi.mock('./image-lightbox', () => ({
  ImageLightbox: () => null,
}))
vi.mock('@/lib/rehype-phosphor-emoji', () => ({
  rehypePhosphorEmoji: () => {},
}))

// ── isSafeHref unit tests (the core predicate) ───────────────────────────────

describe('isSafeHref — scheme allow-list', () => {
  // Allowed schemes
  it('allows http:', () => {
    expect(isSafeHref('http://example.com/path')).toBe(true)
  })

  it('allows https:', () => {
    expect(isSafeHref('https://github.com/x')).toBe(true)
  })

  it('allows mailto:', () => {
    expect(isSafeHref('mailto:user@example.com')).toBe(true)
  })

  it('allows tel:', () => {
    expect(isSafeHref('tel:+1234567890')).toBe(true)
  })

  // Disallowed schemes
  it('rejects javascript: (XSS primitive — FE H1)', () => {
    expect(isSafeHref('javascript:alert(1)')).toBe(false)
  })

  it('rejects javascript: with encoded content', () => {
    expect(isSafeHref('javascript:fetch("/api/v1/admin/users")')).toBe(false)
  })

  it('rejects data: (potential XSS vector)', () => {
    expect(isSafeHref('data:text/html,<script>alert(1)</script>')).toBe(false)
  })

  it('rejects data: image (could carry malicious payload)', () => {
    expect(isSafeHref('data:image/png;base64,AAAA')).toBe(false)
  })

  it('rejects vbscript:', () => {
    expect(isSafeHref('vbscript:MsgBox(1)')).toBe(false)
  })

  it('rejects file: scheme', () => {
    expect(isSafeHref('file:///etc/passwd')).toBe(false)
  })

  it('rejects ftp: scheme', () => {
    expect(isSafeHref('ftp://files.example.com/secret')).toBe(false)
  })

  it('rejects relative paths (not parseable by URL constructor)', () => {
    expect(isSafeHref('/relative/path')).toBe(false)
  })

  it('rejects empty string', () => {
    expect(isSafeHref('')).toBe(false)
  })
})
