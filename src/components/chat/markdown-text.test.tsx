/**
 * markdown-text.tsx — tests for the `a` renderer + rewriteLegacyURL (ADR-044)
 *
 * Since ADR-044 (preview-on-main-listener), `/preview/` shares the SPA's own
 * origin — there is no operator-configured preview host/port left to fetch
 * from /api/v1/about, so MarkdownText's `a` renderer is now a static,
 * module-level `createLinkRenderer(null)` (the shared "no rewrite" case in
 * markdown-shared.tsx): links render exactly as the backend returned them.
 * MarkdownText no longer calls useQuery/fetchAboutInfo at all.
 *
 * rewriteLegacyURL itself is retained in preview-url.ts (old-transcript
 * replay safety) and is still exercised here directly as a pure-function
 * regression suite — it is no longer wired into MarkdownText's live render
 * path, but stays correct for any caller that does pass a non-null
 * effectivePreview (see preview-url.test.ts for the primary coverage).
 *
 * Since MarkdownText reads from MessagePrimitive context (AssistantUI-specific),
 * we test rewriteLegacyURL directly here rather than rendering the component.
 *
 * We also verify the module exports MarkdownText (existence + type check).
 *
 * Traces to: docs/internal/specs/preview-on-main-listener-spec.md — FR-016/FR-017.
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

// ── rewriteLegacyURL pure-function regression (no longer wired into the live
//    render path — see the file header comment) ──────────────────────────────

describe('markdown-text — rewriteLegacyURL regression coverage (preview-on-main-listener FR-016/FR-017)', () => {
  // Traces to: docs/internal/specs/preview-on-main-listener-spec.md
  //
  // MarkdownText's `a` renderer is now the static `createLinkRenderer(null)`
  // (no rewrite — see markdown-text.tsx). rewriteLegacyURL stays correct as a
  // pure function for old-transcript replay (any caller that DOES pass a
  // non-null effectivePreview) and is regression-tested directly here.

  it('rewrites 0.0.0.0 serve path to actual hostname with the given port', () => {
    // Traces to: preview-on-main-listener-spec.md — FR-016/017 spec row 1
    const result = rewriteLegacyURL(
      'http://0.0.0.0:5000/serve/m/t/',
      '10.0.0.5',
      5001,
    )
    expect(result).toBe('http://10.0.0.5:5001/serve/m/t/')
  })

  it('rewrites 0.0.0.0 dev path to localhost with the given port', () => {
    // Traces to: preview-on-main-listener-spec.md — FR-016/017 spec row 2
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
