/**
 * markdown-text.tsx — tests for rewriteLegacyURL integration in the `a` renderer
 *
 * MarkdownText uses rewriteLegacyURL on every link href before rendering.
 * These tests verify that the `a` renderer correctly rewrites legacy
 * bind-all hosts in link hrefs (FR-016 / FR-017).
 *
 * Since MarkdownText reads from MessagePrimitive context (AssistantUI-specific),
 * we test the rewriteLegacyURL function directly here for the markdown-text
 * integration scenario. The component's `a` renderer logic is a one-liner
 * delegating entirely to rewriteLegacyURL, which is already parameterized-tested
 * in preview-url.test.ts.
 *
 * We also verify the module exports MarkdownText (existence + type check).
 *
 * Traces to: docs/internal/specs/chat-served-iframe-preview-spec.md
 * FR-016 / FR-017: Legacy URL rewrite in markdown link renderer
 */

import { describe, it, expect, vi } from 'vitest'
import { rewriteLegacyURL } from '@/lib/preview-url'
// Static import (vi.mock calls below are hoisted above all imports by vitest, so
// this resolves against the mocks). Was a per-test `await import('./markdown-text')`
// which timed out under full-suite parallel load — a static import resolves once.
import { MarkdownText } from './markdown-text'

// ── Top-level mocks for markdown-text module import ───────────────────────────
// These vi.mock calls are hoisted by vitest regardless of position.
// They must be at the top level (not inside describe/it) to avoid the
// "not at the top level" warning and to work correctly in future vitest versions.

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

// ── Module export guard ───────────────────────────────────────────────────────

describe('markdown-text — module exports MarkdownText', () => {
  it('MarkdownText is exported as a memoized component', () => {
    // Traces to: chat-served-iframe-preview-spec.md — markdown-text module structure
    expect(MarkdownText).toBeDefined()
    // React.memo wraps a function component — the result has a $$typeof and type property
    expect(typeof MarkdownText).toBe('object')
  })
})

// ── rewriteLegacyURL integration for markdown `a` renderer ───────────────────

describe('markdown-text — a renderer uses rewriteLegacyURL (FR-016 / FR-017)', () => {
  // Traces to: chat-served-iframe-preview-spec.md — Scenario: Legacy URL rewrite in markdown links

  // The `a` renderer in MarkdownTextImpl calls:
  //   rewriteLegacyURL(href ?? '', window.location.hostname, previewPort)
  // We verify the pure function's behavior matches what the renderer will produce.

  it('rewrites 0.0.0.0 serve path to actual hostname with preview port', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-016/017 spec row 1
    const result = rewriteLegacyURL(
      'http://0.0.0.0:5000/serve/m/t/',
      '10.0.0.5',
      5001,
    )
    expect(result).toBe('http://10.0.0.5:5001/serve/m/t/')
  })

  it('rewrites 0.0.0.0 dev path to localhost with preview port', () => {
    // Traces to: chat-served-iframe-preview-spec.md — FR-016/017 spec row 2
    const result = rewriteLegacyURL(
      'http://0.0.0.0:5000/dev/m/t/',
      'localhost',
      5001,
    )
    expect(result).toBe('http://localhost:5001/dev/m/t/')
  })

  it('rewrites 127.0.0.1 to actual hostname for serve path', () => {
    // Traces to: chat-served-iframe-preview-spec.md — loopback rewrite
    const result = rewriteLegacyURL(
      'http://127.0.0.1:5000/serve/m/t/',
      '93.184.216.34',
      5001,
    )
    expect(result).toBe('http://93.184.216.34:5001/serve/m/t/')
  })

  it('passes through external link unchanged', () => {
    // Traces to: chat-served-iframe-preview-spec.md — foreign host unchanged
    const result = rewriteLegacyURL(
      'https://github.com/repo',
      '10.0.0.5',
      5001,
    )
    expect(result).toBe('https://github.com/repo')
  })

  it('passes through relative path unchanged', () => {
    const result = rewriteLegacyURL('/relative', '10.0.0.5', 5001)
    expect(result).toBe('/relative')
  })

  it('differentiation: two different legacy hosts produce different rewritten URLs', () => {
    // Anti-shortcut: proves rewriteLegacyURL is not hardcoded
    const r1 = rewriteLegacyURL('http://0.0.0.0:5000/serve/a/b/', 'host1.example.com', 5001)
    const r2 = rewriteLegacyURL('http://0.0.0.0:5000/serve/a/b/', 'host2.example.com', 5001)
    expect(r1).toBe('http://host1.example.com:5001/serve/a/b/')
    expect(r2).toBe('http://host2.example.com:5001/serve/a/b/')
    expect(r1).not.toBe(r2)
  })

  it('non-serve 0.0.0.0 path retains main port (not preview port)', () => {
    // Rule 6: only /serve/ and /dev/ paths get the preview port swap
    const result = rewriteLegacyURL('http://0.0.0.0:5000/about', '10.0.0.5', 5001)
    expect(result).toBe('http://10.0.0.5:5000/about')
  })
})
